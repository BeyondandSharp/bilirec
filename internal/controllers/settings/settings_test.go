package settings

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	settingssvc "github.com/bilirec/bilirec/internal/services/settings"
	jwtware "github.com/gofiber/contrib/v3/jwt"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/extractors"
	jwtlib "github.com/golang-jwt/jwt/v5"
	"go.uber.org/fx/fxtest"
	"golang.org/x/crypto/bcrypt"
)

const testSettingsJWTSecret = "settings-test-secret"

func newTestSettingsService(t *testing.T) *settingssvc.Service {
	t.Helper()
	lc := fxtest.NewLifecycle(t)
	svc := settingssvc.NewService(lc, &config.Config{DatabaseDir: t.TempDir()}, nil, nil)
	lc.RequireStart()
	t.Cleanup(lc.RequireStop)
	return svc
}

func newSettingsApp(t *testing.T, svc *settingssvc.Service, withJWT bool) *fiber.App {
	t.Helper()
	app := fiber.New()
	if withJWT {
		app.Use(jwtware.New(jwtware.Config{
			SigningKey: jwtware.SigningKey{Key: []byte(testSettingsJWTSecret)},
			Extractor:  extractors.FromAuthHeader("Bearer"),
			// Mirror the production REST middleware: auth failures are 401.
			ErrorHandler: func(c fiber.Ctx, _ error) error {
				return c.Status(fiber.StatusUnauthorized).SendString("unauthorized")
			},
		}))
	}
	NewController(app, svc)
	return app
}

func settingsRequest(t *testing.T, app *fiber.App, method, path, body, token string, wantStatus int) []byte {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	}
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
		t.Fatalf("%s %s: status = %d, want %d (body %s)", method, path, resp.StatusCode, wantStatus, raw)
	}
	return raw
}

func decodeSettingsResponse(t *testing.T, raw []byte) SettingsResponse {
	t.Helper()
	var resp SettingsResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("invalid settings response: %v (%s)", err, raw)
	}
	return resp
}

// withNoAuth switches the global read-only config to "REST auth disabled" for
// the duration of the test.
func withNoAuth(t *testing.T) {
	t.Helper()
	old := config.ReadOnly
	config.ReadOnly = config.NewGlobalReadOnlyForTest(false, 0)
	t.Cleanup(func() { config.ReadOnly = old })
}

// withAuth switches the global read-only config to admin+viewer RBAC.
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
		"admin", string(adminHash), "viewer", string(viewerHash), testSettingsJWTSecret,
	)
	t.Cleanup(func() { config.ReadOnly = old })
}

