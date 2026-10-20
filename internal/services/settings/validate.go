package settings

import (
	"regexp"
	"strings"
	"sync"

	"github.com/bilirec/bilirec/internal/services/naming"
	"github.com/bilirec/bilirec/internal/services/subscribe"
)

// validateGlobal validates a complete settings value set per
// docs/FEATURES-V2.md §2. Returns *ValidationError (HTTP 400) on failure.
func validateGlobal(g GlobalSettings) error {
	switch g.SegmentMode {
	case "off", "time", "size":
	default:
		return validationErrorf("segment_mode 无效：仅支持 off、time、size")
	}
	if g.SegmentMode == "time" {
		if g.SegmentMinutes < 1 || g.SegmentMinutes > 1440 {
			return validationErrorf("segment_minutes 必须在 1-1440 之间")
		}
	}
	if g.SegmentMode == "size" {
		if g.SegmentSizeMB < 16 || g.SegmentSizeMB > 65536 {
			return validationErrorf("segment_size_mb 必须在 16-65536 之间")
		}
	}

	if g.MinFileSizeMB < 0 || g.MinFileSizeMB > 1024 {
		return validationErrorf("min_file_size_mb 必须在 0-1024 之间")
	}
	if err := naming.ValidatePathTemplate(g.FilenameTemplate); err != nil {
		return validationErrorf("filename_template 无效：%v", err)
	}
	if len(g.TitleFilterPatterns) > 4096 {
		return validationErrorf("title_filter_patterns 不能超过 4096 字节")
	}
	if err := compileTitlePatterns(g.TitleFilterPatterns); err != nil {
		return err
	}

	if !validDanmakuTransport(g.DanmakuTransport) {
		return validationErrorf("danmaku_transport 无效：仅支持 auto、ws、wss（不支持 tcp）")
	}

	if !validAPIHost(g.BiliLiveAPIHost) {
		return validationErrorf("bili_live_api_host 必须是绝对的 http(s) URL")
	}
	if g.BiliLiveAPIFallbackHost != "" && !validAPIHost(g.BiliLiveAPIFallbackHost) {
		return validationErrorf("bili_live_api_fallback_host 必须是绝对的 http(s) URL")
	}
	if len(g.BiliCDN) > 8 {
		return validationErrorf("bili_cdn 最多支持 8 个条目")
	}
	for _, entry := range g.BiliCDN {
		if !hostNamePattern.MatchString(entry) {
			return validationErrorf("bili_cdn 条目无效：%s（仅允许 A-Z a-z 0-9 . -）", entry)
		}
	}
	if len(g.BiliReplaceCN01) > 8 {
		return validationErrorf("bili_replace_cn01 最多支持 8 个条目")
	}
	for _, entry := range g.BiliReplaceCN01 {
		if !hostNamePattern.MatchString(entry) {
			return validationErrorf("bili_replace_cn01 条目无效：%s（仅允许 A-Z a-z 0-9 . -）", entry)
		}
	}
	return nil
}

// ValidateRoomOverrides validates the v2 override fields of a room config as a
// complete GlobalSettings built from the env-seeded defaults plus the room
// overrides (docs/FEATURES-V2.md §2 rules). Returns *ValidationError on failure.
func (s *Service) ValidateRoomOverrides(cfg *subscribe.RoomConfig) error {
	if cfg == nil || s == nil {
		return nil
	}
	s.mu.RLock()
	base := cloneGlobal(s.defaults)
	s.mu.RUnlock()

	eff := effectiveFromGlobal(base)
	applyRoomOverrides(&eff, cfg)
	return validateGlobal(globalFromEffective(eff))
}

// compileTitlePatterns compiles every non-empty line of the filter config as an
// RE2 expression. Empty configuration means "no filtering".
func compileTitlePatterns(patterns string) error {
	for idx, line := range strings.Split(patterns, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if _, err := regexp.Compile(line); err != nil {
			return validationErrorf("title_filter_patterns 第 %d 行正则无效：%v", idx+1, err)
		}
	}
	return nil
}

// titlePatternCache caches compiled filter rule sets by pattern string. The
// title filter runs on every subcheck cycle, so compiling once per settings
// snapshot matters (docs/FEATURES-V2.md §3.3).
var titlePatternCache sync.Map // string -> []*regexp.Regexp

// CompiledTitlePatterns returns the compiled non-empty filter lines, caching
// the result by pattern string. Invalid patterns are skipped defensively
// (settings validation rejects them before they are persisted).
func CompiledTitlePatterns(patterns string) []*regexp.Regexp {
	if patterns == "" {
		return nil
	}
	if cached, ok := titlePatternCache.Load(patterns); ok {
		return cached.([]*regexp.Regexp)
	}
	compiled := make([]*regexp.Regexp, 0, 4)
	for _, line := range strings.Split(patterns, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		re, err := regexp.Compile(line)
		if err != nil {
			continue
		}
		compiled = append(compiled, re)
	}
	titlePatternCache.Store(patterns, compiled)
	return compiled
}

// TitleMatches reports whether any non-empty filter line matches title.
func TitleMatches(patterns, title string) bool {
	for _, re := range CompiledTitlePatterns(patterns) {
		if re.MatchString(title) {
			return true
		}
	}
	return false
}
