// SPDX-License-Identifier: GPL-3.0-or-later

package room

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bilirec/bilirec/internal/modules/config"
	settingssvc "github.com/bilirec/bilirec/internal/services/settings"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx/fxtest"
)

func newRoomConfigValidationApp(t *testing.T) *fiber.App {
	t.Helper()
	lc := fxtest.NewLifecycle(t)
	settingsSvc := settingssvc.NewService(lc, &config.Config{DatabaseDir: t.TempDir()}, nil)
	lc.RequireStart()
	t.Cleanup(lc.RequireStop)

	c := &Controller{settingsSvc: settingsSvc}
	app := fiber.New()
	app.Put("/room/:roomID/config", c.updateRoomConfig)
	return app
}

func putRoomConfig(t *testing.T, app *fiber.App, body string) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/room/42/config", strings.NewReader(body))
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, raw
}

func TestRoomConfigPutRejectsInvalidOverrides(t *testing.T) {
	app := newRoomConfigValidationApp(t)

	status, raw := putRoomConfig(t, app, `{"segment_mode":"bogus"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", status, raw)
	}
	if !strings.Contains(string(raw), "segment_mode") {
		t.Fatalf("expected Chinese segment_mode validation message, got %s", raw)
	}

	status, raw = putRoomConfig(t, app, `{"danmaku_transport":"tcp"}`)
	if status != http.StatusBadRequest || !strings.Contains(string(raw), "danmaku_transport") {
		t.Fatalf("status = %d body %s, want 400 with danmaku_transport", status, raw)
	}

	status, raw = putRoomConfig(t, app, `{"filename_template":"/abs/{title}"}`)
	if status != http.StatusBadRequest || !strings.Contains(string(raw), "filename_template") {
		t.Fatalf("status = %d body %s, want 400 with filename_template", status, raw)
	}

	status, raw = putRoomConfig(t, app, `{"filename_template":"a/../{title}"}`)
	if status != http.StatusBadRequest || !strings.Contains(string(raw), "filename_template") {
		t.Fatalf("status = %d body %s, want 400 for traversal template", status, raw)
	}

	status, raw = putRoomConfig(t, app, `{"bili_cdn":["bad_host!"]}`)
	if status != http.StatusBadRequest || !strings.Contains(string(raw), "bili_cdn") {
		t.Fatalf("status = %d body %s, want 400 with bili_cdn", status, raw)
	}

	// The 400 body carries the Chinese validation message (fiber's default
	// error body is plain text; the REST error handler mirrors it).
	if len(raw) == 0 || !strings.Contains(string(raw), "无效") {
		t.Fatalf("expected Chinese validation message body, got %q", raw)
	}
}
