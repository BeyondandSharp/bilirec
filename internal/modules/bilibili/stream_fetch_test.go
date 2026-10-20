package bilibili

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	bili "github.com/CuteReimu/bilibili/v2"
	"github.com/go-resty/resty/v2"
)

// ---------------------------------------------------------------------------
// Test helpers (all offline: every HTTP request goes to an httptest server)
// ---------------------------------------------------------------------------

const (
	testWBIKey    = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	testWBIKeySub = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	testRoomID    = 12345
)

type memoryTestStorage struct {
	data map[string]any
}

func newMemoryTestStorage() *memoryTestStorage {
	return &memoryTestStorage{data: map[string]any{}}
}

func (s *memoryTestStorage) Set(key string, value any) { s.data[key] = value }

func (s *memoryTestStorage) Get(key string) (any, bool) {
	value, ok := s.data[key]
	return value, ok
}

// streamFetchTestTransport routes the hard-coded official API host to the fake
// official httptest server and rejects any other non-loopback host so tests can
// never reach the real network.
type streamFetchTestTransport struct {
	officialURL *url.URL
	base        http.RoundTripper
}

func (tr *streamFetchTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Hostname()
	if host == officialTestHostname {
		clone := req.Clone(req.Context())
		clone.URL.Scheme = tr.officialURL.Scheme
		clone.URL.Host = tr.officialURL.Host
		clone.Host = tr.officialURL.Host
		return tr.base.RoundTrip(clone)
	}
	if host == "127.0.0.1" || host == "localhost" || host == "::1" {
		return tr.base.RoundTrip(req)
	}
	return nil, fmt.Errorf("stream fetch test transport: unexpected network request to %q", req.URL.String())
}

const officialTestHostname = "api.live.bilibili.com"

// newTestBilibiliClient builds a *Client wired for offline tests. The official
// Bilibili API host is served by official.
func newTestBilibiliClient(t *testing.T, official http.HandlerFunc) *Client {
	t.Helper()
	officialServer := httptest.NewServer(official)
	t.Cleanup(officialServer.Close)
	return newTestBilibiliClientWithOfficialURL(t, officialServer.URL)
}

func newTestBilibiliClientWithOfficialURL(t *testing.T, officialServerURL string) *Client {
	t.Helper()

	officialURL, err := url.Parse(officialServerURL)
	if err != nil {
		t.Fatalf("parse official server url: %v", err)
	}

	liveClient := configureLiveClient(
		resty.New().
			SetRedirectPolicy(resty.NoRedirectPolicy()).
			SetTransport(&streamFetchTestTransport{officialURL: officialURL, base: http.DefaultTransport}),
		"application/json",
	)

	wbi := bili.NewDefaultWbi().WithStorage(newMemoryTestStorage())
	wbi.SetKeys(testWBIKey, testWBIKeySub)

	return &Client{
		Client:     bili.New(),
		liveClient: liveClient,
		wbi:        wbi,
		ctx:        context.Background(),
	}
}

// testAPIServer is a recording fake Bilibili API server.
type testAPIServer struct {
	*httptest.Server

	mu      sync.Mutex
	queries []url.Values
	paths   []string
	headers []http.Header
}

func newTestAPIServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *testAPIServer {
	t.Helper()
	server := &testAPIServer{}
	server.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server.mu.Lock()
		server.queries = append(server.queries, r.URL.Query())
		server.paths = append(server.paths, r.URL.Path)
		server.headers = append(server.headers, r.Header.Clone())
		server.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(server.Server.Close)
	return server
}

func (s *testAPIServer) requestCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queries)
}

func (s *testAPIServer) queryAt(index int) url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.queries[index]
}

func (s *testAPIServer) pathAt(index int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.paths[index]
}

func (s *testAPIServer) headerAt(index int) http.Header {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.headers[index]
}

func (s *testAPIServer) cdnParams() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.queries))
	for _, query := range s.queries {
		out = append(out, query.Get("cdn"))
	}
	return out
}

// newTestClientWithUnusedOfficial builds a client whose official API host
// answers with an empty (but successful) response. Tests that set a custom
// APIHost and fail it exercise the official safety net without turning the
// official response into an unexpected error.
func newTestClientWithUnusedOfficial(t *testing.T) *Client {
	t.Helper()
	return newTestBilibiliClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, v2EmptyBody())
	})
}

