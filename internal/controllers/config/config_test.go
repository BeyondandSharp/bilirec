package config

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/modules/configfile"
	settingssvc "github.com/bilirec/bilirec/internal/services/settings"
	"github.com/gofiber/fiber/v3"
)

func withNoAuth(t *testing.T) {
	t.Helper()
	old := config.ReadOnly
	config.ReadOnly = config.NewGlobalReadOnlyForTest(false, 0)
	t.Cleanup(func() { config.ReadOnly = old })
}

func newTestController(t *testing.T) (*fiber.App, *configfile.Store) {
	t.Helper()
	dir := t.TempDir()
	store, err := configfile.LoadStore(configfile.Options{
		ConfigPath:  filepath.Join(dir, "config.json"),
		SecretsPath: filepath.Join(dir, "secrets.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	NewController(app, store, nil)
	return app, store
}

func request(t *testing.T, app *fiber.App, method, path, body string, wantStatus int) []byte {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s: status = %d, want %d (body %s)", method, path, resp.StatusCode, wantStatus, raw)
	}
	return raw
}

func decodeConfig(t *testing.T, raw []byte) ConfigResponse {
	t.Helper()
	var resp ConfigResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("invalid /config response: %v (%s)", err, raw)
	}
	return resp
}

func TestConfigGetIncludesSchemaValuesSources(t *testing.T) {
	withNoAuth(t)
	app, store := newTestController(t)
	resp := decodeConfig(t, request(t, app, http.MethodGet, "/config", "", http.StatusOK))
	if resp.ConfigFile != store.ConfigPath() || resp.SecretsFile != store.SecretsPath() {
		t.Fatalf("paths = %q %q", resp.ConfigFile, resp.SecretsFile)
	}
	if len(resp.Schema.Groups) < 10 {
		t.Fatalf("schema groups = %d", len(resp.Schema.Groups))
	}
	for _, group := range resp.Schema.Groups {
		if group.ID == "features" {
			t.Fatal("GET /config schema must not include the features group")
		}
		for _, field := range group.Fields {
			for _, featureKey := range featureSchemaKeysForTest() {
				if field.Key == featureKey {
					t.Fatalf("feature key %s leaked into /config schema", field.Key)
				}
			}
		}
	}
	if _, ok := resp.Values["MAX_RECOVERY_ATTEMPTS"]; !ok {
		t.Fatal("values should include MAX_RECOVERY_ATTEMPTS")
	}
	if _, ok := resp.Values["JWT_SECRET"]; ok {
		t.Fatal("/config must never expose secret values")
	}
	if _, ok := resp.Values["SEGMENT_MODE"]; ok {
		t.Fatal("FEATURES-V2 keys stay in /settings, not /config values")
	}
	if got := resp.Sources["MAX_RECOVERY_ATTEMPTS"]; got != "default" {
		t.Fatalf("source = %q, want default", got)
	}
	// /config values/sources exclude FEATURES-V2 and secret keys.
	for key := range resp.Sources {
		for _, featureKey := range featureSchemaKeysForTest() {
			if key == featureKey {
				t.Fatalf("feature key %s leaked into /config sources", key)
			}
		}
		for _, secretKey := range configfile.SecretKeys {
			if key == secretKey {
				t.Fatalf("secret key %s leaked into /config sources", key)
			}
		}
	}
	// requires:"ffmpeg" fields are disabled when no convert provider exists.
	for _, group := range resp.Schema.Groups {
		for _, field := range group.Fields {
			if field.Key == "CONVERT_TO_MP4" && field.DisabledReason == "" {
				t.Fatal("CONVERT_TO_MP4 should carry a disabled_reason without providers")
			}
		}
	}
}

func TestConfigPutRoundTripAndRestartRequired(t *testing.T) {
	withNoAuth(t)
	app, store := newTestController(t)
	raw := request(t, app, http.MethodPut, "/config", `{"values":{"MAX_RECOVERY_ATTEMPTS":"7"}}`, http.StatusOK)
	resp := decodeConfig(t, raw)
	if resp.Values["MAX_RECOVERY_ATTEMPTS"] != "7" {
		t.Fatalf("value = %q", resp.Values["MAX_RECOVERY_ATTEMPTS"])
	}
	if resp.Sources["MAX_RECOVERY_ATTEMPTS"] != "file" {
		t.Fatalf("source = %q", resp.Sources["MAX_RECOVERY_ATTEMPTS"])
	}
	restart := false
	for _, key := range resp.RestartRequired {
		if key == "MAX_RECOVERY_ATTEMPTS" {
			restart = true
		}
	}
	if !restart {
		t.Fatalf("restart_required = %v", resp.RestartRequired)
	}
	if value, ok := store.ConfigValue("MAX_RECOVERY_ATTEMPTS"); !ok || value != "7" {
		t.Fatalf("persisted value = %q ok=%v", value, ok)
	}

	raw = request(t, app, http.MethodPut, "/config", `{"clear":["MAX_RECOVERY_ATTEMPTS"]}`, http.StatusOK)
	resp = decodeConfig(t, raw)
	if _, ok := resp.Overrides["MAX_RECOVERY_ATTEMPTS"]; ok {
		t.Fatal("clear should remove the override")
	}
}

func TestConfigPutRejectsInvalid(t *testing.T) {
	withNoAuth(t)
	t.Setenv("HOST", "0.0.0.0")
	app, _ := newTestController(t)

	cases := []struct {
		name string
		body string
	}{
		{"unknown key", `{"values":{"NOT_A_KEY":"x"}}`},
		{"bootstrap key", `{"values":{"CONFIG_FILE":"x"}}`},
		{"secret key", `{"values":{"JWT_SECRET":"x"}}`},
		{"feature key", `{"values":{"SEGMENT_MODE":"time"}}`},
		{"env locked", `{"values":{"HOST":"127.0.0.1"}}`},
		{"invalid number", `{"values":{"MAX_RECOVERY_ATTEMPTS":"abc"}}`},
		{"out of range", `{"values":{"MAX_RECOVERY_ATTEMPTS":"-5"}}`},
		{"invalid enum", `{"values":{"DOWNLOADER":"bogus"}}`},
		{"invalid bool", `{"values":{"METRICS_ENABLED":"yes"}}`},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			request(t, app, http.MethodPut, "/config", tt.body, http.StatusBadRequest)
		})
	}
}

