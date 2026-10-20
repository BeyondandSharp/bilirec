// SPDX-License-Identifier: GPL-3.0-or-later

package flv

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// flvFixturePath points at the T15 fixture directory under the repo-root
// testdata/ directory (the Go module root is the repo root after the layout
// restructure). Tests skip when it is absent.
func flvFixturePath(name string) string {
	return filepath.Join("..", "..", "testdata", "flv", "fixtures", name)
}

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(flvFixturePath(name))
	if err != nil {
		t.Skipf("fixture %s not available: %v", name, err)
	}
	return data
}

// feedChunks feeds data to detect in fixed-size chunks and reports how many
// times the given error was raised.
func feedChunks(t *testing.T, data []byte, chunkSize int, detect func([]byte) error, want error) int {
	t.Helper()
	raised := 0
	for offset := 0; offset < len(data); offset += chunkSize {
		end := offset + chunkSize
		if end > len(data) {
			end = len(data)
		}
		err := detect(data[offset:end])
		if err != nil {
			if want == nil || !errors.Is(err, want) {
				t.Fatalf("unexpected detector error: %v", err)
			}
			raised++
		}
	}
	return raised
}

func TestScriptTagSplitDetectorFixtures(t *testing.T) {
	control := loadFixture(t, "v2_clean_prefix.flv")
	mid := loadFixture(t, "v2_script_tag_mid.flv")

	controlDetector := NewScriptTagSplitDetector()
	if raised := feedChunks(t, control, 1024, controlDetector.Detect, nil); raised != 0 {
		t.Fatalf("control fixture raised ErrScriptTagSplit %d times, want 0", raised)
	}

	if !fixtureHasScriptAfterMedia(mid) {
		// Structural regression guard: the generator must insert the injected
		// Script tag after the first media data tag (docs/FEATURES-V2.md §5.2;
		// qa-harness gen_v2_detectors.py verifies this too).
		t.Fatal("v2_script_tag_mid.flv has no Script tag after the first media data tag")
	}
	detector := NewScriptTagSplitDetector()
	if raised := feedChunks(t, mid, 1024, detector.Detect, ErrScriptTagSplit); raised != 1 {
		t.Fatalf("mid-stream script tag raised %d times, want exactly 1", raised)
	}
	// Detection is latched: later chunks do not re-raise.
	if err := detector.Detect(mid); err != nil {
		t.Fatalf("detector must raise only once, got %v", err)
	}
}

func TestAnnexBDetectorFixtures(t *testing.T) {
	control := loadFixture(t, "v2_clean_prefix.flv")
	annexb := loadFixture(t, "v2_annexb_video.flv")

	controlDetector := NewAnnexBDetector()
	if raised := feedChunks(t, control, 1024, controlDetector.Detect, nil); raised != 0 {
		t.Fatalf("control fixture raised ErrAnnexBSplit %d times, want 0", raised)
	}

	detector := NewAnnexBDetector()
	if raised := feedChunks(t, annexb, 1024, detector.Detect, ErrAnnexBSplit); raised != 1 {
		t.Fatalf("annexb fixture raised %d times, want exactly 1", raised)
	}
	if err := detector.Detect(annexb); err != nil {
		t.Fatalf("detector must raise only once, got %v", err)
	}
}

// --- synthetic FLV helpers -------------------------------------------------

func syntheticFlv(t *testing.T, tags ...[]byte) []byte {
	t.Helper()
	header := append([]byte("FLV"), 0x01, 0x05, 0x00, 0x00, 0x00, 0x09)
	header = append(header, 0x00, 0x00, 0x00, 0x00) // PreviousTagSize0
	var out []byte
	out = append(out, header...)
	for _, tag := range tags {
		out = append(out, tag...)
	}
	return out
}

func syntheticTag(tagType byte, payload []byte) []byte {
	tag := make([]byte, TagHeaderSize+len(payload)+PrevTagSizeBytes)
	tag[0] = tagType
	tag[1] = byte(len(payload) >> 16)
	tag[2] = byte(len(payload) >> 8)
	tag[3] = byte(len(payload))
	copy(tag[TagHeaderSize:], payload)
	binary.BigEndian.PutUint32(tag[TagHeaderSize+len(payload):], uint32(TagHeaderSize+len(payload)))
	return tag
}

func scriptTag() []byte {
	// Minimal AMF0 onMetaData with an empty ECMA array is enough for the
	// tag-type based detector.
	return syntheticTag(TagTypeScript, []byte{0x02, 0x00, 0x0A, 'o', 'n', 'M', 'e', 't', 'a', 'D', 'a', 't', 'a', 0x08, 0x00, 0x00, 0x00, 0x00})
}

func avcSequenceHeaderTag() []byte {
	return syntheticTag(TagTypeVideo, []byte{0x17, 0x00, 0x00, 0x00, 0x00, 0x01, 0x64, 0x00, 0x1F})
}

func aacSequenceHeaderTag() []byte {
	return syntheticTag(TagTypeAudio, []byte{0xAF, 0x00, 0x12, 0x10})
}

func avcMediaTagAVCC(nalus ...[]byte) []byte {
	payload := []byte{0x17, 0x01, 0x00, 0x00, 0x00}
	for _, nalu := range nalus {
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(nalu)))
		payload = append(payload, length[:]...)
		payload = append(payload, nalu...)
	}
	return syntheticTag(TagTypeVideo, payload)
}

