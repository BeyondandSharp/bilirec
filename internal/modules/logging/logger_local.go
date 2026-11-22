package logging

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	// crashFollowWriter re-registers the runtime crash output when lumberjack
	// rotates this file, so fatal dumps keep landing in the current log file.
	tr := logsink.NewFileTransport(crashFollowWriter{path: path, w: fileWriter})
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
	activateSourceSet(nil)
	if !cfg.LocalLogsEnabled {
		return
	}

	logPath := LocalLogFilePath(cfg)
	if cfg.LocalLogsSplit {
		set := newSourceLoggerSet(cfg)
		activateSourceSet(set)
		logger.SetLocalCore(newSourceRouterCore(set))
		// Split mode still has a main aggregate file at logPath; crash dumps
		// land there even though normal entries are routed to per-source files.
		// The main sink is created lazily, so make its directory now.
		if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
			log.Warnf("创建本地日志目录失败，无法启用崩溃输出：%v", err)
		} else if err := logger.EnableCrashCapture(logPath); err != nil {
			log.Warnf("启用崩溃输出到本地日志失败：%v", err)
		}
		lc.Append(fx.StopHook(func(ctx context.Context) error {
			logger.DisableCrashCapture()
			_ = set.Sync()
			logger.ClearLocalCore()
			return set.Close(ctx)
		}))
		log.Infof("本地日志已启用（按来源）：%s format=%s", set.SourceDir(), cfg.LocalLogsFormat)
		return
	}

	sink, err := newLocalSink(cfg, logPath)
	if err != nil {
		log.Warnf("初始化本地日志失败，已跳过：%v", err)
		return
	}
	logger.SetLocalCore(newLocalCoreForFormat(cfg.LocalLogsFormat, sink))
	if err := logger.EnableCrashCapture(logPath); err != nil {
		log.Warnf("启用崩溃输出到本地日志失败：%v", err)
	}
	lc.Append(fx.StopHook(func(ctx context.Context) error {
		logger.DisableCrashCapture()
		return shutdownSink(logger.ClearLocalCore, sink, ctx)
	}))
	log.Infof("本地日志已启用：%s format=%s", logPath, cfg.LocalLogsFormat)
}

// crashFollowWriter 在每次批次写入后让崩溃转储跟随当前日志文件。
// lumberjack 按大小换档后，runtime 仍持有旧 inode 的 fd；这里发现路径指向
// 新文件时重新注册（同一文件时是廉价的 SameFile 检查）。
type crashFollowWriter struct {
	path string
	w    io.WriteCloser
}

func (f crashFollowWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	logger.NoteCrashLogWrite(f.path)
	return n, err
}

func (f crashFollowWriter) Close() error { return f.w.Close() }