func TestConfigResetPreservesUnknownAndFeatureKeys(t *testing.T) {
	withNoAuth(t)
	app, store := newTestController(t)
	// Simulate unknown keys and a feature key already stored in config.json.
	if err := store.SetConfig(map[string]string{"SOMETHING_UNKNOWN": "keep", "SEGMENT_MODE": "time"}, nil); err != nil {
		t.Fatal(err)
	}
	request(t, app, http.MethodPost, "/config/reset", "", http.StatusOK)
	if value, ok := store.ConfigValue("SOMETHING_UNKNOWN"); !ok || value != "keep" {
		t.Fatalf("unknown key lost after reset: %q ok=%v", value, ok)
	}
	if value, ok := store.ConfigValue("SEGMENT_MODE"); !ok || value != "time" {
		t.Fatalf("feature key must not be reset by /config/reset: %q ok=%v", value, ok)
	}
}

func TestSecretsEndpointsMaskAndPersist(t *testing.T) {
	withNoAuth(t)
	app, store := newTestController(t)

	raw := request(t, app, http.MethodGet, "/config/secrets", "", http.StatusOK)
	var state SecretsResponse
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Set) != len(configfile.SecretKeys) {
		t.Fatalf("secret keys = %v", state.Set)
	}
	if len(state.Schema.Groups) != 1 || state.Schema.Groups[0].ID != "secrets" {
		t.Fatalf("secrets schema groups = %+v", state.Schema.Groups)
	}
	if len(state.Schema.Groups[0].Fields) != len(configfile.SecretKeys) {
		t.Fatalf("secrets schema fields = %d", len(state.Schema.Groups[0].Fields))
	}
	for _, field := range state.Schema.Groups[0].Fields {
		if !field.Sensitive || field.Kind != "secret" {
			t.Fatalf("bad secret schema field %+v", field)
		}
	}
	for key, set := range state.Set {
		if set {
			t.Fatalf("%s should be unset initially", key)
		}
	}

	raw = request(t, app, http.MethodPut, "/config/secrets", `{"values":{"JWT_SECRET":"highly-secret"}}`, http.StatusOK)
	if strings.Contains(string(raw), "highly-secret") {
		t.Fatalf("secret value leaked in response: %s", raw)
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	if !state.Set["JWT_SECRET"] {
		t.Fatal("JWT_SECRET should be set")
	}
	info, err := os.Stat(store.SecretsPath())
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("secrets file mode = %#o", got)
	}
	// GET again still never returns the value.
	raw = request(t, app, http.MethodGet, "/config/secrets", "", http.StatusOK)
	if strings.Contains(string(raw), "highly-secret") {
		t.Fatalf("secret value leaked via GET: %s", raw)
	}

	request(t, app, http.MethodPost, "/config/secrets/reset", "", http.StatusOK)
	if store.SecretIsSet("JWT_SECRET") {
		t.Fatal("secret should be cleared by reset")
	}

	// Unknown / non-secret keys are rejected.
	request(t, app, http.MethodPut, "/config/secrets", `{"values":{"HOST":"x"}}`, http.StatusBadRequest)
	request(t, app, http.MethodPut, "/config/secrets", `{"values":{"UNKNOWN":"x"}}`, http.StatusBadRequest)
}

func TestConfigResetAndSecretsResetRequireAdmin(t *testing.T) {
	withNoAuth(t)
	app, _ := newTestController(t)
	// Viewer/admin enforcement is covered by the RBAC middleware tests; with
	// auth disabled the handlers are reachable, so just check the routes exist.
	request(t, app, http.MethodPost, "/config/reset", "", http.StatusOK)
	request(t, app, http.MethodPost, "/config/secrets/reset", "", http.StatusOK)
}

func featureSchemaKeysForTest() []string {
	return settingssvc.FeatureSchemaKeys()
}
