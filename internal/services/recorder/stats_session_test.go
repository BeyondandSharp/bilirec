package recorder

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/services/repair"
	"github.com/bilirec/bilirec/pkg/pool"
	"github.com/puzpuzpuz/xsync/v4"
)

func writeFakeStatsRepairBin(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake sidecars use /bin/sh")
	}
	p := filepath.Join(t.TempDir(), "flvrepair")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func newStatsTestRecorder() *Service {
	return &Service{recording: xsync.NewMap[int, *Info]()}
}

func TestGetStatsSessionPipelineFields(t *testing.T) {
	r := newStatsTestRecorder()
	info := &Info{startTime: time.Now(), room: &bilibili.LiveRoomInfoDetail{Title: "标题"}}
	info.SetStream(10000, false, "flv")
	info.SetSessionSource("ffmpeg", "ts", "on")
	r.recording.Store(42, info)

	stats, ok := r.GetStats(42)
	if !ok {
		t.Fatal("expected stats for recording room")
	}
	if stats.Downloader != "ffmpeg" {
		t.Fatalf("downloader = %q, want ffmpeg", stats.Downloader)
	}
	if stats.SourceFormat != "ts" {
		t.Fatalf("source_format = %q, want ts", stats.SourceFormat)
	}
	if stats.Repair != "on" {
		t.Fatalf("repair = %q, want on", stats.Repair)
	}
	// The recorded format stays FLV for external downloaders.
	if stats.ActualStreamFormat != "flv" {
		t.Fatalf("actual_stream_format = %q, want flv", stats.ActualStreamFormat)
	}
}

func TestGetStatsSessionPipelineFieldsDefaults(t *testing.T) {
	r := newStatsTestRecorder()
	info := &Info{startTime: time.Now()}
	r.recording.Store(7, info)

	stats, ok := r.GetStats(7)
	if !ok {
		t.Fatal("expected stats")
	}
	if stats.Downloader != "" || stats.SourceFormat != "" {
		t.Fatalf("unset session source should stay empty: downloader=%q source=%q", stats.Downloader, stats.SourceFormat)
	}
	if stats.Repair != "off" {
		t.Fatalf("repair = %q, want off without repair service", stats.Repair)
	}
}

func TestGetStatsWithNilRepairServiceDoesNotPanic(t *testing.T) {
	r := newStatsTestRecorder()
	info := &Info{startTime: time.Now()}
	info.SetSessionSource("native", "fmp4", "off")
	r.recording.Store(9, info)

	stats, ok := r.GetStats(9)
	if !ok {
		t.Fatal("expected stats")
	}
	if stats.Downloader != "native" || stats.SourceFormat != "fmp4" || stats.Repair != "off" {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestGetStatsReportsLiveRepairState(t *testing.T) {
	// A sidecar that reads one byte and dies every time. It starts as "on",
	// restarts once, then degrades; GetStats must report the live state.
	bin := writeFakeStatsRepairBin(t, `dd bs=1 count=1 2>/dev/null
exit 1`)
	repairSvc := repair.NewService(&config.Config{
		FLVRepair:            "on",
		FLVRepairBin:         bin,
		FLVRepairMode:        "best-effort",
		FLVRepairMaxBufferMB: 16,
	})
	if !repairSvc.Enabled() {
		t.Fatal("expected repair service to be enabled")
	}

	const roomID = 555
	in := make(chan []byte, 64)
	out, err := repairSvc.WithRoom(roomID).WrapStream(context.Background(), in, pool.NewBucketedBytesPool(64*1024))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		close(in)
		for range out {
		}
	}()

	r := newStatsTestRecorder()
	info := &Info{startTime: time.Now(), room: &bilibili.LiveRoomInfoDetail{Title: "repair"}}
	info.SetSessionSource("native", "flv", "on")
	r.recording.Store(roomID, info)
	r.repair = repairSvc

	waitState := func(want string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if state, ok := repairSvc.State(roomID); ok && state == want {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		state, ok := repairSvc.State(roomID)
		t.Fatalf("repair state = %q (ok=%v), want %q", state, ok, want)
	}

	waitState("on")
	stats, ok := r.GetStats(roomID)
	if !ok {
		t.Fatal("expected stats")
	}
	if stats.Repair != "on" {
		t.Fatalf("live repair = %q, want on", stats.Repair)
	}

	// Drive the sidecar to its second death so repair degrades.
	deadline := time.After(15 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			select {
			case in <- []byte("chunk"):
			default:
			}
			if state, ok := repairSvc.State(roomID); ok && state == "degraded" {
				goto degraded
			}
		case <-deadline:
			t.Fatal("timed out waiting for degraded repair state")
		}
	}

degraded:
	stats, ok = r.GetStats(roomID)
	if !ok {
		t.Fatal("expected stats")
	}
	if stats.Repair != "degraded" {
		t.Fatalf("live repair = %q, want degraded", stats.Repair)
	}
	if !strings.Contains(info.GetSessionRepair(), "on") {
		t.Fatalf("start-time repair state should stay on, got %q", info.GetSessionRepair())
	}
}

func TestStatsJSONIncludesSessionPipelineFields(t *testing.T) {
	r := newStatsTestRecorder()
	info := &Info{startTime: time.Now()}
	info.SetStream(20000, false, "flv")
	info.SetSessionSource("streamlink", "fmp4", "on")
	r.recording.Store(11, info)

	stats, ok := r.GetStats(11)
	if !ok {
		t.Fatal("expected stats")
	}
	raw, err := json.Marshal(stats)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["downloader"] != "streamlink" {
		t.Fatalf("downloader = %v, want streamlink (%s)", decoded["downloader"], raw)
	}
	if decoded["source_format"] != "fmp4" {
		t.Fatalf("source_format = %v, want fmp4 (%s)", decoded["source_format"], raw)
	}
	if decoded["repair"] != "on" {
		t.Fatalf("repair = %v, want on (%s)", decoded["repair"], raw)
	}
	if decoded["actual_stream_format"] != "flv" {
		t.Fatalf("actual_stream_format = %v, want flv (%s)", decoded["actual_stream_format"], raw)
	}
}

func TestStatsJSONOmitsUnsetSessionFields(t *testing.T) {
	r := newStatsTestRecorder()
	info := &Info{startTime: time.Now()}
	r.recording.Store(12, info)

	stats, ok := r.GetStats(12)
	if !ok {
		t.Fatal("expected stats")
	}
	raw, err := json.Marshal(stats)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, exists := decoded["downloader"]; exists {
		t.Fatalf("unset downloader must be omitted: %s", raw)
	}
	if _, exists := decoded["source_format"]; exists {
		t.Fatalf("unset source_format must be omitted: %s", raw)
	}
	// repair always has a value (off) so clients do not need a default.
	if decoded["repair"] != "off" {
		t.Fatalf("repair = %v, want off (%s)", decoded["repair"], raw)
	}
}