// writeJSON writes body with a JSON content type.
func writeJSON(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// fakeStreamEntry describes one playurl codec/url_info pair for fake responses.
type fakeStreamEntry struct {
	Protocol string
	Format   string
	Codec    string
	Host     string
	BaseURL  string
	Extra    string
	Qn       int
	AcceptQn []int
}

func fakeStream(format, codec, host, baseURL, extra string, qn int) fakeStreamEntry {
	protocol := "http_stream"
	if format == "ts" || format == "fmp4" {
		protocol = "http_hls"
	}
	return fakeStreamEntry{
		Protocol: protocol,
		Format:   format,
		Codec:    codec,
		Host:     host,
		BaseURL:  baseURL,
		Extra:    extra,
		Qn:       qn,
		AcceptQn: []int{qn},
	}
}

// v2StreamBody builds a successful playurl v2 response.
func v2StreamBody(entries ...fakeStreamEntry) []byte {
	streams := make([]any, 0, len(entries))
	for _, entry := range entries {
		streams = append(streams, map[string]any{
			"protocol_name": entry.Protocol,
			"format": []any{
				map[string]any{
					"format_name": entry.Format,
					"codec": []any{
						map[string]any{
							"codec_name": entry.Codec,
							"base_url":   entry.BaseURL,
							"current_qn": entry.Qn,
							"accept_qn":  entry.AcceptQn,
							"url_info": []any{
								map[string]any{"host": entry.Host, "extra": entry.Extra},
							},
						},
					},
				},
			},
		})
	}

	body := map[string]any{
		"code":    0,
		"message": "0",
		"ttl":     1,
		"data": map[string]any{
			"playurl_info": map[string]any{
				"playurl": map[string]any{"stream": streams},
			},
		},
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return encoded
}

// v2EmptyBody builds a successful response without any playurl information
// (the "0 URLs" case of FEATURES-V2 §6.1).
func v2EmptyBody() []byte {
	return []byte(`{"code":0,"message":"0","ttl":1,"data":{"playurl_info":null}}`)
}

// v2ErrorBody builds an API error response.
func v2ErrorBody(code int, message string) []byte {
	encoded, err := json.Marshal(map[string]any{"code": code, "message": message})
	if err != nil {
		panic(err)
	}
	return encoded
}

func flvEntry(host, path, extra string, qn int) fakeStreamEntry {
	return fakeStream("flv", "avc", host, path, extra, qn)
}

func urlsOf(infos []StreamURLInfo) []string {
	out := make([]string, 0, len(infos))
	for _, info := range infos {
		out = append(out, info.URL)
	}
	return out
}

// ---------------------------------------------------------------------------
// StreamFetchConfig normalization / option handling
// ---------------------------------------------------------------------------

func TestNormalizeStreamFetchConfig(t *testing.T) {
	t.Run("zero value stays nil", func(t *testing.T) {
		if got := normalizeStreamFetchConfig(StreamFetchConfig{}); got != nil {
			t.Fatalf("expected nil for empty config, got %+v", got)
		}
	})

	t.Run("blank entries are empty", func(t *testing.T) {
		cfg := StreamFetchConfig{
			APIHost:     "   ",
			CDN:         []string{"  ", ""},
			ReplaceCN01: []string{" "},
		}
		if got := normalizeStreamFetchConfig(cfg); got != nil {
			t.Fatalf("expected nil for blank config, got %+v", got)
		}
	})

	t.Run("trim and deduplicate", func(t *testing.T) {
		cfg := StreamFetchConfig{
			APIHost:         " https://api.example.com/ ",
			FallbackAPIHost: "https://fallback.example.com/",
			CDN:             []string{" line-a ", "line-b", "line-a", ""},
			CDNFallback:     true,
			ForceSource:     true,
			ReplaceCN01:     []string{"cn-a.example.com", " cn-b.example.com ", "cn-a.example.com"},
		}
		got := normalizeStreamFetchConfig(cfg)
		if got == nil {
			t.Fatal("expected normalized config, got nil")
		}
		if got.APIHost != "https://api.example.com" {
			t.Fatalf("APIHost = %q", got.APIHost)
		}
		if got.FallbackAPIHost != "https://fallback.example.com" {
			t.Fatalf("FallbackAPIHost = %q", got.FallbackAPIHost)
		}
		if strings.Join(got.CDN, ",") != "line-a,line-b" {
			t.Fatalf("CDN = %v", got.CDN)
		}
		if strings.Join(got.ReplaceCN01, ",") != "cn-a.example.com,cn-b.example.com" {
			t.Fatalf("ReplaceCN01 = %v", got.ReplaceCN01)
		}
		if !got.CDNFallback || !got.ForceSource {
			t.Fatalf("flags lost: %+v", got)
		}
	})

	t.Run("official host folds into empty config", func(t *testing.T) {
		cfg := normalizeStreamFetchConfig(StreamFetchConfig{APIHost: officialStreamAPIHost})
		if cfg != nil {
			t.Fatalf("official API host must be a no-op, got %+v", cfg)
		}
		cfg = normalizeStreamFetchConfig(StreamFetchConfig{
			APIHost: officialStreamAPIHost + "/",
			CDN:     []string{"line-a"},
		})
		if cfg == nil || cfg.APIHost != "" || len(cfg.CDN) != 1 {
			t.Fatalf("official host must fold to empty, got %+v", cfg)
		}
	})

	t.Run("invalid api host is ignored", func(t *testing.T) {
		for _, host := range []string{"://bad", "ftp://api.example.com", "api.example.com", "https://"} {
			got := normalizeStreamFetchConfig(StreamFetchConfig{APIHost: host})
			if got != nil {
				t.Fatalf("expected invalid host %q to be ignored, got %+v", host, got)
			}
		}
	})
}

func TestWithFetchConfigOption(t *testing.T) {
	opts := defaultGetStreamURLsOptions()
	if opts.fetch != nil {
		t.Fatal("default options must not carry a fetch config")
	}

	// Empty config must be a no-op (legacy behavior).
	WithFetchConfig(StreamFetchConfig{})(&opts)
	if opts.fetch != nil {
		t.Fatalf("zero config must leave fetch nil, got %+v", opts.fetch)
	}

	// Non-empty config is normalized and stored.
	WithFetchConfig(StreamFetchConfig{
		APIHost:     "https://api.example.com/",
		CDN:         []string{" cdna ", "cdna"},
		ForceSource: true,
	})(&opts)
	if opts.fetch == nil {
		t.Fatal("expected fetch config to be stored")
	}
	if opts.fetch.APIHost != "https://api.example.com" {
		t.Fatalf("APIHost = %q", opts.fetch.APIHost)
	}
	if len(opts.fetch.CDN) != 1 || opts.fetch.CDN[0] != "cdna" {
		t.Fatalf("CDN = %v", opts.fetch.CDN)
	}
	if !opts.fetch.ForceSource {
		t.Fatal("ForceSource lost")
	}
}

func TestStreamAPIHostCandidates(t *testing.T) {
	tests := []struct {
		name string
		cfg  *StreamFetchConfig
		want []string
	}{
		{
			name: "nil config uses official only",
			cfg:  nil,
			want: []string{officialStreamAPIHost},
		},
		{
			name: "empty config uses official only",
			cfg:  &StreamFetchConfig{},
			want: []string{officialStreamAPIHost},
		},
		{
			name: "custom primary then official safety net",
			cfg:  &StreamFetchConfig{APIHost: "https://primary.example.com"},
			want: []string{"https://primary.example.com", officialStreamAPIHost},
		},
		{
			name: "primary then fallback then official",
			cfg: &StreamFetchConfig{
				APIHost:         "https://primary.example.com",
				FallbackAPIHost: "https://fallback.example.com",
			},
			want: []string{"https://primary.example.com", "https://fallback.example.com", officialStreamAPIHost},
		},
		{
			name: "fallback only keeps official primary",
			cfg:  &StreamFetchConfig{FallbackAPIHost: "https://fallback.example.com"},
			want: []string{officialStreamAPIHost, "https://fallback.example.com"},
		},
		{
			name: "official primary is not duplicated",
			cfg:  &StreamFetchConfig{APIHost: officialStreamAPIHost},
			want: []string{officialStreamAPIHost},
		},
		{
			name: "trailing slash normalized",
			cfg:  &StreamFetchConfig{APIHost: "https://primary.example.com/"},
			want: []string{"https://primary.example.com", officialStreamAPIHost},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := streamAPIHostCandidates(test.cfg)
			if strings.Join(got, "|") != strings.Join(test.want, "|") {
				t.Fatalf("candidates = %v, want %v", got, test.want)
			}
		})
	}
}

func TestIsFallbackEligible(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "room not found", err: ErrRoomNotFound, want: false},
		{name: "wrapped room not found", err: fmt.Errorf("%w: x", ErrRoomNotFound), want: false},
		{name: "canceled", err: fmt.Errorf("x: %w", context.Canceled), want: false},
		{name: "deadline", err: fmt.Errorf("x: %w", context.DeadlineExceeded), want: false},
		{name: "sign error", err: &streamSignError{err: errors.New("boom")}, want: false},
		{name: "generic network error", err: errors.New("connection reset"), want: true},
		{name: "api error", err: &APIError{Code: -352, Message: "risk"}, want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isFallbackEligible(test.err); got != test.want {
				t.Fatalf("isFallbackEligible(%v) = %v, want %v", test.err, got, test.want)
			}
		})
	}
}

