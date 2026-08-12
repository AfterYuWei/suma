package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/dockport/dockport/server/internal/audit"
	"github.com/dockport/dockport/server/internal/auth"
	composeService "github.com/dockport/dockport/server/internal/compose"
	containerdomain "github.com/dockport/dockport/server/internal/container"
	"github.com/dockport/dockport/server/internal/docker"
	imageService "github.com/dockport/dockport/server/internal/image"
	monitorService "github.com/dockport/dockport/server/internal/monitor"
	networkService "github.com/dockport/dockport/server/internal/network"
	settingsService "github.com/dockport/dockport/server/internal/settings"
	systemService "github.com/dockport/dockport/server/internal/system"
	"github.com/dockport/dockport/server/internal/task"
	volumeService "github.com/dockport/dockport/server/internal/volume"
	"github.com/dockport/dockport/server/webui"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

const sessionCookie = "dockport_session"

type Dependencies struct {
	Engine       docker.Engine
	Containers   containerdomain.Service
	Auth         *auth.Service
	Audit        *audit.Service
	Tasks        *task.Service
	Images       *imageService.Service
	Networks     networkService.Service
	Volumes      volumeService.Service
	Compose      *composeService.Service
	Settings     *settingsService.Service
	Monitor      *monitorService.Service
	System       *systemService.Service
	CookieSecure bool
}
type credentials struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}
type initializeRequest struct {
	Username        string `json:"username" binding:"required"`
	Password        string `json:"password" binding:"required"`
	ConfirmPassword string `json:"confirm_password" binding:"required"`
}

