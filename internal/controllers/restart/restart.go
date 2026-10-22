// SPDX-License-Identifier: GPL-3.0-only

// Package restart exposes the admin-only process restart endpoint.
package restart

import (
	"errors"

	"github.com/bilirec/bilirec/internal/modules/rest"
	"github.com/bilirec/bilirec/internal/services/restart"
	"github.com/gofiber/fiber/v3"
)

type Controller struct{}

func NewController(app *fiber.App) *Controller {
	c := &Controller{}
	app.Post("/system/restart", rest.AdminOnly, c.restart)
	return c
}

type response struct {
	Status string `json:"status"`
}

// @Summary Restart the backend process
// @Description Gracefully stops the backend and re-executes it so persisted settings apply. Admin only.
// @Tags system
// @Security BearerAuth
// @Produce json
// @Success 202 {object} response "Restart accepted"
// @Failure 403 {string} string "Forbidden"
// @Failure 409 {string} string "Restart already in progress"
// @Failure 501 {string} string "Restart unsupported in this runtime"
// @Router /system/restart [post]
func (c *Controller) restart(ctx fiber.Ctx) error {
	if err := restart.Request(); err != nil {
		switch {
		case errors.Is(err, restart.ErrDisabled):
			return fiber.NewError(fiber.StatusNotImplemented, err.Error())
		case errors.Is(err, restart.ErrAlreadyRequested):
			return fiber.NewError(fiber.StatusConflict, err.Error())
		default:
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
	}
	return ctx.Status(fiber.StatusAccepted).JSON(response{Status: "restarting"})
}
