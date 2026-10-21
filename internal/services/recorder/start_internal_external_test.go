// SPDX-License-Identifier: GPL-3.0-only

package recorder

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/config"
	rs "github.com/bilirec/bilirec/internal/record_strategies"
	"github.com/bilirec/bilirec/internal/services/downloader"
	"github.com/bilirec/bilirec/internal/services/stream"
	"github.com/bilirec/bilirec/pkg/logger"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

func initRecorderExternalTestConfig(t *testing.T) *config.Config {
	t.Helper()
	var cfg *config.Config
	app := fxtest.New(t, config.Module, fx.Populate(&cfg))
	app.RequireStart()
	t.Cleanup(app.RequireStop)
	return cfg
}

type fakeExternalDownloader struct {
	open func(ctx context.Context, spec downloader.Spec) (io.ReadCloser, error)
}

func (f *fakeExternalDownloader) Name() string { return "fake" }

func (f *fakeExternalDownloader) Open(ctx context.Context, spec downloader.Spec) (io.ReadCloser, error) {
	return f.open(ctx, spec)
}

func TestResolveDownloaderKind(t *testing.T) {
	cases := []struct {
		name string
		cfg  *config.Config
		opts RecordStartOptions
		want downloader.Kind
	}{
		{name: "nil config defaults to native", cfg: nil, opts: RecordStartOptions{}, want: downloader.KindNative},
		{name: "auto is native", cfg: &config.Config{Downloader: "auto"}, want: downloader.KindNative},
		{name: "native", cfg: &config.Config{Downloader: "native"}, want: downloader.KindNative},
		{name: "ffmpeg", cfg: &config.Config{Downloader: "ffmpeg"}, want: downloader.KindFFmpeg},
		{name: "streamlink", cfg: &config.Config{Downloader: " STREAMLINK "}, want: downloader.KindStreamlink},
		{name: "unknown is native", cfg: &config.Config{Downloader: "bogus"}, want: downloader.KindNative},
		{
			name: "request override wins",
			cfg:  &config.Config{Downloader: "ffmpeg"},
			opts: RecordStartOptions{hasDownloader: true, downloaderKind: downloader.KindStreamlink},
			want: downloader.KindStreamlink,
		},
		{
			name: "request native overrides external config",
			cfg:  &config.Config{Downloader: "ffmpeg"},
			opts: RecordStartOptions{hasDownloader: true, downloaderKind: downloader.KindNative},
			want: downloader.KindNative,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveDownloaderKind(tc.opts, tc.cfg); got != tc.want {
				t.Fatalf("resolveDownloaderKind = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSupportsExternalDownloader(t *testing.T) {
	for _, format := range []string{"flv", "ts", "fmp4"} {
		if !supportsExternalDownloader(format) {
			t.Fatalf("expected %q to support external downloader", format)
		}
	}
	if supportsExternalDownloader("unknown") {
		t.Fatal("unexpected external downloader support for unknown format")
	}
}

func TestConnectExternalStreamSuccess(t *testing.T) {
	cfg := initRecorderExternalTestConfig(t)
	st := stream.NewService(cfg)

	payload := []byte("FLV-EXTERNAL-PAYLOAD")
	var gotSpec downloader.Spec
	registry := downloader.NewRegistry(downloader.Options{})
	registry.Register(downloader.KindFFmpeg, &fakeExternalDownloader{
		open: func(_ context.Context, spec downloader.Spec) (io.ReadCloser, error) {
			gotSpec = spec
			return io.NopCloser(bytes.NewReader(payload)), nil
		},
	})
	svc := &Service{st: st, downloaders: registry, cfg: cfg}

	chunkPool, release := st.AcquireChunkPool(10000)
	var releaseOnce sync.Once
	released := make(chan struct{})
	releasePool := func() { releaseOnce.Do(func() { close(released); release() }) }

	streamInfo := bilibili.StreamURLInfo{URL: "https://cdn.example/live.m3u8", Format: "ts", Qn: 10000}
	ch, strategy, ok := svc.connectExternalStream(logger.Nop(), context.Background(), downloader.KindFFmpeg, streamInfo, chunkPool, releasePool, rs.FlvSplitOptions{})
	if !ok {
		t.Fatal("expected external stream to connect")
	}
	if strategy == nil || strategy.FileExtension() != ".flv" {
		t.Fatalf("external sessions must use the FLV strategy, got %v", strategy)
	}
	if gotSpec.URL != streamInfo.URL || gotSpec.Format != "ts" {
		t.Fatalf("unexpected spec passed to downloader: %+v", gotSpec)
	}

	var data []byte
	for chunk := range ch {
		data = append(data, chunk...)
		if cap(chunk) > 0 {
			chunkPool.Put(chunk[:cap(chunk)])
		}
	}
	if !bytes.Equal(data, payload) {
		t.Fatalf("got %q, want %q", data, payload)
	}
	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatal("external stream did not release the chunk pool")
	}
}

func TestConnectExternalStreamFallsBackOnOpenError(t *testing.T) {
	cfg := initRecorderExternalTestConfig(t)
	st := stream.NewService(cfg)

	registry := downloader.NewRegistry(downloader.Options{})
	registry.Register(downloader.KindFFmpeg, &fakeExternalDownloader{
		open: func(_ context.Context, _ downloader.Spec) (io.ReadCloser, error) {
			return nil, errors.New("spawn failed")
		},
	})
	svc := &Service{st: st, downloaders: registry, cfg: cfg}

	chunkPool, release := st.AcquireChunkPool(10000)
	released := false
	releasePool := func() { released = true; release() }

	ch, strategy, ok := svc.connectExternalStream(
		logger.Nop(),
		context.Background(),
		downloader.KindFFmpeg,
		bilibili.StreamURLInfo{URL: "https://cdn.example/live.flv", Format: "flv", Qn: 10000},
		chunkPool,
		releasePool,
		rs.FlvSplitOptions{},
	)
	if ok {
		t.Fatal("expected external stream to fail so the caller can fall back to native")
	}
	if ch != nil || strategy != nil {
		t.Fatal("failed external connect must not return a channel or strategy")
	}
	if released {
		t.Fatal("failed external connect must leave the chunk pool lease to the caller")
	}
	release() // caller-owned lease
}

func TestConnectExternalStreamWithoutRegistry(t *testing.T) {
	cfg := initRecorderExternalTestConfig(t)
	st := stream.NewService(cfg)
	svc := &Service{st: st, cfg: cfg}

	chunkPool, release := st.AcquireChunkPool(10000)
	defer release()

	ch, strategy, ok := svc.connectExternalStream(
		logger.Nop(),
		context.Background(),
		downloader.KindStreamlink,
		bilibili.StreamURLInfo{URL: "https://cdn.example/live.flv", Format: "flv", Qn: 10000},
		chunkPool,
		func() {},
		rs.FlvSplitOptions{},
	)
	if ok || ch != nil || strategy != nil {
		t.Fatal("missing registry must fall back to native")
	}
}
