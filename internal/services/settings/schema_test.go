package settings

import (
	"strconv"
	"strings"
	"testing"

	"github.com/bilirec/bilirec/internal/modules/configfile"
)

func TestSchemaCompleteness(t *testing.T) {
	keys := ManagedKeys()
	if len(keys) != 130 {
		t.Logf("managed keys = %d (expected ~128; update-check keys included)", len(keys))
	}
	seen := map[string]struct{}{}
	for _, key := range keys {
		if _, dup := seen[key]; dup {
			t.Fatalf("duplicate schema key %s", key)
		}
		seen[key] = struct{}{}
		if configfile.IsBootstrapKey(key) {
			t.Fatalf("bootstrap key %s must not be managed", key)
		}
		if configfile.IsSecretKey(key) {
			t.Fatalf("secret key %s must not appear in /config schema", key)
		}
		if key == "CI" || key == "KEEP_TEST_FILES" || key == "BILIREC_WRITE_SIM_ARTIFACTS" {
			t.Fatalf("test-only key %s must not be managed", key)
		}
		field, ok := FieldByKey(key)
		if !ok {
			t.Fatalf("FieldByKey(%s) missing", key)
		}
		if field.Label.ZhCN == "" || field.Label.ZhTW == "" || field.Label.En == "" {
			t.Fatalf("field %s is missing a 3-language label: %+v", key, field.Label)
		}
		if field.Description.ZhCN == "" || field.Description.ZhTW == "" || field.Description.En == "" {
			t.Fatalf("field %s is missing a 3-language description", key)
		}
		switch field.Kind {
		case "bool", "number", "size", "string", "textarea", "enum", "csv", "duration":
		default:
			t.Fatalf("field %s has unexpected kind %q", key, field.Kind)
		}
		if field.Kind == "enum" && len(field.Enum) == 0 {
			t.Fatalf("enum field %s has no values", key)
		}
		if field.Kind == "number" && field.Default != "" {
			if _, err := strconv.ParseInt(field.Default, 10, 64); err != nil {
				t.Fatalf("number field %s default %q is not an integer", key, field.Default)
			}
		}
		if field.Kind == "size" && field.Default != "" {
			if _, err := parseSizeValue(field.Default, field.SizeUnit); err != nil {
				t.Fatalf("size field %s default %q invalid: %v", key, field.Default, err)
			}
		}
		if field.Kind == "bool" && field.Default != "" && field.Default != "true" && field.Default != "false" {
			t.Fatalf("bool field %s default %q invalid", key, field.Default)
		}
		if field.Min != nil && field.Max != nil && *field.Min > *field.Max {
			t.Fatalf("field %s min > max", key)
		}
	}
	// The JSON schema returned by GET /config must not contain a features group:
	// FEATURES-V2 keys are rendered by /settings and rejected by PUT /config.
	featureSet := make(map[string]struct{})
	for _, key := range configfile.FeatureKeys {
		featureSet[key] = struct{}{}
	}
	for _, group := range ManagedSchema().Groups {
		if group.ID == "features" {
			t.Fatal("ManagedSchema must not include the features group")
		}
		for _, field := range group.Fields {
			if _, isFeature := featureSet[field.Key]; isFeature {
				t.Fatalf("feature key %s leaked into /config schema group %s", field.Key, group.ID)
			}
		}
	}
	// Generic (non-feature) fields carry curated copy: no raw env-key fragments.
	for _, key := range ManagedKeys() {
		if _, isFeature := featureSet[key]; isFeature {
			continue
		}
		field, _ := FieldByKey(key)
		for _, label := range []string{field.Label.ZhCN, field.Label.ZhTW, field.Label.En} {
			if label == key || strings.Contains(label, "_") {
				t.Fatalf("field %s has a raw env-key label %q", key, label)
			}
			// Raw env-key fragments show up as long ALL-CAPS runs
			// (e.g. "Cloudconvert" / "RECORDTYPES"); acronyms <=5 chars
			// (CDN, API, MP4, FLV, FRP, HLS, JWT, SSE, TLS) are fine.
			if containsLongUpperRun(label, 6) {
				t.Fatalf("field %s has a raw-key-style label %q", key, label)
			}
		}
	}
	// Acronyms/units must be rendered correctly.
	for key, want := range map[string]string{
		"CLOUDCONVERT_THRESHOLD": "CloudConvert",
		"FFMPEG_BIN":             "FFmpeg",
		"FLV_REPAIR":             "FLV",
		"FRP_ENABLED":            "FRP",
		"METRICS_ENABLED":        "Prometheus",
		"GITHUB_API_URL":         "GitHub",
	} {
		field, _ := FieldByKey(key)
		if !strings.Contains(field.Label.En, want) {
			t.Fatalf("%s en label %q missing %q", key, field.Label.En, want)
		}
	}
	for key, want := range map[string][2]string{
		"TZ":   {"时区", "時區"},
		"PORT": {"监听端口", "監聽連接埠"},
	} {
		field, _ := FieldByKey(key)
		if field.Label.ZhCN != want[0] {
			t.Fatalf("%s zh-CN label = %q, want %q", key, field.Label.ZhCN, want[0])
		}
		if field.Label.ZhTW != want[1] {
			t.Fatalf("%s zh-TW label = %q, want %q", key, field.Label.ZhTW, want[1])
		}
	}
	// FEATURES-V2 keys stay owned by /settings and are also listed in the schema.
	for _, key := range configfile.FeatureKeys {
		if _, ok := seen[key]; !ok {
			t.Fatalf("feature key %s missing from schema", key)
		}
		field, _ := FieldByKey(key)
		if field.Group != "features" {
			t.Fatalf("feature key %s group = %q", key, field.Group)
		}
		if !field.RestartRequired {
			continue
		}
		t.Fatalf("feature key %s must hot-apply (restart_required=false)", key)
	}
	// Default filename template is the CONFIG-V2 §3 value.
	field, _ := FieldByKey("FILENAME_TEMPLATE")
	if field.Default != "{roomid}-{uname}/{date}_{time}_{title}" {
		t.Fatalf("FILENAME_TEMPLATE default = %q", field.Default)
	}
	// Curated labels must not change the defaults (CONFIG-V2 §3 / T-27).
	for key, wantDefault := range map[string]string{
		"PORT":                   "8080",
		"METRICS_PORT":           "9090",
		"FFMPEG_BIN":             "ffmpeg",
		"OUTPUT_DIR":             "records",
		"CLOUDCONVERT_THRESHOLD": "1073741824",
		"TZ":                     "Asia/Hong_Kong",
	} {
		field, _ := FieldByKey(key)
		if field.Default != wantDefault {
			t.Fatalf("%s default = %q, want %q", key, field.Default, wantDefault)
		}
	}
	// Update-check keys coordinated with go-bili (T-B).
	for key, wantDefault := range map[string]string{
		"CHECK_UPDATE":             "true",
		"UPDATE_CHECK_REPO":        "BeyondandSharp/bilirec",
		"UPDATE_CHECK_RELEASE_URL": "https://github.com/BeyondandSharp/bilirec/releases/latest",
		"UPDATE_CHECK_API_URL":     "",
		"GITHUB_API_URL":           "https://api.github.com",
	} {
		f, ok := FieldByKey(key)
		if !ok {
			t.Fatalf("update key %s missing from schema", key)
		}
		if f.Group != "update" {
			t.Fatalf("update key %s group = %q", key, f.Group)
		}
		if f.Default != wantDefault {
			t.Fatalf("update key %s default = %q, want %q", key, f.Default, wantDefault)
		}
	}
}

