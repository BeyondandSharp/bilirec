// SPDX-License-Identifier: GPL-3.0-only

// Package logs exposes the admin-only local log viewer API introduced by
// docs/LOGS-VIEW.md: tail of the current local log file and a download endpoint.
// It never accepts a path parameter; only the configured LOCAL_LOGS_PATH (or
// <DATABASE_DIR>/bilirec.log) is opened and rotated backups are never exposed.
package logs

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
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
)

// Controller exposes GET /logs and GET /logs/download (admin only).
type Controller struct {
	cfg *config.Config
}

// NewController registers the admin-only log viewer routes.
func NewController(app *fiber.App, cfg *config.Config) *Controller {
	c := &Controller{cfg: cfg}
	app.Get("/logs", rest.AdminOnly, c.getLogs)
	app.Get("/logs/download", rest.AdminOnly, c.downloadLogs)
	return c
}

// LogsResponse is the GET /logs payload (docs/LOGS-VIEW.md).
type LogsResponse struct {
	Enabled   bool     `json:"enabled"`
	Path      string   `json:"path"`
	Exists    bool     `json:"exists"`
	Size      int64    `json:"size"`
	MTime     string   `json:"mtime"`
	Format    string   `json:"format"`
	Lines     []string `json:"lines"`
	Truncated bool     `json:"truncated"`
}

// logFilePath returns the configured local log file path, mirroring
// internal/modules/logging wireLocalLogger: LOCAL_LOGS_PATH when set, otherwise
// <DATABASE_DIR>/bilirec.log. It never uses request input.
func logFilePath(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	if path := strings.TrimSpace(cfg.LocalLogsPath); path != "" {
		return path
	}
	return filepath.Join(cfg.DatabaseDir, "bilirec.log")
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

// @Summary Tail the local log file
// @Description Returns the last lines of the current local log file (max 2 MiB read window). Admin only. Only the configured LOCAL_LOGS_PATH (or DATABASE_DIR/bilirec.log) is read; rotated files are never exposed.
// @Tags logs
// @Security BearerAuth
// @Produce json
// @Param lines query int false "Number of lines to return (default 500, min 1, max 2000)"
// @Success 200 {object} LogsResponse "Log tail"
// @Failure 400 {string} string "Invalid lines parameter"
// @Failure 401 {string} string "Unauthorized"
// @Failure 403 {string} string "Not Admin"
// @Failure 500 {string} string "Internal server error"
// @Router /logs [get]
func (c *Controller) getLogs(ctx fiber.Ctx) error {
	lines, err := parseLines(ctx.Query("lines"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	path := logFilePath(c.cfg)
	response := LogsResponse{
		Enabled: c.enabled(),
		Path:    path,
		Format:  c.format(),
		Lines:   []string{},
	}
	if path == "" {
		return ctx.JSON(response)
	}
	info, statErr := os.Stat(path)
	if statErr != nil {
		if !errors.Is(statErr, os.ErrNotExist) {
			log.Warnf("读取日志文件信息失败：%v", statErr)
			return fiber.ErrInternalServerError
		}
		return ctx.JSON(response)
	}
	if info.IsDir() {
		log.Warnf("日志路径是目录而不是文件：%s", path)
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

// @Summary Download the local log file
// @Description Streams the current local log file as text/plain attachment. Admin only; 404 when local logging is disabled or the file is missing.
// @Tags logs
// @Security BearerAuth
// @Produce plain
// @Success 200 {string} string "Log file contents"
// @Failure 401 {string} string "Unauthorized"
// @Failure 403 {string} string "Not Admin"
// @Failure 404 {string} string "Local logging disabled or file missing"
// @Failure 500 {string} string "Internal server error"
// @Router /logs/download [get]
func (c *Controller) downloadLogs(ctx fiber.Ctx) error {
	if !c.enabled() {
		return fiber.NewError(fiber.StatusNotFound, "本地日志未启用")
	}
	path := logFilePath(c.cfg)
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fiber.NewError(fiber.StatusNotFound, "日志文件不存在")
		}
		log.Errorf("读取日志文件信息失败：%v", err)
		return fiber.ErrInternalServerError
	}
	if info.IsDir() {
		return fiber.NewError(fiber.StatusNotFound, "日志文件不存在")
	}
	file, err := os.Open(path)
	if err != nil {
		log.Errorf("打开日志文件失败：%v", err)
		return fiber.ErrInternalServerError
	}
	ctx.Set(fiber.HeaderContentType, "text/plain; charset=utf-8")
	ctx.Set(fiber.HeaderContentDisposition, `attachment; filename="`+downloadName+`"`)
	return ctx.SendStream(file)
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
