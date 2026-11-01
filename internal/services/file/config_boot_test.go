package file

import (
	"context"
	"strconv"
	"testing"

	"github.com/bilirec/bilirec/internal/modules/config"
	"go.uber.org/fx"
)

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
