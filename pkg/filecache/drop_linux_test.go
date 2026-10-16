//go:build linux

package filecache

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReleaseFilePageCache_TempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "release.bin")
	data := make([]byte, 128*1024)
	for i := range data {
		data[i] = byte(i)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ReleaseFilePageCache(path); err != nil {
		t.Fatalf("ReleaseFilePageCache: %v", err)
	}
}