func avcMediaTagAnnexB(raw []byte) []byte {
	payload := []byte{0x17, 0x01, 0x00, 0x00, 0x00}
	payload = append(payload, raw...)
	return syntheticTag(TagTypeVideo, payload)
}

// fixtureHasScriptAfterMedia reports whether the fixture actually contains a
// Script tag after the first media data (non-sequence-header) tag.
func fixtureHasScriptAfterMedia(data []byte) bool {
	walker := &tagWalker{}
	sawMedia := false
	found := false
	_ = walker.walk(data, func(tagType byte, tagData []byte) error {
		switch tagType {
		case TagTypeScript:
			if sawMedia {
				found = true
			}
		case TagTypeVideo:
			if _, isHeader, _ := ClassifyVideoTag(tagData); !isHeader {
				sawMedia = true
			}
		case TagTypeAudio:
			if !isAudioSequenceHeader(tagData) {
				sawMedia = true
			}
		}
		return nil
	})
	return found
}

func TestScriptTagSplitDetectorSynthetic(t *testing.T) {
	// Script before any media tag is allowed.
	before := syntheticFlv(t, scriptTag(), avcSequenceHeaderTag(), aacSequenceHeaderTag(), avcMediaTagAVCC([]byte{0x65, 0x88}))
	detector := NewScriptTagSplitDetector()
	if err := detector.Detect(before); err != nil {
		t.Fatalf("script tag before media must not trigger: %v", err)
	}

	// Config tags alone do not count as media, so a later script tag still
	// must not trigger.
	configOnly := syntheticFlv(t, scriptTag(), avcSequenceHeaderTag(), aacSequenceHeaderTag(), scriptTag())
	detector = NewScriptTagSplitDetector()
	if err := detector.Detect(configOnly); err != nil {
		t.Fatalf("script tag after only config tags must not trigger: %v", err)
	}

	// Script after a media data tag triggers exactly once.
	after := syntheticFlv(t, avcSequenceHeaderTag(), aacSequenceHeaderTag(), avcMediaTagAVCC([]byte{0x65, 0x88}), scriptTag())
	detector = NewScriptTagSplitDetector()
	if err := detector.Detect(after); !errors.Is(err, ErrScriptTagSplit) {
		t.Fatalf("script after media tag: err = %v, want ErrScriptTagSplit", err)
	}
	if err := detector.Detect(after); err != nil {
		t.Fatalf("detector must raise only once: %v", err)
	}
}

func TestAnnexBDetectorSynthetic(t *testing.T) {
	avcc := syntheticFlv(t, avcSequenceHeaderTag(), avcMediaTagAVCC([]byte{0x65, 0x88, 0x80}))
	detector := NewAnnexBDetector()
	if err := detector.Detect(avcc); err != nil {
		t.Fatalf("AVCC length-prefixed NALU must not trigger: %v", err)
	}

	fourByte := syntheticFlv(t, avcSequenceHeaderTag(), avcMediaTagAnnexB([]byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88}))
	detector = NewAnnexBDetector()
	if err := detector.Detect(fourByte); !errors.Is(err, ErrAnnexBSplit) {
		t.Fatalf("4-byte annexb start code: err = %v, want ErrAnnexBSplit", err)
	}
	if err := detector.Detect(fourByte); err != nil {
		t.Fatalf("detector must raise only once: %v", err)
	}

	threeByte := syntheticFlv(t, avcSequenceHeaderTag(), avcMediaTagAnnexB([]byte{0x65, 0x00, 0x00, 0x01, 0x88}))
	detector = NewAnnexBDetector()
	if err := detector.Detect(threeByte); !errors.Is(err, ErrAnnexBSplit) {
		t.Fatalf("3-byte annexb start code: err = %v, want ErrAnnexBSplit", err)
	}
}

func TestSplitDetectorsHandleChunkBoundaries(t *testing.T) {
	data := syntheticFlv(t, avcSequenceHeaderTag(), aacSequenceHeaderTag(), avcMediaTagAVCC([]byte{0x65, 0x88}), scriptTag())

	// Feed one byte at a time: tags span chunk boundaries.
	detector := NewScriptTagSplitDetector()
	var err error
	for i := 0; i < len(data) && err == nil; i++ {
		err = detector.Detect(data[i : i+1])
	}
	if !errors.Is(err, ErrScriptTagSplit) {
		t.Fatalf("chunk-boundary scan err = %v, want ErrScriptTagSplit", err)
	}

	annexB := syntheticFlv(t, avcSequenceHeaderTag(), avcMediaTagAnnexB([]byte{0x00, 0x00, 0x00, 0x01, 0x65}))
	detector2 := NewAnnexBDetector()
	err = nil
	for i := 0; i < len(annexB) && err == nil; i++ {
		err = detector2.Detect(annexB[i : i+1])
	}
	if !errors.Is(err, ErrAnnexBSplit) {
		t.Fatalf("chunk-boundary annexb err = %v, want ErrAnnexBSplit", err)
	}
}

func TestSplitDetectorReset(t *testing.T) {
	data := syntheticFlv(t, avcSequenceHeaderTag(), avcMediaTagAVCC([]byte{0x65}), scriptTag())
	detector := NewScriptTagSplitDetector()
	if err := detector.Detect(data); !errors.Is(err, ErrScriptTagSplit) {
		t.Fatalf("unexpected err: %v", err)
	}
	detector.Reset()
	if err := detector.Detect(avcSequenceHeaderTag()); err != nil {
		t.Fatalf("reset detector errored: %v", err)
	}
}
