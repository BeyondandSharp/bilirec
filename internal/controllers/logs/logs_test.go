package logs

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	jwtware "github.com/gofiber/contrib/v3/jwt"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/extractors"
	jwtlib "github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const testLogsJWTSecret = "logs-test-secret"

func withNoAuth(t *testing.T) {
	t.Helper()
	old := config.ReadOnly
	config.ReadOnly = config.NewGlobalReadOnlyForTest(false, 0)
	t.Cleanup(func() { config.ReadOnly = old })
}

func withAuth(t *testing.T) {
	t.Helper()
	adminHash, err := bcrypt.GenerateFromPassword([]byte("adminpass"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	viewerHash, err := bcrypt.GenerateFromPassword([]byte("viewerpass"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	old := config.ReadOnly
	config.ReadOnly = config.NewGlobalReadOnlyWithAuthForTest(
		"admin", string(adminHash), "viewer", string(viewerHash), testLogsJWTSecret,
	)
	t.Cleanup(func() { config.ReadOnly = old })
}

func mintLogsJWT(t *testing.T, role string) string {
	t.Helper()
	claims := jwtlib.MapClaims{
		"name": "test-" + role,
		"iat":  time.Now().Unix(),
		"exp":  time.Now().Add(time.Hour).Unix(),
		"iss":  "bilirec",
		"role": role,
	}
	token := jwtlib.NewWithClaims(jwtlib.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(testLogsJWTSecret))
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func newLogsApp(t *testing.T, cfg *config.Config, withJWT bool) *fiber.App {
	t.Helper()
	app := fiber.New()
	if withJWT {
		app.Use(jwtware.New(jwtware.Config{
			SigningKey: jwtware.SigningKey{Key: []byte(testLogsJWTSecret)},
			Extractor:  extractors.FromAuthHeader("Bearer"),
			ErrorHandler: func(c fiber.Ctx, _ error) error {
				return c.Status(fiber.StatusUnauthorized).SendString("unauthorized")
			},
		}))
	}
	NewController(app, cfg)
	return app
}

func logsRequest(t *testing.T, app *fiber.App, path, token string, wantStatus int) ([]byte, http.Header) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set(fiber.HeaderAuthorization, "Bearer "+token)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != wantStatus {
		t.Fatalf("GET %s: status = %d, want %d (body %s)", path, resp.StatusCode, wantStatus, raw)
	}
	return raw, resp.Header
}

func decodeLogs(t *testing.T, raw []byte) LogsResponse {
	t.Helper()
	var resp LogsResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("invalid /logs response: %v (%s)", err, raw)
	}
	return resp
}

func writeLines(t *testing.T, path string, lines []string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func numberedLines(n int) []string {
	lines := make([]string, 0, n)
	for i := 0; i < n; i++ {
		lines = append(lines, fmt.Sprintf("line-%04d", i))
	}
	return lines
}

func TestLogFilePathRules(t *testing.T) {
	if got := logFilePath(&config.Config{LocalLogsPath: "/tmp/custom.log", DatabaseDir: "db"}); got != "/tmp/custom.log" {
		t.Fatalf("configured path = %q", got)
	}
	if got := logFilePath(&config.Config{DatabaseDir: "db"}); got != filepath.Join("db", "bilirec.log") {
		t.Fatalf("fallback path = %q", got)
	}
	if got := logFilePath(nil); got != "" {
		t.Fatalf("nil config path = %q", got)
	}
}

func TestParseLines(t *testing.T) {
	if got, err := parseLines(""); err != nil || got != defaultLogLines {
		t.Fatalf("default = %d err=%v", got, err)
	}
	if got, err := parseLines("2000"); err != nil || got != 2000 {
		t.Fatalf("max = %d err=%v", got, err)
	}
	for _, raw := range []string{"0", "-1", "2001", "abc", "1.5"} {
		if _, err := parseLines(raw); err == nil {
			t.Fatalf("parseLines(%q) should fail", raw)
		}
	}
}

func TestLogsTailAndTruncation(t *testing.T) {
	withNoAuth(t)
	path := filepath.Join(t.TempDir(), "logs", "bilirec.log")
	writeLines(t, path, numberedLines(20))
	cfg := &config.Config{LocalLogsEnabled: true, LocalLogsPath: path, LocalLogsFormat: "jsonline"}
	app := newLogsApp(t, cfg, false)

	raw, _ := logsRequest(t, app, "/logs?lines=5", "", http.StatusOK)
	resp := decodeLogs(t, raw)
	if !resp.Enabled || !resp.Exists || resp.Size == 0 || resp.MTime == "" {
		t.Fatalf("metadata = %+v", resp)
	}
	if resp.Format != "jsonline" || resp.Path != path {
		t.Fatalf("format/path = %q %q", resp.Format, resp.Path)
	}
	if len(resp.Lines) != 5 || resp.Lines[4] != "line-0019" || resp.Lines[0] != "line-0015" {
		t.Fatalf("lines = %v", resp.Lines)
	}
	if !resp.Truncated {
		t.Fatal("expected truncated=true when more lines exist than requested")
	}

	// Default line count returns at most 500.
	writeLines(t, path, numberedLines(600))
	raw, _ = logsRequest(t, app, "/logs", "", http.StatusOK)
	resp = decodeLogs(t, raw)
	if len(resp.Lines) != 500 || resp.Lines[499] != "line-0599" {
		t.Fatalf("default lines = %d last=%q", len(resp.Lines), resp.Lines[len(resp.Lines)-1])
	}
	if !resp.Truncated {
		t.Fatal("expected truncated=true for default line cap")
	}

	// Requesting all lines of a small file is not truncated.
	writeLines(t, path, numberedLines(600))
	raw, _ = logsRequest(t, app, "/logs?lines=2000", "", http.StatusOK)
	resp = decodeLogs(t, raw)
	if len(resp.Lines) != 600 || resp.Truncated {
		t.Fatalf("lines=%d truncated=%v", len(resp.Lines), resp.Truncated)
	}
}

func TestLogsDisabled(t *testing.T) {
	withNoAuth(t)
	path := filepath.Join(t.TempDir(), "bilirec.log")
	writeLines(t, path, []string{"should-not-be-returned"})
	cfg := &config.Config{LocalLogsEnabled: false, LocalLogsPath: path}
	app := newLogsApp(t, cfg, false)

	raw, _ := logsRequest(t, app, "/logs", "", http.StatusOK)
	resp := decodeLogs(t, raw)
	if resp.Enabled {
		t.Fatal("enabled must be false")
	}
	if !resp.Exists || resp.Size == 0 {
		t.Fatalf("exists/size should reflect the file: %+v", resp)
	}
	if len(resp.Lines) != 0 || resp.Truncated {
		t.Fatalf("disabled response must not contain lines: %+v", resp)
	}
	logsRequest(t, app, "/logs/download", "", http.StatusNotFound)
}

func TestLogsMissingFile(t *testing.T) {
	withNoAuth(t)
	path := filepath.Join(t.TempDir(), "missing", "bilirec.log")
	cfg := &config.Config{LocalLogsEnabled: true, LocalLogsPath: path}
	app := newLogsApp(t, cfg, false)

	raw, _ := logsRequest(t, app, "/logs", "", http.StatusOK)
	resp := decodeLogs(t, raw)
	if !resp.Enabled || resp.Exists || len(resp.Lines) != 0 || resp.Truncated {
		t.Fatalf("missing file response = %+v", resp)
	}
	if resp.Path != path {
		t.Fatalf("path = %q", resp.Path)
	}
	logsRequest(t, app, "/logs/download", "", http.StatusNotFound)
}

func TestLogsInvalidLines(t *testing.T) {
	withNoAuth(t)
	path := filepath.Join(t.TempDir(), "bilirec.log")
	writeLines(t, path, []string{"line"})
	app := newLogsApp(t, &config.Config{LocalLogsEnabled: true, LocalLogsPath: path}, false)
	for _, query := range []string{"lines=0", "lines=-1", "lines=2001", "lines=abc", "lines=1.5"} {
		logsRequest(t, app, "/logs?"+query, "", http.StatusBadRequest)
	}
}

func TestLogsFallbackPath(t *testing.T) {
	withNoAuth(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "bilirec.log")
	writeLines(t, path, []string{"fallback-line"})
	cfg := &config.Config{LocalLogsEnabled: true, DatabaseDir: dir}
	app := newLogsApp(t, cfg, false)

	raw, _ := logsRequest(t, app, "/logs", "", http.StatusOK)
	resp := decodeLogs(t, raw)
	if resp.Path != filepath.Join(dir, "bilirec.log") {
		t.Fatalf("fallback path = %q", resp.Path)
	}
	if len(resp.Lines) != 1 || resp.Lines[0] != "fallback-line" {
		t.Fatalf("lines = %v", resp.Lines)
	}
}

func TestLogsOversizedFileDropsPartialFirstLine(t *testing.T) {
	withNoAuth(t)
	path := filepath.Join(t.TempDir(), "bilirec.log")
	// A >2 MiB first line forces the tail window to start mid-line.
	huge := strings.Repeat("A", 3<<20)
	writeLines(t, path, append([]string{huge}, numberedLines(10)...))
	cfg := &config.Config{LocalLogsEnabled: true, LocalLogsPath: path}
	app := newLogsApp(t, cfg, false)

	raw, _ := logsRequest(t, app, "/logs?lines=2000", "", http.StatusOK)
	resp := decodeLogs(t, raw)
	if !resp.Truncated {
		t.Fatal("expected truncated=true for a file larger than the 2 MiB window")
	}
	if len(resp.Lines) != 10 {
		t.Fatalf("lines = %d, want the 10 complete lines after the huge line", len(resp.Lines))
	}
	for i, line := range resp.Lines {
		want := fmt.Sprintf("line-%04d", i)
		if line != want {
			t.Fatalf("line %d = %q (partial line was not dropped), want %q", i, line, want)
		}
	}
}

func TestLogsDownload(t *testing.T) {
	withNoAuth(t)
	path := filepath.Join(t.TempDir(), "bilirec.log")
	writeLines(t, path, []string{"download-line-1", "download-line-2"})
	app := newLogsApp(t, &config.Config{LocalLogsEnabled: true, LocalLogsPath: path}, false)

	raw, header := logsRequest(t, app, "/logs/download", "", http.StatusOK)
	if !strings.Contains(header.Get("Content-Type"), "text/plain") {
		t.Fatalf("content-type = %q", header.Get("Content-Type"))
	}
	if got := header.Get("Content-Disposition"); !strings.Contains(got, "attachment") || !strings.Contains(got, `filename="bilirec.log"`) {
		t.Fatalf("content-disposition = %q", got)
	}
	if string(raw) != "download-line-1\ndownload-line-2\n" {
		t.Fatalf("download body mismatch: %q", raw)
	}
}

func TestLogsRoutesRequireAdmin(t *testing.T) {
	withAuth(t)
	path := filepath.Join(t.TempDir(), "bilirec.log")
	writeLines(t, path, []string{"line"})
	app := newLogsApp(t, &config.Config{LocalLogsEnabled: true, LocalLogsPath: path}, true)

	logsRequest(t, app, "/logs", "", http.StatusUnauthorized)
	logsRequest(t, app, "/logs", mintLogsJWT(t, "viewer"), http.StatusForbidden)
	logsRequest(t, app, "/logs", mintLogsJWT(t, "admin"), http.StatusOK)
	logsRequest(t, app, "/logs/download", mintLogsJWT(t, "viewer"), http.StatusForbidden)
	logsRequest(t, app, "/logs/download", mintLogsJWT(t, "admin"), http.StatusOK)
}

func TestLogsSourcesSplitAndSelection(t *testing.T) {
	withNoAuth(t)
	base := filepath.Join(t.TempDir(), "bilirec.log")
	writeLines(t, base, []string{"main-line"})
	sourceDir := base + ".d"
	writeLines(t, filepath.Join(sourceDir, "access", "current.log"), []string{"access-line"})
	writeLines(t, filepath.Join(sourceDir, "system", "current.log"), []string{"recorder-line"})
	// Rotated backups must never appear as sources.
	writeLines(t, filepath.Join(sourceDir, "access", "access-2026-01-01T00-00-00.000.log"), []string{"old-rest"})
	cfg := &config.Config{LocalLogsEnabled: true, LocalLogsSplit: true, LocalLogsPath: base}
	app := newLogsApp(t, cfg, false)

	raw, _ := logsRequest(t, app, "/logs/sources", "", http.StatusOK)
	var sources LogSourcesResponse
	if err := json.Unmarshal(raw, &sources); err != nil {
		t.Fatal(err)
	}
	if !sources.Enabled || !sources.Split || sources.BasePath != base || sources.SourcesDir != sourceDir {
		t.Fatalf("sources metadata = %+v", sources)
	}
	names := make([]string, 0, len(sources.Sources))
	for _, source := range sources.Sources {
		names = append(names, source.Name)
	}
	if strings.Join(names, ",") != "main,access,system" {
		t.Fatalf("sources = %v", names)
	}

	raw, _ = logsRequest(t, app, "/logs?source=access&lines=10", "", http.StatusOK)
	resp := decodeLogs(t, raw)
	if resp.Source != "access" || !resp.Split || len(resp.Lines) != 1 || resp.Lines[0] != "access-line" {
		t.Fatalf("rest response = %+v", resp)
	}

	raw, header := logsRequest(t, app, "/logs/download?source=access", "", http.StatusOK)
	if string(raw) != "access-line\n" || !strings.Contains(header.Get("Content-Disposition"), `filename="access.log"`) {
		t.Fatalf("rest download = %q disposition=%q", raw, header.Get("Content-Disposition"))
	}

	// Request input never becomes an arbitrary path.
	for _, source := range []string{"../access", "missing", "access/current.log"} {
		logsRequest(t, app, "/logs?source="+source, "", http.StatusBadRequest)
	}
	logsRequest(t, app, "/logs/download?source=missing", "", http.StatusBadRequest)
}

func TestLogsSourcesDisabledSplit(t *testing.T) {
	withNoAuth(t)
	base := filepath.Join(t.TempDir(), "bilirec.log")
	writeLines(t, base, []string{"main-line"})
	cfg := &config.Config{LocalLogsEnabled: true, LocalLogsSplit: false, LocalLogsPath: base}
	app := newLogsApp(t, cfg, false)

	raw, _ := logsRequest(t, app, "/logs/sources", "", http.StatusOK)
	var sources LogSourcesResponse
	if err := json.Unmarshal(raw, &sources); err != nil {
		t.Fatal(err)
	}
	if sources.Split || len(sources.Sources) != 1 || sources.Sources[0].Name != "main" {
		t.Fatalf("unsplit sources = %+v", sources)
	}
	logsRequest(t, app, "/logs?source=access", "", http.StatusBadRequest)
}
