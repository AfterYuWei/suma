package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/deployment"
	"gorm.io/gorm"
)

func releasePlanResponse(c *gin.Context, value any, err error, accepted bool) {
	c.Header("Cache-Control", "no-store")
	if err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, deployment.ErrPlanChanged) || errors.Is(err, deployment.ErrPlanBusy) || errors.Is(err, compose.ErrProjectBusy) {
			status = http.StatusConflict
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			status = http.StatusNotFound
		}
		failure(c, status, 17730, err.Error())
		return
	}
	if accepted {
		c.JSON(http.StatusAccepted, envelope{Code: 0, Message: "success", Data: value})
		return
	}
	success(c, value)
}

func registerBatchReleasePlans(routes *gin.RouterGroup, deps Dependencies) {
	routes.POST("/:name/release-plans", func(c *gin.Context) {
		var input deployment.PrepareReleaseInput
		if c.ShouldBindJSON(&input) != nil {
			failure(c, 400, 17731, "Invalid release preparation")
			return
		}
		value, err := deps.CD.PrepareRelease(c.Request.Context(), c.Param("name"), input, requestActor(c))
		releasePlanResponse(c, value, err, true)
	})
	routes.GET("/:name/release-plans", func(c *gin.Context) {
		value, err := deps.CD.PreparedRelease(c.Request.Context(), c.Param("name"), c.Query("task_id"))
		releasePlanResponse(c, value, err, false)
	})
	routes.GET("/:name/release-plans/:planID", func(c *gin.Context) {
		value, err := deps.CD.ReleasePlan(c.Request.Context(), c.Param("name"), c.Param("planID"))
		releasePlanResponse(c, value, err, false)
	})
	routes.POST("/:name/release-plans/:planID/confirm", func(c *gin.Context) {
		var input deployment.ConfirmReleaseInput
		if c.ShouldBindJSON(&input) != nil {
			failure(c, 400, 17731, "Invalid release confirmation")
			return
		}
		value, err := deps.CD.ConfirmRelease(c.Request.Context(), c.Param("name"), c.Param("planID"), input, requestActor(c))
		releasePlanResponse(c, value, err, true)
	})
}
