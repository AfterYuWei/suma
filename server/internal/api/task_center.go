package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/suma/suma/server/internal/cleanup"
	"github.com/suma/suma/server/internal/imageupdate"
	"github.com/suma/suma/server/internal/projectcd"
	"github.com/suma/suma/server/internal/task"
	"github.com/suma/suma/server/internal/taskcenter"
	"gorm.io/gorm"
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
		scope := c.DefaultQuery("scope", "all")
		if scope != "all" && scope != "node" && scope != "control_plane" {
			failure(c, http.StatusBadRequest, codeTaskCenterInvalid, "Invalid automation scope")
			return
		}
		views, err := deps.TaskCenter.Schedules(c.Request.Context(), c.Query("kind"), c.Query("node_id"), taskcenter.ScheduleFilter{Scope: c.Query("scope"), IncludeArchived: c.Query("include_archived") == "true"})
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
		row, err := deps.TaskCenter.Create(c.Request.Context(), input, currentUser(c).Username, taskCenterActor(c))
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
		runs, err := deps.TaskCenter.Preview(taskcenter.ScheduleInput{Kind: c.Query("kind"), ScheduleType: c.Query("schedule_type"), Cron: c.Query("cron"), Timezone: c.Query("timezone"), IntervalSeconds: queryInt(c, "interval_seconds", 0)}, 5)
		if err != nil {
			failure(c, http.StatusBadRequest, codeTaskCenterPreviewInvalid, "Schedule or timezone is invalid")
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
			ExpectedVersion  uint64 `json:"expected_version"`
			ConfirmationName string `json:"confirmation_name"`
			Authorize        bool   `json:"authorize"`
		}
		if c.ShouldBindJSON(&input) != nil {
			failure(c, http.StatusBadRequest, codeTaskCenterInvalid, "Invalid schedule")
			return
		}
		actor := taskCenterActor(c)
		actor.ConfirmationName, actor.Authorize = input.ConfirmationName, input.Authorize
		row, err := deps.TaskCenter.Update(c.Request.Context(), id, input.ScheduleInput, input.ExpectedVersion, actor)
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
		version, err := strconv.ParseUint(c.Query("expected_version"), 10, 64)
		if err != nil || version == 0 {
			failure(c, http.StatusBadRequest, codeTaskCenterInvalid, "Expected rule version is required")
			return
		}
		view, viewErr := deps.TaskCenter.Schedule(c.Request.Context(), id)
		if err := deps.TaskCenter.Delete(c.Request.Context(), id, version); err != nil {
			if taskCenterFailure(c, err) {
				return
			}
			failure(c, http.StatusInternalServerError, codeTaskCenterStartFailed, "Unable to delete schedule")
			return
		}
		if viewErr == nil {
			recordAudit(c, deps.Audit, "task_center.schedule.archive", "task_schedule", view.Name, "success")
		}
		success(c, gin.H{"id": id})
	})
	group.POST("/schedules/:id/run", func(c *gin.Context) {
		id, ok := taskCenterID(c)
		if !ok {
			return
		}
		var input struct {
			Action           string `json:"action"`
			ConfirmationName string `json:"confirmation_name"`
			Authorize        bool   `json:"authorize"`
		}
		if err := c.ShouldBindJSON(&input); err != nil && !errors.Is(err, io.EOF) {
			failure(c, http.StatusBadRequest, codeTaskCenterInvalidParams, "Invalid task action")
			return
		}
		actor := taskCenterActor(c)
		actor.ConfirmationName, actor.Authorize = input.ConfirmationName, input.Authorize
		run, err := deps.TaskCenter.RunNow(c.Request.Context(), id, taskcenter.DispatchRequest{Action: input.Action, Actor: actor})
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
		filter, ok := taskCenterRunFilter(c)
		if !ok {
			return
		}
		filter.ScheduleID = id
		page, err := deps.TaskCenter.TriggerRecords(c.Request.Context(), filter, queryInt(c, "limit", 20), queryInt(c, "offset", 0))
		if err != nil {
			failure(c, http.StatusInternalServerError, codeTaskCenterStartFailed, "Unable to list trigger records")
			return
		}
		if _, paged := c.GetQuery("offset"); paged {
			success(c, page)
		} else {
			success(c, page.Items)
		}
	})
	group.GET("/runs", func(c *gin.Context) {
		filter, ok := taskCenterRunFilter(c)
		if !ok {
			return
		}
		page, err := deps.TaskCenter.TriggerRecords(c.Request.Context(), filter, queryInt(c, "limit", 20), queryInt(c, "offset", 0))
		if err != nil {
			failure(c, http.StatusInternalServerError, codeTaskCenterStartFailed, "Unable to list trigger records")
			return
		}
		success(c, page)
	})
	group.GET("/runs/:id", func(c *gin.Context) {
		id, ok := taskCenterID(c)
		if !ok {
			return
		}
		row, err := deps.TaskCenter.TriggerRecord(c.Request.Context(), id)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			failure(c, http.StatusNotFound, codeTaskCenterNotFound, "Trigger record not found")
			return
		}
		if err != nil {
			failure(c, http.StatusInternalServerError, codeTaskCenterStartFailed, "Unable to load trigger record")
			return
		}
		success(c, row)
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
		taskID, err := deps.TaskCenter.StartManual(c.Request.Context(), input.Kind, input.NodeID, input.Params, taskCenterActor(c))
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
		source := c.Query("source")
		status := c.Query("status")
		search := strings.TrimSpace(c.Query("search"))
		if !slices.Contains([]string{"", "manual", "scheduled", "webhook", "system", "unrecorded"}, source) ||
			!slices.Contains([]string{"", "active", "finished", "pending", "running", "success", "failed", "canceled"}, status) ||
			len([]rune(search)) > 256 || len(c.Query("type")) > 64 {
			failure(c, http.StatusBadRequest, codeTaskCenterInvalid, "Invalid task record filter")
			return
		}
		scheduleID, ok := taskCenterRuleFilter(c)
		if !ok {
			return
		}
		// Historical node IDs remain valid after node deletion; this is a DB-only
		// evidence query and does not resolve a live Docker runtime.
		rows, total, err := deps.Tasks.Records(c.Request.Context(), task.RecordQuery{Scope: mapScope(scope), NodeID: c.Query("node_id"), Status: status, TypePrefix: c.Query("type"), Search: search, Source: source, ScheduleID: scheduleID, Limit: queryInt(c, "limit", 50), Offset: queryInt(c, "offset", 0), Summary: c.Query("view") == "summary"})
		if err != nil {
			failure(c, http.StatusInternalServerError, codeTaskCenterStartFailed, "Unable to list task records")
			return
		}
		success(c, gin.H{"items": rows, "total": total})
	})
}

