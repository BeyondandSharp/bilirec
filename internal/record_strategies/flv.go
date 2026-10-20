package record_strategies

import (
	"context"
	"errors"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/processors"
	"github.com/bilirec/bilirec/pkg/flv"
	"github.com/bilirec/bilirec/pkg/pipeline"
	"github.com/bilirec/bilirec/pkg/pool"
)

const (
	flvStateVideoHdr = "videoHdr"
	flvStateAudioHdr = "audioHdr"
)

// FlvStrategy handles HTTP-FLV byte streams.
// It is a zero-invasion extraction of the logic previously inlined in rotate().
type FlvStrategy struct {
	sharedFixer            *flv.RealtimeFixer
	writerPool             *pool.BucketedBytesPool
	releaseWriterPool      func()
	releaseParseBufferPool func()

	// lastVideoHeader/lastAudioHeader track the current segment's sequence
	// headers so configured (size/time/title) rotations can carry them into
	// the next segment, exactly like header-change rotations do.
	lastVideoHeader []byte
	lastAudioHeader []byte

	// splitOptions enables the optional §5.2 FLV split detectors.
	splitOptions FlvSplitOptions
}

// FlvSplitOptions enables the optional FLV mid-stream split detectors
// (docs/FEATURES-V2.md §5.2). Both default to false (no behavior change).
type FlvSplitOptions struct {
	// SplitOnScriptTag rotates when a Script tag appears after the first media
	// data tag (FlvSplitOnScriptTag, default false).
	SplitOnScriptTag bool
	// SplitOnAnnexB rotates when Annex-B start codes are found in video NALU
	// payloads (enabled only when FlvDisableSplitOnAnnexB is false).
	SplitOnAnnexB bool
}

func NewFlvStrategy(qn int) *FlvStrategy {
	return NewFlvStrategyWithOptions(qn, FlvSplitOptions{})
}

func NewFlvStrategyWithOptions(qn int, split FlvSplitOptions) *FlvStrategy {
	writerPool, releaseWriterPool := acquireWriterPool(qn)
	parsePool, releaseParsePool := acquireParseBufferPool(qn)
	readBufSize := config.ReadStreamBytesPoolSizeForQn(qn)
	return &FlvStrategy{
		sharedFixer: flv.NewRealtimeFixer(
			flv.WithBufferPool(parsePool),
			flv.WithBufferSizes(readBufSize, readBufSize),
		),
		writerPool:             writerPool,
		releaseWriterPool:      releaseWriterPool,
		releaseParseBufferPool: releaseParsePool,
		splitOptions:           split,
	}
}

func (s *FlvStrategy) FileExtension() string { return ".flv" }

func (s *FlvStrategy) BuildPipeline(ctx context.Context, outputPath string, state *RotationState, hooks PipelineHooks) (*pipeline.Pipe[[]byte], error) {
	videoHdr := state.Data[flvStateVideoHdr]
	audioHdr := state.Data[flvStateAudioHdr]

	if hooks.OnTimestampJump != nil {
		s.sharedFixer.SetTimestampJumpReporter(hooks.OnTimestampJump)
	}
	if len(videoHdr) > 0 {
		s.lastVideoHeader = append([]byte(nil), videoHdr...)
	}
	if len(audioHdr) > 0 {
		s.lastAudioHeader = append([]byte(nil), audioHdr...)
	}

	procList := []*pipeline.ProcessorInfo[[]byte]{
		processors.NewFlvStreamFixerWithFixer(s.sharedFixer),
		processors.NewFlvHeaderSplitDetectorSeededWithHook(videoHdr, s.observeHeaders),
	}
	if s.splitOptions.SplitOnScriptTag {
		procList = append(procList, processors.NewFlvScriptTagSplitDetector())
	}
	if s.splitOptions.SplitOnAnnexB {
		procList = append(procList, processors.NewFlvAnnexBSplitDetector())
	}
	pipe := pipeline.New(append(procList,
		processors.NewFlvHeaderWriter(videoHdr, audioHdr),
		processors.NewBufferedStreamWriter(
			outputPath,
			processors.WithBufferSize(config.ReadOnly.LiveStreamWriterBufferSize()),
			processors.WithSkipSmallFlushThreshold(config.ReadOnly.SkipSmallFlushThreshold()),
			processors.WithSyncPeriod(time.Duration(config.ReadOnly.LiveStreamWriterSyncPeriodSecs())*time.Second),
			processors.WithColdCacheReleasePeriod(time.Duration(config.ReadOnly.LiveStreamWriterColdCacheReleaseSecs())*time.Second),
			processors.WithFlushPeriod(time.Duration(config.ReadOnly.LiveStreamWriterFlushPeriodSecs())*time.Second),
			processors.WithChanBufferSize(config.ReadOnly.LiveStreamWriterChanBufferSize()),
			processors.WithBytesPool(s.writerPool),
			processors.WithDropFilePageCache(config.ReadOnly.DropFilePageCache()),
			processors.WithSequentialWrite(config.ReadOnly.SequentialWrite()),
			processors.WithOnBytesWritten(hooks.OnBytesWritten),
			processors.WithOnFlush(hooks.OnFlush),
			processors.WithOnSync(hooks.OnSync),
			processors.WithOnEnqueueWait(hooks.OnEnqueueWait),
		),
	)...)
	return pipe, nil
}

