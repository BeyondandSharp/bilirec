// SPDX-License-Identifier: GPL-3.0-only

package processors

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/flvmeta"
	"github.com/bilirec/bilirec/pkg/flv"
	"github.com/bilirec/bilirec/pkg/pipeline"
)

func flvTestTag(tagType byte, ts uint32, payload []byte) []byte {
	tag := flv.NewTagBytes(tagType, payload)
	// FLV timestamp layout: 3-byte timestamp + 1-byte extended timestamp.
	tag[4] = byte(ts >> 16)
	tag[5] = byte(ts >> 8)
	tag[6] = byte(ts)
	tag[7] = byte(ts >> 24)
	return tag
}

func flvTestVideoSeq() []byte {
	return flvTestTag(flv.TagTypeVideo, 0, []byte{0x17, 0x00, 0x00, 0x00, 0x00, 0x01, 0x64, 0x00, 0x32})
}
func flvTestAudioSeq() []byte {
	return flvTestTag(flv.TagTypeAudio, 0, []byte{0xaf, 0x00, 0x11, 0x90})
}
func flvTestVideoKey(ts uint32) []byte {
	return flvTestTag(flv.TagTypeVideo, ts, []byte{0x17, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0x65})
}
func flvTestVideoP(ts uint32) []byte {
	return flvTestTag(flv.TagTypeVideo, ts, []byte{0x27, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x41})
}
func flvTestAudio(ts uint32) []byte {
	return flvTestTag(flv.TagTypeAudio, ts, []byte{0xaf, 0x01, 0x01, 0x02})
}

// flvTestSourceScript builds a minimal source onMetaData tag with an extra
// "encoder" field, so tests cover both replacement and source-field preservation.
func flvTestSourceScript(t *testing.T) []byte {
	t.Helper()
	body := []byte{0x02, 0x00, 0x0a}
	body = append(body, "onMetaData"...)
	body = append(body, 0x08, 0x00, 0x00, 0x00, 0x02) // ECMA array, 2 entries
	body = append(body, 0x00, 0x07)
	body = append(body, "encoder"...)
	body = append(body, 0x02, 0x00, 0x03)
	body = append(body, "obs"...)
	body = append(body, 0x00, 0x08)
	body = append(body, "duration"...)
	body = append(body, 0x00) // AMF number
	body = append(body, make([]byte, 8)...)
	body = append(body, 0x00, 0x00, 0x09)
	return flvTestTag(18, 0, body)
}

func newMetadataTestPipe(t *testing.T, builder *flvmeta.Builder, path string, patchInterval time.Duration) *pipeline.Pipe[[]byte] {
	t.Helper()
	proc := NewBufferedStreamWriter(path,
		WithSkipSmallFlushThreshold(0),
		WithBufferSize(64*1024),
		WithFlvMetadata(builder),
		WithFlvMetadataPatchInterval(patchInterval),
	)
	return pipeline.New(
		NewFlvMetadataExtractor(builder),
		NewFlvHeaderWriter(nil, nil),
		proc,
	)
}

func readWrittenMetadata(t *testing.T, path string) (map[string]any, int) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0
	}
	if len(data) < 13+11+4 {
		return nil, 0
	}
	size := int(data[14])<<16 | int(data[15])<<8 | int(data[16])
	if data[13] != 18 || len(data) < 13+11+size+4 {
		return nil, 0
	}
	body, err := flvmeta.ParseMetadataTagBody(data[24 : 24+size])
	if err != nil {
		return nil, 0
	}
	return body, size
}

