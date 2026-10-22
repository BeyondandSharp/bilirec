package restart

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bilirec/bilirec/internal/modules/config"
	restartsvc "github.com/bilirec/bilirec/internal/services/restart"
	"github.com/gofiber/fiber/v3"
)

func TestRestartEndpoint(t *testing.T) {
	old := config.ReadOnly
	config.ReadOnly = config.NewGlobalReadOnlyForTest(false, 0)
	t.Cleanup(func() { config.ReadOnly = old })
	restartsvc.ResetForTest()
	t.Cleanup(restartsvc.ResetForTest)

	app := fiber.New()
	NewController(app)

	post := func() int {
		req := httptest.NewRequest(http.MethodPost, "/system/restart", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	if got := post(); got != http.StatusNotImplemented {
		t.Fatalf("disabled status = %d", got)
	}
	restartsvc.Enable()
	if got := post(); got != http.StatusAccepted {
		t.Fatalf("accepted status = %d", got)
	}
	if got := post(); got != http.StatusConflict {
		t.Fatalf("duplicate status = %d", got)
	}
}
