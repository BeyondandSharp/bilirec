// SPDX-License-Identifier: GPL-3.0-only

package flvreframe

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func makePayload(prefix ...byte) []byte { return prefix }

func makeTag(typ byte, ts uint32, payload []byte) []byte {
	h := make([]byte, tagHeaderLen)
	h[0] = typ
	size := uint32(len(payload))
	h[1] = byte(size >> 16)
	h[2] = byte(size >> 8)
	h[3] = byte(size)
	h[4] = byte(ts >> 16)
	h[5] = byte(ts >> 8)
	h[6] = byte(ts)
	h[7] = byte(ts >> 24)
	out := append(h, payload...)
	var foot [tagFooterLen]byte
	binary.BigEndian.PutUint32(foot[:], tagHeaderLen+size)
	return append(out, foot[:]...)
}

func makeFLV(tags ...[]byte) []byte {
	body := []byte{'F', 'L', 'V', 1, 0x05, 0, 0, 0, flvHeaderLen, 0, 0, 0, 0}
	for _, t := range tags {
		body = append(body, t...)
	}
	return body
}

func videoSeq() []byte { return makeTag(9, 0, []byte{0x17, 0x00, 0x00, 0x00, 0x00, 0x01}) }
func audioSeq() []byte { return makeTag(8, 0, []byte{0xaf, 0x00, 0x11}) }
func videoKey(ts uint32) []byte {
	return makeTag(9, ts, []byte{0x17, 0x01, 0x00, 0x00, 0x00, 0xaa})
}
func videoP(ts uint32) []byte {
	return makeTag(9, ts, []byte{0x27, 0x01, 0x00, 0x00, 0x00, 0xbb})
}
func audioFrame(ts uint32) []byte {
	return makeTag(8, ts, []byte{0xaf, 0x01, 0xcc})
}

func writeTemp(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "in.flv")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

type parsedTag struct {
	typ     byte
	ts      uint32
	payload []byte
}

func parseOutput(t *testing.T, path string) []parsedTag {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < flvHeaderLen+tagFooterLen || string(data[:3]) != "FLV" {
		t.Fatalf("output is not an FLV file: %x", data[:min(16, len(data))])
	}
	pos := int64(binary.BigEndian.Uint32(data[5:9])) + tagFooterLen
	var tags []parsedTag
	for pos+tagHeaderLen+tagFooterLen <= int64(len(data)) {
		h := data[pos : pos+tagHeaderLen]
		size := int64(uint32(h[1])<<16 | uint32(h[2])<<8 | uint32(h[3]))
		end := pos + tagHeaderLen + size
		if end+tagFooterLen > int64(len(data)) {
			t.Fatalf("truncated tag at %d", pos)
		}
		prev := binary.BigEndian.Uint32(data[end : end+tagFooterLen])
		if prev != uint32(tagHeaderLen)+uint32(size) {
			t.Fatalf("bad previous tag size %d at %d (want %d)", prev, pos, tagHeaderLen+size)
		}
		tags = append(tags, parsedTag{
			typ:     h[0],
			ts:      uint32(h[4])<<16 | uint32(h[5])<<8 | uint32(h[6]) | uint32(h[7])<<24,
			payload: append([]byte(nil), data[pos+tagHeaderLen:end]...),
		})
		pos = end + tagFooterLen
	}
	if pos != int64(len(data)) {
		t.Fatalf("trailing bytes in output: %d", int64(len(data))-pos)
	}
	return tags
}

func repair(t *testing.T, data []byte, opts Options) (Result, []parsedTag) {
	t.Helper()
	in := writeTemp(t, data)
	out := filepath.Join(t.TempDir(), "out.flv")
	result, err := Repair(in, out, opts)
	if err != nil {
		t.Fatalf("Repair: %v", err)
	}
	return result, parseOutput(t, out)
}

func TestCleanRoundTripRebasesTimestamps(t *testing.T) {
	input := makeFLV(videoSeq(), audioSeq(), videoKey(1000), audioFrame(1000), videoP(1016))
	result, tags := repair(t, input, Options{})
	if len(tags) != 5 {
		t.Fatalf("tags = %d, want 5", len(tags))
	}
	wantTS := []uint32{0, 0, 0, 0, 16}
	for i, want := range wantTS {
		if tags[i].ts != want {
			t.Fatalf("tag %d ts = %d, want %d", i, tags[i].ts, want)
		}
	}
	if result.TagsIn != 5 || result.TagsOut != 5 || len(result.Gaps) != 0 || result.DroppedBytes != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if !bytes.Equal(tags[2].payload, []byte{0x17, 0x01, 0, 0, 0, 0xaa}) {
		t.Fatalf("payload not preserved: %x", tags[2].payload)
	}
}

func TestKeepTimestamps(t *testing.T) {
	input := makeFLV(videoSeq(), audioSeq(), videoKey(1000), audioFrame(1000), videoP(1016))
	_, tags := repair(t, input, Options{KeepTimestamps: true})
	if tags[2].ts != 1000 || tags[4].ts != 1016 {
		t.Fatalf("timestamps changed: %d %d", tags[2].ts, tags[4].ts)
	}
}

