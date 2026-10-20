// SPDX-License-Identifier: GPL-3.0-or-later

// Package repair integrates the `flvrepair` sidecar into recording sessions.
//
// It implements docs/INTERFACES.md §2.1: WrapStream pumps the FLV byte channel
// through `flvrepair stream` (stdin -> stdout) and returns a channel of
// pool-owned buffers. Repair failures are never fatal: a sidecar death is
// retried once and then the rest of the session is passed through unchanged.
package repair

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/pkg/logger"
	"github.com/bilirec/bilirec/pkg/pool"
)

var log = logger.Named("repair")

const (
	// maxRestarts is the number of sidecar restarts allowed per session.
	maxRestarts = 1
	// shutdownTimeout bounds the graceful sidecar shutdown (close stdin, wait).
	shutdownTimeout = 5 * time.Second
	// killWaitTimeout bounds the wait after a forced kill.
	killWaitTimeout = 2 * time.Second
	// maxStderrLineBytes bounds a single buffered stderr line.
	maxStderrLineBytes = 64 * 1024

	defaultBin          = "flvrepair"
	defaultMode         = "best-effort"
	defaultMaxBufferMB  = 64
	defaultReadSize     = 512 * 1024
	defaultChanBufSize  = 16
	repairStateOn       = "on"
	repairStateDegraded = "degraded"
)

// Service owns the flvrepair binary resolution and spawns one sidecar per
// wrapped FLV session.
type Service struct {
	cfg         *config.Config
	log         logger.Logger
	binPath     string
	mode        string
	maxBufferMB int
	enabled     bool

	// roomID is set on the per-room view returned by WithRoom; it scopes the
	// shared live-state registry.
	roomID int
	states *stateRegistry
}

// stateRegistry tracks the live repair state per room ("on" while a sidecar
// session is active, "degraded" after repair was abandoned). It is shared by
// all WithRoom copies because they are shallow copies of the same Service.
type stateRegistry struct {
	m sync.Map // roomID int -> state string
}

func newStateRegistry() *stateRegistry { return &stateRegistry{} }

func (r *stateRegistry) set(roomID int, state string) {
	if r == nil || roomID <= 0 || state == "" {
		return
	}
	r.m.Store(roomID, state)
}

func (r *stateRegistry) clear(roomID int) {
	if r == nil || roomID <= 0 {
		return
	}
	r.m.Delete(roomID)
}

func (r *stateRegistry) get(roomID int) (string, bool) {
	if r == nil || roomID <= 0 {
		return "", false
	}
	value, ok := r.m.Load(roomID)
	if !ok {
		return "", false
	}
	state, _ := value.(string)
	return state, state != ""
}

// NewService resolves the sidecar binary once at startup (docs §2.3:
// FLV_REPAIR=auto is on iff flvrepair resolves).
func NewService(cfg *config.Config) *Service {
	s := &Service{cfg: cfg, log: log, states: newStateRegistry()}
	s.resolve(cfg)
	return s
}

func (s *Service) resolve(cfg *config.Config) {
	if cfg == nil {
		return
	}

	mode := strings.ToLower(strings.TrimSpace(cfg.FLVRepairMode))
	if mode != "strict" {
		mode = defaultMode
	}
	maxBufferMB := cfg.FLVRepairMaxBufferMB
	if maxBufferMB <= 0 {
		maxBufferMB = defaultMaxBufferMB
	}
	bin := strings.TrimSpace(cfg.FLVRepairBin)
	if bin == "" {
		bin = defaultBin
	}
	s.mode = mode
	s.maxBufferMB = maxBufferMB

	flag := strings.ToLower(strings.TrimSpace(cfg.FLVRepair))
	if flag == "off" {
		s.log.Infof("FLV 修复已关闭（FLV_REPAIR=off）")
		return
	}

	path, err := exec.LookPath(bin)
	if err != nil {
		if flag == "on" {
			s.log.Warnf("FLV_REPAIR=on 但未找到 flvrepair（%s），所有会话将直通不修复", bin)
		} else {
			s.log.Infof("未找到 flvrepair（%s），FLV_REPAIR=auto 关闭修复", bin)
		}
		return
	}
	s.binPath = path
	s.enabled = true
	s.log.Infof("FLV 修复已启用：bin=%s mode=%s max_buffer=%dMiB", path, mode, maxBufferMB)
}

// Enabled reports whether repair is active for new sessions.
func (s *Service) Enabled() bool {
	return s != nil && s.enabled
}

