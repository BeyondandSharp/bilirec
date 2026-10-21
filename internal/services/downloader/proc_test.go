// SPDX-License-Identifier: GPL-3.0-only

package downloader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bilirec/bilirec/pkg/logger"
)

// writeFakeBin writes an executable shell script that stands in for a
// downloader binary. Tests using it are skipped on Windows.
func writeFakeBin(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake executables use /bin/sh")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "fake-bin")
	content := "#!/bin/sh\n" + script + "\n"
	if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFFmpegDownloaderOpenReadClose(t *testing.T) {
	fake := writeFakeBin(t, `printf 'FLV-HEADER-BYTES'`)
	d := NewFFmpegDownloader(Options{FFmpegBin: fake, StartTimeout: 3 * time.Second})

	rc, err := d.Open(context.Background(), Spec{URL: "https://cdn.example/live.flv", Format: "flv"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "FLV-HEADER-BYTES" {
		t.Fatalf("got %q, want FLV-HEADER-BYTES", data)
	}
	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}
	// Close must be idempotent.
	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := rc.Read(make([]byte, 1)); err == nil {
		t.Fatal("read after close must fail")
	}
}

func TestFFmpegDownloaderNonZeroExitBeforeFirstByte(t *testing.T) {
	fake := writeFakeBin(t, `exit 3`)
	d := NewFFmpegDownloader(Options{FFmpegBin: fake, StartTimeout: 3 * time.Second})

	_, err := d.Open(context.Background(), Spec{URL: "https://cdn.example/live.flv", Format: "flv"})
	if err == nil {
		t.Fatal("expected Open to fail when the process exits before producing output")
	}
	if !errors.Is(err, ErrNoFirstByte) {
		t.Fatalf("expected ErrNoFirstByte, got %v", err)
	}
	if errors.Is(err, ErrStartTimeout) {
		t.Fatalf("did not expect ErrStartTimeout, got %v", err)
	}
}

func TestFFmpegDownloaderStartTimeoutKillsProcess(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	fake := writeFakeBin(t, fmt.Sprintf("echo $$ > %q\nexec sleep 30", pidFile))
	d := NewFFmpegDownloader(Options{FFmpegBin: fake, StartTimeout: 200 * time.Millisecond})

	start := time.Now()
	_, err := d.Open(context.Background(), Spec{URL: "https://cdn.example/live.flv", Format: "flv"})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected Open to time out")
	}
	if !errors.Is(err, ErrStartTimeout) {
		t.Fatalf("expected ErrStartTimeout, got %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("start timeout took too long: %v", elapsed)
	}

	pid := waitPIDFile(t, pidFile)
	assertProcessGone(t, pid)
}

func TestFFmpegDownloaderCloseKillsProcessTree(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("/proc based process checks are linux-only")
	}
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "parent.pid")
	childFile := filepath.Join(dir, "child.pid")
	fake := writeFakeBin(t, fmt.Sprintf(`printf 'FLV'
sleep 30 &
echo $! > %q
echo $$ > %q
wait`, childFile, pidFile))

	d := NewFFmpegDownloader(Options{FFmpegBin: fake, StartTimeout: 3 * time.Second})
	rc, err := d.Open(context.Background(), Spec{URL: "https://cdn.example/live.flv", Format: "flv"})
	if err != nil {
		t.Fatal(err)
	}
	parentPID := waitPIDFile(t, pidFile)
	childPID := waitPIDFile(t, childFile)

	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}
	assertProcessGone(t, parentPID)
	assertProcessGone(t, childPID)
}

func TestStreamlinkDownloaderHybridPipeline(t *testing.T) {
	streamlink := writeFakeBin(t, `printf 'PAYLOAD-FROM-STREAMLINK'`)
	ffmpeg := writeFakeBin(t, `cat`)
	d := NewStreamlinkDownloader(Options{
		StreamlinkBin: streamlink,
		FFmpegBin:     ffmpeg,
		StartTimeout:  3 * time.Second,
	})

	rc, err := d.Open(context.Background(), Spec{URL: "https://cdn.example/live.flv", Format: "flv"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "PAYLOAD-FROM-STREAMLINK" {
		t.Fatalf("got %q, want PAYLOAD-FROM-STREAMLINK", data)
	}
	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestStreamlinkDownloaderSpawnFailure(t *testing.T) {
	d := NewStreamlinkDownloader(Options{
		StreamlinkBin: filepath.Join(t.TempDir(), "does-not-exist"),
		FFmpegBin:     "ffmpeg",
		StartTimeout:  200 * time.Millisecond,
	})
	_, err := d.Open(context.Background(), Spec{URL: "https://cdn.example/live.flv", Format: "flv"})
	if err == nil {
		t.Fatal("expected spawn error")
	}
	if errors.Is(err, ErrStartTimeout) {
		t.Fatalf("spawn failure must not be reported as start timeout: %v", err)
	}
}

func TestOpenHonorsContextCancel(t *testing.T) {
	fake := writeFakeBin(t, `exec sleep 30`)
	d := NewFFmpegDownloader(Options{FFmpegBin: fake, StartTimeout: 30 * time.Second})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := d.Open(ctx, Spec{URL: "https://cdn.example/live.flv", Format: "flv"})
		errCh <- err
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Open did not return after context cancellation")
	}
}

func TestLineWriterPrefixesStderr(t *testing.T) {
	var buf bytes.Buffer
	logger.SetOutput(&buf)
	t.Cleanup(func() { logger.SetOutput(os.Stderr) })

	w := newLineWriter("ffmpeg", logger.Named("test"))
	if _, err := w.Write([]byte("first line\nsecond ")); err != nil {
		t.Fatal(err)
	}
	w.flush()

	out := buf.String()
	if !strings.Contains(out, "[ffmpeg] first line") {
		t.Fatalf("missing prefixed line in output: %q", out)
	}
	if !strings.Contains(out, "[ffmpeg] second") {
		t.Fatalf("missing prefixed trailing line in output: %q", out)
	}
}

func waitPIDFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if parseErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("pid file %s was not written", path)
	return 0
}

func assertProcessGone(t *testing.T, pid int) {
	t.Helper()
	if runtime.GOOS != "linux" {
		return
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); os.IsNotExist(err) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process %d still exists after kill", pid)
}
