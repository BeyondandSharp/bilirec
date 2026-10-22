package configfile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readJSON(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return out
}

// newTestStore creates an isolated store without touching the process env.
func newTestStore(t *testing.T, configJSON, secretsJSON, dotenv string) (*Store, Options) {
	t.Helper()
	dir := t.TempDir()
	opts := Options{
		ConfigPath:  filepath.Join(dir, "config.json"),
		SecretsPath: filepath.Join(dir, "secrets.json"),
		EnvPath:     filepath.Join(dir, ".env.local"),
	}
	if configJSON != "" {
		writeFile(t, opts.ConfigPath, configJSON)
	}
	if secretsJSON != "" {
		writeFile(t, opts.SecretsPath, secretsJSON)
	}
	if dotenv != "" {
		writeFile(t, opts.EnvPath, dotenv)
	}
	store, err := LoadStore(opts)
	if err != nil {
		t.Fatal(err)
	}
	return store, opts
}

func TestLoadStoreParsesConfigAndSecrets(t *testing.T) {
	store, _ := newTestStore(t,
		`{"HOST":"0.0.0.0","PORT":8081,"DEBUG":true,"FOO":"bar","NULLED":null}`,
		`{"JWT_SECRET":"s3cret","UNKNOWN_SECRET_FILE_KEY":"kept"}`,
		"HOST=dotenv-host\nDOTENV_ONLY=dotenv-value\n")
	if got, ok := store.ConfigValue("HOST"); !ok || got != "0.0.0.0" {
		t.Fatalf("HOST = %q ok=%v", got, ok)
	}
	// numbers/bools are coerced to strings.
	if got, _ := store.ConfigValue("PORT"); got != "8081" {
		t.Fatalf("PORT = %q", got)
	}
	if got, _ := store.ConfigValue("DEBUG"); got != "true" {
		t.Fatalf("DEBUG = %q", got)
	}
	if _, ok := store.ConfigValue("NULLED"); ok {
		t.Fatal("null must delete the key")
	}
	if _, ok := store.ConfigValue("UNKNOWN_SECRET_FILE_KEY"); ok {
		t.Fatal("secret file keys must not leak into config values")
	}
	if !store.SecretIsSet("JWT_SECRET") {
		t.Fatal("JWT_SECRET should be set")
	}
	// Unknown keys are preserved on save.
	if err := store.SetConfig(map[string]string{"HOST": "127.0.0.1"}, nil); err != nil {
		t.Fatal(err)
	}
	values := readJSON(t, store.ConfigPath())
	if values["FOO"] != "bar" {
		t.Fatalf("unknown key FOO lost: %v", values)
	}
}

func TestLoadAndApplyPrecedence(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	secretsPath := filepath.Join(dir, "secrets.json")
	envPath := filepath.Join(dir, ".env.local")
	writeFile(t, cfgPath, `{"T17_FILE_ONLY":"file","T17_BOTH":"file","PASSWORD":"from-config"}`)
	writeFile(t, secretsPath, `{"JWT_SECRET":"from-secrets"}`)
	writeFile(t, envPath, "T17_DOTENV_ONLY=dotenv\nT17_BOTH=dotenv\n")

	keys := []string{"T17_FILE_ONLY", "T17_DOTENV_ONLY", "T17_BOTH", "PASSWORD", "JWT_SECRET", "T17_ENV_ONLY"}
	for _, key := range keys {
		_ = os.Unsetenv(key)
		t.Cleanup(func() { _ = os.Unsetenv(key) })
	}
	t.Setenv("CONFIG_FILE", cfgPath)
	t.Setenv("SECRETS_FILE", secretsPath)
	t.Setenv("ENV_FILE", envPath)
	t.Setenv("T17_ENV_ONLY", "env")

	store, err := LoadAndApply()
	if err != nil {
		t.Fatal(err)
	}
	// file > dotenv; env > file; dotenv when neither.
	if got := os.Getenv("T17_FILE_ONLY"); got != "file" {
		t.Fatalf("FILE_ONLY = %q", got)
	}
	if got := os.Getenv("T17_DOTENV_ONLY"); got != "dotenv" {
		t.Fatalf("DOTENV_ONLY = %q", got)
	}
	if got := os.Getenv("T17_BOTH"); got != "file" {
		t.Fatalf("BOTH = %q (config must beat dotenv)", got)
	}
	if got := os.Getenv("T17_ENV_ONLY"); got != "env" {
		t.Fatalf("ENV_ONLY = %q", got)
	}
	if got := store.Source("T17_FILE_ONLY"); got != SourceFile {
		t.Fatalf("FILE_ONLY source = %q", got)
	}
	if got := store.Source("T17_DOTENV_ONLY"); got != SourceDotEnv {
		t.Fatalf("DOTENV_ONLY source = %q", got)
	}
	if got := store.Source("T17_ENV_ONLY"); got != SourceEnv {
		t.Fatalf("ENV_ONLY source = %q", got)
	}
	if !store.IsEnvLocked("T17_ENV_ONLY") {
		t.Fatal("env-provided key must be locked")
	}
	// Secret key in config.json is migrated to secrets.json and applied from
	// the secrets source; it never stays in config.json.
	if got := os.Getenv("JWT_SECRET"); got != "from-secrets" {
		t.Fatalf("JWT_SECRET = %q", got)
	}
	if got := store.Source("JWT_SECRET"); got != SourceSecret {
		t.Fatalf("JWT_SECRET source = %q", got)
	}
	configValues := readJSON(t, cfgPath)
	if _, ok := configValues["PASSWORD"]; ok {
		t.Fatalf("PASSWORD must be migrated out of config.json: %v", configValues)
	}
	secretsValues := readJSON(t, secretsPath)
	if secretsValues["PASSWORD"] != "from-config" {
		t.Fatalf("PASSWORD not migrated into secrets.json: %v", secretsValues)
	}
	if got := os.Getenv("PASSWORD"); got != "from-config" {
		t.Fatalf("PASSWORD applied = %q", got)
	}
}

