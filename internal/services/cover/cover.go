// SPDX-License-Identifier: GPL-3.0-or-later

// Package cover fetches Bilibili live-room covers for recordings
// (docs/FEATURES-V2.md §3.4). It is strictly best effort: every failure is a
// warning and recording is never affected.
package cover

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/pkg/logger"
)

const (
	// defaultTimeout is the per-request timeout (§3.4: 5s).
	defaultTimeout = 5 * time.Second
	// defaultMaxBytes caps the downloaded cover size (§3.4: 20MB).
	defaultMaxBytes = 20 * 1024 * 1024
	// coverSuffix is appended to the video basename.
	coverSuffix = "-cover.jpg"
)

// Service downloads stream covers.
type Service struct {
	bilic    *bilibili.Client
	client   *http.Client
	maxBytes int64
	timeout  time.Duration
}

// NewService is the fx provider.
func NewService(bilic *bilibili.Client) *Service {
	return &Service{
		bilic:    bilic,
		client:   &http.Client{},
		maxBytes: defaultMaxBytes,
		timeout:  defaultTimeout,
	}
}

// PathFor returns the cover sidecar path for a video output path: same
// directory/basename plus "-cover.jpg".
func PathFor(videoPath string) string {
	ext := filepath.Ext(videoPath)
	return strings.TrimSuffix(videoPath, ext) + coverSuffix
}

// SaveAsync fetches the cover asynchronously and writes it next to the video.
// It never fails the recording: all errors are logged as warnings.
func (s *Service) SaveAsync(ctx context.Context, coverURL, destPath string, l logger.Logger) {
	if s == nil || strings.TrimSpace(coverURL) == "" || strings.TrimSpace(destPath) == "" {
		return
	}
	go func() {
		if err := s.save(ctx, coverURL, destPath); err != nil && ctx.Err() == nil {
			l.Warnf("获取直播封面失败：%v", err)
		}
	}()
}

// save downloads and writes the cover unless the file already exists.
func (s *Service) save(ctx context.Context, coverURL, destPath string) error {
	if _, err := os.Stat(destPath); err == nil {
		return nil // already fetched for this segment
	}

	data, err := s.fetch(ctx, coverURL)
	if err != nil {
		return err
	}
	tmp := destPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("写入封面失败：%w", err)
	}
	if err := os.Rename(tmp, destPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("保存封面失败：%w", err)
	}
	return nil
}

func (s *Service) fetch(ctx context.Context, coverURL string) ([]byte, error) {
	timeout := s.timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, coverURL, nil)
	if err != nil {
		return nil, err
	}
	if s.bilic != nil {
		for key, value := range s.bilic.StreamRequestHeaders() {
			req.Header.Set(key, value)
		}
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("封面响应状态码 %d", resp.StatusCode)
	}
	contentType := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	if !strings.HasPrefix(contentType, "image/") {
		return nil, fmt.Errorf("封面内容类型无效：%q", contentType)
	}

	maxBytes := s.maxBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxBytes
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, errors.New("封面文件超过大小限制")
	}
	return data, nil
}
