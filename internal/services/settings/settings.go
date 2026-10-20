// Package settings implements the persisted recording settings introduced by
// docs/FEATURES-V2.md §1: env-seeded defaults, persisted global overrides in
// bbolt (DATABASE_DIR/settings.db, bucket Settings, key global), and an
// immutable per-session Effective snapshot that merges room overrides.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/services/subscribe"
	"github.com/bilirec/bilirec/pkg/db"
	"github.com/bilirec/bilirec/pkg/logger"
	"go.uber.org/fx"
)

var log = logger.Named("settings")

const (
	settingsDBFile    = "settings.db"
	settingsBucket    = "Settings"
	settingsGlobalKey = "global"
)

// DanmakuTypes aliases subscribe.DanmakuTypes: room overrides and global
// settings share one type (avoiding an import cycle) so Effective values merge
// without conversions.
type DanmakuTypes = subscribe.DanmakuTypes

// GlobalSettings is the JSON shape shared by the persisted overrides, the API
// request/response and the env-seeded defaults (docs/FEATURES-V2.md §1.1).
type GlobalSettings struct {
	SegmentMode    string `json:"segment_mode"` // off|time|size
	SegmentMinutes int    `json:"segment_minutes"`
	SegmentSizeMB  int    `json:"segment_size_mb"`
	SegmentByTitle bool   `json:"segment_by_title"`

	MinFileSizeMB       int    `json:"min_file_size_mb"`
	FilenameTemplate    string `json:"filename_template"`
	TitleFilterPatterns string `json:"title_filter_patterns"`
	SaveStreamCover     bool   `json:"save_stream_cover"`

	DanmakuTypes     DanmakuTypes `json:"danmaku_types"`
	DanmakuTransport string       `json:"danmaku_transport"` // auto|ws|wss

	FlvWriteMetadata        bool `json:"flv_write_metadata"`
	FlvSplitOnScriptTag     bool `json:"flv_split_on_script_tag"`
	FlvDisableSplitOnAnnexB bool `json:"flv_disable_split_on_annexb"`

	BiliLiveAPIHost         string   `json:"bili_live_api_host"`
	BiliLiveAPIFallbackHost string   `json:"bili_live_api_fallback_host"`
	BiliCDN                 []string `json:"bili_cdn"`
	BiliCDNFallback         bool     `json:"bili_cdn_fallback"`
	BiliForceSource         bool     `json:"bili_force_source"`
	BiliReplaceCN01         []string `json:"bili_replace_cn01"`
}

// GlobalSettingsPatch is a partial GlobalSettings where nil means "not
// present". PUT /settings bodies decode into this type so only present keys
// overwrite the persisted overrides.
type GlobalSettingsPatch struct {
	SegmentMode    *string `json:"segment_mode"`
	SegmentMinutes *int    `json:"segment_minutes"`
	SegmentSizeMB  *int    `json:"segment_size_mb"`
	SegmentByTitle *bool   `json:"segment_by_title"`

	MinFileSizeMB       *int    `json:"min_file_size_mb"`
	FilenameTemplate    *string `json:"filename_template"`
	TitleFilterPatterns *string `json:"title_filter_patterns"`
	SaveStreamCover     *bool   `json:"save_stream_cover"`

	DanmakuTypes     *DanmakuTypes `json:"danmaku_types"`
	DanmakuTransport *string       `json:"danmaku_transport"`

	FlvWriteMetadata        *bool `json:"flv_write_metadata"`
	FlvSplitOnScriptTag     *bool `json:"flv_split_on_script_tag"`
	FlvDisableSplitOnAnnexB *bool `json:"flv_disable_split_on_annexb"`

	BiliLiveAPIHost         *string   `json:"bili_live_api_host"`
	BiliLiveAPIFallbackHost *string   `json:"bili_live_api_fallback_host"`
	BiliCDN                 *[]string `json:"bili_cdn"`
	BiliCDNFallback         *bool     `json:"bili_cdn_fallback"`
	BiliForceSource         *bool     `json:"bili_force_source"`
	BiliReplaceCN01         *[]string `json:"bili_replace_cn01"`
}

// Effective is the immutable per-session snapshot (docs/FEATURES-V2.md §1.2).
type Effective struct {
	SegmentMode    string
	SegmentMinutes int
	SegmentSizeMB  int
	SegmentByTitle bool

	MinFileSizeMB       int
	FilenameTemplate    string
	TitleFilterPatterns string
	SaveStreamCover     bool

	DanmakuTypes     DanmakuTypes
	DanmakuTransport string

	FlvWriteMetadata        bool
	FlvSplitOnScriptTag     bool
	FlvDisableSplitOnAnnexB bool

	BiliLiveAPIHost         string
	BiliLiveAPIFallbackHost string
	BiliCDN                 []string
	BiliCDNFallback         bool
	BiliForceSource         bool
	BiliReplaceCN01         []string
}