func TestSecretSchema(t *testing.T) {
	keys := SecretSchemaKeys()
	if len(keys) != len(configfile.SecretKeys) {
		t.Fatalf("secret schema keys = %v, want %v", keys, configfile.SecretKeys)
	}
	for i, key := range keys {
		if key != configfile.SecretKeys[i] {
			t.Fatalf("secret key order/content mismatch: %v vs %v", keys, configfile.SecretKeys)
		}
		field, ok := FieldByKey(key)
		if !ok || !field.Sensitive || field.Kind != "secret" {
			t.Fatalf("secret field %s invalid: %+v", key, field)
		}
		if field.Label.En == "" {
			t.Fatalf("secret field %s missing label", key)
		}
	}
}

func TestValidateFieldValue(t *testing.T) {
	port, _ := FieldByKey("PORT")
	if err := ValidateFieldValue(port, "8080"); err != nil {
		t.Fatalf("valid port rejected: %v", err)
	}
	if err := ValidateFieldValue(port, "70000"); err == nil {
		t.Fatal("out-of-range port accepted")
	}
	if err := ValidateFieldValue(port, "abc"); err == nil {
		t.Fatal("non-numeric port accepted")
	}
	enabled, _ := FieldByKey("METRICS_ENABLED")
	if err := ValidateFieldValue(enabled, "yes"); err == nil {
		t.Fatal("non-bool bool value accepted")
	}
	mode, _ := FieldByKey("DOWNLOADER")
	if err := ValidateFieldValue(mode, "auto"); err != nil {
		t.Fatalf("valid enum rejected: %v", err)
	}
	if err := ValidateFieldValue(mode, "bogus"); err == nil {
		t.Fatal("invalid enum accepted")
	}
	duration, _ := FieldByKey("LOCAL_LOGS_FLUSH_INTERVAL")
	if err := ValidateFieldValue(duration, "1s"); err != nil {
		t.Fatalf("valid duration rejected: %v", err)
	}
	if err := ValidateFieldValue(duration, "soon"); err == nil {
		t.Fatal("invalid duration accepted")
	}
	template, _ := FieldByKey("FILENAME_TEMPLATE")
	if err := ValidateFieldValue(template, "{roomid}-{uname}/{date}"); err != nil {
		t.Fatalf("valid template rejected: %v", err)
	}
	if err := ValidateFieldValue(template, "../escape"); err == nil {
		t.Fatal("traversal template accepted")
	}
}

// containsLongUpperRun reports whether s contains a run of n or more uppercase
// ASCII letters (a raw env-key fragment like CLOUDCONVERT/RECORDTYPES).
func containsLongUpperRun(s string, n int) bool {
	run := 0
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			run++
			if run >= n {
				return true
			}
			continue
		}
		run = 0
	}
	return false
}

func TestSizeFieldValidation(t *testing.T) {
	field, ok := FieldByKey("MIN_DISK_SPACE_BYTES")
	if !ok || field.Kind != "size" || field.SizeUnit != "B" {
		t.Fatalf("MIN_DISK_SPACE_BYTES field = %+v", field)
	}
	for _, value := range []string{"1 B", "512 KiB", "5 MiB", "2 GiB", "1 TiB", "1 PiB", "5368709120"} {
		if err := ValidateFieldValue(field, value); err != nil {
			t.Errorf("valid size %q rejected: %v", value, err)
		}
	}
	if err := ValidateFieldValue(field, "5 XB"); err == nil {
		t.Fatal("invalid size unit accepted")
	}
}
