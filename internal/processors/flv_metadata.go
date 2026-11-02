// SPDX-License-Identifier: GPL-3.0-only

package processors

import (
	"context"
	"time"

	"github.com/bilirec/bilirec/internal/flvmeta"
	"github.com/bilirec/bilirec/pkg/logger"
	"github.com/bilirec/bilirec/pkg/pipeline"
)

const (
	// defaultMetadataPatchInterval bounds how often onMetaData is rewritten in
	// place. Each write is ~114 KiB, so 5s keeps duration reasonably fresh
	// without excessive SD-card wear.
	defaultMetadataPatchInterval = 5 * time.Second

	// maxMetadataPreBuffer caps how many bytes are held before the first media
	// tag arrives. Sequence headers normally fit easily; the cap protects
	// pathological streams (or a source with no media tags at all).
	maxMetadataPreBuffer = 4 << 20

	// maxFLVTagDataSize mirrors the FLV reframer sanity bound and keeps the
	// extractor/tracker from buffering absurd tag sizes.
	maxFLVTagDataSize = 32 << 20
)

// FlvMetadataExtractor strips source onMetaData script tags from the tag
// stream and feeds their fields into builder. The writer re-emits a complete,
// fixed-size onMetaData tag with duration/keyframes, so keeping the source tag
// would produce a duplicate script tag.
type flvMetadataExtractorProcessor struct {
	builder *flvmeta.Builder
	buf     []byte
}

// NewFlvMetadataExtractor returns a processor placed before the FLV header
// writer. It expects a tag stream (no FLV file header) and drops onMetaData
// script tags.
func NewFlvMetadataExtractor(builder *flvmeta.Builder) *pipeline.ProcessorInfo[[]byte] {
	return pipeline.NewProcessorInfo(
		"flv-metadata-extractor",
		&flvMetadataExtractorProcessor{builder: builder},
	)
}

func (p *flvMetadataExtractorProcessor) Open(ctx context.Context, log logger.Logger) error {
	p.buf = p.buf[:0]
	return nil
}

func (p *flvMetadataExtractorProcessor) Process(ctx context.Context, log logger.Logger, data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}
	p.buf = append(p.buf, data...)

	// Defensive: upstream normally strips the file header, but tolerate it.
	if len(p.buf) >= 13 && string(p.buf[:3]) == "FLV" {
		p.buf = p.buf[13:]
	}

	out := make([]byte, 0, len(p.buf))
	for {
		if len(p.buf) < 11 {
			break
		}
		typ := p.buf[0]
		size := uint32(p.buf[1])<<16 | uint32(p.buf[2])<<8 | uint32(p.buf[3])
		if size == 0 || size > maxFLVTagDataSize {
			// Not a tag stream we understand; pass the rest through unchanged.
			out = append(out, p.buf...)
			p.buf = p.buf[:0]
			return out, nil
		}
		total := 11 + int(size) + 4
		if len(p.buf) < total {
			break
		}
		tag := p.buf[:total]
		if typ == 18 {
			payload := tag[11 : 11+int(size)]
			if flvmeta.IsOnMetaData(payload) || flvmeta.LooksLikeOnMetaData(payload) {
				p.builder.SetBaseFromScript(payload)
				p.buf = p.buf[total:]
				continue
			}
		}
		out = append(out, tag...)
		p.buf = p.buf[total:]
	}
	if len(p.buf) > 0 && len(out) > 0 {
		p.buf = append([]byte(nil), p.buf...)
	}
	return out, nil
}

func (p *flvMetadataExtractorProcessor) Close() error { return nil }

type flvMetadataState struct {
	builder           *flvmeta.Builder
	patchInterval     time.Duration
	initialized       bool
	initializing      bool
	offset            int64
	tagLen            int
	preBuf            []byte
	tracker           *flvTagTracker
	lastPatch         time.Time
	patchedVersion    int64
	lastKeyframeCount int
	buildFailed       bool
	tagLenMismatch    bool
}

func newFlvMetadataState(builder *flvmeta.Builder, patchInterval time.Duration) *flvMetadataState {
	if patchInterval <= 0 {
		patchInterval = defaultMetadataPatchInterval
	}
	return &flvMetadataState{builder: builder, patchInterval: patchInterval}
}

// bufferInitialData accumulates header/sequence-header bytes until the first
// media tag is visible. onMetaData must precede sequence headers, and the base
// metadata from the source script tag must be known before building it.
func (s *flvMetadataState) bufferInitialData(w *BufferedStreamWriterProcessor, data []byte) {
	if s.buildFailed {
		w.writePiece(data)
		return
	}
	s.preBuf = append(s.preBuf, data...)
	if !s.ready() && len(s.preBuf) < maxMetadataPreBuffer {
		return
	}
	s.initialize(w)
}

