// SPDX-License-Identifier: GPL-3.0-only

// Package logs exposes the admin-only local log viewer API introduced by
// docs/LOGS-VIEW.md: tail of the current local log file, its per-source files
// and download endpoints. It never accepts a path parameter; only the configured
// LOCAL_LOGS_PATH or a validated source name below its .d directory is opened.
package logs

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/modules/logging"
	"github.com/bilirec/bilirec/internal/modules/rest"
	"github.com/bilirec/bilirec/pkg/logger"
	"github.com/gofiber/fiber/v3"
)

var log = logger.Named("logs")

const (
	defaultLogLines = 500
	maxLogLines     = 2000
	// tailWindowBytes bounds how much of the current file is read (2 MiB).
	tailWindowBytes = 2 << 20
	downloadName    = "bilirec.log"
	mainSource      = "main"
)

// Controller exposes /logs, /logs/sources and /logs/download (admin only).
type Controller struct {
	cfg *config.Config
}

// NewController registers the admin-only log viewer routes.
func NewController(app *fiber.App, cfg *config.Config) *Controller {
	c := &Controller{cfg: cfg}
	app.Get("/logs", rest.AdminOnly, c.getLogs)
	app.Get("/logs/sources", rest.AdminOnly, c.listSources)
	app.Get("/logs/download", rest.AdminOnly, c.downloadLogs)
	return c
}

// LogsResponse is the GET /logs payload (docs/LOGS-VIEW.md).
type LogsResponse struct {
	Source    string   `json:"source"`
	Split     bool     `json:"split"`
	Enabled   bool     `json:"enabled"`
	Path      string   `json:"path"`
	Exists    bool     `json:"exists"`
	Size      int64    `json:"size"`
	MTime     string   `json:"mtime"`
	Format    string   `json:"format"`
	Lines     []string `json:"lines"`
	Truncated bool     `json:"truncated"`
}

// LogSource describes one current log file exposed by /logs/sources.
type LogSource struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	Size   int64  `json:"size"`
	MTime  string `json:"mtime"`
}

// LogSourcesResponse is the GET /logs/sources payload.
type LogSourcesResponse struct {
	Enabled    bool        `json:"enabled"`
	Split      bool        `json:"split"`
	BasePath   string      `json:"base_path"`
	SourcesDir string      `json:"sources_dir"`
	Sources    []LogSource `json:"sources"`
}

// logFilePath returns the configured main local log path (docs/LOGS-VIEW.md).
func logFilePath(cfg *config.Config) string {
	return logging.LocalLogFilePath(cfg)
}

func (c *Controller) split() bool {
	return c.cfg != nil && c.cfg.LocalLogsSplit
}

func (c *Controller) sourcesDir() string {
	return logging.LocalLogSourceDir(c.cfg)
}

func (c *Controller) enabled() bool {
	return c.cfg != nil && c.cfg.LocalLogsEnabled
}

func (c *Controller) format() string {
	if c.cfg == nil || strings.TrimSpace(c.cfg.LocalLogsFormat) == "" {
		return "pretty"
	}
	return c.cfg.LocalLogsFormat
}

// parseLines validates the ?lines= query parameter (default 500, 1..2000).
func parseLines(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultLogLines, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, errors.New("lines 必须是整数")
	}
	if n < 1 || n > maxLogLines {
		return 0, errors.New("lines 必须在 1 到 2000 之间")
	}
	return n, nil
}

