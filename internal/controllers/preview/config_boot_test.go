package preview

import (
	"context"
	"testing"

	"github.com/bilirec/bilirec/internal/modules/config"
	"go.uber.org/fx"
)

func bootConfigReadOnly(tb testing.TB) {
	tb.Helper()
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
