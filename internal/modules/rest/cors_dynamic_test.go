package rest

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
)

func TestDynamicCORSHotUpdatesOrigins(t *testing.T) {
	old := activeCORSOrigins.Load()
	t.Cleanup(func() { activeCORSOrigins.Store(old) })
	activeCORSOrigins.Store(newCORSOrigins(&config.Config{
		FrontendURLs:   []string{"https://old.example.com"},
		ProductionMode: true,
	}))

	app := fiber.New()
	app.Use(cors.New(dynamicCORSConfig()))
	app.Get("/version", func(c fiber.Ctx) error { return c.SendStatus(http.StatusOK) })

	preflight := func(origin string) string {
		req := httptest.NewRequest(http.MethodOptions, "/version", nil)
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Method", "GET")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.Header.Get("Access-Control-Allow-Origin")
	}

	if got := preflight("https://old.example.com"); got != "https://old.example.com" {
		t.Fatalf("old origin ACAO = %q", got)
	}
	activeCORSOrigins.Load().set([]string{"https://new.example.com"}, true)
	if got := preflight("https://new.example.com"); got != "https://new.example.com" {
		t.Fatalf("new origin ACAO = %q", got)
	}
	if got := preflight("https://old.example.com"); got != "" {
		t.Fatalf("old origin should be rejected, got %q", got)
	}
}
