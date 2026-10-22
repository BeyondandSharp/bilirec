// SPDX-License-Identifier: GPL-3.0-only

// Package webhook exposes the admin-only webhook test endpoint.
package webhook

import (
	"errors"

	"github.com/bilirec/bilirec/internal/modules/rest"
	wh "github.com/bilirec/bilirec/internal/services/webhook"
	"github.com/gofiber/fiber/v3"
)

// Controller exposes POST /webhook/test.
type Controller struct {
	svc *wh.Service
}

func NewController(app *fiber.App, svc *wh.Service) *Controller {
	c := &Controller{svc: svc}
	app.Post("/webhook/test", rest.AdminOnly, c.testWebhook)
	return c
}

// @Summary Send a sample webhook
// @Description Sends one complete FileClosed sample payload to every configured WEBHOOK_URLS target. Admin only.
// @Tags webhook
// @Security BearerAuth
// @Produce json
// @Success 200 {object} webhook.TestReport "All deliveries succeeded"
// @Failure 400 {string} string "No webhook URLs configured"
// @Failure 401 {string} string "Unauthorized"
// @Failure 403 {string} string "Not Admin"
// @Failure 502 {object} webhook.TestReport "At least one delivery failed"
// @Router /webhook/test [post]
func (c *Controller) testWebhook(ctx fiber.Ctx) error {
	if c.svc == nil {
		return fiber.NewError(fiber.StatusBadRequest, "未配置 webhook 地址")
	}
	report, err := c.svc.SendSample(ctx.Context())
	if err != nil {
		if errors.Is(err, wh.ErrNotConfigured) {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	ok := true
	for _, result := range report.Results {
		if !result.OK {
			ok = false
			break
		}
	}
	if !ok {
		return ctx.Status(fiber.StatusBadGateway).JSON(report)
	}
	return ctx.JSON(report)
}