// WithRoom returns a shallow copy of the service whose logs carry the given
// room id, so every repair session log includes the room field required by
// docs/INTERFACES.md §2.4. The binary resolution and configuration are shared.
func (s *Service) WithRoom(roomID int) *Service {
	if s == nil {
		return nil
	}
	clone := *s
	clone.log = s.log.With("room", roomID)
	clone.roomID = roomID
	return &clone
}

// State reports the live repair state for a room: "on" while a sidecar session
// is active, "degraded" once repair was abandoned for the session, and
// ok=false when no repair session is active for the room. The recording status
// API uses this to expose the live state (docs follow-up T7).
func (s *Service) State(roomID int) (string, bool) {
	if s == nil {
		return "", false
	}
	return s.states.get(roomID)
}

// setState records the live state for this service's room (no-op without a
// room-scoped WithRoom view).
func (s *Service) setState(state string) {
	if s == nil {
		return
	}
	s.states.set(s.roomID, state)
}

// clearState forgets this service's room once its repair session ends.
func (s *Service) clearState() {
	if s == nil {
		return
	}
	s.states.clear(s.roomID)
}

// SessionOptions configures one repair session (docs/FEATURES-V2.md §5.1).
// Zero Mode/MaxBufferMB values mean "use the service defaults resolved from
// FLV_REPAIR_MODE / FLV_REPAIR_MAX_BUFFER_MB". WriteMetadata=true keeps
// script/onMetaData tags (the historical behavior); false passes
// `--write-metadata drop` to the sidecar.
type SessionOptions struct {
	Mode          string
	MaxBufferMB   int
	WriteMetadata bool
}

// sessionConfig is the resolved configuration for one sidecar generation.
type sessionConfig struct {
	mode          string
	maxBufferMB   int
	writeMetadata bool
}

// resolveSessionConfig applies per-session options over the service defaults.
// With no explicit options the historical behavior is kept (mode/max-buffer
// from config, metadata kept).
func (s *Service) resolveSessionConfig(opts []SessionOptions) sessionConfig {
	cfg := sessionConfig{
		mode:          s.mode,
		maxBufferMB:   s.maxBufferMB,
		writeMetadata: true,
	}
	if cfg.mode == "" {
		cfg.mode = defaultMode
	}
	if cfg.maxBufferMB <= 0 {
		cfg.maxBufferMB = defaultMaxBufferMB
	}
	if len(opts) == 0 {
		return cfg
	}
	opt := opts[0]
	if mode := strings.ToLower(strings.TrimSpace(opt.Mode)); mode != "" {
		if mode == "strict" {
			cfg.mode = "strict"
		} else {
			cfg.mode = defaultMode
		}
	}
	if opt.MaxBufferMB > 0 {
		cfg.maxBufferMB = opt.MaxBufferMB
	}
	cfg.writeMetadata = opt.WriteMetadata
	return cfg
}

// bufferPool is the subset of pool.BucketedBytesPool used by repair. It is an
// interface so tests can verify buffer lifecycle deterministically.
type bufferPool interface {
	GetSized(size int) []byte
	Put(buf []byte)
}

// WrapStream pumps ch through a `flvrepair stream` sidecar and returns a
// channel of buffers owned by chunkPool. When repair is disabled or
// unavailable, ch is returned unchanged.
//
// Input buffers are returned to chunkPool after they are written to the
// sidecar. Output buffers are taken from chunkPool; the consumer must return
// them (recorder.rev does). The returned channel is always closed when the
// pump finishes.
func (s *Service) WrapStream(ctx context.Context, ch <-chan []byte, chunkPool *pool.BucketedBytesPool, opts ...SessionOptions) (<-chan []byte, error) {
	if chunkPool == nil {
		return ch, nil
	}
	return s.wrapStream(ctx, ch, chunkPool, opts...)
}

func (s *Service) wrapStream(ctx context.Context, ch <-chan []byte, chunkPool bufferPool, opts ...SessionOptions) (<-chan []byte, error) {
	if !s.Enabled() || ch == nil {
		return ch, nil
	}
	cfg := s.resolveSessionConfig(opts)
	out := make(chan []byte, s.chanBufferSize())
	go s.run(ctx, ch, chunkPool, out, cfg)
	return out, nil
}

func (s *Service) readSize() int {
	if s.cfg != nil && s.cfg.ReadStreamBytesPoolSize > 0 {
		return s.cfg.ReadStreamBytesPoolSize
	}
	return defaultReadSize
}

