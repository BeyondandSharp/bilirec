// SPDX-License-Identifier: GPL-3.0-only

// Package flvreframe reconstructs FLV tag framing for recordings damaged by
// the fork's pre-0.6 "split" bug.
//
// This package is fork-owned code. It deliberately does NOT import pkg/flv or
// any part of the ported BililiveRecorder repair pipeline
// (rust/crates/flv-repair and its Go counterpart), because its algorithm and
// guarantees are different:
//
//   - It trusts each tag's own 24-bit data size instead of the previous-tag
//     size fields, which the buggy splitter corrupted.
//   - On a gap it re-synchronises on the next structurally valid tag pair and
//     drops only the unparseable bytes; it does not try to "fix" media tags.
//   - It rebuilds the FLV header, every previous-tag size field, and rebases
//     timestamps to the first media timestamp (keeping them monotonic).
//
// The BililiveRecorder-derived flvrepair tool remains the right place for
// BLR-specific rules (anomaly classes R1-R8, metadata duration, Annex-B, ...).
// Typical recovery is two steps:
//
//	flvreframe --input broken.flv --output reframed.flv
//	flvrepair file --input reframed.flv --output final.flv
//
// See docs/FLV-REFRAME.md for the full rationale, limitations and the exact
// failure mode this tool was written for.
package flvreframe

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

const (
	flvHeaderLen = 9
	tagHeaderLen = 11
	tagFooterLen = 4

	maxTagDataSize    = 32 << 20 // sanity bound for a single FLV tag
	defaultResyncSize = 16 << 20 // how far to scan for the next tag after a gap
	readWindow        = 1 << 20  // resync scan window
)

var (
	// ErrNoTags is returned when the input contains no parseable FLV tag.
	ErrNoTags = errors.New("flvreframe: no valid FLV tag found")
)

// Options controls a repair run. The zero value is valid: timestamps are
// rebased and the default resync search window is used.
type Options struct {
	// TrimBrokenVideoGOPs drops the video frames after each detected gap until
	// the next video keyframe. Audio tags are kept, so A/V timing stays intact
	// and the file plays without decode artifacts at the cost of a short video
	// freeze/skip at every gap.
	TrimBrokenVideoGOPs bool

	// KeepTimestamps keeps the original timestamps instead of rebasing them to
	// the first media timestamp. Monotonic clamping is applied either way.
	KeepTimestamps bool

	// ResyncSize is the maximum number of bytes scanned for the next tag after
	// a gap. 0 uses defaultResyncSize.
	ResyncSize int64
}

// Gap describes one damaged byte range that was skipped while rebuilding.
type Gap struct {
	Offset              int64  `json:"offset"`
	Bytes               int64  `json:"bytes"`
	PreviousTimestampMS uint32 `json:"previous_timestamp_ms"`
	NextTimestampMS     uint32 `json:"next_timestamp_ms"`
}

// Result summarises a repair run.
type Result struct {
	InputBytes         int64  `json:"input_bytes"`
	OutputBytes        int64  `json:"output_bytes"`
	TagsIn             int    `json:"tags_in"`
	TagsOut            int    `json:"tags_out"`
	DroppedVideoTags   int    `json:"dropped_video_tags"`
	DroppedBytes       int64  `json:"dropped_bytes"`
	TrailingBytes      int64  `json:"trailing_bytes"`
	PrevSizeMismatches int    `json:"prev_size_mismatches"`
	Gaps               []Gap  `json:"gaps"`
	FirstTimestampMS   uint32 `json:"first_timestamp_ms"`
	LastTimestampMS    uint32 `json:"last_timestamp_ms"`
}

type tag struct {
	off     int64
	typ     byte
	size    uint32
	ts      uint32
	isVideo bool
	isAudio bool
	isSeq   bool
	isKey   bool
	dropped bool
}

type gapInternal struct {
	Gap
	nextIndex int // index of the first tag after the gap
}

type reframer struct {
	in       *os.File
	size     int64
	resync   int64
	tags     []tag
	gaps     []gapInternal
	prevMis  int
	trailing int64
}

// Repair rebuilds inPath into outPath. The input file is never modified.
func Repair(inPath, outPath string, opts Options) (Result, error) {
	in, err := os.Open(inPath)
	if err != nil {
		return Result{}, fmt.Errorf("flvreframe: open input: %w", err)
	}
	defer in.Close()

	st, err := in.Stat()
	if err != nil {
		return Result{}, fmt.Errorf("flvreframe: stat input: %w", err)
	}
	if st.Size() < flvHeaderLen+tagFooterLen {
		return Result{}, fmt.Errorf("flvreframe: input too small (%d bytes)", st.Size())
	}

	r := &reframer{in: in, size: st.Size(), resync: opts.ResyncSize}
	if r.resync <= 0 {
		r.resync = defaultResyncSize
	}
	if err := r.scan(); err != nil {
		return Result{}, err
	}
	if len(r.tags) == 0 {
		return Result{}, ErrNoTags
	}

	result, err := r.write(outPath, opts)
	if err != nil {
		return Result{}, err
	}
	result.InputBytes = r.size
	return result, nil
}

