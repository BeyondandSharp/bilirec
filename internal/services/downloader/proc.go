// SPDX-License-Identifier: GPL-3.0-only

package downloader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/bilirec/bilirec/pkg/logger"
)

const (
	// maxStderrLineBytes bounds a single log line buffered from child stderr.
	maxStderrLineBytes = 64 * 1024
	// closeWaitTimeout bounds the wait for process reaping after a kill.
	closeWaitTimeout = 5 * time.Second
	// firstByteDrainTimeout bounds waiting for the stdout reader goroutine to
	// unblock after the process tree was killed.
	firstByteDrainTimeout = 3 * time.Second
)

// lineWriter splits a child's stderr into lines and logs each line with a
// stable `[ffmpeg]` / `[streamlink]` prefix. It never blocks and always
// reports the full write.
type lineWriter struct {
	prefix string
	logger logger.Logger

	mu  sync.Mutex
	buf []byte
}

func newLineWriter(prefix string, l logger.Logger) *lineWriter {
	return &lineWriter{prefix: prefix, logger: l}
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.buf = append(w.buf, p...)
	for {
		idx := bytes.IndexByte(w.buf, '\n')
		if idx < 0 {
			break
		}
		w.emit(w.buf[:idx])
		w.buf = w.buf[idx+1:]
	}
	if len(w.buf) > maxStderrLineBytes {
		w.emit(w.buf)
		w.buf = w.buf[:0]
	}
	return len(p), nil
}

// flush logs any unterminated trailing line. It is called after the process
// has been reaped so no more writes can race with it.
func (w *lineWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) > 0 {
		w.emit(w.buf)
		w.buf = w.buf[:0]
	}
}

func (w *lineWriter) emit(line []byte) {
	text := string(bytes.TrimRight(line, "\r"))
	if text == "" {
		return
	}
	switch w.prefix {
	case "ffmpeg":
		w.logger.Warnf("[ffmpeg] %s", text)
	default:
		w.logger.Infof("[%s] %s", w.prefix, text)
	}
}

// procSet owns the process tree of one download session. Children are started
// in their own process groups so Close can terminate the whole tree.
type procSet struct {
	cmds     []*exec.Cmd
	stdout   *os.File
	flushers []*lineWriter

	done      chan struct{}
	closeOnce sync.Once
	waitErr   error // valid after done is closed
}

func newProcSet(cmds []*exec.Cmd, stdout *os.File, flushers []*lineWriter) *procSet {
	p := &procSet{
		cmds:     cmds,
		stdout:   stdout,
		flushers: flushers,
		done:     make(chan struct{}),
	}
	go func() {
		var firstErr error
		for _, cmd := range cmds {
			if err := cmd.Wait(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		for _, w := range flushers {
			w.flush()
		}
		p.waitErr = firstErr
		close(p.done)
	}()
	return p
}

// Close kills the whole process tree and waits (bounded) for it to be reaped.
// It is idempotent and safe to call from the reader goroutine and from the
// context watcher concurrently.
func (p *procSet) Close() error {
	if p == nil {
		return nil
	}
	p.closeOnce.Do(func() {
		for _, cmd := range p.cmds {
			_ = killProcessTree(cmd)
		}
		timer := time.NewTimer(closeWaitTimeout)
		defer timer.Stop()
		select {
		case <-p.done:
		case <-timer.C:
			log.Warn("外部下载器进程未在超时内退出")
		}
		if p.stdout != nil {
			_ = p.stdout.Close()
		}
	})
	return nil
}

// exitError returns the first process wait error, waiting up to timeout for
// the processes to be reaped.
func (p *procSet) exitError(timeout time.Duration) error {
	if p == nil {
		return nil
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-p.done:
		return p.waitErr
	case <-timer.C:
		return nil
	}
}

// readResult is the outcome of the single-byte first read.
type readResult struct {
	n   int
	b   byte
	err error
}

// awaitFirstByte blocks until the process tree writes its first byte, exits,
// the deadline expires or ctx is cancelled. On failure the process tree is
// killed and reaped.
func (p *procSet) awaitFirstByte(ctx context.Context, timeout time.Duration) ([]byte, error) {
	resCh := make(chan readResult, 1)
	go func() {
		buf := make([]byte, 1)
		n, err := p.stdout.Read(buf)
		resCh <- readResult{n: n, b: buf[0], err: err}
	}()

	var timer *time.Timer
	var timeoutCh <-chan time.Time
	if timeout > 0 {
		timer = time.NewTimer(timeout)
		defer timer.Stop()
		timeoutCh = timer.C
	}

	select {
	case res := <-resCh:
		if res.n > 0 {
			return []byte{res.b}, nil
		}
		if res.err != nil && !errors.Is(res.err, io.EOF) {
			_ = p.Close()
			return nil, fmt.Errorf("%w：%v", ErrNoFirstByte, res.err)
		}
		_ = p.Close()
		if waitErr := p.exitError(time.Second); waitErr != nil {
			return nil, fmt.Errorf("%w：%v", ErrNoFirstByte, waitErr)
		}
		return nil, ErrNoFirstByte
	case <-timeoutCh:
		_ = p.Close()
		p.drainRead(resCh)
		return nil, fmt.Errorf("%w（%v）", ErrStartTimeout, timeout)
	case <-ctx.Done():
		_ = p.Close()
		p.drainRead(resCh)
		return nil, ctx.Err()
	}
}

// drainRead waits (bounded) for the stdout reader goroutine to unblock after
// the process tree has been killed, so it does not leak.
func (p *procSet) drainRead(resCh <-chan readResult) {
	timer := time.NewTimer(firstByteDrainTimeout)
	defer timer.Stop()
	select {
	case <-resCh:
	case <-timer.C:
	}
}

// watchContext kills the process tree when ctx is cancelled.
func watchContext(ctx context.Context, p *procSet) {
	if ctx == nil {
		return
	}
	go func() {
		select {
		case <-ctx.Done():
			_ = p.Close()
		case <-p.done:
		}
	}()
}

// startCmd starts one child process with prefixed stderr logging.
func startCmd(label, bin string, args []string, stdin io.Reader, stdout io.Writer, l logger.Logger) (*exec.Cmd, *lineWriter, error) {
	cmd := exec.Command(bin, args...)
	configureProcessGroup(cmd)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	lines := newLineWriter(label, l)
	cmd.Stderr = lines
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	return cmd, lines, nil
}

// prefixedReadCloser prepends the eagerly read first byte to the process
// stdout stream and kills the process tree on Close.
type prefixedReadCloser struct {
	prefix []byte
	proc   *procSet
}

func (r *prefixedReadCloser) Read(p []byte) (int, error) {
	if len(r.prefix) > 0 {
		n := copy(p, r.prefix)
		r.prefix = r.prefix[n:]
		return n, nil
	}
	return r.proc.stdout.Read(p)
}

func (r *prefixedReadCloser) Close() error {
	return r.proc.Close()
}
