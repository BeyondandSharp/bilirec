package file_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/services/file"
	"github.com/bilirec/bilirec/internal/services/path"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

func TestIsRecordingMediaFilename(t *testing.T) {
	media := []string{"a.mp4", "b.M4A", "c.ts", "seg.fmp4", "live.flv"}
	for _, name := range media {
		if !file.IsRecordingMediaFilename(name) {
			t.Fatalf("%q should be media", name)
		}
	}

	nonMedia := []string{"danmaku.jsonl", "danmaku.xml", "notes.log", "readme", "dir"}
	for _, name := range nonMedia {
		if file.IsRecordingMediaFilename(name) {
			t.Fatalf("%q should not be media", name)
		}
	}
}

func TestOpenForPlaybackMP4(t *testing.T) {
	tempDir := t.TempDir()
	os.Setenv("OUTPUT_DIR", tempDir)

	filePath := filepath.Join(tempDir, "test.mp4")
	if err := os.WriteFile(filePath, []byte("123"), 0644); err != nil {
		t.Fatalf("failed to create mp4 test file: %v", err)
	}

	var svc *file.Service
	app := fxtest.New(t,
		config.Module,
		fx.Provide(path.NewService),
		fx.Provide(file.NewService),
		fx.Populate(&svc),
	)
	app.RequireStart()
	defer app.RequireStop()

	fullPath, mimeType, err := svc.OpenForPlayback("test.mp4")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if fullPath != filePath {
		t.Fatalf("expected full path %s, got %s", filePath, fullPath)
	}
	if mimeType != "video/mp4" {
		t.Fatalf("expected mime video/mp4, got %s", mimeType)
	}
}

func TestOpenForPlaybackM4A(t *testing.T) {
	tempDir := t.TempDir()
	os.Setenv("OUTPUT_DIR", tempDir)

	filePath := filepath.Join(tempDir, "test.m4a")
	if err := os.WriteFile(filePath, []byte("123"), 0644); err != nil {
		t.Fatalf("failed to create m4a test file: %v", err)
	}

	var svc *file.Service
	app := fxtest.New(t,
		config.Module,
		fx.Provide(path.NewService),
		fx.Provide(file.NewService),
		fx.Populate(&svc),
	)
	app.RequireStart()
	defer app.RequireStop()

	fullPath, mimeType, err := svc.OpenForPlayback("test.m4a")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if fullPath != filePath {
		t.Fatalf("expected full path %s, got %s", filePath, fullPath)
	}
	if mimeType != "audio/mp4" {
		t.Fatalf("expected mime audio/mp4, got %s", mimeType)
	}
}

func TestOpenForPlaybackUnsupportedMedia(t *testing.T) {
	tempDir := t.TempDir()
	os.Setenv("OUTPUT_DIR", tempDir)

	filePath := filepath.Join(tempDir, "test.mkv")
	if err := os.WriteFile(filePath, []byte("123"), 0644); err != nil {
		t.Fatalf("failed to create mkv test file: %v", err)
	}

	var svc *file.Service
	app := fxtest.New(t,
		config.Module,
		fx.Provide(path.NewService),
		fx.Provide(file.NewService),
		fx.Populate(&svc),
	)
	app.RequireStart()
	defer app.RequireStop()

	_, _, err := svc.OpenForPlayback("test.mkv")
	if !errors.Is(err, file.ErrUnsupportedPlaybackMedia) {
		t.Fatalf("expected ErrUnsupportedPlaybackMedia, got %v", err)
	}
}

func TestOpenForPlaybackDirectory(t *testing.T) {
	tempDir := t.TempDir()
	os.Setenv("OUTPUT_DIR", tempDir)

	if err := os.MkdirAll(filepath.Join(tempDir, "videos"), 0755); err != nil {
		t.Fatalf("failed to create test directory: %v", err)
	}

	var svc *file.Service
	app := fxtest.New(t,
		config.Module,
		fx.Provide(path.NewService),
		fx.Provide(file.NewService),
		fx.Populate(&svc),
	)
	app.RequireStart()
	defer app.RequireStop()

	_, _, err := svc.OpenForPlayback("videos")
	if !errors.Is(err, file.ErrIsDirectory) {
		t.Fatalf("expected ErrIsDirectory, got %v", err)
	}
}
