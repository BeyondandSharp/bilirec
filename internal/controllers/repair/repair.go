package repair

import (
	"errors"
	"os"
	"strings"

	"github.com/bilirec/bilirec/internal/modules/rest"
	"github.com/bilirec/bilirec/internal/services/path"
	"github.com/bilirec/bilirec/internal/services/recorder"
	"github.com/bilirec/bilirec/internal/services/repairqueue"
	"github.com/bilirec/bilirec/pkg/logger"
	"github.com/gofiber/fiber/v3"
)

var log = logger.Named("repair")

type Controller struct {
	queue    *repairqueue.Service
	pathSvc  *path.Service
	recorder *recorder.Service
}

func NewController(app *fiber.App, queue *repairqueue.Service, pathSvc *path.Service, recorderSvc *recorder.Service) *Controller {
	c := &Controller{queue: queue, pathSvc: pathSvc, recorder: recorderSvc}
	repairs := app.Group("/repair")
	repairs.Get("/tasks", rest.AdminOnly, c.listTasks)
	repairs.Post("/tasks", rest.AdminOnly, c.addTasks)
	repairs.Delete("/tasks", rest.AdminOnly, c.deleteTasks)
	repairs.Post("/tasks/run", rest.AdminOnly, c.runTasks)
	return c
}

type pathsRequest struct {
	Paths []string `json:"paths"`
}

type listResponse struct {
	Enabled bool                `json:"enabled"`
	Items   []repairqueue.Entry `json:"items"`
}

// @Summary List batch video repair tasks
// @Tags repair
// @Security BearerAuth
// @Produce json
// @Success 200 {object} listResponse
// @Router /repair/tasks [get]
func (c *Controller) listTasks(ctx fiber.Ctx) error {
	items := c.queue.List()
	if items == nil {
		items = []repairqueue.Entry{}
	}
	return ctx.JSON(listResponse{Enabled: c.queue.Enabled(), Items: items})
}

// @Summary Add videos to the batch repair list
// @Tags repair
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body pathsRequest true "Relative recording paths"
// @Success 200 {object} listResponse
// @Router /repair/tasks [post]
func (c *Controller) addTasks(ctx fiber.Ctx) error {
	var req pathsRequest
	if err := ctx.Bind().JSON(&req); err != nil {
		return fiber.ErrBadRequest
	}
	paths, err := c.validatedPaths(req.Paths)
	if err != nil {
		return err
	}
	if _, err := c.queue.Add(paths); err != nil {
		return c.fiberError(err)
	}
	return c.listTasks(ctx)
}

// @Summary Delete videos from the batch repair list
// @Tags repair
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body pathsRequest true "Relative recording paths"
// @Success 200 {object} listResponse
// @Router /repair/tasks [delete]
func (c *Controller) deleteTasks(ctx fiber.Ctx) error {
	var req pathsRequest
	if err := ctx.Bind().JSON(&req); err != nil {
		return fiber.ErrBadRequest
	}
	paths, err := c.validatedPaths(req.Paths)
	if err != nil {
		return err
	}
	if err := c.queue.Remove(paths); err != nil {
		return c.fiberError(err)
	}
	return c.listTasks(ctx)
}

// @Summary Run batch repair for selected videos
// @Tags repair
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body pathsRequest true "Relative recording paths"
// @Success 200 {object} listResponse
// @Router /repair/tasks/run [post]
func (c *Controller) runTasks(ctx fiber.Ctx) error {
	var req pathsRequest
	if err := ctx.Bind().JSON(&req); err != nil {
		return fiber.ErrBadRequest
	}
	paths, err := c.validatedPaths(req.Paths)
	if err != nil {
		return err
	}
	if _, err := c.queue.Queue(paths); err != nil {
		return c.fiberError(err)
	}
	return c.listTasks(ctx)
}

func (c *Controller) validatedPaths(raw []string) ([]string, error) {
	if len(raw) == 0 || len(raw) > 100 {
		return nil, fiber.NewError(fiber.StatusBadRequest, "paths 数量必须为 1–100")
	}
	paths := make([]string, 0, len(raw))
	for _, value := range raw {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, fiber.NewError(fiber.StatusBadRequest, "文件路径为空")
		}
		full, err := c.pathSvc.ValidatePath(value)
		if err != nil {
			return nil, c.fiberError(err)
		}
		rel, err := c.pathSvc.GetRelativePath(full)
		if err != nil {
			return nil, c.fiberError(err)
		}
		rel = strings.TrimPrefix(rel, string(os.PathSeparator))
		if c.recorder != nil && c.recorder.IsRecording(rel) {
			return nil, fiber.NewError(fiber.StatusConflict, "正在录制的文件不能加入修复列表")
		}
		paths = append(paths, rel)
	}
	return paths, nil
}

func (c *Controller) fiberError(err error) error {
	switch {
	case errors.Is(err, repairqueue.ErrNotAvailable):
		return fiber.NewError(fiber.StatusBadRequest, "视频修复工具不可用")
	case errors.Is(err, repairqueue.ErrNotFLV):
		return fiber.NewError(fiber.StatusBadRequest, "只能修复 FLV 视频")
	case errors.Is(err, repairqueue.ErrRunning):
		return fiber.NewError(fiber.StatusConflict, "正在修复的文件不能删除")
	case errors.Is(err, repairqueue.ErrNotFound):
		return fiber.NewError(fiber.StatusNotFound, "修复列表中没有该文件")
	case errors.Is(err, path.ErrInvalidFilePath):
		return fiber.NewError(fiber.StatusBadRequest, "无效文件路径")
	case errors.Is(err, path.ErrAccessDenied):
		return fiber.NewError(fiber.StatusForbidden, "无法访问该文件路径")
	case os.IsNotExist(err):
		return fiber.NewError(fiber.StatusNotFound, "文件不存在")
	default:
		log.Warnf("修复请求失败：%v", err)
		return fiber.ErrInternalServerError
	}
}
