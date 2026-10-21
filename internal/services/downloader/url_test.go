// SPDX-License-Identifier: GPL-3.0-only

package downloader

import "testing"

func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{
			name: "progressive flv gets httpstream prefix",
			url:  "https://cdn.example/live/abc.flv?wsSecret=sig&wsTime=1",
			want: "httpstream://https://cdn.example/live/abc.flv?wsSecret=sig&wsTime=1",
		},
		{
			name: "progressive mp4 gets httpstream prefix",
			url:  "http://cdn.example/a.mp4",
			want: "httpstream://http://cdn.example/a.mp4",
		},
		{
			name: "progressive ts gets httpstream prefix",
			url:  "https://cdn.example/a.ts?token=1",
			want: "httpstream://https://cdn.example/a.ts?token=1",
		},
		{
			name: "uppercase extension normalizes",
			url:  "https://cdn.example/LIVE.FLV",
			want: "httpstream://https://cdn.example/LIVE.FLV",
		},
		{
			name: "hls manifest unchanged",
			url:  "https://cdn.example/live.m3u8?token=1",
			want: "https://cdn.example/live.m3u8?token=1",
		},
		{
			name: "dash manifest unchanged",
			url:  "https://cdn.example/manifest.mpd",
			want: "https://cdn.example/manifest.mpd",
		},
		{
			name: "existing httpstream prefix not doubled",
			url:  "httpstream://https://cdn.example/live.flv",
			want: "httpstream://https://cdn.example/live.flv",
		},
		{
			name: "existing hls prefix unchanged",
			url:  "hls://https://cdn.example/live.m3u8",
			want: "hls://https://cdn.example/live.m3u8",
		},
		{
			name: "existing dash prefix unchanged",
			url:  "dash://https://cdn.example/manifest.mpd",
			want: "dash://https://cdn.example/manifest.mpd",
		},
		{
			name: "unknown plugin protocol with http target unchanged",
			url:  "custom://https://cdn.example/live.flv",
			want: "custom://https://cdn.example/live.flv",
		},
		{
			name: "webpage url unchanged",
			url:  "https://www.twitch.tv/example",
			want: "https://www.twitch.tv/example",
		},
		{
			name: "query url without media extension unchanged",
			url:  "https://www.youtube.com/watch?v=abc",
			want: "https://www.youtube.com/watch?v=abc",
		},
		{
			name: "non http scheme unchanged",
			url:  "rtmp://cdn.example/live",
			want: "rtmp://cdn.example/live",
		},
		{
			name: "empty url unchanged",
			url:  "",
			want: "",
		},
		{
			name: "path dot segment does not produce extension",
			url:  "https://cdn.example/v1.2/stream",
			want: "https://cdn.example/v1.2/stream",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeURL(tt.url); got != tt.want {
				t.Fatalf("NormalizeURL(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}
