// SPDX-License-Identifier: GPL-3.0-or-later

package bilibili

import (
	"strings"
	"testing"

	bili "github.com/CuteReimu/bilibili/v2"
)

func TestStreamRequestHeaders(t *testing.T) {
	client := &Client{Client: bili.New()}
	client.SetCookiesString("SESSDATA=abc123; Path=/; Domain=.bilibili.com\nbuvid3=xyz; Path=/")

	headers := client.StreamRequestHeaders()

	if got := headers["User-Agent"]; got != liveUserAgent {
		t.Fatalf("User-Agent = %q, want %q", got, liveUserAgent)
	}
	if got := headers["Referer"]; got != liveReferer {
		t.Fatalf("Referer = %q, want %q", got, liveReferer)
	}
	if got := headers["Origin"]; got != liveOrigin {
		t.Fatalf("Origin = %q, want %q", got, liveOrigin)
	}
	cookie := headers["Cookie"]
	if cookie != "SESSDATA=abc123; buvid3=xyz" {
		t.Fatalf("Cookie = %q, want %q", cookie, "SESSDATA=abc123; buvid3=xyz")
	}
	if strings.Contains(cookie, "Path=") || strings.Contains(cookie, "Domain=") {
		t.Fatalf("Cookie header must not contain cookie attributes: %q", cookie)
	}
}

func TestStreamRequestHeadersWithoutCookies(t *testing.T) {
	client := &Client{Client: bili.New()}
	headers := client.StreamRequestHeaders()
	if _, ok := headers["Cookie"]; ok {
		t.Fatalf("expected no Cookie header without cookies, got %q", headers["Cookie"])
	}
	if len(headers) != 3 {
		t.Fatalf("expected 3 headers, got %v", headers)
	}
}

func TestStreamCookieHeaderTrimsAttributes(t *testing.T) {
	client := &Client{Client: bili.New()}
	client.SetCookiesString("a=1; Path=/\n\n b=2 ")
	if got := client.streamCookieHeader(); got != "a=1; b=2" {
		t.Fatalf("streamCookieHeader = %q, want %q", got, "a=1; b=2")
	}
}
