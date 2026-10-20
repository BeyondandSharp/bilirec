// SPDX-License-Identifier: GPL-3.0-or-later

package bilibili

import "testing"

func TestDialURLTransportSelection(t *testing.T) {
	host := DanmuInfoHost{Host: "live.example.com", WssPort: 443}

	tests := []struct {
		name      string
		transport string
		plainWS   bool
		want      string
	}{
		{name: "default is wss", transport: "", want: "wss://live.example.com:443/sub"},
		{name: "auto is wss", transport: "auto", want: "wss://live.example.com:443/sub"},
		{name: "explicit wss", transport: "wss", want: "wss://live.example.com:443/sub"},
		{name: "explicit ws uses plain port", transport: "ws", want: "ws://live.example.com:2244/sub"},
		{name: "unknown falls back to wss", transport: "tcp", want: "wss://live.example.com:443/sub"},
		{name: "test override keeps test port", transport: "wss", plainWS: true, want: "ws://live.example.com:443/sub"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewLiveMessageClient(1, 2, "")
			c.plainWS = tt.plainWS
			c.SetTransport(tt.transport)
			if got := c.dialURL(host); got != tt.want {
				t.Fatalf("dialURL = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDispatchRawFallback(t *testing.T) {
	c := NewLiveMessageClient(1, 2, "")

	var raw []byte
	var rawCmd string
	c.HandleRaw(func(cmd string, body []byte) {
		rawCmd = cmd
		raw = append([]byte(nil), body...)
	})

	// Unknown command: raw handler receives the normalized cmd + body.
	body := []byte(`{"cmd":"UNKNOWN_EVENT:1:2","data":{"x":1}}`)
	c.dispatchMessage(body)
	if rawCmd != "UNKNOWN_EVENT" {
		t.Fatalf("raw cmd = %q, want UNKNOWN_EVENT", rawCmd)
	}
	if string(raw) != string(body) {
		t.Fatalf("raw body = %q, want %q", raw, body)
	}

	// Known command: dispatched to its handler, not to the raw fallback.
	raw = nil
	var known []byte
	c.HandleFunc("DANMU_MSG", func(body []byte) { known = append([]byte(nil), body...) })
	knownBody := []byte(`{"cmd":"DANMU_MSG","info":[]}`)
	c.dispatchMessage(knownBody)
	if string(known) != string(knownBody) {
		t.Fatalf("known handler body = %q, want %q", known, knownBody)
	}
	if raw != nil {
		t.Fatalf("known command must not hit the raw fallback, got %q", raw)
	}

	// Nil raw handler is a no-op for unknown commands.
	c.HandleRaw(nil)
	c.dispatchMessage([]byte(`{"cmd":"UNKNOWN"}`))
}

func TestSetTransportNormalizes(t *testing.T) {
	c := NewLiveMessageClient(1, 2, "")
	c.SetTransport(" WS ")
	if c.transport != "ws" {
		t.Fatalf("transport = %q, want ws", c.transport)
	}
	c.SetTransport("auto")
	if c.transport != "wss" {
		t.Fatalf("auto transport = %q, want wss", c.transport)
	}
}
