package recorder

import (
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/services/downloader"
)

type RecordStartOptions struct {
	hasDuration           bool
	duration              time.Duration
	streamOptions         []bilibili.GetStreamURLsOption
	recordDanmaku         bool
	deleteOldestOnLowDisk bool
	hasDownloader         bool
	downloaderKind        downloader.Kind
}

type RecordStartOption func(*RecordStartOptions)

func newRecordStartOptions() RecordStartOptions {
	return RecordStartOptions{}
}

func WithDuration(d time.Duration) RecordStartOption {
	return func(o *RecordStartOptions) {
		// Internal recorder semantics: d == 0 means unlimited.
		o.hasDuration = true
		o.duration = d
	}
}

func WithStreamOptions(opts ...bilibili.GetStreamURLsOption) RecordStartOption {
	return func(o *RecordStartOptions) {
		o.streamOptions = opts
	}
}

// WithRecordDanmaku enables or disables live-chat sidecar recording for this start.
// Default is false when the option is omitted.
func WithRecordDanmaku(enabled bool) RecordStartOption {
	return func(o *RecordStartOptions) {
		o.recordDanmaku = enabled
	}
}

// WithDeleteOldestOnLowDisk enables deleting this room's oldest recording by filename
// when free space is below MIN_DISK_SPACE_BYTES before starting.
func WithDeleteOldestOnLowDisk(enabled bool) RecordStartOption {
	return func(o *RecordStartOptions) {
		o.deleteOldestOnLowDisk = enabled
	}
}

// WithDownloader overrides the global DOWNLOADER for this recording.
// downloader.KindNative keeps the built-in HTTP path.
func WithDownloader(kind downloader.Kind) RecordStartOption {
	return func(o *RecordStartOptions) {
		o.hasDownloader = true
		o.downloaderKind = kind
	}
}

func snapshotStartOptions(o RecordStartOptions) RecordStartOptions {
	out := o
	if len(o.streamOptions) > 0 {
		out.streamOptions = append([]bilibili.GetStreamURLsOption(nil), o.streamOptions...)
	}
	return out
}