func (r *reframer) scan() error {
	var hdr [flvHeaderLen]byte
	if _, err := r.in.ReadAt(hdr[:], 0); err != nil {
		return fmt.Errorf("flvreframe: read FLV header: %w", err)
	}
	if string(hdr[0:3]) != "FLV" || hdr[3] != 1 {
		return fmt.Errorf("flvreframe: not an FLV version 1 file")
	}
	dataOffset := int64(binary.BigEndian.Uint32(hdr[5:9]))
	if dataOffset < flvHeaderLen || dataOffset+tagFooterLen > r.size {
		return fmt.Errorf("flvreframe: invalid FLV data offset %d", dataOffset)
	}

	pos := dataOffset + tagFooterLen // skip the first previous-tag-size field
	for pos+tagHeaderLen+tagFooterLen <= r.size {
		t, ok := r.headerAt(pos)
		if !ok {
			next, ok := r.findNextTag(pos + 1)
			if !ok {
				r.trailing = r.size - pos
				return nil
			}
			var prevTS uint32
			if len(r.tags) > 0 {
				prevTS = r.tags[len(r.tags)-1].ts
			}
			nh, _ := r.headerAt(next)
			r.gaps = append(r.gaps, gapInternal{
				Gap: Gap{
					Offset:              pos,
					Bytes:               next - pos,
					PreviousTimestampMS: prevTS,
					NextTimestampMS:     nh.ts,
				},
				nextIndex: len(r.tags),
			})
			pos = next
			continue
		}
		r.readTagFlags(&t)
		r.readPrevSizeMismatch(t)
		r.tags = append(r.tags, t)
		pos = t.off + tagHeaderLen + int64(t.size) + tagFooterLen
	}
	if pos < r.size {
		r.trailing = r.size - pos
	}
	return nil
}

// headerAt reports whether a structurally valid FLV tag header starts at pos.
// It intentionally ignores the previous-tag-size field that follows the tag:
// the buggy splitter wrote wrong values there, and trusting it would reject
// otherwise intact tags.
func (r *reframer) headerAt(pos int64) (tag, bool) {
	if pos < 0 || pos+tagHeaderLen+tagFooterLen > r.size {
		return tag{}, false
	}
	var h [tagHeaderLen]byte
	if _, err := r.in.ReadAt(h[:], pos); err != nil && err != io.EOF {
		return tag{}, false
	}
	typ := h[0]
	if typ != 8 && typ != 9 && typ != 18 {
		return tag{}, false
	}
	size := uint32(h[1])<<16 | uint32(h[2])<<8 | uint32(h[3])
	if size == 0 || size > maxTagDataSize {
		return tag{}, false
	}
	if h[8] != 0 || h[9] != 0 || h[10] != 0 { // FLV StreamID must be zero
		return tag{}, false
	}
	end := pos + tagHeaderLen + int64(size) + tagFooterLen
	if end > r.size {
		return tag{}, false
	}
	ts := uint32(h[4])<<16 | uint32(h[5])<<8 | uint32(h[6]) | uint32(h[7])<<24
	return tag{off: pos, typ: typ, size: size, ts: ts}, true
}

// findNextTag scans forward for the next tag that is structurally valid and
// followed by another valid tag. Requiring a pair avoids resynchronising on
// random media bytes that happen to look like a header.
func (r *reframer) findNextTag(from int64) (int64, bool) {
	limit := r.resync
	if limit <= 0 {
		limit = defaultResyncSize
	}
	end := from + limit
	if end > r.size {
		end = r.size
	}
	buf := make([]byte, readWindow)
	base := from
	for base < end {
		n := int64(len(buf))
		if base+n > end {
			n = end - base
		}
		if n <= 0 {
			break
		}
		got, err := r.in.ReadAt(buf[:n], base)
		if got <= 0 {
			break
		}
		if err != nil && err != io.EOF {
			break
		}
		for i := 0; i < got; i++ {
			if !isTagType(buf[i]) {
				continue
			}
			p := base + int64(i)
			t, ok := r.headerAt(p)
			if !ok {
				continue
			}
			next := p + tagHeaderLen + int64(t.size) + tagFooterLen
			if next == r.size {
				return p, true // last tag in the file
			}
			if next+tagHeaderLen > r.size {
				continue
			}
			if _, ok := r.headerAt(next); ok {
				return p, true
			}
		}
		if int64(got) < n {
			break
		}
		base += int64(got)
	}
	return 0, false
}

func isTagType(b byte) bool { return b == 8 || b == 9 || b == 18 }

func (r *reframer) readTagFlags(t *tag) {
	if t.size < 2 {
		return
	}
	var pl [2]byte
	if _, err := r.in.ReadAt(pl[:], t.off+tagHeaderLen); err != nil {
		return
	}
	switch t.typ {
	case 9:
		t.isVideo = true
		t.isSeq = pl[1] == 0
		t.isKey = (pl[0]>>4) == 1 && pl[1] == 1
	case 8:
		t.isAudio = true
		t.isSeq = pl[1] == 0
	}
}

