package settings

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/bilirec/bilirec/internal/services/naming"
	"github.com/bilirec/bilirec/pkg/size"
)

// DefaultFilenameTemplate is the CONFIG-V2 §3 default path template (relative to
// OUTPUT_DIR; "/" creates subdirectories).
const DefaultFilenameTemplate = "{roomid}-{uname}/{date}_{time}_{title}"

// builtinDefaults returns the built-in defaults used when an env value is
// invalid and as the base for every merged value. They reproduce the pre-v2
// recording behavior (all new features off/inherit, metadata kept, current
// danmaku types).
func builtinDefaults() GlobalSettings {
	return GlobalSettings{
		SegmentMode:    "off",
		SegmentMinutes: 120,
		SegmentSizeMB:  2048,
		SegmentByTitle: false,

		MinFileSizeMB: 0,
		// CONFIG-V2 §3: default path template relative to OUTPUT_DIR. An
		// explicit empty value keeps the legacy v0.2.0 naming.
		FilenameTemplate:    DefaultFilenameTemplate,
		TitleFilterPatterns: "",
		SaveStreamCover:     false,

		DanmakuTypes:     DanmakuTypes{RawEvents: false, SuperChat: true, Gift: true, Guard: true},
		DanmakuTransport: "auto",

		FlvWriteMetadata:        true,
		FlvSplitOnScriptTag:     false,
		FlvDisableSplitOnAnnexB: true,

		BiliLiveAPIHost:         "https://api.live.bilibili.com",
		BiliLiveAPIFallbackHost: "",
		BiliCDN:                 []string{},
		BiliCDNFallback:         false,
		BiliForceSource:         false,
		BiliReplaceCN01:         []string{},
	}
}

// loadDefaultsFromEnv parses the §1.1 environment defaults once at startup.
// Invalid values fall back to built-in defaults with a single warning each.
func loadDefaultsFromEnv() GlobalSettings {
	d := builtinDefaults()

	d.SegmentMode = envString("SEGMENT_MODE", d.SegmentMode, validSegmentMode)
	d.SegmentMinutes = envInt("SEGMENT_MINUTES", d.SegmentMinutes, 1, 1440)
	d.SegmentSizeMB = envSizeMiB("SEGMENT_SIZE_MB", d.SegmentSizeMB, 16, 65536)
	d.SegmentByTitle = envBool("SEGMENT_BY_TITLE", d.SegmentByTitle)

	d.MinFileSizeMB = envSizeMiB("MIN_FILE_SIZE_MB", d.MinFileSizeMB, 0, 1024)
	// FILENAME_TEMPLATE distinguishes "unset" (new path-template default) from
	// an explicit empty string (legacy naming, docs/CONFIG-V2.md §3).
	if raw, ok := envRaw("FILENAME_TEMPLATE"); ok {
		if raw == "" {
			d.FilenameTemplate = ""
		} else if validFilenameTemplate(raw) {
			d.FilenameTemplate = raw
		} else {
			log.Warnf("环境变量 FILENAME_TEMPLATE 无效（%q），使用默认值 %q", raw, DefaultFilenameTemplate)
		}
	}
	d.TitleFilterPatterns = envString("TITLE_FILTER_PATTERNS", d.TitleFilterPatterns, validTitleFilterPatterns)
	d.SaveStreamCover = envBool("SAVE_STREAM_COVER", d.SaveStreamCover)

	d.DanmakuTypes = envDanmakuTypes(d.DanmakuTypes)
	d.DanmakuTransport = envString("DANMAKU_TRANSPORT", d.DanmakuTransport, validDanmakuTransport)

	d.FlvWriteMetadata = envBool("FLV_WRITE_METADATA", d.FlvWriteMetadata)
	d.FlvSplitOnScriptTag = envBool("FLV_SPLIT_ON_SCRIPT_TAG", d.FlvSplitOnScriptTag)
	d.FlvDisableSplitOnAnnexB = envBool("FLV_DISABLE_SPLIT_ON_ANNEXB", d.FlvDisableSplitOnAnnexB)

	d.BiliLiveAPIHost = envString("BILI_LIVE_API_HOST", d.BiliLiveAPIHost, validAPIHost)
	d.BiliLiveAPIFallbackHost = envString("BILI_LIVE_API_FALLBACK_HOST", d.BiliLiveAPIFallbackHost, validOptionalAPIHost)
	d.BiliCDN = envCSV("BILI_CDN", d.BiliCDN, validHostList)
	d.BiliCDNFallback = envBool("BILI_CDN_FALLBACK", d.BiliCDNFallback)
	d.BiliForceSource = envBool("BILI_FORCE_SOURCE", d.BiliForceSource)
	d.BiliReplaceCN01 = envCSV("BILI_REPLACE_CN01", d.BiliReplaceCN01, validHostList)

	return d
}

