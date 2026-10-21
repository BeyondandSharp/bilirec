// SPDX-License-Identifier: GPL-3.0-only

package downloader

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

func TestParseKind(t *testing.T) {
	tests := []struct {
		raw     string
		want    Kind
		wantErr bool
	}{
		{raw: "native", want: KindNative},
		{raw: "FFmpeg", want: KindFFmpeg},
		{raw: " streamlink ", want: KindStreamlink},
		{raw: "auto", wantErr: true},
		{raw: "", wantErr: true},
		{raw: "youtube-dl", wantErr: true},
	}
	for _, tt := range tests {
		kind, err := ParseKind(tt.raw)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("ParseKind(%q): expected error", tt.raw)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseKind(%q): %v", tt.raw, err)
		}
		if kind != tt.want {
			t.Fatalf("ParseKind(%q) = %q, want %q", tt.raw, kind, tt.want)
		}
	}
}

func TestKindIsExternal(t *testing.T) {
	if KindNative.IsExternal() {
		t.Fatal("native must not be external")
	}
	if !KindFFmpeg.IsExternal() || !KindStreamlink.IsExternal() {
		t.Fatal("ffmpeg/streamlink must be external")
	}
}

func TestOrderedHeaders(t *testing.T) {
	headers := map[string]string{
		"Origin":     "https://live.bilibili.com",
		"Referer":    "https://live.bilibili.com/",
		"User-Agent": "UA/1.0",
		"Cookie":     "SESSDATA=abc; buvid3=def",
		"X-Extra":    "extra-value",
		"X-Empty":    "",
	}
	got := orderedHeaders(headers)
	want := [][2]string{
		{"Cookie", "SESSDATA=abc; buvid3=def"},
		{"User-Agent", "UA/1.0"},
		{"Referer", "https://live.bilibili.com/"},
		{"Origin", "https://live.bilibili.com"},
		{"X-Extra", "extra-value"},
	}
	if len(got) != len(want) {
		t.Fatalf("orderedHeaders len = %d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("orderedHeaders[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestHeaderLines(t *testing.T) {
	headers := map[string]string{
		"Cookie":     "SESSDATA=abc",
		"User-Agent": "UA/1.0",
		"Referer":    "https://live.bilibili.com/",
		"Origin":     "https://live.bilibili.com",
	}
	want := "Cookie: SESSDATA=abc\r\n" +
		"User-Agent: UA/1.0\r\n" +
		"Referer: https://live.bilibili.com/\r\n" +
		"Origin: https://live.bilibili.com\r\n"
	if got := headerLines(headers); got != want {
		t.Fatalf("headerLines = %q, want %q", got, want)
	}
	if got := headerLines(nil); got != "" {
		t.Fatalf("headerLines(nil) = %q, want empty", got)
	}
	if got := headerLines(map[string]string{"Cookie": ""}); got != "" {
		t.Fatalf("headerLines(empty values) = %q, want empty", got)
	}
}

func TestStreamlinkHeaderArgs(t *testing.T) {
	headers := map[string]string{
		"Cookie":     "SESSDATA=abc",
		"User-Agent": "UA/1.0",
	}
	want := []string{"--http-header", "Cookie=SESSDATA=abc", "--http-header", "User-Agent=UA/1.0"}
	got := streamlinkHeaderArgs(headers)
	if len(got) != len(want) {
		t.Fatalf("streamlinkHeaderArgs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("streamlinkHeaderArgs[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestFFmpegArgs(t *testing.T) {
	spec := Spec{
		URL:    "https://cdn.example/live/abc.flv?wsSecret=sig",
		Format: "flv",
		Headers: map[string]string{
			"Cookie":     "SESSDATA=abc",
			"User-Agent": "UA/1.0",
			"Referer":    "https://live.bilibili.com/",
			"Origin":     "https://live.bilibili.com",
		},
	}
	want := []string{
		"-loglevel", "error", "-y",
		"-headers", "Cookie: SESSDATA=abc\r\nUser-Agent: UA/1.0\r\nReferer: https://live.bilibili.com/\r\nOrigin: https://live.bilibili.com\r\n",
		"-rw_timeout", "20000000",
		"-fflags", "+genpts",
		"-i", "https://cdn.example/live/abc.flv?wsSecret=sig",
		"-c", "copy",
		"-reset_timestamps", "1",
		"-avoid_negative_ts", "make_zero",
		"-f", "flv",
		"-",
	}
	assertArgs(t, ffmpegArgs(spec), want)
}

func TestFFmpegArgsHLS(t *testing.T) {
	spec := Spec{URL: "https://cdn.example/live/index.m3u8", Format: "ts"}
	got := ffmpegArgs(spec)
	if !containsPair(got, "-max_reload", "1000") {
		t.Fatalf("expected -max_reload 1000 for HLS, got %v", got)
	}
	fmp4 := ffmpegArgs(Spec{URL: "https://cdn.example/live/index.m3u8", Format: "fmp4"})
	if !containsPair(fmp4, "-max_reload", "1000") {
		t.Fatalf("expected -max_reload 1000 for fmp4, got %v", fmp4)
	}
	progressive := ffmpegArgs(Spec{URL: "https://cdn.example/live/abc.flv", Format: "flv"})
	if containsPair(progressive, "-max_reload", "1000") {
		t.Fatalf("did not expect -max_reload for progressive flv, got %v", progressive)
	}
	for _, arg := range progressive {
		if arg == "-headers" {
			t.Fatalf("did not expect -headers without headers, got %v", progressive)
		}
	}
}

func TestStreamlinkArgs(t *testing.T) {
	spec := Spec{
		URL:    "https://cdn.example/live/abc.flv?wsSecret=sig",
		Format: "flv",
		Headers: map[string]string{
			"Cookie":     "SESSDATA=abc",
			"User-Agent": "UA/1.0",
			"Referer":    "https://live.bilibili.com/",
			"Origin":     "https://live.bilibili.com",
		},
	}
	want := []string{
		"--stream-segment-threads", "3",
		"--hls-playlist-reload-attempts", "1",
		"--http-header", "Cookie=SESSDATA=abc",
		"--http-header", "User-Agent=UA/1.0",
		"--http-header", "Referer=https://live.bilibili.com/",
		"--http-header", "Origin=https://live.bilibili.com",
		"httpstream://https://cdn.example/live/abc.flv?wsSecret=sig",
		"best",
		"-O",
	}
	assertArgs(t, streamlinkArgs(spec), want)

	hls := streamlinkArgs(Spec{URL: "https://cdn.example/live/index.m3u8", Format: "ts"})
	assertArgsLast(t, hls, "https://cdn.example/live/index.m3u8")
	if !containsPair(hls, "--stream-segment-threads", "3") {
		t.Fatalf("expected segment threads flag, got %v", hls)
	}
}

func TestStreamlinkFFmpegArgs(t *testing.T) {
	want := []string{
		"-loglevel", "error",
		"-y",
		"-fflags", "+genpts",
		"-i", "pipe:0",
		"-c", "copy",
		"-reset_timestamps", "1",
		"-avoid_negative_ts", "make_zero",
		"-f", "flv",
		"-",
	}
	assertArgs(t, streamlinkFFmpegArgs(), want)
}

func TestRegistry(t *testing.T) {
	r := NewRegistry(Options{})
	if r.Get(KindFFmpeg) == nil || r.Get(KindStreamlink) == nil {
		t.Fatal("registry must provide ffmpeg and streamlink downloaders")
	}
	if r.Get(KindNative) != nil {
		t.Fatal("registry must not provide a native downloader")
	}
	var nilRegistry *Registry
	if nilRegistry.Get(KindFFmpeg) != nil {
		t.Fatal("nil registry Get must return nil")
	}

	fake := &stubDownloader{name: "fake"}
	r.Register(KindFFmpeg, fake)
	if r.Get(KindFFmpeg) != fake {
		t.Fatal("Register did not replace downloader")
	}
}

func TestOptionsDefaults(t *testing.T) {
	opts := Options{}.withDefaults()
	if opts.FFmpegBin != "ffmpeg" || opts.StreamlinkBin != "streamlink" {
		t.Fatalf("unexpected default bins: %+v", opts)
	}
	if opts.StartTimeout != 20*time.Second {
		t.Fatalf("unexpected default timeout: %v", opts.StartTimeout)
	}
	custom := Options{FFmpegBin: "/opt/ffmpeg", StreamlinkBin: "/opt/streamlink", StartTimeout: time.Second}.withDefaults()
	if custom.StartTimeout != time.Second {
		t.Fatalf("custom timeout not preserved: %v", custom.StartTimeout)
	}
}

type stubDownloader struct{ name string }

func (s *stubDownloader) Name() string { return s.name }
func (s *stubDownloader) Open(_ context.Context, _ Spec) (io.ReadCloser, error) {
	return nil, errors.New("stub")
}

func assertArgs(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("args len = %d, want %d\ngot:  %q\nwant: %q", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q\ngot:  %q", i, got[i], want[i], got)
		}
	}
}

func assertArgsLast(t *testing.T, args []string, wantLastURL string) {
	t.Helper()
	if len(args) < 3 {
		t.Fatalf("unexpected args: %v", args)
	}
	if args[len(args)-3] != wantLastURL {
		t.Fatalf("args %v: url = %q, want %q", args, args[len(args)-3], wantLastURL)
	}
	if args[len(args)-2] != "best" || args[len(args)-1] != "-O" {
		t.Fatalf("args %v: expected trailing \"best -O\"", args)
	}
}

func containsPair(args []string, key, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == key && args[i+1] == value {
			return true
		}
	}
	return false
}
