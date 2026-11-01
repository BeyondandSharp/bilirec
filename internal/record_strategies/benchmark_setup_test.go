package record_strategies

import (
	"context"
	"os"
	"sync"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/pkg/logger"
	"go.uber.org/fx"
)

var benchmarkConfigOnce sync.Once

func ensureBenchmarkConfig() {
	benchmarkConfigOnce.Do(func() {
		_ = os.Setenv("SKIP_SMALL_FLUSH", "true")
		_ = os.Setenv("SKIP_SMALL_FLUSH_THRESHOLD", "1024")
		if config.ReadOnly != nil {
			return
		}
		logger.SetLevel(logger.ErrorLevel)
		app := fx.New(
			config.Module,
			fx.NopLogger,
			fx.Invoke(func(*config.Config) {}),
		)
		if err := app.Start(context.Background()); err != nil {
			panic(err)
		}
		if config.ReadOnly == nil {
			panic("config.ReadOnly not initialized for benchmarks")
		}
	})
}
