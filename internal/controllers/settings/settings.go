package settings

import (
	"github.com/bilirec/bilirec/internal/modules/rest"
	settingssvc "github.com/bilirec/bilirec/internal/services/settings"
	"github.com/bilirec/bilirec/pkg/logger"
	"github.com/gofiber/fiber/v3"
)

var log = logger.Named("settings")

// Controller exposes the global settings REST API (docs/FEATURES-V2.md §2).
type Controller struct {
	svc *settingssvc.Service
}

// NewController registers GET/PUT /settings and POST /settings/reset.
// GET is available to admin+viewer, mutations are admin-only.
func NewController(app *fiber.App, svc *settingssvc.Service) *Controller {
	c := &Controller{svc: svc}
	app.Get("/settings", rest.RequiredRoles(rest.RoleAdmin, rest.RoleViewer), c.getSettings)
	app.Put("/settings", rest.AdminOnly, c.updateSettings)
	app.Post("/settings/reset", rest.AdminOnly, c.resetSettings)
	return c
}

// @Summary Get global settings
// @Description Returns env-seeded defaults, persisted overrides and the merged values (docs/FEATURES-V2.md §2).
// @Tags settings
// @Security BearerAuth
// @Produce json
// @Success 200 {object} SettingsResponse "Settings"
// @Failure 403 {string} string "Forbidden"
// @Failure 500 {string} string "Internal server error"
// @Router /settings [get]
func (c *Controller) getSettings(ctx fiber.Ctx) error {
	return ctx.JSON(c.response())
}

// @Summary Update global settings
// @Description Partial update: only keys present in the JSON body overwrite persisted overrides; absent keys keep their current override (or the env default). Values are resolved at session start (new sessions only).
// @Tags settings
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param payload body settingssvc.GlobalSettingsPatch true "Partial settings patch"
// @Success 200 {object} SettingsResponse "Updated settings"
// @Failure 400 {string} string "Invalid settings"
// @Failure 403 {string} string "Not Admin"
// @Failure 500 {string} string "Internal server error"
// @Router /settings [put]
func (c *Controller) updateSettings(ctx fiber.Ctx) error {
	var patch settingssvc.GlobalSettingsPatch
	if err := ctx.Bind().Body(&patch); err != nil {
		log.Warnf("无法解析设置更新请求：%v", err)
		return fiber.NewError(fiber.StatusBadRequest, "无效的请求数据")
	}
	if err := c.svc.Update(patch); err != nil {
		if settingssvc.IsValidationError(err) {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		log.Errorf("更新全局设置失败：%v", err)
		return fiber.ErrInternalServerError
	}
	return ctx.JSON(c.response())
}

// @Summary Reset global settings
// @Description Clears persisted overrides so values fall back to the env-seeded defaults.
// @Tags settings
// @Security BearerAuth
// @Success 200 {string} string "OK"
// @Failure 403 {string} string "Not Admin"
// @Failure 500 {string} string "Internal server error"
// @Router /settings/reset [post]
func (c *Controller) resetSettings(ctx fiber.Ctx) error {
	if err := c.svc.Reset(); err != nil {
		log.Errorf("重置全局设置失败：%v", err)
		return fiber.ErrInternalServerError
	}
	return ctx.SendStatus(fiber.StatusOK)
}

func (c *Controller) response() SettingsResponse {
	defaults, overrides, values := c.svc.Get()
	return SettingsResponse{Defaults: defaults, Overrides: overrides, Values: values}
}
