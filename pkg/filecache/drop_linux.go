//go:build linux

package filecache

import (
	"os"

	"golang.org/x/sys/unix"
)

// DropOpenFileCache advises the kernel to release page cache for an open file.
func DropOpenFileCache(f *os.File) error {
	if f == nil {
		return nil
	}
	return unix.Fadvise(int(f.Fd()), 0, 0, unix.FADV_DONTNEED)
}

// DropFilePageCache opens path read-only and drops its page cache.
func DropFilePageCache(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return DropOpenFileCache(f)
}

// ReleaseFilePageCache writes back dirty data pages, then advises the kernel
// to drop the page cache. FADV_DONTNEED leaves dirty pages resident.
func ReleaseFilePageCache(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := unix.SyncFileRange(int(f.Fd()), 0, 0, syncFileRangeIntegrity); err != nil {
		return err
	}
	return DropOpenFileCache(f)
}
