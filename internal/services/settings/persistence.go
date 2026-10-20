package settings

import (
	"strconv"
	"strings"

	"github.com/bilirec/bilirec/internal/modules/configfile"
)

// featureEnvToJSON maps FEATURES-V2 env keys to the /settings JSON field names
// used by the additive `sources` response.
var featureEnvToJSON = []struct {
	envKey  string
	jsonKey string
}{
	{"SEGMENT_MODE", "segment_mode"},
	{"SEGMENT_MINUTES", "segment_minutes"},
	{"SEGMENT_SIZE_MB", "segment_size_mb"},
	{"SEGMENT_BY_TITLE", "segment_by_title"},
	{"MIN_FILE_SIZE_MB", "min_file_size_mb"},
	{"FILENAME_TEMPLATE", "filename_template"},
	{"TITLE_FILTER_PATTERNS", "title_filter_patterns"},
	{"SAVE_STREAM_COVER", "save_stream_cover"},
	{"DANMAKU_RECORD_TYPES", "danmaku_types"},
	{"DANMAKU_TRANSPORT", "danmaku_transport"},
	{"FLV_WRITE_METADATA", "flv_write_metadata"},
	{"FLV_SPLIT_ON_SCRIPT_TAG", "flv_split_on_script_tag"},
	{"FLV_DISABLE_SPLIT_ON_ANNEXB", "flv_disable_split_on_annexb"},
	{"BILI_LIVE_API_HOST", "bili_live_api_host"},
	{"BILI_LIVE_API_FALLBACK_HOST", "bili_live_api_fallback_host"},
	{"BILI_CDN", "bili_cdn"},
	{"BILI_CDN_FALLBACK", "bili_cdn_fallback"},
	{"BILI_FORCE_SOURCE", "bili_force_source"},
	{"BILI_REPLACE_CN01", "bili_replace_cn01"},
}

// Sources returns the source ("env"|"file"|"dotenv"|"default") of every
// FEATURES-V2 field for the additive /settings response (docs/CONFIG-V2.md §4).
func (s *Service) Sources() map[string]string {
	sources := make(map[string]string, len(featureEnvToJSON))
	for _, entry := range featureEnvToJSON {
		source := configfile.SourceDefault
		if s.store != nil {
			source = s.store.Source(entry.envKey)
		}
		sources[entry.jsonKey] = string(source)
	}
	return sources
}

// ConfigFile returns the resolved config.json path (may be empty in tests).
func (s *Service) ConfigFile() string {
	if s.store == nil {
		return ""
	}
	return s.store.ConfigPath()
}

// SecretsFile returns the resolved secrets.json path.
func (s *Service) SecretsFile() string {
	if s.store == nil {
		return ""
	}
	return s.store.SecretsPath()
}

// RestartRequired reports FEATURES-V2 keys whose config-file value still needs
// a restart to be applied. Generic env restarts are surfaced by /config, so the
// /settings response stays scoped to feature keys (CONFIG-V2 §4).
func (s *Service) RestartRequired() []string {
	if s.store == nil {
		return nil
	}
	pending := s.store.RestartRequired()
	if len(pending) == 0 {
		return nil
	}
	feature := make(map[string]struct{}, len(configfile.FeatureKeys))
	for _, key := range configfile.FeatureKeys {
		feature[key] = struct{}{}
	}
	out := make([]string, 0, len(pending))
	for _, key := range pending {
		if _, ok := feature[key]; ok {
			out = append(out, key)
		}
	}
	return out
}

// patchFromStore decodes the FEATURES-V2 keys persisted in config.json into a
// GlobalSettingsPatch. Invalid manual file values are warned about and skipped.
func patchFromStore(store *configfile.Store) GlobalSettingsPatch {
	var patch GlobalSettingsPatch
	if store == nil {
		return patch
	}
	values := store.ConfigOverrides(configfile.FeatureKeys)
	get := func(key string) (string, bool) {
		value, ok := values[key]
		return strings.TrimSpace(value), ok
	}
	if value, ok := get("SEGMENT_MODE"); ok {
		patch.SegmentMode = &value
	}
	if value, ok := get("SEGMENT_MINUTES"); ok {
		if n, err := strconv.Atoi(value); err == nil {
			patch.SegmentMinutes = &n
		}
	}
	if value, ok := get("SEGMENT_SIZE_MB"); ok {
		if n, err := strconv.Atoi(value); err == nil {
			patch.SegmentSizeMB = &n
		}
	}
	if value, ok := get("SEGMENT_BY_TITLE"); ok {
		if b, ok := parseStoredBool(value); ok {
			patch.SegmentByTitle = &b
		}
	}
	if value, ok := get("MIN_FILE_SIZE_MB"); ok {
		if n, err := strconv.Atoi(value); err == nil {
			patch.MinFileSizeMB = &n
		}
	}
	if value, ok := get("FILENAME_TEMPLATE"); ok {
		patch.FilenameTemplate = &value
	}
	if value, ok := get("TITLE_FILTER_PATTERNS"); ok {
		patch.TitleFilterPatterns = &value
	}
	if value, ok := get("SAVE_STREAM_COVER"); ok {
		if b, ok := parseStoredBool(value); ok {
			patch.SaveStreamCover = &b
		}
	}
	if value, ok := get("DANMAKU_RECORD_TYPES"); ok {
		if types, valid := parseDanmakuTypes(value); valid {
			patch.DanmakuTypes = &types
		} else {
			log.Warnf("config.json 中的 DANMAKU_RECORD_TYPES 无效，已忽略：%q", value)
		}
	}
	if value, ok := get("DANMAKU_TRANSPORT"); ok {
		patch.DanmakuTransport = &value
	}
	if value, ok := get("FLV_WRITE_METADATA"); ok {
		if b, ok := parseStoredBool(value); ok {
			patch.FlvWriteMetadata = &b
		}
	}
	if value, ok := get("FLV_SPLIT_ON_SCRIPT_TAG"); ok {
		if b, ok := parseStoredBool(value); ok {
			patch.FlvSplitOnScriptTag = &b
		}
	}
	if value, ok := get("FLV_DISABLE_SPLIT_ON_ANNEXB"); ok {
		if b, ok := parseStoredBool(value); ok {
			patch.FlvDisableSplitOnAnnexB = &b
		}
	}
	if value, ok := get("BILI_LIVE_API_HOST"); ok {
		patch.BiliLiveAPIHost = &value
	}
	if value, ok := get("BILI_LIVE_API_FALLBACK_HOST"); ok {
		patch.BiliLiveAPIFallbackHost = &value
	}
	if value, ok := get("BILI_CDN"); ok {
		list := splitCSV(value)
		patch.BiliCDN = &list
	}
	if value, ok := get("BILI_CDN_FALLBACK"); ok {
		if b, ok := parseStoredBool(value); ok {
			patch.BiliCDNFallback = &b
		}
	}
	if value, ok := get("BILI_FORCE_SOURCE"); ok {
		if b, ok := parseStoredBool(value); ok {
			patch.BiliForceSource = &b
		}
	}
	if value, ok := get("BILI_REPLACE_CN01"); ok {
		list := splitCSV(value)
		patch.BiliReplaceCN01 = &list
	}
	return patch
}

