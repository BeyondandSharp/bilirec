// SPDX-License-Identifier: GPL-3.0-only

package recorder

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/modules/metrics"
	rs "github.com/bilirec/bilirec/internal/record_strategies"
	"github.com/bilirec/bilirec/internal/services/cover"
	"github.com/bilirec/bilirec/internal/services/danmaku"
	"github.com/bilirec/bilirec/internal/services/settings"
	"github.com/bilirec/bilirec/pkg/ds"
	"github.com/bilirec/bilirec/pkg/flv"
	"github.com/bilirec/bilirec/pkg/pipeline"
	"github.com/puzpuzpuz/xsync/v4"
)

func TestInfoSegmentSizeTrigger(t *testing.T) {
	info := &Info{effective: settings.Effective{SegmentMode: "size", SegmentSizeMB: 1}}
	info.resetSegmentRotation(time.Now())

	info.addSegmentBytes(600 * 1024)
	if info.RotationPending() {
		t.Fatal("rotation must not trigger before the size threshold")
	}
	info.addSegmentBytes(600 * 1024)
	if !info.RotationPending() {
		t.Fatal("rotation should trigger at the size threshold")
	}
	if reason := info.takeRotationReason(); reason != "size" {
		t.Fatalf("reason = %q, want size", reason)
	}
	if info.RotationPending() {
		t.Fatal("taking the reason must clear the pending flag")
	}
}

func TestInfoSegmentTimeTrigger(t *testing.T) {
	info := &Info{effective: settings.Effective{SegmentMode: "time", SegmentMinutes: 1}}
	info.resetSegmentRotation(time.Now().Add(-2 * time.Minute))
	info.checkSegmentTime()
	if !info.RotationPending() {
		t.Fatal("rotation should trigger after the segment duration")
	}
	if reason := info.takeRotationReason(); reason != "time" {
		t.Fatalf("reason = %q, want time", reason)
	}

	// Other modes never trigger a time rotation.
	off := &Info{effective: settings.Effective{SegmentMode: "off", SegmentMinutes: 1}}
	off.resetSegmentRotation(time.Now().Add(-2 * time.Minute))
	off.checkSegmentTime()
	if off.RotationPending() {
		t.Fatal("time rotation must not trigger when mode != time")
	}

	// Fresh segment: no trigger.
	fresh := &Info{effective: settings.Effective{SegmentMode: "time", SegmentMinutes: 10}}
	fresh.resetSegmentRotation(time.Now())
	fresh.checkSegmentTime()
	if fresh.RotationPending() {
		t.Fatal("fresh segment must not rotate")
	}
}

func TestInfoTitleRotationFirstReasonWins(t *testing.T) {
	info := &Info{room: nil}
	info.SetTitle("新标题")
	if got := info.Title(); got != "新标题" {
		t.Fatalf("Title() = %q", got)
	}
	info.RequestRotation("title")
	info.RequestRotation("size")
	if reason := info.takeRotationReason(); reason != "title" {
		t.Fatalf("first rotation reason should win, got %q", reason)
	}

	// Segment reset clears pending rotation state.
	info.RequestRotation("size")
	info.resetSegmentRotation(time.Now())
	if info.RotationPending() {
		t.Fatal("segment reset must clear the pending rotation")
	}
	if reason := info.takeRotationReason(); reason != "" {
		t.Fatalf("segment reset must clear the reason, got %q", reason)
	}
}

func TestInfoTitleFallsBackToRoom(t *testing.T) {
	info := &Info{}
	if got := info.Title(); got != "" {
		t.Fatalf("empty info title = %q", got)
	}
	room := roomInfoWithTitle("房间标题")
	info.room = room
	if got := info.Title(); got != "房间标题" {
		t.Fatalf("room title fallback = %q", got)
	}
	info.SetTitle("直播标题")
	if got := info.Title(); got != "直播标题" {
		t.Fatalf("live title = %q", got)
	}
}

