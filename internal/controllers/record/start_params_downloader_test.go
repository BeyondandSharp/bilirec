// SPDX-License-Identifier: GPL-3.0-or-later

package record

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestResolvedStartParamsDownloaderQuery(t *testing.T) {
	app := fiber.New()
	app.Post("/start", func(c fiber.Ctx) error {
		resolved, err := parseStartRecordingParams(c)
		if err != nil {
			return err
		}
		if !resolved.hasDownloader || resolved.downloaderRaw != "ffmpeg" {
			return fiber.NewError(fiber.StatusTeapot, "downloader query not resolved")
		}
		return c.SendStatus(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/start?downloader=ffmpeg", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestResolvedStartParamsDownloaderJSON(t *testing.T) {
	app := fiber.New()
	app.Post("/start", func(c fiber.Ctx) error {
		resolved, err := parseStartRecordingParams(c)
		if err != nil {
			return err
		}
		if !resolved.hasDownloader || resolved.downloaderRaw != "streamlink" {
			return fiber.NewError(fiber.StatusTeapot, "downloader json not resolved")
		}
		return c.SendStatus(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/start", strings.NewReader(`{"downloader":"streamlink"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestRecordStartOptionsFromResolvedDownloader(t *testing.T) {
	t.Run("invalid", func(t *testing.T) {
		_, err := recordStartOptionsFromResolved(resolvedStartParams{hasDownloader: true, downloaderRaw: "youtube-dl"})
		if err == nil {
			t.Fatal("expected error for invalid downloader")
		}
	})

	t.Run("auto inherits global config", func(t *testing.T) {
		opts, err := recordStartOptionsFromResolved(resolvedStartParams{hasDownloader: true, downloaderRaw: "auto"})
		if err != nil {
			t.Fatal(err)
		}
		if len(opts) != 1 {
			t.Fatalf("expected only the stream options wrapper, got %d options", len(opts))
		}
	})

	t.Run("explicit kinds add an option", func(t *testing.T) {
		for _, kind := range []string{"native", "ffmpeg", "streamlink"} {
			opts, err := recordStartOptionsFromResolved(resolvedStartParams{hasDownloader: true, downloaderRaw: kind})
			if err != nil {
				t.Fatalf("%s: %v", kind, err)
			}
			if len(opts) != 2 {
				t.Fatalf("%s: expected stream options + downloader, got %d options", kind, len(opts))
			}
		}
	})

	t.Run("omitted downloader adds no option", func(t *testing.T) {
		opts, err := recordStartOptionsFromResolved(resolvedStartParams{})
		if err != nil {
			t.Fatal(err)
		}
		if len(opts) != 1 {
			t.Fatalf("expected only the stream options wrapper, got %d options", len(opts))
		}
	})
}
