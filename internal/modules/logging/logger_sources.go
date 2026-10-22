package logging

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/pkg/logger"
	logsink "github.com/bilirec/bilirec/pkg/sink"
	"go.uber.org/zap/zapcore"
)

var errTooManySources = fmt.Errorf("too many log sources")

const (
	maxLogSources = 64
	// accessGroup is the Fiber access-log group. Its INFO/DEBUG entries are
	// kept out of the main aggregate so requests do not drown app logs.
	accessGroup = "access"
)

var logGroupNames = []string{"live", "system", "recorder", "access", "update"}

// logGroupForLogger groups noisy/related logger names into the fixed UI groups.
// Unknown named loggers fall back to "system".
var logGroupForLogger = map[string]string{
	"bilibili": "live", "danmaku": "live", "stream": "live",
	"logging": "system", "notify": "system", "subcheck": "system",
	"auth": "system", "config": "system", "settings": "system", "room": "system",
	"subscribe": "system", "webhook": "system", "path": "system", "metrics": "system",
	"janitor": "system", "expose": "system", "fx": "system", "logs": "system",
	"convert": "recorder", "pipeline": "recorder", "recorder": "recorder",
	"repair": "recorder", "downloader": "recorder", "media": "recorder",
	"file": "recorder", "ffmpeg_core": "recorder", "fallback": "recorder",
	"rest": "access", "updatecheck": "update",
}

func logGroupForName(raw string) string {
	name := strings.ToLower(strings.TrimSpace(raw))
	if name == "" {
		return ""
	}
	if group, ok := logGroupForLogger[name]; ok {
		return group
	}
	return "system"
}

type sourceSink struct {
	core zapcore.Core
	sink *logsink.AsyncBufferedSink
}

// sourceLoggerSet owns the main aggregate plus lazily-created per-group files.
//
// Layout (LOCAL_LOGS_SPLIT=true):
//
//	<LOCAL_LOGS_PATH>                  main aggregate
//	<LOCAL_LOGS_PATH>.d/<group>/current.log
//
// Main receives unnamed entries and every enabled named group except INFO/DEBUG
// from the access group; group files receive every entry for that group.
type sourceLoggerSet struct {
	cfg       *config.Config
	basePath  string
	sourceDir string

	mu            sync.Mutex
	main          *sourceSink
	sources       map[string]*sourceSink
	enabledGroups map[string]struct{}
	// warned tracks per-source creation failures so we do not spam stderr.
	warned map[string]struct{}
}

func newSourceLoggerSet(cfg *config.Config) *sourceLoggerSet {
	enabled := make(map[string]struct{}, len(logGroupNames))
	if cfg.LocalLogsGroups == nil {
		for _, group := range logGroupNames {
			enabled[group] = struct{}{}
		}
	} else {
		for _, group := range cfg.LocalLogsGroups {
			group = strings.ToLower(strings.TrimSpace(group))
			for _, known := range logGroupNames {
				if group == known {
					enabled[group] = struct{}{}
					break
				}
			}
		}
	}
	return &sourceLoggerSet{
		cfg:           cfg,
		basePath:      LocalLogFilePath(cfg),
		sourceDir:     LocalLogSourceDir(cfg),
		sources:       make(map[string]*sourceSink),
		enabledGroups: enabled,
		warned:        make(map[string]struct{}),
	}
}

func (s *sourceLoggerSet) groupEnabled(group string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.enabledGroups[group]
	return ok
}

func (s *sourceLoggerSet) SourceDir() string {
	if s == nil {
		return ""
	}
	return s.sourceDir
}

func (s *sourceLoggerSet) warnOnce(key string, format string, args ...any) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if _, ok := s.warned[key]; ok {
		s.mu.Unlock()
		return
	}
	s.warned[key] = struct{}{}
	s.mu.Unlock()
	// Use stderr directly: using the named logger here would recurse into this
	// same source router if the failure happened during sink creation.
	_, _ = fmt.Fprintf(os.Stderr, "[logging] "+format+"\n", args...)
}

func (s *sourceLoggerSet) newSink(path string) (*sourceSink, error) {
	buffered, err := newLocalSink(s.cfg, path)
	if err != nil {
		return nil, err
	}
	return &sourceSink{
		core: newLocalCoreForFormat(s.cfg.LocalLogsFormat, buffered),
		sink: buffered,
	}, nil
}