func NewRouter(deps Dependencies) *gin.Engine {
	router := gin.New()
	router.Use(gin.Recovery(), requestID(), securityHeaders())
	v1 := router.Group("/api/v1")

	v1.GET("/auth/status", func(c *gin.Context) {
		needsSetup, err := deps.Auth.NeedsSetup(c.Request.Context())
		if err != nil {
			failure(c, http.StatusInternalServerError, 11000, "Unable to read setup status")
			return
		}
		success(c, gin.H{"needs_setup": needsSetup})
	})
	v1.POST("/auth/initialize", func(c *gin.Context) {
		var input initializeRequest
		if c.ShouldBindJSON(&input) != nil {
			failure(c, http.StatusBadRequest, 11001, "Username and password are required")
			return
		}
		if input.Password != input.ConfirmPassword {
			failure(c, http.StatusBadRequest, 11002, "Passwords do not match")
			return
		}
		user, err := deps.Auth.Initialize(c.Request.Context(), input.Username, input.Password)
		if err != nil {
			failure(c, http.StatusConflict, 11003, err.Error())
			return
		}
		c.JSON(http.StatusCreated, envelope{Code: 0, Message: "success", Data: user})
	})
	v1.POST("/auth/login", func(c *gin.Context) {
		var input credentials
		if c.ShouldBindJSON(&input) != nil {
			failure(c, http.StatusBadRequest, 11001, "Username and password are required")
			return
		}
		token, user, err := deps.Auth.Login(c.Request.Context(), input.Username, input.Password, c.ClientIP())
		if err != nil {
			failure(c, http.StatusUnauthorized, 11004, "Invalid username or password")
			return
		}
		setSessionCookie(c, token, deps.CookieSecure)
		_ = deps.Audit.Record(c.Request.Context(), &user.ID, "login", "session", user.Username, c.ClientIP(), "success")
		success(c, user)
	})
	v1.POST("/auth/logout", func(c *gin.Context) {
		token, _ := c.Cookie(sessionCookie)
		if err := deps.Auth.Logout(c.Request.Context(), token); err != nil {
			failure(c, http.StatusInternalServerError, 11005, "Unable to log out")
			return
		}
		clearSessionCookie(c, deps.CookieSecure)
		success(c, gin.H{})
	})
	v1.GET("/auth/session", requireAuth(deps.Auth), func(c *gin.Context) { user, _ := c.Get("user"); success(c, user) })

	v1.GET("/health", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if err := deps.Engine.Ping(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, envelope{Code: 10001, Message: "Docker unavailable", Data: gin.H{"status": "degraded", "docker": "unavailable"}})
			return
		}
		success(c, gin.H{"status": "ok", "docker": "connected"})
	})
	v1.GET("/docker/info", requireAuth(deps.Auth), func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		info, err := deps.Engine.Info(ctx)
		if err != nil {
			failure(c, http.StatusServiceUnavailable, 10002, "Unable to read Docker information")
			return
		}
		success(c, info)
	})
	v1.GET("/overview", requireAuth(deps.Auth), func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		value, err := deps.Monitor.Overview(ctx)
		if err != nil {
			failure(c, http.StatusServiceUnavailable, 10003, "Unable to read host overview")
			return
		}
		success(c, value)
	})
	containers := v1.Group("/containers", requireAuth(deps.Auth))
	containers.GET("", func(c *gin.Context) {
		rows, err := deps.Containers.List(c.Request.Context())
		if err != nil {
			failure(c, http.StatusServiceUnavailable, 12001, "Unable to list containers")
			return
		}
		success(c, rows)
	})
	containers.GET("/metrics", func(c *gin.Context) {
		rows, err := deps.Containers.Metrics(c.Request.Context())
		if err != nil {
			failure(c, http.StatusServiceUnavailable, 12008, "Unable to read container metrics")
			return
		}
		success(c, rows)
	})
	containers.POST("/batch", func(c *gin.Context) {
		var input struct {
			IDs           []string `json:"ids" binding:"required"`
			Action        string   `json:"action" binding:"required"`
			RemoveVolumes bool     `json:"remove_volumes"`
		}
		if c.ShouldBindJSON(&input) != nil || len(input.IDs) == 0 || len(input.IDs) > 100 {
			failure(c, http.StatusBadRequest, 12009, "Between 1 and 100 container IDs are required")
			return
		}
		allowed := map[string]bool{"start": true, "stop": true, "restart": true, "pause": true, "unpause": true, "kill": true, "remove": true}
		if !allowed[input.Action] {
			failure(c, http.StatusBadRequest, 12010, "Unsupported batch action")
			return
		}
		type batchResult struct {
			ID      string `json:"id"`
			Success bool   `json:"success"`
		}
		results := make([]batchResult, 0, len(input.IDs))
		for _, id := range input.IDs {
			id = strings.TrimSpace(id)
			if id == "" {
				results = append(results, batchResult{ID: id, Success: false})
				continue
			}
			var err error
			switch input.Action {
			case "start":
				err = deps.Containers.Start(c.Request.Context(), id)
			case "stop":
				err = deps.Containers.Stop(c.Request.Context(), id)
			case "restart":
				err = deps.Containers.Restart(c.Request.Context(), id)
			case "pause":
				err = deps.Containers.Pause(c.Request.Context(), id)
			case "unpause":
				err = deps.Containers.Unpause(c.Request.Context(), id)
			case "kill":
				err = deps.Containers.Kill(c.Request.Context(), id)
			case "remove":
				err = deps.Containers.Remove(c.Request.Context(), id, input.RemoveVolumes)
			}
			result := "success"
			if err != nil {
				result = "failed"
			}
			recordAudit(c, deps.Audit, "container."+input.Action, "container", id, result)
			results = append(results, batchResult{ID: id, Success: err == nil})
		}
		success(c, gin.H{"action": input.Action, "results": results})
	})
	containers.GET("/:id", func(c *gin.Context) {
		row, err := deps.Containers.Get(c.Request.Context(), c.Param("id"))
		if err != nil {
			failure(c, http.StatusNotFound, 12002, "Container not found")
			return
		}
		success(c, row)
	})
	containers.POST("/:id/:action", func(c *gin.Context) {
		id, action := c.Param("id"), c.Param("action")
		var err error
		switch action {
		case "start":
			err = deps.Containers.Start(c.Request.Context(), id)
		case "stop":
			err = deps.Containers.Stop(c.Request.Context(), id)
		case "restart":
			err = deps.Containers.Restart(c.Request.Context(), id)
		case "pause":
			err = deps.Containers.Pause(c.Request.Context(), id)
		case "unpause":
			err = deps.Containers.Unpause(c.Request.Context(), id)
		case "kill":
			err = deps.Containers.Kill(c.Request.Context(), id)
		default:
			failure(c, http.StatusNotFound, 12003, "Unknown container action")
			return
		}
		result := "success"
		if err != nil {
			result = "failed"
		}
		recordAudit(c, deps.Audit, "container."+action, "container", id, result)
		if err != nil {
			failure(c, http.StatusConflict, 12004, "Container action failed")
			return
		}
		success(c, gin.H{"id": id, "action": action})
	})
	containers.PATCH("/:id", func(c *gin.Context) {
		var input struct {
			Name string `json:"name" binding:"required"`
		}
		if c.ShouldBindJSON(&input) != nil || strings.TrimSpace(input.Name) == "" {
			failure(c, http.StatusBadRequest, 12005, "Container name is required")
			return
		}
		if err := deps.Containers.Rename(c.Request.Context(), c.Param("id"), strings.TrimSpace(input.Name)); err != nil {
			recordAudit(c, deps.Audit, "container.rename", "container", c.Param("id"), "failed")
			failure(c, http.StatusConflict, 12006, "Unable to rename container")
			return
		}
		recordAudit(c, deps.Audit, "container.rename", "container", c.Param("id"), "success")
		success(c, gin.H{"id": c.Param("id"), "name": strings.TrimSpace(input.Name)})
	})
	containers.DELETE("/:id", func(c *gin.Context) {
		if err := deps.Containers.Remove(c.Request.Context(), c.Param("id"), c.Query("remove_volumes") == "true"); err != nil {
			recordAudit(c, deps.Audit, "container.remove", "container", c.Param("id"), "failed")
			failure(c, http.StatusConflict, 12007, "Unable to remove container")
			return
		}
		recordAudit(c, deps.Audit, "container.remove", "container", c.Param("id"), "success")
		success(c, gin.H{"id": c.Param("id")})
	})
	images := v1.Group("/images", requireAuth(deps.Auth))
	images.GET("", func(c *gin.Context) {
		rows, err := deps.Images.List(c.Request.Context())
		if err != nil {
			failure(c, http.StatusServiceUnavailable, 13001, "Unable to list images")
			return
		}
		success(c, rows)
	})
	images.GET("/:id", func(c *gin.Context) {
		row, err := deps.Images.Get(c.Request.Context(), c.Param("id"))
		if err != nil {
			failure(c, http.StatusNotFound, 13002, "Image not found")
			return
		}
		success(c, row)
	})
	images.POST("/pull", func(c *gin.Context) {
		var input struct {
			Reference string `json:"reference" binding:"required"`
		}
		if c.ShouldBindJSON(&input) != nil {
			failure(c, http.StatusBadRequest, 13003, "Image reference is required")
			return
		}
		row, err := deps.Images.Pull(input.Reference)
		if err != nil {
			failure(c, http.StatusInternalServerError, 13004, "Unable to start image pull")
			return
		}
		recordAudit(c, deps.Audit, "image.pull", "image", input.Reference, "success")
		c.JSON(http.StatusAccepted, envelope{Code: 0, Message: "success", Data: row})
	})
	images.POST("/:id/tag", func(c *gin.Context) {
		var input struct {
			Reference string `json:"reference" binding:"required"`
		}
		if c.ShouldBindJSON(&input) != nil {
			failure(c, http.StatusBadRequest, 13003, "Image reference is required")
			return
		}
		if err := deps.Images.Tag(c.Request.Context(), c.Param("id"), input.Reference); err != nil {
			failure(c, http.StatusConflict, 13005, "Unable to tag image")
			return
		}
		recordAudit(c, deps.Audit, "image.tag", "image", input.Reference, "success")
		success(c, gin.H{"reference": input.Reference})
	})
	images.DELETE("/:id", func(c *gin.Context) {
		err := deps.Images.Remove(c.Request.Context(), c.Param("id"), c.Query("force") == "true")
		result := "success"
		if err != nil {
			result = "failed"
		}
		recordAudit(c, deps.Audit, "image.remove", "image", c.Param("id"), result)
		if err != nil {
			failure(c, http.StatusConflict, 13006, "Unable to remove image")
			return
		}
		success(c, gin.H{"id": c.Param("id")})
	})
	networks := v1.Group("/networks", requireAuth(deps.Auth))
	networks.GET("", func(c *gin.Context) {
		rows, err := deps.Networks.ListNetworks(c.Request.Context())
		if err != nil {
			failure(c, http.StatusServiceUnavailable, 14001, "Unable to list networks")
			return
		}
		success(c, rows)
	})
	networks.GET("/:id", func(c *gin.Context) {
		row, err := deps.Networks.InspectNetwork(c.Request.Context(), c.Param("id"))
		if err != nil {
			failure(c, http.StatusNotFound, 14002, "Network not found")
			return
		}
		success(c, row)
	})
	networks.POST("", func(c *gin.Context) {
		var input networkService.CreateRequest
		if c.ShouldBindJSON(&input) != nil || strings.TrimSpace(input.Name) == "" {
			failure(c, http.StatusBadRequest, 14003, "Network name is required")
			return
		}
		row, err := deps.Networks.CreateNetwork(c.Request.Context(), input)
		if err != nil {
			failure(c, http.StatusConflict, 14004, "Unable to create network")
			return
		}
		recordAudit(c, deps.Audit, "network.create", "network", row.Name, "success")
		c.JSON(http.StatusCreated, envelope{Code: 0, Message: "success", Data: row})
	})
	networks.DELETE("/:id", func(c *gin.Context) {
		if c.Query("confirm") != "true" {
			failure(c, http.StatusBadRequest, 14005, "Network deletion must be confirmed")
			return
		}
		err := deps.Networks.RemoveNetwork(c.Request.Context(), c.Param("id"))
		result := "success"
		if err != nil {
			result = "failed"
		}
		recordAudit(c, deps.Audit, "network.remove", "network", c.Param("id"), result)
		if err != nil {
			failure(c, http.StatusConflict, 14006, "Unable to remove network")
			return
		}
		success(c, gin.H{"id": c.Param("id")})
	})
	volumes := v1.Group("/volumes", requireAuth(deps.Auth))
	volumes.GET("", func(c *gin.Context) {
		rows, err := deps.Volumes.ListVolumes(c.Request.Context())
		if err != nil {
			failure(c, http.StatusServiceUnavailable, 15001, "Unable to list volumes")
			return
		}
		success(c, rows)
	})
	volumes.GET("/:id", func(c *gin.Context) {
		row, err := deps.Volumes.InspectVolume(c.Request.Context(), c.Param("id"))
		if err != nil {
			failure(c, http.StatusNotFound, 15002, "Volume not found")
			return
		}
		success(c, row)
	})
	volumes.POST("", func(c *gin.Context) {
		var input volumeService.CreateRequest
		if c.ShouldBindJSON(&input) != nil || strings.TrimSpace(input.Name) == "" {
			failure(c, http.StatusBadRequest, 15003, "Volume name is required")
			return
		}
		row, err := deps.Volumes.CreateVolume(c.Request.Context(), input)
		if err != nil {
			failure(c, http.StatusConflict, 15004, "Unable to create volume")
			return
		}
		recordAudit(c, deps.Audit, "volume.create", "volume", row.Name, "success")
		c.JSON(http.StatusCreated, envelope{Code: 0, Message: "success", Data: row})
	})
	volumes.DELETE("/:id", func(c *gin.Context) {
		if c.Query("confirm") != c.Param("id") {
			failure(c, http.StatusBadRequest, 15005, "Type the volume name to confirm permanent data loss")
			return
		}
		err := deps.Volumes.RemoveVolume(c.Request.Context(), c.Param("id"))
		result := "success"
		if err != nil {
			result = "failed"
		}
		recordAudit(c, deps.Audit, "volume.remove", "volume", c.Param("id"), result)
		if err != nil {
			message := "Unable to remove volume"
			if err == volumeService.ErrInUse {
				message = "Volume is used by a container"
			}
			failure(c, http.StatusConflict, 15006, message)
			return
		}
		success(c, gin.H{"id": c.Param("id")})
	})
	compose := v1.Group("/compose", requireAuth(deps.Auth))
	compose.GET("", func(c *gin.Context) {
		rows, err := deps.Compose.List(c.Request.Context())
		if err != nil {
			failure(c, http.StatusInternalServerError, 18001, "Unable to list Compose projects")
			return
		}
		success(c, rows)
	})
	compose.GET("/:name", func(c *gin.Context) {
		row, err := deps.Compose.Get(c.Request.Context(), c.Param("name"))
		if err != nil {
			failure(c, http.StatusNotFound, 18002, "Compose project not found")
			return
		}
		success(c, row)
	})
	compose.GET("/:name/services", func(c *gin.Context) {
		rows, err := deps.Compose.Services(c.Request.Context(), c.Param("name"))
		if err != nil {
			failure(c, http.StatusNotFound, 18011, "Unable to list Compose services")
			return
		}
		success(c, rows)
	})
	compose.GET("/:name/logs", func(c *gin.Context) {
		value, err := deps.Compose.Logs(c.Request.Context(), c.Param("name"))
		if err != nil {
			failure(c, http.StatusConflict, 18012, value)
			return
		}
		success(c, gin.H{"logs": value})
	})
	compose.POST("", func(c *gin.Context) {
		var input struct {
			Name        string `json:"name" binding:"required"`
			Compose     string `json:"compose" binding:"required"`
			Environment string `json:"environment"`
		}
		if c.ShouldBindJSON(&input) != nil {
			failure(c, http.StatusBadRequest, 18003, "Project name and Compose YAML are required")
			return
		}
		row, err := deps.Compose.Create(c.Request.Context(), input.Name, input.Compose, input.Environment)
		if err != nil {
			failure(c, http.StatusConflict, 18004, err.Error())
			return
		}
		recordAudit(c, deps.Audit, "compose.create", "compose", input.Name, "success")
		c.JSON(http.StatusCreated, envelope{Code: 0, Message: "success", Data: row})
	})
	compose.PUT("/:name", func(c *gin.Context) {
		var input struct {
			Compose     string `json:"compose" binding:"required"`
			Environment string `json:"environment"`
		}
		if c.ShouldBindJSON(&input) != nil {
			failure(c, http.StatusBadRequest, 18003, "Compose YAML is required")
			return
		}
		row, err := deps.Compose.Save(c.Request.Context(), c.Param("name"), input.Compose, input.Environment)
		if err != nil {
			failure(c, http.StatusConflict, 18005, "Unable to save Compose project")
			return
		}
		recordAudit(c, deps.Audit, "compose.save", "compose", c.Param("name"), "success")
		success(c, row)
	})
	compose.DELETE("/:name", func(c *gin.Context) {
		if c.Query("confirm") != c.Param("name") {
			failure(c, http.StatusBadRequest, 18009, "Type the project name to confirm removal")
			return
		}
		if err := deps.Compose.Remove(c.Request.Context(), c.Param("name")); err != nil {
			failure(c, http.StatusConflict, 18010, "Unable to remove Compose project")
			return
		}
		recordAudit(c, deps.Audit, "compose.remove", "compose", c.Param("name"), "success")
		success(c, gin.H{"name": c.Param("name")})
	})
	compose.POST("/:name/validate", func(c *gin.Context) {
		var input struct {
			Compose     string `json:"compose" binding:"required"`
			Environment string `json:"environment"`
		}
		if c.ShouldBindJSON(&input) != nil {
			failure(c, http.StatusBadRequest, 18003, "Compose YAML is required")
			return
		}
		if err := deps.Compose.Validate(c.Request.Context(), c.Param("name"), input.Compose, input.Environment); err != nil {
			failure(c, http.StatusUnprocessableEntity, 18006, err.Error())
			return
		}
		success(c, gin.H{"valid": true})
	})
	compose.POST("/:name/:action", func(c *gin.Context) {
		action := c.Param("action")
		allowed := map[string]bool{"up": true, "down": true, "start": true, "stop": true, "restart": true, "pull": true, "build": true, "update": true}
		if !allowed[action] {
			failure(c, http.StatusNotFound, 18007, "Unknown Compose action")
			return
		}
		row, err := deps.Compose.Action(c.Request.Context(), c.Param("name"), action)
		if err != nil {
			failure(c, http.StatusConflict, 18008, "Unable to start Compose action")
			return
		}
		recordAudit(c, deps.Audit, "compose."+action, "compose", c.Param("name"), "success")
		c.JSON(http.StatusAccepted, envelope{Code: 0, Message: "success", Data: row})
	})
	v1.GET("/settings", requireAuth(deps.Auth), func(c *gin.Context) {
		values, err := deps.Settings.Get(c.Request.Context())
		if err != nil {
			failure(c, http.StatusInternalServerError, 19001, "Unable to read settings")
			return
		}
		success(c, values)
	})
	v1.PUT("/settings", requireAuth(deps.Auth), func(c *gin.Context) {
		var input map[string]string
		if c.ShouldBindJSON(&input) != nil {
			failure(c, http.StatusBadRequest, 19002, "Invalid settings")
			return
		}
		values, err := deps.Settings.Update(c.Request.Context(), input)
		if err != nil {
			failure(c, http.StatusBadRequest, 19003, err.Error())
			return
		}
		recordAudit(c, deps.Audit, "settings.update", "settings", "DockPort", "success")
		success(c, values)
	})
	v1.GET("/audit-logs", requireAuth(deps.Auth), func(c *gin.Context) {
		rows, err := deps.Audit.List(c.Request.Context(), 100)
		if err != nil {
			failure(c, http.StatusInternalServerError, 17001, "Unable to list audit logs")
			return
		}
		success(c, rows)
	})
	v1.GET("/tasks", requireAuth(deps.Auth), func(c *gin.Context) {
		rows, err := deps.Tasks.List(c.Request.Context())
		if err != nil {
			failure(c, http.StatusInternalServerError, 16001, "Unable to list tasks")
			return
		}
		success(c, rows)
	})
	v1.GET("/tasks/:id/logs", requireAuth(deps.Auth), func(c *gin.Context) {
		rows, err := deps.Tasks.Logs(c.Request.Context(), c.Param("id"))
		if err != nil {
			failure(c, http.StatusInternalServerError, 16002, "Unable to list task logs")
			return
		}
		success(c, rows)
	})
	v1.POST("/tasks/:id/cancel", requireAuth(deps.Auth), func(c *gin.Context) {
		if !deps.Tasks.Cancel(c.Param("id")) {
			failure(c, http.StatusConflict, 16003, "Task is not running")
			return
		}
		success(c, gin.H{"id": c.Param("id")})
	})
	v1.POST("/system/prune", requireAuth(deps.Auth), func(c *gin.Context) {
		var input struct {
			Confirm string `json:"confirm"`
		}
		if c.ShouldBindJSON(&input) != nil || input.Confirm != "PRUNE" {
			failure(c, http.StatusBadRequest, 19501, "Type PRUNE to confirm system cleanup")
			return
		}
		row, err := deps.System.Prune()
		if err != nil {
			failure(c, http.StatusInternalServerError, 19502, "Unable to start system prune")
			return
		}
		recordAudit(c, deps.Audit, "system.prune", "system", "local", "success")
		c.JSON(http.StatusAccepted, envelope{Code: 0, Message: "success", Data: row})
	})
	ws := router.Group("/ws/containers", requireAuth(deps.Auth))
	ws.GET("/:id/logs", func(c *gin.Context) { streamLogs(c, deps.Containers) })
	ws.GET("/:id/stats", func(c *gin.Context) { streamStats(c, deps.Containers) })
	ws.GET("/:id/terminal", func(c *gin.Context) { streamTerminal(c, deps.Containers) })
	taskWS := router.Group("/ws/tasks", requireAuth(deps.Auth))
	taskWS.GET("/:id", func(c *gin.Context) { streamTask(c, deps.Tasks) })
	router.NoRoute(gin.WrapH(webui.Handler()))
	return router
}

