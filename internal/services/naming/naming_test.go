// SPDX-License-Identifier: GPL-3.0-only

package naming

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func fixedTime() time.Time {
	return time.Date(2026, 3, 9, 14, 5, 7, 0, time.Local)
}

func TestRenderPathVariables(t *testing.T) {
	vars := Vars{
		RoomID:  1234,
		ShortID: 42,
		Uname:   "主播/名字",
		Title:   `标题: "测试" <live>`,
		Time:    fixedTime(),
		Segment: 3,
	}
	tests := []struct {
		template string
		want     string
	}{
		{"{roomid}", "1234"},
		{"{shortid}", "42"},
		{"{uname}", "主播名字"},
		{"{title}", "标题 测试 live"},
		{"{year}-{month}-{day}", "2026-03-09"},
		{"{hour}{minute}{second}", "140507"},
		{"{date}_{time}", "20260309_140507"},
		{"{segment}", "3"},
		{"{title}-{date}", "标题 测试 live-20260309"},
		{"unknown-{name}-kept", "unknown-{name}-kept"},
		{"{title", "{title"},
		{"{uname}/{title}", "主播名字/标题 测试 live"},
		{"live/{year}/{month}/{date}", "live/2026/03/20260309"},
	}
	for _, tt := range tests {
		t.Run(tt.template, func(t *testing.T) {
			got, err := RenderPath(tt.template, vars)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("RenderPath(%q) = %q, want %q", tt.template, got, tt.want)
			}
		})
	}
}

func TestRenderPathStrftime(t *testing.T) {
	vars := Vars{Time: fixedTime()}
	got, err := RenderPath("%Y%m%d_%H%M%S", vars)
	if err != nil {
		t.Fatal(err)
	}
	if got != "20260309_140507" {
		t.Fatalf("strftime = %q", got)
	}

	// Unsupported %-directives stay literal.
	got, err = RenderPath("%Y-%q-%", vars)
	if err != nil {
		t.Fatal(err)
	}
	if got != "2026-%q-%" {
		t.Fatalf("unknown strftime directives must stay literal, got %q", got)
	}

	// Percent inside a substituted title is never treated as a directive.
	got, err = RenderPath("{title}-%Y", Vars{Title: "%Y", Time: fixedTime()})
	if err != nil {
		t.Fatal(err)
	}
	if got != "%Y-2026" {
		t.Fatalf("title percent handling = %q", got)
	}
}

func TestRenderPathSanitizesValues(t *testing.T) {
	// A title containing "/" must not create a subdirectory.
	vars := Vars{Title: "a/b\\c:d*e?f\"g<h>i|j\u0001k", Time: fixedTime()}
	got, err := RenderPath("sub/{title}", vars)
	if err != nil {
		t.Fatal(err)
	}
	if got != "sub/abcdefghijk" {
		t.Fatalf("sanitized path = %q", got)
	}

	// Literal segments are sanitized with the same rules.
	got, err = RenderPath("a:b/c*d/{title}", Vars{Title: "x", Time: fixedTime()})
	if err != nil {
		t.Fatal(err)
	}
	if got != "ab/cd/x" {
		t.Fatalf("literal segment sanitize = %q", got)
	}

	// Leading/trailing dots and spaces are trimmed per segment.
	got, err = RenderPath("  ..hello.. /{title}", Vars{Title: "  ..hi.. ", Time: fixedTime()})
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello/hi" {
		t.Fatalf("trimmed path = %q", got)
	}
}

func TestRenderPathTruncatesLongValues(t *testing.T) {
	// ASCII title: the 80-rune cap applies and stays under the 200-byte
	// segment cap.
	got, err := RenderPath("{title}", Vars{Title: strings.Repeat("x", 200)})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != maxTitleRunes {
		t.Fatalf("title should be truncated to %d runes, got %d", maxTitleRunes, len(got))
	}

	// CJK title: 80 runes exceed 200 bytes => render error (segment cap).
	if _, err := RenderPath("{title}", Vars{Title: strings.Repeat("喵", 200)}); err == nil {
		t.Fatal("expected segment-size error for long CJK title")
	}

	// A literal segment longer than 200 bytes is rejected structurally.
	if _, err := RenderPath(strings.Repeat("a", 201), Vars{}); err == nil {
		t.Fatal("expected segment-size error for long literal segment")
	}
}

func TestRenderPathInvalidMatrix(t *testing.T) {
	longSegment := strings.Repeat("a", maxSegmentBytes+1)
	longTemplate := strings.Repeat("a", maxTemplateBytes+1)
	// Many 80-byte segments: template is short but the rendered path exceeds
	// the 512-byte total cap.
	manyTitles := strings.Repeat("{title}/", 7) + "{title}"

	tests := []struct {
		name     string
		template string
		vars     Vars
	}{
		{"empty", "", Vars{}},
		{"absolute", "/abs/path", Vars{}},
		{"double slash", "a//b", Vars{}},
		{"trailing slash", "a/b/", Vars{}},
		{"dot segment", "a/./b", Vars{}},
		{"dotdot segment", "a/../b", Vars{}},
		{"leading dotdot", "../escape", Vars{}},
		{"backslash", `a\b`, Vars{}},
		{"control char", "a\x01b", Vars{}},
		{"template too long", longTemplate, Vars{}},
		{"segment too long", longSegment, Vars{}},
		{"rendered total too long", manyTitles, Vars{Title: strings.Repeat("x", 80)}},
		{"rendered segment empty", "{title}", Vars{Title: "... "}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := RenderPath(tt.template, tt.vars); err == nil {
				t.Fatalf("expected error for template %q", tt.template)
			}
		})
	}
}