func (s *Service) chanBufferSize() int {
	if s.cfg != nil && s.cfg.ReadStreamChanBufferSize > 0 {
		return s.cfg.ReadStreamChanBufferSize
	}
	return defaultChanBufSize
}

// sidecarArgs mirrors the frozen CLI (docs/INTERFACES.md §1.1 +
// docs/FEATURES-V2.md §5.1): `flvrepair stream [--mode ...]
// [--max-buffer ...] [--write-metadata drop]`. The keep default omits the flag
// so the default command line (and output) stays byte-identical.
func (s *Service) sidecarArgs(cfg sessionConfig) []string {
	args := []string{
		"stream",
		"--mode", cfg.mode,
		"--max-buffer", fmt.Sprintf("%dMiB", cfg.maxBufferMB),
	}
	if !cfg.writeMetadata {
		args = append(args, "--write-metadata", "drop")
	}
	return args
}

// sessionStats feeds the log fields required by docs §2.4.
type sessionStats struct {
	bytesIn  atomic.Int64
	bytesOut atomic.Int64
	restarts atomic.Int32
	degraded atomic.Bool
}

func (st *sessionStats) repairState() string {
	if st.degraded.Load() {
		return repairStateDegraded
	}
	return repairStateOn
}

// run owns one recording session's sidecar lifecycle.
func (s *Service) run(ctx context.Context, in <-chan []byte, chunkPool bufferPool, out chan<- []byte, cfg sessionConfig) {
	stats := &sessionStats{}
	defer func() {
		s.log.Infof("flvrepair stream end: repair=%s bytes_in=%d bytes_out=%d repair_restarts=%d",
			stats.repairState(),
			stats.bytesIn.Load(),
			stats.bytesOut.Load(),
			stats.restarts.Load(),
		)
		close(out)
	}()

	if ctx.Err() != nil {
		return
	}

	// Expose the live state to the recording status API for the lifetime of
	// this repair session.
	s.setState(repairStateOn)
	defer s.clearState()

	var pending []byte
	for {
		sc, err := s.startSidecar(cfg)
		if err != nil {
			s.log.Warnf("flvrepair 启动失败，本次会话剩余数据直通（repair=degraded）：%v", err)
			stats.degraded.Store(true)
			s.setState(repairStateDegraded)
			s.passThrough(ctx, in, chunkPool, out, pending, stats)
			return
		}

		outcome, carry := s.pumpSession(ctx, sc, in, chunkPool, out, pending, stats)
		pending = carry
		sc.Close()

		if outcome == outcomeDone || ctx.Err() != nil {
			return
		}
		if int(stats.restarts.Load()) >= maxRestarts {
			s.log.Warnf("flvrepair 侧车再次退出，本次会话剩余数据直通（repair=degraded）")
			stats.degraded.Store(true)
			s.setState(repairStateDegraded)
			s.passThrough(ctx, in, chunkPool, out, pending, stats)
			return
		}
		stats.restarts.Add(1)
		s.log.Warnf("flvrepair 侧车中途退出，正在重启（restart=%d/%d）", stats.restarts.Load(), maxRestarts)
	}
}

// passThrough forwards the remaining input buffers unchanged.
func (s *Service) passThrough(ctx context.Context, in <-chan []byte, chunkPool bufferPool, out chan<- []byte, pending []byte, stats *sessionStats) {
	if pending != nil {
		stats.bytesOut.Add(int64(len(pending)))
		select {
		case out <- pending:
		case <-ctx.Done():
			chunkPool.Put(pending)
			return
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case buf, ok := <-in:
			if !ok {
				return
			}
			stats.bytesIn.Add(int64(len(buf)))
			stats.bytesOut.Add(int64(len(buf)))
			select {
			case out <- buf:
			case <-ctx.Done():
				chunkPool.Put(buf)
				return
			}
		}
	}
}

// pumpOutcome classifies why a sidecar generation stopped.
type pumpOutcome int

const (
	outcomeDone pumpOutcome = iota // input exhausted (or ctx cancelled)
	outcomeDied                    // sidecar exited while input was still flowing
)

type inputResult struct {
	// carry is an input buffer that was not written at all; it must be handed
	// to the next sidecar generation (or pass-through) instead of being Put.
	carry []byte
	err   error
}

