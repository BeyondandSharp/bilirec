// SPDX-License-Identifier: GPL-3.0-or-later

package cover

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bilirec/bilirec/pkg/logger"
)

func testCoverService(maxBytes int64) *Service {
	return &Service{
		client:   &http.Client{},
		maxBytes: maxBytes,
		timeout:  2 * time.Second,
	}
}

func TestSaveAsyncWritesCover(t *testing.T) {
	payload := []byte("fake-jpeg-bytes")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "video-cover.jpg")
	svc := testCoverService(0)
	svc.SaveAsync(context.Background(), server.URL+"/cover.jpg", dest, logger.Nop())

	waitForFile(t, dest)
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("cover content = %q, want %q", got, payload)
	}
}

func TestSaveSkipsExistingFile(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("new"))
	}))
	defer server.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "video-cover.jpg")
	if err := os.WriteFile(dest, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := testCoverService(0)
	if err := svc.save(context.Background(), server.URL, dest); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 0 {
		t.Fatalf("existing cover must not be re-fetched, requests = %d", requests.Load())
	}
	got, _ := os.ReadFile(dest)
	if string(got) != "existing" {
		t.Fatalf("existing cover overwritten: %q", got)
	}
}

func TestFetchRejectsNonImageContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>"))
	}))
	defer server.Close()

	svc := testCoverService(0)
	if _, err := svc.fetch(context.Background(), server.URL); err == nil {
		t.Fatal("expected non-image content type to be rejected")
	}
}

func TestFetchRejectsOversizedCover(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte(strings.Repeat("x", 2048)))
	}))
	defer server.Close()

	svc := testCoverService(1024)
	if _, err := svc.fetch(context.Background(), server.URL); err == nil {
		t.Fatal("expected oversized cover to be rejected")
	}
}

func TestFetchRejectsNon200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer server.Close()

	svc := testCoverService(0)
	if _, err := svc.fetch(context.Background(), server.URL); err == nil {
		t.Fatal("expected non-200 to be rejected")
	}
}

func TestSaveAsyncEmptyInputsAreNoOps(t *testing.T) {
	svc := testCoverService(0)
	svc.SaveAsync(context.Background(), "", "/tmp/x-cover.jpg", logger.Nop())
	svc.SaveAsync(context.Background(), "http://example.invalid/x", "", logger.Nop())
	var nilSvc *Service
	nilSvc.SaveAsync(context.Background(), "http://example.invalid/x", "/tmp/y-cover.jpg", logger.Nop())
}

func TestPathFor(t *testing.T) {
	got := PathFor("/records/user-1/title-20260309_140507.flv")
	if want := "/records/user-1/title-20260309_140507-cover.jpg"; got != want {
		t.Fatalf("PathFor = %q, want %q", got, want)
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("file %s was not written", path)
}
