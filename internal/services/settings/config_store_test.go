package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/modules/configfile"
	"github.com/bilirec/bilirec/pkg/db"
	"go.uber.org/fx/fxtest"
)

func newStoreForTest(t *testing.T) *configfile.Store {
	t.Helper()
	dir := t.TempDir()
	store, err := configfile.LoadStore(configfile.Options{
		ConfigPath:  filepath.Join(dir, "config.json"),
		SecretsPath: filepath.Join(dir, "secrets.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestFeatureOverridesPersistToConfigFile(t *testing.T) {
	clearSettingsEnv(t)
	store := newStoreForTest(t)
	lc := fxtest.NewLifecycle(t)
	svc := newService(lc, &config.Config{DatabaseDir: t.TempDir()}, nil, store)
	lc.RequireStart()
	defer lc.RequireStop()

	minutes := 45
	mode := "time"
	if err := svc.Update(GlobalSettingsPatch{SegmentMinutes: &minutes, SegmentMode: &mode}); err != nil {
		t.Fatal(err)
	}
	_, overrides, values := svc.Get()
	if overrides.SegmentMinutes != 45 || values.SegmentMode != "time" {
		t.Fatalf("in-memory overrides not applied: %+v %+v", overrides, values)
	}
	if svc.ConfigFile() != store.ConfigPath() {
		t.Fatalf("ConfigFile() = %q", svc.ConfigFile())
	}
	// Persisted to config.json (not settings.db).
	data, err := readFileBytes(store.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]string
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["SEGMENT_MINUTES"] != "45" || raw["SEGMENT_MODE"] != "time" {
		t.Fatalf("config.json missing feature overrides: %v", raw)
	}
	sources := svc.Sources()
	if sources["segment_minutes"] != "file" || sources["segment_mode"] != "file" {
		t.Fatalf("sources = %v", sources)
	}
	if len(svc.RestartRequired()) != 0 {
		t.Fatalf("feature changes must hot-apply, restart_required = %v", svc.RestartRequired())
	}

	// A fresh service with the same files loads the overrides back.
	store2, err := configfile.LoadStore(configfile.Options{ConfigPath: store.ConfigPath(), SecretsPath: store.SecretsPath()})
	if err != nil {
		t.Fatal(err)
	}
	lc2 := fxtest.NewLifecycle(t)
	svc2 := newService(lc2, &config.Config{DatabaseDir: t.TempDir()}, nil, store2)
	lc2.RequireStart()
	defer lc2.RequireStop()
	_, overrides2, _ := svc2.Get()
	if overrides2.SegmentMinutes != 45 || overrides2.SegmentMode != "time" {
		t.Fatalf("reloaded overrides = %+v", overrides2)
	}

	// Reset clears the feature keys from config.json.
	if err := svc.Reset(); err != nil {
		t.Fatal(err)
	}
	data, err = readFileBytes(store.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	raw = map[string]string{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["SEGMENT_MINUTES"]; ok {
		t.Fatalf("reset left SEGMENT_MINUTES in config.json: %v", raw)
	}
}

func TestLegacySettingsDBMigratesToConfigFile(t *testing.T) {
	clearSettingsEnv(t)
	dir := t.TempDir()
	databaseDir := filepath.Join(dir, "database")
	client, err := db.Open(filepath.Join(databaseDir, settingsDBFile))
	if err != nil {
		t.Fatal(err)
	}
	bucket, err := client.Bucket(settingsBucket)
	if err != nil {
		t.Fatal(err)
	}
	minutes := 90
	legacy, err := json.Marshal(GlobalSettingsPatch{SegmentMinutes: &minutes})
	if err != nil {
		t.Fatal(err)
	}
	if err := bucket.Put([]byte(settingsGlobalKey), legacy); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}

	store := newStoreForTest(t)
	lc := fxtest.NewLifecycle(t)
	svc := newService(lc, &config.Config{DatabaseDir: databaseDir}, nil, store)
	lc.RequireStart()

	_, overrides, _ := svc.Get()
	if overrides.SegmentMinutes != 90 {
		t.Fatalf("legacy override not migrated: %+v", overrides)
	}
	value, ok := store.ConfigValue("SEGMENT_MINUTES")
	if !ok || value != "90" {
		t.Fatalf("config.json after migration = %q ok=%v", value, ok)
	}
	lc.RequireStop()
	// Idempotent: a second start does not duplicate/overwrite.
	lc2 := fxtest.NewLifecycle(t)
	svc2 := newService(lc2, &config.Config{DatabaseDir: databaseDir}, nil, store)
	lc2.RequireStart()
	defer lc2.RequireStop()
	if _, overrides2, _ := svc2.Get(); overrides2.SegmentMinutes != 90 {
		t.Fatalf("second migration changed overrides: %+v", overrides2)
	}
}

func TestFilenameTemplateExplicitEmptyKeepsLegacy(t *testing.T) {
	clearSettingsEnv(t)
	t.Setenv("FILENAME_TEMPLATE", "")
	if got := loadDefaultsFromEnv().FilenameTemplate; got != "" {
		t.Fatalf("explicit empty FILENAME_TEMPLATE = %q, want legacy empty", got)
	}
}

func readFileBytes(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func TestSettingsRestartRequiredScopedToFeatureKeys(t *testing.T) {
	clearSettingsEnv(t)
	store := newStoreForTest(t)
	lc := fxtest.NewLifecycle(t)
	svc := newService(lc, &config.Config{DatabaseDir: t.TempDir()}, nil, store)
	lc.RequireStart()
	defer lc.RequireStop()

	// A generic env key change needs a restart and is reported by /config…
	if err := store.SetConfig(map[string]string{"PORT": "18099"}, nil); err != nil {
		t.Fatal(err)
	}
	if len(store.RestartRequired()) == 0 {
		t.Fatal("store should report the generic restart-required key for /config")
	}
	// …but /settings' restart_required is scoped to FEATURES-V2 keys only.
	if pending := svc.RestartRequired(); len(pending) != 0 {
		t.Fatalf("/settings restart_required must not include generic env keys: %v", pending)
	}
}

func TestConfigStoreDoesNotCreateLegacySettingsDB(t *testing.T) {
	clearSettingsEnv(t)
	store := newStoreForTest(t)
	dbDir := t.TempDir()
	lc := fxtest.NewLifecycle(t)
	svc := newService(lc, &config.Config{DatabaseDir: dbDir}, nil, store)
	lc.RequireStart()
	lc.RequireStop()
	_ = svc

	if _, err := os.Stat(filepath.Join(dbDir, settingsDBFile)); !os.IsNotExist(err) {
		t.Fatalf("fresh CONFIG-V2 start must not create settings.db, stat err=%v", err)
	}
}

func TestFeatureSizeUnitsPersistAndLoad(t *testing.T) {
	clearSettingsEnv(t)
	store := newStoreForTest(t)
	lc := fxtest.NewLifecycle(t)
	svc := newService(lc, &config.Config{DatabaseDir: t.TempDir()}, nil, store)
	lc.RequireStart()
	defer lc.RequireStop()

	segment := 2048
	minFile := 512
	if err := svc.Update(GlobalSettingsPatch{SegmentSizeMB: &segment, MinFileSizeMB: &minFile}); err != nil {
		t.Fatal(err)
	}
	data, err := readFileBytes(store.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]string
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["SEGMENT_SIZE_MB"] != "2 GiB" || raw["MIN_FILE_SIZE_MB"] != "512 MiB" {
		t.Fatalf("config size units = %q / %q", raw["SEGMENT_SIZE_MB"], raw["MIN_FILE_SIZE_MB"])
	}

	store2, err := configfile.LoadStore(configfile.Options{ConfigPath: store.ConfigPath(), SecretsPath: store.SecretsPath()})
	if err != nil {
		t.Fatal(err)
	}
	lc2 := fxtest.NewLifecycle(t)
	svc2 := newService(lc2, &config.Config{DatabaseDir: t.TempDir()}, nil, store2)
	lc2.RequireStart()
	defer lc2.RequireStop()
	_, _, values := svc2.Get()
	if values.SegmentSizeMB != segment || values.MinFileSizeMB != minFile {
		t.Fatalf("loaded size values = %d / %d", values.SegmentSizeMB, values.MinFileSizeMB)
	}
}