func TestStreamAPIURL(t *testing.T) {
	tests := map[string]string{
		officialStreamAPIHost:               officialStreamAPIHost + streamAPIPathV2,
		"https://api.example.com/":          "https://api.example.com" + streamAPIPathV2,
		"https://api.example.com/sub/path/": "https://api.example.com/sub/path" + streamAPIPathV2,
	}
	for host, want := range tests {
		if got := streamAPIURL(host); got != want {
			t.Fatalf("streamAPIURL(%q) = %q, want %q", host, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// Default behavior (empty config == legacy behavior)
// ---------------------------------------------------------------------------

func TestGetStreamURLsV2DefaultRequestUnchanged(t *testing.T) {
	server := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, v2StreamBody(
			flvEntry("https://cdn-a.example.com", "/live/a.flv", "?token=1", int(QualityOriginal)),
		))
	})
	client := newTestBilibiliClient(t, server.Server.Config.Handler.ServeHTTP)

	// No option at all.
	plain, err := client.GetStreamURLsV2(testRoomID)
	if err != nil {
		t.Fatalf("plain fetch: %v", err)
	}
	// Explicit empty config must be equivalent.
	empty, err := client.GetStreamURLsV2(testRoomID, WithFetchConfig(StreamFetchConfig{}))
	if err != nil {
		t.Fatalf("empty-config fetch: %v", err)
	}

	if len(plain) != 1 || len(empty) != 1 {
		t.Fatalf("unexpected result sizes: plain=%d empty=%d", len(plain), len(empty))
	}
	if plain[0].URL != empty[0].URL {
		t.Fatalf("empty config changed result: %q vs %q", plain[0].URL, empty[0].URL)
	}

	if got := server.requestCount(); got != 2 {
		t.Fatalf("expected 2 requests, got %d", got)
	}
	for i := 0; i < server.requestCount(); i++ {
		if path := server.pathAt(i); path != streamAPIPathV2 {
			t.Fatalf("request %d path = %q, want %q", i, path, streamAPIPathV2)
		}
		query := server.queryAt(i)
		if query.Get("cdn") != "" {
			t.Fatalf("request %d must not send cdn, got %q", i, query.Get("cdn"))
		}
		for _, key := range []string{"room_id", "qn", "no_playurl", "mask", "platform", "protocol", "format", "codec", "dolby", "panorama", "hdr_type", "web_location", "only_audio", "wts", "w_rid"} {
			if query.Get(key) == "" {
				t.Fatalf("request %d missing query param %q", i, key)
			}
		}
		if got := query.Get("room_id"); got != fmt.Sprint(testRoomID) {
			t.Fatalf("room_id = %q", got)
		}
		if got := query.Get("qn"); got != fmt.Sprint(int(QualityOriginal)) {
			t.Fatalf("qn = %q", got)
		}
		if got := query.Get("protocol"); got != "0,1" {
			t.Fatalf("protocol = %q", got)
		}
		if got := query.Get("format"); got != "0,1,2" {
			t.Fatalf("format = %q", got)
		}
		if got := query.Get("codec"); got != "0,1,2" {
			t.Fatalf("codec = %q", got)
		}
		if got := query.Get("only_audio"); got != "0" {
			t.Fatalf("only_audio = %q", got)
		}
	}

	// Existing sorting must be preserved.
	server2 := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, v2StreamBody(
			fakeStream("ts", "avc", "https://cdn-ts.example.com", "/live/a.m3u8", "?token=1", int(QualityOriginal)),
			flvEntry("https://cdn-flv.example.com", "/live/a.flv", "?token=1", int(QualityOriginal)),
		))
	})
	client2 := newTestBilibiliClient(t, server2.Server.Config.Handler.ServeHTTP)
	infos, err := client2.GetStreamURLsV2(testRoomID)
	if err != nil {
		t.Fatalf("sort fetch: %v", err)
	}
	formats := make([]string, 0, len(infos))
	for _, info := range infos {
		formats = append(formats, info.Format)
	}
	if strings.Join(formats, ",") != "flv,ts" {
		t.Fatalf("sorted formats = %v, want [flv ts]", formats)
	}
}

