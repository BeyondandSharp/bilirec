package danmaku

import "github.com/bilirec/bilirec/internal/services/settings"

// DanmakuTypes aliases settings.DanmakuTypes so recorder can pass the effective
// session snapshot without conversions (docs/FEATURES-V2.md §4).
type DanmakuTypes = settings.DanmakuTypes

// SessionOptions configures one danmaku recording session. Transport is
// auto|ws|wss; Types filters which message kinds are persisted.
type SessionOptions struct {
	Types     DanmakuTypes
	Transport string
}

// defaultSessionOptions reproduces the pre-v2 behavior: chat + super chat +
// gift + guard recorded, no raw events, transport auto (wss with fallback).
func defaultSessionOptions() SessionOptions {
	return SessionOptions{
		Types: DanmakuTypes{
			RawEvents: false,
			SuperChat: true,
			Gift:      true,
			Guard:     true,
		},
		Transport: "auto",
	}
}

// normalizeTransport maps the configured value to "auto", "ws" or "wss".
func normalizeTransport(transport string) string {
	switch transport {
	case "ws":
		return "ws"
	case "wss":
		return "wss"
	default:
		return "auto"
	}
}
