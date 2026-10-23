package danmaku

import (
	"context"
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

func useDanmakuOverflowPolicy(tb testing.TB, policy string) {
	bootConfigReadOnly(tb, map[string]string{"DANMAKU_OVERFLOW_POLICY": policy})
}