// pumpSession runs one sidecar generation. It returns the carry buffer (if the
// sidecar died before receiving a whole buffer) so data is not dropped
// gratuitously across a restart.
func (s *Service) pumpSession(
	ctx context.Context,
	sc *sidecar,
	in <-chan []byte,
	chunkPool bufferPool,
	out chan<- []byte,
	pending []byte,
	stats *sessionStats,
) (pumpOutcome, []byte) {
	inCh := make(chan inputResult, 1)
	outCh := make(chan error, 1)
	go func() { inCh <- s.writeInput(ctx, sc.stdin, in, chunkPool, pending, stats) }()
	go func() { outCh <- s.readOutput(ctx, sc.stdout, chunkPool, out, stats) }()

	var (
		inDone, outDone bool
		inRes           inputResult
		outErr          error
		earlyOut        bool

		drainTimer *time.Timer
		drainCh    <-chan time.Time
	)
	stopDrainTimer := func() {
		if drainTimer != nil {
			drainTimer.Stop()
			drainTimer = nil
			drainCh = nil
		}
	}
	defer stopDrainTimer()

	for !(inDone && outDone) {
		select {
		case inRes = <-inCh:
			inDone = true
			inCh = nil
		case outErr = <-outCh:
			outDone = true
			outCh = nil
			if !inDone {
				earlyOut = true
			}
		case <-ctx.Done():
			sc.kill()
			if !inDone {
				inRes = <-inCh
				inDone = true
			}
			if !outDone {
				<-outCh
				outDone = true
			}
			return outcomeDone, nil
		case <-drainCh:
			// stdin is closed but the sidecar did not exit in time: force it.
			sc.kill()
			if !outDone {
				outErr = <-outCh
				outDone = true
				outCh = nil
			}
			if !inDone {
				inRes = <-inCh
				inDone = true
				inCh = nil
			}
			return outcomeDone, nil
		}

		// Tear the other side down when a failure is observed, or start the
		// graceful-drain deadline once stdin is closed normally.
		switch {
		case inDone && !outDone:
			if inRes.err != nil {
				sc.kill()
				outErr = <-outCh
				outDone = true
				outCh = nil
			} else if drainTimer == nil {
				// stdin closed normally; give the sidecar 5s to flush stdout
				// before it is killed (docs/INTERFACES.md §2.1).
				drainTimer = time.NewTimer(shutdownTimeout)
				drainCh = drainTimer.C
			}
		case outDone && !inDone:
			// stdout finished (EOF or error) while input is still flowing:
			// the sidecar exited early, so unblock the writer.
			sc.kill()
			inRes = <-inCh
			inDone = true
			inCh = nil
		}
	}

	if earlyOut || inRes.err != nil || outErr != nil {
		return outcomeDied, inRes.carry
	}
	return outcomeDone, nil
}

// writeInput pumps the input channel into the sidecar stdin and returns input
// buffers to the pool after they have been written.
func (s *Service) writeInput(
	ctx context.Context,
	stdin io.WriteCloser,
	in <-chan []byte,
	chunkPool bufferPool,
	pending []byte,
	stats *sessionStats,
) inputResult {
	writeBuffer := func(buf []byte) inputResult {
		n, err := writeFull(stdin, buf)
		if err == nil || n > 0 {
			chunkPool.Put(buf)
			stats.bytesIn.Add(int64(len(buf)))
		}
		if err != nil {
			if n == 0 {
				// Nothing reached the sidecar: hand the buffer to the next
				// generation instead of dropping the part of the stream.
				return inputResult{carry: buf, err: err}
			}
			// Partially written: the prefix is gone with the dead sidecar.
			return inputResult{err: err}
		}
		return inputResult{}
	}

	if pending != nil {
		if res := writeBuffer(pending); res.err != nil {
			return res
		}
	}

	for {
		select {
		case <-ctx.Done():
			return inputResult{err: ctx.Err()}
		case buf, ok := <-in:
			if !ok {
				if stdin != nil {
					_ = stdin.Close()
				}
				return inputResult{}
			}
			if res := writeBuffer(buf); res.err != nil {
				return res
			}
		}
	}
}