func streamTask(c *gin.Context, service *task.Service) {
	connection, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer connection.Close()
	logs, _ := service.Logs(c.Request.Context(), c.Param("id"))
	for _, log := range logs {
		if connection.WriteJSON(task.Event{Type: "log", Message: log.Message, Time: log.CreatedAt}) != nil {
			return
		}
	}
	events, unsubscribe := service.Subscribe(c.Param("id"))
	defer unsubscribe()
	for event := range events {
		connection.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if connection.WriteJSON(event) != nil {
			return
		}
		if event.Status == task.StatusSuccess || event.Status == task.StatusFailed || event.Status == task.StatusCanceled {
			return
		}
	}
}

var upgrader = websocket.Upgrader{ReadBufferSize: 1024, WriteBufferSize: 4096, CheckOrigin: func(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	return origin == "" || strings.Contains(origin, r.Host)
}}

func streamLogs(c *gin.Context, service containerdomain.Service) {
	connection, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	defer connection.Close()
	stream, err := service.Logs(ctx, c.Param("id"), c.Query("since"), c.DefaultQuery("tail", "200"))
	if err != nil {
		_ = connection.WriteJSON(gin.H{"type": "error", "message": "Unable to open container logs"})
		return
	}
	defer stream.Close()
	go watchDisconnect(connection, cancel)
	writer := &websocketTextWriter{connection: connection}
	_, _ = io.Copy(writer, stream)
}