// ---------------------------------------------------------------------------
// Host fallback (§6.1)
// ---------------------------------------------------------------------------

func TestGetStreamURLsV2HostFallback(t *testing.T) {
	mediaHost := "https://cdn-fallback.example.com"
	goodBody := v2StreamBody(flvEntry(mediaHost, "/live/ok.flv", "?token=1", int(QualityOriginal)))

	t.Run("network error on primary", func(t *testing.T) {
		dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		deadURL := dead.URL
		dead.Close()

		fallback := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, goodBody)
		})
		client := newTestClientWithUnusedOfficial(t)

		infos, err := client.GetStreamURLsV2(testRoomID, WithFetchConfig(StreamFetchConfig{
			APIHost:         deadURL,
			FallbackAPIHost: fallback.URL,
		}))
		if err != nil {
			t.Fatalf("expected fallback to succeed, got %v", err)
		}
		if len(infos) != 1 || infos[0].URL != mediaHost+"/live/ok.flv?token=1" {
			t.Fatalf("unexpected infos: %+v", infos)
		}
		if got := fallback.requestCount(); got != 1 {
			t.Fatalf("fallback request count = %d", got)
		}
	})

	t.Run("http status on primary", func(t *testing.T) {
		primary := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		})
		fallback := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, goodBody)
		})
		client := newTestClientWithUnusedOfficial(t)

		infos, err := client.GetStreamURLsV2(testRoomID, WithFetchConfig(StreamFetchConfig{
			APIHost:         primary.URL,
			FallbackAPIHost: fallback.URL,
		}))
		if err != nil {
			t.Fatalf("expected fallback to succeed, got %v", err)
		}
		if len(infos) != 1 {
			t.Fatalf("unexpected infos: %+v", infos)
		}
		if primary.requestCount() != 1 || fallback.requestCount() != 1 {
			t.Fatalf("request counts primary=%d fallback=%d", primary.requestCount(), fallback.requestCount())
		}
	})

	t.Run("geo restriction on primary", func(t *testing.T) {
		primary := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, v2ErrorBody(streamAPICodeGeoRestricted, "geo restricted"))
		})
		fallback := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, goodBody)
		})
		client := newTestClientWithUnusedOfficial(t)

		if _, err := client.GetStreamURLsV2(testRoomID, WithFetchConfig(StreamFetchConfig{
			APIHost:         primary.URL,
			FallbackAPIHost: fallback.URL,
		})); err != nil {
			t.Fatalf("expected fallback to succeed, got %v", err)
		}
		if fallback.requestCount() != 1 {
			t.Fatalf("fallback request count = %d", fallback.requestCount())
		}
	})

	t.Run("empty urls on primary", func(t *testing.T) {
		primary := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, v2EmptyBody())
		})
		fallback := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, goodBody)
		})
		client := newTestClientWithUnusedOfficial(t)

		infos, err := client.GetStreamURLsV2(testRoomID, WithFetchConfig(StreamFetchConfig{
			APIHost:         primary.URL,
			FallbackAPIHost: fallback.URL,
		}))
		if err != nil {
			t.Fatalf("expected fallback to succeed, got %v", err)
		}
		if len(infos) != 1 {
			t.Fatalf("unexpected infos: %+v", infos)
		}
	})

	t.Run("api error on primary", func(t *testing.T) {
		primary := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, v2ErrorBody(-352, "risk control"))
		})
		fallback := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, goodBody)
		})
		client := newTestClientWithUnusedOfficial(t)

		if _, err := client.GetStreamURLsV2(testRoomID, WithFetchConfig(StreamFetchConfig{
			APIHost:         primary.URL,
			FallbackAPIHost: fallback.URL,
		})); err != nil {
			t.Fatalf("expected fallback to succeed, got %v", err)
		}
	})

	t.Run("room not found does not fall back", func(t *testing.T) {
		primary := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, v2ErrorBody(streamAPICodeRoomNotFoundV2, "room not found"))
		})
		fallback := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, goodBody)
		})
		client := newTestClientWithUnusedOfficial(t)

		_, err := client.GetStreamURLsV2(testRoomID, WithFetchConfig(StreamFetchConfig{
			APIHost:         primary.URL,
			FallbackAPIHost: fallback.URL,
		}))
		if !errors.Is(err, ErrRoomNotFound) {
			t.Fatalf("expected ErrRoomNotFound, got %v", err)
		}
		if fallback.requestCount() != 0 {
			t.Fatalf("fallback must not be attempted, got %d requests", fallback.requestCount())
		}
	})

	t.Run("custom primary without fallback still reaches official host", func(t *testing.T) {
		dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		deadURL := dead.URL
		dead.Close()

		// official fake server is wired through the test transport
		client := newTestBilibiliClient(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, goodBody)
		})

		infos, err := client.GetStreamURLsV2(testRoomID, WithFetchConfig(StreamFetchConfig{
			APIHost: deadURL,
		}))
		if err != nil {
			t.Fatalf("expected official safety net to succeed, got %v", err)
		}
		if len(infos) != 1 {
			t.Fatalf("unexpected infos: %+v", infos)
		}
	})

	t.Run("all hosts fail returns error", func(t *testing.T) {
		primary := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "primary boom", http.StatusInternalServerError)
		})
		fallback := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "fallback boom", http.StatusInternalServerError)
		})
		client := newTestBilibiliClient(t, func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "official boom", http.StatusInternalServerError)
		})

		_, err := client.GetStreamURLsV2(testRoomID, WithFetchConfig(StreamFetchConfig{
			APIHost:         primary.URL,
			FallbackAPIHost: fallback.URL,
		}))
		if err == nil {
			t.Fatal("expected error when every host fails")
		}
		if !strings.Contains(err.Error(), "状态码") {
			t.Fatalf("unexpected error: %v", err)
		}
		if primary.requestCount() != 1 || fallback.requestCount() != 1 {
			t.Fatalf("request counts primary=%d fallback=%d", primary.requestCount(), fallback.requestCount())
		}
	})

	t.Run("invalid custom host falls back to official", func(t *testing.T) {
		client := newTestBilibiliClient(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, goodBody)
		})
		infos, err := client.GetStreamURLsV2(testRoomID, WithFetchConfig(StreamFetchConfig{
			APIHost: "not-a-url",
		}))
		if err != nil {
			t.Fatalf("expected official fallback, got %v", err)
		}
		if len(infos) != 1 {
			t.Fatalf("unexpected infos: %+v", infos)
		}
	})
}

