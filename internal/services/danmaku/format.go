package danmaku

import (
	"fmt"
	"time"
)

// FormatEncoder serializes parsed danmaku events into a segment file format.
type FormatEncoder interface {
	Ext() string
	AppendHeader(buf []byte, meta RoomMeta, start time.Time) []byte
	AppendFooter(buf []byte) []byte
	AppendDanmaku(buf []byte, e Danmaku, ts string) []byte
	AppendSuperChat(buf []byte, e SuperChat, ts string) []byte
	AppendGift(buf []byte, e Gift, ts string) []byte
	AppendGuard(buf []byte, e Guard, ts string) []byte
	// SupportsRaw reports whether unknown commands can be persisted as raw
	// records (JSONL only; XML ignores them, docs/FEATURES-V2.md §4).
	SupportsRaw() bool
	// AppendRaw serializes an unknown command payload; returns nil when the
	// format does not support raw records.
	AppendRaw(buf []byte, cmd string, raw []byte, ts string) []byte
}

// NewFormatEncoder returns an encoder for the given format name (jsonl or xml).
func NewFormatEncoder(format string) (FormatEncoder, error) {
	switch format {
	case "jsonl":
		return jsonlEncoder{}, nil
	case "xml":
		return xmlEncoder{}, nil
	default:
		return nil, fmt.Errorf("unsupported danmaku output format: %s", format)
	}
}