// roomConfigSource is the subset of *subscribe.Service used by Resolve. It is
// an interface so settings tests can inject room overrides without a DB.
type roomConfigSource interface {
	GetConfig(roomID int) (*subscribe.RoomConfig, error)
}

// Service owns the defaults, the persisted overrides and the resolved values.
type Service struct {
	cfg      *config.Config
	roomCfgs roomConfigSource

	mu        sync.RWMutex
	defaults  GlobalSettings
	patch     GlobalSettingsPatch // persisted overrides (only explicit fields)
	overrides GlobalSettings
	values    GlobalSettings

	valuesPtr atomic.Pointer[GlobalSettings]

	bucket *db.Bucket
	client *db.Client
}

// NewService is the fx provider. The service opens DATABASE_DIR/settings.db on
// fx start and seeds defaults from the environment.
func NewService(lc fx.Lifecycle, cfg *config.Config, subSvc *subscribe.Service) *Service {
	// Keep a nil *subscribe.Service from becoming a non-nil interface value
	// (tests provide nil when room overrides are not exercised).
	var roomCfgs roomConfigSource
	if subSvc != nil {
		roomCfgs = subSvc
	}
	return newService(lc, cfg, roomCfgs)
}

func newService(lc fx.Lifecycle, cfg *config.Config, roomCfgs roomConfigSource) *Service {
	s := &Service{
		cfg:      cfg,
		roomCfgs: roomCfgs,
		defaults: loadDefaultsFromEnv(),
	}
	s.values = applyPatch(s.defaults, GlobalSettingsPatch{})
	s.overrides = normalizeGlobal(GlobalSettings{})
	s.publishValues()
	lc.Append(fx.StartStopHook(s.start, s.stop))
	return s
}

func (s *Service) start() error {
	if s.cfg == nil {
		return errors.New("settings：缺少配置")
	}
	client, err := db.Open(filepath.Join(s.cfg.DatabaseDir, settingsDBFile))
	if err != nil {
		return fmt.Errorf("settings：打开设置数据库失败：%w", err)
	}
	bucket, err := client.Bucket(settingsBucket)
	if err != nil {
		_ = client.Close()
		return fmt.Errorf("settings：打开设置 bucket 失败：%w", err)
	}
	s.client = client
	s.bucket = bucket

	s.mu.Lock()
	defer s.mu.Unlock()
	patch, err := s.loadOverridesLocked()
	if err != nil {
		log.Warnf("设置数据库内容无效，使用默认设置：%v", err)
		patch = GlobalSettingsPatch{}
	}
	s.patch = patch
	s.recomputeLocked()
	return nil
}

func (s *Service) stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bucket = nil
	client := s.client
	s.client = nil
	if client != nil {
		return client.Close()
	}
	return nil
}

func (s *Service) loadOverridesLocked() (GlobalSettingsPatch, error) {
	var patch GlobalSettingsPatch
	if s.bucket == nil {
		return patch, nil
	}
	var raw []byte
	if err := s.bucket.GetFunc([]byte(settingsGlobalKey), func(v []byte) error {
		raw = append(raw, v...)
		return nil
	}); err != nil {
		return patch, err
	}
	if len(raw) == 0 {
		return patch, nil
	}
	if err := json.Unmarshal(raw, &patch); err != nil {
		return GlobalSettingsPatch{}, err
	}
	return patch, nil
}

// recomputeLocked recomputes values/overrides from defaults+patch and publishes
// the immutable snapshot. Callers must hold s.mu.
func (s *Service) recomputeLocked() {
	s.overrides = normalizeGlobal(applyPatch(GlobalSettings{}, s.patch))
	values := applyPatch(s.defaults, s.patch)
	s.values = normalizeGlobal(values)
	s.publishValues()
}

func (s *Service) publishValues() {
	snapshot := s.values
	snapshot = cloneGlobal(snapshot)
	s.valuesPtr.Store(&snapshot)
}

func (s *Service) currentValues() GlobalSettings {
	if p := s.valuesPtr.Load(); p != nil {
		return *p
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneGlobal(s.values)
}

// Get returns the env-seeded defaults, the persisted overrides (only explicitly
// saved fields are non-zero) and the merged values.
func (s *Service) Get() (defaults GlobalSettings, overrides GlobalSettings, values GlobalSettings) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneGlobal(s.defaults), cloneGlobal(s.overrides), cloneGlobal(s.values)
}

