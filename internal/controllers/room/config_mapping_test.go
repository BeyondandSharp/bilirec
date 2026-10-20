package room

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bilirec/bilirec/internal/services/subscribe"
	"github.com/gofiber/fiber/v3"
)

func roomConfigTestApp(t *testing.T) *fiber.App {
	t.Helper()
	app := fiber.New()
	app.Put("/room/:roomID/config", func(c fiber.Ctx) error {
		var req UpdateRoomConfigRequest
		if err := c.Bind().Body(&req); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "无效的请求数据")
		}
		cfg := roomConfigFromUpdate(req, req.StreamProfiles)
		return c.JSON(roomConfigResponse(42, cfg))
	})
	return app
}

func doJSONRequest(t *testing.T, app *fiber.App, method, path, body string, wantStatus int) map[string]any {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
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
	if resp.StatusCode != wantStatus {
		t.Fatalf("status = %d, want %d (body %s)", resp.StatusCode, wantStatus, raw)
	}
	var decoded map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("invalid JSON response: %v (%s)", err, raw)
		}
	}
	return decoded
}

func TestRoomConfigAPIRoundTrip(t *testing.T) {
	app := roomConfigTestApp(t)

	body := `{
		"auto_record": true,
		"notify": true,
		"record_duration_minutes": 30,
		"qn": 10000,
		"only_audio": false,
		"record_danmaku": true,
		"stream_profiles": ["http-flv"],
		"delete_oldest_on_low_disk": true,
		"segment_mode": "time",
		"segment_minutes": 45,
		"segment_size_mb": 512,
		"segment_by_title": true,
		"min_file_size_mb": 10,
		"filename_template": "{title}-{date}",
		"title_filter_patterns": "^广告",
		"save_stream_cover": true,
		"danmaku_types": {"raw_events": true, "super_chat": true, "gift": false, "guard": true},
		"danmaku_transport": "wss",
		"flv_write_metadata": false,
		"flv_split_on_script_tag": true,
		"flv_disable_split_on_annexb": false,
		"bili_live_api_host": "https://live.example.com",
		"bili_live_api_fallback_host": "https://fb.example.com",
		"bili_cdn": ["cn-gotcha01"],
		"bili_cdn_fallback": true,
		"bili_force_source": true,
		"bili_replace_cn01": ["a.example.com"]
	}`
	resp := doJSONRequest(t, app, http.MethodPut, "/room/42/config", body, http.StatusOK)

	checks := map[string]any{
		"room_id":                     float64(42),
		"auto_record":                 true,
		"notify":                      true,
		"record_duration_minutes":     float64(30),
		"qn":                          float64(10000),
		"record_danmaku":              true,
		"delete_oldest_on_low_disk":   true,
		"segment_mode":                "time",
		"segment_minutes":             float64(45),
		"segment_size_mb":             float64(512),
		"segment_by_title":            true,
		"min_file_size_mb":            float64(10),
		"filename_template":           "{title}-{date}",
		"title_filter_patterns":       "^广告",
		"save_stream_cover":           true,
		"danmaku_transport":           "wss",
		"flv_write_metadata":          false,
		"flv_split_on_script_tag":     true,
		"flv_disable_split_on_annexb": false,
		"bili_live_api_host":          "https://live.example.com",
		"bili_live_api_fallback_host": "https://fb.example.com",
		"bili_cdn_fallback":           true,
		"bili_force_source":           true,
	}
	for key, want := range checks {
		got, ok := resp[key]
		if !ok {
			t.Fatalf("response missing %q: %v", key, resp)
		}
		if got != want {
			t.Fatalf("response %q = %v, want %v", key, got, want)
		}
	}
	danmaku, ok := resp["danmaku_types"].(map[string]any)
	if !ok || danmaku["raw_events"] != true || danmaku["gift"] != false {
		t.Fatalf("danmaku_types round-trip failed: %v", resp["danmaku_types"])
	}
	cdn, ok := resp["bili_cdn"].([]any)
	if !ok || len(cdn) != 1 || cdn[0] != "cn-gotcha01" {
		t.Fatalf("bili_cdn round-trip failed: %v", resp["bili_cdn"])
	}
	replace, ok := resp["bili_replace_cn01"].([]any)
	if !ok || len(replace) != 1 || replace[0] != "a.example.com" {
		t.Fatalf("bili_replace_cn01 round-trip failed: %v", resp["bili_replace_cn01"])
	}
}

func TestRoomConfigLegacyPutOmitsOverrides(t *testing.T) {
	app := roomConfigTestApp(t)

	// A legacy client sends only the old fields: every v2 override must come
	// back as nil and be omitted from the response (= inherit global).
	body := `{"auto_record": false, "notify": true, "record_duration_minutes": 0, "qn": 0,
		"only_audio": false, "record_danmaku": false, "stream_profiles": [], "delete_oldest_on_low_disk": false}`
	resp := doJSONRequest(t, app, http.MethodPut, "/room/42/config", body, http.StatusOK)

	newKeys := []string{
		"segment_mode", "segment_minutes", "segment_size_mb", "segment_by_title",
		"min_file_size_mb", "filename_template", "title_filter_patterns", "save_stream_cover",
		"danmaku_types", "danmaku_transport", "flv_write_metadata", "flv_split_on_script_tag",
		"flv_disable_split_on_annexb", "bili_live_api_host", "bili_live_api_fallback_host",
		"bili_cdn", "bili_cdn_fallback", "bili_force_source", "bili_replace_cn01",
	}
	for _, key := range newKeys {
		if _, exists := resp[key]; exists {
			t.Fatalf("legacy PUT must leave %q nil/omitted, got %v", key, resp[key])
		}
	}
	if resp["room_id"] != float64(42) || resp["notify"] != true {
		t.Fatalf("legacy fields lost: %v", resp)
	}
}

func TestRoomConfigRequestClearsOverrideWhenAbsent(t *testing.T) {
	mode := "time"
	req := UpdateRoomConfigRequest{SegmentMode: &mode}
	cfg := roomConfigFromUpdate(req, nil)
	if cfg.SegmentMode == nil || *cfg.SegmentMode != "time" {
		t.Fatalf("present override must be stored: %+v", cfg.SegmentMode)
	}

	// Absent on the next PUT = nil override (clears the previous one).
	cfg = roomConfigFromUpdate(UpdateRoomConfigRequest{}, nil)
	if cfg.SegmentMode != nil {
		t.Fatalf("absent override must be nil: %+v", cfg.SegmentMode)
	}
	if cfg.StreamProfiles != nil {
		t.Fatalf("absent stream_profiles must stay nil: %+v", cfg.StreamProfiles)
	}
}

func TestRoomConfigResponseInheritShape(t *testing.T) {
	resp := roomConfigResponse(7, &subscribe.RoomConfig{AutoRecord: true, Notify: true})
	if resp.RoomId != 7 || !resp.AutoRecord || !resp.Notify {
		t.Fatalf("legacy fields lost: %+v", resp)
	}
	if resp.SegmentMode != nil || resp.DanmakuTypes != nil || resp.BiliCDN != nil {
		t.Fatalf("inherit fields must be nil: %+v", resp)
	}
}
