package room

import (
	"slices"

	"github.com/bilirec/bilirec/internal/services/subscribe"
)

// clonePtr deep-copies a pointer so API request/response values never alias
// the cached room config.
func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// cloneStrSlicePtr deep-copies a *[]string.
func cloneStrSlicePtr(p *[]string) *[]string {
	if p == nil {
		return nil
	}
	v := slices.Clone(*p)
	return &v
}

// roomConfigResponse converts the stored room config into the additive API
// response (nil override fields = inherit global).
func roomConfigResponse(roomID int, cfg *subscribe.RoomConfig) RoomConfigResponse {
	resp := RoomConfigResponse{RoomId: roomID}
	if cfg == nil {
		return resp
	}
	resp.AddedAt = cfg.AddedAt
	resp.AutoRecord = cfg.AutoRecord
	resp.Notify = cfg.Notify
	resp.RecordDurationMinutes = cfg.RecordDurationMinutes
	resp.Qn = cfg.Qn
	resp.OnlyAudio = cfg.OnlyAudio
	resp.RecordDanmaku = cfg.RecordDanmaku
	resp.StreamProfiles = slices.Clone(cfg.StreamProfiles)
	resp.DeleteOldestOnLowDisk = cfg.DeleteOldestOnLowDisk

	resp.SegmentMode = clonePtr(cfg.SegmentMode)
	resp.SegmentMinutes = clonePtr(cfg.SegmentMinutes)
	resp.SegmentSizeMB = clonePtr(cfg.SegmentSizeMB)
	resp.SegmentByTitle = clonePtr(cfg.SegmentByTitle)
	resp.MinFileSizeMB = clonePtr(cfg.MinFileSizeMB)
	resp.FilenameTemplate = clonePtr(cfg.FilenameTemplate)
	resp.TitleFilterPatterns = clonePtr(cfg.TitleFilterPatterns)
	resp.SaveStreamCover = clonePtr(cfg.SaveStreamCover)
	resp.DanmakuTypes = clonePtr(cfg.DanmakuTypes)
	resp.DanmakuTransport = clonePtr(cfg.DanmakuTransport)
	resp.FlvWriteMetadata = clonePtr(cfg.FlvWriteMetadata)
	resp.FlvSplitOnScriptTag = clonePtr(cfg.FlvSplitOnScriptTag)
	resp.FlvDisableSplitOnAnnexB = clonePtr(cfg.FlvDisableSplitOnAnnexB)
	resp.BiliLiveAPIHost = clonePtr(cfg.BiliLiveAPIHost)
	resp.BiliLiveAPIFallbackHost = clonePtr(cfg.BiliLiveAPIFallbackHost)
	resp.BiliCDN = cloneStrSlicePtr(cfg.BiliCDN)
	resp.BiliCDNFallback = clonePtr(cfg.BiliCDNFallback)
	resp.BiliForceSource = clonePtr(cfg.BiliForceSource)
	resp.BiliReplaceCN01 = cloneStrSlicePtr(cfg.BiliReplaceCN01)
	return resp
}

// roomConfigFromUpdate builds the stored room config from a PUT request.
// New override fields keep nil semantics (absent/null = inherit global).
func roomConfigFromUpdate(req UpdateRoomConfigRequest, streamProfiles []string) *subscribe.RoomConfig {
	cfg := &subscribe.RoomConfig{
		AutoRecord:            req.AutoRecord,
		Notify:                req.Notify,
		RecordDurationMinutes: req.RecordDurationMinutes,
		Qn:                    req.Qn,
		OnlyAudio:             req.OnlyAudio,
		RecordDanmaku:         req.RecordDanmaku,
		StreamProfiles:        slices.Clone(streamProfiles),
		DeleteOldestOnLowDisk: req.DeleteOldestOnLowDisk,

		SegmentMode:             clonePtr(req.SegmentMode),
		SegmentMinutes:          clonePtr(req.SegmentMinutes),
		SegmentSizeMB:           clonePtr(req.SegmentSizeMB),
		SegmentByTitle:          clonePtr(req.SegmentByTitle),
		MinFileSizeMB:           clonePtr(req.MinFileSizeMB),
		FilenameTemplate:        clonePtr(req.FilenameTemplate),
		TitleFilterPatterns:     clonePtr(req.TitleFilterPatterns),
		SaveStreamCover:         clonePtr(req.SaveStreamCover),
		DanmakuTypes:            clonePtr(req.DanmakuTypes),
		DanmakuTransport:        clonePtr(req.DanmakuTransport),
		FlvWriteMetadata:        clonePtr(req.FlvWriteMetadata),
		FlvSplitOnScriptTag:     clonePtr(req.FlvSplitOnScriptTag),
		FlvDisableSplitOnAnnexB: clonePtr(req.FlvDisableSplitOnAnnexB),
		BiliLiveAPIHost:         clonePtr(req.BiliLiveAPIHost),
		BiliLiveAPIFallbackHost: clonePtr(req.BiliLiveAPIFallbackHost),
		BiliCDN:                 cloneStrSlicePtr(req.BiliCDN),
		BiliCDNFallback:         clonePtr(req.BiliCDNFallback),
		BiliForceSource:         clonePtr(req.BiliForceSource),
		BiliReplaceCN01:         cloneStrSlicePtr(req.BiliReplaceCN01),
	}
	return cfg
}