func (s *sourceLoggerSet) mainSink() (*sourceSink, error) {
	s.mu.Lock()
	if s.main != nil {
		sink := s.main
		s.mu.Unlock()
		return sink, nil
	}
	s.mu.Unlock()

	sink, err := s.newSink(s.basePath)
	if err != nil {
		s.warnOnce("main", "init main log failed: %v", err)
		return nil, err
	}

	s.mu.Lock()
	if s.main == nil {
		s.main = sink
	} else {
		// Another goroutine won the race; close our duplicate.
		_ = sink.sink.Stop(context.Background())
		sink = s.main
	}
	s.mu.Unlock()
	return sink, nil
}

func (s *sourceLoggerSet) sourceSink(name string) (*sourceSink, error) {
	s.mu.Lock()
	if sink := s.sources[name]; sink != nil {
		s.mu.Unlock()
		return sink, nil
	}
	if len(s.sources) >= maxLogSources {
		s.mu.Unlock()
		s.warnOnce("max-sources", "too many log sources; %q falls back to main", name)
		return nil, errTooManySources
	}
	s.mu.Unlock()

	path := filepath.Join(s.sourceDir, name, "current.log")
	sink, err := s.newSink(path)
	if err != nil {
		s.warnOnce("source:"+name, "init source log %q failed, falling back to main: %v", name, err)
		return nil, err
	}

	s.mu.Lock()
	if existing := s.sources[name]; existing != nil {
		_ = sink.sink.Stop(context.Background())
		sink = existing
	} else {
		s.sources[name] = sink
	}
	s.mu.Unlock()
	return sink, nil
}

func (s *sourceLoggerSet) write(ent zapcore.Entry, fields []zapcore.Field) error {
	group := logGroupForName(ent.LoggerName)
	if group == "" {
		sink, err := s.mainSink()
		if err != nil {
			return err
		}
		return sink.core.Write(ent, fields)
	}
	if !s.groupEnabled(group) {
		return nil
	}

	sourceErr := error(nil)
	if sink, err := s.sourceSink(group); err != nil {
		sourceErr = err
	} else if err := sink.core.Write(ent, fields); err != nil {
		sourceErr = err
		s.warnOnce("write:"+group, "write source log %q failed: %v", group, err)
	}

	// HTTP access INFO/DEBUG only belongs in access/current.log. WARN/ERROR
	// still goes to main so failures remain visible by default.
	if group == accessGroup && ent.Level < zapcore.WarnLevel {
		return sourceErr
	}

	mainSink, err := s.mainSink()
	if err != nil {
		return err
	}
	if err := mainSink.core.Write(ent, fields); err != nil {
		return err
	}
	return sourceErr
}

func (s *sourceLoggerSet) Sync() error {
	s.mu.Lock()
	sinks := make([]*sourceSink, 0, len(s.sources)+1)
	if s.main != nil {
		sinks = append(sinks, s.main)
	}
	for _, sink := range s.sources {
		sinks = append(sinks, sink)
	}
	s.mu.Unlock()

	var firstErr error
	for _, sink := range sinks {
		if err := sink.sink.Sync(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *sourceLoggerSet) Close(ctx context.Context) error {
	s.mu.Lock()
	sinks := make([]*sourceSink, 0, len(s.sources)+1)
	if s.main != nil {
		sinks = append(sinks, s.main)
	}
	for _, sink := range s.sources {
		sinks = append(sinks, sink)
	}
	s.main = nil
	s.sources = make(map[string]*sourceSink)
	s.mu.Unlock()

	var firstErr error
	for _, sink := range sinks {
		if err := sink.sink.Stop(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// sourceRouterCore routes every local-log entry to its source sink (and the
// main aggregate when appropriate).
type sourceRouterCore struct {
	set    *sourceLoggerSet
	fields []zapcore.Field
}

func newSourceRouterCore(set *sourceLoggerSet) zapcore.Core {
	return &sourceRouterCore{set: set}
}

func (c *sourceRouterCore) Enabled(l zapcore.Level) bool {
	return l >= logger.Level()
}

func (c *sourceRouterCore) With(fields []zapcore.Field) zapcore.Core {
	next := &sourceRouterCore{
		set:    c.set,
		fields: make([]zapcore.Field, 0, len(c.fields)+len(fields)),
	}
	next.fields = append(next.fields, c.fields...)
	next.fields = append(next.fields, fields...)
	return next
}

func (c *sourceRouterCore) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if !c.Enabled(ent.Level) {
		return ce
	}
	return ce.AddCore(ent, c)
}

func (c *sourceRouterCore) Write(ent zapcore.Entry, fields []zapcore.Field) error {
	all := make([]zapcore.Field, 0, len(c.fields)+len(fields))
	all = append(all, c.fields...)
	all = append(all, fields...)
	return c.set.write(ent, all)
}

func (c *sourceRouterCore) Sync() error {
	return c.set.Sync()
}
