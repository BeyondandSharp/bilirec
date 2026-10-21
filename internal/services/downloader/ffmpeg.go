// SPDX-License-Identifier: GPL-3.0-only

package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// FFmpegDownloader fetches a live stream with ffmpeg and remuxes it to FLV on
// stdout: `ffmpeg -loglevel error -y -headers <hdr> -rw_timeout 20000000
// [-max_reload 1000] -fflags +genpts -i <url> -c copy -reset_timestamps 1
// -avoid_negative_ts make_zero -f flv -`.
type FFmpegDownloader struct {
	opts Options
}

// NewFFmpegDownloader builds an ffmpeg downloader.
func NewFFmpegDownloader(opts Options) *FFmpegDownloader {
	return &FFmpegDownloader{opts: opts.withDefaults()}
}

// Name implements Downloader.
func (d *FFmpegDownloader) Name() string { return string(KindFFmpeg) }

// Open implements Downloader.
func (d *FFmpegDownloader) Open(ctx context.Context, spec Spec) (io.ReadCloser, error) {
	if strings.TrimSpace(spec.URL) == "" {
		return nil, errors.New("ffmpeg：流 URL 为空")
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg：创建输出管道失败：%w", err)
	}
	cmd, lines, err := startCmd("ffmpeg", d.opts.FFmpegBin, ffmpegArgs(spec), nil, outW, log)
	if err != nil {
		_ = outR.Close()
		_ = outW.Close()
		return nil, fmt.Errorf("启动 ffmpeg 失败：%w", err)
	}
	// The parent no longer writes to the child's stdout: EOF arrives once the
	// process tree exits.
	_ = outW.Close()
	proc := newProcSet([]*exec.Cmd{cmd}, outR, []*lineWriter{lines})
	watchContext(ctx, proc)

	first, err := proc.awaitFirstByte(ctx, d.opts.StartTimeout)
	if err != nil {
		return nil, err
	}
	return &prefixedReadCloser{prefix: first, proc: proc}, nil
}

// ffmpegArgs builds the ffmpeg command line for one stream spec.
func ffmpegArgs(spec Spec) []string {
	args := []string{"-loglevel", "error", "-y"}
	if headers := headerLines(spec.Headers); headers != "" {
		args = append(args, "-headers", headers)
	}
	args = append(args, "-rw_timeout", "20000000")
	if isHLS(spec) {
		args = append(args, "-max_reload", "1000")
	}
	args = append(args,
		"-fflags", "+genpts",
		"-i", spec.URL,
	)
	args = append(args, ffmpegOutputArgs()...)
	return args
}

// ffmpegOutputArgs are the shared output options that produce a clean FLV
// stream on stdout.
func ffmpegOutputArgs() []string {
	return []string{
		"-c", "copy",
		"-reset_timestamps", "1",
		"-avoid_negative_ts", "make_zero",
		"-f", "flv",
		"-",
	}
}
