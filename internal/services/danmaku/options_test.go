package danmaku

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bilirec/bilirec/internal/modules/metrics"
	"github.com/bilirec/bilirec/pkg/pool"
	"github.com/puzpuzpuz/xsync/v4"
)

const (
	testSCJSON    = `{"cmd":"SUPER_CHAT_MESSAGE","data":{"uid":123,"id":999,"start_time":1700000000,"end_time":1700000060,"user_info":{"uname":"sc用户","name_color":"#646c7a","face":"https://example.com/a.png"},"price":30,"time":60,"message":"醒目留言","background_color":"#EDF5FF","background_bottom_color":"#2A60B2","background_price_color":"#7497CD","message_font_color":"#FFFFFF","background_image":"https://example.com/bg.png","background_color_start":"#111111","background_color_end":"#222222"}}`
	testGiftJSON  = `{"cmd":"SEND_GIFT","data":{"uname":"礼物用户","uid":456,"giftName":"小花花","num":2,"price":100,"coin_type":"gold"}}`
	testGuardJSON = `{"cmd":"GUARD_BUY","data":{"username":"舰长用户","uid":789,"guard_level":3,"num":1,"price":198000,"gift_name":"舰长"}}`
	testRawJSON   = `{"cmd":"UNKNOWN_EVENT","data":{"foo":"bar","n":1}}`
)

func newOptionsTestService() *Service {
	return &Service{
		outputFormat: "jsonl",
		sessions:     xsync.NewMap[int, *session](),
		metrics:      &metrics.Exporter{},
		pool:         pool.NewBytesPool(4096),
	}
}

func newOptionsTestSession(t *testing.T, format string, options SessionOptions) *session {
	t.Helper()
	svc := newOptionsTestService()
	enc, err := NewFormatEncoder(format)
	if err != nil {
		t.Fatal(err)
	}
	sess := newSession(1, RoomMeta{}, svc, enc, context.Background(), options)
	return sess
}

func TestSessionTypeFiltering(t *testing.T) {
	disabled := newOptionsTestSession(t, "jsonl", SessionOptions{
		Types:     DanmakuTypes{RawEvents: false, SuperChat: false, Gift: false, Guard: false},
		Transport: "auto",
	})
	disabled.handleSuperChat([]byte(testSCJSON))
	disabled.handleGift([]byte(testGiftJSON))
	disabled.handleGuard([]byte(testGuardJSON))
	disabled.handleDanmaku([]byte(testDanmakuJSON))
	if got := len(disabled.msgCh); got != 1 {
		t.Fatalf("disabled types: queued %d fragments, want 1 (chat only)", got)
	}

	enabled := newOptionsTestSession(t, "jsonl", defaultSessionOptions())
	enabled.handleSuperChat([]byte(testSCJSON))
	enabled.handleGift([]byte(testGiftJSON))
	enabled.handleGuard([]byte(testGuardJSON))
	enabled.handleDanmaku([]byte(testDanmakuJSON))
	if got := len(enabled.msgCh); got != 4 {
		t.Fatalf("enabled types: queued %d fragments, want 4", got)
	}
}

func TestSessionRawEventsJSONL(t *testing.T) {
	sess := newOptionsTestSession(t, "jsonl", defaultSessionOptions())
	sess.handleRaw("UNKNOWN_EVENT", []byte(testRawJSON))
	if got := len(sess.msgCh); got != 0 {
		t.Fatalf("raw_events=false must not queue raw records, got %d", got)
	}

	options := defaultSessionOptions()
	options.Types.RawEvents = true
	sess = newOptionsTestSession(t, "jsonl", options)
	sess.handleRaw("UNKNOWN_EVENT", []byte(testRawJSON))
	if got := len(sess.msgCh); got != 1 {
		t.Fatalf("raw_events=true queued %d fragments, want 1", got)
	}
	frag := <-sess.msgCh
	line := strings.TrimSpace(string(frag))
	var decoded struct {
		Type string          `json:"type"`
		Cmd  string          `json:"cmd"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(line), &decoded); err != nil {
		t.Fatalf("raw jsonl line invalid: %v (%s)", err, line)
	}
	if decoded.Type != "raw" || decoded.Cmd != "UNKNOWN_EVENT" {
		t.Fatalf("raw record = %+v", decoded)
	}
	if string(decoded.Data) != `{"foo":"bar","n":1}` {
		t.Fatalf("raw data = %s", decoded.Data)
	}
}

func TestSessionRawEventsXMLIgnored(t *testing.T) {
	options := defaultSessionOptions()
	options.Types.RawEvents = true
	sess := newOptionsTestSession(t, "xml", options)
	sess.handleRaw("UNKNOWN_EVENT", []byte(testRawJSON))
	if got := len(sess.msgCh); got != 0 {
		t.Fatalf("xml format must ignore raw events, queued %d", got)
	}
}

func TestNormalizeTransport(t *testing.T) {
	tests := map[string]string{
		"":       "auto",
		"auto":   "auto",
		"ws":     "ws",
		"wss":    "wss",
		"tcp":    "auto",
		" AUTO ": "auto",
	}
	for input, want := range tests {
		if got := normalizeTransport(input); got != want {
			t.Fatalf("normalizeTransport(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDefaultSessionOptionsPreservesBehavior(t *testing.T) {
	opts := defaultSessionOptions()
	if opts.Types.RawEvents {
		t.Fatal("raw events must default off")
	}
	if !opts.Types.SuperChat || !opts.Types.Gift || !opts.Types.Guard {
		t.Fatalf("default danmaku types changed: %+v", opts.Types)
	}
	if opts.Transport != "auto" {
		t.Fatalf("default transport = %q, want auto", opts.Transport)
	}
}

func TestIsDialOrTLSError(t *testing.T) {
	dialErrors := []string{
		"弹幕 websocket 连接失败：dial tcp 1.2.3.4:443: connect: connection refused",
		"弹幕 websocket 连接失败：tls: failed to verify certificate: x509: certificate signed by unknown authority",
		"弹幕 websocket 连接失败：dial tcp: i/o timeout",
		"弹幕 websocket 连接失败：no such host",
	}
	for _, msg := range dialErrors {
		if !isDialOrTLSError(errString(msg)) {
			t.Fatalf("expected dial/TLS classification for %q", msg)
		}
	}
	for _, msg := range []string{"", "服务器关闭了弹幕 websocket 连接", "context canceled"} {
		if isDialOrTLSError(errString(msg)) {
			t.Fatalf("unexpected dial/TLS classification for %q", msg)
		}
	}
	if isDialOrTLSError(nil) {
		t.Fatal("nil error must not classify")
	}
}

type errString string

func (e errString) Error() string { return string(e) }