func (r *reframer) readPrevSizeMismatch(t tag) {
	var buf [tagFooterLen]byte
	at := t.off + tagHeaderLen + int64(t.size)
	if _, err := r.in.ReadAt(buf[:], at); err != nil {
		return
	}
	if binary.BigEndian.Uint32(buf[:]) != uint32(tagHeaderLen)+t.size {
		r.prevMis++
	}
}

func (r *reframer) write(outPath string, opts Options) (Result, error) {
	// Decide the timestamp base before trimming so the choice does not depend
	// on which broken GOPs are removed.
	var base uint32
	if !opts.KeepTimestamps {
		for _, t := range r.tags {
			if t.ts > 0 {
				base = t.ts
				break
			}
		}
	}
	if opts.TrimBrokenVideoGOPs {
		r.markBrokenVideoGOPs()
	}

	out, err := os.OpenFile(outPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return Result{}, fmt.Errorf("flvreframe: create output: %w", err)
	}
	defer out.Close()

	hasAudio, hasVideo := false, false
	for _, t := range r.tags {
		if t.dropped {
			continue
		}
		if t.isAudio {
			hasAudio = true
		}
		if t.isVideo {
			hasVideo = true
		}
	}
	var flags byte
	if hasAudio {
		flags |= 0x01
	}
	if hasVideo {
		flags |= 0x04
	}
	var hdr [flvHeaderLen + tagFooterLen]byte
	copy(hdr[0:3], "FLV")
	hdr[3] = 1
	hdr[4] = flags
	binary.BigEndian.PutUint32(hdr[5:9], flvHeaderLen)
	// hdr[9:13] is the first previous-tag-size field, zero.
	if _, err := out.Write(hdr[:]); err != nil {
		return Result{}, fmt.Errorf("flvreframe: write header: %w", err)
	}

	result := Result{TagsIn: len(r.tags)}
	for _, g := range r.gaps {
		result.Gaps = append(result.Gaps, g.Gap)
		result.DroppedBytes += g.Bytes
	}
	result.TrailingBytes = r.trailing
	result.DroppedBytes += r.trailing
	result.PrevSizeMismatches = r.prevMis

	buf := make([]byte, 1<<20)
	lastTS := uint32(0)
	firstWritten := true
	for _, t := range r.tags {
		if t.dropped {
			if t.isVideo {
				result.DroppedVideoTags++
			}
			continue
		}
		ts := t.ts
		if !opts.KeepTimestamps {
			ts -= base
			if ts > t.ts { // uint32 underflow: original timestamp was below base
				ts = 0
			}
		}
		if ts < lastTS {
			ts = lastTS
		}
		lastTS = ts
		if firstWritten {
			result.FirstTimestampMS = ts
			firstWritten = false
		}

		var tagHdr [tagHeaderLen]byte
		tagHdr[0] = t.typ
		tagHdr[1] = byte(t.size >> 16)
		tagHdr[2] = byte(t.size >> 8)
		tagHdr[3] = byte(t.size)
		tagHdr[4] = byte(ts >> 16)
		tagHdr[5] = byte(ts >> 8)
		tagHdr[6] = byte(ts)
		tagHdr[7] = byte(ts >> 24)
		// tagHdr[8:11] is the zero StreamID.
		if _, err := out.Write(tagHdr[:]); err != nil {
			return Result{}, fmt.Errorf("flvreframe: write tag header: %w", err)
		}
		src := io.NewSectionReader(r.in, t.off+tagHeaderLen, int64(t.size))
		if _, err := io.CopyBuffer(out, src, buf); err != nil {
			return Result{}, fmt.Errorf("flvreframe: copy tag data: %w", err)
		}
		var footer [tagFooterLen]byte
		binary.BigEndian.PutUint32(footer[:], uint32(tagHeaderLen)+t.size)
		if _, err := out.Write(footer[:]); err != nil {
			return Result{}, fmt.Errorf("flvreframe: write previous tag size: %w", err)
		}
		result.TagsOut++
	}
	if err := out.Sync(); err != nil {
		return Result{}, fmt.Errorf("flvreframe: sync output: %w", err)
	}
	result.LastTimestampMS = lastTS
	if st, err := os.Stat(outPath); err == nil {
		result.OutputBytes = st.Size()
	}
	return result, nil
}

// markBrokenVideoGOPs sets dropped on video tags that follow a gap until the
// next video keyframe (exclusive). Audio tags are never dropped: keeping them
// preserves the timeline and A/V sync.
func (r *reframer) markBrokenVideoGOPs() {
	for _, g := range r.gaps {
		start := -1
		for i := g.nextIndex; i < len(r.tags); i++ {
			if r.tags[i].isVideo {
				start = i
				break
			}
		}
		if start < 0 {
			continue
		}
		keyframe := -1
		for i := start; i < len(r.tags); i++ {
			if r.tags[i].isVideo && r.tags[i].isKey {
				keyframe = i
				break
			}
		}
		if keyframe < 0 {
			keyframe = len(r.tags)
		}
		for i := start; i < keyframe; i++ {
			if r.tags[i].isVideo {
				r.tags[i].dropped = true
			}
		}
	}
}
