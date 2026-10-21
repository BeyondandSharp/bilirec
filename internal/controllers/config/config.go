// SPDX-License-Identifier: GPL-3.0-or-later

// Package config exposes the unified configuration API introduced by
// docs/CONFIG-V2.md §4: /config (non-secret env keys) and /config/secrets
// (write-only secret values).
package config

import (
	"strconv"
	"strings"

	"github.com/bilirec/bilirec/internal/modules/configfile"
	"github.com/bilirec/bilirec/internal/modules/rest"
	convertsvc "github.com/bilirec/bilirec/internal/services/convert"
	settingssvc "github.com/bilirec/bilirec/internal/services/settings"
	"github.com/bilirec/bilirec/pkg/logger"
	"github.com/gofiber/fiber/v3"
)

var log = logger.Named("config")

// Controller exposes /config and /config/secrets.
type Controller struct {
	store   *configfile.Store
	convert *convertsvc.Service
}

// NewController registers the config endpoints. GET is available to
// admin+viewer, mutations are admin-only.
func NewController(app *fiber.App, store *configfile.Store, convertSvc *convertsvc.Service) *Controller {
	c := &Controller{store: store, convert: convertSvc}
	app.Get("/config", rest.RequiredRoles(rest.RoleAdmin, rest.RoleViewer), c.getConfig)
	app.Put("/config", rest.AdminOnly, c.updateConfig)
	app.Post("/config/reset", rest.AdminOnly, c.resetConfig)
	app.Get("/config/secrets", rest.RequiredRoles(rest.RoleAdmin, rest.RoleViewer), c.getSecrets)
	app.Put("/config/secrets", rest.AdminOnly, c.updateSecrets)
	app.Post("/config/secrets/reset", rest.AdminOnly, c.resetSecrets)
	return c
}

// ConfigResponse is the GET/PUT /config body (docs/CONFIG-V2.md §4).
type ConfigResponse struct {
	ConfigFile      string             `json:"config_file"`
	SecretsFile     string             `json:"secrets_file"`
	Schema          settingssvc.Schema `json:"schema"`
	Values          map[string]string  `json:"values"`
	Overrides       map[string]string  `json:"overrides"`
	Defaults        map[string]string  `json:"defaults"`
	Sources         map[string]string  `json:"sources"`
	RestartRequired []string           `json:"restart_required"`
}

// ConfigUpdateRequest is the PUT /config body.
type ConfigUpdateRequest struct {
	Values map[string]string `json:"values"`
	Clear  []string          `json:"clear"`
}

// SecretsResponse is the GET /config/secrets body (never values).
type SecretsResponse struct {
	Set map[string]bool `json:"set"`
	// Schema carries the sensitive-field metadata so the UI can label the
	// write-only inputs; values are still never exposed.
	Schema settingssvc.Schema `json:"schema"`
}

// SecretsUpdateRequest is the PUT /config/secrets body.
type SecretsUpdateRequest struct {
	Values map[string]string `json:"values"`
	Clear  []string          `json:"clear"`
}

// configFieldKeys returns managed keys that belong to /config (feature keys are
// edited through /settings and secrets through /config/secrets).
func configFieldKeys() []string {
	keys := make([]string, 0, len(settingssvc.ManagedKeys()))
	for _, key := range settingssvc.ManagedKeys() {
		field, ok := settingssvc.FieldByKey(key)
		if !ok || field.Group == "features" {
			continue
		}
		keys = append(keys, key)
	}
	return keys
}

// effectiveValue returns the /config value/source, mapping the legacy
// MAX_RETRY_MINUTES alias to its seconds equivalent when the new key is absent.
// The recorder itself performs the same conversion at startup.
func (c *Controller) effectiveValue(key, defaultValue string) (string, configfile.Source) {
	value, source := c.store.EffectiveValue(key, defaultValue)
	if key == "MAX_RETRY_SECONDS" && (source == configfile.SourceDefault || strings.TrimSpace(value) == "") {
		if raw, legacySource := c.store.EffectiveValue("MAX_RETRY_MINUTES", ""); raw != "" {
			if minutes, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && minutes >= 0 {
				return strconv.Itoa(minutes * 60), legacySource
			}
		}
	}
	return value, source
}