func TestFlvMetadataWrittenWithDurationAndKeyframes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.flv")
	builder := flvmeta.NewBuilder()
	pipe := newMetadataTestPipe(t, builder, path, time.Hour)
	ctx := context.Background()
	if err := pipe.Open(ctx); err != nil {
		t.Fatal(err)
	}

	// Chunk 1: source metadata + sequence headers, no media yet. The writer
	// buffers these bytes until the first media tag arrives.
	chunk1 := append(flvTestSourceScript(t), flvTestVideoSeq()...)
	chunk1 = append(chunk1, flvTestAudioSeq()...)
	if _, err := pipe.Process(ctx, chunk1); err != nil {
		t.Fatal(err)
	}

	// Chunk 2: media tags. Keyframes at 0ms and 2000ms; last data tag 2040ms.
	chunk2 := append(flvTestVideoKey(0), flvTestAudio(0)...)
	chunk2 = append(chunk2, flvTestVideoP(40)...)
	chunk2 = append(chunk2, flvTestVideoP(1000)...)
	chunk2 = append(chunk2, flvTestAudio(2040)...)
	chunk2 = append(chunk2, flvTestVideoKey(2000)...)
	chunk2 = append(chunk2, flvTestVideoP(2040)...)
	if _, err := pipe.Process(ctx, chunk2); err != nil {
		t.Fatal(err)
	}
	pipe.Close()

	body, _ := readWrittenMetadata(t, path)
	if body == nil {
		t.Fatal("metadata tag not written at file start")
	}
	if got := body["duration"].(float64); got != 2.04 {
		data, _ := os.ReadFile(path)
		if len(data) > 80 {
			data = data[:80]
		}
		t.Fatalf("duration = %v, want 2.04; head=% x", got, data)
	}
	if got := body["encoder"].(string); got != "obs" {
		t.Fatalf("source encoder field = %v, want obs", body["encoder"])
	}
	keyframes := body["keyframes"].(map[string]any)
	times := keyframes["times"].([]any)
	positions := keyframes["filepositions"].([]any)
	if len(times) != 2 {
		t.Fatalf("keyframe times = %v", times)
	}
	if times[0].(float64) != 0 || times[1].(float64) != 2 {
		t.Fatalf("keyframe times = %v", times)
	}
	// Verify filepositions point at the actual video tag headers.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var keyframeOffsets []int
	off := 13
	for off+11 <= len(data) {
		typ := data[off]
		size := int(data[off+1])<<16 | int(data[off+2])<<8 | int(data[off+3])
		if off+11+size+4 > len(data) {
			t.Fatalf("truncated tag at %d", off)
		}
		if typ == 9 && size >= 2 && data[off+11]>>4 == 1 && data[off+12] == 1 {
			keyframeOffsets = append(keyframeOffsets, off)
		}
		off += 11 + size + 4
	}
	if off != len(data) {
		t.Fatalf("trailing bytes: %d", len(data)-off)
	}
	if len(keyframeOffsets) != len(positions) {
		t.Fatalf("keyframe offsets = %v, metadata positions = %v", keyframeOffsets, positions)
	}
	for i, want := range keyframeOffsets {
		if int(positions[i].(float64)) != want {
			t.Fatalf("fileposition[%d] = %v, want %d", i, positions[i], want)
		}
	}
	// Exactly one script tag (ours), no duplicate source metadata.
	scriptTags := 0
	off = 13
	for off+11 <= len(data) {
		size := int(data[off+1])<<16 | int(data[off+2])<<8 | int(data[off+3])
		if data[off] == 18 {
			scriptTags++
		}
		off += 11 + size + 4
	}
	if scriptTags != 1 {
		t.Fatalf("script tags = %d, want 1", scriptTags)
	}
}

func TestFlvMetadataRewrittenWhileRecording(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.flv")
	builder := flvmeta.NewBuilder()
	pipe := newMetadataTestPipe(t, builder, path, 10*time.Millisecond)
	ctx := context.Background()
	if err := pipe.Open(ctx); err != nil {
		t.Fatal(err)
	}
	first := append(flvTestSourceScript(t), flvTestVideoSeq()...)
	first = append(first, flvTestAudioSeq()...)
	first = append(first, flvTestVideoKey(0)...)
	first = append(first, flvTestAudio(0)...)
	if _, err := pipe.Process(ctx, first); err != nil {
		t.Fatal(err)
	}
	// Wait until the initial metadata tag exists on disk.
	deadline := time.Now().Add(3 * time.Second)
	for {
		if body, _ := readWrittenMetadata(t, path); body != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("initial metadata tag was not flushed")
		}
		time.Sleep(10 * time.Millisecond)
	}

	time.Sleep(30 * time.Millisecond)
	if _, err := pipe.Process(ctx, append(flvTestVideoP(500), flvTestVideoP(1000)...)); err != nil {
		t.Fatal(err)
	}
	// The patch interval has elapsed; metadata should be rewritten with the
	// newer duration before the segment is closed.
	deadline = time.Now().Add(3 * time.Second)
	for {
		body, _ := readWrittenMetadata(t, path)
		if body != nil {
			if duration, ok := body["duration"].(float64); ok && duration == 1.0 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("metadata duration was not rewritten during recording")
		}
		time.Sleep(10 * time.Millisecond)
	}
	pipe.Close()
}
