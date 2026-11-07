package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/suma/suma/server/internal/imageupdate"
	"github.com/suma/suma/server/internal/taskcenter"
)

// Task Center error codes: 22xxx.
const (
	codeTaskCenterInvalid        = 22001
	codeTaskCenterUnknownKind    = 22002
	codeTaskCenterConflict       = 22003
	codeTaskCenterBusy           = 22004
	codeTaskCenterNotFound       = 22005
	codeTaskCenterDuplicate      = 22006
	codeTaskCenterInvalidParams  = 22007
	codeTaskCenterNodeMissing    = 22008
	codeTaskCenterStartFailed    = 22009
	codeTaskCenterPreviewInvalid = 22010
)

func registerTaskCenterRoutes(v1 *gin.RouterGroup, deps Dependencies) {
	group := v1.Group("/task-center")
	group.Use(requireAuth(deps.Auth))
	group.GET("/kinds", func(c *gin.Context) {
		success(c, deps.TaskCenterKinds.List())
	})
	group.GET("/schedules", func(c *gin.Context) {
		views, err := deps.TaskCenter.Schedules(c.Request.Context(), c.Query("kind"), c.Query("node_id"))
		if err != nil {
			failure(c, http.StatusInternalServerError, codeTaskCenterStartFailed, "Unable to list schedules")
			return
		}
		success(c, views)
	})
	group.POST("/schedules", func(c *gin.Context) {
		var input taskcenter.ScheduleInput
		if c.ShouldBindJSON(&input) != nil {
			failure(c, http.StatusBadRequest, codeTaskCenterInvalid, "Invalid schedule")
			return
		}
		row, err := deps.TaskCenter.Create(c.Request.Context(), input, currentUser(c).Username)
		if err != nil {
			if taskCenterFailure(c, err) {
				return
			}
			failure(c, http.StatusInternalServerError, codeTaskCenterStartFailed, "Unable to create schedule")
			return
		}
		recordAudit(c, deps.Audit, "task_center.schedule", "task_schedule", row.Name, "success")
		success(c, row)
	})
	group.GET("/schedules/preview", func(c *gin.Context) {
		runs, err := taskcenter.NextRuns(c.Query("cron"), c.Query("timezone"), time.Now(), 5)
		if err != nil || len(runs) == 0 {
			failure(c, http.StatusBadRequest, codeTaskCenterPreviewInvalid, "Cron expression or timezone is invalid")
			return
		}
		success(c, runs)
	})
	group.GET("/schedules/:id", func(c *gin.Context) {
		id, ok := taskCenterID(c)
		if !ok {
			return
		}
		view, err := deps.TaskCenter.Schedule(c.Request.Context(), id)
		if err != nil {
			if taskCenterFailure(c, err) {
				return
			}
			failure(c, http.StatusInternalServerError, codeTaskCenterStartFailed, "Unable to load schedule")
			return
		}
		success(c, view)
	})
	group.PUT("/schedules/:id", func(c *gin.Context) {
		id, ok := taskCenterID(c)
		if !ok {
			return
		}
		var input struct {
			taskcenter.ScheduleInput
			ExpectedVersion uint64 `json:"expected_version"`
		}
		if c.ShouldBindJSON(&input) != nil {
			failure(c, http.StatusBadRequest, codeTaskCenterInvalid, "Invalid schedule")
			return
		}
		row, err := deps.TaskCenter.Update(c.Request.Context(), id, input.ScheduleInput, input.ExpectedVersion)
		if err != nil {
			if taskCenterFailure(c, err) {
				return
			}
			failure(c, http.StatusInternalServerError, codeTaskCenterStartFailed, "Unable to update schedule")
			return
		}
		recordAudit(c, deps.Audit, "task_center.schedule", "task_schedule", row.Name, "success")
		success(c, row)
	})
	group.DELETE("/schedules/:id", func(c *gin.Context) {
		id, ok := taskCenterID(c)
		if !ok {
			return
		}
		view, viewErr := deps.TaskCenter.Schedule(c.Request.Context(), id)
		if viewErr == nil {
			defer recordAudit(c, deps.Audit, "task_center.schedule", "task_schedule", view.Name, "success")
		}
		if err := deps.TaskCenter.Delete(c.Request.Context(), id); err != nil {
			if taskCenterFailure(c, err) {
				return
			}
			failure(c, http.StatusInternalServerError, codeTaskCenterStartFailed, "Unable to delete schedule")
			return
		}
		success(c, gin.H{"id": id})
	})
	group.POST("/schedules/:id/run", func(c *gin.Context) {
		id, ok := taskCenterID(c)
		if !ok {
			return
		}
		run, err := deps.TaskCenter.RunNow(c.Request.Context(), id)
		if err != nil {
			if taskCenterFailure(c, err) {
				return
			}
			failure(c, http.StatusInternalServerError, codeTaskCenterStartFailed, "Unable to start schedule run")
			return
		}
		recordAudit(c, deps.Audit, "task_center.schedule.run", "task_schedule", strconv.FormatUint(uint64(id), 10), "success")
		success(c, run)
	})
	group.GET("/schedules/:id/runs", func(c *gin.Context) {
		id, ok := taskCenterID(c)
		if !ok {
			return
		}
		runs, err := deps.TaskCenter.Runs(c.Request.Context(), id, queryInt(c, "limit", 20))
		if err != nil {
			failure(c, http.StatusInternalServerError, codeTaskCenterStartFailed, "Unable to list schedule runs")
			return
		}
		success(c, runs)
	})
	group.POST("/tasks", func(c *gin.Context) {
		var input struct {
			Kind   string          `json:"kind" binding:"required"`
			NodeID string          `json:"node_id"`
			Params json.RawMessage `json:"params"`
		}
		if c.ShouldBindJSON(&input) != nil {
			failure(c, http.StatusBadRequest, codeTaskCenterInvalidParams, "Invalid task request")
			return
		}
		if input.Params == nil {
			input.Params = json.RawMessage("{}")
		}
		taskID, err := deps.TaskCenter.StartManual(c.Request.Context(), input.Kind, input.NodeID, input.Params)
		if err != nil {
			if taskCenterFailure(c, err) {
				return
			}
			failure(c, http.StatusInternalServerError, codeTaskCenterStartFailed, "Unable to start task")
			return
		}
		recordAudit(c, deps.Audit, "task_center.task", "task", input.Kind, "success")
		success(c, gin.H{"task_id": taskID})
	})
	group.GET("/image-updates", func(c *gin.Context) {
		nodeID := c.Query("node_id")
		if nodeID == "" {
			failure(c, http.StatusBadRequest, codeTaskCenterNodeMissing, "A node is required")
			return
		}
		if deps.Nodes != nil {
			if _, err := deps.Nodes.Get(c.Request.Context(), nodeID); err != nil {
				failure(c, http.StatusNotFound, codeTaskCenterNodeMissing, "Docker node not found")
				return
			}
		}
		projects, err := deps.ImageUpdates.View(c.Request.Context(), nodeID, c.Query("project"))
		if err != nil {
			failure(c, http.StatusInternalServerError, codeTaskCenterStartFailed, "Unable to load image update state")
			return
		}
		success(c, projects)
	})
	group.GET("/records", func(c *gin.Context) {
		scope := c.DefaultQuery("scope", "all")
		if scope != "all" && scope != "control_plane" && scope != "node" {
			failure(c, http.StatusBadRequest, codeTaskCenterInvalid, "Scope must be all, control_plane or node")
			return
		}
		nodeID := c.Query("node_id")
		if nodeID != "" && deps.Nodes != nil {
			if _, err := deps.Nodes.Get(c.Request.Context(), nodeID); err != nil {
				failure(c, http.StatusNotFound, codeTaskCenterNodeMissing, "Docker node not found")
				return
			}
		}
		rows, total, err := deps.Tasks.ListRecords(c.Request.Context(), mapScope(scope), nodeID, c.Query("status"), c.Query("type"), queryInt(c, "limit", 50), queryInt(c, "offset", 0))
		if err != nil {
			failure(c, http.StatusInternalServerError, codeTaskCenterStartFailed, "Unable to list task records")
			return
		}
		success(c, gin.H{"items": rows, "total": total})
	})
}