// validSourceName mirrors the logging package's file-name rules. Source names
// are never used as arbitrary paths: they must be a single safe component.
func validSourceName(name string) bool {
	if name == "" || name == "." || name == ".." || name == mainSource {
		return false
	}
	if len(name) > 64 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

func sourcePathForName(cfg *config.Config, name string) string {
	return filepath.Join(logging.LocalLogSourceDir(cfg), name, "current.log")
}

// resolveSource maps the optional ?source= value to a concrete path. The empty
// value and "main" resolve to the configured main log file; other values must
// be safe, existing per-source directories. Request input never becomes a path
// on its own.
func (c *Controller) resolveSource(raw string) (name, path string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == mainSource {
		return mainSource, logFilePath(c.cfg), nil
	}
	if !c.split() {
		return "", "", errors.New("日志未按来源拆分")
	}
	if !validSourceName(raw) {
		return "", "", errors.New("无效的日志来源")
	}
	if c.cfg == nil {
		return "", "", errors.New("日志配置不可用")
	}
	dir := logging.LocalLogSourceDir(c.cfg)
	info, statErr := os.Lstat(filepath.Join(dir, raw))
	if statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return "", "", errors.New("日志来源不存在")
		}
		return "", "", statErr
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", "", errors.New("无效的日志来源")
	}
	path = sourcePathForName(c.cfg, raw)
	fileInfo, statErr := os.Lstat(path)
	if statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return "", "", errors.New("日志来源不存在")
		}
		return "", "", statErr
	}
	if fileInfo.Mode()&os.ModeSymlink != 0 || !fileInfo.Mode().IsRegular() {
		return "", "", errors.New("日志来源不是普通文件")
	}
	return raw, path, nil
}

func fileSource(name, path string) (LogSource, bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return LogSource{Name: name, Path: path}, false, nil
		}
		return LogSource{}, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return LogSource{Name: name, Path: path}, false, errors.New("日志来源不是普通文件")
	}
	return LogSource{
		Name:   name,
		Path:   path,
		Exists: true,
		Size:   info.Size(),
		MTime:  info.ModTime().Format(time.RFC3339),
	}, true, nil
}

// listSourceFiles enumerates only <sources-dir>/<source>/current.log files.
// Rotated lumberjack backups live in the same directory but are not exposed.
func (c *Controller) listSourceFiles() ([]LogSource, error) {
	out := make([]LogSource, 0, 8)
	if c.cfg == nil {
		return out, nil
	}
	main, _, err := fileSource(mainSource, logFilePath(c.cfg))
	if err != nil {
		return nil, err
	}
	out = append(out, main)
	if !c.split() {
		return out, nil
	}
	dir := logging.LocalLogSourceDir(c.cfg)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !validSourceName(entry.Name()) {
			continue
		}
		source, ok, err := fileSource(entry.Name(), filepath.Join(dir, entry.Name(), "current.log"))
		if err != nil {
			log.Warnf("检查日志来源 %q 失败：%v", entry.Name(), err)
			continue
		}
		if !ok {
			continue
		}
		out = append(out, source)
	}
	sort.Slice(out[1:], func(i, j int) bool { return out[1+i].Name < out[1+j].Name })
	return out, nil
}

// @Summary List local log sources
// @Description Lists the main local log file and per-source current.log files. Admin only; rotated backups and arbitrary paths are never exposed.
// @Tags logs
// @Security BearerAuth
// @Produce json
// @Success 200 {object} LogSourcesResponse "Log sources"
// @Failure 401 {string} string "Unauthorized"
// @Failure 403 {string} string "Not Admin"
// @Failure 500 {string} string "Internal server error"
// @Router /logs/sources [get]
func (c *Controller) listSources(ctx fiber.Ctx) error {
	sources, err := c.listSourceFiles()
	if err != nil {
		log.Errorf("列出日志来源失败：%v", err)
		return fiber.ErrInternalServerError
	}
	resp := LogSourcesResponse{
		Enabled:    c.enabled(),
		Split:      c.split(),
		BasePath:   logFilePath(c.cfg),
		SourcesDir: c.sourcesDir(),
		Sources:    sources,
	}
	return ctx.JSON(resp)
}

