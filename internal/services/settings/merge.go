package settings

import "slices"

// normalizeGlobal guarantees non-nil slices and a stable JSON shape for API
// responses and snapshots.
func normalizeGlobal(g GlobalSettings) GlobalSettings {
	if g.BiliCDN == nil {
		g.BiliCDN = []string{}
	}
	if g.BiliReplaceCN01 == nil {
		g.BiliReplaceCN01 = []string{}
	}
	return g
}

// cloneGlobal deep-copies a GlobalSettings (slices).
func cloneGlobal(g GlobalSettings) GlobalSettings {
	g.BiliCDN = append([]string{}, g.BiliCDN...)
	g.BiliReplaceCN01 = append([]string{}, g.BiliReplaceCN01...)
	return g
}

// applyPatch overlays non-nil patch fields onto dst.
func applyPatch(dst GlobalSettings, p GlobalSettingsPatch) GlobalSettings {
	if p.SegmentMode != nil {
		dst.SegmentMode = *p.SegmentMode
	}
	if p.SegmentMinutes != nil {
		dst.SegmentMinutes = *p.SegmentMinutes
	}
	if p.SegmentSizeMB != nil {
		dst.SegmentSizeMB = *p.SegmentSizeMB
	}
	if p.SegmentByTitle != nil {
		dst.SegmentByTitle = *p.SegmentByTitle
	}
	if p.MinFileSizeMB != nil {
		dst.MinFileSizeMB = *p.MinFileSizeMB
	}
	if p.FilenameTemplate != nil {
		dst.FilenameTemplate = *p.FilenameTemplate
	}
	if p.TitleFilterPatterns != nil {
		dst.TitleFilterPatterns = *p.TitleFilterPatterns
	}
	if p.SaveStreamCover != nil {
		dst.SaveStreamCover = *p.SaveStreamCover
	}
	if p.DanmakuTypes != nil {
		dst.DanmakuTypes = *p.DanmakuTypes
	}
	if p.DanmakuTransport != nil {
		dst.DanmakuTransport = *p.DanmakuTransport
	}
	if p.FlvWriteMetadata != nil {
		dst.FlvWriteMetadata = *p.FlvWriteMetadata
	}
	if p.FlvSplitOnScriptTag != nil {
		dst.FlvSplitOnScriptTag = *p.FlvSplitOnScriptTag
	}
	if p.FlvDisableSplitOnAnnexB != nil {
		dst.FlvDisableSplitOnAnnexB = *p.FlvDisableSplitOnAnnexB
	}
	if p.BiliLiveAPIHost != nil {
		dst.BiliLiveAPIHost = *p.BiliLiveAPIHost
	}
	if p.BiliLiveAPIFallbackHost != nil {
		dst.BiliLiveAPIFallbackHost = *p.BiliLiveAPIFallbackHost
	}
	if p.BiliCDN != nil {
		dst.BiliCDN = slices.Clone(*p.BiliCDN)
	}
	if p.BiliCDNFallback != nil {
		dst.BiliCDNFallback = *p.BiliCDNFallback
	}
	if p.BiliForceSource != nil {
		dst.BiliForceSource = *p.BiliForceSource
	}
	if p.BiliReplaceCN01 != nil {
		dst.BiliReplaceCN01 = slices.Clone(*p.BiliReplaceCN01)
	}
	return dst
}

// mergePatch returns base with every non-nil field of next replaced.
func mergePatch(base, next GlobalSettingsPatch) GlobalSettingsPatch {
	if next.SegmentMode != nil {
		base.SegmentMode = next.SegmentMode
	}
	if next.SegmentMinutes != nil {
		base.SegmentMinutes = next.SegmentMinutes
	}
	if next.SegmentSizeMB != nil {
		base.SegmentSizeMB = next.SegmentSizeMB
	}
	if next.SegmentByTitle != nil {
		base.SegmentByTitle = next.SegmentByTitle
	}
	if next.MinFileSizeMB != nil {
		base.MinFileSizeMB = next.MinFileSizeMB
	}
	if next.FilenameTemplate != nil {
		base.FilenameTemplate = next.FilenameTemplate
	}
	if next.TitleFilterPatterns != nil {
		base.TitleFilterPatterns = next.TitleFilterPatterns
	}
	if next.SaveStreamCover != nil {
		base.SaveStreamCover = next.SaveStreamCover
	}
	if next.DanmakuTypes != nil {
		base.DanmakuTypes = next.DanmakuTypes
	}
	if next.DanmakuTransport != nil {
		base.DanmakuTransport = next.DanmakuTransport
	}
	if next.FlvWriteMetadata != nil {
		base.FlvWriteMetadata = next.FlvWriteMetadata
	}
	if next.FlvSplitOnScriptTag != nil {
		base.FlvSplitOnScriptTag = next.FlvSplitOnScriptTag
	}
	if next.FlvDisableSplitOnAnnexB != nil {
		base.FlvDisableSplitOnAnnexB = next.FlvDisableSplitOnAnnexB
	}
	if next.BiliLiveAPIHost != nil {
		base.BiliLiveAPIHost = next.BiliLiveAPIHost
	}
	if next.BiliLiveAPIFallbackHost != nil {
		base.BiliLiveAPIFallbackHost = next.BiliLiveAPIFallbackHost
	}
	if next.BiliCDN != nil {
		base.BiliCDN = next.BiliCDN
	}
	if next.BiliCDNFallback != nil {
		base.BiliCDNFallback = next.BiliCDNFallback
	}
	if next.BiliForceSource != nil {
		base.BiliForceSource = next.BiliForceSource
	}
	if next.BiliReplaceCN01 != nil {
		base.BiliReplaceCN01 = next.BiliReplaceCN01
	}
	return base
}

func isZeroPatch(p GlobalSettingsPatch) bool {
	return p.SegmentMode == nil &&
		p.SegmentMinutes == nil &&
		p.SegmentSizeMB == nil &&
		p.SegmentByTitle == nil &&
		p.MinFileSizeMB == nil &&
		p.FilenameTemplate == nil &&
		p.TitleFilterPatterns == nil &&
		p.SaveStreamCover == nil &&
		p.DanmakuTypes == nil &&
		p.DanmakuTransport == nil &&
		p.FlvWriteMetadata == nil &&
		p.FlvSplitOnScriptTag == nil &&
		p.FlvDisableSplitOnAnnexB == nil &&
		p.BiliLiveAPIHost == nil &&
		p.BiliLiveAPIFallbackHost == nil &&
		p.BiliCDN == nil &&
		p.BiliCDNFallback == nil &&
		p.BiliForceSource == nil &&
		p.BiliReplaceCN01 == nil
}