// taskCenterFailure maps domain errors onto 22xxx responses; it reports
// whether the error was handled.
func taskCenterFailure(c *gin.Context, err error) bool {
	var busy *imageupdate.BusyError
	switch {
	case errors.As(err, &busy):
		failure(c, http.StatusConflict, codeTaskCenterBusy, busy.Error())
		return true
	case errors.Is(err, taskcenter.ErrNotFound):
		failure(c, http.StatusNotFound, codeTaskCenterNotFound, "Schedule not found")
	case errors.Is(err, taskcenter.ErrVersionConflict):
		failure(c, http.StatusConflict, codeTaskCenterConflict, "Schedule changed concurrently; reload and retry")
	case errors.Is(err, taskcenter.ErrBusy):
		failure(c, http.StatusConflict, codeTaskCenterBusy, err.Error())
	case errors.Is(err, taskcenter.ErrDuplicate):
		failure(c, http.StatusConflict, codeTaskCenterDuplicate, "An identical schedule already exists")
	case errors.Is(err, taskcenter.ErrUnknownKind):
		failure(c, http.StatusBadRequest, codeTaskCenterUnknownKind, "Unknown task kind")
	case errors.Is(err, taskcenter.ErrInvalidParams):
		failure(c, http.StatusBadRequest, codeTaskCenterInvalidParams, err.Error())
	case errors.Is(err, taskcenter.ErrInvalid):
		failure(c, http.StatusBadRequest, codeTaskCenterInvalid, err.Error())
	default:
		return false
	}
	return true
}

func taskCenterID(c *gin.Context) (uint, bool) {
	parsed, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || parsed == 0 {
		failure(c, http.StatusBadRequest, codeTaskCenterInvalid, "Invalid schedule id")
		return 0, false
	}
	return uint(parsed), true
}

func queryInt(c *gin.Context, name string, fallback int) int {
	value, err := strconv.Atoi(c.Query(name))
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

func mapScope(scope string) string {
	switch scope {
	case "control_plane":
		return "control_plane"
	case "node":
		return "node"
	default:
		return ""
	}
}