func taskCenterRuleFilter(c *gin.Context) (uint, bool) {
	value := c.Query("schedule_id")
	if value == "" {
		return 0, true
	}
	id, err := strconv.ParseUint(value, 10, 64)
	if err != nil || id == 0 {
		failure(c, http.StatusBadRequest, codeTaskCenterInvalid, "Invalid rule filter")
		return 0, false
	}
	return uint(id), true
}

func taskCenterRunFilter(c *gin.Context) (taskcenter.RunFilter, bool) {
	filter := taskcenter.RunFilter{Scope: c.DefaultQuery("scope", "all"), NodeID: c.Query("node_id"), Source: c.Query("source"), Status: c.Query("status"), Summary: c.Query("view") == "summary"}
	if !slices.Contains([]string{"all", "node", "control_plane"}, filter.Scope) ||
		!slices.Contains([]string{"", "manual", "scheduled", "webhook", "system"}, filter.Source) ||
		!slices.Contains([]string{"", "dispatching", "started", "skipped", "start_failed", "interrupted"}, filter.Status) {
		failure(c, http.StatusBadRequest, codeTaskCenterInvalid, "Invalid trigger record filter")
		return filter, false
	}
	var ok bool
	filter.ScheduleID, ok = taskCenterRuleFilter(c)
	return filter, ok
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
	case errors.Is(err, task.ErrInvalidParams), errors.Is(err, taskcenter.ErrUnsupportedAction), errors.Is(err, taskcenter.ErrBusinessOwned):
		failure(c, http.StatusBadRequest, codeTaskCenterInvalidParams, err.Error())
	case errors.Is(err, taskcenter.ErrPaused):
		failure(c, http.StatusConflict, codeTaskCenterConflict, err.Error())
	case errors.Is(err, cleanup.ErrConfirmation):
		failure(c, http.StatusBadRequest, codeTaskCenterInvalidParams, cleanup.ErrConfirmation.Error())
	case errors.Is(err, cleanup.ErrConflict), errors.Is(err, projectcd.ErrConflict):
		failure(c, http.StatusConflict, codeTaskCenterConflict, err.Error())
	case errors.Is(err, cleanup.ErrInvalid):
		failure(c, http.StatusBadRequest, codeTaskCenterInvalidParams, cleanup.ErrInvalid.Error())
	case errors.Is(err, taskcenter.ErrStopped):
		failure(c, http.StatusServiceUnavailable, codeTaskCenterStartFailed, taskcenter.ErrStopped.Error())
	case errors.Is(err, taskcenter.ErrInvalid):
		failure(c, http.StatusBadRequest, codeTaskCenterInvalid, err.Error())
	default:
		return false
	}
	return true
}

func taskCenterActor(c *gin.Context) taskcenter.Actor {
	user := currentUser(c)
	return taskcenter.Actor{ID: &user.ID, Name: user.Username, IP: requestClientIP(c)}
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
