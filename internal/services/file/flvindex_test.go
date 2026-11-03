package file

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func testFLVTag(typ byte, ts uint32, payload []byte) []byte {
	tag := make([]byte, 11+len(payload)+4)
	tag[0] = typ
	tag[1] = byte(len(payload) >> 16)
	tag[2] = byte(len(payload) >> 8)
	tag[3] = byte(len(payload))
	tag[4] = byte(ts >> 16)
	tag[5] = byte(ts >> 8)
	tag[6] = byte(ts)
	tag[7] = byte(ts >> 24)
	copy(tag[11:], payload)
	binary.BigEndian.PutUint32(tag[11+len(payload):], uint32(11+len(payload)))
	return tag
}

func TestScanFLVIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.flv")
	var b []byte
	b = append(b, 'F', 'L', 'V', 1, 0x05, 0, 0, 0, 9, 0, 0, 0, 0)
	positions := map[uint32]int64{}
	add := func(typ byte, ts uint32, payload []byte) {
		if typ == 9 && len(payload) >= 2 && payload[0]>>4 == 1 && payload[1] == 1 {
			positions[ts] = int64(len(b))
		}
		b = append(b, testFLVTag(typ, ts, payload)...)
	}
	add(9, 0, []byte{0x17, 0x00, 0, 0, 0, 0})
	add(9, 1000, []byte{0x17, 0x01, 0, 0, 0, 0})
	add(8, 1000, []byte{0xaf, 0x00, 0x11, 0x90})
	add(9, 4000, []byte{0x17, 0x01, 0, 0, 0, 0})
	add(9, 4100, []byte{0x27, 0x01, 0, 0, 0, 0})
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	index, err := scanFLVIndex(path, int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	if index.DurationMS != 4100 {
		t.Fatalf("duration = %d, want 4100", index.DurationMS)
	}
	if !reflect.DeepEqual(index.TimesMS, []int64{1000, 4000}) {
		t.Fatalf("times = %v", index.TimesMS)
	}
	wantPositions := []int64{positions[1000], positions[4000]}
	if !reflect.DeepEqual(index.FilePositions, wantPositions) {
		t.Fatalf("positions = %v, want %v", index.FilePositions, wantPositions)
	}
}

func TestScanFLVIndexNoKeyframes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nokey.flv")
	b := []byte{'F', 'L', 'V', 1, 0x05, 0, 0, 0, 9, 0, 0, 0, 0}
	b = append(b, testFLVTag(8, 0, []byte{0xaf, 0x00, 0x11, 0x90})...)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := scanFLVIndex(path, int64(len(b))); err == nil {
		t.Fatal("expected ErrFLVIndexUnsupported")
	}
}