// @Summary Tail a local log source
// @Description Returns the last lines of the main local log file or a validated per-source current.log (max 2 MiB read window). Admin only.
// @Tags logs
// @Security BearerAuth
// @Produce json
// @Param lines query int false "Number of lines to return (default 500, min 1, max 2000)"
// @Param source query string false "Source name from /logs/sources (default main)"
// @Success 200 {object} LogsResponse "Log tail"
// @Failure 400 {string} string "Invalid lines or source parameter"
// @Failure 401 {string} string "Unauthorized"
// @Failure 403 {string} string "Not Admin"
// @Failure 500 {string} string "Internal server error"
// @Router /logs [get]
func (c *Controller) getLogs(ctx fiber.Ctx) error {
	lines, err := parseLines(ctx.Query("lines"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	source, path, err := c.resolveSource(ctx.Query("source"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	response := LogsResponse{
		Source:  source,
		Split:   c.split(),
		Enabled: c.enabled(),
		Path:    path,
		Format:  c.format(),
		Lines:   []string{},
	}
	if path == "" {
		return ctx.JSON(response)
	}
	info, statErr := os.Lstat(path)
	if statErr != nil {
		if !errors.Is(statErr, os.ErrNotExist) {
			log.Warnf("读取日志文件信息失败：%v", statErr)
			return fiber.ErrInternalServerError
		}
		return ctx.JSON(response)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		log.Warnf("日志路径不是普通文件：%s", path)
		return ctx.JSON(response)
	}
	response.Exists = true
	response.Size = info.Size()
	response.MTime = info.ModTime().Format(time.RFC3339)
	if !response.Enabled {
		return ctx.JSON(response)
	}
	tail, truncated, err := tailFile(path, lines)
	if err != nil {
		log.Errorf("读取日志尾部失败：%v", err)
		return fiber.ErrInternalServerError
	}
	response.Lines = tail
	response.Truncated = truncated
	return ctx.JSON(response)
}

func downloadFilename(source string) string {
	if source == "" || source == mainSource {
		return downloadName
	}
	return source + ".log"
}

// @Summary Download a local log source
// @Description Streams the current main log file or a validated per-source current.log as text/plain attachment. Admin only; 404 when local logging is disabled or the file is missing.
// @Tags logs
// @Security BearerAuth
// @Produce plain
// @Param source query string false "Source name from /logs/sources (default main)"
// @Success 200 {string} string "Log file contents"
// @Failure 400 {string} string "Invalid source parameter"
// @Failure 401 {string} string "Unauthorized"
// @Failure 403 {string} string "Not Admin"
// @Failure 404 {string} string "Local logging disabled or file missing"
// @Failure 500 {string} string "Internal server error"
// @Router /logs/download [get]
func (c *Controller) downloadLogs(ctx fiber.Ctx) error {
	if !c.enabled() {
		return fiber.NewError(fiber.StatusNotFound, "本地日志未启用")
	}
	source, path, err := c.resolveSource(ctx.Query("source"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fiber.NewError(fiber.StatusNotFound, "日志文件不存在")
		}
		log.Errorf("读取日志文件信息失败：%v", err)
		return fiber.ErrInternalServerError
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fiber.NewError(fiber.StatusNotFound, "日志文件不存在")
	}
	file, err := os.Open(path)
	if err != nil {
		log.Errorf("打开日志文件失败：%v", err)
		return fiber.ErrInternalServerError
	}
	ctx.Set(fiber.HeaderContentType, "text/plain; charset=utf-8")
	ctx.Set(fiber.HeaderContentDisposition, `attachment; filename="`+downloadFilename(source)+`"`)
	return ctx.SendStream(file)
}

// tailFile reads at most the last tailWindowBytes of path and returns the last
// `lines` complete lines. truncated is true when bytes were dropped from the
// beginning of the window or more lines existed than requested.
func tailFile(path string, lines int) ([]string, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, false, err
	}
	size := info.Size()
	start := int64(0)
	truncated := false
	if size > tailWindowBytes {
		start = size - tailWindowBytes
		truncated = true
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return nil, false, err
	}
	data, err := io.ReadAll(io.LimitReader(file, tailWindowBytes))
	if err != nil {
		return nil, false, err
	}
	if start > 0 {
		// We may have started mid-line: drop the partial first line.
		idx := strings.IndexByte(string(data), '\n')
		if idx < 0 {
			return []string{}, true, nil
		}
		data = data[idx+1:]
	}
	text := string(data)
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return []string{}, truncated, nil
	}
	all := strings.Split(text, "\n")
	if len(all) > lines {
		truncated = true
		all = all[len(all)-lines:]
	}
	return all, truncated, nil
}