func TestRevReturnsRotationSentinel(t *testing.T) {
	r := &Service{
		recording: xsync.NewMap[int, *Info](),
		m:         new(metrics.Exporter),
	}
	info := &Info{
		effective: settings.Effective{SegmentMode: "size", SegmentSizeMB: 1},
	}
	info.resetSegmentRotation(time.Now())

	ch := make(chan []byte, 8)
	chunk := make([]byte, 600*1024)
	ch <- chunk
	ch <- chunk
	reasonChunk := flv.NewTagBytes(flv.TagTypeVideo, []byte{0x17, 0x01, 0x00, 0x00, 0x00})
	ch <- reasonChunk
	close(ch)

	pipe := pipeline.New[[]byte]()
	err := r.rev(42, ch, info, context.Background(), pipe)
	if !errors.Is(err, rs.ErrRotationRequested) {
		t.Fatalf("rev error = %v, want ErrRotationRequested", err)
	}
	if !info.RotationPending() {
		t.Fatal("rev should leave the rotation request for the rotate loop")
	}
	if reason := info.takeRotationReason(); reason != "size" {
		t.Fatalf("rotation reason = %q, want size", reason)
	}
}

func TestRevTimeRotationBetweenChunks(t *testing.T) {
	r := &Service{
		recording: xsync.NewMap[int, *Info](),
		m:         new(metrics.Exporter),
	}
	info := &Info{effective: settings.Effective{SegmentMode: "time", SegmentMinutes: 1}}
	info.resetSegmentRotation(time.Now().Add(-2 * time.Minute))

	ch := make(chan []byte, 2)
	ch <- []byte("first")                                                         // triggers the time rotation request
	ch <- flv.NewTagBytes(flv.TagTypeVideo, []byte{0x17, 0x01, 0x00, 0x00, 0x00}) // keyframe for the split
	close(ch)

	pipe := pipeline.New[[]byte]()
	err := r.rev(42, ch, info, context.Background(), pipe)
	if !errors.Is(err, rs.ErrRotationRequested) {
		t.Fatalf("rev error = %v, want ErrRotationRequested", err)
	}
	if reason := info.takeRotationReason(); reason != "time" {
		t.Fatalf("rotation reason = %q, want time", reason)
	}
}

func TestRevCarrySplitsAtKeyframe(t *testing.T) {
	r := &Service{
		recording: xsync.NewMap[int, *Info](),
		m:         new(metrics.Exporter),
	}
	info := &Info{effective: settings.Effective{SegmentMode: "time", SegmentMinutes: 1}}
	info.resetSegmentRotation(time.Now().Add(-2 * time.Minute))

	nonKey := flv.NewTagBytes(flv.TagTypeVideo, []byte{0x27, 0x01, 0x00, 0x00, 0x00})
	keyframe := flv.NewTagBytes(flv.TagTypeVideo, []byte{0x17, 0x01, 0x00, 0x00, 0x00})
	ch := make(chan []byte, 3)
	ch <- []byte("trigger-rotation")
	ch <- nonKey
	ch <- keyframe
	close(ch)

	pipe := pipeline.New[[]byte]()
	carry, err := r.revCarry(42, ch, info, context.Background(), pipe, nil, flv.FindRotationBoundary)
	if !errors.Is(err, rs.ErrRotationRequested) {
		t.Fatalf("revCarry error = %v, want ErrRotationRequested", err)
	}
	if string(carry) != string(keyframe) {
		t.Fatalf("carry should start at keyframe; got %d bytes", len(carry))
	}
}

