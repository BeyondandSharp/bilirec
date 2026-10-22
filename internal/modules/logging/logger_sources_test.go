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

	restBody := readLogFile(t, filepath.Join(dir, "bilirec.log.d", "access", "current.log"))
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

func TestLogGroupForName(t *testing.T) {
	cases := map[string]string{
		"":            "",
		"rest":        "access",
		"bilibili":    "live",
		"danmaku":     "live",
		"stream":      "live",
		"logging":     "system",
		"notify":      "system",
		"subcheck":    "system",
		"convert":     "recorder",
		"pipeline":    "recorder",
		"recorder":    "recorder",
		"repair":      "recorder",
		"updatecheck": "update",
		"unknown":     "system",
	}
	for raw, want := range cases {
		if got := logGroupForName(raw); got != want {
			t.Errorf("logGroupForName(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestSplitLogGroupsFilter(t *testing.T) {
	color := false
	logger.Init(logger.Options{Output: io.Discard, Color: &color})
	resetCoresForTest(t)

	dir := t.TempDir()
	base := filepath.Join(dir, "bilirec.log")
	lc := fxtest.NewLifecycle(t)
	wireLocalLogger(lc, &config.Config{
		LocalLogsEnabled:       true,
		LocalLogsSplit:         true,
		LocalLogsGroups:        []string{"system"},
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
	logger.Named("logging").Info("system-info")
	logger.Named("recorder").Info("recorder-disabled")
	logger.L().Info("unnamed")
	logger.Sync()
	lc.RequireStop()

	if _, err := os.Stat(filepath.Join(dir, "bilirec.log.d", "system", "current.log")); err != nil {
		t.Fatalf("system group missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "bilirec.log.d", "recorder", "current.log")); !os.IsNotExist(err) {
		t.Fatalf("disabled recorder group should not exist, err=%v", err)
	}
	mainBody := readLogFile(t, base)
	if !strings.Contains(mainBody, "system-info") || !strings.Contains(mainBody, "unnamed") {
		t.Fatalf("main missing enabled entries: %q", mainBody)
	}
	if strings.Contains(mainBody, "recorder-disabled") {
		t.Fatalf("disabled group leaked into main: %q", mainBody)
	}
}

func TestSplitLogGroupsCanDisableAllNamedLoggers(t *testing.T) {
	color := false
	logger.Init(logger.Options{Output: io.Discard, Color: &color})
	resetCoresForTest(t)

	dir := t.TempDir()
	base := filepath.Join(dir, "bilirec.log")
	lc := fxtest.NewLifecycle(t)
	wireLocalLogger(lc, &config.Config{
		LocalLogsEnabled:       true,
		LocalLogsSplit:         true,
		LocalLogsGroups:        []string{},
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
	logger.Named("rest").Info("access-disabled")
	logger.Named("recording").Info("named-disabled")
	logger.L().Info("main-only")
	logger.Sync()
	lc.RequireStop()

	mainBody := readLogFile(t, base)
	if !strings.Contains(mainBody, "main-only") {
		t.Fatalf("main missing unnamed line: %q", mainBody)
	}
	if strings.Contains(mainBody, "access-disabled") || strings.Contains(mainBody, "named-disabled") {
		t.Fatalf("disabled named logger leaked into main: %q", mainBody)
	}
}
