package danmaku

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	"go.uber.org/fx"
	"github.com/bilirec/bilirec/pkg/benchreport"
	"github.com/bilirec/bilirec/pkg/logger"
	"github.com/bytedance/sonic"
	"github.com/bytedance/sonic/encoder"
)

// Danmaku parse/encode/write benchmarks. Reports ns/op, B/op, allocs/op, plus
// a benchreport CPU/RAM summary. CI uploads the log as a future baseline.
//
//	go test ./internal/services/danmaku -bench=. -benchmem -count=1

func benchDanmakuSetup(b *testing.B) {
	b.Helper()
	logger.SetLevel(logger.ErrorLevel)
	if config.ReadOnly == nil {
		app := fx.New(config.Module, fx.NopLogger, fx.Invoke(func(*config.Config) {}))
		if err := app.Start(context.Background()); err != nil {
			b.Fatal(err)
		}
		b.Cleanup(func() { _ = app.Stop(context.Background()) })
	}
}

func BenchmarkParseDanmaku(b *testing.B) {
	benchDanmakuSetup(b)
	raw := []byte(perfDanmakuJSON)
	mon := benchreport.Start(b, int64(len(raw)))
	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))
	b.ResetTimer()
	mon.MarkTimerStart()
	for i := 0; b.Loop(); i++ {
		if _, ok := parseDanmaku(raw); !ok {
			b.Fatal("parse failed")
		}
		mon.SamplePeriodically(i)
	}
}

func BenchmarkParseGift(b *testing.B) {
	benchDanmakuSetup(b)
	raw := []byte(perfGiftJSON)
	mon := benchreport.Start(b, int64(len(raw)))
	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))
	b.ResetTimer()
	mon.MarkTimerStart()
	for i := 0; b.Loop(); i++ {
		_, _ = parseGift(raw)
		mon.SamplePeriodically(i)
	}
}

func BenchmarkParseGiftV2(b *testing.B) {
	benchDanmakuSetup(b)
	item := buildGiftV2ItemMessage(1, "小花花", 2)
	pb := buildSendGiftBroadcastPB(sendGiftBroadcastTestOpts{
		uid:       456,
		uname:     "礼物用户",
		face:      "https://example.com/f.png",
		nameColor: "#00D1F1",
		items:     [][]byte{item},
	})
	raw := buildSendGiftV2JSON(pb)
	mon := benchreport.Start(b, int64(len(raw)))
	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))
	b.ResetTimer()
	mon.MarkTimerStart()
	for i := 0; b.Loop(); i++ {
		gifts, err := parseGiftV2(raw)
		if err != nil || len(gifts) != 1 {
			b.Fatal(err)
		}
		mon.SamplePeriodically(i)
	}
}

func BenchmarkParseSuperChat(b *testing.B) {
	benchDanmakuSetup(b)
	raw := []byte(perfSuperChatJSON)
	mon := benchreport.Start(b, int64(len(raw)))
	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))
	b.ResetTimer()
	mon.MarkTimerStart()
	for i := 0; b.Loop(); i++ {
		_, _ = parseSuperChat(raw)
		mon.SamplePeriodically(i)
	}
}

func BenchmarkEncodeDanmaku(b *testing.B) {
	benchDanmakuSetup(b)
	e, ok := parseDanmaku([]byte(perfDanmakuJSON))
	if !ok {
		b.Fatal("parse")
	}

	b.Run("xml", func(b *testing.B) {
		svc := newTestService()
		enc := xmlEncoder{}
		sample := enc.AppendDanmaku(nil, e, "1.234")
		mon := benchreport.Start(b, int64(len(sample)))
		b.ReportAllocs()
		b.SetBytes(int64(len(sample)))
		b.ResetTimer()
		mon.MarkTimerStart()
		for i := 0; b.Loop(); i++ {
			buf := svc.pool.GetBytes()
			_ = enc.AppendDanmaku(buf[:0], e, "1.234")
			svc.pool.PutBytes(buf)
			mon.SamplePeriodically(i)
		}
	})

	b.Run("jsonl", func(b *testing.B) {
		svc := newTestService()
		enc := jsonlEncoder{}
		sample := enc.AppendDanmaku(nil, e, "1.234")
		mon := benchreport.Start(b, int64(len(sample)))
		b.ReportAllocs()
		b.SetBytes(int64(len(sample)))
		b.ResetTimer()
		mon.MarkTimerStart()
		for i := 0; b.Loop(); i++ {
			buf := svc.pool.GetBytes()
			_ = enc.AppendDanmaku(buf[:0], e, "1.234")
			svc.pool.PutBytes(buf)
			mon.SamplePeriodically(i)
		}
	})
}

