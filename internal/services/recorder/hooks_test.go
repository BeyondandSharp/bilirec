package recorder

import (
	"testing"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/metrics"
	"github.com/bilirec/bilirec/internal/services/settings"
)

func TestPipelineHooks_MetricsDisabled(t *testing.T) {
	r := &Service{m: &metrics.Exporter{}}
	info := &Info{}
	hooks := r.pipelineHooks(123, info)

	if hooks.OnFlush != nil || hooks.OnSync != nil || hooks.OnEnqueueWait != nil || hooks.OnTimestampJump != nil {
		t.Fatal("metric observers must be nil when exporter is disabled")
	}
	if hooks.OnBytesWritten == nil {
		t.Fatal("OnBytesWritten must always be set for stats")
	}

	hooks.OnBytesWritten(4096)
	if got := info.bytesWritten.Load(); got != 4096 {
		t.Fatalf("expected bytesWritten 4096, got %d", got)
	}
}

func TestPipelineHooksRecordingMetadata(t *testing.T) {
	r := &Service{m: &metrics.Exporter{}}
	info := &Info{
		room: &bilibili.LiveRoomInfoDetail{
			RoomID:         123456,
			ShortID:        42,
			Uname:          "主播",
			ParentAreaName: "虚拟主播",
			AreaName:       "虚拟日常",
		},
		effective:      settings.Effective{FlvWriteMetadata: true},
		streamHost:     "example.bilivideo.com",
		streamHostFull: "example.bilivideo.com",
	}
	info.SetTitle("直播标题")
	hooks := r.pipelineHooks(123456, info)
	if hooks.Metadata == nil {
		t.Fatal("Metadata hook is nil")
	}
	md := hooks.Metadata()
	if md.Title != "直播标题" || md.StreamTitle != "直播标题" {
		t.Fatalf("titles = %q/%q", md.Title, md.StreamTitle)
	}
	if md.Name != "主播" || md.RoomID != 123456 || md.ShortID != 42 {
		t.Fatalf("room fields = %+v", md)
	}
	if md.AreaNameParent != "虚拟主播" || md.AreaNameChild != "虚拟日常" {
		t.Fatalf("area fields = %+v", md)
	}
	if md.StreamHost != "example.bilivideo.com" || md.StreamHostFull != "example.bilivideo.com" {
		t.Fatalf("stream fields = %+v", md)
	}
	if md.RecorderVersion == "" || md.StartTime.IsZero() {
		t.Fatalf("version/start = %+v", md)
	}
}
