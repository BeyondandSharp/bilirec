package logging

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	// sourceRest is the Fiber access-log logger name. Its INFO entries are kept
	// out of the main aggregate so request logs do not drown application logs.
	sourceRest = "rest"
)

type sourceSink struct {
	core zapcore.Core
	sink *logsink.AsyncBufferedSink
}

// sourceLoggerSet owns the main aggregate plus lazily-created per-source files.
//
// Layout (LOCAL_LOGS_SPLIT=true):
//
//	<LOCAL_LOGS_PATH>                  main aggregate
//	<LOCAL_LOGS_PATH>.d/<source>/current.log
//
// Main receives unnamed entries and every named entry except INFO/DEBUG from
// the REST access logger; per-source files receive every entry for that logger.
type sourceLoggerSet struct {
	cfg       *config.Config
	basePath  string
	sourceDir string

	mu      sync.Mutex
	main    *sourceSink
	sources map[string]*sourceSink
	// warned tracks per-source creation failures so we do not spam stderr.
	warned map[string]struct{}
}

func newSourceLoggerSet(cfg *config.Config) *sourceLoggerSet {
	return &sourceLoggerSet{
		cfg:       cfg,
		basePath:  LocalLogFilePath(cfg),
		sourceDir: LocalLogSourceDir(cfg),
		sources:   make(map[string]*sourceSink),
		warned:    make(map[string]struct{}),
	}
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
	name := normalizeSourceName(ent.LoggerName)
	if name == "" {
		sink, err := s.mainSink()
		if err != nil {
			return err
		}
		return sink.core.Write(ent, fields)
	}

	sourceErr := error(nil)
	if sink, err := s.sourceSink(name); err != nil {
		sourceErr = err
	} else if err := sink.core.Write(ent, fields); err != nil {
		sourceErr = err
		s.warnOnce("write:"+name, "write source log %q failed: %v", name, err)
	}

	// HTTP access lines are only useful in rest/current.log. Keep their
	// WARN/ERROR entries in main so failures are still visible by default.
	if name == sourceRest && ent.Level < zapcore.WarnLevel {
		return sourceErr
	}

	mainSink, err := s.mainSink()
	if err != nil {
		if sourceErr != nil {
			return err
		}
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

func (s *sourceLoggerSet) sourceNames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, 0, len(s.sources))
	for name := range s.sources {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// normalizeSourceName converts a zap logger name into a safe file name.
// The empty name is the main aggregate; "main" is reserved.
func normalizeSourceName(raw string) string {
	name := strings.ToLower(strings.TrimSpace(raw))
	if name == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "._-")
	if out == "" {
		return "other"
	}
	if out == "main" {
		return "main_source"
	}
	if len(out) > 64 {
		out = out[:64]
	}
	return out
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
