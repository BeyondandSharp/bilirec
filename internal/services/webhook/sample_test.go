package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bilirec/bilirec/internal/modules/config"
)

func TestSendSampleFullPayload(t *testing.T) {
	var got envelope
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("invalid payload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	svc := newWebhookService(t, &config.Config{WebhookURLs: srv.URL})
	report, err := svc.SendSample(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !report.Sent || report.EventType != EventFileClosed || report.EventID == "" {
		t.Fatalf("report = %+v", report)
	}
	if len(report.Results) != 1 || !report.Results[0].OK || report.Results[0].Status != http.StatusOK {
		t.Fatalf("results = %+v", report.Results)
	}
	if got.EventType != EventFileClosed || got.EventID != report.EventID {
		t.Fatalf("envelope = %+v", got)
	}
	data, err := json.Marshal(got.EventData)
	if err != nil {
		t.Fatal(err)
	}
	var parsed roomEventData
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.SessionID == "" || parsed.RoomID == 0 || parsed.Name == "" || parsed.Title == "" ||
		parsed.RelativePath == "" || parsed.FileOpenTime == "" || parsed.FileCloseTime == "" ||
		parsed.FileSize == 0 || parsed.Duration == 0 || !parsed.Recording || !parsed.Streaming || !parsed.DanmakuConnected {
		t.Fatalf("sample data incomplete: %+v", parsed)
	}
}

func TestSendSampleReportsFailureAndNotConfigured(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	svc := newWebhookService(t, &config.Config{WebhookURLs: srv.URL})
	report, err := svc.SendSample(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 1 || report.Results[0].OK || report.Results[0].Status != http.StatusInternalServerError {
		t.Fatalf("failure report = %+v", report.Results)
	}

	disabled := newWebhookService(t, &config.Config{})
	if _, err := disabled.SendSample(context.Background()); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("expected ErrNotConfigured, got %v", err)
	}
}
