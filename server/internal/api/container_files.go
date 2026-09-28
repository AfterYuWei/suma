package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/suma/suma/server/internal/auth"
	"github.com/suma/suma/server/internal/containerfiles"
	"github.com/suma/suma/server/internal/task"
)

func fileFailure(c *gin.Context, err error) {
	status := http.StatusUnprocessableEntity
	if errors.Is(err, containerfiles.ErrConflict) {
		status = http.StatusConflict
	}
	if errors.Is(err, containerfiles.ErrUnsupported) {
		status = http.StatusForbidden
	}
	failure(c, status, 20400, err.Error())
}

func fileUser(c *gin.Context) *uint {
	if value, ok := c.Get("user"); ok {
		id := value.(auth.User).ID
		return &id
	}
	return nil
}

func registerNodeFileRoutes(group *gin.RouterGroup, deps Dependencies) {
	files := group.Group("/containers/:id/files")
	files.Use(func(c *gin.Context) { c.Header("Cache-Control", "no-store"); c.Next() })
	files.GET("", func(c *gin.Context) {
		adapter, _, ok := resolveNode(c, deps)
		if !ok {
			return
		}
		if deps.Files == nil {
			failure(c, 503, 20401, "Container file service unavailable")
			return
		}
		folder := c.DefaultQuery("path", "/")
		row, err := deps.Files.List(c.Request.Context(), adapter, c.Param("id"), folder, c.Query("cursor"))
		if err != nil {
			fileFailure(c, err)
			return
		}
		success(c, row)
	})
	files.GET("/content", func(c *gin.Context) {
		adapter, view, ok := resolveNode(c, deps)
		if !ok {
			return
		}
		if deps.Files == nil {
			failure(c, 503, 20401, "Container file service unavailable")
			return
		}
		row, err := deps.Files.ReadForNode(c.Request.Context(), adapter, view.ID, c.Param("id"), c.Query("path"))
		if err != nil {
			fileFailure(c, err)
			return
		}
		success(c, row)
	})
	files.PUT("/content", func(c *gin.Context) {
		adapter, view, ok := resolveNode(c, deps)
		if !ok {
			return
		}
		if deps.Files == nil {
			failure(c, 503, 20401, "Container file service unavailable")
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, (containerfiles.MaxTextBytes*2)+4096)
		var input struct {
			Path    string `json:"path"`
			Content string `json:"content"`
			ETag    string `json:"etag"`
		}
		if c.ShouldBindJSON(&input) != nil || input.ETag == "" {
			failure(c, 400, 20402, "Invalid file save request")
			return
		}
		row, err := deps.Files.Save(c.Request.Context(), adapter, view.ID, c.Param("id"), input.Path, input.ETag, input.Content, fileUser(c))
		result := "success"
		if err != nil {
			result = "failed"
		}
		recordNodeAudit(c, deps, view.ID, view.Name, "container.file.save", "container_file", c.Param("id")+":"+input.Path, result)
		if err != nil {
			fileFailure(c, err)
			return
		}
		success(c, row)
	})
	files.GET("/history", func(c *gin.Context) {
		adapter, view, ok := resolveNode(c, deps)
		if !ok {
			return
		}
		if deps.Files == nil {
			failure(c, 503, 20401, "Container file service unavailable")
			return
		}
		rows, err := deps.Files.History(c.Request.Context(), adapter, view.ID, c.Param("id"), c.Query("path"))
		if err != nil {
			fileFailure(c, err)
			return
		}
		success(c, rows)
	})
	files.GET("/history/:revision", func(c *gin.Context) {
		_, view, ok := resolveNode(c, deps)
		if !ok {
			return
		}
		if deps.Files == nil {
			failure(c, 503, 20401, "Container file service unavailable")
			return
		}
		id, err := strconv.ParseUint(c.Param("revision"), 10, 32)
		if err != nil {
			failure(c, 400, 20403, "Invalid revision ID")
			return
		}
		row, err := deps.Files.RevisionContent(c.Request.Context(), view.ID, c.Param("id"), c.Query("path"), uint(id))
		if err != nil {
			fileFailure(c, err)
			return
		}
		success(c, row)
	})
	files.POST("/restore", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		adapter, view, ok := resolveNode(c, deps)
		if !ok {
			return
		}
		if deps.Files == nil {
			failure(c, 503, 20401, "Container file service unavailable")
			return
		}
		var input struct {
			Path       string `json:"path"`
			RevisionID uint   `json:"revision_id"`
			ETag       string `json:"etag"`
		}
		if c.ShouldBindJSON(&input) != nil || input.RevisionID == 0 || input.ETag == "" {
			failure(c, 400, 20404, "Invalid restore request")
			return
		}
		row, err := deps.Files.Restore(c.Request.Context(), adapter, view.ID, c.Param("id"), input.Path, input.RevisionID, input.ETag, fileUser(c))
		result := "success"
		if err != nil {
			result = "failed"
		}
		recordNodeAudit(c, deps, view.ID, view.Name, "container.file.restore", "container_file", c.Param("id")+":"+input.Path, result)
		if err != nil {
			fileFailure(c, err)
			return
		}
		success(c, row)
	})
	files.POST("/actions", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		adapter, view, ok := resolveNode(c, deps)
		if !ok {
			return
		}
		if deps.Files == nil {
			failure(c, 503, 20401, "Container file service unavailable")
			return
		}
		var input containerfiles.Action
		if c.ShouldBindJSON(&input) != nil {
			failure(c, 400, 20405, "Invalid file action")
			return
		}
		id := c.Param("id")
		if input.Action == "copy" || input.Action == "move" || input.Action == "delete" {
			userID, ip := fileUser(c), requestClientIP(c)
			taskRow, err := deps.Tasks.StartForNode(view.ID, view.Name, "container.file."+input.Action, fmt.Sprintf("%s %s", input.Action, input.Path), func(ctx context.Context, report task.Reporter) error {
				report(10, "Applying container file operation")
				err := deps.Files.ApplyForNode(ctx, adapter, view.ID, id, input)
				outcome := "success"
				if err != nil {
					outcome = "failed"
				}
				_ = deps.Audit.RecordForNode(context.Background(), view.ID, view.Name, userID, "container.file."+input.Action, "container_file", id+":"+input.Path, ip, outcome)
				if err == nil {
					report(100, "Container file operation completed")
				}
				return err
			})
			if err != nil {
				fileFailure(c, err)
				return
			}
			success(c, taskRow)
			return
		}
		err := deps.Files.ApplyForNode(c.Request.Context(), adapter, view.ID, id, input)
		outcome := "success"
		if err != nil {
			outcome = "failed"
		}
		recordNodeAudit(c, deps, view.ID, view.Name, "container.file."+input.Action, "container_file", id+":"+input.Path, outcome)
		if err != nil {
			fileFailure(c, err)
			return
		}
		success(c, gin.H{"path": input.Path, "target": input.Target})
	})
}