func (c *Controller) response() ConfigResponse {
	fields := settingssvc.ManagedSchema()
	ffmpegMissing := c.convert == nil || len(c.convert.Providers()) == 0
	response := ConfigResponse{
		ConfigFile:      c.store.ConfigPath(),
		SecretsFile:     c.store.SecretsPath(),
		Values:          make(map[string]string),
		Overrides:       make(map[string]string),
		Defaults:        make(map[string]string),
		Sources:         make(map[string]string),
		RestartRequired: c.store.RestartRequired(),
	}
	// Inject disabled_reason for requires:"ffmpeg" fields.
	for gi := range fields.Groups {
		for fi := range fields.Groups[gi].Fields {
			field := &fields.Groups[gi].Fields[fi]
			if field.Requires == "ffmpeg" && ffmpegMissing {
				field.DisabledReason = "未检测到可用的转码器（ffmpeg 或 CloudConvert）"
			}
		}
	}
	response.Schema = fields
	for _, key := range configFieldKeys() {
		field, ok := settingssvc.FieldByKey(key)
		if !ok {
			continue
		}
		response.Defaults[key] = field.Default
		if value, exists := c.store.ConfigValue(key); exists {
			response.Overrides[key] = value
		}
		value, source := c.effectiveValue(key, field.Default)
		response.Values[key] = value
		response.Sources[key] = string(source)
	}
	return response
}

// @Summary Get the unified config
// @Description Returns the /config schema, effective values, persisted overrides, defaults, per-key sources and restart-required keys. Secret keys are never included (docs/CONFIG-V2.md §4).
// @Tags config
// @Security BearerAuth
// @Produce json
// @Success 200 {object} ConfigResponse "Unified config"
// @Failure 403 {string} string "Forbidden"
// @Router /config [get]
func (c *Controller) getConfig(ctx fiber.Ctx) error {
	return ctx.JSON(c.response())
}