func (s *flvMetadataState) ready() bool {
	if len(s.preBuf) < 13 {
		return false
	}
	if string(s.preBuf[:3]) != "FLV" {
		return false
	}
	off := 13
	for off+11 <= len(s.preBuf) {
		typ := s.preBuf[off]
		size := uint32(s.preBuf[off+1])<<16 | uint32(s.preBuf[off+2])<<8 | uint32(s.preBuf[off+3])
		if size == 0 || size > maxFLVTagDataSize {
			return false
		}
		total := 11 + int(size) + 4
		if off+total > len(s.preBuf) {
			return false
		}
		if typ == 8 || typ == 9 {
			if size >= 2 && s.preBuf[off+11+1] != 0 { // packet type 1 = media
				return true
			}
		}
		off += total
	}
	return false
}

func (s *flvMetadataState) initialize(w *BufferedStreamWriterProcessor) {
	if s.initialized {
		return
	}
	if len(s.preBuf) < 13 || string(s.preBuf[:3]) != "FLV" {
		// Not a recognizable FLV stream; fall back to plain writing.
		s.buildFailed = true
		if len(s.preBuf) > 0 {
			w.writePiece(s.preBuf)
			s.preBuf = nil
		}
		return
	}
	tag, err := s.builder.BuildTag()
	if err != nil {
		w.logger.Warnf("构建 FLV onMetaData 失败，跳过元数据写入：%v", err)
		s.buildFailed = true
		w.writePiece(s.preBuf)
		s.preBuf = nil
		return
	}
	s.initialized = true
	s.initializing = true
	s.tagLen = len(tag)
	s.tracker = newFlvTagTracker(s.builder, int64(13+len(tag)))
	s.lastPatch = time.Now()
	s.patchedVersion = s.builder.Version()
	s.lastKeyframeCount = s.builder.KeyframeCount()

	header := s.preBuf[:13]
	rest := s.preBuf[13:]
	w.writePiece(header)
	s.offset = w.bytesWritten.Load()
	w.writePiece(tag)
	w.writePiece(rest)
	if w.writer != nil {
		if err := w.writer.Flush(); err != nil {
			w.logger.Warnf("写入 onMetaData 后 flush 失败：%v", err)
		} else {
			w.syncBufferedBytes()
		}
	}
	s.preBuf = nil
	s.initializing = false
	s.maybePatch(w, time.Now(), true)
	s.lastKeyframeCount = s.builder.KeyframeCount()
}

// observe feeds freshly written output bytes to the tag tracker. start is the
// absolute file offset of data[0].
func (s *flvMetadataState) observe(w *BufferedStreamWriterProcessor, data []byte, start int64) {
	if !s.initialized {
		return
	}
	if s.tracker != nil {
		s.tracker.Feed(data, start)
	}
	now := time.Now()
	// BililiveRecorder rewrites metadata after each data group/keyframe. Do the
	// same for keyframes; between keyframes the 5s interval bounds duration lag.
	if count := s.builder.KeyframeCount(); count != s.lastKeyframeCount {
		s.lastKeyframeCount = count
		s.maybePatch(w, now, true)
		return
	}
	s.maybePatch(w, now, false)
}

func (s *flvMetadataState) maybePatch(w *BufferedStreamWriterProcessor, now time.Time, force bool) {
	if !s.initialized || s.initializing || s.buildFailed || s.tagLen == 0 {
		return
	}
	if !force {
		if s.builder.Version() == s.patchedVersion {
			return
		}
		if now.Sub(s.lastPatch) < s.patchInterval {
			return
		}
	}
	file := w.file.Load()
	if file == nil {
		return
	}
	tag, err := s.builder.BuildTag()
	if err != nil {
		w.logger.Warnf("构建 FLV onMetaData 失败，停止回写：%v", err)
		s.buildFailed = true
		return
	}
	if len(tag) != s.tagLen {
		if !s.tagLenMismatch {
			w.logger.Warnf("onMetaData 长度发生变化（%d -> %d），停止回写", s.tagLen, len(tag))
			s.tagLenMismatch = true
		}
		return
	}
	if _, err := file.WriteAt(tag, s.offset); err != nil {
		w.logger.Warnf("回写 FLV onMetaData 失败：%v", err)
		return
	}
	s.lastPatch = now
	s.patchedVersion = s.builder.Version()
}

func (s *flvMetadataState) finalize(w *BufferedStreamWriterProcessor) {
	if !s.initialized {
		if len(s.preBuf) > 0 {
			s.initialize(w)
		}
	}
	s.maybePatch(w, time.Now(), true)
}