// readOutput reads the sidecar stdout into pool-owned buffers and forwards
// them to out.
func (s *Service) readOutput(
	ctx context.Context,
	stdout io.Reader,
	chunkPool bufferPool,
	out chan<- []byte,
	stats *sessionStats,
) error {
	readSize := s.readSize()
	for {
		buf := chunkPool.GetSized(readSize)
		n, err := stdout.Read(buf)
		if n > 0 {
			stats.bytesOut.Add(int64(n))
			select {
			case out <- buf[:n]:
			case <-ctx.Done():
				chunkPool.Put(buf)
				return ctx.Err()
			}
		} else {
			chunkPool.Put(buf)
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func writeFull(w io.Writer, buf []byte) (int, error) {
	written := 0
	for len(buf) > 0 {
		n, err := w.Write(buf)
		written += n
		buf = buf[n:]
		if err != nil {
			return written, err
		}
		if n == 0 {
			return written, io.ErrShortWrite
		}
	}
	return written, nil
}

// sidecar is one spawned flvrepair process.
type sidecar struct {
	cmd       *exec.Cmd
	stdin     *os.File
	stdout    *os.File
	lines     *stderrWriter
	done      chan struct{}
	waitErr   error
	closeOnce sync.Once
}

func (s *Service) startSidecar(cfg sessionConfig) (*sidecar, error) {
	cmd := exec.Command(s.binPath, s.sidecarArgs(cfg)...)
	configureProcessGroup(cmd)

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("flvrepair：创建 stdin 管道失败：%w", err)
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		_ = stdinR.Close()
		_ = stdinW.Close()
		return nil, fmt.Errorf("flvrepair：创建 stdout 管道失败：%w", err)
	}

	lines := newStderrWriter(s.log)
	cmd.Stdin = stdinR
	cmd.Stdout = stdoutW
	cmd.Stderr = lines
	if err := cmd.Start(); err != nil {
		_ = stdinR.Close()
		_ = stdinW.Close()
		_ = stdoutR.Close()
		_ = stdoutW.Close()
		return nil, fmt.Errorf("启动 flvrepair 失败：%w", err)
	}
	// The child holds its own descriptors.
	_ = stdinR.Close()
	_ = stdoutW.Close()

	sc := &sidecar{
		cmd:    cmd,
		stdin:  stdinW,
		stdout: stdoutR,
		lines:  lines,
		done:   make(chan struct{}),
	}
	go func() {
		sc.waitErr = cmd.Wait()
		lines.flush()
		close(sc.done)
	}()
	return sc, nil
}

// kill force-terminates the whole sidecar process tree.
func (sc *sidecar) kill() {
	if sc == nil {
		return
	}
	_ = killProcessTree(sc.cmd)
	if sc.stdin != nil {
		_ = sc.stdin.Close()
	}
}

// Close gracefully stops the sidecar: close stdin, wait up to 5s, then kill.
func (sc *sidecar) Close() {
	if sc == nil {
		return
	}
	sc.closeOnce.Do(func() {
		if sc.stdin != nil {
			_ = sc.stdin.Close()
		}
		timer := time.NewTimer(shutdownTimeout)
		defer timer.Stop()
		select {
		case <-sc.done:
		case <-timer.C:
			log.Warn("flvrepair 未在 5 秒内退出，强制终止")
			_ = killProcessTree(sc.cmd)
			select {
			case <-sc.done:
			case <-time.After(killWaitTimeout):
				log.Warn("flvrepair 进程未能被回收")
			}
		}
		if sc.stdout != nil {
			_ = sc.stdout.Close()
		}
	})
}

// stderrWriter splits sidecar stderr into JSON lines and logs them with a
// [flvrepair] prefix. Non-JSON lines are logged as warnings.
type stderrWriter struct {
	log logger.Logger

	mu  sync.Mutex
	buf []byte
}

func newStderrWriter(l logger.Logger) *stderrWriter {
	return &stderrWriter{log: l}
}

func (w *stderrWriter) Write(p []byte) (int, error) {
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

// flush logs any unterminated trailing line after the child is reaped.
func (w *stderrWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) > 0 {
		w.emit(w.buf)
		w.buf = w.buf[:0]
	}
}

func (w *stderrWriter) emit(line []byte) {
	text := strings.TrimSpace(string(line))
	if text == "" {
		return
	}
	var event struct {
		Level string `json:"level"`
		Event string `json:"event"`
	}
	if err := json.Unmarshal(line, &event); err == nil && (event.Level != "" || event.Event != "") {
		switch strings.ToLower(event.Level) {
		case "error":
			w.log.Errorf("[flvrepair] %s", text)
		case "warn":
			w.log.Warnf("[flvrepair] %s", text)
		default:
			w.log.Infof("[flvrepair] %s", text)
		}
		return
	}
	w.log.Warnf("[flvrepair] %s", text)
}
