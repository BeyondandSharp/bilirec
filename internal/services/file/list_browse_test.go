package file_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/services/file"
	"github.com/bilirec/bilirec/internal/services/path"
	"github.com/bilirec/bilirec/utils"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

func setupFileService(t *testing.T) (*file.Service, string) {
	t.Helper()
	dir := t.TempDir()
	os.Setenv("OUTPUT_DIR", dir)

	var fileService *file.Service
	app := fxtest.New(t,
		config.Module,
		fx.Provide(path.NewService),
		fx.Provide(file.NewService),
		fx.Populate(&fileService),
	)
	app.RequireStart()
	t.Cleanup(app.RequireStop)
	return fileService, dir
}

func touchFile(t *testing.T, path string, mod time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func touchDir(t *testing.T, path string, mod time.Time) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func TestListTreeWithOptionsSortsFilesNewestFirst(t *testing.T) {
	svc, root := setupFileService(t)
	room := filepath.Join(root, "anchor-1")
	if err := os.MkdirAll(room, 0o755); err != nil {
		t.Fatal(err)
	}

	touchFile(t, filepath.Join(room, "old-20200101_000000.flv"), time.Now())
	touchFile(t, filepath.Join(room, "new-20260101_120000.flv"), time.Now())
	touchFile(t, filepath.Join(room, "mid-20250101_000000.flv"), time.Now())
	touchFile(t, filepath.Join(room, "no-stamp.txt"), time.Now())

	paged, err := svc.ListTreeWithOptions("anchor-1", file.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if paged.Total != 4 {
		t.Fatalf("total=%d want 4", paged.Total)
	}
	names := []string{paged.Items[0].Name, paged.Items[1].Name, paged.Items[2].Name, paged.Items[3].Name}
	want := []string{"new-20260101_120000.flv", "mid-20250101_000000.flv", "old-20200101_000000.flv", "no-stamp.txt"}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("order[%d]=%q want %q full=%v", i, names[i], want[i], names)
		}
	}
}

func TestListTreeWithOptionsSortsSameSecondBySegment(t *testing.T) {
	svc, root := setupFileService(t)
	room := filepath.Join(root, "anchor-1")
	if err := os.MkdirAll(room, 0o755); err != nil {
		t.Fatal(err)
	}
	touchFile(t, filepath.Join(room, "t-20260101_000000.flv"), time.Now())
	touchFile(t, filepath.Join(room, "t-20260101_000000-2.flv"), time.Now())

	paged, err := svc.ListTreeWithOptions("anchor-1", file.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(paged.Items) != 2 || paged.Items[0].Name != "t-20260101_000000-2.flv" {
		t.Fatalf("got %v", paged.Items)
	}
}

func TestListTreeWithOptionsSortsDirsByModTime(t *testing.T) {
	svc, root := setupFileService(t)
	oldMod := time.Date(2020, 1, 1, 0, 0, 0, 0, time.Local)
	newMod := time.Date(2026, 6, 1, 0, 0, 0, 0, time.Local)
	touchDir(t, filepath.Join(root, "aaa-111"), oldMod)
	touchDir(t, filepath.Join(root, "zzz-222"), newMod)
	touchFile(t, filepath.Join(root, "file-20260101_000000.flv"), time.Now())

	paged, err := svc.ListTreeWithOptions("", file.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(paged.Items) != 3 {
		t.Fatalf("len=%d", len(paged.Items))
	}
	if !paged.Items[0].IsDir || paged.Items[0].Name != "zzz-222" {
		t.Fatalf("first=%+v", paged.Items[0])
	}
	if !paged.Items[1].IsDir || paged.Items[1].Name != "aaa-111" {
		t.Fatalf("second=%+v", paged.Items[1])
	}
	if paged.Items[2].IsDir {
		t.Fatalf("third should be file")
	}
}

func TestListTreeWithOptionsTimeRangeFilter(t *testing.T) {
	svc, root := setupFileService(t)
	room := filepath.Join(root, "anchor-1")
	if err := os.MkdirAll(room, 0o755); err != nil {
		t.Fatal(err)
	}
	touchFile(t, filepath.Join(room, "a-20260101_000000.flv"), time.Now())
	touchFile(t, filepath.Join(room, "b-20260115_120000.flv"), time.Now())
	touchFile(t, filepath.Join(room, "c-20260201_000000.flv"), time.Now())
	touchFile(t, filepath.Join(room, "side-20260115_120000.jsonl"), time.Now())
	touchFile(t, filepath.Join(room, "readme.txt"), time.Now())

	start := mustParseStamp(t, "20260110_000000")
	end := mustParseStamp(t, "20260120_235959")
	paged, err := svc.ListTreeWithOptions("anchor-1", file.ListOptions{Start: &start, End: &end})
	if err != nil {
		t.Fatal(err)
	}
	if paged.Total != 2 {
		t.Fatalf("total=%d want 2", paged.Total)
	}
	names := []string{paged.Items[0].Name, paged.Items[1].Name}
	if names[0] != "b-20260115_120000.flv" || names[1] != "side-20260115_120000.jsonl" {
		t.Fatalf("got %v", names)
	}
}

func TestListTreeWithOptionsPaginationAfterSort(t *testing.T) {
	svc, root := setupFileService(t)
	room := filepath.Join(root, "anchor-1")
	if err := os.MkdirAll(room, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"a-20260101_000000.flv",
		"b-20260102_000000.flv",
		"c-20260103_000000.flv",
	} {
		touchFile(t, filepath.Join(room, name), time.Now())
	}

	paged, err := svc.ListTreeWithOptions("anchor-1", file.ListOptions{Offset: 1, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if paged.Total != 3 || len(paged.Items) != 1 || paged.Items[0].Name != "b-20260102_000000.flv" {
		t.Fatalf("got total=%d items=%v", paged.Total, paged.Items)
	}
}

func mustParseStamp(t *testing.T, stamp string) time.Time {
	t.Helper()
	parsed, err := time.ParseInLocation(utils.RecordingNameStampLayout, stamp, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