func streamStats(c *gin.Context, service containerdomain.Service) {
	connection, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	defer connection.Close()
	stream, err := service.Stats(ctx, c.Param("id"))
	if err != nil {
		_ = connection.WriteJSON(gin.H{"type": "error", "message": "Unable to open container stats"})
		return
	}
	defer stream.Close()
	go watchDisconnect(connection, cancel)
	decoder := json.NewDecoder(stream)
	for {
		var sample json.RawMessage
		if decoder.Decode(&sample) != nil {
			return
		}
		connection.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if connection.WriteMessage(websocket.TextMessage, sample) != nil {
			return
		}
	}
}

func streamTerminal(c *gin.Context, service containerdomain.Service) {
	connection, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	defer connection.Close()
	session, err := service.Terminal(ctx, c.Param("id"), 120, 36)
	if err != nil {
		_ = connection.WriteJSON(gin.H{"type": "error", "message": "Unable to open container terminal"})
		return
	}
	defer session.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		buffer := make([]byte, 32*1024)
		for {
			count, err := session.Read(buffer)
			if count > 0 {
				connection.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if connection.WriteMessage(websocket.BinaryMessage, buffer[:count]) != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		select {
		case <-done:
			return
		default:
		}
		messageType, value, err := connection.ReadMessage()
		if err != nil {
			return
		}
		if messageType == websocket.BinaryMessage {
			if _, err := session.Write(value); err != nil {
				return
			}
			continue
		}
		var message struct {
			Type    string `json:"type"`
			Data    string `json:"data"`
			Columns uint   `json:"cols"`
			Rows    uint   `json:"rows"`
		}
		if json.Unmarshal(value, &message) != nil {
			continue
		}
		switch message.Type {
		case "input":
			if _, err := session.Write([]byte(message.Data)); err != nil {
				return
			}
		case "resize":
			if message.Columns > 0 && message.Rows > 0 {
				_ = session.Resize(ctx, message.Columns, message.Rows)
			}
		}
	}
}

type websocketTextWriter struct{ connection *websocket.Conn }

func (w *websocketTextWriter) Write(value []byte) (int, error) {
	w.connection.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := w.connection.WriteMessage(websocket.TextMessage, value); err != nil {
		return 0, err
	}
	return len(value), nil
}
func watchDisconnect(connection *websocket.Conn, cancel context.CancelFunc) {
	defer cancel()
	connection.SetReadLimit(1024)
	for {
		if _, _, err := connection.ReadMessage(); err != nil {
			return
		}
	}
}

func recordAudit(c *gin.Context, service *audit.Service, action, resourceType, resourceName, result string) {
	var userID *uint
	if value, exists := c.Get("user"); exists {
		user := value.(auth.User)
		userID = &user.ID
	}
	_ = service.Record(c.Request.Context(), userID, action, resourceType, resourceName, c.ClientIP(), result)
}

func requireAuth(service *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, _ := c.Cookie(sessionCookie)
		user, err := service.Authenticate(c.Request.Context(), token)
		if err != nil {
			failure(c, http.StatusUnauthorized, 11006, "Authentication required")
			c.Abort()
			return
		}
		c.Set("user", user)
		c.Next()
	}
}

func setSessionCookie(c *gin.Context, token string, secure bool) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(sessionCookie, token, 86400, "/", "", secure, true)
}
func clearSessionCookie(c *gin.Context, secure bool) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(sessionCookie, "", -1, "/", "", secure, true)
}

func requestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-ID")
		if id == "" {
			id = time.Now().UTC().Format("20060102150405.000000000")
		}
		c.Header("X-Request-ID", id)
		c.Next()
	}
}

func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "same-origin")
		c.Next()
	}
}
