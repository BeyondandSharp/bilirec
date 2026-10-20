package settings

import "github.com/bilirec/bilirec/internal/services/settings"

// SettingsResponse is the GET/PUT /settings payload (docs/FEATURES-V2.md §2).
type SettingsResponse struct {
	Defaults  settings.GlobalSettings `json:"defaults"`
	Overrides settings.GlobalSettings `json:"overrides"`
	Values    settings.GlobalSettings `json:"values"`
	// Additive CONFIG-V2 §4 fields.
	Sources         map[string]string `json:"sources"`
	ConfigFile      string            `json:"config_file"`
	SecretsFile     string            `json:"secrets_file"`
	RestartRequired []string          `json:"restart_required"`
}
