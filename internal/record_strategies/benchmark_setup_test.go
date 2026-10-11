package record_strategies

import (
	"context"
	"os"
	"sync"

	"github.com/bilirec/bilirec/pkg/logger"

	"github.com/bilirec/bilirec/internal/modules/config"
	"go.uber.org/fx"
)

var benchmarkConfigOnce sync.Once

// ensureBenchmarkConfig initializes global config for strategy pipeline benchmarks.
// Strategy BuildPipeline reads config.ReadOnly, which is normally set by fx at app start.
func ensureBenchmarkConfig() {
	benchmarkConfigOnce.Do(func() {
		if config.ReadOnly != nil {
			return
		}
		// Benchmarks/strategy tests need a low commit threshold so small fixtures still create files.
		_ = os.Setenv("SKIP_SMALL_FLUSH", "true")
		_ = os.Setenv("SKIP_SMALL_FLUSH_THRESHOLD", "1024")
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
