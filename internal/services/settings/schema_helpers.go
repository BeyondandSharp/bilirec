package settings

import (
	"time"

	"github.com/bilirec/bilirec/internal/services/naming"
)

// parseDurationValue validates Go duration syntax used by schema kind "duration".
func parseDurationValue(value string) (time.Duration, error) {
	return time.ParseDuration(value)
}

// SecretsSchema returns the /config/secrets metadata document. Secret values are
// never part of it; only labels/descriptions and the set/unset state.
func SecretsSchema() Schema {
	return Schema{Groups: []Group{{
		ID: "secrets",
		Label: LangText{
			ZhCN: "敏感配置", ZhTW: "敏感設定", En: "Secrets",
		},
		Description: LangText{
			ZhCN: "仅写入、不回显的敏感配置（保存在 secrets.json，权限 0600）。",
			ZhTW: "僅寫入、不回顯的敏感設定（儲存在 secrets.json，權限 0600）。",
			En:   "Write-only secrets stored in secrets.json with mode 0600.",
		},
		Fields: SecretFields(),
	}}}
}

// validateFilenameTemplateValue applies the FEATURES-V2 §3.2 path-template rules.
func validateFilenameTemplateValue(value string) error {
	return naming.ValidatePathTemplate(value)
}