func TestFileModesAtomicWriteAndTightening(t *testing.T) {
	store, _ := newTestStore(t, `{"HOST":"1.2.3.4"}`, `{}`, "")
	if err := store.SetConfig(map[string]string{"PORT": "9099"}, nil); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(store.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("config mode = %#o, want 0644", got)
	}
	if err := store.SetSecrets(map[string]string{"JWT_SECRET": "abc"}, nil); err != nil {
		t.Fatal(err)
	}
	info, err = os.Stat(store.SecretsPath())
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("secrets mode = %#o, want 0600", got)
	}
	// Too-broad permissions are tightened on the next write.
	if err := os.Chmod(store.SecretsPath(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSecrets(map[string]string{"JWT_SECRET": "xyz"}, nil); err != nil {
		t.Fatal(err)
	}
	info, err = os.Stat(store.SecretsPath())
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("secrets mode after tightening = %#o", got)
	}
	// Atomic writes leave no temp files behind.
	entries, err := os.ReadDir(filepath.Dir(store.ConfigPath()))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".tmp" {
			t.Fatalf("temporary file left behind: %s", entry.Name())
		}
	}
}

func TestMalformedFilesAreIgnored(t *testing.T) {
	store, _ := newTestStore(t, `{not json`, `{"JWT_SECRET":"ok"}`, "")
	if got := len(store.ConfigKeys()); got != 0 {
		t.Fatalf("malformed config should load empty, got %d keys", got)
	}
	if !store.SecretIsSet("JWT_SECRET") {
		t.Fatal("valid secrets file should still load")
	}
}

func TestRestartRequiredAndFeatureKeys(t *testing.T) {
	store, _ := newTestStore(t, `{}`, `{}`, "")
	if err := store.SetConfig(map[string]string{"PORT": "8081", "SEGMENT_MODE": "time"}, nil); err != nil {
		t.Fatal(err)
	}
	pending := store.RestartRequired()
	if len(pending) != 1 || pending[0] != "PORT" {
		t.Fatalf("restart_required = %v, want [PORT]", pending)
	}
	if err := store.ResetConfig([]string{"PORT", "SEGMENT_MODE"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.ConfigValue("PORT"); ok {
		t.Fatal("PORT should be cleared")
	}
}

func TestUnknownManagedKeyRejected(t *testing.T) {
	store, _ := newTestStore(t, `{}`, `{}`, "")
	if err := store.SetConfig(map[string]string{"CONFIG_FILE": "x"}, nil); err == nil {
		t.Fatal("bootstrap keys must be rejected")
	}
	if err := store.SetConfig(map[string]string{"PASSWORD": "x"}, nil); err == nil {
		t.Fatal("secret keys must be rejected by config persistence")
	}
	if err := store.SetSecrets(map[string]string{"HOST": "x"}, nil); err == nil {
		t.Fatal("non-secret keys must be rejected by secrets persistence")
	}
}

func TestConfigChangeListenerAndHotKeys(t *testing.T) {
	store, _ := newTestStore(t, `{}`, `{}`, "")
	var changes []ConfigChange
	store.OnConfigChange(func(change ConfigChange) {
		changes = append(changes, change)
	})
	if err := store.SetConfig(map[string]string{
		"PORT":              "8081",
		"WEBHOOK_URLS":      "https://example.com/a",
		"FRONTEND_URLS":     "https://app.example.com",
		"LOCAL_LOGS_GROUPS": "live,access",
		"SEGMENT_MODE":      "time",
	}, nil); err != nil {
		t.Fatal(err)
	}
	if len(changes) != 5 {
		t.Fatalf("changes = %+v", changes)
	}
	pending := store.RestartRequired()
	for _, key := range pending {
		if key == "WEBHOOK_URLS" || key == "FRONTEND_URLS" || key == "LOCAL_LOGS_GROUPS" || key == "SEGMENT_MODE" {
			t.Fatalf("hot key %s unexpectedly marked restart-required", key)
		}
	}
	if err := store.ResetConfig([]string{"WEBHOOK_URLS"}); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, change := range changes {
		if change.Key == "WEBHOOK_URLS" && !change.Present {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing clear notification: %+v", changes)
	}
}