func TestFinalizeMinFileSizeRemovesSmallFile(t *testing.T) {
	dir := t.TempDir()
	outputPath := filepath.Join(dir, "small.flv")
	if err := os.WriteFile(outputPath, []byte("small"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &Service{
		cfg:          &config.Config{},
		m:            new(metrics.Exporter),
		writingFiles: ds.NewSyncedSet[string](),
	}
	info := &Info{effective: settings.Effective{MinFileSizeMB: 1}}

	r.finalize(42, info, outputPath, false)

	if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
		t.Fatalf("too-small file should be removed, stat err = %v", err)
	}
}

func TestFinalizeKeepsSmallFileWhenThresholdDisabled(t *testing.T) {
	dir := t.TempDir()
	outputPath := filepath.Join(dir, "small.flv")
	if err := os.WriteFile(outputPath, []byte("small"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &Service{
		cfg:          &config.Config{},
		m:            new(metrics.Exporter),
		writingFiles: ds.NewSyncedSet[string](),
	}
	info := &Info{effective: settings.Effective{MinFileSizeMB: 0}}

	r.finalize(42, info, outputPath, false)

	if _, err := os.Stat(outputPath); err != nil {
		t.Fatalf("file should be kept when MinFileSizeMB is disabled: %v", err)
	}
}

func TestAdvanceRotationFileTime(t *testing.T) {
	t0 := time.Date(2026, 3, 9, 14, 5, 7, 0, time.Local)
	info := &Info{fileTime: t0}

	advanceRotationFileTime(info, 0, t0.Add(10*time.Second))
	if !info.fileTime.Equal(t0) {
		t.Fatalf("segment 0 must keep fileTime, got %v", info.fileTime)
	}

	advanceRotationFileTime(info, 1, t0.Add(10*time.Second))
	if want := t0.Add(10 * time.Second); !info.fileTime.Equal(want) {
		t.Fatalf("segment 1 fileTime = %v, want %v", info.fileTime, want)
	}

	// Same-second rotations still advance so filenames cannot collide.
	advanceRotationFileTime(info, 2, t0.Add(10*time.Second))
	if want := t0.Add(11 * time.Second); !info.fileTime.Equal(want) {
		t.Fatalf("same-second rotation fileTime = %v, want %v", info.fileTime, want)
	}
}

func TestRotateFilePathLegacyDefaultNaming(t *testing.T) {
	dir := t.TempDir()
	r := &Service{cfg: &config.Config{OutputDir: dir}}
	info := &Info{
		room:     roomInfoWithTitle("测试标题"),
		fileTime: time.Date(2026, 3, 9, 14, 5, 7, 0, time.Local),
	}
	path, err := r.rotateFilePath(info, 0, ".flv")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := filepath.Base(path), "测试标题-20260309_140507.flv"; got != want {
		t.Fatalf("legacy default naming = %q, want %q", got, want)
	}
	// Long titles keep the legacy 20-rune truncation by default.
	longTitle := "这是一个非常非常长的直播标题需要被截断到二十个字符以内才能保持旧行为"
	info.room = roomInfoWithTitle(longTitle)
	info.fileTime = time.Date(2026, 3, 9, 14, 5, 8, 0, time.Local)
	path, err = r.rotateFilePath(info, 0, ".flv")
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Base(path)
	if len([]rune(base)) > 20+1+15+4 {
		t.Fatalf("legacy title truncation changed: %q", base)
	}
}

func TestRotateFilePathTemplateNaming(t *testing.T) {
	dir := t.TempDir()
	r := &Service{cfg: &config.Config{OutputDir: dir}}
	info := &Info{
		room:     roomInfoWithTitle("标题"),
		fileTime: time.Date(2026, 3, 9, 14, 5, 7, 0, time.Local),
		effective: settings.Effective{
			FilenameTemplate: "{title}-{date}_{time}-{segment}",
		},
	}
	path, err := r.rotateFilePath(info, 2, ".flv")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := filepath.Base(path), "标题-20260309_140507-2-2.flv"; got != want {
		t.Fatalf("template naming = %q, want %q", got, want)
	}
}

func TestRotateFilePathUsesLiveTitle(t *testing.T) {
	dir := t.TempDir()
	r := &Service{cfg: &config.Config{OutputDir: dir}}
	info := &Info{
		room:      roomInfoWithTitle("旧标题"),
		fileTime:  time.Date(2026, 3, 9, 14, 5, 7, 0, time.Local),
		effective: settings.Effective{FilenameTemplate: "{title}"},
	}
	info.SetTitle("新标题")
	path, err := r.rotateFilePath(info, 0, ".flv")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := filepath.Base(path), "新标题.flv"; got != want {
		t.Fatalf("live title naming = %q, want %q", got, want)
	}
}

func roomInfoWithTitle(title string) *bilibili.LiveRoomInfoDetail {
	return &bilibili.LiveRoomInfoDetail{RoomID: 42, Uname: "主播", Title: title}
}

func TestRotateFilePathPathTemplateSubdirs(t *testing.T) {
	root := t.TempDir()
	r := &Service{cfg: &config.Config{OutputDir: root}}
	info := &Info{
		room:     &bilibili.LiveRoomInfoDetail{RoomID: 42, Uname: "主播", Title: "标题"},
		fileTime: time.Date(2026, 3, 9, 14, 5, 7, 0, time.Local),
		effective: settings.Effective{
			FilenameTemplate: "{uname}-{roomid}/{year}/{month}/{title}",
		},
	}
	path, err := r.rotateFilePath(info, 0, ".flv")
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("主播-42", "2026", "03", "标题.flv")
	if rel != want {
		t.Fatalf("path template rel = %q, want %q", rel, want)
	}

	// Cover and danmaku sidecars derive from the same relative dir/basename.
	if got, wantCover := cover.PathFor(path), filepath.Join(filepath.Dir(path), "标题-cover.jpg"); got != wantCover {
		t.Fatalf("cover path = %q, want %q", got, wantCover)
	}
	if got, wantDm := danmaku.PathForVideo(path, ".jsonl"), filepath.Join(filepath.Dir(path), "标题.jsonl"); got != wantDm {
		t.Fatalf("danmaku path = %q, want %q", got, wantDm)
	}

	// A template without directory segments stays flat under OUTPUT_DIR (no
	// implicit <uname>-<roomid>/ prefix).
	info.effective.FilenameTemplate = "{title}-{date}"
	flat, err := r.rotateFilePath(info, 0, ".flv")
	if err != nil {
		t.Fatal(err)
	}
	if got, wantFlat := filepath.Dir(flat), root; got != wantFlat {
		t.Fatalf("flat template dir = %q, want %q", got, wantFlat)
	}
	if got, wantBase := filepath.Base(flat), "标题-20260309.flv"; got != wantBase {
		t.Fatalf("flat template base = %q, want %q", got, wantBase)
	}
}

func TestRotateFilePathPathTemplateRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	r := &Service{cfg: &config.Config{OutputDir: root}}
	info := &Info{
		room:     roomInfoWithTitle("标题"),
		fileTime: time.Date(2026, 3, 9, 14, 5, 7, 0, time.Local),
		effective: settings.Effective{
			FilenameTemplate: "../escape/{title}",
		},
	}
	if _, err := r.rotateFilePath(info, 0, ".flv"); err == nil {
		t.Fatal("expected traversal template to be rejected")
	}
}

func TestRotateFilePathDefaultPathTemplate(t *testing.T) {
	root := t.TempDir()
	r := &Service{cfg: &config.Config{OutputDir: root}}
	info := &Info{
		room:     &bilibili.LiveRoomInfoDetail{RoomID: 42, Uname: "主播", Title: "标题"},
		fileTime: time.Date(2026, 3, 9, 14, 5, 7, 0, time.Local),
		effective: settings.Effective{
			FilenameTemplate: settings.DefaultFilenameTemplate,
		},
	}
	path, err := r.rotateFilePath(info, 0, ".flv")
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("42-主播", "20260309_140507_标题.flv")
	if rel != want {
		t.Fatalf("default path template rel = %q, want %q", rel, want)
	}
}