// envSizeMiB parses a size env value with bare numbers interpreted as MiB.
func envSizeMiB(key string, def, minValue, maxValue int) int {
	raw, ok := envRaw(key)
	if !ok || raw == "" {
		return def
	}
	parsed, err := size.ParseMiB(raw)
	if err != nil || parsed < minValue || parsed > maxValue {
		log.Warnf("环境变量 %s 无效（%q），使用默认值 %d", key, raw, def)
		return def
	}
	return parsed
}

func envDanmakuTypes(def DanmakuTypes) DanmakuTypes {
	raw, ok := envRaw("DANMAKU_RECORD_TYPES")
	if !ok || raw == "" {
		return def
	}
	types, valid := parseDanmakuTypes(raw)
	if !valid {
		log.Warnf("环境变量 DANMAKU_RECORD_TYPES 无效（%q），使用默认值 raw_events=%t,super_chat=%t,gift=%t,guard=%t",
			raw, def.RawEvents, def.SuperChat, def.Gift, def.Guard)
		return def
	}
	return types
}

// parseDanmakuTypes parses a CSV of raw_events|super_chat|gift|guard.
func parseDanmakuTypes(raw string) (DanmakuTypes, bool) {
	var types DanmakuTypes
	for _, token := range strings.Split(raw, ",") {
		token = strings.TrimSpace(strings.ToLower(token))
		if token == "" {
			continue
		}
		switch token {
		case "raw_events":
			types.RawEvents = true
		case "super_chat":
			types.SuperChat = true
		case "gift":
			types.Gift = true
		case "guard":
			types.Guard = true
		default:
			return DanmakuTypes{}, false
		}
	}
	return types, true
}

func validSegmentMode(value string) bool {
	switch value {
	case "off", "time", "size":
		return true
	default:
		return false
	}
}

func validDanmakuTransport(value string) bool {
	switch value {
	case "auto", "ws", "wss":
		return true
	default:
		return false
	}
}

func validFilenameTemplate(value string) bool {
	return naming.ValidatePathTemplate(value) == nil
}

func validTitleFilterPatterns(value string) bool {
	if len(value) > 4096 {
		return false
	}
	return compileTitlePatterns(value) == nil
}

// hostNamePattern is the allow-list for BILI_CDN / BILI_REPLACE_CN01 entries.
var hostNamePattern = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)

func validAPIHost(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	return parsed.Host != ""
}

func validOptionalAPIHost(raw string) bool {
	return strings.TrimSpace(raw) == "" || validAPIHost(raw)
}

func validHostList(values []string) bool {
	if len(values) > 8 {
		return false
	}
	for _, value := range values {
		if !hostNamePattern.MatchString(value) {
			return false
		}
	}
	return true
}

// cloneStrings returns a non-nil copy so empty defaults keep the [] JSON shape.
func cloneStrings(values []string) []string {
	out := make([]string, len(values))
	copy(out, values)
	return out
}
