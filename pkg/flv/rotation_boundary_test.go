package flv

import "testing"

func TestFindRotationBoundaryVideoKeyframe(t *testing.T) {
	audio := NewTagBytes(TagTypeAudio, []byte{0xaf, 0x01, 0x00})
	nonKey := NewTagBytes(TagTypeVideo, []byte{0x27, 0x01, 0x00, 0x00, 0x00})
	seqHeader := NewTagBytes(TagTypeVideo, []byte{0x17, 0x00, 0x00, 0x00, 0x00})
	keyframe := NewTagBytes(TagTypeVideo, []byte{0x17, 0x01, 0x00, 0x00, 0x00})

	var stream []byte
	stream = append(stream, audio...)
	stream = append(stream, nonKey...)
	stream = append(stream, seqHeader...)
	want := len(stream)
	stream = append(stream, keyframe...)

	if got := FindRotationBoundary(stream, false); got != want {
		t.Fatalf("keyframe boundary = %d, want %d", got, want)
	}
}

func TestFindRotationBoundaryAudioFallback(t *testing.T) {
	audio := NewTagBytes(TagTypeAudio, []byte{0xaf, 0x01, 0x00})
	videoNonKey := NewTagBytes(TagTypeVideo, []byte{0x27, 0x01, 0x00, 0x00, 0x00})
	stream := append(append([]byte(nil), audio...), videoNonKey...)

	if got := FindRotationBoundary(stream, false); got != -1 {
		t.Fatalf("no video keyframe: got %d, want -1", got)
	}
	if got := FindRotationBoundary(stream, true); got != 0 {
		t.Fatalf("audio fallback boundary = %d, want 0", got)
	}
}

func TestFindRotationBoundaryWaitsForCompleteTag(t *testing.T) {
	keyframe := NewTagBytes(TagTypeVideo, []byte{0x17, 0x01, 0x00, 0x00, 0x00})
	if got := FindRotationBoundary(keyframe[:len(keyframe)-1], false); got != -1 {
		t.Fatalf("partial keyframe accepted at %d", got)
	}
}
