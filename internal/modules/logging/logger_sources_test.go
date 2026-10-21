package logging

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/pkg/logger"
	"go.uber.org/fx/fxtest"
)

func readLogFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

func TestSplitLocalLoggerRoutesSourcesAndKeepsRestInfoOutOfMain(t *testing.T) {
	color := false
	logger.Init(logger.Options{Output: io.Discard, Color: &color})
	resetCoresForTest(t)

	dir := t.TempDir()
	base := filepath.Join(dir, "bilirec.log")
	lc := fxtest.NewLifecycle(t)
	wireLocalLogger(lc, &config.Config{
		LocalLogsEnabled:       true,
		LocalLogsSplit:         true,
		LocalLogsPath:          base,
		LocalLogsFormat:        "pretty",
		LocalLogsOverflow:      "block",
		LocalLogsMaxSizeMB:     20,
		LocalLogsMaxAgeDays:    7,
		LocalLogsMaxBackups:    3,
		LocalLogsBatchBytes:    1024,
		LocalLogsBufferSize:    16,
		LocalLogsFlushInterval: 20 * time.Millisecond,
	})
	lc.RequireStart()

	logger.Named("rest").Info("http-access-info")
	logger.Named("rest").Warn("http-access-warn")
	logger.Named("recorder").Info("recorder-info")
	logger.L().Info("unnamed-main-line")
	logger.Sync()
	lc.RequireStop()

	restBody := readLogFile(t, filepath.Join(dir, "bilirec.log.d", "rest", "current.log"))
	if !strings.Contains(restBody, "http-access-info") || !strings.Contains(restBody, "http-access-warn") {
		t.Fatalf("rest source missing entries: %q", restBody)
	}
	recBody := readLogFile(t, filepath.Join(dir, "bilirec.log.d", "recorder", "current.log"))
	if !strings.Contains(recBody, "recorder-info") {
		t.Fatalf("recorder source missing entry: %q", recBody)
	}
	mainBody := readLogFile(t, base)
	if !strings.Contains(mainBody, "recorder-info") || !strings.Contains(mainBody, "unnamed-main-line") {
		t.Fatalf("main missing app entries: %q", mainBody)
	}
	if strings.Contains(mainBody, "http-access-info") {
		t.Fatalf("main must not contain REST INFO access logs: %q", mainBody)
	}
	if !strings.Contains(mainBody, "http-access-warn") {
		t.Fatalf("main should contain REST WARN logs: %q", mainBody)
	}
}

func TestNormalizeSourceName(t *testing.T) {
	cases := map[string]string{
		"":             "",
		"rest":         "rest",
		"src/with bad": "src_with_bad",
		"main":         "main_source",
		"..":           "other",
		"UPPER":        "upper",
		"ffmpeg_core":  "ffmpeg_core",
		"updatecheck":  "updatecheck",
	}
	for raw, want := range cases {
		if got := normalizeSourceName(raw); got != want {
			t.Errorf("normalizeSourceName(%q) = %q, want %q", raw, got, want)
		}
	}
}