// @Summary Update the unified config
// @Description Persists non-secret managed keys to config.json (0644, atomic). Unknown/bootstrap/secret/feature keys are rejected; env-provided keys are locked; values are validated against the schema.
// @Tags config
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param payload body ConfigUpdateRequest true "Values to set and keys to clear"
// @Success 200 {object} ConfigResponse "Updated unified config"
// @Failure 400 {string} string "Invalid values or keys"
// @Failure 403 {string} string "Not Admin"
// @Router /config [put]
func (c *Controller) updateConfig(ctx fiber.Ctx) error {
	var req ConfigUpdateRequest
	if err := ctx.Bind().Body(&req); err != nil {
		log.Warnf("无法解析配置更新请求：%v", err)
		return fiber.NewError(fiber.StatusBadRequest, "无效的请求数据")
	}
	if err := c.validateConfigUpdate(req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	if err := c.store.SetConfig(req.Values, req.Clear); err != nil {
		log.Errorf("保存配置失败：%v", err)
		return fiber.ErrInternalServerError
	}
	return ctx.JSON(c.response())
}

// validateConfigUpdate checks keys and values before persisting.
func (c *Controller) validateConfigUpdate(req ConfigUpdateRequest) error {
	features := map[string]struct{}{}
	for _, key := range settingssvc.FeatureSchemaKeys() {
		features[key] = struct{}{}
	}
	for key, value := range req.Values {
		if err := c.validateConfigKey(key, features); err != nil {
			return err
		}
		field, _ := settingssvc.FieldByKey(key)
		if err := settingssvc.ValidateFieldValue(field, value); err != nil {
			return err
		}
	}
	for _, key := range req.Clear {
		if err := c.validateConfigKey(key, features); err != nil {
			return err
		}
	}
	return nil
}

func (c *Controller) validateConfigKey(key string, features map[string]struct{}) error {
	if configfile.IsBootstrapKey(key) {
		return fiber.NewError(fiber.StatusBadRequest, "引导配置项不能通过 /config 修改："+key)
	}
	if configfile.IsSecretKey(key) {
		return fiber.NewError(fiber.StatusBadRequest, "敏感配置项请通过 /config/secrets 修改："+key)
	}
	if _, isFeature := features[key]; isFeature {
		return fiber.NewError(fiber.StatusBadRequest, "FEATURES-V2 配置项请通过 /settings 修改："+key)
	}
	if _, ok := settingssvc.FieldByKey(key); !ok {
		return fiber.NewError(fiber.StatusBadRequest, "未知的配置项："+key)
	}
	if c.store.IsEnvLocked(key) {
		return fiber.NewError(fiber.StatusBadRequest, "该配置项由环境变量提供，无法通过配置文件修改："+key)
	}
	return nil
}

// @Summary Reset the unified config
// @Description Removes every managed non-secret key from config.json, preserving unknown keys.
// @Tags config
// @Security BearerAuth
// @Produce json
// @Success 200 {object} ConfigResponse "Reset unified config"
// @Failure 403 {string} string "Not Admin"
// @Router /config/reset [post]
func (c *Controller) resetConfig(ctx fiber.Ctx) error {
	if err := c.store.ResetConfig(configFieldKeys()); err != nil {
		log.Errorf("重置配置失败：%v", err)
		return fiber.ErrInternalServerError
	}
	return ctx.JSON(c.response())
}

func (c *Controller) secretsResponse() SecretsResponse {
	response := SecretsResponse{Set: make(map[string]bool), Schema: settingssvc.SecretsSchema()}
	for _, key := range settingssvc.SecretSchemaKeys() {
		response.Set[key] = c.store.SecretIsSet(key)
	}
	return response
}

// @Summary Get secret config state
// @Description Returns only whether each managed secret is set; values are never exposed.
// @Tags config
// @Security BearerAuth
// @Produce json
// @Success 200 {object} SecretsResponse "Secret state"
// @Failure 403 {string} string "Forbidden"
// @Router /config/secrets [get]
func (c *Controller) getSecrets(ctx fiber.Ctx) error {
	return ctx.JSON(c.secretsResponse())
}

// @Summary Update secret config
// @Description Writes secrets.json with mode 0600 (atomic). Only the managed secret keys are accepted; non-empty values overwrite and `clear` removes keys.
// @Tags config
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param payload body SecretsUpdateRequest true "Secret values to set and keys to clear"
// @Success 200 {object} SecretsResponse "Updated secret state"
// @Failure 400 {string} string "Invalid keys or values"
// @Failure 403 {string} string "Not Admin"
// @Router /config/secrets [put]
func (c *Controller) updateSecrets(ctx fiber.Ctx) error {
	var req SecretsUpdateRequest
	if err := ctx.Bind().Body(&req); err != nil {
		log.Warnf("无法解析敏感配置更新请求：%v", err)
		return fiber.NewError(fiber.StatusBadRequest, "无效的请求数据")
	}
	values := make(map[string]string, len(req.Values))
	for key, value := range req.Values {
		if !configfile.IsSecretKey(key) {
			return fiber.NewError(fiber.StatusBadRequest, "未知的敏感配置项："+key)
		}
		if strings.TrimSpace(value) == "" {
			// Empty means "keep current value" for write-only inputs.
			continue
		}
		if c.store.IsEnvLocked(key) {
			return fiber.NewError(fiber.StatusBadRequest, "该敏感配置项由环境变量提供，无法通过配置文件修改："+key)
		}
		values[key] = value
	}
	for _, key := range req.Clear {
		if !configfile.IsSecretKey(key) {
			return fiber.NewError(fiber.StatusBadRequest, "未知的敏感配置项："+key)
		}
		if c.store.IsEnvLocked(key) {
			return fiber.NewError(fiber.StatusBadRequest, "该敏感配置项由环境变量提供，无法通过配置文件修改："+key)
		}
	}
	if err := c.store.SetSecrets(values, req.Clear); err != nil {
		log.Errorf("保存敏感配置失败：%v", err)
		return fiber.ErrInternalServerError
	}
	return ctx.JSON(c.secretsResponse())
}

// @Summary Reset secret config
// @Description Removes all managed secret values from secrets.json.
// @Tags config
// @Security BearerAuth
// @Produce json
// @Success 200 {object} SecretsResponse "Reset secret state"
// @Failure 403 {string} string "Not Admin"
// @Router /config/secrets/reset [post]
func (c *Controller) resetSecrets(ctx fiber.Ctx) error {
	if err := c.store.ResetSecrets(nil); err != nil {
		log.Errorf("重置敏感配置失败：%v", err)
		return fiber.ErrInternalServerError
	}
	return ctx.JSON(c.secretsResponse())
}
