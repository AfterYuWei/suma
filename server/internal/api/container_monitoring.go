package api

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	monitor "github.com/suma/suma/server/internal/containermonitor"
	"gorm.io/gorm"
)

func monitoringFailure(c *gin.Context, err error) {
	switch {
	case errors.Is(err, monitor.ErrInput):
		failure(c, 400, 21201, "Invalid monitoring request")
	case errors.Is(err, monitor.ErrConfirmation):
		failure(c, 409, 21202, "Confirm the monitoring history range to be removed")
	case errors.Is(err, monitor.ErrConflict):
		failure(c, 409, 21203, "Monitoring settings changed; reload before saving")
	case errors.Is(err, gorm.ErrRecordNotFound):
		failure(c, 404, 21204, "Monitoring history not found")
	default:
		failure(c, 503, 21205, "Container monitoring unavailable")
	}
}
func monitoringTime(c *gin.Context, key string, fallback time.Time) (time.Time, bool) {
	if c.Query(key) == "" {
		return fallback, true
	}
	value, err := time.Parse(time.RFC3339Nano, c.Query(key))
	if err != nil {
		monitoringFailure(c, monitor.ErrInput)
		return time.Time{}, false
	}
	return value, true
}
func registerMonitoringRoutes(v1 *gin.RouterGroup, deps Dependencies) {
	group := v1.Group("/monitoring", requireAuth(deps.Auth))
	group.Use(func(c *gin.Context) {
		if deps.ContainerMonitoring == nil {
			monitoringFailure(c, errors.New("unavailable"))
			c.Abort()
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	group.GET("/settings", func(c *gin.Context) { success(c, deps.ContainerMonitoring.Settings()) })
	group.PUT("/settings", func(c *gin.Context) {
		var input monitor.SavePolicy
		if err := c.ShouldBindJSON(&input); err != nil {
			monitoringFailure(c, monitor.ErrInput)
			return
		}
		policy, taskID, err := deps.ContainerMonitoring.SaveSettings(c.Request.Context(), input)
		if err != nil {
			monitoringFailure(c, err)
			return
		}
		recordAudit(c, deps.Audit, "monitoring.settings.update", "monitoring", "containers", "success")
		success(c, gin.H{"settings": policy, "task_id": taskID})
	})
	group.GET("/status", func(c *gin.Context) { success(c, deps.ContainerMonitoring.Status()) })
	group.GET("/instances", func(c *gin.Context) {
		now := time.Now().UTC()
		since, ok := monitoringTime(c, "since", time.Time{})
		if !ok {
			return
		}
		until, ok := monitoringTime(c, "until", now)
		if !ok {
			return
		}
		page, limit := 1, 50
		var err error
		if c.Query("page") != "" {
			page, err = strconv.Atoi(c.Query("page"))
			if err != nil || page < 1 {
				monitoringFailure(c, monitor.ErrInput)
				return
			}
		}
		if c.Query("limit") != "" {
			limit, err = strconv.Atoi(c.Query("limit"))
			if err != nil {
				monitoringFailure(c, monitor.ErrInput)
				return
			}
		}
		if !since.IsZero() && !until.After(since) {
			monitoringFailure(c, monitor.ErrInput)
			return
		}
		result, err := deps.ContainerMonitoring.Instances(c.Request.Context(), monitor.InstanceQuery{Search: c.Query("search"), NodeID: c.Query("node_id"), ContainerID: c.Query("container_id"), Since: since, Until: until, Page: page, Limit: limit})
		if err != nil {
			monitoringFailure(c, err)
			return
		}
		success(c, result)
	})
	group.GET("/instances/:instanceID", func(c *gin.Context) {
		result, err := deps.ContainerMonitoring.Instance(c.Request.Context(), c.Param("instanceID"))
		if err != nil {
			monitoringFailure(c, err)
			return
		}
		success(c, result)
	})
	group.GET("/instances/:instanceID/events", func(c *gin.Context) {
		now := time.Now().UTC()
		since, ok := monitoringTime(c, "since", now.Add(-time.Hour))
		if !ok {
			return
		}
		until, ok := monitoringTime(c, "until", now)
		if !ok {
			return
		}
		limit := 200
		var err error
		if c.Query("limit") != "" {
			limit, err = strconv.Atoi(c.Query("limit"))
			if err != nil {
				monitoringFailure(c, monitor.ErrInput)
				return
			}
		}
		result, err := deps.ContainerMonitoring.Events(c.Request.Context(), c.Param("instanceID"), since, until, c.Query("kind"), c.Query("cursor"), limit)
		if err != nil {
			monitoringFailure(c, err)
			return
		}
		success(c, result)
	})
	group.POST("/query", func(c *gin.Context) {
		var input monitor.Query
		if err := c.ShouldBindJSON(&input); err != nil {
			monitoringFailure(c, monitor.ErrInput)
			return
		}
		result, err := deps.ContainerMonitoring.Query(c.Request.Context(), input)
		if err != nil {
			monitoringFailure(c, err)
			return
		}
		success(c, result)
	})
}
