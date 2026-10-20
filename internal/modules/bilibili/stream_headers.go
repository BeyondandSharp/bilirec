// SPDX-License-Identifier: GPL-3.0-or-later

package bilibili

import "strings"

// StreamRequestHeaders returns the HTTP headers needed to fetch a live stream
// outside the resty clients (for example with the ffmpeg or streamlink
// downloaders): Cookie, User-Agent, Referer and Origin.
//
// The user agent, referer and origin are the same constants the live resty
// clients use. The Cookie header is omitted in anonymous mode or when the
// client has no cookies yet.
func (c *Client) StreamRequestHeaders() map[string]string {
	headers := map[string]string{
		"User-Agent": liveUserAgent,
		"Referer":    liveReferer,
		"Origin":     liveOrigin,
	}
	if c == nil {
		return headers
	}
	if cookie := c.streamCookieHeader(); cookie != "" {
		headers["Cookie"] = cookie
	}
	return headers
}

// streamCookieHeader formats the client cookies as a Cookie request header
// value ("name=value; name2=value2").
//
// GetCookiesString returns one cookie per line including attributes
// (Path/Domain/...), so strip everything after the first ";" of each line.
func (c *Client) streamCookieHeader() string {
	raw := strings.TrimSpace(c.GetCookiesString())
	if raw == "" {
		return ""
	}
	lines := strings.Split(raw, "\n")
	parts := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if idx := strings.IndexByte(line, ';'); idx >= 0 {
			line = strings.TrimSpace(line[:idx])
		}
		if line != "" {
			parts = append(parts, line)
		}
	}
	return strings.Join(parts, "; ")
}
