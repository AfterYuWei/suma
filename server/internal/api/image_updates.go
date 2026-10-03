package api

import (
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/suma/suma/server/internal/imageupdate"
	"gorm.io/gorm"
	"net/http"
)

func imageUpdateFailure(c *gin.Context, err error) {
	var busy *imageupdate.BusyError
	if errors.As(err, &busy) {
		c.JSON(http.StatusConflict, envelope{Code: 20701, Message: busy.Error(), Data: gin.H{"task_id": busy.TaskID}})
		return
	}
	status := http.StatusUnprocessableEntity
	message := "Invalid image detection request or credential selection"
	if errors.Is(err, imageupdate.ErrConflict) {
		status = http.StatusConflict
		message = err.Error()
	}
	if errors.Is(err, imageupdate.ErrUnavailable) {
		status = http.StatusServiceUnavailable
		message = err.Error()
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		status = http.StatusNotFound
		message = "Docker node not found"
	}
	failure(c, status, 20702, message)
}
func registerImageUpdateRoutes(v1 *gin.RouterGroup, deps Dependencies) {
	if deps.ImageUpdates == nil {
		return
	}
	routes := v1.Group("/nodes/:nodeID/image-updates", requireAuth(deps.Auth))
	actor := func(c *gin.Context) imageupdate.Actor {
		u := currentUser(c)
		return imageupdate.Actor{UserID: &u.ID, IP: requestClientIP(c)}
	}
	routes.GET("", func(c *gin.Context) {
		rows, err := deps.ImageUpdates.View(c.Request.Context(), c.Param("nodeID"), c.Query("project_name"))
		if err != nil {
			imageUpdateFailure(c, err)
			return
		}
		success(c, rows)
	})
	routes.POST("/check", func(c *gin.Context) {
		var input imageupdate.CheckInput
		if !bindCleanup(c, &input) {
			return
		}
		row, err := deps.ImageUpdates.Check(c.Request.Context(), c.Param("nodeID"), input, actor(c))
		if err != nil {
			imageUpdateFailure(c, err)
			return
		}
		c.JSON(http.StatusAccepted, envelope{Code: 0, Message: "success", Data: row})
	})
	routes.GET("/policy", func(c *gin.Context) {
		row, err := deps.ImageUpdates.Policy(c.Request.Context(), c.Param("nodeID"))
		if err != nil {
			imageUpdateFailure(c, err)
			return
		}
		success(c, row)
	})
	routes.PUT("/policy", func(c *gin.Context) {
		var input imageupdate.PolicyInput
		if !bindCleanup(c, &input) {
			return
		}
		row, err := deps.ImageUpdates.UpdatePolicy(c.Request.Context(), c.Param("nodeID"), input, actor(c))
		if err != nil {
			imageUpdateFailure(c, err)
			return
		}
		success(c, row)
	})
}
