// SPDX-License-Identifier: GPL-3.0-or-later

// Package downloader provides external stream downloaders for recording
// sessions. It implements the contract in docs/INTERFACES.md §2.1: an ffmpeg
// downloader and a streamlink -> ffmpeg hybrid downloader, both remuxing the
// live stream to FLV on stdout.
package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/bilirec/bilirec/pkg/logger"
)

var log = logger.Named("downloader")

// Kind identifies the stream fetch path used by a recording session.
type Kind string

const (
	KindNative     Kind = "native"
	KindFFmpeg     Kind = "ffmpeg"
	KindStreamlink Kind = "streamlink"
)

// ParseKind parses an explicit downloader kind. "auto" is not a Kind: callers
// that support it must resolve it against their configured default before
// calling ParseKind.
func ParseKind(raw string) (Kind, error) {
	switch kind := Kind(strings.ToLower(strings.TrimSpace(raw))); kind {
	case KindNative, KindFFmpeg, KindStreamlink:
		return kind, nil
	default:
		return "", fmt.Errorf("无效的下载器：%q（仅支持 native、ffmpeg、streamlink）", raw)
	}
}

// IsExternal reports whether the kind opens an external process.
func (k Kind) IsExternal() bool {
	return k == KindFFmpeg || k == KindStreamlink
}

// Spec describes how to fetch one stream.
type Spec struct {
	URL     string            // resolved stream/playlist URL (from bilibili.StreamURLInfo)
	Format  string            // source format: "flv" | "ts" | "fmp4"
	Headers map[string]string // Cookie, User-Agent, Referer, Origin
}

// Downloader spawns an external downloader process tree.
type Downloader interface {
	Name() string
	// Open spawns the process tree and returns a reader of the FLV output.
	// It waits for the first output byte up to the configured start timeout;
	// on failure the spawned processes are killed. Close on the returned
	// io.ReadCloser kills the whole process tree.
	Open(ctx context.Context, spec Spec) (io.ReadCloser, error)
}

// Options configures the external downloader binaries and startup deadline.
type Options struct {
	FFmpegBin     string
	StreamlinkBin string
	StartTimeout  time.Duration // first-byte deadline; <= 0 means 20s
}

const (
	defaultFFmpegBin     = "ffmpeg"
	defaultStreamlinkBin = "streamlink"
	defaultStartTimeout  = 20 * time.Second
)

func (o Options) withDefaults() Options {
	if strings.TrimSpace(o.FFmpegBin) == "" {
		o.FFmpegBin = defaultFFmpegBin
	}
	if strings.TrimSpace(o.StreamlinkBin) == "" {
		o.StreamlinkBin = defaultStreamlinkBin
	}
	if o.StartTimeout <= 0 {
		o.StartTimeout = defaultStartTimeout
	}
	return o
}

// Errors returned by external downloader startup.
var (
	// ErrStartTimeout means the process tree produced no output before the
	// configured first-byte deadline.
	ErrStartTimeout = errors.New("外部下载器启动超时：未在限定时间内收到首字节")
	// ErrNoFirstByte means the process tree exited before producing any output.
	ErrNoFirstByte = errors.New("外部下载器在输出首字节前退出")
)

// Registry holds the available external downloaders keyed by Kind.
type Registry struct {
	downloaders map[Kind]Downloader
}

// NewRegistry builds the default downloader set.
func NewRegistry(opts Options) *Registry {
	opts = opts.withDefaults()
	r := &Registry{downloaders: make(map[Kind]Downloader, 2)}
	r.downloaders[KindFFmpeg] = NewFFmpegDownloader(opts)
	r.downloaders[KindStreamlink] = NewStreamlinkDownloader(opts)
	return r
}

// Get returns the downloader for kind, or nil (native / unknown).
func (r *Registry) Get(kind Kind) Downloader {
	if r == nil {
		return nil
	}
	return r.downloaders[kind]
}

// Register installs (or replaces) the downloader for kind. Used by tests and
// by future per-platform overrides.
func (r *Registry) Register(kind Kind, d Downloader) {
	if r == nil || d == nil {
		return
	}
	if r.downloaders == nil {
		r.downloaders = make(map[Kind]Downloader, 2)
	}
	r.downloaders[kind] = d
}

// headerOrder is the deterministic header order used when formatting the
// external downloader command lines.
var headerOrder = []string{"Cookie", "User-Agent", "Referer", "Origin"}

// orderedHeaders returns non-empty headers as ordered key/value pairs:
// contract headers first in a fixed order, then any extras sorted by key.
func orderedHeaders(headers map[string]string) [][2]string {
	if len(headers) == 0 {
		return nil
	}
	out := make([][2]string, 0, len(headers))
	used := make(map[string]struct{}, len(headers))
	for _, key := range headerOrder {
		value, ok := lookupHeader(headers, key)
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		out = append(out, [2]string{key, value})
		used[key] = struct{}{}
	}
	extras := make([]string, 0, len(headers))
	for key := range headers {
		if _, ok := used[key]; ok {
			continue
		}
		if canon, ok := canonicalHeaderKey(key); ok {
			if _, dup := used[canon]; dup {
				continue
			}
			used[canon] = struct{}{}
		}
		extras = append(extras, key)
	}
	sort.Strings(extras)
	for _, key := range extras {
		if strings.TrimSpace(headers[key]) == "" {
			continue
		}
		out = append(out, [2]string{key, headers[key]})
	}
	return out
}

func lookupHeader(headers map[string]string, key string) (string, bool) {
	if value, ok := headers[key]; ok {
		return value, true
	}
	for k, v := range headers {
		if strings.EqualFold(k, key) {
			return v, true
		}
	}
	return "", false
}

func canonicalHeaderKey(key string) (string, bool) {
	for _, known := range headerOrder {
		if strings.EqualFold(key, known) {
			return known, true
		}
	}
	return "", false
}

// headerLines formats headers for ffmpeg's -headers argument ("K: V\r\n"...).
func headerLines(headers map[string]string) string {
	ordered := orderedHeaders(headers)
	if len(ordered) == 0 {
		return ""
	}
	var b strings.Builder
	for _, kv := range ordered {
		b.WriteString(kv[0])
		b.WriteString(": ")
		b.WriteString(kv[1])
		b.WriteString("\r\n")
	}
	return b.String()
}

// streamlinkHeaderArgs formats headers for streamlink (--http-header K=V).
func streamlinkHeaderArgs(headers map[string]string) []string {
	ordered := orderedHeaders(headers)
	if len(ordered) == 0 {
		return nil
	}
	args := make([]string, 0, len(ordered)*2)
	for _, kv := range ordered {
		args = append(args, "--http-header", kv[0]+"="+kv[1])
	}
	return args
}

// isHLS reports whether ffmpeg needs HLS-specific input options.
func isHLS(spec Spec) bool {
	switch spec.Format {
	case "ts", "fmp4":
		return true
	}
	return strings.Contains(strings.ToLower(spec.URL), ".m3u8")
}
