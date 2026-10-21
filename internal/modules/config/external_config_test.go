// SPDX-License-Identifier: GPL-3.0-only

package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bilirec/bilirec/pkg/logger"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

func TestParseDownloader(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"", "auto"},
		{"auto", "auto"},
		{"native", "native"},
		{"ffmpeg", "ffmpeg"},
		{" STREAMLINK ", "streamlink"},
		{"FFmpeg", "ffmpeg"},
		{"unknown", "auto"},
		{"youtube-dl", "auto"},
	}
	for _, tt := range tests {
		if got := parseDownloader(tt.raw); got != tt.want {
			t.Fatalf("parseDownloader(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestParseFLVRepair(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"", "auto"},
		{"auto", "auto"},
		{"on", "on"},
		{"ON", "on"},
		{"off", "off"},
		{" yes ", "auto"},
	}
	for _, tt := range tests {
		if got := parseFLVRepair(tt.raw); got != tt.want {
			t.Fatalf("parseFLVRepair(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestParseFLVRepairMode(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"", "best-effort"},
		{"best-effort", "best-effort"},
		{"strict", "strict"},
		{" STRICT ", "strict"},
		{"unknown", "best-effort"},
	}
	for _, tt := range tests {
		if got := parseFLVRepairMode(tt.raw); got != tt.want {
			t.Fatalf("parseFLVRepairMode(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func clearExternalEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"DOWNLOADER",
		"DOWNLOADER_FALLBACK",
		"FFMPEG_BIN",
		"STREAMLINK_BIN",
		"EXTERNAL_START_TIMEOUT_SECS",
		"FLV_REPAIR",
		"FLV_REPAIR_BIN",
		"FLV_REPAIR_MODE",
		"FLV_REPAIR_MAX_BUFFER_MB",
		"TITLE_POLL_SECS",
	} {
		t.Setenv(key, "")
	}
}

func setConfigTestDirs(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("OUTPUT_DIR", filepath.Join(dir, "records"))
	t.Setenv("DATABASE_DIR", filepath.Join(dir, "database"))
	t.Setenv("SECRET_DIR", filepath.Join(dir, "secrets"))
	t.Setenv("BILIBILI_LOGIN_MODE", "anonymous")
}

func loadTestConfig(t *testing.T) *Config {
	t.Helper()
	var cfg *Config
	app := fxtest.New(t, Module, fx.Populate(&cfg))
	app.RequireStart()
	t.Cleanup(app.RequireStop)
	return cfg
}

func TestExternalConfigDefaults(t *testing.T) {
	clearExternalEnv(t)
	setConfigTestDirs(t)
	cfg := loadTestConfig(t)

	if cfg.Downloader != "auto" {
		t.Fatalf("DOWNLOADER default = %q, want auto", cfg.Downloader)
	}
	if len(cfg.DownloaderFallback) != 1 || cfg.DownloaderFallback[0] != "native" {
		t.Fatalf("DOWNLOADER_FALLBACK default = %v, want [native]", cfg.DownloaderFallback)
	}
	if cfg.FFmpegBin != "ffmpeg" {
		t.Fatalf("FFMPEG_BIN default = %q, want ffmpeg", cfg.FFmpegBin)
	}
	if cfg.StreamlinkBin != "streamlink" {
		t.Fatalf("STREAMLINK_BIN default = %q, want streamlink", cfg.StreamlinkBin)
	}
	if cfg.ExternalStartTimeoutSecs != 20 {
		t.Fatalf("EXTERNAL_START_TIMEOUT_SECS default = %d, want 20", cfg.ExternalStartTimeoutSecs)
	}
	if cfg.FLVRepair != "auto" {
		t.Fatalf("FLV_REPAIR default = %q, want auto", cfg.FLVRepair)
	}
	if cfg.FLVRepairBin != "flvrepair" {
		t.Fatalf("FLV_REPAIR_BIN default = %q, want flvrepair", cfg.FLVRepairBin)
	}
	if cfg.FLVRepairMode != "best-effort" {
		t.Fatalf("FLV_REPAIR_MODE default = %q, want best-effort", cfg.FLVRepairMode)
	}
	if cfg.FLVRepairMaxBufferMB != 64 {
		t.Fatalf("FLV_REPAIR_MAX_BUFFER_MB default = %d, want 64", cfg.FLVRepairMaxBufferMB)
	}
	if cfg.TitlePollSecs != 20 {
		t.Fatalf("TITLE_POLL_SECS default = %d, want 20", cfg.TitlePollSecs)
	}
}

func TestExternalConfigEnvOverrides(t *testing.T) {
	setConfigTestDirs(t)
	t.Setenv("DOWNLOADER", "FFmpeg")
	t.Setenv("DOWNLOADER_FALLBACK", "native, ffmpeg")
	t.Setenv("FFMPEG_BIN", "/opt/ffmpeg")
	t.Setenv("STREAMLINK_BIN", "/opt/streamlink")
	t.Setenv("EXTERNAL_START_TIMEOUT_SECS", "5")
	t.Setenv("FLV_REPAIR", "ON")
	t.Setenv("FLV_REPAIR_BIN", "/opt/flvrepair")
	t.Setenv("FLV_REPAIR_MODE", "STRICT")
	t.Setenv("FLV_REPAIR_MAX_BUFFER_MB", "128")
	t.Setenv("TITLE_POLL_SECS", "7")

	cfg := loadTestConfig(t)

	if cfg.Downloader != "ffmpeg" {
		t.Fatalf("Downloader = %q, want ffmpeg", cfg.Downloader)
	}
	if len(cfg.DownloaderFallback) != 2 || cfg.DownloaderFallback[0] != "native" || cfg.DownloaderFallback[1] != "ffmpeg" {
		t.Fatalf("DownloaderFallback = %v, want [native ffmpeg]", cfg.DownloaderFallback)
	}
	if cfg.FFmpegBin != "/opt/ffmpeg" || cfg.StreamlinkBin != "/opt/streamlink" {
		t.Fatalf("bins = %q/%q", cfg.FFmpegBin, cfg.StreamlinkBin)
	}
	if cfg.ExternalStartTimeoutSecs != 5 {
		t.Fatalf("ExternalStartTimeoutSecs = %d, want 5", cfg.ExternalStartTimeoutSecs)
	}
	if cfg.FLVRepair != "on" || cfg.FLVRepairBin != "/opt/flvrepair" || cfg.FLVRepairMode != "strict" || cfg.FLVRepairMaxBufferMB != 128 {
		t.Fatalf("repair config = %q/%q/%q/%d", cfg.FLVRepair, cfg.FLVRepairBin, cfg.FLVRepairMode, cfg.FLVRepairMaxBufferMB)
	}
	if cfg.TitlePollSecs != 7 {
		t.Fatalf("TITLE_POLL_SECS = %d, want 7", cfg.TitlePollSecs)
	}
}

func TestExternalConfigInvalidValuesFallBack(t *testing.T) {
	setConfigTestDirs(t)
	t.Setenv("DOWNLOADER", "bogus")
	t.Setenv("FLV_REPAIR", "sometimes")
	t.Setenv("FLV_REPAIR_MODE", "lenient")

	cfg := loadTestConfig(t)

	if cfg.Downloader != "auto" {
		t.Fatalf("invalid DOWNLOADER should fall back to auto, got %q", cfg.Downloader)
	}
	if cfg.FLVRepair != "auto" {
		t.Fatalf("invalid FLV_REPAIR should fall back to auto, got %q", cfg.FLVRepair)
	}
	if cfg.FLVRepairMode != "best-effort" {
		t.Fatalf("invalid FLV_REPAIR_MODE should fall back to best-effort, got %q", cfg.FLVRepairMode)
	}
}

// Regression test for the Lead review finding on T3: external downloader /
// repair validation warnings must be emitted once in Validate(), not from the
// per-pipeline LiveStreamWriterSyncPeriodSecs getter.
func TestLiveStreamWriterSyncPeriodGetterDoesNotLogExternalWarnings(t *testing.T) {
	var buf bytes.Buffer
	logger.SetOutput(&buf)
	t.Cleanup(func() { logger.SetOutput(os.Stderr) })

	g := &GlobalReadOnly{config: &Config{
		liveStreamWriterSyncPeriod: -1,
		ExternalStartTimeoutSecs:   0,
		FLVRepairMaxBufferMB:       0,
	}}
	if got := g.LiveStreamWriterSyncPeriodSecs(); got != defaultLiveStreamWriterSyncPeriodSecs {
		t.Fatalf("LiveStreamWriterSyncPeriodSecs = %d, want %d", got, defaultLiveStreamWriterSyncPeriodSecs)
	}
	out := buf.String()
	if strings.Contains(out, "EXTERNAL_START_TIMEOUT_SECS") || strings.Contains(out, "FLV_REPAIR_MAX_BUFFER_MB") {
		t.Fatalf("getter must not emit external config warnings, got %q", out)
	}
}