func BenchmarkPipeline_HandleAndWrite(b *testing.B) {
	for _, format := range []string{"jsonl", "xml"} {
		b.Run(format, func(b *testing.B) {
			benchmarkHandleAndWrite(b, format)
		})
	}
}

func benchmarkHandleAndWrite(b *testing.B, format string) {
	b.Helper()
	benchDanmakuSetup(b)
	useDanmakuOverflowPolicy(b, "block")

	svc := newTestService()
	svc.outputFormat = format
	sess, _ := newTestSession(svc, format)
	videoPath := filepath.Join(b.TempDir(), "bench.flv")
	if err := os.MkdirAll(filepath.Dir(videoPath), 0o755); err != nil {
		b.Fatal(err)
	}
	go sess.writeLoop(videoPath, time.Now())
	time.Sleep(20 * time.Millisecond)

	raw := []byte(perfDanmakuJSON)
	mon := benchreport.Start(b, int64(len(raw)))
	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))
	b.ResetTimer()
	mon.MarkTimerStart()
	for i := 0; b.Loop(); i++ {
		sess.handleDanmaku(raw)
		mon.SamplePeriodically(i)
	}
	b.StopTimer()
	close(sess.msgCh)
	select {
	case <-sess.done:
	case <-time.After(30 * time.Second):
		b.Fatal("writeLoop did not finish")
	}
}

func BenchmarkEnqueue_DropWhenFull(b *testing.B) {
	benchDanmakuSetup(b)
	useDanmakuOverflowPolicy(b, "drop")

	svc := newTestService()
	sess, _ := newTestSession(svc, "jsonl")
	raw := []byte(perfDanmakuJSON)

	// Fill the channel once so the hot path is the drop branch.
	for i := 0; i < cap(sess.msgCh); i++ {
		sess.handleDanmaku(raw)
	}

	mon := benchreport.Start(b, int64(len(raw)))
	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))
	b.ResetTimer()
	mon.MarkTimerStart()
	for i := 0; b.Loop(); i++ {
		sess.handleDanmaku(raw)
		mon.SamplePeriodically(i)
	}
	b.StopTimer()
	for len(sess.msgCh) > 0 {
		svc.pool.PutBytes(<-sess.msgCh)
	}
}

// BenchmarkJSONLMarshalLibs compares stdlib json.Marshal, sonic.Marshal (new
// []byte each call), and encoder.EncodeInto (appends into a reused buffer).
// Production JSONL encoding uses EncodeInto.
func BenchmarkJSONLMarshalLibs(b *testing.B) {
	benchDanmakuSetup(b)
	e, ok := parseDanmaku([]byte(perfDanmakuJSON))
	if !ok {
		b.Fatal("parse")
	}
	line := jsonlDanmakuLine{
		Type: "danmaku", TS: 1.234, User: e.Uname, UID: e.UID, Text: e.Text,
		Mode: e.Mode, FontSize: e.FontSize, Color: e.Color, SendTime: e.SendTime,
		DmType: e.DmType, GuardLevel: e.GuardLevel, UserLevel: e.UserLevel, Admin: e.Admin,
		MedalLevel: e.MedalLevel, MedalName: e.MedalName,
		Direction: e.Direction, EmoticonUnique: e.EmoticonUnique, EmoticonURL: e.EmoticonURL,
	}

	b.Run("stdlib", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; b.Loop(); i++ {
			out, err := json.Marshal(line)
			if err != nil {
				b.Fatal(err)
			}
			if len(out) == 0 {
				b.Fatal("empty")
			}
		}
	})

	b.Run("sonic_marshal", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; b.Loop(); i++ {
			out, err := sonic.Marshal(line)
			if err != nil {
				b.Fatal(err)
			}
			if len(out) == 0 {
				b.Fatal("empty")
			}
		}
	})

	b.Run("sonic_encodeinto", func(b *testing.B) {
		buf := make([]byte, 0, 512)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; b.Loop(); i++ {
			buf = buf[:0]
			if err := encoder.EncodeInto(&buf, line, 0); err != nil {
				b.Fatal(err)
			}
			if len(buf) == 0 {
				b.Fatal("empty")
			}
		}
	})
}
