package recorder

import (
	"testing"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/config"
	settingssvc "github.com/bilirec/bilirec/internal/services/settings"
	"go.uber.org/fx/fxtest"
)

func TestSessionEffectiveSnapshot(t *testing.T) {
	lc := fxtest.NewLifecycle(t)
	settingsSvc := settingssvc.NewService(lc, &config.Config{DatabaseDir: t.TempDir()}, nil, nil)
	lc.RequireStart()
	t.Cleanup(lc.RequireStop)

	r := &Service{settings: settingsSvc}

	// New sessions resolve the current values; defaults keep v2 features off.
	userEff := r.sessionEffective(internalStartParams{roomId: 42, mode: startModeUser})
	if userEff.SegmentMode != "off" || !userEff.FlvWriteMetadata || !userEff.FlvDisableSplitOnAnnexB {
		t.Fatalf("unexpected default effective snapshot: %+v", userEff)
	}

	// A settings change applies to the next new session only.
	mode := "time"
	minutes := 30
	patterns := "^广告"
	if err := settingsSvc.Update(settingssvc.GlobalSettingsPatch{
		SegmentMode: &mode, SegmentMinutes: &minutes, TitleFilterPatterns: &patterns,
	}); err != nil {
		t.Fatal(err)
	}
	nextEff := r.sessionEffective(internalStartParams{roomId: 42, mode: startModeUser})
	if nextEff.SegmentMode != "time" || nextEff.SegmentMinutes != 30 || nextEff.TitleFilterPatterns != "^广告" {
		t.Fatalf("new session must resolve updated settings: %+v", nextEff)
	}

	// Recovery reuses the original session snapshot.
	session := &Info{effective: userEff}
	recovered := r.sessionEffective(internalStartParams{roomId: 42, mode: startModeRecovery, session: session})
	if recovered.SegmentMode != "off" {
		t.Fatalf("recovery must keep the original snapshot: %+v", recovered)
	}

	// Nil settings service falls back to the zero snapshot (unit tests).
	if eff := (&Service{}).sessionEffective(internalStartParams{roomId: 42}); eff.SegmentMode != "" {
		t.Fatalf("nil settings must return zero snapshot: %+v", eff)
	}
}

func TestStreamFetchOptionsMapping(t *testing.T) {
	base := []bilibili.GetStreamURLsOption{bilibili.WithQn(bilibili.QualityOriginal)}
	eff := settingssvc.Effective{
		BiliLiveAPIHost:         "https://api.example.com",
		BiliLiveAPIFallbackHost: "https://fb.example.com",
		BiliCDN:                 []string{"cdn1"},
		BiliCDNFallback:         true,
		BiliForceSource:         true,
		BiliReplaceCN01:         []string{"host1.example.com"},
	}
	fetch := streamFetchOptions(eff)
	if len(fetch) != 1 {
		t.Fatalf("expected one fetch option, got %d", len(fetch))
	}
	combined := appendStreamOptions(base, fetch)
	if len(combined) != 2 {
		t.Fatalf("combined options = %d, want 2", len(combined))
	}
	// The base slice must not be mutated (start options are reused per attempt).
	if len(base) != 1 || cap(base) != 1 {
		t.Fatalf("appendStreamOptions mutated its input: len=%d cap=%d", len(base), cap(base))
	}
}
