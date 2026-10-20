package subscribe

import "testing"

// legacyRoomConfig mirrors the pre-v2 persisted RoomConfig shape (gob field
// names are the exported Go names).
type legacyRoomConfig struct {
	AutoRecord            bool
	Notify                bool
	RecordDurationMinutes int
	Qn                    int
	OnlyAudio             bool
	RecordDanmaku         bool
	StreamProfiles        []string
	DeleteOldestOnLowDisk bool
}

func TestParseRoomConfigLegacyRecord(t *testing.T) {
	legacy := &legacyRoomConfig{
		AutoRecord:            true,
		Notify:                true,
		RecordDurationMinutes: 30,
		Qn:                    10000,
		OnlyAudio:             true,
		RecordDanmaku:         true,
		StreamProfiles:        []string{"http-flv"},
		DeleteOldestOnLowDisk: true,
	}
	data, err := roomConfigSerializer.Serialize(legacy)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := parseRoomConfig(data)
	if err != nil {
		t.Fatalf("legacy record must decode: %v", err)
	}
	if !cfg.AutoRecord || !cfg.Notify || cfg.RecordDurationMinutes != 30 || cfg.Qn != 10000 ||
		!cfg.OnlyAudio || !cfg.RecordDanmaku || !cfg.DeleteOldestOnLowDisk {
		t.Fatalf("legacy fields lost: %+v", cfg)
	}
	if len(cfg.StreamProfiles) != 1 || cfg.StreamProfiles[0] != "http-flv" {
		t.Fatalf("legacy stream profiles lost: %+v", cfg.StreamProfiles)
	}

	// No migration: all v2 override fields decode as nil (= inherit global).
	if cfg.SegmentMode != nil || cfg.SegmentMinutes != nil || cfg.SegmentSizeMB != nil ||
		cfg.SegmentByTitle != nil || cfg.MinFileSizeMB != nil || cfg.FilenameTemplate != nil ||
		cfg.TitleFilterPatterns != nil || cfg.SaveStreamCover != nil || cfg.DanmakuTypes != nil ||
		cfg.DanmakuTransport != nil || cfg.FlvWriteMetadata != nil || cfg.FlvSplitOnScriptTag != nil ||
		cfg.FlvDisableSplitOnAnnexB != nil || cfg.BiliLiveAPIHost != nil ||
		cfg.BiliLiveAPIFallbackHost != nil || cfg.BiliCDN != nil || cfg.BiliCDNFallback != nil ||
		cfg.BiliForceSource != nil || cfg.BiliReplaceCN01 != nil {
		t.Fatalf("v2 override fields must be nil for legacy records: %+v", cfg)
	}
}

func TestRoomConfigOverrideRoundTrip(t *testing.T) {
	mode := "time"
	minutes := 45
	transport := "wss"
	danmakuTypes := DanmakuTypes{RawEvents: true, SuperChat: true}
	cdn := []string{"cn-gotcha01"}
	cfg := &RoomConfig{
		SegmentMode:      &mode,
		SegmentMinutes:   &minutes,
		DanmakuTransport: &transport,
		DanmakuTypes:     &danmakuTypes,
		BiliCDN:          &cdn,
	}
	data, err := roomConfigSerializer.Serialize(cfg)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := parseRoomConfig(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.SegmentMode == nil || *decoded.SegmentMode != "time" {
		t.Fatalf("segment mode lost: %+v", decoded.SegmentMode)
	}
	if decoded.SegmentMinutes == nil || *decoded.SegmentMinutes != 45 {
		t.Fatalf("segment minutes lost: %+v", decoded.SegmentMinutes)
	}
	if decoded.DanmakuTypes == nil || !decoded.DanmakuTypes.RawEvents {
		t.Fatalf("danmaku types lost: %+v", decoded.DanmakuTypes)
	}
	if decoded.BiliCDN == nil || len(*decoded.BiliCDN) != 1 || (*decoded.BiliCDN)[0] != "cn-gotcha01" {
		t.Fatalf("cdn lost: %+v", decoded.BiliCDN)
	}
	if decoded.FilenameTemplate != nil || decoded.BiliForceSource != nil {
		t.Fatalf("unset overrides must stay nil: %+v", decoded)
	}
}

func TestCloneRoomConfigDeepCopiesOverrides(t *testing.T) {
	mode := "time"
	cdn := []string{"a.example.com"}
	danmakuTypes := DanmakuTypes{Gift: true}
	original := &RoomConfig{
		SegmentMode:  &mode,
		BiliCDN:      &cdn,
		DanmakuTypes: &danmakuTypes,
	}
	clone := cloneRoomConfig(original)

	*clone.SegmentMode = "size"
	(*clone.BiliCDN)[0] = "b.example.com"
	clone.DanmakuTypes.Gift = false

	if *original.SegmentMode != "time" {
		t.Fatal("SegmentMode pointer aliased")
	}
	if (*original.BiliCDN)[0] != "a.example.com" {
		t.Fatal("BiliCDN slice aliased")
	}
	if !original.DanmakuTypes.Gift {
		t.Fatal("DanmakuTypes aliased")
	}
}
