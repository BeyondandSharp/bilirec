// SPDX-License-Identifier: GPL-3.0-only

package repair

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/pkg/logger"
	"github.com/bilirec/bilirec/pkg/pool"
)

// writeFakeSidecar writes an executable shell script that stands in for the
// flvrepair binary. Sidecar tests are skipped on Windows.
func writeFakeSidecar(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake sidecars use /bin/sh")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "flvrepair")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func newTestService(bin string) *Service {
	return &Service{
		log:         logger.Nop(),
		binPath:     bin,
		mode:        "best-effort",
		maxBufferMB: 64,
		enabled:     true,
	}
}

// countingPool is a bufferPool test double that records buffer lifecycle.
type countingPool struct {
	mu   sync.Mutex
	gets int
	puts []int
}

func (p *countingPool) GetSized(size int) []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gets++
	return make([]byte, size)
}

func (p *countingPool) Put(buf []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.puts = append(p.puts, len(buf))
}

func (p *countingPool) putCountOfSize(size int) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, put := range p.puts {
		if put == size {
			n++
		}
	}
	return n
}

func (p *countingPool) getCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.gets
}

func readMarkerCount(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return n
}

func waitMarkerAtLeast(t *testing.T, path string, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if readMarkerCount(path) >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("marker %s did not reach %d (got %d)", path, want, readMarkerCount(path))
}

func TestSidecarArgs(t *testing.T) {
	s := newTestService("flvrepair")
	if got, want := strings.Join(s.sidecarArgs(s.resolveSessionConfig(nil)), " "), "stream --mode best-effort --max-buffer 64MiB"; got != want {
		t.Fatalf("sidecarArgs = %q, want %q", got, want)
	}
	s.mode = "strict"
	s.maxBufferMB = 16
	if got, want := strings.Join(s.sidecarArgs(s.resolveSessionConfig(nil)), " "), "stream --mode strict --max-buffer 16MiB"; got != want {
		t.Fatalf("sidecarArgs = %q, want %q", got, want)
	}
}

func TestSidecarArgsSessionOptions(t *testing.T) {
	s := newTestService("flvrepair")

	// Default/keep: no --write-metadata flag so behavior is byte-identical.
	keep := s.resolveSessionConfig([]SessionOptions{{WriteMetadata: true}})
	if got := strings.Join(s.sidecarArgs(keep), " "); strings.Contains(got, "--write-metadata") {
		t.Fatalf("keep must not pass --write-metadata: %q", got)
	}

	// Drop passes the flag explicitly.
	drop := s.resolveSessionConfig([]SessionOptions{{WriteMetadata: false}})
	if got, want := strings.Join(s.sidecarArgs(drop), " "), "stream --mode best-effort --max-buffer 64MiB --write-metadata drop"; got != want {
		t.Fatalf("drop sidecarArgs = %q, want %q", got, want)
	}

	// Per-session mode / max-buffer overrides.
	override := s.resolveSessionConfig([]SessionOptions{{Mode: "strict", MaxBufferMB: 8, WriteMetadata: true}})
	if got, want := strings.Join(s.sidecarArgs(override), " "), "stream --mode strict --max-buffer 8MiB"; got != want {
		t.Fatalf("override sidecarArgs = %q, want %q", got, want)
	}

	// No options keeps the historical default (metadata kept).
	noOpts := s.resolveSessionConfig(nil)
	if !noOpts.writeMetadata || noOpts.mode != "best-effort" || noOpts.maxBufferMB != 64 {
		t.Fatalf("unexpected default session config: %+v", noOpts)
	}
}

func TestWrapStreamSessionOptionsWriteMetadata(t *testing.T) {
	fake := writeFakeSidecar(t, `cat`)
	svc := newTestService(fake)

	in := make(chan []byte, 1)
	out, err := svc.WrapStream(context.Background(), in, pool.NewBucketedBytesPool(64*1024), SessionOptions{WriteMetadata: true})
	if err != nil {
		t.Fatal(err)
	}
	in <- []byte("x")
	close(in)
	for range out {
	}
}