// ---------------------------------------------------------------------------
// CDN handling (§6.2)
// ---------------------------------------------------------------------------

func TestGetStreamURLsV2CDNIteration(t *testing.T) {
	t.Run("iterates cdn list until first success", func(t *testing.T) {
		server := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Query().Get("cdn") {
			case "cdn-a":
				writeJSON(w, v2EmptyBody())
			case "cdn-b":
				writeJSON(w, v2StreamBody(flvEntry("https://cdn-b.example.com", "/live/b.flv", "", int(QualityOriginal))))
			default:
				http.Error(w, "unexpected cdn", http.StatusBadRequest)
			}
		})
		client := newTestClientWithUnusedOfficial(t)

		infos, err := client.GetStreamURLsV2(testRoomID, WithFetchConfig(StreamFetchConfig{
			APIHost: server.URL,
			CDN:     []string{"cdn-a", "cdn-b"},
		}))
		if err != nil {
			t.Fatalf("fetch: %v", err)
		}
		if len(infos) != 1 || !strings.Contains(infos[0].URL, "cdn-b.example.com") {
			t.Fatalf("unexpected infos: %+v", infos)
		}
		if got := strings.Join(server.cdnParams(), ","); got != "cdn-a,cdn-b" {
			t.Fatalf("cdn params = %q", got)
		}
	})

	t.Run("stops at first successful cdn", func(t *testing.T) {
		server := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, v2StreamBody(flvEntry("https://cdn-a.example.com", "/live/a.flv", "", int(QualityOriginal))))
		})
		client := newTestClientWithUnusedOfficial(t)

		if _, err := client.GetStreamURLsV2(testRoomID, WithFetchConfig(StreamFetchConfig{
			APIHost: server.URL,
			CDN:     []string{"cdn-a", "cdn-b", "cdn-c"},
		})); err != nil {
			t.Fatalf("fetch: %v", err)
		}
		if got := server.requestCount(); got != 1 {
			t.Fatalf("expected 1 request, got %d", got)
		}
		if got := server.cdnParams()[0]; got != "cdn-a" {
			t.Fatalf("cdn param = %q", got)
		}
	})

	t.Run("cdn fallback collects and de-duplicates", func(t *testing.T) {
		shared := flvEntry("https://shared.example.com", "/live/shared.flv", "", int(QualityOriginal))
		server := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Query().Get("cdn") {
			case "cdn-a":
				writeJSON(w, v2StreamBody(
					flvEntry("https://a1.example.com", "/live/a1.flv", "", int(QualityOriginal)),
					shared,
				))
			case "cdn-b":
				writeJSON(w, v2StreamBody(
					shared,
					flvEntry("https://b1.example.com", "/live/b1.flv", "", int(QualityOriginal)),
				))
			default:
				http.Error(w, "unexpected cdn", http.StatusBadRequest)
			}
		})
		client := newTestClientWithUnusedOfficial(t)

		infos, err := client.GetStreamURLsV2(testRoomID, WithFetchConfig(StreamFetchConfig{
			APIHost:     server.URL,
			CDN:         []string{"cdn-a", "cdn-b"},
			CDNFallback: true,
		}))
		if err != nil {
			t.Fatalf("fetch: %v", err)
		}
		got := urlsOf(infos)
		want := []string{
			"https://a1.example.com/live/a1.flv",
			"https://shared.example.com/live/shared.flv",
			"https://b1.example.com/live/b1.flv",
		}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("collected urls = %v, want %v", got, want)
		}
	})

	t.Run("all cdns empty returns empty without error", func(t *testing.T) {
		server := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, v2EmptyBody())
		})
		client := newTestClientWithUnusedOfficial(t)

		infos, err := client.GetStreamURLsV2(testRoomID, WithFetchConfig(StreamFetchConfig{
			APIHost: server.URL,
			CDN:     []string{"cdn-a", "cdn-b"},
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(infos) != 0 {
			t.Fatalf("expected no infos, got %+v", infos)
		}
		if got := server.requestCount(); got != 2 {
			t.Fatalf("expected 2 requests, got %d", got)
		}
	})
}

