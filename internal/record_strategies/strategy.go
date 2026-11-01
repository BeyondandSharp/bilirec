package record_strategies

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/processors"
	"github.com/bilirec/bilirec/pkg/flv"
	"github.com/bilirec/bilirec/pkg/pipeline"
	"github.com/bilirec/bilirec/pkg/pool"
)

// PipelineHooks are session-level observers applied when a segment pipeline is built.
// Writer callbacks apply to every format. OnTimestampJump is used by FLV only.
type PipelineHooks struct {
	OnBytesWritten  func(n int)
	OnFlush         func(processors.FlushEvent)
	OnSync          func(processors.SyncEvent)
	OnEnqueueWait   func(processors.EnqueueWaitEvent)
	OnTimestampJump flv.TimestampJumpReporter
}

// RotationState carries format-specific state across segment rotations.
type RotationState struct {
	Data map[string][]byte
}

// ErrRotationRequested signals a configured segmentation point (size/time/title
// triggers in docs/FEATURES-V2.md §3.1). Strategies map it to ErrActionRotate
// while preserving their per-format header/state carry.
var ErrRotationRequested = errors.New("按配置切段")

type ErrAction uint8

const (
	ErrActionAbort ErrAction = iota
	ErrActionRotate
)

type ErrHandleResult struct {
	Action     ErrAction
	State      *RotationState
	AbortDelay time.Duration
}

// RecordStrategy encapsulates format-specific pipeline construction and rotation logic.
type RecordStrategy interface {
	FileExtension() string
	BuildPipeline(ctx context.Context, outputPath string, state *RotationState, hooks PipelineHooks) (*pipeline.Pipe[[]byte], error)
	HandleErr(err error) ErrHandleResult
	Close() error
}

// KeyframeRotationStrategy is implemented by strategies whose stream can be
// split at a keyframe tag boundary. The recorder holds pending rotation data
// until FindRotationBoundary returns an offset, then moves that suffix into the
// next segment. Strategies without this interface keep chunk-boundary rotation.
type KeyframeRotationStrategy interface {
	FindRotationBoundary(data []byte, allowAudioFallback bool) int
}

// NewFor selects a record strategy for the given stream URL metadata.
//
// Note: the fork's recorder keeps its own selection (it carries FLV split
// options and downloader choices); NewFor exists for upstream-compatible
// callers/tests and uses the default FLV options.
func NewFor(info bilibili.StreamURLInfo) (RecordStrategy, error) {
	switch info.Format {
	case "flv":
		return NewFlvStrategy(info.Qn), nil
	case "ts":
		return NewHlsTsStrategy(info.Qn), nil
	case "fmp4":
		return NewHlsFmp4Strategy(info.Qn), nil
	default:
		return nil, fmt.Errorf("unsupported format: %s", info.Format)
	}
}

var (
	writerPoolOnce sync.Once
	writerPools    *pool.LazyDualPool[*pool.BucketedBytesPool]

	parsePoolOnce sync.Once
	parsePools    *pool.LazyDualPool[*pool.BufferPool]
)

func newWriterPool(size int) *pool.BucketedBytesPool {
	chanBuf := config.ReadOnly.LiveStreamWriterChanBufferSize()
	perBucket := config.LiveStreamWriterBytesPoolBoundedCapacityPerBucket(chanBuf)
	return pool.NewBucketedBytesPool(size,
		pool.WithPoolBoundedMode(true),
		pool.WithPoolBoundedCapacity(perBucket),
		pool.WithPoolMaxRetainedBytes(perBucket*size),
	)
}

func newParseBufferPool(size int) *pool.BufferPool {
	return pool.NewBufferPool(
		size,
		size,
		pool.WithBoundedMode(true),
		pool.WithBoundedCapacity(2),
	)
}

func getWriterPools() *pool.LazyDualPool[*pool.BucketedBytesPool] {
	writerPoolOnce.Do(func() {
		writerPools = pool.NewLazyDualPool(
			pool.DefaultLazyDualPoolIdleTTL,
			func() *pool.BucketedBytesPool { return newWriterPool(config.ReadOnly.LiveStreamWriterBytesPoolSize()) },
			func() *pool.BucketedBytesPool {
				return newWriterPool(config.ReadOnly.LiveStreamWriterBytesPoolSizeHigh())
			},
		)
	})
	return writerPools
}

func getParseBufferPools() *pool.LazyDualPool[*pool.BufferPool] {
	parsePoolOnce.Do(func() {
		parsePools = pool.NewLazyDualPool(
			pool.DefaultLazyDualPoolIdleTTL,
			func() *pool.BufferPool { return newParseBufferPool(config.ReadOnly.ReadStreamBytesPoolSize()) },
			func() *pool.BufferPool { return newParseBufferPool(config.ReadOnly.ReadStreamBytesPoolSizeHigh()) },
		)
	})
	return parsePools
}

func acquireWriterPool(qn int) (*pool.BucketedBytesPool, func()) {
	return getWriterPools().Acquire(config.IsHighQualityQn(qn))
}

func acquireParseBufferPool(qn int) (*pool.BufferPool, func()) {
	return getParseBufferPools().Acquire(config.IsHighQualityQn(qn))
}