// envMapFromPatch serializes a patch into FEATURES-V2 env-key values for
// config.json persistence.
func envMapFromPatch(patch GlobalSettingsPatch) map[string]string {
	out := make(map[string]string, len(featureEnvToJSON))
	if patch.SegmentMode != nil {
		out["SEGMENT_MODE"] = *patch.SegmentMode
	}
	if patch.SegmentMinutes != nil {
		out["SEGMENT_MINUTES"] = strconv.Itoa(*patch.SegmentMinutes)
	}
	if patch.SegmentSizeMB != nil {
		out["SEGMENT_SIZE_MB"] = strconv.Itoa(*patch.SegmentSizeMB)
	}
	if patch.SegmentByTitle != nil {
		out["SEGMENT_BY_TITLE"] = strconv.FormatBool(*patch.SegmentByTitle)
	}
	if patch.MinFileSizeMB != nil {
		out["MIN_FILE_SIZE_MB"] = strconv.Itoa(*patch.MinFileSizeMB)
	}
	if patch.FilenameTemplate != nil {
		out["FILENAME_TEMPLATE"] = *patch.FilenameTemplate
	}
	if patch.TitleFilterPatterns != nil {
		out["TITLE_FILTER_PATTERNS"] = *patch.TitleFilterPatterns
	}
	if patch.SaveStreamCover != nil {
		out["SAVE_STREAM_COVER"] = strconv.FormatBool(*patch.SaveStreamCover)
	}
	if patch.DanmakuTypes != nil {
		out["DANMAKU_RECORD_TYPES"] = danmakuTypesCSV(*patch.DanmakuTypes)
	}
	if patch.DanmakuTransport != nil {
		out["DANMAKU_TRANSPORT"] = *patch.DanmakuTransport
	}
	if patch.FlvWriteMetadata != nil {
		out["FLV_WRITE_METADATA"] = strconv.FormatBool(*patch.FlvWriteMetadata)
	}
	if patch.FlvSplitOnScriptTag != nil {
		out["FLV_SPLIT_ON_SCRIPT_TAG"] = strconv.FormatBool(*patch.FlvSplitOnScriptTag)
	}
	if patch.FlvDisableSplitOnAnnexB != nil {
		out["FLV_DISABLE_SPLIT_ON_ANNEXB"] = strconv.FormatBool(*patch.FlvDisableSplitOnAnnexB)
	}
	if patch.BiliLiveAPIHost != nil {
		out["BILI_LIVE_API_HOST"] = *patch.BiliLiveAPIHost
	}
	if patch.BiliLiveAPIFallbackHost != nil {
		out["BILI_LIVE_API_FALLBACK_HOST"] = *patch.BiliLiveAPIFallbackHost
	}
	if patch.BiliCDN != nil {
		out["BILI_CDN"] = strings.Join(*patch.BiliCDN, ",")
	}
	if patch.BiliCDNFallback != nil {
		out["BILI_CDN_FALLBACK"] = strconv.FormatBool(*patch.BiliCDNFallback)
	}
	if patch.BiliForceSource != nil {
		out["BILI_FORCE_SOURCE"] = strconv.FormatBool(*patch.BiliForceSource)
	}
	if patch.BiliReplaceCN01 != nil {
		out["BILI_REPLACE_CN01"] = strings.Join(*patch.BiliReplaceCN01, ",")
	}
	return out
}

func parseStoredBool(raw string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1", "yes", "on":
		return true, true
	case "false", "0", "no", "off":
		return false, true
	default:
		return false, false
	}
}

func splitCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func danmakuTypesCSV(types DanmakuTypes) string {
	var out []string
	if types.RawEvents {
		out = append(out, "raw_events")
	}
	if types.SuperChat {
		out = append(out, "super_chat")
	}
	if types.Gift {
		out = append(out, "gift")
	}
	if types.Guard {
		out = append(out, "guard")
	}
	return strings.Join(out, ",")
}
