package webhook

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
)

func TestAccountAuthFailedPayload(t *testing.T) {
	events := make(chan envelope, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var event envelope
		if err := json.Unmarshal(body, &event); err != nil {
			t.Errorf("invalid payload: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		events <- event
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	svc := newWebhookService(t, &config.Config{WebhookURLs: srv.URL})
	svc.AccountAuthFailed("cookie_refresh", "刷新 Cookie 失败", errors.New("SESSDATA expired"))

	select {
	case event := <-events:
		if event.EventType != EventAccountAuthFailed {
			t.Fatalf("EventType = %q, want %q", event.EventType, EventAccountAuthFailed)
		}
		if event.EventID == "" || event.EventTimestamp == "" {
			t.Fatalf("missing envelope fields: %+v", event)
		}
		data, err := json.Marshal(event.EventData)
		if err != nil {
			t.Fatal(err)
		}
		var parsed accountAuthEventData
		if err := json.Unmarshal(data, &parsed); err != nil {
			t.Fatal(err)
		}
		if parsed.Stage != "cookie_refresh" || parsed.Message != "刷新 Cookie 失败" || parsed.Error != "SESSDATA expired" {
			t.Fatalf("EventData = %+v", parsed)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for AccountAuthFailed webhook")
	}
}
