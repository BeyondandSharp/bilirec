package settings

import (
	"slices"

	"github.com/bilirec/bilirec/internal/services/subscribe"
)

// effectiveFromGlobal converts the resolved global values into the immutable
// per-session snapshot.
func effectiveFromGlobal(g GlobalSettings) Effective {
	return Effective{
		SegmentMode:    g.SegmentMode,
		SegmentMinutes: g.SegmentMinutes,
		SegmentSizeMB:  g.SegmentSizeMB,
		SegmentByTitle: g.SegmentByTitle,

		MinFileSizeMB:       g.MinFileSizeMB,
		FilenameTemplate:    g.FilenameTemplate,
		TitleFilterPatterns: g.TitleFilterPatterns,
		SaveStreamCover:     g.SaveStreamCover,

		DanmakuTypes:     g.DanmakuTypes,
		DanmakuTransport: g.DanmakuTransport,

		FlvWriteMetadata:        g.FlvWriteMetadata,
		FlvSplitOnScriptTag:     g.FlvSplitOnScriptTag,
		FlvDisableSplitOnAnnexB: g.FlvDisableSplitOnAnnexB,

		BiliLiveAPIHost:         g.BiliLiveAPIHost,
		BiliLiveAPIFallbackHost: g.BiliLiveAPIFallbackHost,
		BiliCDN:                 append([]string{}, g.BiliCDN...),
		BiliCDNFallback:         g.BiliCDNFallback,
		BiliForceSource:         g.BiliForceSource,
		BiliReplaceCN01:         append([]string{}, g.BiliReplaceCN01...),
	}
}

// globalFromEffective converts an Effective snapshot back into the Global
// settings shape (used to validate room overrides against §2 rules).
func globalFromEffective(eff Effective) GlobalSettings {
	return GlobalSettings{
		SegmentMode:    eff.SegmentMode,
		SegmentMinutes: eff.SegmentMinutes,
		SegmentSizeMB:  eff.SegmentSizeMB,
		SegmentByTitle: eff.SegmentByTitle,

		MinFileSizeMB:       eff.MinFileSizeMB,
		FilenameTemplate:    eff.FilenameTemplate,
		TitleFilterPatterns: eff.TitleFilterPatterns,
		SaveStreamCover:     eff.SaveStreamCover,

		DanmakuTypes:     eff.DanmakuTypes,
		DanmakuTransport: eff.DanmakuTransport,

		FlvWriteMetadata:        eff.FlvWriteMetadata,
		FlvSplitOnScriptTag:     eff.FlvSplitOnScriptTag,
		FlvDisableSplitOnAnnexB: eff.FlvDisableSplitOnAnnexB,

		BiliLiveAPIHost:         eff.BiliLiveAPIHost,
		BiliLiveAPIFallbackHost: eff.BiliLiveAPIFallbackHost,
		BiliCDN:                 append([]string{}, eff.BiliCDN...),
		BiliCDNFallback:         eff.BiliCDNFallback,
		BiliForceSource:         eff.BiliForceSource,
		BiliReplaceCN01:         append([]string{}, eff.BiliReplaceCN01...),
	}
}

// applyRoomOverrides overlays the non-nil room override fields onto eff
// (nil = inherit global). See docs/FEATURES-V2.md §1.3.
func applyRoomOverrides(eff *Effective, cfg *subscribe.RoomConfig) {
	if eff == nil || cfg == nil {
		return
	}
	if cfg.SegmentMode != nil {
		eff.SegmentMode = *cfg.SegmentMode
	}
	if cfg.SegmentMinutes != nil {
		eff.SegmentMinutes = *cfg.SegmentMinutes
	}
	if cfg.SegmentSizeMB != nil {
		eff.SegmentSizeMB = *cfg.SegmentSizeMB
	}
	if cfg.SegmentByTitle != nil {
		eff.SegmentByTitle = *cfg.SegmentByTitle
	}
	if cfg.MinFileSizeMB != nil {
		eff.MinFileSizeMB = *cfg.MinFileSizeMB
	}
	if cfg.FilenameTemplate != nil {
		eff.FilenameTemplate = *cfg.FilenameTemplate
	}
	if cfg.TitleFilterPatterns != nil {
		eff.TitleFilterPatterns = *cfg.TitleFilterPatterns
	}
	if cfg.SaveStreamCover != nil {
		eff.SaveStreamCover = *cfg.SaveStreamCover
	}
	if cfg.DanmakuTypes != nil {
		eff.DanmakuTypes = *cfg.DanmakuTypes
	}
	if cfg.DanmakuTransport != nil {
		eff.DanmakuTransport = *cfg.DanmakuTransport
	}
	if cfg.FlvWriteMetadata != nil {
		eff.FlvWriteMetadata = *cfg.FlvWriteMetadata
	}
	if cfg.FlvSplitOnScriptTag != nil {
		eff.FlvSplitOnScriptTag = *cfg.FlvSplitOnScriptTag
	}
	if cfg.FlvDisableSplitOnAnnexB != nil {
		eff.FlvDisableSplitOnAnnexB = *cfg.FlvDisableSplitOnAnnexB
	}
	if cfg.BiliLiveAPIHost != nil {
		eff.BiliLiveAPIHost = *cfg.BiliLiveAPIHost
	}
	if cfg.BiliLiveAPIFallbackHost != nil {
		eff.BiliLiveAPIFallbackHost = *cfg.BiliLiveAPIFallbackHost
	}
	if cfg.BiliCDN != nil {
		eff.BiliCDN = slices.Clone(*cfg.BiliCDN)
	}
	if cfg.BiliCDNFallback != nil {
		eff.BiliCDNFallback = *cfg.BiliCDNFallback
	}
	if cfg.BiliForceSource != nil {
		eff.BiliForceSource = *cfg.BiliForceSource
	}
	if cfg.BiliReplaceCN01 != nil {
		eff.BiliReplaceCN01 = slices.Clone(*cfg.BiliReplaceCN01)
	}
}