func TestEnabledResolution(t *testing.T) {
	fake := writeFakeSidecar(t, `cat`)
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	cases := []struct {
		name string
		cfg  *config.Config
		want bool
	}{
		{name: "off", cfg: &config.Config{FLVRepair: "off", FLVRepairBin: fake}, want: false},
		{name: "auto missing binary", cfg: &config.Config{FLVRepair: "auto", FLVRepairBin: missing}, want: false},
		{name: "on missing binary", cfg: &config.Config{FLVRepair: "on", FLVRepairBin: missing}, want: false},
		{name: "on resolved binary", cfg: &config.Config{FLVRepair: "on", FLVRepairBin: fake}, want: true},
		{name: "auto resolved binary", cfg: &config.Config{FLVRepair: "auto", FLVRepairBin: fake}, want: true},
		{name: "empty flag is auto", cfg: &config.Config{FLVRepairBin: fake}, want: true},
		{name: "nil config", cfg: nil, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewService(tc.cfg)
			if got := svc.Enabled(); got != tc.want {
				t.Fatalf("Enabled() = %v, want %v", got, tc.want)
			}
		})
	}

	t.Run("defaults and overrides", func(t *testing.T) {
		svc := NewService(&config.Config{FLVRepair: "auto", FLVRepairBin: fake})
		if svc.mode != "best-effort" || svc.maxBufferMB != 64 {
			t.Fatalf("defaults = %q/%d", svc.mode, svc.maxBufferMB)
		}
		if svc.binPath != fake {
			t.Fatalf("binPath = %q, want %q", svc.binPath, fake)
		}
		svc = NewService(&config.Config{
			FLVRepair:            "auto",
			FLVRepairBin:         fake,
			FLVRepairMode:        "strict",
			FLVRepairMaxBufferMB: 16,
		})
		if svc.mode != "strict" || svc.maxBufferMB != 16 {
			t.Fatalf("overrides = %q/%d", svc.mode, svc.maxBufferMB)
		}
	})
}

func TestWrapStreamDisabledReturnsInputChannel(t *testing.T) {
	in := make(chan []byte)
	svc := &Service{enabled: false, log: logger.Nop()}
	out, err := svc.WrapStream(context.Background(), in, pool.NewBucketedBytesPool(64*1024))
	if err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatal("disabled repair must return the input channel unchanged")
	}
}

func TestWrapStreamNilPoolReturnsInputChannel(t *testing.T) {
	in := make(chan []byte)
	svc := newTestService("flvrepair")
	out, err := svc.WrapStream(context.Background(), in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatal("nil chunk pool must return the input channel unchanged")
	}
}

func TestWrapStreamPipesAndReturnsInputBuffers(t *testing.T) {
	fake := writeFakeSidecar(t, `cat`)
	svc := newTestService(fake)
	chunkPool := &countingPool{}

	in := make(chan []byte, 8)
	out, err := svc.wrapStream(context.Background(), in, chunkPool)
	if err != nil {
		t.Fatal(err)
	}

	chunks := [][]byte{
		[]byte("chunk-one"),
		[]byte("chunk-two-longer"),
		[]byte("c3"),
	}
	go func() {
		for _, chunk := range chunks {
			in <- chunk
		}
		close(in)
	}()

	var got []byte
	for buf := range out {
		got = append(got, buf...)
		chunkPool.Put(buf) // recorder.rev returns output buffers to the pool
	}
	want := bytes.Join(chunks, nil)
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for _, chunk := range chunks {
		if n := chunkPool.putCountOfSize(len(chunk)); n != 1 {
			t.Fatalf("input buffer of %d bytes was Put %d times, want 1", len(chunk), n)
		}
	}
	if chunkPool.getCount() == 0 {
		t.Fatal("expected output buffers to come from the chunk pool")
	}
}

