// SPDX-License-Identifier: GPL-3.0-or-later

package stream

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/bilirec/bilirec/pkg/pool"
)

type externalCloseTracker struct {
	io.ReadCloser
	mu     sync.Mutex
	closed bool
}

func (c *externalCloseTracker) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return c.ReadCloser.Close()
}

func (c *externalCloseTracker) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func TestReadExternalStreamDeliversPayload(t *testing.T) {
	cfg := initStreamTestConfig(t)
	svc := NewService(cfg)

	const qn = 10000
	chunkPool, release := svc.AcquireChunkPool(qn)
	var releaseOnce sync.Once
	released := make(chan struct{})
	releasePool := func() { releaseOnce.Do(func() { close(released); release() }) }

	payload := bytes.Repeat([]byte{0xAB, 0xCD}, 150*1024)
	rc := io.NopCloser(bytes.NewReader(payload))
	ch := svc.ReadExternalStream(rc, context.Background(), qn, chunkPool, releasePool)

	var got []byte
	for data := range ch {
		got = append(got, data...)
		svc.putChunk(chunkPool, data)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: got %d bytes, want %d", len(got), len(payload))
	}

	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatal("releasePool was not called after the stream ended")
	}
}

func TestReadExternalStreamIdleTimeoutClosesReader(t *testing.T) {
	svc := &Service{
		streamIdleTimeout: 50 * time.Millisecond,
		chanBufferSize:    2,
	}
	chunkPool := pool.NewBucketedBytesPool(64 * 1024)
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	rc := &externalCloseTracker{ReadCloser: pr}

	var releaseOnce sync.Once
	released := make(chan struct{})
	releasePool := func() { releaseOnce.Do(func() { close(released) }) }

	ch := svc.ReadExternalStream(rc, context.Background(), 10000, chunkPool, releasePool)

	select {
	case _, ok := <-ch:
		if ok {
			// A single empty read is fine; drain until the channel closes.
			for range ch {
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("channel did not close after idle timeout")
	}

	deadline := time.Now().Add(2 * time.Second)
	for !rc.isClosed() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !rc.isClosed() {
		t.Fatal("idle timeout did not close the external reader")
	}
	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatal("releasePool was not called after idle timeout")
	}
}

func TestReadExternalStreamCancelledContext(t *testing.T) {
	svc := &Service{streamIdleTimeout: time.Second, chanBufferSize: 2}
	chunkPool := pool.NewBucketedBytesPool(64 * 1024)
	rc := &externalCloseTracker{ReadCloser: io.NopCloser(bytes.NewReader([]byte("data")))}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var releaseOnce sync.Once
	released := make(chan struct{})
	ch := svc.ReadExternalStream(rc, ctx, 10000, chunkPool, func() {
		releaseOnce.Do(func() { close(released) })
	})

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected no data from an already-cancelled context")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("channel did not close on cancelled context")
	}
	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatal("releasePool was not called on cancellation")
	}
	if !rc.isClosed() {
		t.Fatal("external reader was not closed on cancellation")
	}
}