// observeHeaders records the current sequence-header tags (called from the
// pipeline goroutine; HandleErr runs on the same goroutine after the pipeline
// stops).
func (s *FlvStrategy) observeHeaders(videoTag, audioTag []byte) {
	if len(videoTag) > 0 {
		s.lastVideoHeader = append([]byte(nil), videoTag...)
	}
	if len(audioTag) > 0 {
		s.lastAudioHeader = append([]byte(nil), audioTag...)
	}
}

func (s *FlvStrategy) HandleErr(err error) ErrHandleResult {
	if errors.Is(err, ErrRotationRequested) {
		return ErrHandleResult{Action: ErrActionRotate, State: s.currentHeadersState()}
	}
	if errors.Is(err, processors.ErrScriptTagSplit) || errors.Is(err, processors.ErrAnnexBSplit) {
		// Configured FLV split detectors: rotate with the current headers so
		// the next segment starts with a valid FLV header (docs §5.2).
		return ErrHandleResult{Action: ErrActionRotate, State: s.currentHeadersState()}
	}

	var headerChanged *flv.FlvHeaderChangedError
	if errors.As(err, &headerChanged) {
		// Reset per-stream fixer state on rotation:
		// - timestamps should restart from 0 in the new segment
		// - dedup cache should not suppress fresh leading tags across segment boundary
		s.sharedFixer.ResetTimestampStore()
		s.sharedFixer.ResetDedupCache()
		state := &RotationState{
			Data: map[string][]byte{
				flvStateVideoHdr: headerChanged.VideoHeaderTag,
				flvStateAudioHdr: headerChanged.AudioHeaderTag,
			},
		}
		return ErrHandleResult{Action: ErrActionRotate, State: state}
	}

	if errors.Is(err, processors.ErrNotFlvFile) {
		return ErrHandleResult{Action: ErrActionAbort, AbortDelay: 5 * time.Second}
	}

	return ErrHandleResult{Action: ErrActionAbort}
}

// currentHeadersState builds a rotation state carrying the most recent
// sequence headers (used for configured rotations).
func (s *FlvStrategy) currentHeadersState() *RotationState {
	state := &RotationState{Data: map[string][]byte{}}
	if len(s.lastVideoHeader) > 0 {
		state.Data[flvStateVideoHdr] = append([]byte(nil), s.lastVideoHeader...)
	}
	if len(s.lastAudioHeader) > 0 {
		state.Data[flvStateAudioHdr] = append([]byte(nil), s.lastAudioHeader...)
	}
	return state
}

func (s *FlvStrategy) Close() error {
	s.sharedFixer.Close()
	if s.releaseParseBufferPool != nil {
		s.releaseParseBufferPool()
		s.releaseParseBufferPool = nil
	}
	if s.releaseWriterPool != nil {
		s.releaseWriterPool()
		s.releaseWriterPool = nil
		s.writerPool = nil
	}
	return nil
}