// ---------------------------------------------------------------------------
// force_source profile preference (§6.3)
// ---------------------------------------------------------------------------

func TestGetStreamURLsV2ForceSourcePreference(t *testing.T) {
	defaultOrderBody := v2StreamBody(
		fakeStream("ts", "avc", "https://cdn-ts.example.com", "/live/a.m3u8", "", int(QualityOriginal)),
		fakeStream("fmp4", "avc", "https://cdn-fmp4.example.com", "/live/a.m3u8", "", int(Quality4K)),
		flvEntry("https://cdn-flv.example.com", "/live/a.flv", "", int(QualityOriginal)),
	)

	t.Run("without force_source legacy order is kept", func(t *testing.T) {
		server := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, defaultOrderBody)
		})
		client := newTestBilibiliClient(t, server.Server.Config.Handler.ServeHTTP)

		infos, err := client.GetStreamURLsV2(testRoomID)
		if err != nil {
			t.Fatalf("fetch: %v", err)
		}
		formats := make([]string, 0, len(infos))
		for _, info := range infos {
			formats = append(formats, info.Format)
		}
		if strings.Join(formats, ",") != "flv,ts,fmp4" {
			t.Fatalf("formats = %v, want [flv ts fmp4]", formats)
		}
	})

	t.Run("force_source prefers ts/fmp4 original quality", func(t *testing.T) {
		server := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, defaultOrderBody)
		})
		client := newTestClientWithUnusedOfficial(t)

		infos, err := client.GetStreamURLsV2(testRoomID, WithFetchConfig(StreamFetchConfig{APIHost: server.URL, ForceSource: true}))
		if err != nil {
			t.Fatalf("fetch: %v", err)
		}
		formats := make([]string, 0, len(infos))
		for _, info := range infos {
			formats = append(formats, info.Format)
		}
		if strings.Join(formats, ",") != "ts,fmp4,flv" {
			t.Fatalf("formats = %v, want [ts fmp4 flv]", formats)
		}
	})

	t.Run("force_source respects profile allow-list", func(t *testing.T) {
		server := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, defaultOrderBody)
		})
		client := newTestClientWithUnusedOfficial(t)

		infos, err := client.GetStreamURLsV2(testRoomID,
			WithProfiles(ProfileHTTPFLV),
			WithFetchConfig(StreamFetchConfig{APIHost: server.URL, ForceSource: true}),
		)
		if err != nil {
			t.Fatalf("fetch: %v", err)
		}
		if len(infos) != 1 || infos[0].Format != "flv" {
			t.Fatalf("unexpected infos: %+v", infos)
		}
	})

	t.Run("force_source ignores sub-original quality", func(t *testing.T) {
		server := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, v2StreamBody(
				fakeStream("ts", "avc", "https://cdn-ts.example.com", "/live/a.m3u8", "", int(QualityBluRay)),
				flvEntry("https://cdn-flv.example.com", "/live/a.flv", "", int(QualityBluRay)),
			))
		})
		client := newTestClientWithUnusedOfficial(t)

		infos, err := client.GetStreamURLsV2(testRoomID, WithFetchConfig(StreamFetchConfig{APIHost: server.URL, ForceSource: true}))
		if err != nil {
			t.Fatalf("fetch: %v", err)
		}
		formats := make([]string, 0, len(infos))
		for _, info := range infos {
			formats = append(formats, info.Format)
		}
		if strings.Join(formats, ",") != "flv,ts" {
			t.Fatalf("formats = %v, want [flv ts]", formats)
		}
	})
}

