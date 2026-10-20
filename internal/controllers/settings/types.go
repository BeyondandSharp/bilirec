package settings

import "github.com/bilirec/bilirec/internal/services/settings"

// SettingsResponse is the GET/PUT /settings payload (docs/FEATURES-V2.md §2).
type SettingsResponse struct {
	Defaults  settings.GlobalSettings `json:"defaults"`
	Overrides settings.GlobalSettings `json:"overrides"`
	Values    settings.GlobalSettings `json:"values"`
}
