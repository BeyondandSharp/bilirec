package recorder

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/services/settings"
	"github.com/bilirec/bilirec/pkg/backoff"
	"github.com/bilirec/bilirec/pkg/ds"
	"github.com/bilirec/bilirec/pkg/pool"
)

type Info struct {
	status             atomic.Pointer[RecordStatus]
	bytesRead          atomic.Uint64
	bytesWritten       atomic.Uint64
	actualQn           atomic.Int32
	actualStreamFormat ds.Atomic[string]
	// Session pipeline source, exposed through the recording status API:
	// downloader actually used (native|ffmpeg|streamlink), original Bilibili
	// source format (flv|ts|fmp4) and repair state at session start (off|on).
	// Live repair state (degraded) is tracked by repair.Service.
	sessionDownloader ds.Atomic[string]
	sessionSource     ds.Atomic[string]
	sessionRepair     ds.Atomic[string]
	// title is the live room title; it may be updated by the title poller
	// (docs/FEATURES-V2.md §3.1) and is used by naming/webhooks/stop messages.
	title       ds.Atomic[string]
	isAudioOnly atomic.Bool
	// liveSubtitleEnabled mirrors the room's AI-subtitle availability observed
	// when the stream URLs were fetched (upstream v0.4.0).
	liveSubtitleEnabled atomic.Bool
	startTime           time.Time // duration / stats (ElapsedSeconds, max recording)
	fileTime            time.Time // rotateFilePath filename timestamp
	outputPath          ds.Atomic[string]
	maxDuration         time.Duration // internal runtime semantics: 0 = unlimited

	// effective is the immutable settings snapshot resolved at session start
	// (docs/FEATURES-V2.md §1.2). Recovery keeps the original snapshot.
	effective settings.Effective

	// Per-segment rotation state (docs/FEATURES-V2.md §3.1). segmentBytes and
	// segmentStart are only touched from the recording goroutine; the rotation
	// request may also be set by the title poller goroutine.
	segmentBytes      atomic.Int64
	segmentStart      time.Time
	rotationRequested atomic.Bool
	rotationReasonMu  sync.Mutex
	rotationReason    string

	ctx          context.Context
	cancel       context.CancelFunc
	startOptions RecordStartOptions
	room         *bilibili.LiveRoomInfoDetail
	backoff      backoff.Backoff
	chunkPool    *pool.BucketedBytesPool

	sessionID       string
	segmentOpenTime time.Time
}

func (r *Info) SetStream(qn int, audioOnly bool, streamFormat string) {
	r.actualQn.Store(int32(qn))
	r.isAudioOnly.Store(audioOnly)
	if streamFormat != "" {
		r.actualStreamFormat.Store(streamFormat)
	}
}

// SetSessionSource records which pipeline fetched this session's stream:
// downloader is the kind actually used (after any native fallback),
// sourceFormat is the original Bilibili format (flv|ts|fmp4) and repair is
// the repair state at start (off|on). Values are stored atomically because
// GetStats may read them concurrently on recovery.
func (r *Info) SetSessionSource(downloader, sourceFormat, repair string) {
	if downloader != "" {
		r.sessionDownloader.Store(downloader)
	}
	if sourceFormat != "" {
		r.sessionSource.Store(sourceFormat)
	}
	r.sessionRepair.Store(repair)
}

// GetSessionDownloader returns the downloader actually used by this session.
func (r *Info) GetSessionDownloader() string {
	return r.sessionDownloader.LoadOr("")
}

// GetSessionSourceFormat returns the original Bilibili source format.
func (r *Info) GetSessionSourceFormat() string {
	return r.sessionSource.LoadOr("")
}

// GetSessionRepair returns the repair state recorded at session start
// (off|on). The live state (including degraded) comes from repair.Service.
func (r *Info) GetSessionRepair() string {
	return r.sessionRepair.LoadOr("off")
}

// SetTitle updates the live room title (thread-safe).
func (r *Info) SetTitle(title string) {
	r.title.Store(title)
}

// Title returns the current live room title, falling back to the title seen at
// session start.
func (r *Info) Title() string {
	if title, ok := r.title.Load(); ok {
		return title
	}
	if r.room != nil {
		return r.room.Title
	}
	return ""
}

// RequestRotation marks the current segment for rotation with a reason
// (size|time|title). The first request in a segment wins.
func (r *Info) RequestRotation(reason string) {
	r.rotationReasonMu.Lock()
	if r.rotationReason == "" {
		r.rotationReason = reason
	}
	r.rotationReasonMu.Unlock()
	r.rotationRequested.Store(true)
}

// RotationPending reports whether a rotation was requested for the current
// segment.
func (r *Info) RotationPending() bool {
	return r.rotationRequested.Load()
}

// takeRotationReason returns and clears the current rotation reason.
func (r *Info) takeRotationReason() string {
	r.rotationRequested.Store(false)
	r.rotationReasonMu.Lock()
	defer r.rotationReasonMu.Unlock()
	reason := r.rotationReason
	r.rotationReason = ""
	return reason
}

// resetSegmentRotation clears per-segment counters after a rotation.
func (r *Info) resetSegmentRotation(start time.Time) {
	r.segmentBytes.Store(0)
	r.segmentStart = start
	r.rotationRequested.Store(false)
	r.rotationReasonMu.Lock()
	r.rotationReason = ""
	r.rotationReasonMu.Unlock()
}

// addSegmentBytes records bytes written into the current segment and requests a
// rotation when the configured size threshold is reached.
func (r *Info) addSegmentBytes(n int) {
	r.segmentBytes.Add(int64(n))
	if r.effective.SegmentMode != "size" || r.effective.SegmentSizeMB <= 0 {
		return
	}
	if r.segmentBytes.Load() >= int64(r.effective.SegmentSizeMB)*1024*1024 {
		r.RequestRotation("size")
	}
}

// checkSegmentTime requests a rotation when the configured segment duration has
// elapsed.
func (r *Info) checkSegmentTime() {
	if r.effective.SegmentMode != "time" || r.effective.SegmentMinutes <= 0 {
		return
	}
	if r.segmentStart.IsZero() {
		return
	}
	if time.Since(r.segmentStart) >= time.Duration(r.effective.SegmentMinutes)*time.Minute {
		r.RequestRotation("time")
	}
}

func (r *Info) SetOutputPath(path string) {
	r.outputPath.Store(path)
}

func (r *Info) OutputPath() string {
	v, ok := r.outputPath.Load()
	if !ok {
		return ""
	}
	return v
}