func TestApplyForceSourcePreference(t *testing.T) {
	streams := []StreamURLInfo{
		{Format: "flv", Qn: 10000, URL: "flv"},
		{Format: "ts", Qn: 10000, URL: "ts"},
		{Format: "fmp4", Qn: 20000, URL: "fmp4"},
	}
	options := defaultGetStreamURLsOptions()

	if got := applyForceSourcePreference(streams, options, nil); urlsOf(got)[0] != "flv" {
		t.Fatalf("nil config must not reorder: %v", urlsOf(got))
	}
	if got := applyForceSourcePreference(streams, options, &StreamFetchConfig{}); urlsOf(got)[0] != "flv" {
		t.Fatalf("force off must not reorder: %v", urlsOf(got))
	}
	if got := applyForceSourcePreference(streams, options, &StreamFetchConfig{ForceSource: true}); strings.Join(urlsOf(got), ",") != "ts,fmp4,flv" {
		t.Fatalf("force reorder = %v", urlsOf(got))
	}

	hlsOnly := defaultGetStreamURLsOptions()
	hlsOnly.profiles = []StreamProfile{ProfileHLSTS}
	if got := applyForceSourcePreference(streams, hlsOnly, &StreamFetchConfig{ForceSource: true}); strings.Join(urlsOf(got), ",") != "ts,fmp4,flv" {
		t.Fatalf("hls allow-list reorder = %v", urlsOf(got))
	}

	flvOnly := defaultGetStreamURLsOptions()
	flvOnly.profiles = []StreamProfile{ProfileHTTPFLV}
	if got := applyForceSourcePreference(streams, flvOnly, &StreamFetchConfig{ForceSource: true}); strings.Join(urlsOf(got), ",") != "flv,ts,fmp4" {
		t.Fatalf("flv-only allow-list must not reorder: %v", urlsOf(got))
	}

	single := []StreamURLInfo{{Format: "flv", Qn: 10000, URL: "only"}}
	if got := applyForceSourcePreference(single, options, &StreamFetchConfig{ForceSource: true}); len(got) != 1 {
		t.Fatalf("single stream changed: %v", urlsOf(got))
	}
}

// ---------------------------------------------------------------------------
// cn-gotcha01 host replacement (§6.3)
// ---------------------------------------------------------------------------

const cn01MediaURL = "http://original-cn-gotcha01.bilivideo.com/live/stream.flv?token=abc"

func cn01MediaBody() []byte {
	return v2StreamBody(fakeStream("flv", "avc", "http://original-cn-gotcha01.bilivideo.com", "/live/stream.flv", "?token=abc", int(QualityOriginal)))
}

func TestGetStreamURLsV2CN01Replacement(t *testing.T) {
	t.Run("replaces host with first replying candidate", func(t *testing.T) {
		probe := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Range"); got != "bytes=0-1023" {
				t.Errorf("probe Range header = %q", got)
			}
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte("0123456789"))
		})
		api := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, cn01MediaBody())
		})
		client := newTestClientWithUnusedOfficial(t)

		candidate := strings.TrimPrefix(probe.URL, "http://")
		infos, err := client.GetStreamURLsV2(testRoomID, WithFetchConfig(StreamFetchConfig{
			APIHost:     api.URL,
			ForceSource: true,
			ReplaceCN01: []string{candidate},
		}))
		if err != nil {
			t.Fatalf("fetch: %v", err)
		}
		want := "http://" + candidate + "/live/stream.flv?token=abc"
		if len(infos) != 1 || infos[0].URL != want {
			t.Fatalf("infos = %v, want %q", urlsOf(infos), want)
		}
		if probe.requestCount() != 1 {
			t.Fatalf("probe request count = %d", probe.requestCount())
		}
		if got := probe.headerAt(0).Get("User-Agent"); got == "" {
			t.Fatal("probe missing User-Agent header")
		}
	})

	t.Run("applies without force_source", func(t *testing.T) {
		probe := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusPartialContent)
		})
		api := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, cn01MediaBody())
		})
		client := newTestClientWithUnusedOfficial(t)

		candidate := strings.TrimPrefix(probe.URL, "http://")
		infos, err := client.GetStreamURLsV2(testRoomID, WithFetchConfig(StreamFetchConfig{
			APIHost:     api.URL,
			ReplaceCN01: []string{candidate},
		}))
		if err != nil {
			t.Fatalf("fetch: %v", err)
		}
		want := "http://" + candidate + "/live/stream.flv?token=abc"
		if len(infos) != 1 || infos[0].URL != want {
			t.Fatalf("infos = %v, want %q", urlsOf(infos), want)
		}
		if probe.requestCount() != 1 {
			t.Fatalf("probe request count = %d", probe.requestCount())
		}
	})

	t.Run("empty replace_cn01 keeps original", func(t *testing.T) {
		api := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, cn01MediaBody())
		})
		client := newTestClientWithUnusedOfficial(t)

		infos, err := client.GetStreamURLsV2(testRoomID, WithFetchConfig(StreamFetchConfig{
			APIHost:     api.URL,
			ForceSource: true,
		}))
		if err != nil {
			t.Fatalf("fetch: %v", err)
		}
		if len(infos) != 1 || infos[0].URL != cn01MediaURL {
			t.Fatalf("URL must stay original, got %v", urlsOf(infos))
		}
	})

	t.Run("non cn-gotcha01 host is untouched", func(t *testing.T) {
		probe := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusPartialContent)
		})
		api := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, v2StreamBody(flvEntry("https://cdn-normal.example.com", "/live/a.flv", "", int(QualityOriginal))))
		})
		client := newTestClientWithUnusedOfficial(t)

		infos, err := client.GetStreamURLsV2(testRoomID, WithFetchConfig(StreamFetchConfig{
			APIHost:     api.URL,
			ForceSource: true,
			ReplaceCN01: []string{strings.TrimPrefix(probe.URL, "http://")},
		}))
		if err != nil {
			t.Fatalf("fetch: %v", err)
		}
		if len(infos) != 1 || infos[0].URL != "https://cdn-normal.example.com/live/a.flv" {
			t.Fatalf("URL changed: %v", urlsOf(infos))
		}
		if probe.requestCount() != 0 {
			t.Fatalf("probe must not run for non cn01 hosts, got %d", probe.requestCount())
		}
	})

	t.Run("keeps original URL when probe fails", func(t *testing.T) {
		probe := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "no", http.StatusInternalServerError)
		})
		api := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, cn01MediaBody())
		})
		client := newTestClientWithUnusedOfficial(t)

		infos, err := client.GetStreamURLsV2(testRoomID, WithFetchConfig(StreamFetchConfig{
			APIHost:     api.URL,
			ForceSource: true,
			ReplaceCN01: []string{strings.TrimPrefix(probe.URL, "http://")},
		}))
		if err != nil {
			t.Fatalf("fetch: %v", err)
		}
		if len(infos) != 1 || infos[0].URL != cn01MediaURL {
			t.Fatalf("URL must stay original, got %v", urlsOf(infos))
		}
	})

	t.Run("keeps original URL when probe timeouts", func(t *testing.T) {
		probe := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(300 * time.Millisecond)
			w.WriteHeader(http.StatusPartialContent)
		})
		api := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, cn01MediaBody())
		})
		client := newTestClientWithUnusedOfficial(t)
		client.cn01ProbeTimeout = 50 * time.Millisecond

		start := time.Now()
		infos, err := client.GetStreamURLsV2(testRoomID, WithFetchConfig(StreamFetchConfig{
			APIHost:     api.URL,
			ForceSource: true,
			ReplaceCN01: []string{strings.TrimPrefix(probe.URL, "http://")},
		}))
		if err != nil {
			t.Fatalf("fetch: %v", err)
		}
		if infos[0].URL != cn01MediaURL {
			t.Fatalf("URL must stay original, got %v", urlsOf(infos))
		}
		if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
			t.Fatalf("probe did not respect timeout, took %v", elapsed)
		}
	})

	t.Run("falls through to a working candidate", func(t *testing.T) {
		badProbe := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "no", http.StatusInternalServerError)
		})
		goodProbe := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusPartialContent)
		})
		api := newTestAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, cn01MediaBody())
		})
		client := newTestClientWithUnusedOfficial(t)

		badCandidate := strings.TrimPrefix(badProbe.URL, "http://")
		goodCandidate := strings.TrimPrefix(goodProbe.URL, "http://")
		infos, err := client.GetStreamURLsV2(testRoomID, WithFetchConfig(StreamFetchConfig{
			APIHost:     api.URL,
			ForceSource: true,
			ReplaceCN01: []string{badCandidate, goodCandidate},
		}))
		if err != nil {
			t.Fatalf("fetch: %v", err)
		}
		want := "http://" + goodCandidate + "/live/stream.flv?token=abc"
		if infos[0].URL != want {
			t.Fatalf("URL = %q, want %q", infos[0].URL, want)
		}
	})
}