func TestValidatePathTemplate(t *testing.T) {
	valid := []string{
		"",
		"   ",
		"{title}-{date}",
		"{uname}-{roomid}/{title}",
		"a/b/c",
		"{title}/{year}/{month}",
	}
	for _, template := range valid {
		if err := ValidatePathTemplate(template); err != nil {
			t.Fatalf("ValidatePathTemplate(%q) = %v, want nil", template, err)
		}
	}

	invalid := []string{
		"/absolute",
		"a//b",
		"a/",
		"a/./b",
		"a/../b",
		`a\b`,
		"a\x00b",
		"a\x7fb",
		strings.Repeat("a", maxTemplateBytes+1),
		strings.Repeat("a", maxSegmentBytes+1),
	}
	for _, template := range invalid {
		if err := ValidatePathTemplate(template); err == nil {
			t.Fatalf("ValidatePathTemplate(%q) = nil, want error", template)
		}
	}
}

func TestDefaultTemplateRenderPath(t *testing.T) {
	got, err := RenderPath(DefaultTemplate, Vars{Title: "直播标题", Time: fixedTime()})
	if err != nil {
		t.Fatal(err)
	}
	if got != "直播标题-20260309_140507" {
		t.Fatalf("default template render = %q", got)
	}
}

func TestResolvePathInRootCreatesSubdirs(t *testing.T) {
	root := t.TempDir()
	path, err := ResolvePathInRoot(root, "{uname}-{roomid}/{year}/{month}/{title}", ".flv", Vars{
		RoomID: 42, Uname: "主播", Title: "标题", Time: fixedTime(),
	})
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("主播-42", "2026", "03", "标题.flv")
	if rel != want {
		t.Fatalf("resolved rel = %q, want %q", rel, want)
	}
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Fatalf("parent dir was not created: %v", err)
	}
	if !isWithinRoot(root, path) {
		t.Fatalf("resolved path escaped root: %s", path)
	}
}

func TestResolvePathInRootCollisionInSubdir(t *testing.T) {
	root := t.TempDir()
	template := "sub/{title}"
	vars := Vars{Title: "video", Time: fixedTime()}

	first, err := ResolvePathInRoot(root, template, ".flv", vars)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(first, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := ResolvePathInRoot(root, template, ".flv", vars)
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(second); got != "video-1.flv" {
		t.Fatalf("collision basename = %q, want video-1.flv", got)
	}
	if filepath.Dir(second) != filepath.Dir(first) {
		t.Fatalf("collision moved to a different dir: %q vs %q", filepath.Dir(second), filepath.Dir(first))
	}
}

func TestResolvePathInRootSegmentSuffix(t *testing.T) {
	root := t.TempDir()
	path, err := ResolvePathInRoot(root, "sub/{title}", ".flv", Vars{Title: "video", Time: fixedTime(), Segment: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := filepath.Base(path), "video-2.flv"; got != want {
		t.Fatalf("segment basename = %q, want %q", got, want)
	}
	if !strings.HasSuffix(filepath.Dir(path), "sub") {
		t.Fatalf("segment path dir = %q, want .../sub", filepath.Dir(path))
	}
}

func TestIsWithinRoot(t *testing.T) {
	root := t.TempDir()
	if !isWithinRoot(root, filepath.Join(root, "a", "b.flv")) {
		t.Fatal("expected nested path inside root")
	}
	if isWithinRoot(root, filepath.Join(root, "..", "escape.flv")) {
		t.Fatal("expected ../ path outside root")
	}
}

func TestUniquePathCollision(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 3, 9, 14, 5, 7, 123456789, time.Local)
	oldNow := nowFunc
	nowFunc = func() time.Time { return now }
	t.Cleanup(func() { nowFunc = oldNow })

	path, err := UniquePath(dir, "video", ".flv", 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(path); got != "video.flv" {
		t.Fatalf("base = %q", got)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	path, err = UniquePath(dir, "video", ".flv", 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(path); got != "video-1.flv" {
		t.Fatalf("collision suffix = %q, want video-1.flv", got)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	path, err = UniquePath(dir, "video", ".flv", 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(path); got != "video-2.flv" {
		t.Fatalf("collision suffix = %q, want video-2.flv", got)
	}
}

func TestUniquePathTimestampFallback(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 3, 9, 14, 5, 7, 0, time.Local)
	oldNow := nowFunc
	nowFunc = func() time.Time { return now }
	t.Cleanup(func() { nowFunc = oldNow })

	base := strings.Repeat("b", 180)
	for i := 0; i <= 999; i++ {
		name := base + ".flv"
		if i > 0 {
			name = base + "-" + strconv.Itoa(i) + ".flv"
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	path, err := UniquePath(dir, base, ".flv", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(filepath.Base(path), base+"-") || !strings.Contains(filepath.Base(path), strconv.FormatInt(now.UnixNano(), 10)) {
		t.Fatalf("expected timestamp fallback, got %q", filepath.Base(path))
	}
}

func TestUniquePathSegmentSuffix(t *testing.T) {
	dir := t.TempDir()
	path, err := UniquePath(dir, "video", ".flv", 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(path); got != "video-2.flv" {
		t.Fatalf("segment suffix = %q", got)
	}
}

func TestResolvePathInRootRelativeRootStaysRelative(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	path, err := ResolvePathInRoot("records", "sub/{title}", ".flv", Vars{Title: "video", Time: fixedTime()})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.IsAbs(path) {
		t.Fatalf("relative OUTPUT_DIR must produce a relative path, got %q", path)
	}
	want := filepath.Join("records", "sub", "video.flv")
	if path != want {
		t.Fatalf("relative path = %q, want %q", path, want)
	}
	if _, err := os.Stat(filepath.Dir(filepath.Join(dir, want))); err != nil {
		t.Fatalf("file parent not created under cwd: %v", err)
	}
}