func TestWrapStreamStartFailurePassesThrough(t *testing.T) {
	svc := newTestService(filepath.Join(t.TempDir(), "missing-flvrepair"))
	chunkPool := &countingPool{}
	in := make(chan []byte, 8)
	out, err := svc.wrapStream(context.Background(), in, chunkPool)
	if err != nil {
		t.Fatal(err)
	}

	chunks := [][]byte{[]byte("alpha"), []byte("beta")}
	go func() {
		for _, chunk := range chunks {
			in <- chunk
		}
		close(in)
	}()

	var got []byte
	for buf := range out {
		got = append(got, buf...)
		chunkPool.Put(buf)
	}
	if want := bytes.Join(chunks, nil); !bytes.Equal(got, want) {
		t.Fatalf("pass-through got %q, want %q", got, want)
	}
}

func TestWrapStreamRestartsOnceAndContinues(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "runs")
	// First invocation consumes one byte and dies; later invocations pass
	// data through like a healthy sidecar.
	fake := writeFakeSidecar(t, fmt.Sprintf(`marker=%q
n=0
if [ -f "$marker" ]; then n=$(cat "$marker"); fi
n=$((n+1))
echo "$n" > "$marker"
if [ "$n" -eq 1 ]; then
  dd bs=1 count=1 2>/dev/null
  exit 1
fi
cat`, marker))
	svc := newTestService(fake)
	chunkPool := &countingPool{}

	in := make(chan []byte, 64)
	out, err := svc.wrapStream(context.Background(), in, chunkPool)
	if err != nil {
		t.Fatal(err)
	}

	in <- []byte("first-chunk")
	waitMarkerAtLeast(t, marker, 1)

	finalChunk := []byte("FINAL-CHUNK-PAYLOAD")
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.After(15 * time.Second)
	var got []byte
	sentFinal := false
	closed := false
	for !closed {
		select {
		case buf, ok := <-out:
			if !ok {
				closed = true
				continue
			}
			got = append(got, buf...)
			chunkPool.Put(buf)
		case <-ticker.C:
			if sentFinal {
				continue
			}
			if readMarkerCount(marker) >= 2 {
				in <- finalChunk
				sentFinal = true
				close(in)
				continue
			}
			select {
			case in <- []byte("keepalive"):
			default:
			}
		case <-deadline:
			t.Fatal("timed out waiting for wrap to finish")
		}
	}

	if !sentFinal {
		t.Fatal("sidecar was never restarted")
	}
	if !bytes.Contains(got, finalChunk) {
		t.Fatalf("restarted sidecar did not forward the final chunk (got %d bytes)", len(got))
	}
	if runs := readMarkerCount(marker); runs != 2 {
		t.Fatalf("sidecar ran %d times, want 2", runs)
	}
}

func TestWrapStreamDegradesAfterSecondDeath(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "runs")
	// Every invocation consumes one byte and dies without output.
	fake := writeFakeSidecar(t, fmt.Sprintf(`marker=%q
n=0
if [ -f "$marker" ]; then n=$(cat "$marker"); fi
n=$((n+1))
echo "$n" > "$marker"
dd bs=1 count=1 2>/dev/null
exit 1`, marker))
	svc := newTestService(fake)
	chunkPool := &countingPool{}

	in := make(chan []byte, 64)
	out, err := svc.wrapStream(context.Background(), in, chunkPool)
	if err != nil {
		t.Fatal(err)
	}

	in <- []byte("first")
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.After(20 * time.Second)

	sentinel := []byte("SENTINEL-PASSTHROUGH-PAYLOAD")
	seenSentinel := false
	i := 0
	closed := false
	for !closed {
		select {
		case buf, ok := <-out:
			if !ok {
				closed = true
				continue
			}
			if bytes.Contains(buf, sentinel) {
				seenSentinel = true
			}
			chunkPool.Put(buf)
		case <-ticker.C:
			if seenSentinel {
				close(in)
				continue
			}
			i++
			payload := []byte(fmt.Sprintf("chunk-%d", i))
			if i > 20 {
				// Past the second death the session is in pass-through mode:
				// a sentinel must arrive verbatim.
				payload = sentinel
			}
			select {
			case in <- payload:
			default:
			}
		case <-deadline:
			t.Fatal("timed out waiting for degraded pass-through")
		}
	}
	if !seenSentinel {
		t.Fatal("degraded pass-through did not forward a sentinel buffer")
	}
	if runs := readMarkerCount(marker); runs < 2 {
		t.Fatalf("sidecar ran %d times, want at least 2", runs)
	}
}