func TestURLWithReplacedHost(t *testing.T) {
	base, err := url.Parse("http://original-cn-gotcha01.bilivideo.com/live/a.flv?token=1")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		candidate string
		want      string
		wantErr   bool
	}{
		{
			name:      "host only",
			candidate: "alt-cn-gotcha01.bilivideo.com",
			want:      "http://alt-cn-gotcha01.bilivideo.com/live/a.flv?token=1",
		},
		{
			name:      "host and port",
			candidate: "127.0.0.1:8080",
			want:      "http://127.0.0.1:8080/live/a.flv?token=1",
		},
		{
			name:      "scheme and host",
			candidate: "https://secure.example.com",
			want:      "https://secure.example.com/live/a.flv?token=1",
		},
		{name: "empty", candidate: "  ", wantErr: true},
		{name: "invalid scheme url", candidate: "http://", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := urlWithReplacedHost(base, test.candidate)
			if test.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Misc
// ---------------------------------------------------------------------------

func TestAppendUniqueStreamURLInfos(t *testing.T) {
	a := StreamURLInfo{URL: "a"}
	b := StreamURLInfo{URL: "b"}
	got := appendUniqueStreamURLInfos([]StreamURLInfo{a}, []StreamURLInfo{a, b, b})
	if strings.Join(urlsOf(got), ",") != "a,b" {
		t.Fatalf("unique urls = %v", urlsOf(got))
	}
}

func TestBuildStreamURLInfosAudioOnlyFilter(t *testing.T) {
	body := v2StreamBody(
		fakeStream("flv", "avc", "https://cdn.example.com", "/live/video.flv", "", int(QualityOriginal)),
		fakeStream("flv", "avc", "https://cdn.example.com", "/live/audio.flv", "?ptype=1", int(QualityOriginal)),
	)
	var sr StreamResponseV2
	if err := json.Unmarshal(body, &sr); err != nil {
		t.Fatal(err)
	}

	videoOnly := buildStreamURLInfos(sr, defaultGetStreamURLsOptions())
	if len(videoOnly) != 1 || videoOnly[0].IsAudioOnly {
		t.Fatalf("video-only infos = %+v", videoOnly)
	}

	options := defaultGetStreamURLsOptions()
	options.onlyAudio = true
	audioOnly := buildStreamURLInfos(sr, options)
	if len(audioOnly) != 1 || !audioOnly[0].IsAudioOnly {
		t.Fatalf("audio-only infos = %+v", audioOnly)
	}
}
