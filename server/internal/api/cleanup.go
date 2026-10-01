package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/suma/suma/server/internal/cleanup"
	"gorm.io/gorm"
)

func cleanupActor(c *gin.Context) cleanup.Actor {
	user := currentUser(c)
	return cleanup.Actor{UserID: &user.ID, IP: requestClientIP(c)}
}
func cleanupFailure(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	message := "Unable to complete cleanup operation"
	switch {
	case errors.Is(err, cleanup.ErrConflict):
		status = http.StatusConflict
		message = err.Error()
	case errors.Is(err, cleanup.ErrConfirmation):
		status = http.StatusUnprocessableEntity
		message = err.Error()
	case errors.Is(err, cleanup.ErrInvalid):
		status = http.StatusUnprocessableEntity
		message = err.Error()
	case errors.Is(err, cleanup.ErrUnavailable):
		status = http.StatusServiceUnavailable
		message = err.Error()
	case errors.Is(err, gorm.ErrRecordNotFound):
		status = http.StatusNotFound
		message = "Cleanup record or node not found"
	}
	failure(c, status, 20601, message)
}
func bindCleanup(c *gin.Context, value any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 1024*1024))
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil {
		failure(c, 400, 20602, "Invalid cleanup request")
		return false
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		failure(c, 400, 20602, "Invalid cleanup request")
		return false
	}
	return true
}
func registerCleanupRoutes(v1 *gin.RouterGroup, deps Dependencies) {
	if deps.Cleanup == nil || deps.Nodes == nil {
		return
	}
	v1.GET("/cleanup/policies", requireAuth(deps.Auth), func(c *gin.Context) {
		nodes, err := deps.Nodes.List(c.Request.Context())
		if err != nil {
			cleanupFailure(c, err)
			return
		}
		type summary struct {
			NodeID   string       `json:"node_id"`
			NodeName string       `json:"node_name"`
			View     cleanup.View `json:"view"`
		}
		rows := []summary{}
		for _, node := range nodes {
			view, err := deps.Cleanup.Get(c.Request.Context(), node.ID, false)
			if err != nil {
				cleanupFailure(c, err)
				return
			}
			rows = append(rows, summary{NodeID: node.ID, NodeName: node.Name, View: view})
		}
		success(c, rows)
	})
	routes := v1.Group("/nodes/:nodeID/cleanup", requireAuth(deps.Auth), func(c *gin.Context) {
		if _, err := deps.Nodes.Get(c.Request.Context(), c.Param("nodeID")); err != nil {
			cleanupFailure(c, err)
			c.Abort()
			return
		}
		c.Next()
	})
	routes.GET("/policy", func(c *gin.Context) {
		view, err := deps.Cleanup.Get(c.Request.Context(), c.Param("nodeID"), true)
		if err != nil {
			cleanupFailure(c, err)
			return
		}
		success(c, view)
	})
	routes.PUT("/policy", func(c *gin.Context) {
		var input cleanup.Update
		if !bindCleanup(c, &input) {
			return
		}
		p, err := deps.Cleanup.Update(c.Request.Context(), c.Param("nodeID"), input, cleanupActor(c))
		if err != nil {
			cleanupFailure(c, err)
			return
		}
		success(c, p)
	})
	routes.POST("/preview", func(c *gin.Context) {
		var input struct{}
		if !bindCleanup(c, &input) {
			return
		}
		p, err := deps.Cleanup.Preview(c.Request.Context(), c.Param("nodeID"))
		if err != nil {
			cleanupFailure(c, err)
			return
		}
		success(c, p)
	})
	routes.POST("/run", func(c *gin.Context) {
		var input struct {
			PreviewID        string `json:"preview_id"`
			ConfirmationName string `json:"confirmation_name"`
		}
		if !bindCleanup(c, &input) {
			return
		}
		row, err := deps.Cleanup.StartRun(c.Request.Context(), c.Param("nodeID"), input.PreviewID, input.ConfirmationName, cleanupActor(c))
		if err != nil {
			cleanupFailure(c, err)
			return
		}
		c.JSON(http.StatusAccepted, envelope{Code: 0, Message: "success", Data: row})
	})
	routes.GET("/runs", func(c *gin.Context) {
		page, _ := strconv.Atoi(c.Query("page"))
		rows, err := deps.Cleanup.Runs(c.Request.Context(), c.Param("nodeID"), page, c.Query("failed") == "true")
		if err != nil {
			cleanupFailure(c, err)
			return
		}
		success(c, rows)
	})
	routes.GET("/runs/:runID", func(c *gin.Context) {
		row, err := deps.Cleanup.Run(c.Request.Context(), c.Param("nodeID"), c.Param("runID"))
		if err != nil {
			cleanupFailure(c, err)
			return
		}
		success(c, row)
	})
}
