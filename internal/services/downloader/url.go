// SPDX-License-Identifier: GPL-3.0-only

package downloader

import (
	"net/url"
	"path"
	"strings"
)

// NormalizeURL rewrites an already-resolved progressive HTTP(S) media URL so
// that streamlink's built-in http plugin matches it. Streamlink treats a bare
// positional URL as a plugin URL: HLS (.m3u8) and DASH (.mpd) are auto-detected
// without a prefix, but progressive HTTP bodies (e.g. bilibili .flv CDN links)
// need an explicit `httpstream://` scheme, otherwise streamlink exits with
// "No plugin can handle URL".
//
// URLs that already carry a streamlink protocol prefix and webpage URLs
// without a media extension are left unchanged. Derived from biliup
// (crates/biliup-cli/src/server/core/downloader/streamlink.rs, MIT).
func NormalizeURL(rawURL string) string {
	if hasStreamlinkProtocolPrefix(rawURL) {
		return rawURL
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return rawURL
	}

	ext := mediaExtension(rawURL)
	switch ext {
	case "m3u8", "mpd":
		// Streamlink auto-detects these without an explicit protocol prefix.
		return rawURL
	}
	if isProgressiveHTTPExt(ext) {
		return "httpstream://" + rawURL
	}
	return rawURL
}

func hasStreamlinkProtocolPrefix(rawURL string) bool {
	lower := strings.ToLower(rawURL)
	for _, prefix := range []string{"httpstream://", "hls://", "dash://"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	// Accept any `foo://http(s)://...` form so we never double-prefix.
	if _, rest, ok := strings.Cut(lower, "://"); ok {
		return strings.HasPrefix(rest, "http://") || strings.HasPrefix(rest, "https://")
	}
	return false
}

func mediaExtension(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return mediaExtensionFromString(rawURL)
	}
	seg := path.Base(parsed.Path)
	if seg == "" || seg == "/" || seg == "." {
		return ""
	}
	ext := extensionOf(seg)
	if ext == "" {
		return ""
	}
	return strings.ToLower(ext)
}

func mediaExtensionFromString(rawURL string) string {
	beforeQuery, _, _ := strings.Cut(rawURL, "?")
	beforeQuery, _, _ = strings.Cut(beforeQuery, "#")
	return extensionOf(beforeQuery)
}

func extensionOf(name string) string {
	idx := strings.LastIndexByte(name, '.')
	if idx < 0 || idx == len(name)-1 {
		return ""
	}
	ext := name[idx+1:]
	if ext == "" {
		return ""
	}
	for i := 0; i < len(ext); i++ {
		c := ext[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			continue
		}
		return ""
	}
	return ext
}

// isProgressiveHTTPExt lists container/segment extensions that streamlink's
// http plugin consumes as a progressive HTTP body (HLS/DASH manifests are
// handled separately).
func isProgressiveHTTPExt(ext string) bool {
	switch ext {
	case "flv", "ts", "mp4", "m4v", "m4a", "f4v", "f4a", "aac", "mp3", "mkv", "webm", "ogg", "ogv", "opus":
		return true
	default:
		return false
	}
}
