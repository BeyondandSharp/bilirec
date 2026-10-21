// SPDX-License-Identifier: GPL-3.0-only

package subcheck

import (
	"testing"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/config"
	settingssvc "github.com/bilirec/bilirec/internal/services/settings"
	"go.uber.org/fx/fxtest"
)

func TestSkipByTitleFilter(t *testing.T) {
	lc := fxtest.NewLifecycle(t)
	settingsSvc := settingssvc.NewService(lc, &config.Config{DatabaseDir: t.TempDir()}, nil, nil)
	lc.RequireStart()
	t.Cleanup(lc.RequireStop)

	patterns := "^广告\n^推广"
	if err := settingsSvc.Update(settingssvc.GlobalSettingsPatch{TitleFilterPatterns: &patterns}); err != nil {
		t.Fatal(err)
	}
	s := &Service{settingsSvc: settingsSvc}

	if !s.skipByTitleFilter(1, &bilibili.LiveRoomInfoDetail{Title: "广告位招租"}) {
		t.Fatal("expected title filter to match")
	}
	if !s.skipByTitleFilter(1, &bilibili.LiveRoomInfoDetail{Title: "推广中"}) {
		t.Fatal("expected second pattern to match")
	}
	if s.skipByTitleFilter(1, &bilibili.LiveRoomInfoDetail{Title: "正常直播"}) {
		t.Fatal("non-matching title must not be skipped")
	}
	if s.skipByTitleFilter(1, &bilibili.LiveRoomInfoDetail{Title: ""}) {
		t.Fatal("empty title must not be skipped")
	}
	if s.skipByTitleFilter(1, nil) {
		t.Fatal("nil room info must not be skipped")
	}
	if (&Service{}).skipByTitleFilter(1, &bilibili.LiveRoomInfoDetail{Title: "广告"}) {
		t.Fatal("missing settings service must not skip")
	}
}

func TestSkipByTitleFilterDisabled(t *testing.T) {
	lc := fxtest.NewLifecycle(t)
	settingsSvc := settingssvc.NewService(lc, &config.Config{DatabaseDir: t.TempDir()}, nil, nil)
	lc.RequireStart()
	t.Cleanup(lc.RequireStop)

	s := &Service{settingsSvc: settingsSvc}
	if s.skipByTitleFilter(1, &bilibili.LiveRoomInfoDetail{Title: "广告位招租"}) {
		t.Fatal("no patterns configured must not skip")
	}
}
