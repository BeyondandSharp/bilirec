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

// StreamlinkDownloader fetches a live stream through a
// `streamlink ... -O` -> `ffmpeg -i pipe:0` hybrid and remuxes it to FLV on
// stdout. Close kills both processes.
type StreamlinkDownloader struct {
	opts Options
}

// NewStreamlinkDownloader builds a streamlink -> ffmpeg downloader.
func NewStreamlinkDownloader(opts Options) *StreamlinkDownloader {
	return &StreamlinkDownloader{opts: opts.withDefaults()}
}

// Name implements Downloader.
func (d *StreamlinkDownloader) Name() string { return string(KindStreamlink) }

// Open implements Downloader.
func (d *StreamlinkDownloader) Open(ctx context.Context, spec Spec) (io.ReadCloser, error) {
	if strings.TrimSpace(spec.URL) == "" {
		return nil, errors.New("streamlink：流 URL 为空")
	}

	// streamlink stdout -> ffmpeg stdin.
	slR, slW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("streamlink：创建管道失败：%w", err)
	}
	// ffmpeg stdout -> recorder.
	outR, outW, err := os.Pipe()
	if err != nil {
		_ = slR.Close()
		_ = slW.Close()
		return nil, fmt.Errorf("streamlink：创建输出管道失败：%w", err)
	}

	slCmd, slLines, err := startCmd("streamlink", d.opts.StreamlinkBin, streamlinkArgs(spec), nil, slW, log)
	if err != nil {
		_ = slR.Close()
		_ = slW.Close()
		_ = outR.Close()
		_ = outW.Close()
		return nil, fmt.Errorf("启动 streamlink 失败：%w", err)
	}

	ffCmd, ffLines, err := startCmd("ffmpeg", d.opts.FFmpegBin, streamlinkFFmpegArgs(), slR, outW, log)
	if err != nil {
		_ = killProcessTree(slCmd)
		_ = slCmd.Wait()
		_ = slR.Close()
		_ = slW.Close()
		_ = outR.Close()
		_ = outW.Close()
		return nil, fmt.Errorf("启动 ffmpeg 失败：%w", err)
	}

	// The children hold their own descriptors; drop the parent copies so EOF
	// propagates when either side exits.
	_ = slR.Close()
	_ = slW.Close()
	_ = outW.Close()

	proc := newProcSet([]*exec.Cmd{slCmd, ffCmd}, outR, []*lineWriter{slLines, ffLines})
	watchContext(ctx, proc)

	first, err := proc.awaitFirstByte(ctx, d.opts.StartTimeout)
	if err != nil {
		return nil, err
	}
	return &prefixedReadCloser{prefix: first, proc: proc}, nil
}

// streamlinkArgs builds the streamlink CLI arguments.
func streamlinkArgs(spec Spec) []string {
	args := []string{
		"--stream-segment-threads", "3",
		"--hls-playlist-reload-attempts", "1",
	}
	args = append(args, streamlinkHeaderArgs(spec.Headers)...)
	args = append(args,
		NormalizeURL(spec.URL),
		"best",
		"-O",
	)
	return args
}

// streamlinkFFmpegArgs builds the ffmpeg stage of the hybrid pipeline. Input
// headers/timeouts are handled by streamlink, so only the remux part differs
// from the direct ffmpeg downloader.
func streamlinkFFmpegArgs() []string {
	args := []string{
		"-loglevel", "error",
		"-y",
		"-fflags", "+genpts",
		"-i", "pipe:0",
	}
	args = append(args, ffmpegOutputArgs()...)
	return args
}
