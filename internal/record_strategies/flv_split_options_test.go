// SPDX-License-Identifier: GPL-3.0-only

package record_strategies

import (
	"context"
	"encoding/binary"
	"errors"
	"path/filepath"
	"testing"

	"github.com/bilirec/bilirec/internal/processors"
	"github.com/bilirec/bilirec/pkg/pipeline"
)

func splitTestTag(tagType byte, payload []byte) []byte {
	tag := make([]byte, 11+len(payload)+4)
	tag[0] = tagType
	tag[1] = byte(len(payload) >> 16)
	tag[2] = byte(len(payload) >> 8)
	tag[3] = byte(len(payload))
	copy(tag[11:], payload)
	binary.BigEndian.PutUint32(tag[11+len(payload):], uint32(11+len(payload)))
	return tag
}

func splitTestFLV(tags ...[]byte) []byte {
	out := append([]byte("FLV"), 0x01, 0x05, 0x00, 0x00, 0x00, 0x09, 0x00, 0x00, 0x00, 0x00)
	for _, tag := range tags {
		out = append(out, tag...)
	}
	return out
}

func splitTestScriptTag() []byte {
	return splitTestTag(0x12, []byte{0x02, 0x00, 0x0A, 'o', 'n', 'M', 'e', 't', 'a', 'D', 'a', 't', 'a', 0x08, 0x00, 0x00, 0x00, 0x00})
}

func splitTestVideoHeader() []byte {
	return splitTestTag(0x09, []byte{0x17, 0x00, 0x00, 0x00, 0x00, 0x01, 0x64})
}

func splitTestMediaTag(annexB bool) []byte {
	payload := []byte{0x17, 0x01, 0x00, 0x00, 0x00}
	if annexB {
		payload = append(payload, 0x00, 0x00, 0x00, 0x01, 0x65, 0x88)
	} else {
		payload = append(payload, 0x00, 0x00, 0x00, 0x02, 0x65, 0x88)
	}
	return splitTestTag(0x09, payload)
}

func buildFlvTestPipeline(t *testing.T, strategy *FlvStrategy) *pipeline.Pipe[[]byte] {
	t.Helper()
	ctx := context.Background()
	pipe, err := strategy.BuildPipeline(ctx, filepath.Join(t.TempDir(), "split.flv"), &RotationState{Data: map[string][]byte{}}, PipelineHooks{})
	if err != nil {
		t.Fatalf("BuildPipeline: %v", err)
	}
	if err := pipe.Open(ctx); err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(pipe.Close)
	return pipe
}

func TestFlvStrategyScriptTagSplitEnabled(t *testing.T) {
	ensureBenchmarkConfig()
	strategy := NewFlvStrategyWithOptions(10000, FlvSplitOptions{SplitOnScriptTag: true})
	defer strategy.Close()
	pipe := buildFlvTestPipeline(t, strategy)

	data := splitTestFLV(splitTestVideoHeader(), splitTestMediaTag(false), splitTestScriptTag())
	if _, err := pipe.Process(context.Background(), data); !errors.Is(err, processors.ErrScriptTagSplit) {
		t.Fatalf("process err = %v, want ErrScriptTagSplit", err)
	}
}

func TestFlvStrategyScriptTagSplitDefaultDisabled(t *testing.T) {
	ensureBenchmarkConfig()
	strategy := NewFlvStrategy(10000)
	defer strategy.Close()
	pipe := buildFlvTestPipeline(t, strategy)

	data := splitTestFLV(splitTestVideoHeader(), splitTestMediaTag(false), splitTestScriptTag())
	if _, err := pipe.Process(context.Background(), data); err != nil {
		t.Fatalf("default strategy must not split on script tag: %v", err)
	}
}

func TestFlvStrategyAnnexBSplitEnabled(t *testing.T) {
	ensureBenchmarkConfig()
	strategy := NewFlvStrategyWithOptions(10000, FlvSplitOptions{SplitOnAnnexB: true})
	defer strategy.Close()
	pipe := buildFlvTestPipeline(t, strategy)

	data := splitTestFLV(splitTestVideoHeader(), splitTestMediaTag(true))
	if _, err := pipe.Process(context.Background(), data); !errors.Is(err, processors.ErrAnnexBSplit) {
		t.Fatalf("process err = %v, want ErrAnnexBSplit", err)
	}
}

func TestFlvStrategyAnnexBSplitDefaultDisabled(t *testing.T) {
	ensureBenchmarkConfig()
	strategy := NewFlvStrategy(10000)
	defer strategy.Close()
	pipe := buildFlvTestPipeline(t, strategy)

	data := splitTestFLV(splitTestVideoHeader(), splitTestMediaTag(true))
	if _, err := pipe.Process(context.Background(), data); err != nil {
		t.Fatalf("default strategy must not split on annexb: %v", err)
	}
}

func TestFlvStrategyDetectorSplitRotatesWithHeaders(t *testing.T) {
	ensureBenchmarkConfig()
	strategy := NewFlvStrategy(10000)
	defer strategy.Close()
	video := []byte("video-header")
	audio := []byte("audio-header")
	strategy.observeHeaders(video, audio)

	for _, detectorErr := range []error{processors.ErrScriptTagSplit, processors.ErrAnnexBSplit} {
		result := strategy.HandleErr(detectorErr)
		if result.Action != ErrActionRotate {
			t.Fatalf("%v: action = %v, want rotate", detectorErr, result.Action)
		}
		if result.State == nil || string(result.State.Data[flvStateVideoHdr]) != string(video) {
			t.Fatalf("%v: headers not carried: %+v", detectorErr, result.State)
		}
	}
}
