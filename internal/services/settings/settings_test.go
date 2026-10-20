package settings

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/services/subscribe"
	"go.uber.org/fx/fxtest"
)

// clearSettingsEnv empties every settings env var so tests are hermetic.
func clearSettingsEnv(t *testing.T) {
	t.Helper()
	for _, key := range envVars {
		t.Setenv(key, "")
	}
}

func TestBuiltinDefaultsReproduceCurrentBehavior(t *testing.T) {
	clearSettingsEnv(t)
	got := loadDefaultsFromEnv()
	want := builtinDefaults()

	if got.SegmentMode != "off" || got.SegmentMinutes != 120 || got.SegmentSizeMB != 2048 {
		t.Fatalf("unexpected segmentation defaults: %+v", got)
	}
	if got.SegmentByTitle || got.MinFileSizeMB != 0 || got.FilenameTemplate != "" ||
		got.TitleFilterPatterns != "" || got.SaveStreamCover {
		t.Fatalf("unexpected naming/filter/cover defaults: %+v", got)
	}
	if got.DanmakuTypes != (DanmakuTypes{SuperChat: true, Gift: true, Guard: true}) {
		t.Fatalf("unexpected danmaku type defaults: %+v", got.DanmakuTypes)
	}
	if got.DanmakuTransport != "auto" {
		t.Fatalf("danmaku transport default = %q", got.DanmakuTransport)
	}
	if !got.FlvWriteMetadata || got.FlvSplitOnScriptTag || !got.FlvDisableSplitOnAnnexB {
		t.Fatalf("unexpected FLV defaults: %+v", got)
	}
	if got.BiliLiveAPIHost != "https://api.live.bilibili.com" || got.BiliLiveAPIFallbackHost != "" {
		t.Fatalf("unexpected bilibili host defaults: %+v", got)
	}
	if len(got.BiliCDN) != 0 || len(got.BiliReplaceCN01) != 0 || got.BiliCDNFallback || got.BiliForceSource {
		t.Fatalf("unexpected bilibili CDN defaults: %+v", got)
	}
	if got.BiliCDN == nil || got.BiliReplaceCN01 == nil {
		t.Fatalf("cdn slices should be empty non-nil slices: %#v %#v", got.BiliCDN, got.BiliReplaceCN01)
	}
	if !globalEqual(got, want) {
		t.Fatalf("defaults mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestEnvDefaultsParsing(t *testing.T) {
	clearSettingsEnv(t)
	t.Setenv("SEGMENT_MODE", "time")
	t.Setenv("SEGMENT_MINUTES", "45")
	t.Setenv("SEGMENT_SIZE_MB", "512")
	t.Setenv("SEGMENT_BY_TITLE", "true")
	t.Setenv("MIN_FILE_SIZE_MB", "10")
	t.Setenv("FILENAME_TEMPLATE", "{uname}-{roomid}/{title}-{date}")
	t.Setenv("TITLE_FILTER_PATTERNS", "^广告\n^推广")
	t.Setenv("SAVE_STREAM_COVER", "true")
	t.Setenv("DANMAKU_RECORD_TYPES", "raw_events,super_chat")
	t.Setenv("DANMAKU_TRANSPORT", "wss")
	t.Setenv("FLV_WRITE_METADATA", "false")
	t.Setenv("FLV_SPLIT_ON_SCRIPT_TAG", "true")
	t.Setenv("FLV_DISABLE_SPLIT_ON_ANNEXB", "false")
	t.Setenv("BILI_LIVE_API_HOST", "https://live.example.com")
	t.Setenv("BILI_LIVE_API_FALLBACK_HOST", "https://fallback.example.com")
	t.Setenv("BILI_CDN", "cn-gotcha01,cn-gotcha02")
	t.Setenv("BILI_CDN_FALLBACK", "true")
	t.Setenv("BILI_FORCE_SOURCE", "true")
	t.Setenv("BILI_REPLACE_CN01", "a.example.com,b.example.com")

	got := loadDefaultsFromEnv()
	if got.SegmentMode != "time" || got.SegmentMinutes != 45 || got.SegmentSizeMB != 512 || !got.SegmentByTitle {
		t.Fatalf("segmentation env not applied: %+v", got)
	}
	if got.MinFileSizeMB != 10 || got.FilenameTemplate != "{uname}-{roomid}/{title}-{date}" || got.TitleFilterPatterns != "^广告\n^推广" || !got.SaveStreamCover {
		t.Fatalf("naming env not applied: %+v", got)
	}
	if !got.DanmakuTypes.RawEvents || !got.DanmakuTypes.SuperChat || got.DanmakuTypes.Gift || got.DanmakuTypes.Guard {
		t.Fatalf("danmaku types env not applied: %+v", got.DanmakuTypes)
	}
	if got.DanmakuTransport != "wss" || got.FlvWriteMetadata || !got.FlvSplitOnScriptTag || got.FlvDisableSplitOnAnnexB {
		t.Fatalf("danmaku/flv env not applied: %+v", got)
	}
	if got.BiliLiveAPIHost != "https://live.example.com" || got.BiliLiveAPIFallbackHost != "https://fallback.example.com" {
		t.Fatalf("api hosts env not applied: %+v", got)
	}
	if len(got.BiliCDN) != 2 || got.BiliCDN[0] != "cn-gotcha01" || !got.BiliCDNFallback || !got.BiliForceSource {
		t.Fatalf("cdn env not applied: %+v", got)
	}
	if len(got.BiliReplaceCN01) != 2 || got.BiliReplaceCN01[1] != "b.example.com" {
		t.Fatalf("replace env not applied: %+v", got.BiliReplaceCN01)
	}
}

func TestInvalidEnvFallsBackToBuiltinDefaults(t *testing.T) {
	clearSettingsEnv(t)
	t.Setenv("SEGMENT_MODE", "bogus")
	t.Setenv("SEGMENT_MINUTES", "0")
	t.Setenv("SEGMENT_SIZE_MB", "999999")
	t.Setenv("MIN_FILE_SIZE_MB", "-1")
	t.Setenv("TITLE_FILTER_PATTERNS", "[")
	t.Setenv("DANMAKU_RECORD_TYPES", "raw_events,unknown")
	t.Setenv("DANMAKU_TRANSPORT", "tcp")
	t.Setenv("SEGMENT_BY_TITLE", "yes")
	t.Setenv("BILI_LIVE_API_HOST", "not-a-url")
	t.Setenv("BILI_CDN", "bad_host!")
	t.Setenv("FILENAME_TEMPLATE", "../escape/{title}")
	t.Setenv("BILI_REPLACE_CN01", "a,b,c,d,e,f,g,h,i")

	got := loadDefaultsFromEnv()
	want := builtinDefaults()
	if !globalEqual(got, want) {
		t.Fatalf("invalid env must fall back to builtin defaults:\n got %+v\nwant %+v", got, want)
	}
}

func TestValidateGlobalMatrix(t *testing.T) {
	valid := builtinDefaults()
	if err := validateGlobal(valid); err != nil {
		t.Fatalf("builtin defaults must validate: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*GlobalSettings)
	}{
		{"bad segment mode", func(g *GlobalSettings) { g.SegmentMode = "bogus" }},
		{"time minutes low", func(g *GlobalSettings) { g.SegmentMode = "time"; g.SegmentMinutes = 0 }},
		{"time minutes high", func(g *GlobalSettings) { g.SegmentMode = "time"; g.SegmentMinutes = 1441 }},
		{"size mb low", func(g *GlobalSettings) { g.SegmentMode = "size"; g.SegmentSizeMB = 15 }},
		{"size mb high", func(g *GlobalSettings) { g.SegmentMode = "size"; g.SegmentSizeMB = 65537 }},
		{"min file size low", func(g *GlobalSettings) { g.MinFileSizeMB = -1 }},
		{"min file size high", func(g *GlobalSettings) { g.MinFileSizeMB = 1025 }},
		{"filename too long", func(g *GlobalSettings) { g.FilenameTemplate = string(make([]byte, 257)) }},
		{"filename absolute", func(g *GlobalSettings) { g.FilenameTemplate = "/abs/{title}" }},
		{"filename dotdot", func(g *GlobalSettings) { g.FilenameTemplate = "a/../{title}" }},
		{"filename backslash", func(g *GlobalSettings) { g.FilenameTemplate = `a\b` }},
		{"filename empty segment", func(g *GlobalSettings) { g.FilenameTemplate = "a//b" }},
		{"filename trailing slash", func(g *GlobalSettings) { g.FilenameTemplate = "a/b/" }},
		{"filename control char", func(g *GlobalSettings) { g.FilenameTemplate = "a\x01b" }},
		{"filename segment too long", func(g *GlobalSettings) {
			g.FilenameTemplate = strings.Repeat("a", 201)
		}},
		{"patterns too long", func(g *GlobalSettings) { g.TitleFilterPatterns = string(make([]byte, 4097)) }},
		{"patterns invalid regex", func(g *GlobalSettings) { g.TitleFilterPatterns = "valid\n[" }},
		{"bad transport", func(g *GlobalSettings) { g.DanmakuTransport = "tcp" }},
		{"bad api host", func(g *GlobalSettings) { g.BiliLiveAPIHost = "ftp://example.com" }},
		{"bad fallback host", func(g *GlobalSettings) { g.BiliLiveAPIFallbackHost = "/relative" }},
		{"too many cdn", func(g *GlobalSettings) {
			g.BiliCDN = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}
		}},
		{"bad cdn entry", func(g *GlobalSettings) { g.BiliCDN = []string{"bad_host!"} }},
		{"bad replace entry", func(g *GlobalSettings) { g.BiliReplaceCN01 = []string{"with space"} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := valid
			tt.mutate(&got)
			err := validateGlobal(got)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !IsValidationError(err) {
				t.Fatalf("expected *ValidationError, got %T: %v", err, err)
			}
		})
	}

	// Path templates with "/" are valid (subdirectories under OUTPUT_DIR).
	subdir := valid
	subdir.FilenameTemplate = "{uname}-{roomid}/{year}/{month}/{title}"
	if err := validateGlobal(subdir); err != nil {
		t.Fatalf("valid path template rejected: %v", err)
	}

	// Mode-specific ranges: minutes outside [1,1440] are fine when mode != time.
	off := valid
	off.SegmentMode = "off"
	off.SegmentMinutes = 0
	if err := validateGlobal(off); err != nil {
		t.Fatalf("minutes are only constrained in time mode: %v", err)
	}
	size := valid
	size.SegmentMode = "size"
	size.SegmentSizeMB = 2048
	if err := validateGlobal(size); err != nil {
		t.Fatalf("valid size mode rejected: %v", err)
	}
	timeMode := valid
	timeMode.SegmentMode = "time"
	timeMode.SegmentMinutes = 120
	if err := validateGlobal(timeMode); err != nil {
		t.Fatalf("valid time mode rejected: %v", err)
	}
}

func newTestSettingsService(t *testing.T, roomCfgs roomConfigSource) *Service {
	t.Helper()
	lc := fxtest.NewLifecycle(t)
	cfg := &config.Config{DatabaseDir: t.TempDir()}
	svc := newService(lc, cfg, roomCfgs)
	lc.RequireStart()
	t.Cleanup(lc.RequireStop)
	return svc
}

func TestPersistenceAndReset(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{DatabaseDir: dir}

	openSvc := func() (*Service, func()) {
		lc := fxtest.NewLifecycle(t)
		svc := newService(lc, cfg, nil)
		lc.RequireStart()
		stopped := false
		stop := func() {
			if stopped {
				return
			}
			stopped = true
			lc.RequireStop()
		}
		t.Cleanup(stop)
		return svc, stop
	}

	svc, stop := openSvc()
	segTime := "time"
	minutes := 30
	transport := "wss"
	template := "{title}-{year}"
	if err := svc.Update(GlobalSettingsPatch{
		SegmentMode:      &segTime,
		SegmentMinutes:   &minutes,
		DanmakuTransport: &transport,
		FilenameTemplate: &template,
	}); err != nil {
		t.Fatal(err)
	}

	defaults, overrides, values := svc.Get()
	if values.SegmentMode != "time" || values.SegmentMinutes != 30 || values.DanmakuTransport != "wss" {
		t.Fatalf("values not updated: %+v", values)
	}
	if overrides.SegmentMode != "time" || overrides.FilenameTemplate != template {
		t.Fatalf("overrides not reported: %+v", overrides)
	}
	if defaults.SegmentMode != "off" {
		t.Fatalf("defaults must stay immutable: %+v", defaults)
	}
	// Release the bbolt file lock before a second service opens the same DB.
	stop()

	// A second process/service loads the persisted overrides.
	reloaded, stopReloaded := openSvc()
	_, reloadedOverrides, reloadedValues := reloaded.Get()
	if reloadedValues.SegmentMode != "time" || reloadedValues.SegmentMinutes != 30 {
		t.Fatalf("persisted values not reloaded: %+v", reloadedValues)
	}
	if reloadedOverrides.FilenameTemplate != template || reloadedOverrides.DanmakuTransport != "wss" {
		t.Fatalf("persisted overrides not reloaded: %+v", reloadedOverrides)
	}

	if err := reloaded.Reset(); err != nil {
		t.Fatal(err)
	}
	_, overridesAfterReset, valuesAfterReset := reloaded.Get()
	if !globalEqual(valuesAfterReset, defaults) {
		t.Fatalf("reset must restore defaults:\n got %+v\nwant %+v", valuesAfterReset, defaults)
	}
	if !globalEqual(overridesAfterReset, normalizeGlobal(GlobalSettings{})) {
		t.Fatalf("reset must clear overrides: %+v", overridesAfterReset)
	}
	stopReloaded()

	afterReset, stopAfterReset := openSvc()
	defer stopAfterReset()
	_, persistedOverrides, persistedValues := afterReset.Get()
	if !globalEqual(persistedValues, defaults) || !globalEqual(persistedOverrides, normalizeGlobal(GlobalSettings{})) {
		t.Fatalf("reset must clear the DB: overrides=%+v values=%+v", persistedOverrides, persistedValues)
	}
}

func TestPartialUpdateMergesOverrides(t *testing.T) {
	svc := newTestSettingsService(t, nil)

	template := "abc"
	if err := svc.Update(GlobalSettingsPatch{FilenameTemplate: &template}); err != nil {
		t.Fatal(err)
	}
	minSize := 5
	if err := svc.Update(GlobalSettingsPatch{MinFileSizeMB: &minSize}); err != nil {
		t.Fatal(err)
	}
	_, overrides, values := svc.Get()
	if overrides.FilenameTemplate != "abc" || values.MinFileSizeMB != 5 {
		t.Fatalf("partial updates must merge: overrides=%+v values=%+v", overrides, values)
	}
}

func TestUpdateValidationRejectsAndKeepsPreviousValues(t *testing.T) {
	svc := newTestSettingsService(t, nil)
	mode := "bogus"
	err := svc.Update(GlobalSettingsPatch{SegmentMode: &mode})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !IsValidationError(err) {
		t.Fatalf("expected *ValidationError, got %v", err)
	}
	_, overrides, values := svc.Get()
	if !globalEqual(overrides, normalizeGlobal(GlobalSettings{})) {
		t.Fatalf("invalid update must not persist overrides: %+v", overrides)
	}
	if values.SegmentMode != "off" {
		t.Fatalf("invalid update changed values: %+v", values)
	}
}

type fakeRoomConfigs struct {
	configs map[int]*subscribe.RoomConfig
	err     error
}

func (f fakeRoomConfigs) GetConfig(roomID int) (*subscribe.RoomConfig, error) {
	if f.err != nil {
		return nil, f.err
	}
	cfg, ok := f.configs[roomID]
	if !ok {
		return nil, subscribe.ErrRoomNotSubscribed
	}
	return cfg, nil
}

func TestResolveInheritance(t *testing.T) {
	mode := "size"
	sizeMB := 64
	minSize := 3
	transport := "ws"
	danmakuTypes := subscribe.DanmakuTypes{RawEvents: true, SuperChat: true}
	cdn := []string{"room-cdn"}
	rooms := fakeRoomConfigs{configs: map[int]*subscribe.RoomConfig{
		42: {
			SegmentMode:      &mode,
			SegmentSizeMB:    &sizeMB,
			MinFileSizeMB:    &minSize,
			DanmakuTransport: &transport,
			DanmakuTypes:     &danmakuTypes,
			BiliCDN:          &cdn,
		},
	}}
	svc := newTestSettingsService(t, rooms)

	// Global override applies everywhere.
	globalSize := 128
	globalMode := "size"
	if err := svc.Update(GlobalSettingsPatch{SegmentMode: &globalMode, SegmentSizeMB: &globalSize}); err != nil {
		t.Fatal(err)
	}

	// Unsubscribed room / roomID 0: global only.
	for _, roomID := range []int{0, -1, 999} {
		eff := svc.Resolve(roomID)
		if eff.SegmentMode != "size" || eff.SegmentSizeMB != 128 {
			t.Fatalf("room %d should inherit global values: %+v", roomID, eff)
		}
		if eff.BiliCDNFallback || len(eff.BiliCDN) != 0 {
			t.Fatalf("room %d should inherit global cdn: %+v", roomID, eff)
		}
	}

	// Room override wins for non-nil fields, nil fields inherit.
	eff := svc.Resolve(42)
	if eff.SegmentMode != "size" || eff.SegmentSizeMB != 64 {
		t.Fatalf("room override not applied: %+v", eff)
	}
	if eff.MinFileSizeMB != 3 || eff.DanmakuTransport != "ws" {
		t.Fatalf("room pointer overrides not applied: %+v", eff)
	}
	if !eff.DanmakuTypes.RawEvents || !eff.DanmakuTypes.SuperChat || eff.DanmakuTypes.Gift {
		t.Fatalf("room danmaku types not applied: %+v", eff.DanmakuTypes)
	}
	if len(eff.BiliCDN) != 1 || eff.BiliCDN[0] != "room-cdn" {
		t.Fatalf("room cdn not applied: %+v", eff.BiliCDN)
	}
	// Inherited from global defaults.
	if eff.FlvWriteMetadata != true || eff.DanmakuTransport == "auto" {
		t.Fatalf("unexpected inherited values: %+v", eff)
	}
	if eff.BiliLiveAPIHost != "https://api.live.bilibili.com" {
		t.Fatalf("inherited api host: %+v", eff)
	}

	// Mutating the returned snapshot must not affect later resolutions.
	eff.BiliCDN[0] = "mutated"
	eff2 := svc.Resolve(42)
	if eff2.BiliCDN[0] != "room-cdn" {
		t.Fatalf("room cdn snapshot aliased: %+v", eff2.BiliCDN)
	}
}

func TestResolveIgnoresRoomConfigErrors(t *testing.T) {
	svc := newTestSettingsService(t, fakeRoomConfigs{err: errors.New("boom")})
	eff := svc.Resolve(42)
	if eff.SegmentMode != "off" || eff.BiliLiveAPIHost != "https://api.live.bilibili.com" {
		t.Fatalf("errors must fall back to global values: %+v", eff)
	}
}

func TestResolveDefaultsMatchBuiltinSnapshot(t *testing.T) {
	clearSettingsEnv(t)
	svc := newTestSettingsService(t, nil)
	got := svc.Resolve(-1)
	want := effectiveFromGlobal(builtinDefaults())
	if got.SegmentMode != want.SegmentMode || got.SegmentMinutes != want.SegmentMinutes ||
		got.DanmakuTypes != want.DanmakuTypes || got.FlvWriteMetadata != want.FlvWriteMetadata ||
		got.BiliLiveAPIHost != want.BiliLiveAPIHost {
		t.Fatalf("default effective snapshot changed:\n got %+v\nwant %+v", got, want)
	}
}

func globalEqual(a, b GlobalSettings) bool {
	return reflect.DeepEqual(normalizeGlobal(a), normalizeGlobal(b))
}

func TestServiceWithoutDatabaseStillUpdatesInMemory(t *testing.T) {
	// newService without lifecycle start has no bucket; Update must still apply
	// in memory (used by unit tests and as a safe fallback).
	lc := fxtest.NewLifecycle(t)
	svc := newService(lc, &config.Config{DatabaseDir: filepath.Join(t.TempDir(), "db")}, nil)
	minutes := 90
	if err := svc.Update(GlobalSettingsPatch{SegmentMinutes: &minutes}); err != nil {
		t.Fatal(err)
	}
	if _, _, values := svc.Get(); values.SegmentMinutes != 90 {
		t.Fatalf("in-memory update not applied: %+v", values)
	}
}

func TestTitleMatches(t *testing.T) {
	if !TitleMatches("^广告\n^推广", "广告位招租") {
		t.Fatal("expected first pattern to match")
	}
	if !TitleMatches("^广告\n^推广", "推广活动") {
		t.Fatal("expected second pattern to match")
	}
	if TitleMatches("^广告", "正常直播") {
		t.Fatal("expected no match")
	}
	if TitleMatches("", "广告") {
		t.Fatal("empty patterns must never match")
	}
	// Invalid lines are skipped defensively rather than panicking.
	if TitleMatches("[", "anything") {
		t.Fatal("invalid patterns must not match")
	}
}

func TestValidateRoomOverrides(t *testing.T) {
	clearSettingsEnv(t)
	svc := newTestSettingsService(t, nil)

	if err := svc.ValidateRoomOverrides(nil); err != nil {
		t.Fatalf("nil config must be valid: %v", err)
	}

	mode := "time"
	minutes := 60
	transport := "ws"
	patterns := "^广告"
	cdn := []string{"cn-gotcha01"}
	valid := &subscribe.RoomConfig{
		SegmentMode:         &mode,
		SegmentMinutes:      &minutes,
		DanmakuTransport:    &transport,
		TitleFilterPatterns: &patterns,
		BiliCDN:             &cdn,
	}
	if err := svc.ValidateRoomOverrides(valid); err != nil {
		t.Fatalf("valid room overrides rejected: %v", err)
	}

	badMode := "bogus"
	badModeCfg := &subscribe.RoomConfig{SegmentMode: &badMode}
	err := svc.ValidateRoomOverrides(badModeCfg)
	if err == nil || !IsValidationError(err) {
		t.Fatalf("invalid segment mode must be a validation error, got %v", err)
	}

	// A room that overrides mode=time must also satisfy the minutes range
	// (defaults supply 120, an explicit 0 is invalid).
	zero := 0
	badMinutes := &subscribe.RoomConfig{SegmentMode: &mode, SegmentMinutes: &zero}
	if err := svc.ValidateRoomOverrides(badMinutes); err == nil || !IsValidationError(err) {
		t.Fatalf("invalid minutes must be a validation error, got %v", err)
	}

	badPattern := "["
	badPatternCfg := &subscribe.RoomConfig{TitleFilterPatterns: &badPattern}
	if err := svc.ValidateRoomOverrides(badPatternCfg); err == nil || !IsValidationError(err) {
		t.Fatalf("invalid regex must be a validation error, got %v", err)
	}

	badCDN := []string{"bad_host!"}
	badCDNCfg := &subscribe.RoomConfig{BiliCDN: &badCDN}
	if err := svc.ValidateRoomOverrides(badCDNCfg); err == nil || !IsValidationError(err) {
		t.Fatalf("invalid cdn must be a validation error, got %v", err)
	}

	badTemplate := "../escape/{title}"
	badTemplateCfg := &subscribe.RoomConfig{FilenameTemplate: &badTemplate}
	if err := svc.ValidateRoomOverrides(badTemplateCfg); err == nil || !IsValidationError(err) {
		t.Fatalf("invalid room filename template must be a validation error, got %v", err)
	}

	badTransport := "tcp"
	badTransportCfg := &subscribe.RoomConfig{DanmakuTransport: &badTransport}
	if err := svc.ValidateRoomOverrides(badTransportCfg); err == nil || !IsValidationError(err) {
		t.Fatalf("tcp transport must be a validation error, got %v", err)
	}
}