func mintSettingsJWT(t *testing.T, role string) string {
	t.Helper()
	claims := jwtlib.MapClaims{
		"name": "test-" + role,
		"iat":  time.Now().Unix(),
		"exp":  time.Now().Add(time.Hour).Unix(),
		"iss":  "bilirec",
		"role": role,
	}
	token := jwtlib.NewWithClaims(jwtlib.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(testSettingsJWTSecret))
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func TestSettingsAPIDefaults(t *testing.T) {
	withNoAuth(t)
	svc := newTestSettingsService(t)
	app := newSettingsApp(t, svc, false)

	raw := settingsRequest(t, app, http.MethodGet, "/settings", "", "", http.StatusOK)
	resp := decodeSettingsResponse(t, raw)

	if resp.Defaults.SegmentMode != "off" || resp.Defaults.DanmakuTransport != "auto" ||
		!resp.Defaults.FlvWriteMetadata || !resp.Defaults.FlvDisableSplitOnAnnexB {
		t.Fatalf("unexpected defaults: %+v", resp.Defaults)
	}
	if !resp.Defaults.DanmakuTypes.SuperChat || !resp.Defaults.DanmakuTypes.Gift || !resp.Defaults.DanmakuTypes.Guard ||
		resp.Defaults.DanmakuTypes.RawEvents {
		t.Fatalf("unexpected danmaku type defaults: %+v", resp.Defaults.DanmakuTypes)
	}
	if resp.Overrides.SegmentMode != "" || resp.Overrides.FilenameTemplate != "" {
		t.Fatalf("overrides should be empty on first start: %+v", resp.Overrides)
	}
	if resp.Values.SegmentMode != resp.Defaults.SegmentMode ||
		resp.Values.DanmakuTransport != resp.Defaults.DanmakuTransport ||
		resp.Values.FlvWriteMetadata != resp.Defaults.FlvWriteMetadata {
		t.Fatalf("values must equal defaults with no overrides: %+v", resp.Values)
	}
	if resp.Values.BiliCDN == nil || resp.Values.BiliReplaceCN01 == nil {
		t.Fatalf("cdn slices must serialize as empty arrays: %#v %#v", resp.Values.BiliCDN, resp.Values.BiliReplaceCN01)
	}
}

func TestSettingsAPIPartialUpdateAndReset(t *testing.T) {
	withNoAuth(t)
	svc := newTestSettingsService(t)
	app := newSettingsApp(t, svc, false)

	raw := settingsRequest(t, app, http.MethodPut, "/settings",
		`{"segment_mode":"time","segment_minutes":45}`, "", http.StatusOK)
	resp := decodeSettingsResponse(t, raw)
	if resp.Values.SegmentMode != "time" || resp.Values.SegmentMinutes != 45 {
		t.Fatalf("values not updated: %+v", resp.Values)
	}
	if resp.Overrides.SegmentMode != "time" || resp.Overrides.SegmentMinutes != 45 {
		t.Fatalf("overrides not reported: %+v", resp.Overrides)
	}
	if resp.Defaults.SegmentMode != "off" {
		t.Fatalf("defaults must stay immutable: %+v", resp.Defaults)
	}

	// A second partial update keeps the previous override (merge semantics).
	raw = settingsRequest(t, app, http.MethodPut, "/settings",
		`{"min_file_size_mb":7,"bili_cdn":["cn-gotcha01"],"danmaku_types":{"raw_events":true,"super_chat":true,"gift":false,"guard":false}}`,
		"", http.StatusOK)
	resp = decodeSettingsResponse(t, raw)
	if resp.Values.SegmentMode != "time" || resp.Values.SegmentMinutes != 45 {
		t.Fatalf("previous overrides lost on partial update: %+v", resp.Values)
	}
	if resp.Values.MinFileSizeMB != 7 || len(resp.Values.BiliCDN) != 1 || resp.Values.BiliCDN[0] != "cn-gotcha01" {
		t.Fatalf("second partial update not applied: %+v", resp.Values)
	}
	if !resp.Values.DanmakuTypes.RawEvents || resp.Values.DanmakuTypes.Gift {
		t.Fatalf("danmaku types override not applied: %+v", resp.Values.DanmakuTypes)
	}

	// Validation errors are 400 with a Chinese message and change nothing.
	raw = settingsRequest(t, app, http.MethodPut, "/settings", `{"segment_mode":"bogus"}`, "", http.StatusBadRequest)
	if !strings.Contains(string(raw), "segment_mode") {
		t.Fatalf("expected segment_mode validation message, got %s", raw)
	}
	raw = settingsRequest(t, app, http.MethodPut, "/settings", `{"segment_minutes":0}`, "", http.StatusBadRequest)
	if !strings.Contains(string(raw), "segment_minutes") {
		t.Fatalf("expected segment_minutes validation message, got %s", raw)
	}
	// Unknown keys are ignored (Go default decoding).
	settingsRequest(t, app, http.MethodPut, "/settings", `{"unknown_key":1}`, "", http.StatusOK)

	settingsRequest(t, app, http.MethodPost, "/settings/reset", "", "", http.StatusOK)
	raw = settingsRequest(t, app, http.MethodGet, "/settings", "", "", http.StatusOK)
	resp = decodeSettingsResponse(t, raw)
	if resp.Values.SegmentMode != "off" || resp.Values.SegmentMinutes != 120 || resp.Values.MinFileSizeMB != 0 {
		t.Fatalf("reset did not restore defaults: %+v", resp.Values)
	}
	if len(resp.Values.BiliCDN) != 0 {
		t.Fatalf("reset did not clear overrides: %+v", resp.Values)
	}
}

func TestSettingsAPIPersistenceAcrossRestart(t *testing.T) {
	withNoAuth(t)
	dir := t.TempDir()

	open := func() (*settingssvc.Service, func()) {
		lc := fxtest.NewLifecycle(t)
		svc := settingssvc.NewService(lc, &config.Config{DatabaseDir: dir}, nil, nil)
		lc.RequireStart()
		stopped := false
		stop := func() {
			if stopped {
				return
			}
			stopped = true
			lc.RequireStop()
		}
		t.Cleanup(stop)
		return svc, stop
	}

	svc, stop := open()
	app := newSettingsApp(t, svc, false)
	settingsRequest(t, app, http.MethodPut, "/settings", `{"filename_template":"{title}"}`, "", http.StatusOK)
	stop()

	reloaded, stopReloaded := open()
	defer stopReloaded()
	app = newSettingsApp(t, reloaded, false)
	raw := settingsRequest(t, app, http.MethodGet, "/settings", "", "", http.StatusOK)
	resp := decodeSettingsResponse(t, raw)
	if resp.Values.FilenameTemplate != "{title}" {
		t.Fatalf("persisted setting not reloaded: %+v", resp.Values)
	}
}

func TestSettingsAPIAuth(t *testing.T) {
	withAuth(t)
	svc := newTestSettingsService(t)
	app := newSettingsApp(t, svc, true)

	adminToken := mintSettingsJWT(t, "admin")
	viewerToken := mintSettingsJWT(t, "viewer")

	// GET: admin + viewer allowed; missing token rejected by JWT middleware.
	settingsRequest(t, app, http.MethodGet, "/settings", "", "", http.StatusUnauthorized)
	settingsRequest(t, app, http.MethodGet, "/settings", "", viewerToken, http.StatusOK)
	settingsRequest(t, app, http.MethodGet, "/settings", "", adminToken, http.StatusOK)

	// PUT: admin only.
	settingsRequest(t, app, http.MethodPut, "/settings", `{"min_file_size_mb":1}`, "", http.StatusUnauthorized)
	settingsRequest(t, app, http.MethodPut, "/settings", `{"min_file_size_mb":1}`, viewerToken, http.StatusForbidden)
	settingsRequest(t, app, http.MethodPut, "/settings", `{"min_file_size_mb":1}`, adminToken, http.StatusOK)

	// Reset: admin only.
	settingsRequest(t, app, http.MethodPost, "/settings/reset", "", viewerToken, http.StatusForbidden)
	settingsRequest(t, app, http.MethodPost, "/settings/reset", "", adminToken, http.StatusOK)
}
