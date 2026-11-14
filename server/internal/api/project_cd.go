package api

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/deployment"
	"github.com/suma/suma/server/internal/projectcd"
	"gorm.io/gorm"
)

func registerProjectCDRoutes(v1 *gin.RouterGroup, deps Dependencies) {
	if deps.ProjectCD == nil {
		return
	}
	routes := v1.Group("/nodes/:nodeID/projects/compose/:name/gitops", requireAuth(deps.Auth))
	routes.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		if _, err := deps.Nodes.Get(c.Request.Context(), c.Param("nodeID")); err != nil {
			failure(c, 404, 20901, "Node not found")
			c.Abort()
		}
	})
	respond := func(c *gin.Context, value any, err error) {
		if err != nil {
			status := 422
			if errors.Is(err, projectcd.ErrConflict) || errors.Is(err, compose.ErrProjectBusy) {
				status = 409
			}
			if errors.Is(err, gorm.ErrRecordNotFound) {
				status = 404
			}
			failure(c, status, 20902, err.Error())
			return
		}
		success(c, value)
	}
	actor := func(c *gin.Context) projectcd.Actor {
		a := requestActor(c)
		return projectcd.Actor{UserID: a.UserID, Name: a.Name, IP: a.IP}
	}
	routes.POST("/release-plans", func(c *gin.Context) {
		var input deployment.PrepareReleaseInput
		if c.ShouldBindJSON(&input) != nil {
			failure(c, 400, 20903, "Invalid release preparation")
			return
		}
		value, err := deps.ProjectCD.PrepareRelease(c.Request.Context(), c.Param("nodeID"), c.Param("name"), input, actor(c))
		releasePlanResponse(c, value, err, true)
	})
	routes.GET("/release-plans", func(c *gin.Context) {
		value, err := deps.ProjectCD.PreparedRelease(c.Request.Context(), c.Param("nodeID"), c.Param("name"), c.Query("task_id"))
		releasePlanResponse(c, value, err, false)
	})
	routes.GET("/release-plans/:planID", func(c *gin.Context) {
		value, err := deps.ProjectCD.ReleasePlan(c.Request.Context(), c.Param("nodeID"), c.Param("name"), c.Param("planID"))
		releasePlanResponse(c, value, err, false)
	})
	routes.POST("/release-plans/:planID/confirm", func(c *gin.Context) {
		var input deployment.ConfirmReleaseInput
		if c.ShouldBindJSON(&input) != nil {
			failure(c, 400, 20903, "Invalid release confirmation")
			return
		}
		value, err := deps.ProjectCD.ConfirmRelease(c.Request.Context(), c.Param("nodeID"), c.Param("name"), c.Param("planID"), input, actor(c))
		releasePlanResponse(c, value, err, true)
	})
	routes.GET("", func(c *gin.Context) {
		value, err := deps.ProjectCD.Get(c.Request.Context(), c.Param("nodeID"), c.Param("name"))
		respond(c, value, err)
	})
	routes.PUT("", func(c *gin.Context) {
		var input projectcd.ConfigureInput
		if c.ShouldBindJSON(&input) != nil {
			failure(c, 400, 20903, "Invalid GitOps configuration")
			return
		}
		value, err := deps.ProjectCD.Configure(c.Request.Context(), c.Param("nodeID"), c.Param("name"), input)
		if err == nil {
			recordNodeAudit(c, deps, c.Param("nodeID"), "", "project.cd.configure", "project", c.Param("name"), "success")
		}
		respond(c, value, err)
	})
	routes.POST("/test-connection", func(c *gin.Context) {
		var input projectcd.ConfigureInput
		if c.ShouldBindJSON(&input) != nil {
			failure(c, 400, 20903, "Invalid Git repository")
			return
		}
		value, err := deps.ProjectCD.TestConnection(c.Request.Context(), c.Param("nodeID"), c.Param("name"), input.Repository)
		respond(c, value, err)
	})
	routes.POST("/check", func(c *gin.Context) {
		value, err := deps.ProjectCD.Check(c.Request.Context(), c.Param("nodeID"), c.Param("name"), "manual", actor(c))
		respond(c, value, err)
	})
	routes.GET("/drift", func(c *gin.Context) {
		value, err := deps.ProjectCD.Drift(c.Request.Context(), c.Param("nodeID"), c.Param("name"))
		respond(c, value, err)
	})
	routes.GET("/preview", func(c *gin.Context) {
		value, err := deps.ProjectCD.Preview(c.Request.Context(), c.Param("nodeID"), c.Param("name"))
		respond(c, value, err)
	})
	type versioned struct {
		ExpectedRevision uint64                           `json:"expected_revision"`
		CandidateID      uint                             `json:"candidate_id"`
		Enabled          bool                             `json:"enabled"`
		Environment      []deployment.EnvironmentVariable `json:"environment"`
	}
	routes.POST("/apply", func(c *gin.Context) {
		var input versioned
		if c.ShouldBindJSON(&input) != nil || input.CandidateID == 0 {
			failure(c, 400, 20903, "Candidate and revision are required")
			return
		}
		value, err := deps.ProjectCD.Apply(c.Request.Context(), c.Param("nodeID"), c.Param("name"), input.CandidateID, input.ExpectedRevision, false, actor(c))
		respond(c, value, err)
	})
	routes.PUT("/automation", func(c *gin.Context) {
		var input versioned
		if c.ShouldBindJSON(&input) != nil {
			failure(c, 400, 20903, "Invalid automation setting")
			return
		}
		value, err := deps.ProjectCD.SetAutomation(c.Request.Context(), c.Param("nodeID"), c.Param("name"), input.Enabled, input.ExpectedRevision)
		if err == nil {
			recordNodeAudit(c, deps, c.Param("nodeID"), "", "project.cd.automation", "project", c.Param("name"), "success")
		}
		respond(c, value, err)
	})
	routes.GET("/environment", func(c *gin.Context) {
		value, err := deps.ProjectCD.Get(c.Request.Context(), c.Param("nodeID"), c.Param("name"))
		respond(c, value.Environment, err)
	})
	routes.GET("/environment/values", func(c *gin.Context) {
		value, err := deps.ProjectCD.Environment(c.Request.Context(), c.Param("nodeID"), c.Param("name"))
		if err == nil {
			recordNodeAudit(c, deps, c.Param("nodeID"), "", "project.cd.environment.read", "project", c.Param("name"), "success")
		}
		respond(c, value, err)
	})
	routes.PUT("/environment", func(c *gin.Context) {
		var input versioned
		if c.ShouldBindJSON(&input) != nil || input.Environment == nil {
			failure(c, 400, 20903, "Environment list and revision are required")
			return
		}
		value, err := deps.ProjectCD.SetEnvironment(c.Request.Context(), c.Param("nodeID"), c.Param("name"), input.ExpectedRevision, input.Environment)
		if err == nil {
			recordNodeAudit(c, deps, c.Param("nodeID"), "", "project.cd.environment.update", "project", c.Param("name"), "success")
		}
		respond(c, value, err)
	})
	routes.GET("/records", func(c *gin.Context) {
		page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
		rows, total, err := deps.ProjectCD.Records(c.Request.Context(), c.Param("nodeID"), c.Param("name"), page, c.Query("include_checks") == "true")
		respond(c, gin.H{"items": rows, "total": total}, err)
	})
	routes.POST("/records/:recordID/rollback", func(c *gin.Context) {
		id, err := strconv.ParseUint(c.Param("recordID"), 10, 32)
		var input versioned
		if err != nil || id == 0 || c.ShouldBindJSON(&input) != nil {
			failure(c, 400, 20903, "Record and revision are required")
			return
		}
		value, err := deps.ProjectCD.Apply(c.Request.Context(), c.Param("nodeID"), c.Param("name"), uint(id), input.ExpectedRevision, true, actor(c))
		respond(c, value, err)
	})
	routes.POST("/detach", func(c *gin.Context) {
		var input versioned
		if c.ShouldBindJSON(&input) != nil {
			failure(c, 400, 20903, "Revision is required")
			return
		}
		value, err := deps.ProjectCD.Detach(c.Request.Context(), c.Param("nodeID"), c.Param("name"), input.ExpectedRevision, actor(c))
		respond(c, value, err)
	})
	v1.POST("/webhooks/gitops/:hookID", func(c *gin.Context) {
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, (2<<20)+1))
		if err != nil {
			failure(c, 400, 20903, "Invalid webhook body")
			return
		}
		ignored, err := deps.ProjectCD.Webhook(c.Request.Context(), c.Param("hookID"), c.Request.Header, body)
		if err != nil {
			status := http.StatusUnprocessableEntity
			if errors.Is(err, deployment.ErrWebhookSignature) {
				status = 401
			}
			if errors.Is(err, gorm.ErrRecordNotFound) {
				status = 404
			}
			failure(c, status, 20904, "GitOps webhook rejected")
			return
		}
		success(c, gin.H{"ignored": ignored})
	})
}
