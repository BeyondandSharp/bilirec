package processors

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/bilirec/bilirec/pkg/logger"
	"github.com/bilirec/bilirec/pkg/pool"
	"github.com/bilirec/bilirec/pkg/rw"
)

type stubFailWriter struct {
	err error
}

func (s *stubFailWriter) Write([]byte) (int, error) {
	return 0, s.err
}

func newFatalTestWriter(path string) *BufferedStreamWriterProcessor {
	w := &BufferedStreamWriterProcessor{
		path:                    path,
		bufferSize:              512,
		chanBufferSize:          4,
		skipSmallFlushThreshold: 512,
		skipSmallFlushThresholdSet: true,
		bytesPool:               pool.NewBucketedBytesPool(4096),
	}
	w.locker = &noOpLocker{}
	return w
}

func TestBufferedStreamWriter_PendingOverflowAbandonsSegment(t *testing.T) {
	tempDir := t.TempDir()
	missingDir := filepath.Join(tempDir, "missing")
	testFile := filepath.Join(missingDir, "out.flv")

	w := newFatalTestWriter(testFile)
	ctx := context.Background()
	if err := w.Open(ctx, logger.Nop()); err != nil {
		t.Fatalf("Open: %v", err)
	}

	w.pendingBytes = maxPendingBytesBeforeDrop
	chunk := w.bytesPool.GetSized(1)
	chunk[0] = 'x'
	w.handleDataChunk(chunk)

	if !errors.Is(w.fatalErr(), ErrPendingOverflow) {
		t.Fatalf("fatalErr = %v, want ErrPendingOverflow", w.fatalErr())
	}
	if w.pendingBytes != 0 || len(w.pendingChunks) != 0 {
		t.Fatalf("expected pending cleared, bytes=%d chunks=%d", w.pendingBytes, len(w.pendingChunks))
	}

	_, err := w.Process(ctx, logger.Nop(), []byte{1, 2, 3})
	if !errors.Is(err, ErrPendingOverflow) {
		t.Fatalf("Process err = %v, want ErrPendingOverflow", err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Fatalf("expected no output file, stat err=%v", err)
	}
}

func TestBufferedStreamWriter_WriteFailureAbandonsSegment(t *testing.T) {
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "out.flv")

	diskErr := errors.New("disk full")
	w := newFatalTestWriter(testFile)
	ctx := context.Background()
	if err := w.Open(ctx, logger.Nop()); err != nil {
		t.Fatalf("Open: %v", err)
	}

	file, err := os.Create(testFile)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	w.file.Store(file)
	w.writer = rw.NewFlushLockedBufferedWriter(&stubFailWriter{err: diskErr}, 8, &noOpLocker{})

	chunk := w.bytesPool.GetSized(64)
	w.handleDataChunk(chunk)

	if w.fatalErr() == nil {
		t.Fatal("expected fatal write error")
	}
	if errors.Is(w.fatalErr(), ErrPendingOverflow) {
		t.Fatalf("fatalErr should not be ErrPendingOverflow: %v", w.fatalErr())
	}
	if !errors.Is(w.fatalErr(), diskErr) {
		t.Fatalf("fatalErr = %v, want disk full", w.fatalErr())
	}

	_, err = w.Process(ctx, logger.Nop(), []byte{'a'})
	if err == nil || errors.Is(err, ErrPendingOverflow) {
		t.Fatalf("Process err = %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(testFile); err != nil {
		t.Fatalf("expected output file to exist after Close: %v", err)
	}
}

func TestBufferedStreamWriter_WriteFailureFlushesBufferedBeforeClose(t *testing.T) {
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "out.flv")

	w := newFatalTestWriter(testFile)
	ctx := context.Background()
	if err := w.Open(ctx, logger.Nop()); err != nil {
		t.Fatalf("Open: %v", err)
	}

	file, err := os.Create(testFile)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	backing := &failAfterWriter{w: file, failAfter: 1, err: errors.New("no space")}
	w.file.Store(file)
	w.writer = rw.NewFlushLockedBufferedWriter(backing, 32, &noOpLocker{})

	first := w.bytesPool.GetSized(16)
	w.handleDataChunk(first)
	if w.fatalErr() != nil {
		t.Fatalf("first chunk should succeed in buffer, fatal=%v", w.fatalErr())
	}

	second := w.bytesPool.GetSized(96)
	w.handleDataChunk(second)
	if w.fatalErr() == nil {
		t.Fatal("expected fatal after flush failure")
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	info, err := os.Stat(testFile)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("expected some bytes flushed to disk before fatal")
	}
}

type failAfterWriter struct {
	w         io.Writer
	failAfter int
	calls     int
	err       error
}

func (f *failAfterWriter) Write(p []byte) (int, error) {
	f.calls++
	if f.calls > f.failAfter {
		return 0, f.err
	}
	return f.w.Write(p)
}
