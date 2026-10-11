package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// Independent reads let a card render engine information without waiting for
// container sampling or disk accounting. The deadline also covers resolution.
func registerNodeOverviewRoutes(resources *gin.RouterGroup, deps Dependencies) {
	group := resources.Group("/overview", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	group.GET("/info", func(c *gin.Context) {
		if _, err := deps.Nodes.Get(c.Request.Context(), c.Param("nodeID")); err != nil {
			failure(c, http.StatusNotFound, 20004, "Docker node not found")
			return
		}
		info, err := deps.Nodes.OverviewInfo(c.Request.Context(), c.Param("nodeID"))
		if err != nil {
			failure(c, http.StatusServiceUnavailable, 20202, "Unable to read node information")
			return
		}
		success(c, info)
	})
	group.GET("/resources", func(c *gin.Context) {
		if _, err := deps.Nodes.Get(c.Request.Context(), c.Param("nodeID")); err != nil {
			failure(c, http.StatusNotFound, 20004, "Docker node not found")
			return
		}
		counts, err := deps.Nodes.OverviewResources(c.Request.Context(), c.Param("nodeID"))
		if err != nil {
			failure(c, http.StatusServiceUnavailable, 20202, "Unable to read node resource counts")
			return
		}
		success(c, counts)
	})
	group.GET("/disk", func(c *gin.Context) {
		if _, err := deps.Nodes.Get(c.Request.Context(), c.Param("nodeID")); err != nil {
			failure(c, http.StatusNotFound, 20004, "Docker node not found")
			return
		}
		usage, err := deps.Nodes.OverviewDiskUsage(c.Request.Context(), c.Param("nodeID"))
		if err != nil {
			failure(c, http.StatusServiceUnavailable, 20202, "Unable to read Docker disk usage")
			return
		}
		success(c, gin.H{"docker_disk_usage_bytes": usage})
	})
	group.GET("/metrics", func(c *gin.Context) {
		size := c.DefaultQuery("size", "medium")
		if size != "small" && size != "medium" && size != "large" {
			failure(c, http.StatusBadRequest, 20102, "Invalid overview card size")
			return
		}
		if _, err := deps.Nodes.Get(c.Request.Context(), c.Param("nodeID")); err != nil {
			failure(c, http.StatusNotFound, 20004, "Docker node not found")
			return
		}
		result, err := deps.Nodes.OverviewMetrics(c.Request.Context(), c.Param("nodeID"), size)
		if err != nil {
			failure(c, http.StatusServiceUnavailable, 20202, "Unable to read container metrics")
			return
		}
		success(c, result)
	})
}