// WithFlvMetadata enables BililiveRecorder-compatible onMetaData writing:
// duration/keyframes/filepositions are maintained while recording and rewritten
// in place. The builder must also be passed to NewFlvMetadataExtractor so that
// source onMetaData fields are preserved.
func WithFlvMetadata(builder *flvmeta.Builder) BufferedStreamWriterOptions {
	return func(p *BufferedStreamWriterProcessor) {
		p.flvMetadata = newFlvMetadataState(builder, p.metadataPatchInterval)
	}
}

// WithFlvMetadataPatchInterval overrides the in-place rewrite interval
// (default 5s). A value <= 0 restores the default.
func WithFlvMetadataPatchInterval(d time.Duration) BufferedStreamWriterOptions {
	return func(p *BufferedStreamWriterProcessor) {
		p.metadataPatchInterval = d
		if p.flvMetadata != nil {
			if d <= 0 {
				d = defaultMetadataPatchInterval
			}
			p.flvMetadata.patchInterval = d
		}
	}
}

// flvTagTracker follows the written FLV stream and reports video keyframes and
// the latest data-tag timestamp. It never buffers full payloads.
type flvTagTracker struct {
	builder     *flvmeta.Builder
	skip        int64
	header      [11]byte
	headerN     int
	headerStart int64
	remaining   int64 // payload + previous-tag-size
	curType     byte
	curTS       uint32
	curPayload  [2]byte
	payloadN    int
	lastTS      int64
}

func newFlvTagTracker(builder *flvmeta.Builder, skip int64) *flvTagTracker {
	return &flvTagTracker{builder: builder, skip: skip}
}

func (t *flvTagTracker) Feed(data []byte, start int64) {
	abs := start
	if t.skip > 0 {
		if abs+int64(len(data)) <= t.skip {
			return
		}
		if abs < t.skip {
			drop := t.skip - abs
			data = data[drop:]
			abs = t.skip
		}
		t.skip = 0
	}
	for i := 0; i < len(data); {
		if t.remaining > 0 {
			n := len(data) - i
			if int64(n) > t.remaining {
				n = int(t.remaining)
			}
			for k := 0; k < n && t.payloadN < 2; k++ {
				t.curPayload[t.payloadN] = data[i+k]
				t.payloadN++
			}
			t.remaining -= int64(n)
			i += n
			abs += int64(n)
			if t.remaining == 0 {
				t.finishTag()
			}
			continue
		}
		t.header[t.headerN] = data[i]
		if t.headerN == 0 {
			t.headerStart = abs
		}
		t.headerN++
		i++
		abs++
		if t.headerN == 11 {
			t.parseHeader()
		}
	}
}

func (t *flvTagTracker) parseHeader() {
	typ := t.header[0]
	size := uint32(t.header[1])<<16 | uint32(t.header[2])<<8 | uint32(t.header[3])
	if (typ != 8 && typ != 9 && typ != 18) || size == 0 || size > maxFLVTagDataSize {
		t.headerN = 0
		return
	}
	t.curType = typ
	t.curTS = uint32(t.header[4])<<16 | uint32(t.header[5])<<8 | uint32(t.header[6]) | uint32(t.header[7])<<24
	t.remaining = int64(size) + 4
	t.payloadN = 0
	t.headerN = 0
}

func (t *flvTagTracker) finishTag() {
	if t.curType == 8 || t.curType == 9 {
		t.lastTS = int64(t.curTS)
		t.builder.SetDuration(t.lastTS)
	}
	if t.curType == 9 && t.payloadN >= 2 && (t.curPayload[0]>>4) == 1 && t.curPayload[1] == 1 {
		t.builder.AddKeyframe(int64(t.curTS), t.headerStart)
	}
}

func (w *BufferedStreamWriterProcessor) writeOutput(data []byte) {
	if w.writer == nil {
		w.bytesPool.Put(data)
		return
	}
	if w.flvMetadata != nil && !w.flvMetadata.initialized {
		w.flvMetadata.bufferInitialData(w, data)
		w.bytesPool.Put(data)
		return
	}
	w.writePiece(data)
	w.bytesPool.Put(data)
}

// writePiece writes one byte slice to the buffered writer and updates metadata
// tracking. It does not return data to the pool; callers own that.
func (w *BufferedStreamWriterProcessor) writePiece(data []byte) {
	if w.writer == nil || len(data) == 0 {
		return
	}
	start := w.bytesWritten.Load()
	n, err := w.writer.Write(data)
	w.noteWriteFailure(len(data), n, err)
	if n > 0 {
		w.addWrittenBytes(n)
		w.syncBufferedBytes()
		if w.flvMetadata != nil && w.flvMetadata.initialized {
			w.flvMetadata.observe(w, data[:n], start)
		}
	}
}