func TestWrapStreamCancellationStopsSidecar(t *testing.T) {
	fake := writeFakeSidecar(t, `cat`)
	svc := newTestService(fake)
	chunkPool := &countingPool{}

	ctx, cancel := context.WithCancel(context.Background())
	in := make(chan []byte, 8)
	out, err := svc.wrapStream(ctx, in, chunkPool)
	if err != nil {
		t.Fatal(err)
	}

	cancel()

	select {
	case _, ok := <-out:
		if ok {
			for range out {
			}
		}
	case <-time.After(6 * time.Second):
		t.Fatal("wrapped channel did not close after cancellation")
	}
	close(in)
}

func TestStderrJSONIsDrained(t *testing.T) {
	var buf bytes.Buffer
	logger.SetOutput(&buf)
	t.Cleanup(func() { logger.SetOutput(os.Stderr) })

	fake := writeFakeSidecar(t, `printf '{"level":"warn","event":"timestamp_jump","count":1,"class":"R1"}\n' >&2
cat`)
	svc := newTestService(fake)
	svc.log = logger.Named("repair-test")
	chunkPool := &countingPool{}

	in := make(chan []byte, 4)
	out, err := svc.wrapStream(context.Background(), in, chunkPool)
	if err != nil {
		t.Fatal(err)
	}
	in <- []byte("x")
	close(in)
	for range out {
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		text := buf.String()
		if strings.Contains(text, "[flvrepair]") && strings.Contains(text, "timestamp_jump") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("sidecar stderr JSON was not logged with a prefix: %q", buf.String())
}

func TestWithRoom(t *testing.T) {
	var nilSvc *Service
	if nilSvc.WithRoom(1) != nil {
		t.Fatal("WithRoom on nil service must return nil")
	}

	fake := writeFakeSidecar(t, `cat`)
	svc := newTestService(fake)
	svc.log = logger.Named("repair-test")
	roomSvc := svc.WithRoom(42)
	if roomSvc == nil || roomSvc == svc {
		t.Fatal("WithRoom must return a shallow copy")
	}
	if !roomSvc.Enabled() || !svc.Enabled() {
		t.Fatal("WithRoom must preserve enablement")
	}

	var buf bytes.Buffer
	logger.SetOutput(&buf)
	t.Cleanup(func() { logger.SetOutput(os.Stderr) })

	in := make(chan []byte, 1)
	out, err := roomSvc.wrapStream(context.Background(), in, &countingPool{})
	if err != nil {
		t.Fatal(err)
	}
	in <- []byte("x")
	close(in)
	for range out {
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), "room=42") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("repair session logs do not carry the room field: %q", buf.String())
}

func TestWrapStreamShutdownClosesStdinThenKills(t *testing.T) {
	if testing.Short() {
		// Still runs under -short; the 5s shutdown wait is the behavior under test.
	}
	pidFile := filepath.Join(t.TempDir(), "pid")
	// Sidecar ignores stdin EOF entirely; Close must wait 5s and then kill it.
	fake := writeFakeSidecar(t, fmt.Sprintf("echo $$ > %q\nexec sleep 30", pidFile))
	svc := newTestService(fake)
	chunkPool := &countingPool{}

	in := make(chan []byte, 1)
	out, err := svc.wrapStream(context.Background(), in, chunkPool)
	if err != nil {
		t.Fatal(err)
	}
	in <- []byte("x")
	close(in)

	start := time.Now()
	for range out {
	}
	elapsed := time.Since(start)
	if elapsed < 4*time.Second {
		t.Fatalf("sidecar was killed too early (%v); stdin EOF must be tried first", elapsed)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("sidecar shutdown took too long: %v", elapsed)
	}

	pid := waitPIDFile(t, pidFile)
	assertNoProcess(t, pid)
}

func waitPIDFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			if pid, convErr := strconv.Atoi(strings.TrimSpace(string(data))); convErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("pid file %s was not written", path)
	return 0
}

func assertNoProcess(t *testing.T, pid int) {
	t.Helper()
	if runtime.GOOS != "linux" {
		return
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); os.IsNotExist(err) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("sidecar process %d still exists after shutdown", pid)
}

// newStatefulTestService is newTestService with the per-room state registry
// initialised (the production service is created through NewService).
func newStatefulTestService(bin string) *Service {
	svc := newTestService(bin)
	svc.states = newStateRegistry()
	return svc
}

func waitRepairState(t *testing.T, svc *Service, roomID int, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if state, ok := svc.State(roomID); ok && state == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	state, ok := svc.State(roomID)
	t.Fatalf("repair state for room %d = %q (ok=%v), want %q", roomID, state, ok, want)
}

func waitRepairStateCleared(t *testing.T, svc *Service, roomID int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := svc.State(roomID); !ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("repair state for room %d was not cleared", roomID)
}

func TestStateRegistryLifecycle(t *testing.T) {
	fake := writeFakeSidecar(t, `cat`)
	svc := newStatefulTestService(fake)
	const roomID = 101

	if _, ok := svc.State(roomID); ok {
		t.Fatal("no repair state expected before a session starts")
	}

	in := make(chan []byte, 8)
	out, err := svc.WithRoom(roomID).wrapStream(context.Background(), in, &countingPool{})
	if err != nil {
		t.Fatal(err)
	}
	// The WithRoom copy wrote the state into the shared registry.
	waitRepairState(t, svc, roomID, repairStateOn)

	in <- []byte("x")
	close(in)
	for range out {
	}

	waitRepairStateCleared(t, svc, roomID)

	// The base service (roomID 0) never registers state.
	if _, ok := svc.State(0); ok {
		t.Fatal("roomID 0 must not be registered")
	}
	var nilSvc *Service
	if _, ok := nilSvc.State(roomID); ok {
		t.Fatal("nil service must report no state")
	}
}

func TestStateRegistryDegraded(t *testing.T) {
	fake := writeFakeSidecar(t, `dd bs=1 count=1 2>/dev/null
exit 1`)
	svc := newStatefulTestService(fake)
	const roomID = 102

	in := make(chan []byte, 64)
	out, err := svc.WithRoom(roomID).wrapStream(context.Background(), in, &countingPool{})
	if err != nil {
		t.Fatal(err)
	}

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.After(15 * time.Second)
	for {
		select {
		case _, ok := <-out:
			if ok {
				continue
			}
			t.Fatal("wrapped channel closed before repair degraded")
		case <-ticker.C:
			select {
			case in <- []byte("chunk"):
			default:
			}
			if state, ok := svc.State(roomID); ok && state == repairStateDegraded {
				close(in)
				for range out {
				}
				waitRepairStateCleared(t, svc, roomID)
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for degraded repair state")
		}
	}
}

func TestStateRegistryConcurrentAccess(t *testing.T) {
	// Exercises State/setState/clearState concurrency under -race: the status
	// API reads State from HTTP goroutines while the pump goroutine updates it.
	fake := writeFakeSidecar(t, `cat`)
	svc := newStatefulTestService(fake)
	const roomID = 103

	in := make(chan []byte, 64)
	out, err := svc.WithRoom(roomID).wrapStream(context.Background(), in, &countingPool{})
	if err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				_, _ = svc.State(roomID)
			}
		}
	}()

	for i := 0; i < 20; i++ {
		in <- []byte("chunk")
	}
	close(in)
	for range out {
	}
	close(stop)
	<-done

	waitRepairStateCleared(t, svc, roomID)
}