func TestResyncsAfterGap(t *testing.T) {
	// Junk between two otherwise valid tags. Its first byte is not a tag type.
	junk := []byte("brokenbytes!!")
	input := makeFLV(videoSeq(), audioSeq(), videoKey(1000), audioFrame(1000), junk, audioFrame(1040), videoP(1050))
	result, tags := repair(t, input, Options{})
	if len(result.Gaps) != 1 {
		t.Fatalf("gaps = %+v", result.Gaps)
	}
	if result.Gaps[0].Bytes != int64(len(junk)) {
		t.Fatalf("gap bytes = %d, want %d", result.Gaps[0].Bytes, len(junk))
	}
	if len(tags) != 6 {
		t.Fatalf("tags = %d, want 6", len(tags))
	}
	if tags[4].typ != 8 || tags[5].typ != 9 {
		t.Fatalf("resynced tags wrong: %+v", tags[4:])
	}
}

func TestResyncSkipsLoneFakeTag(t *testing.T) {
	// A structurally valid fake tag inside the gap must be rejected when its
	// successor is not a tag; resync should land on the real tag pair instead.
	fake := makeTag(9, 30, []byte{0x27, 0x01, 0, 0, 0, 0xdd})
	gap := append([]byte("garbage!"), fake...)
	gap = append(gap, 0x00, 0x00, 0x00)
	input := makeFLV(videoSeq(), audioSeq(), videoKey(1000), gap, audioFrame(1040), videoP(1050))
	result, tags := repair(t, input, Options{})
	if len(result.Gaps) != 1 {
		t.Fatalf("gaps = %+v", result.Gaps)
	}
	if result.Gaps[0].Bytes != int64(len(gap)) {
		t.Fatalf("gap bytes = %d, want %d", result.Gaps[0].Bytes, len(gap))
	}
	if len(tags) != 5 {
		t.Fatalf("tags = %d, want 5: %+v", len(tags), tags)
	}
	// The fake tag must not have leaked into the output.
	for _, tag := range tags[3:] {
		if tag.ts == 30 {
			t.Fatalf("fake tag was accepted: %+v", tag)
		}
	}
}

func TestDropsTrailingBytes(t *testing.T) {
	input := makeFLV(videoSeq(), audioSeq(), videoKey(1000))
	input = append(input, []byte("tail!")...)
	result, tags := repair(t, input, Options{})
	if len(tags) != 3 {
		t.Fatalf("tags = %d, want 3", len(tags))
	}
	if result.TrailingBytes != 5 || result.DroppedBytes != 5 {
		t.Fatalf("trailing = %d, dropped = %d", result.TrailingBytes, result.DroppedBytes)
	}
}

func TestTrimBrokenVideoGOPsKeepsAudio(t *testing.T) {
	junk := []byte("garbage-gap")
	input := makeFLV(
		videoSeq(), audioSeq(),
		videoKey(0), audioFrame(0),
		videoP(40),
		junk,
		videoP(80), audioFrame(80), videoP(120), audioFrame(120),
		videoKey(160), audioFrame(160), videoP(200),
	)
	result, tags := repair(t, input, Options{TrimBrokenVideoGOPs: true})
	if result.DroppedVideoTags != 2 {
		t.Fatalf("dropped video tags = %d, want 2", result.DroppedVideoTags)
	}
	// Remaining tags: key0, audio0, P40, key160, audio160, P200.
	if len(tags) != 10 {
		t.Fatalf("tags = %d, want 10: %+v", len(tags), tags)
	}
	var videoTS []uint32
	for _, tag := range tags {
		if tag.typ == 9 && len(tag.payload) > 1 && tag.payload[1] != 0 {
			videoTS = append(videoTS, tag.ts)
		}
	}
	// Base is 40ms: key0=0, P40=0, key160=120, P200=160.
	if len(videoTS) != 4 || videoTS[0] != 0 || videoTS[1] != 0 || videoTS[2] != 120 || videoTS[3] != 160 {
		t.Fatalf("video timestamps = %v", videoTS)
	}
	if tags[7].typ != 9 || tags[7].payload[0] != 0x17 {
		t.Fatalf("expected keyframe after trim, got %+v", tags[7])
	}
}

func TestClampsTimestampRegression(t *testing.T) {
	input := makeFLV(videoSeq(), audioSeq(), videoKey(1000), videoP(900), videoP(950))
	_, tags := repair(t, input, Options{})
	last := uint32(0)
	for _, tag := range tags {
		if tag.ts < last {
			t.Fatalf("non-monotonic timestamp: %d < %d", tag.ts, last)
		}
		last = tag.ts
	}
}

func TestRejectsNonFLV(t *testing.T) {
	in := writeTemp(t, []byte("not an flv file at all"))
	out := filepath.Join(t.TempDir(), "out.flv")
	if _, err := Repair(in, out, Options{}); err == nil {
		t.Fatal("expected error for non-FLV input")
	}
}
