package recorder

import (
	"time"

	"github.com/bilirec/bilirec/internal/flvmeta"
	"github.com/bilirec/bilirec/internal/processors"
	rs "github.com/bilirec/bilirec/internal/record_strategies"
	"github.com/bilirec/bilirec/pkg/flv"
	"github.com/bilirec/bilirec/pkg/updatecheck"
)

const (
	slowFlushMetricThreshold = 3 * time.Second
	slowSyncMetricThreshold  = 1200 * time.Millisecond
)

// recordingMetadata builds the FLV onMetaData fields from the room/session
// state. It is evaluated once per segment (BuildPipeline) so the title and
// recording start time reflect that segment.
func (r *Service) recordingMetadata(info *Info) flvmeta.RecordingInfo {
	out := flvmeta.RecordingInfo{StartTime: time.Now()}
	if info == nil {
		return out
	}
	out.Title = info.Title()
	out.StreamTitle = out.Title
	out.StreamHost = info.streamHost
	out.StreamHostFull = info.streamHostFull
	out.RecorderVersion = updatecheck.Current()
	if out.RecorderVersion == "" {
		out.RecorderVersion = "dev"
	}
	if info.room != nil {
		out.Name = info.room.Uname
		out.RoomID = info.room.RoomID
		out.ShortID = info.room.ShortID
		out.AreaNameParent = info.room.ParentAreaName
		out.AreaNameChild = info.room.AreaName
	}
	return out
}

func (r *Service) pipelineHooks(roomID int, info *Info) rs.PipelineHooks {
	if !r.m.Enabled() {
		return rs.PipelineHooks{
			WriteMetadata: info.effective.FlvWriteMetadata,
			Metadata: func() flvmeta.RecordingInfo {
				return r.recordingMetadata(info)
			},
			OnBytesWritten: func(n int) {
				info.bytesWritten.Add(uint64(n))
			},
		}
	}

	var sess = r.m.StreamSession(roomID)

	return rs.PipelineHooks{
		WriteMetadata: info.effective.FlvWriteMetadata,
		Metadata: func() flvmeta.RecordingInfo {
			return r.recordingMetadata(info)
		},
		OnBytesWritten: func(n int) {
			info.bytesWritten.Add(uint64(n))
			sess.AddBytesWritten(n)
		},
		OnFlush: func(ev processors.FlushEvent) {
			if ev.Duration > slowFlushMetricThreshold {
				sess.RecordSlowFlush()
			}
		},
		OnSync: func(ev processors.SyncEvent) {
			if ev.Duration > slowSyncMetricThreshold {
				sess.RecordSlowSync()
			}
		},
		OnEnqueueWait: func(ev processors.EnqueueWaitEvent) {
			sess.RecordEnqueueWait(ev.Duration)
		},
		OnTimestampJump: func(w flv.TimestampJumpWarning) {
			if w.Delta > 0 {
				sess.RecordTimestampJump(w.Delta)
			}
		},
	}
}