// Update merges a partial patch into the persisted overrides, validates the
// resulting values, persists the overrides to bbolt and swaps the in-memory
// snapshot. Validation failures return *ValidationError.
func (s *Service) Update(patch GlobalSettingsPatch) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	merged := mergePatch(s.patch, patch)
	candidate := applyPatch(s.defaults, merged)
	if err := validateGlobal(candidate); err != nil {
		return err
	}

	if s.bucket != nil {
		if err := s.persistLocked(merged); err != nil {
			return fmt.Errorf("settings：保存设置失败：%w", err)
		}
	}
	s.patch = merged
	s.recomputeLocked()
	return nil
}

func (s *Service) persistLocked(patch GlobalSettingsPatch) error {
	if isZeroPatch(patch) {
		return s.bucket.Delete([]byte(settingsGlobalKey))
	}
	data, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	return s.bucket.Put([]byte(settingsGlobalKey), data)
}

// Reset clears the persisted overrides so all values fall back to env defaults.
func (s *Service) Reset() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bucket != nil {
		if err := s.bucket.Delete([]byte(settingsGlobalKey)); err != nil {
			return fmt.Errorf("settings：重置设置失败：%w", err)
		}
	}
	s.patch = GlobalSettingsPatch{}
	s.recomputeLocked()
	return nil
}

// Resolve returns the immutable Effective snapshot for a room: merged global
// values with non-nil room overrides applied. roomID <= 0 or an unsubscribed
// room resolves with global settings only.
func (s *Service) Resolve(roomID int) Effective {
	values := s.currentValues()
	eff := effectiveFromGlobal(values)
	if s.roomCfgs != nil && roomID > 0 {
		if roomCfg, err := s.roomCfgs.GetConfig(roomID); err == nil && roomCfg != nil {
			applyRoomOverrides(&eff, roomCfg)
		}
	}
	return eff
}

// ValidationError is returned for invalid settings and maps to HTTP 400.
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

// IsValidationError reports whether err is a settings validation error.
func IsValidationError(err error) bool {
	var v *ValidationError
	return errors.As(err, &v)
}

func validationErrorf(format string, args ...any) *ValidationError {
	return &ValidationError{Message: fmt.Sprintf(format, args...)}
}

// env helpers -------------------------------------------------------------

func envRaw(key string) (string, bool) {
	raw, ok := os.LookupEnv(key)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(raw), true
}

func envString(key, def string, valid func(string) bool) string {
	raw, ok := envRaw(key)
	if !ok || raw == "" {
		return def
	}
	if valid != nil && !valid(raw) {
		log.Warnf("环境变量 %s 无效（%q），使用默认值 %q", key, raw, def)
		return def
	}
	return raw
}

func envInt(key string, def, min, max int) int {
	raw, ok := envRaw(key)
	if !ok || raw == "" {
		return def
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < min || value > max {
		log.Warnf("环境变量 %s 无效（%q），使用默认值 %d", key, raw, def)
		return def
	}
	return value
}

func envBool(key string, def bool) bool {
	raw, ok := envRaw(key)
	if !ok || raw == "" {
		return def
	}
	switch strings.ToLower(raw) {
	case "true", "1":
		return true
	case "false", "0":
		return false
	default:
		log.Warnf("环境变量 %s 无效（%q），使用默认值 %t", key, raw, def)
		return def
	}
}

func envCSV(key string, def []string, valid func([]string) bool) []string {
	raw, ok := envRaw(key)
	if !ok || raw == "" {
		return cloneStrings(def)
	}
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		values = append(values, part)
	}
	if valid != nil && !valid(values) {
		log.Warnf("环境变量 %s 无效（%q），使用默认值 %v", key, raw, def)
		return cloneStrings(def)
	}
	return values
}

var envVars = []string{
	"SEGMENT_MODE", "SEGMENT_MINUTES", "SEGMENT_SIZE_MB", "SEGMENT_BY_TITLE",
	"MIN_FILE_SIZE_MB", "FILENAME_TEMPLATE", "TITLE_FILTER_PATTERNS", "SAVE_STREAM_COVER",
	"DANMAKU_RECORD_TYPES", "DANMAKU_TRANSPORT", "FLV_WRITE_METADATA",
	"FLV_SPLIT_ON_SCRIPT_TAG", "FLV_DISABLE_SPLIT_ON_ANNEXB", "BILI_LIVE_API_HOST",
	"BILI_LIVE_API_FALLBACK_HOST", "BILI_CDN", "BILI_CDN_FALLBACK", "BILI_FORCE_SOURCE",
	"BILI_REPLACE_CN01",
}
