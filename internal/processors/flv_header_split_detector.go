package processors

import (
	"context"
	"errors"

	"github.com/bilirec/bilirec/pkg/logger"

	"github.com/bilirec/bilirec/pkg/flv"
	"github.com/bilirec/bilirec/pkg/pipeline"
)

// ErrVideoHeaderChanged is re-exported so callers in this package can match
// without importing pkg/flv directly.
var ErrVideoHeaderChanged = flv.ErrVideoHeaderChanged

// flvHeaderSplitDetectorProcessor is a thin pipeline wrapper around
// flv.HeaderChangeDetector. All parsing logic lives in pkg/flv.
type flvHeaderSplitDetectorProcessor struct {
	detector        *flv.HeaderChangeDetector
	log             logger.Logger
	seedVideoHeader []byte // pre-seeded initial video header tag bytes
	onHeaders       func(videoTag, audioTag []byte)
	reportedVideo   bool
	reportedAudio   bool
}

// NewFlvHeaderSplitDetector returns a ProcessorInfo that signals pipe rotation
// via ErrVideoHeaderChanged whenever the video sequence header changes.
func NewFlvHeaderSplitDetector() *pipeline.ProcessorInfo[[]byte] {
	return pipeline.NewProcessorInfo(
		"flv-header-split-detector",
		&flvHeaderSplitDetectorProcessor{
			detector: flv.NewHeaderChangeDetector(),
		},
		pipeline.WithErrorStrategy[[]byte](pipeline.ReturnNextOnError),
	)
}

// NewFlvHeaderSplitDetectorSeeded returns a ProcessorInfo pre-seeded with the
// video sequence header from the prior segment. This ensures the detector can
// detect changes from the very first sequence header it sees in the new segment
// rather than treating it as the baseline "first" header.
// videoHeaderTag must be the full FLV tag bytes as returned in
// FlvHeaderChangedError.VideoHeaderTag.
func NewFlvHeaderSplitDetectorSeeded(videoHeaderTag []byte) *pipeline.ProcessorInfo[[]byte] {
	return pipeline.NewProcessorInfo(
		"flv-header-split-detector",
		&flvHeaderSplitDetectorProcessor{
			detector:        flv.NewHeaderChangeDetector(),
			seedVideoHeader: videoHeaderTag,
		},
		pipeline.WithErrorStrategy[[]byte](pipeline.ReturnNextOnError),
	)
}

func NewFlvHeaderSplitDetectorSeededWithHook(videoHeaderTag []byte, onHeaders func(videoTag, audioTag []byte)) *pipeline.ProcessorInfo[[]byte] {
	return pipeline.NewProcessorInfo(
		"flv-header-split-detector",
		&flvHeaderSplitDetectorProcessor{
			detector:        flv.NewHeaderChangeDetector(),
			seedVideoHeader: videoHeaderTag,
			onHeaders:       onHeaders,
		},
		pipeline.WithErrorStrategy[[]byte](pipeline.ReturnNextOnError),
	)
}

func (p *flvHeaderSplitDetectorProcessor) Open(ctx context.Context, log logger.Logger) error {
	p.log = log
	p.reportedVideo = false
	p.reportedAudio = false
	p.detector.Reset()
	if len(p.seedVideoHeader) > 0 {
		p.detector.SeedVideoHeader(p.seedVideoHeader)
	}
	return nil
}

func (p *flvHeaderSplitDetectorProcessor) Process(ctx context.Context, log logger.Logger, data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}

	err := p.detector.DetectChange(data)
	if err == nil {
		p.reportHeaders(p.detector.LastVideoTag(), p.detector.LastAudioTag())
		return data, nil
	}

	var headerChanged *flv.FlvHeaderChangedError
	if errors.As(err, &headerChanged) {
		// Observe the new headers before signalling rotation so a configured
		// size/time rotation later in the session can carry them.
		p.reportedVideo = true
		p.reportedAudio = headerChanged.AudioHeaderTag != nil
		if p.onHeaders != nil {
			p.onHeaders(headerChanged.VideoHeaderTag, headerChanged.AudioHeaderTag)
		}
		log.Infof("🔀 视频序列头已变化，触发管道轮转")
		// Only return bytes after the changed video seq-header tag.
		// Bytes before the changed tag belong to the old stream config and should
		// not be replayed into the next segment.
		if headerChanged.TagEnd >= 0 && headerChanged.TagEnd <= len(data) {
			after := data[headerChanged.TagEnd:]
			if len(after) == 0 {
				return nil, err
			}
			// RealtimeFixer expects each tag to be preceded by PrevTagSize (4 bytes).
			// Since we cut exactly after a tag boundary, inject a synthetic
			// PrevTagSize to keep parser alignment for replay into next segment.
			carried := make([]byte, 0, flv.PrevTagSizeBytes+len(after))
			carried = append(carried, 0, 0, 0, 0)
			carried = append(carried, after...)
			return carried, err
		}
		return nil, err
	}

	return data, err
}

// reportHeaders forwards the current sequence-header tags to the hook the
// first time each of the video/audio headers is seen.
func (p *flvHeaderSplitDetectorProcessor) reportHeaders(videoTag, audioTag []byte) {
	if p.onHeaders == nil {
		return
	}
	changed := false
	if !p.reportedVideo && len(videoTag) > 0 {
		p.reportedVideo = true
		changed = true
	}
	if !p.reportedAudio && len(audioTag) > 0 {
		p.reportedAudio = true
		changed = true
	}
	if !changed {
		return
	}
	p.onHeaders(videoTag, audioTag)
}

func (p *flvHeaderSplitDetectorProcessor) Close() error {
	p.detector.Close()
	return nil
}
