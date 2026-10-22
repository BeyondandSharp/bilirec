// SPDX-License-Identifier: GPL-3.0-only

package webhook_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	whc "github.com/bilirec/bilirec/internal/controllers/webhook"
	"github.com/bilirec/bilirec/internal/modules/config"
	wh "github.com/bilirec/bilirec/internal/services/webhook"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

func TestWebhookTestEndpointSendsSample(t *testing.T) {
	old := config.ReadOnly
	config.ReadOnly = config.NewGlobalReadOnlyForTest(false, 0)
	t.Cleanup(func() { config.ReadOnly = old })

	var received bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("invalid body: %v", err)
		}
		if body["EventType"] != "FileClosed" {
			t.Errorf("event type = %v", body["EventType"])
		}
		received = true
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	app := fiber.New()
	var svc *wh.Service
	lifecycle := fxtest.New(t,
		fx.Provide(func() *config.Config {
			return &config.Config{WebhookURLs: target.URL}
		}),
		fx.Provide(wh.NewService),
		fx.Populate(&svc),
	)
	lifecycle.RequireStart()
	t.Cleanup(lifecycle.RequireStop)
	whc.NewController(app, svc)

	resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/webhook/test", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var report wh.TestReport
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
		t.Fatal(err)
	}
	if !report.Sent || report.EventType != wh.EventFileClosed || len(report.Results) != 1 || !report.Results[0].OK {
		t.Fatalf("report = %+v", report)
	}
	if !received {
		t.Fatal("target did not receive sample webhook")
	}
}
