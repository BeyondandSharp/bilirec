package file

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/bilirec/bilirec/internal/modules/config"
	fileservice "github.com/bilirec/bilirec/internal/services/file"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

func TestSendFileWithIdleCacheRelease_PlaybackResponse(t *testing.T) {
	fullPath, err := filepath.Abs("send_file_test.go")
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(fullPath)
	if err != nil {
		t.Fatal(err)
	}
	app := newPlaybackSendFileTestApp(t, fullPath)

	t.Run("full", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/playback/video.mp4", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		assertPlaybackResponse(t, resp, http.StatusOK, "", content)
	})

	t.Run("range", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/playback/video.mp4", nil)
		req.Header.Set(fiber.HeaderRange, "bytes=3-7")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		contentRange := "bytes 3-7/" + strconv.Itoa(len(content))
		assertPlaybackResponse(t, resp, http.StatusPartialContent, contentRange, content[3:8])
	})
}

func newPlaybackSendFileTestApp(t *testing.T, fullPath string) *fiber.App {
	t.Helper()
	bootServeCacheReadOnly(t, false, 60)

	fileSvc := fileservice.NewService(fxtest.NewLifecycle(t), nil, nil)
	controller := &Controller{fileSvc: fileSvc}
	app := fiber.New()
	app.Get("/playback/video.mp4", func(ctx fiber.Ctx) error {
		ctx.Set(fiber.HeaderContentType, "video/mp4")
		ctx.Set(fiber.HeaderContentDisposition, "inline; filename=\"video.mp4\"")
		return controller.sendFileWithIdleCacheRelease(ctx, fullPath, fiber.SendFile{ByteRange: true})
	})
	return app
}

func assertPlaybackResponse(t *testing.T, resp *http.Response, status int, contentRange string, wantBody []byte) {
	t.Helper()
	if resp.StatusCode != status {
		t.Fatalf("expected status %d, got %d", status, resp.StatusCode)
	}
	if got := resp.Header.Get(fiber.HeaderContentType); got != "video/mp4" {
		t.Fatalf("expected Content-Type video/mp4, got %q", got)
	}
	if got := resp.Header.Get(fiber.HeaderContentDisposition); got != "inline; filename=\"video.mp4\"" {
		t.Fatalf("unexpected Content-Disposition: %q", got)
	}
	if got := resp.Header.Get(fiber.HeaderAcceptRanges); got != "bytes" {
		t.Fatalf("expected Accept-Ranges bytes, got %q", got)
	}
	if got := resp.Header.Get(fiber.HeaderContentRange); got != contentRange {
		t.Fatalf("expected Content-Range %q, got %q", contentRange, got)
	}
	if got := resp.Header.Get(fiber.HeaderContentLength); got != strconv.Itoa(len(wantBody)) {
		t.Fatalf("expected Content-Length %d, got %q", len(wantBody), got)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != string(wantBody) {
		t.Fatalf("expected body %q, got %q", wantBody, body)
	}
}

func bootConfigReadOnly(tb testing.TB, env map[string]string) {
	tb.Helper()
	for k, v := range env {
		tb.Setenv(k, v)
	}
	old := config.ReadOnly
	app := fx.New(config.Module, fx.NopLogger, fx.Invoke(func(*config.Config) {}))
	ctx := context.Background()
	if err := app.Start(ctx); err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		_ = app.Stop(ctx)
		config.ReadOnly = old
	})
}

func bootServeCacheReadOnly(tb testing.TB, dropFilePageCache bool, coldCacheReleaseSecs int) {
	env := map[string]string{
		"LIVE_STREAM_WRITER_COLD_CACHE_RELEASE_SECS": strconv.Itoa(coldCacheReleaseSecs),
	}
	if !dropFilePageCache {
		env["DROP_FILE_PAGE_CACHE"] = "false"
	}
	bootConfigReadOnly(tb, env)
}
