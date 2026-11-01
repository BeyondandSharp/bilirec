package bilibili

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// v2StreamBodyWithSubtitle injects subtitle_cfg into a successful v2 response.
// status < 0 omits the subtitle_cfg object entirely (null).
func v2StreamBodyWithSubtitle(status int, entries ...fakeStreamEntry) []byte {
	var body map[string]any
	if err := json.Unmarshal(v2StreamBody(entries...), &body); err != nil {
		panic(err)
	}
	data, ok := body["data"].(map[string]any)
	if !ok {
		panic("unexpected v2 body shape")
	}
	if status < 0 {
		data["subtitle_cfg"] = nil
	} else {
		data["subtitle_cfg"] = map[string]any{"status": status}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return encoded
}

func TestGetStreamURLsV2WithExtraSubtitleEnabled(t *testing.T) {
	server := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, v2StreamBodyWithSubtitle(1,
			flvEntry("https://cdn-a.example.com", "/live/a.flv", "?token=1", int(QualityOriginal)),
		))
	})
	client := newTestBilibiliClient(t, server.Server.Config.Handler.ServeHTTP)

	urls, extra, err := client.GetStreamURLsV2WithExtra(testRoomID)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(urls) != 1 {
		t.Fatalf("urls = %d, want 1", len(urls))
	}
	if extra == nil || !extra.SubtitleEnabled {
		t.Fatalf("extra = %+v, want SubtitleEnabled", extra)
	}

	// The compatibility wrapper drops the extra but keeps the URLs.
	plain, err := client.GetStreamURLsV2(testRoomID)
	if err != nil {
		t.Fatalf("plain fetch: %v", err)
	}
	if len(plain) != 1 || plain[0].URL != urls[0].URL {
		t.Fatalf("plain urls = %v, want %v", urlsOf(plain), urlsOf(urls))
	}
}

func TestGetStreamURLsV2SubtitleDisabledAndMissing(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
	}{
		{name: "status zero", status: 0},
		{name: "subtitle_cfg absent", status: -1},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, v2StreamBodyWithSubtitle(test.status,
					flvEntry("https://cdn-a.example.com", "/live/a.flv", "?token=1", int(QualityOriginal)),
				))
			})
			client := newTestBilibiliClient(t, server.Server.Config.Handler.ServeHTTP)

			_, extra, err := client.GetStreamURLsV2WithExtra(testRoomID)
			if err != nil {
				t.Fatalf("fetch: %v", err)
			}
			if extra == nil || extra.SubtitleEnabled {
				t.Fatalf("extra = %+v, want disabled", extra)
			}

			enabled, err := client.RoomSubtitleEnabled(testRoomID)
			if err != nil {
				t.Fatalf("RoomSubtitleEnabled: %v", err)
			}
			if enabled {
				t.Fatal("RoomSubtitleEnabled = true, want false")
			}
		})
	}
}

func TestRoomSubtitleEnabledTrue(t *testing.T) {
	server := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, v2StreamBodyWithSubtitle(1,
			flvEntry("https://cdn-a.example.com", "/live/a.flv", "?token=1", int(QualityOriginal)),
		))
	})
	client := newTestBilibiliClient(t, server.Server.Config.Handler.ServeHTTP)

	enabled, err := client.RoomSubtitleEnabled(testRoomID)
	if err != nil {
		t.Fatalf("RoomSubtitleEnabled: %v", err)
	}
	if !enabled {
		t.Fatal("RoomSubtitleEnabled = false, want true")
	}
}

// TestGetStreamURLsV2WithExtraStickyAcrossFallback verifies that extra metadata
// from a successful fallback response is still reported when the primary host
// fails (the advanced fetch path may issue several requests).
func TestGetStreamURLsV2WithExtraStickyAcrossFallback(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream broken", http.StatusBadGateway)
	}))
	t.Cleanup(failing.Close)

	official := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, v2StreamBodyWithSubtitle(1,
			flvEntry("https://cdn-a.example.com", "/live/a.flv", "?token=1", int(QualityOriginal)),
		))
	})
	client := newTestBilibiliClient(t, official.Server.Config.Handler.ServeHTTP)

	urls, extra, err := client.GetStreamURLsV2WithExtra(testRoomID, WithFetchConfig(StreamFetchConfig{
		APIHost: failing.URL,
	}))
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(urls) != 1 {
		t.Fatalf("urls = %d, want 1 (official fallback)", len(urls))
	}
	if extra == nil || !extra.SubtitleEnabled {
		t.Fatalf("extra = %+v, want SubtitleEnabled from fallback response", extra)
	}
}

// TestGetStreamURLsV2WithExtraEmptyPlayurl verifies that subtitle metadata is
// still returned when a successful response carries no playable URLs.
func TestGetStreamURLsV2WithExtraEmptyPlayurl(t *testing.T) {
	server := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []byte(`{"code":0,"message":"0","ttl":1,"data":{"playurl_info":null,"subtitle_cfg":{"status":1}}}`))
	})
	client := newTestBilibiliClient(t, server.Server.Config.Handler.ServeHTTP)

	urls, extra, err := client.GetStreamURLsV2WithExtra(testRoomID)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(urls) != 0 {
		t.Fatalf("urls = %v, want empty", urlsOf(urls))
	}
	if extra == nil || !extra.SubtitleEnabled {
		t.Fatalf("extra = %+v, want SubtitleEnabled", extra)
	}
}
