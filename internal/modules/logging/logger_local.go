package logging

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/pkg/logger"
	logsink "github.com/bilirec/bilirec/pkg/sink"
	"go.uber.org/fx"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

// levelEnabler delegates level checks to pkg/logger's dynamic level.
// This ensures cores built here respect runtime level changes made via logger.SetLevel.
type levelEnabler struct{}

func (levelEnabler) Enabled(l zapcore.Level) bool {
	return l >= logger.Level()
}

// LocalLogFilePath returns the configured main local log file path.
// It mirrors the historic wireLocalLogger fallback and intentionally never
// consumes request input.
func LocalLogFilePath(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	if path := strings.TrimSpace(cfg.LocalLogsPath); path != "" {
		return path
	}
	return filepath.Join(cfg.DatabaseDir, "bilirec.log")
}

// LocalLogSourceDir returns the directory holding per-source current.log files.
// It is empty when split logging is disabled.
func LocalLogSourceDir(cfg *config.Config) string {
	if cfg == nil || !cfg.LocalLogsSplit {
		return ""
	}
	base := LocalLogFilePath(cfg)
	if base == "" {
		return ""
	}
	return base + ".d"
}

func overflowPolicy(raw string) logsink.OverflowPolicy {
	if strings.EqualFold(strings.TrimSpace(raw), "block") {
		return logsink.OverflowBlock
	}
	return logsink.OverflowDrop
}

func newLocalSink(cfg *config.Config, path string) (*logsink.AsyncBufferedSink, error) {
	if cfg == nil || path == "" {
		return nil, errors.New("empty local log path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	fileWriter := &lumberjack.Logger{
		Filename:   path,
		MaxSize:    cfg.LocalLogsMaxSizeMB,
		MaxAge:     cfg.LocalLogsMaxAgeDays,
		MaxBackups: cfg.LocalLogsMaxBackups,
		Compress:   cfg.LocalLogsCompress,
	}
	tr := logsink.NewFileTransport(fileWriter)
	return logsink.NewAsyncBufferedSink(tr, logsink.Options{
		BufferSize:    cfg.LocalLogsBufferSize,
		BatchBytes:    cfg.LocalLogsBatchBytes,
		FlushInterval: cfg.LocalLogsFlushInterval,
		Overflow:      overflowPolicy(cfg.LocalLogsOverflow),
	})
}

func newLocalCoreForFormat(format string, sink *logsink.AsyncBufferedSink) zapcore.Core {
	var enc zapcore.Encoder
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "jsonline":
		enc = logger.NewJsonLineEncoder()
	default:
		enc = logger.NewPrettyEncoder(false)
	}
	return zapcore.NewCore(enc, sink, levelEnabler{})
}

func wireLocalLogger(lc fx.Lifecycle, cfg *config.Config) {
	if !cfg.LocalLogsEnabled {
		return
	}

	if cfg.LocalLogsSplit {
		set := newSourceLoggerSet(cfg)
		logger.SetLocalCore(newSourceRouterCore(set))
		lc.Append(fx.StopHook(func(ctx context.Context) error {
			_ = set.Sync()
			logger.ClearLocalCore()
			return set.Close(ctx)
		}))
		log.Infof("本地日志已启用（按来源）：%s format=%s", set.SourceDir(), cfg.LocalLogsFormat)
		return
	}

	logPath := LocalLogFilePath(cfg)
	sink, err := newLocalSink(cfg, logPath)
	if err != nil {
		log.Warnf("初始化本地日志失败，已跳过：%v", err)
		return
	}
	logger.SetLocalCore(newLocalCoreForFormat(cfg.LocalLogsFormat, sink))
	lc.Append(fx.StopHook(func(ctx context.Context) error {
		return shutdownSink(logger.ClearLocalCore, sink, ctx)
	}))
	log.Infof("本地日志已启用：%s format=%s", logPath, cfg.LocalLogsFormat)
}
