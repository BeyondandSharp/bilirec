package repairqueue

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/services/path"
	"go.uber.org/fx/fxtest"
)

type fakeRepairer struct {
	enabled bool
	run     func(input, output string)
}

func (f *fakeRepairer) Enabled() bool { return f.enabled }

func (f *fakeRepairer) RepairFile(_ context.Context, input, output string) error {
	if f.run != nil {
		f.run(input, output)
	}
	return nil
}

func newTestQueue(t *testing.T, repairer fileRepairer) (*Service, *config.Config) {
	t.Helper()
	root := t.TempDir()
	cfg := &config.Config{OutputDir: filepath.Join(root, "records"), DatabaseDir: filepath.Join(root, "database")}
	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lc := fxtest.NewLifecycle(t)
	svc := newService(lc, cfg, path.NewService(cfg), repairer)
	lc.RequireStart()
	t.Cleanup(lc.RequireStop)
	return svc, cfg
}

func TestAddQueueRepairAndPersist(t *testing.T) {
	svc, cfg := newTestQueue(t, &fakeRepairer{enabled: true, run: func(_, output string) {
		_ = os.WriteFile(output, []byte("fixed"), 0o644)
	}})
	if err := os.WriteFile(filepath.Join(cfg.OutputDir, "a.flv"), []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}

	added, err := svc.Add([]string{"a.flv"})
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 || added[0].OutputPath != "a_fix.flv" {
		t.Fatalf("added = %+v", added)
	}
	if _, err := svc.Queue([]string{"a.flv"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		items := svc.List()
		if len(items) == 1 && items[0].Status == StatusDone {
			if _, err := os.Stat(filepath.Join(cfg.OutputDir, "a_fix.flv")); err != nil {
				t.Fatalf("output missing: %v", err)
			}
			if _, err := os.Stat(filepath.Join(cfg.DatabaseDir, "repair-queue.json")); err != nil {
				t.Fatalf("queue file missing: %v", err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("repair did not finish: %+v", svc.List())
}

func TestAddChoosesNonCollidingFixName(t *testing.T) {
	svc, cfg := newTestQueue(t, &fakeRepairer{enabled: true})
	if err := os.WriteFile(filepath.Join(cfg.OutputDir, "a.flv"), []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.OutputDir, "a_fix.flv"), []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	added, err := svc.Add([]string{"a.flv"})
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 || added[0].OutputPath != "a_fix_1.flv" {
		t.Fatalf("added = %+v", added)
	}
}

func TestAddRejectsUnavailableAndNonFLV(t *testing.T) {
	svc, _ := newTestQueue(t, &fakeRepairer{enabled: false})
	if _, err := svc.Add([]string{"a.flv"}); err != ErrNotAvailable {
		t.Fatalf("disabled add err = %v", err)
	}

	svc, cfg := newTestQueue(t, &fakeRepairer{enabled: true})
	if err := os.WriteFile(filepath.Join(cfg.OutputDir, "a.mp4"), []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Add([]string{"a.mp4"}); err != ErrNotFLV {
		t.Fatalf("non-flv add err = %v", err)
	}
}

func TestListOrderIsStableWithinSameSecond(t *testing.T) {
	svc, cfg := newTestQueue(t, &fakeRepairer{enabled: true})
	paths := []string{"c.flv", "a.flv", "b.flv"}
	for _, name := range paths {
		if err := os.WriteFile(filepath.Join(cfg.OutputDir, name), []byte("source"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.Add(paths); err != nil {
		t.Fatal(err)
	}

	want := append([]string(nil), paths...)
	for i := 0; i < 5; i++ {
		items := svc.List()
		got := make([]string, len(items))
		for index, item := range items {
			got[index] = item.Path
		}
		if len(got) != len(want) {
			t.Fatalf("items = %v, want %v", got, want)
		}
		for index := range want {
			if got[index] != want[index] {
				t.Fatalf("poll %d order = %v, want %v", i, got, want)
			}
		}
	}

	// A restarted service loading the persisted queue keeps the same order.
	lc := fxtest.NewLifecycle(t)
	restarted := newService(lc, cfg, path.NewService(cfg), &fakeRepairer{enabled: true})
	lc.RequireStart()
	t.Cleanup(lc.RequireStop)
	items := restarted.List()
	got := make([]string, len(items))
	for index, item := range items {
		got[index] = item.Path
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("restarted order = %v, want %v", got, want)
		}
	}
}

func TestListProgressForRunningAndDoneEntries(t *testing.T) {
	svc, cfg := newTestQueue(t, &fakeRepairer{enabled: true})
	if err := os.WriteFile(filepath.Join(cfg.OutputDir, "a.flv"), []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Add([]string{"a.flv"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.OutputDir, "a_fix.flv"), []byte("01234"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc.mu.Lock()
	svc.entries["a.flv"].Status = StatusRunning
	svc.mu.Unlock()

	items := svc.List()
	if len(items) != 1 {
		t.Fatalf("items = %+v", items)
	}
	if items[0].Progress < 0.49 || items[0].Progress > 0.51 {
		t.Fatalf("running progress = %v, want 0.5", items[0].Progress)
	}

	svc.mu.Lock()
	svc.entries["a.flv"].Status = StatusDone
	svc.mu.Unlock()
	if got := svc.List()[0].Progress; got != 1 {
		t.Fatalf("done progress = %v, want 1", got)
	}
}
