package api

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/suma/suma/server/internal/auth"
	composeService "github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/docker"
	"github.com/suma/suma/server/internal/image"
	"github.com/suma/suma/server/internal/network"
	"github.com/suma/suma/server/internal/node"
	"github.com/suma/suma/server/internal/system"
	"github.com/suma/suma/server/internal/volume"
)

func registerNodeRoutes(router *gin.Engine, v1 *gin.RouterGroup, deps Dependencies) {
	nodes := v1.Group("/nodes", requireAuth(deps.Auth))
	nodes.GET("", func(c *gin.Context) {
		rows, err := deps.Nodes.List(c.Request.Context())
		if err != nil {
			failure(c, http.StatusInternalServerError, 20001, "Unable to list Docker nodes")
			return
		}
		success(c, rows)
	})
	nodes.POST("", func(c *gin.Context) {
		var input node.Input
		if c.ShouldBindJSON(&input) != nil {
			failure(c, http.StatusBadRequest, 20002, "Invalid Docker node")
			return
		}
		row, err := deps.Nodes.Create(c.Request.Context(), input)
		if err != nil {
			failure(c, http.StatusUnprocessableEntity, 20003, err.Error())
			return
		}
		recordNodeAudit(c, deps, row.ID, row.Name, "node.create", "node", row.Name, "success")
		c.JSON(http.StatusCreated, envelope{Code: 0, Message: "success", Data: row})
	})
	nodes.GET("/:nodeID", func(c *gin.Context) {
		row, err := deps.Nodes.Get(c.Request.Context(), c.Param("nodeID"))
		if err != nil {
			failure(c, http.StatusNotFound, 20004, "Docker node not found")
			return
		}
		success(c, row)
	})
	nodes.PUT("/:nodeID", func(c *gin.Context) {
		var input node.Input
		if c.ShouldBindJSON(&input) != nil {
			failure(c, http.StatusBadRequest, 20002, "Invalid Docker node")
			return
		}
		row, err := deps.Nodes.Update(c.Request.Context(), c.Param("nodeID"), input)
		if err != nil {
			failure(c, http.StatusUnprocessableEntity, 20005, err.Error())
			return
		}
		recordNodeAudit(c, deps, row.ID, row.Name, "node.update", "node", row.Name, "success")
		success(c, row)
	})
	nodes.DELETE("/:nodeID", func(c *gin.Context) {
		row, err := deps.Nodes.Get(c.Request.Context(), c.Param("nodeID"))
		if err != nil {
			failure(c, http.StatusNotFound, 20004, "Docker node not found")
			return
		}
		if err := deps.Nodes.Delete(c.Request.Context(), row.ID); err != nil {
			failure(c, http.StatusConflict, 20006, err.Error())
			return
		}
		recordNodeAudit(c, deps, row.ID, row.Name, "node.delete", "node", row.Name, "success")
		success(c, gin.H{"id": row.ID})
	})
	nodes.POST("/:nodeID/test", func(c *gin.Context) {
		info, err := deps.Nodes.Test(c.Request.Context(), c.Param("nodeID"))
		if err != nil {
			failure(c, http.StatusServiceUnavailable, 20007, err.Error())
			return
		}
		success(c, info)
	})

	tls := v1.Group("/credentials/docker-tls", requireAuth(deps.Auth))
	tls.GET("", func(c *gin.Context) {
		rows, err := deps.Nodes.ListTLSCredentials(c.Request.Context())
		if err != nil {
			failure(c, 500, 20101, "Unable to list Docker TLS credentials")
			return
		}
		success(c, rows)
	})
	tls.POST("", func(c *gin.Context) {
		var input node.TLSCredentialInput
		if c.ShouldBindJSON(&input) != nil {
			failure(c, 400, 20102, "Invalid Docker TLS credential")
			return
		}
		row, err := deps.Nodes.CreateTLSCredential(c.Request.Context(), input)
		if err != nil {
			failure(c, 422, 20103, err.Error())
			return
		}
		recordAudit(c, deps.Audit, "docker_tls.credential.create", "docker_tls_credential", row.Name, "success")
		c.JSON(201, envelope{Code: 0, Message: "success", Data: row})
	})
	tls.PUT("/:id", func(c *gin.Context) {
		id, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil {
			failure(c, 400, 20104, "Invalid credential ID")
			return
		}
		var input node.TLSCredentialInput
		if c.ShouldBindJSON(&input) != nil {
			failure(c, 400, 20102, "Invalid Docker TLS credential")
			return
		}
		row, err := deps.Nodes.UpdateTLSCredential(c.Request.Context(), uint(id), input)
		if err != nil {
			failure(c, 422, 20105, err.Error())
			return
		}
		recordAudit(c, deps.Audit, "docker_tls.credential.update", "docker_tls_credential", row.Name, "success")
		success(c, row)
	})
	tls.DELETE("/:id", func(c *gin.Context) {
		id, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil {
			failure(c, 400, 20104, "Invalid credential ID")
			return
		}
		if err := deps.Nodes.DeleteTLSCredential(c.Request.Context(), uint(id)); err != nil {
			failure(c, 409, 20106, err.Error())
			return
		}
		recordAudit(c, deps.Audit, "docker_tls.credential.remove", "docker_tls_credential", c.Param("id"), "success")
		success(c, gin.H{"id": id})
	})

	resources := nodes.Group("/:nodeID")
	resources.GET("/docker/info", func(c *gin.Context) {
		adapter, view, ok := resolveNode(c, deps)
		if !ok {
			return
		}
		info, err := adapter.Info(c.Request.Context())
		if err != nil {
			failure(c, 503, 20201, "Unable to read Docker information")
			return
		}
		_ = view
		success(c, info)
	})
	resources.GET("/overview", func(c *gin.Context) {
		adapter, view, ok := resolveNode(c, deps)
		if !ok {
			return
		}
		info, err := adapter.Info(c.Request.Context())
		if err != nil {
			failure(c, 503, 20202, "Unable to read node overview")
			return
		}
		metrics, _ := adapter.Metrics(c.Request.Context())
		diskUsage, _ := adapter.DiskUsage(c.Request.Context())
		var cpu float64
		var memory uint64
		for _, value := range metrics {
			cpu += value.CPUPercent
			memory += value.MemoryBytes
		}
		containersAggregate := gin.H{"cpu_percent": cpu, "memory_bytes": memory}
		host := gin.H{"hostname": info.Name, "os": info.OperatingSystem, "kernel": info.KernelVersion, "architecture": info.Architecture, "cpus": info.CPUs, "memory_total": info.MemoryBytes}
		// Host-wide metrics come from this process's own OS. Unix sockets are
		// filesystem-local and loopback TCP implies the same machine; remote TCP
		// nodes stay with engine-provided totals only.
		if coLocatedNode(view) {
			if snapshot, err := deps.Monitor.Snapshot(); err == nil {
				if snapshot.CPUPercent != nil {
					host["cpu_percent"] = *snapshot.CPUPercent
				}
				if snapshot.UptimeSeconds != nil {
					host["uptime_seconds"] = *snapshot.UptimeSeconds
				}
				if snapshot.MemoryTotal != nil {
					host["memory_total"] = *snapshot.MemoryTotal
				}
				if snapshot.MemoryUsed != nil {
					host["memory_used"] = *snapshot.MemoryUsed
				}
				if snapshot.DiskTotal != nil {
					host["disk_total"] = *snapshot.DiskTotal
				}
				if snapshot.DiskUsed != nil {
					host["disk_used"] = *snapshot.DiskUsed
				}
			}
		}
		success(c, gin.H{"host": host, "containers": containersAggregate, "docker": info, "docker_disk_usage_bytes": diskUsage})
	})
	registerNodeContainerRoutes(resources, router, deps)
	registerNodeImageRoutes(resources, deps)
	registerNodeNetworkRoutes(resources, deps)
	registerNodeVolumeRoutes(resources, deps)
	registerNodeComposeRoutes(resources, deps)
	registerNodeProjectRoutes(resources, deps)
	resources.POST("/system/prune", func(c *gin.Context) {
		adapter, view, ok := resolveNode(c, deps)
		if !ok {
			return
		}
		var input struct {
			Confirm string `json:"confirm"`
		}
		if c.ShouldBindJSON(&input) != nil || input.Confirm != "PRUNE" {
			failure(c, 400, 20250, "Type PRUNE to confirm system cleanup")
			return
		}
		row, err := system.NewService(adapter, deps.Tasks).PruneForNode(view.ID, view.Name)
		if err != nil {
			failure(c, 500, 20251, "Unable to start system prune")
			return
		}
		recordNodeAudit(c, deps, view.ID, view.Name, "system.prune", "system", view.Name, "success")
		c.JSON(202, envelope{Code: 0, Message: "success", Data: row})
	})

	ws := router.Group("/ws/nodes/:nodeID/containers", requireAuth(deps.Auth))
	ws.GET("/:id/logs", func(c *gin.Context) {
		adapter, _, ok := resolveNode(c, deps)
		if ok {
			streamLogs(c, adapter)
		}
	})
	ws.GET("/:id/stats", func(c *gin.Context) {
		adapter, _, ok := resolveNode(c, deps)
		if ok {
			streamStats(c, adapter)
		}
	})
	ws.GET("/:id/terminal", func(c *gin.Context) {
		adapter, _, ok := resolveNode(c, deps)
		if ok {
			streamTerminal(c, adapter)
		}
	})
}

func registerNodeComposeRoutes(group *gin.RouterGroup, deps Dependencies) {
	if deps.Compose == nil || deps.ComposeRunner == nil {
		return
	}
	routes := group.Group("/compose")
	service := func(c *gin.Context) (*composeService.Service, node.View, bool) {
		adapter, view, ok := resolveNode(c, deps)
		if !ok {
			return nil, view, false
		}
		target, _, err := deps.Nodes.ComposeTarget(c.Request.Context(), view.ID)
		if err != nil {
			failure(c, 503, 20301, err.Error())
			return nil, view, false
		}
		return deps.Compose.ForNode(view.ID, view.Name, deps.ComposeRunner.ForTarget(target), adapter, view.ConnectionType == node.ConnectionUnix), view, true
	}
	validatePolicy := func(view node.View, content string) error {
		if view.ConnectionType == node.ConnectionTCP {
			return composeService.ValidateRemoteBindMounts(content, view.AllowedBindRoots)
		}
		return nil
	}
	routes.GET("", func(c *gin.Context) {
		current, _, ok := service(c)
		if !ok {
			return
		}
		rows, err := current.List(c.Request.Context())
		if err != nil {
			failure(c, 500, 20302, "Unable to list Compose projects")
			return
		}
		success(c, rows)
	})
	routes.GET("/:name", func(c *gin.Context) {
		current, _, ok := service(c)
		if !ok {
			return
		}
		row, err := current.Get(c.Request.Context(), c.Param("name"))
		if err != nil {
			failure(c, 404, 20303, "Compose project not found")
			return
		}
		success(c, row)
	})
	routes.GET("/:name/services", func(c *gin.Context) {
		current, _, ok := service(c)
		if !ok {
			return
		}
		rows, err := current.Services(c.Request.Context(), c.Param("name"))
		if err != nil {
			failure(c, 404, 20304, "Unable to list Compose services")
			return
		}
		success(c, rows)
	})
	routes.GET("/:name/logs", func(c *gin.Context) {
		current, _, ok := service(c)
		if !ok {
			return
		}
		logs, err := current.Logs(c.Request.Context(), c.Param("name"))
		if err != nil {
			failure(c, 409, 20305, logs)
			return
		}
		success(c, gin.H{"logs": logs})
	})
	routes.POST("", func(c *gin.Context) {
		current, view, ok := service(c)
		if !ok {
			return
		}
		var input struct {
			Name        string `json:"name"`
			Compose     string `json:"compose"`
			Environment string `json:"environment"`
		}
		if c.ShouldBindJSON(&input) != nil || input.Name == "" || input.Compose == "" {
			failure(c, 400, 20306, "Project name and Compose YAML are required")
			return
		}
		if err := validatePolicy(view, input.Compose); err != nil {
			failure(c, 422, 20307, err.Error())
			return
		}
		row, err := current.Create(c.Request.Context(), input.Name, input.Compose, input.Environment)
		if err != nil {
			failure(c, 409, 20308, err.Error())
			return
		}
		recordNodeAudit(c, deps, view.ID, view.Name, "compose.create", "compose", input.Name, "success")
		c.JSON(201, envelope{Code: 0, Message: "success", Data: row})
	})
	routes.POST("/:name/takeover/preview", func(c *gin.Context) {
		current, view, ok := service(c)
		if !ok {
			return
		}
		draft, err := current.BuildTakeoverDraft(c.Request.Context(), c.Param("name"))
		if err != nil {
			failure(c, 409, 20317, err.Error())
			return
		}
		_ = view
		success(c, draft)
	})
	routes.POST("/:name/takeover/render", func(c *gin.Context) {
		current, _, ok := service(c)
		if !ok {
			return
		}
		var input struct {
			Fingerprint string                             `json:"fingerprint"`
			Choices     []composeService.EnvironmentChoice `json:"choices"`
		}
		if c.ShouldBindJSON(&input) != nil || input.Fingerprint == "" {
			failure(c, 400, 20318, "Takeover fingerprint is required")
			return
		}
		draft, err := current.RenderTakeoverDraft(c.Request.Context(), c.Param("name"), input.Fingerprint, input.Choices)
		if err != nil {
			failure(c, 409, 20319, err.Error())
			return
		}
		success(c, draft)
	})
	routes.POST("/:name/takeover/validate", func(c *gin.Context) {
		current, view, ok := service(c)
		if !ok {
			return
		}
		var input struct {
			Compose     string `json:"compose"`
			Environment string `json:"environment"`
		}
		if c.ShouldBindJSON(&input) != nil || input.Compose == "" {
			failure(c, 400, 20322, "Compose YAML is required")
			return
		}
		if err := validatePolicy(view, input.Compose); err != nil {
			failure(c, 422, 20323, err.Error())
			return
		}
		if err := current.ValidateDraft(c.Request.Context(), input.Compose, input.Environment); err != nil {
			failure(c, 422, 20324, err.Error())
			return
		}
		success(c, gin.H{"valid": true})
	})
	routes.POST("/:name/takeover", func(c *gin.Context) {
		current, view, ok := service(c)
		if !ok {
			return
		}
		var input composeService.TakeoverInput
		if c.ShouldBindJSON(&input) != nil {
			failure(c, 400, 20320, "Invalid Project takeover request")
			return
		}
		if err := validatePolicy(view, input.Compose); err != nil {
			failure(c, 422, 20307, err.Error())
			return
		}
		row, err := current.Takeover(c.Request.Context(), c.Param("name"), input)
		if err != nil {
			failure(c, 409, 20321, err.Error())
			return
		}
		recordNodeAudit(c, deps, view.ID, view.Name, "project.takeover", "project", c.Param("name"), "success")
		c.JSON(201, envelope{Code: 0, Message: "success", Data: row})
	})
	routes.POST("/batch", func(c *gin.Context) {
		current, view, ok := service(c)
		if !ok {
			return
		}
		var input struct {
			Names  []string `json:"names"`
			Action string   `json:"action"`
		}
		if c.ShouldBindJSON(&input) != nil || len(input.Names) == 0 || len(input.Names) > 100 {
			failure(c, 400, 20315, "Between 1 and 100 Compose project names are required")
			return
		}
		allowed := map[string]bool{"start": true, "stop": true, "restart": true, "update": true, "down": true}
		if !allowed[input.Action] {
			failure(c, 400, 20316, "Unsupported Compose batch action")
			return
		}
		results := current.BatchAction(c.Request.Context(), input.Names, input.Action)
		for _, result := range results {
			outcome := "failed"
			if result.Success {
				outcome = "success"
			}
			recordNodeAudit(c, deps, view.ID, view.Name, "compose."+input.Action, "compose", result.Name, outcome)
		}
		success(c, gin.H{"action": input.Action, "results": results})
	})
	routes.PUT("/:name", func(c *gin.Context) {
		current, view, ok := service(c)
		if !ok {
			return
		}
		var input struct {
			Compose     string `json:"compose"`
			Environment string `json:"environment"`
		}
		if c.ShouldBindJSON(&input) != nil || input.Compose == "" {
			failure(c, 400, 20306, "Compose YAML is required")
			return
		}
		if err := validatePolicy(view, input.Compose); err != nil {
			failure(c, 422, 20307, err.Error())
			return
		}
		row, err := current.Save(c.Request.Context(), c.Param("name"), input.Compose, input.Environment)
		if err != nil {
			failure(c, 409, 20309, "Unable to save Compose project")
			return
		}
		recordNodeAudit(c, deps, view.ID, view.Name, "compose.save", "compose", c.Param("name"), "success")
		success(c, row)
	})
	routes.POST("/:name/validate", func(c *gin.Context) {
		current, view, ok := service(c)
		if !ok {
			return
		}
		var input struct {
			Compose     string `json:"compose"`
			Environment string `json:"environment"`
		}
		if c.ShouldBindJSON(&input) != nil || input.Compose == "" {
			failure(c, 400, 20306, "Compose YAML is required")
			return
		}
		if err := validatePolicy(view, input.Compose); err != nil {
			failure(c, 422, 20307, err.Error())
			return
		}
		if err := current.Validate(c.Request.Context(), c.Param("name"), input.Compose, input.Environment); err != nil {
			failure(c, 422, 20310, err.Error())
			return
		}
		success(c, gin.H{"valid": true})
	})
	routes.DELETE("/:name", func(c *gin.Context) {
		current, view, ok := service(c)
		if !ok {
			return
		}
		if c.Query("confirm") != c.Param("name") {
			failure(c, 400, 20311, "Type the project name to confirm removal")
			return
		}
		force, preserve := c.Query("force") == "true", c.Query("preserve_volumes") == "true"
		var err error
		if force {
			err = current.ForceRemove(c.Request.Context(), c.Param("name"), preserve)
		} else {
			err = current.Remove(c.Request.Context(), c.Param("name"))
		}
		if err != nil {
			failure(c, 409, 20312, "Unable to remove Compose project")
			return
		}
		action := "compose.remove"
		if force {
			action = "compose.force_remove"
		}
		recordNodeAudit(c, deps, view.ID, view.Name, action, "compose", c.Param("name"), "success")
		success(c, gin.H{"name": c.Param("name")})
	})
	routes.POST("/:name/cleanup", func(c *gin.Context) {
		current, view, ok := service(c)
		if !ok {
			return
		}
		var input struct {
			ConfirmationName string `json:"confirmation_name"`
			RemoveVolumes    bool   `json:"remove_volumes"`
		}
		if c.ShouldBindJSON(&input) != nil {
			failure(c, 400, 20317, "A cleanup confirmation is required")
			return
		}
		row, err := current.CleanupExternalProject(c.Request.Context(), c.Param("name"), input.ConfirmationName, input.RemoveVolumes)
		if err != nil {
			failure(c, 409, 20318, err.Error())
			return
		}
		action := "project.cleanup"
		if input.RemoveVolumes {
			action = "project.cleanup_with_volumes"
		}
		recordNodeAudit(c, deps, view.ID, view.Name, action, "project", c.Param("name"), "success")
		c.JSON(202, envelope{Code: 0, Message: "success", Data: row})
	})
	routes.POST("/:name/:action", func(c *gin.Context) {
		current, view, ok := service(c)
		if !ok {
			return
		}
		action := c.Param("action")
		allowed := map[string]bool{"up": true, "down": true, "start": true, "stop": true, "restart": true, "pull": true, "build": true, "update": true}
		if !allowed[action] {
			failure(c, 404, 20313, "Unknown Compose action")
			return
		}
		row, err := current.Action(c.Request.Context(), c.Param("name"), action)
		if err != nil {
			failure(c, 409, 20314, "Unable to start Compose action")
			return
		}
		recordNodeAudit(c, deps, view.ID, view.Name, "compose."+action, "compose", c.Param("name"), "success")
		c.JSON(202, envelope{Code: 0, Message: "success", Data: row})
	})
}

func registerNodeProjectRoutes(group *gin.RouterGroup, deps Dependencies) {
	if deps.Compose == nil || deps.ComposeRunner == nil {
		return
	}
	projects := group.Group("/projects")
	service := func(c *gin.Context) (*composeService.Service, node.View, bool) {
		adapter, view, ok := resolveNode(c, deps)
		if !ok {
			return nil, view, false
		}
		target, _, err := deps.Nodes.ComposeTarget(c.Request.Context(), view.ID)
		if err != nil {
			failure(c, 503, 20401, err.Error())
			return nil, view, false
		}
		return deps.Compose.ForNode(view.ID, view.Name, deps.ComposeRunner.ForTarget(target), adapter, view.ConnectionType == node.ConnectionUnix), view, true
	}
	validatePolicy := func(view node.View, content string) error {
		if view.ConnectionType == node.ConnectionTCP {
			return composeService.ValidateRemoteBindMounts(content, view.AllowedBindRoots)
		}
		return nil
	}
	projects.GET("", func(c *gin.Context) {
		current, _, ok := service(c)
		if !ok {
			return
		}
		rows, err := current.ListSummaries(c.Request.Context())
		if err != nil {
			failure(c, 500, 20402, "Unable to list Projects")
			return
		}
		success(c, rows)
	})
	projects.POST("", func(c *gin.Context) {
		current, view, ok := service(c)
		if !ok {
			return
		}
		var input struct {
			Backend     string `json:"backend"`
			Name        string `json:"name"`
			Compose     string `json:"compose"`
			Environment string `json:"environment"`
		}
		if c.ShouldBindJSON(&input) != nil || input.Name == "" || input.Compose == "" || (input.Backend != "" && input.Backend != "compose") {
			failure(c, 400, 20403, "A Compose Project name and Compose YAML are required")
			return
		}
		if err := validatePolicy(view, input.Compose); err != nil {
			failure(c, 422, 20404, err.Error())
			return
		}
		row, err := current.Create(c.Request.Context(), input.Name, input.Compose, input.Environment)
		if err != nil {
			failure(c, 409, 20405, err.Error())
			return
		}
		recordNodeAudit(c, deps, view.ID, view.Name, "project.create", "project", input.Name, "success")
		c.JSON(201, envelope{Code: 0, Message: "success", Data: row})
	})
	projects.POST("/batch", func(c *gin.Context) {
		current, view, ok := service(c)
		if !ok {
			return
		}
		var input struct {
			Backend string   `json:"backend"`
			Names   []string `json:"names"`
			Action  string   `json:"action"`
		}
		if c.ShouldBindJSON(&input) != nil || (input.Backend != "" && input.Backend != "compose") || len(input.Names) == 0 || len(input.Names) > 100 {
			failure(c, 400, 20406, "Between 1 and 100 Compose Project names are required")
			return
		}
		allowed := map[string]bool{"start": true, "stop": true, "restart": true, "update": true, "down": true}
		if !allowed[input.Action] {
			failure(c, 400, 20407, "Unsupported Project batch action")
			return
		}
		results := current.BatchAction(c.Request.Context(), input.Names, input.Action)
		for _, result := range results {
			outcome := "failed"
			if result.Success {
				outcome = "success"
			}
			recordNodeAudit(c, deps, view.ID, view.Name, "project."+input.Action, "project", result.Name, outcome)
		}
		success(c, gin.H{"backend": "compose", "action": input.Action, "results": results})
	})
	compose := projects.Group("/compose")
	compose.GET("/:name", func(c *gin.Context) {
		current, _, ok := service(c)
		if !ok {
			return
		}
		row, err := current.Get(c.Request.Context(), c.Param("name"))
		if err != nil {
			failure(c, 404, 20408, "Project not found")
			return
		}
		success(c, row)
	})
	compose.GET("/:name/services", func(c *gin.Context) {
		current, _, ok := service(c)
		if !ok {
			return
		}
		rows, err := current.Services(c.Request.Context(), c.Param("name"))
		if err != nil {
			failure(c, 404, 20409, "Unable to list Project services")
			return
		}
		success(c, rows)
	})
	compose.GET("/:name/logs", func(c *gin.Context) {
		current, _, ok := service(c)
		if !ok {
			return
		}
		value, err := current.Logs(c.Request.Context(), c.Param("name"))
		if err != nil {
			failure(c, 409, 20410, value)
			return
		}
		success(c, gin.H{"logs": value})
	})
	compose.PUT("/:name", func(c *gin.Context) {
		current, view, ok := service(c)
		if !ok {
			return
		}
		var input struct {
			Compose     string `json:"compose"`
			Environment string `json:"environment"`
		}
		if c.ShouldBindJSON(&input) != nil || input.Compose == "" {
			failure(c, 400, 20403, "Compose YAML is required")
			return
		}
		if err := validatePolicy(view, input.Compose); err != nil {
			failure(c, 422, 20404, err.Error())
			return
		}
		row, err := current.Save(c.Request.Context(), c.Param("name"), input.Compose, input.Environment)
		if err != nil {
			failure(c, 409, 20411, "Unable to save Project")
			return
		}
		recordNodeAudit(c, deps, view.ID, view.Name, "project.save", "project", c.Param("name"), "success")
		success(c, row)
	})
	compose.POST("/:name/validate", func(c *gin.Context) {
		current, view, ok := service(c)
		if !ok {
			return
		}
		var input struct {
			Compose     string `json:"compose"`
			Environment string `json:"environment"`
		}
		if c.ShouldBindJSON(&input) != nil || input.Compose == "" {
			failure(c, 400, 20403, "Compose YAML is required")
			return
		}
		if err := validatePolicy(view, input.Compose); err != nil {
			failure(c, 422, 20404, err.Error())
			return
		}
		if err := current.Validate(c.Request.Context(), c.Param("name"), input.Compose, input.Environment); err != nil {
			failure(c, 422, 20412, err.Error())
			return
		}
		success(c, gin.H{"valid": true})
	})
	compose.DELETE("/:name", func(c *gin.Context) {
		current, view, ok := service(c)
		if !ok {
			return
		}
		if c.Query("confirm") != c.Param("name") {
			failure(c, 400, 20413, "Type the Project name to confirm removal")
			return
		}
		force, preserve := c.Query("force") == "true", c.Query("preserve_volumes") == "true"
		var err error
		if force {
			err = current.ForceRemove(c.Request.Context(), c.Param("name"), preserve)
		} else {
			err = current.Remove(c.Request.Context(), c.Param("name"))
		}
		if err != nil {
			failure(c, 409, 20414, "Unable to remove Project")
			return
		}
		recordNodeAudit(c, deps, view.ID, view.Name, "project.delete", "project", c.Param("name"), "success")
		success(c, gin.H{"name": c.Param("name")})
	})
	compose.POST("/:name/actions/:action", func(c *gin.Context) {
		current, view, ok := service(c)
		if !ok {
			return
		}
		action := c.Param("action")
		allowed := map[string]bool{"up": true, "down": true, "start": true, "stop": true, "restart": true, "pull": true, "build": true, "update": true}
		if !allowed[action] {
			failure(c, 404, 20415, "Unknown Project action")
			return
		}
		row, err := current.Action(c.Request.Context(), c.Param("name"), action)
		if err != nil {
			failure(c, 409, 20416, "Unable to start Project action")
			return
		}
		recordNodeAudit(c, deps, view.ID, view.Name, "project."+action, "project", c.Param("name"), "success")
		c.JSON(202, envelope{Code: 0, Message: "success", Data: row})
	})
	compose.POST("/:name/cleanup", func(c *gin.Context) {
		current, view, ok := service(c)
		if !ok {
			return
		}
		var input struct {
			ConfirmationName string `json:"confirmation_name"`
			RemoveVolumes    bool   `json:"remove_volumes"`
		}
		if c.ShouldBindJSON(&input) != nil {
			failure(c, 400, 20433, "A cleanup confirmation is required")
			return
		}
		row, err := current.CleanupExternalProject(c.Request.Context(), c.Param("name"), input.ConfirmationName, input.RemoveVolumes)
		if err != nil {
			failure(c, 409, 20434, err.Error())
			return
		}
		action := "project.cleanup"
		if input.RemoveVolumes {
			action = "project.cleanup_with_volumes"
		}
		recordNodeAudit(c, deps, view.ID, view.Name, action, "project", c.Param("name"), "success")
		c.JSON(202, envelope{Code: 0, Message: "success", Data: row})
	})
	compose.POST("/:name/takeover/preview", func(c *gin.Context) {
		current, _, ok := service(c)
		if !ok {
			return
		}
		draft, err := current.BuildTakeoverDraft(c.Request.Context(), c.Param("name"))
		if err != nil {
			failure(c, 409, 20417, err.Error())
			return
		}
		success(c, draft)
	})
	compose.POST("/:name/takeover/render", func(c *gin.Context) {
		current, _, ok := service(c)
		if !ok {
			return
		}
		var input struct {
			Fingerprint string                             `json:"fingerprint"`
			Choices     []composeService.EnvironmentChoice `json:"choices"`
		}
		if c.ShouldBindJSON(&input) != nil || input.Fingerprint == "" {
			failure(c, 400, 20418, "Takeover fingerprint is required")
			return
		}
		draft, err := current.RenderTakeoverDraft(c.Request.Context(), c.Param("name"), input.Fingerprint, input.Choices)
		if err != nil {
			failure(c, 409, 20419, err.Error())
			return
		}
		success(c, draft)
	})
	compose.POST("/:name/takeover/validate", func(c *gin.Context) {
		current, view, ok := service(c)
		if !ok {
			return
		}
		var input struct {
			Compose     string `json:"compose"`
			Environment string `json:"environment"`
		}
		if c.ShouldBindJSON(&input) != nil || input.Compose == "" {
			failure(c, 400, 20422, "Compose YAML is required")
			return
		}
		if err := validatePolicy(view, input.Compose); err != nil {
			failure(c, 422, 20423, err.Error())
			return
		}
		if err := current.ValidateDraft(c.Request.Context(), input.Compose, input.Environment); err != nil {
			failure(c, 422, 20424, err.Error())
			return
		}
		success(c, gin.H{"valid": true})
	})
	compose.POST("/:name/takeover/shadow/assess", func(c *gin.Context) {
		_, view, ok := service(c)
		if !ok {
			return
		}
		var input struct {
			Compose string `json:"compose"`
		}
		if c.ShouldBindJSON(&input) != nil || input.Compose == "" {
			failure(c, 400, 20425, "Compose YAML is required")
			return
		}
		if err := validatePolicy(view, input.Compose); err != nil {
			failure(c, 422, 20426, err.Error())
			return
		}
		assessment, err := composeService.AssessShadowPreview(input.Compose)
		if err != nil {
			failure(c, 422, 20427, err.Error())
			return
		}
		success(c, assessment)
	})
	compose.POST("/:name/takeover/shadow", func(c *gin.Context) {
		current, view, ok := service(c)
		if !ok {
			return
		}
		var input struct {
			Fingerprint string `json:"fingerprint"`
			Compose     string `json:"compose"`
			Environment string `json:"environment"`
		}
		if c.ShouldBindJSON(&input) != nil || input.Fingerprint == "" || input.Compose == "" {
			failure(c, 400, 20428, "Fingerprint and Compose YAML are required")
			return
		}
		if err := validatePolicy(view, input.Compose); err != nil {
			failure(c, 422, 20429, err.Error())
			return
		}
		session, err := current.StartShadowPreview(c.Request.Context(), c.Param("name"), input.Fingerprint, input.Compose, input.Environment)
		if err != nil {
			failure(c, 409, 20430, err.Error())
			return
		}
		recordNodeAudit(c, deps, view.ID, view.Name, "project.shadow_preview", "project", c.Param("name"), "success")
		c.JSON(202, envelope{Code: 0, Message: "success", Data: session})
	})
	compose.GET("/:name/takeover/shadow/:session", func(c *gin.Context) {
		current, _, ok := service(c)
		if !ok {
			return
		}
		status, err := current.ShadowPreviewStatus(c.Request.Context(), c.Param("session"))
		if err != nil {
			failure(c, 409, 20431, err.Error())
			return
		}
		success(c, status)
	})
	compose.DELETE("/:name/takeover/shadow/:session", func(c *gin.Context) {
		current, view, ok := service(c)
		if !ok {
			return
		}
		row, err := current.StopShadowPreview(c.Param("session"))
		if err != nil {
			failure(c, 409, 20432, err.Error())
			return
		}
		recordNodeAudit(c, deps, view.ID, view.Name, "project.shadow_cleanup", "project", c.Param("name"), "success")
		c.JSON(202, envelope{Code: 0, Message: "success", Data: row})
	})
	compose.POST("/:name/takeover", func(c *gin.Context) {
		current, view, ok := service(c)
		if !ok {
			return
		}
		var input composeService.TakeoverInput
		if c.ShouldBindJSON(&input) != nil {
			failure(c, 400, 20420, "Invalid Project takeover request")
			return
		}
		if err := validatePolicy(view, input.Compose); err != nil {
			failure(c, 422, 20404, err.Error())
			return
		}
		row, err := current.Takeover(c.Request.Context(), c.Param("name"), input)
		if err != nil {
			failure(c, 409, 20421, err.Error())
			return
		}
		recordNodeAudit(c, deps, view.ID, view.Name, "project.takeover", "project", c.Param("name"), "success")
		c.JSON(201, envelope{Code: 0, Message: "success", Data: row})
	})
}

func registerNodeContainerRoutes(group *gin.RouterGroup, _ *gin.Engine, deps Dependencies) {
	containers := group.Group("/containers")
	containers.GET("", func(c *gin.Context) {
		adapter, _, ok := resolveNode(c, deps)
		if !ok {
			return
		}
		rows, err := adapter.List(c.Request.Context())
		if err != nil {
			failure(c, 503, 20210, "Unable to list containers")
			return
		}
		success(c, rows)
	})
	containers.GET("/metrics", func(c *gin.Context) {
		adapter, _, ok := resolveNode(c, deps)
		if !ok {
			return
		}
		rows, err := adapter.Metrics(c.Request.Context())
		if err != nil {
			failure(c, 503, 20211, "Unable to read container metrics")
			return
		}
		success(c, rows)
	})
	containers.POST("/batch", func(c *gin.Context) {
		adapter, view, ok := resolveNode(c, deps)
		if !ok {
			return
		}
		var input struct {
			IDs           []string `json:"ids"`
			Action        string   `json:"action"`
			RemoveVolumes bool     `json:"remove_volumes"`
		}
		if c.ShouldBindJSON(&input) != nil || len(input.IDs) == 0 || len(input.IDs) > 100 {
			failure(c, 400, 20218, "Between 1 and 100 container IDs are required")
			return
		}
		allowed := map[string]bool{"start": true, "stop": true, "restart": true, "pause": true, "unpause": true, "kill": true, "remove": true}
		if !allowed[input.Action] {
			failure(c, 400, 20219, "Unsupported batch action")
			return
		}
		type result struct {
			ID      string `json:"id"`
			Success bool   `json:"success"`
		}
		rows := make([]result, 0, len(input.IDs))
		for _, id := range input.IDs {
			var err error
			switch input.Action {
			case "start":
				err = adapter.Start(c.Request.Context(), id)
			case "stop":
				err = adapter.Stop(c.Request.Context(), id)
			case "restart":
				err = adapter.Restart(c.Request.Context(), id)
			case "pause":
				err = adapter.Pause(c.Request.Context(), id)
			case "unpause":
				err = adapter.Unpause(c.Request.Context(), id)
			case "kill":
				err = adapter.Kill(c.Request.Context(), id)
			case "remove":
				err = adapter.Remove(c.Request.Context(), id, input.RemoveVolumes)
			}
			outcome := "success"
			if err != nil {
				outcome = "failed"
			}
			recordNodeAudit(c, deps, view.ID, view.Name, "container."+input.Action, "container", id, outcome)
			rows = append(rows, result{ID: id, Success: err == nil})
		}
		success(c, gin.H{"action": input.Action, "results": rows})
	})
	containers.GET("/:id", func(c *gin.Context) {
		adapter, _, ok := resolveNode(c, deps)
		if !ok {
			return
		}
		row, err := adapter.Get(c.Request.Context(), c.Param("id"))
		if err != nil {
			failure(c, 404, 20212, "Container not found")
			return
		}
		success(c, row)
	})
	containers.POST("/:id/:action", func(c *gin.Context) {
		adapter, view, ok := resolveNode(c, deps)
		if !ok {
			return
		}
		id, action := c.Param("id"), c.Param("action")
		var err error
		switch action {
		case "start":
			err = adapter.Start(c.Request.Context(), id)
		case "stop":
			err = adapter.Stop(c.Request.Context(), id)
		case "restart":
			err = adapter.Restart(c.Request.Context(), id)
		case "pause":
			err = adapter.Pause(c.Request.Context(), id)
		case "unpause":
			err = adapter.Unpause(c.Request.Context(), id)
		case "kill":
			err = adapter.Kill(c.Request.Context(), id)
		default:
			failure(c, 404, 20213, "Unknown container action")
			return
		}
		result := "success"
		if err != nil {
			result = "failed"
		}
		recordNodeAudit(c, deps, view.ID, view.Name, "container."+action, "container", id, result)
		if err != nil {
			failure(c, 409, 20214, "Container action failed")
			return
		}
		success(c, gin.H{"id": id, "action": action})
	})
	containers.PATCH("/:id", func(c *gin.Context) {
		adapter, view, ok := resolveNode(c, deps)
		if !ok {
			return
		}
		var input struct {
			Name string `json:"name"`
		}
		if c.ShouldBindJSON(&input) != nil || input.Name == "" {
			failure(c, 400, 20215, "Container name is required")
			return
		}
		err := adapter.Rename(c.Request.Context(), c.Param("id"), input.Name)
		result := "success"
		if err != nil {
			result = "failed"
		}
		recordNodeAudit(c, deps, view.ID, view.Name, "container.rename", "container", c.Param("id"), result)
		if err != nil {
			failure(c, 409, 20216, "Unable to rename container")
			return
		}
		success(c, gin.H{"name": input.Name})
	})
	containers.DELETE("/:id", func(c *gin.Context) {
		adapter, view, ok := resolveNode(c, deps)
		if !ok {
			return
		}
		err := adapter.Remove(c.Request.Context(), c.Param("id"), c.Query("volumes") == "true")
		result := "success"
		if err != nil {
			result = "failed"
		}
		recordNodeAudit(c, deps, view.ID, view.Name, "container.remove", "container", c.Param("id"), result)
		if err != nil {
			failure(c, 409, 20217, "Unable to remove container")
			return
		}
		success(c, gin.H{"id": c.Param("id")})
	})
}

func registerNodeImageRoutes(group *gin.RouterGroup, deps Dependencies) {
	images := group.Group("/images")
	images.GET("", func(c *gin.Context) {
		adapter, _, ok := resolveNode(c, deps)
		if !ok {
			return
		}
		rows, err := adapter.ListImages(c.Request.Context())
		if err != nil {
			failure(c, 503, 20220, "Unable to list images")
			return
		}
		success(c, rows)
	})
	images.GET("/:id", func(c *gin.Context) {
		adapter, _, ok := resolveNode(c, deps)
		if !ok {
			return
		}
		row, err := adapter.InspectImage(c.Request.Context(), c.Param("id"))
		if err != nil {
			failure(c, 404, 20221, "Image not found")
			return
		}
		success(c, row)
	})
	images.POST("/pull", func(c *gin.Context) {
		adapter, view, ok := resolveNode(c, deps)
		if !ok {
			return
		}
		var input struct {
			Reference    string `json:"reference"`
			CredentialID *uint  `json:"credential_id"`
		}
		if c.ShouldBindJSON(&input) != nil || input.Reference == "" {
			failure(c, 400, 20222, "Image reference is required")
			return
		}
		service := image.NewService(adapter, deps.Tasks)
		var row database.Task
		var err error
		if input.CredentialID != nil {
			if deps.RegistryCredentials == nil {
				failure(c, 422, 20223, "Registry credentials are unavailable")
				return
			}
			if err := deps.RegistryCredentials.AuthorizedForNode(c.Request.Context(), *input.CredentialID, view.ID); err != nil {
				failure(c, 403, 20223, err.Error())
				return
			}
			material, materialErr := deps.RegistryCredentials.Material(c.Request.Context(), *input.CredentialID)
			if materialErr != nil {
				failure(c, 422, 20223, "Registry credential not found")
				return
			}
			row, err = service.PullForNodeWithRegistry(view.ID, view.Name, input.Reference, material.ServerAddress, material.Username, material.Secret, material.AuthType == "token")
		} else {
			row, err = service.PullForNode(view.ID, view.Name, input.Reference)
		}
		if err != nil {
			failure(c, 500, 20223, "Unable to start image pull")
			return
		}
		recordNodeAudit(c, deps, view.ID, view.Name, "image.pull", "image", input.Reference, "success")
		c.JSON(202, envelope{Code: 0, Message: "success", Data: row})
	})
	images.POST("/:id/tag", func(c *gin.Context) {
		adapter, view, ok := resolveNode(c, deps)
		if !ok {
			return
		}
		var input struct {
			Reference string `json:"reference"`
		}
		if c.ShouldBindJSON(&input) != nil || input.Reference == "" {
			failure(c, 400, 20222, "Image reference is required")
			return
		}
		err := adapter.TagImage(c.Request.Context(), c.Param("id"), input.Reference)
		result := "success"
		if err != nil {
			result = "failed"
		}
		recordNodeAudit(c, deps, view.ID, view.Name, "image.tag", "image", input.Reference, result)
		if err != nil {
			failure(c, 409, 20224, "Unable to tag image")
			return
		}
		success(c, gin.H{"reference": input.Reference})
	})
	images.DELETE("/:id", func(c *gin.Context) {
		adapter, view, ok := resolveNode(c, deps)
		if !ok {
			return
		}
		err := adapter.RemoveImage(c.Request.Context(), c.Param("id"), c.Query("force") == "true")
		result := "success"
		if err != nil {
			result = "failed"
		}
		recordNodeAudit(c, deps, view.ID, view.Name, "image.remove", "image", c.Param("id"), result)
		if err != nil {
			failure(c, 409, 20225, "Unable to remove image")
			return
		}
		success(c, gin.H{"id": c.Param("id")})
	})
}

func registerNodeNetworkRoutes(group *gin.RouterGroup, deps Dependencies) {
	routes := group.Group("/networks")
	routes.GET("", func(c *gin.Context) {
		adapter, _, ok := resolveNode(c, deps)
		if !ok {
			return
		}
		rows, err := adapter.ListNetworks(c.Request.Context())
		if err != nil {
			failure(c, 503, 20230, "Unable to list networks")
			return
		}
		success(c, rows)
	})
	routes.GET("/:id", func(c *gin.Context) {
		adapter, _, ok := resolveNode(c, deps)
		if !ok {
			return
		}
		row, err := adapter.InspectNetwork(c.Request.Context(), c.Param("id"))
		if err != nil {
			failure(c, 404, 20231, "Network not found")
			return
		}
		success(c, row)
	})
	routes.POST("", func(c *gin.Context) {
		adapter, view, ok := resolveNode(c, deps)
		if !ok {
			return
		}
		var input network.CreateRequest
		if c.ShouldBindJSON(&input) != nil {
			failure(c, 400, 20232, "Invalid network")
			return
		}
		row, err := adapter.CreateNetwork(c.Request.Context(), input)
		if err != nil {
			failure(c, 409, 20233, "Unable to create network")
			return
		}
		recordNodeAudit(c, deps, view.ID, view.Name, "network.create", "network", row.Name, "success")
		c.JSON(201, envelope{Code: 0, Message: "success", Data: row})
	})
	routes.DELETE("/:id", func(c *gin.Context) {
		adapter, view, ok := resolveNode(c, deps)
		if !ok {
			return
		}
		err := adapter.RemoveNetwork(c.Request.Context(), c.Param("id"))
		result := "success"
		if err != nil {
			result = "failed"
		}
		recordNodeAudit(c, deps, view.ID, view.Name, "network.remove", "network", c.Param("id"), result)
		if err != nil {
			failure(c, 409, 20234, "Unable to remove network")
			return
		}
		success(c, gin.H{"id": c.Param("id")})
	})
}

func registerNodeVolumeRoutes(group *gin.RouterGroup, deps Dependencies) {
	routes := group.Group("/volumes")
	routes.GET("", func(c *gin.Context) {
		adapter, _, ok := resolveNode(c, deps)
		if !ok {
			return
		}
		rows, err := adapter.ListVolumes(c.Request.Context())
		if err != nil {
			failure(c, 503, 20240, "Unable to list volumes")
			return
		}
		success(c, rows)
	})
	routes.GET("/:name", func(c *gin.Context) {
		adapter, _, ok := resolveNode(c, deps)
		if !ok {
			return
		}
		row, err := adapter.InspectVolume(c.Request.Context(), c.Param("name"))
		if err != nil {
			failure(c, 404, 20241, "Volume not found")
			return
		}
		success(c, row)
	})
	routes.POST("", func(c *gin.Context) {
		adapter, view, ok := resolveNode(c, deps)
		if !ok {
			return
		}
		var input volume.CreateRequest
		if c.ShouldBindJSON(&input) != nil {
			failure(c, 400, 20242, "Invalid volume")
			return
		}
		row, err := adapter.CreateVolume(c.Request.Context(), input)
		if err != nil {
			failure(c, 409, 20243, "Unable to create volume")
			return
		}
		recordNodeAudit(c, deps, view.ID, view.Name, "volume.create", "volume", row.Name, "success")
		c.JSON(201, envelope{Code: 0, Message: "success", Data: row})
	})
	routes.DELETE("/:name", func(c *gin.Context) {
		adapter, view, ok := resolveNode(c, deps)
		if !ok {
			return
		}
		err := adapter.RemoveVolume(c.Request.Context(), c.Param("name"))
		result := "success"
		if err != nil {
			result = "failed"
		}
		recordNodeAudit(c, deps, view.ID, view.Name, "volume.remove", "volume", c.Param("name"), result)
		if err != nil {
			failure(c, 409, 20244, "Unable to remove volume")
			return
		}
		success(c, gin.H{"name": c.Param("name")})
	})
}

func coLocatedNode(view node.View) bool {
	if view.ConnectionType == node.ConnectionUnix {
		return true
	}
	endpoint, err := url.Parse(view.Endpoint)
	if err != nil {
		return false
	}
	switch hostname := endpoint.Hostname(); hostname {
	case "localhost", "::1":
		return true
	}
	if strings.HasPrefix(view.Endpoint, "tcp://127.") {
		return true
	}
	return false
}

func resolveNode(c *gin.Context, deps Dependencies) (*docker.Adapter, node.View, bool) {
	view, err := deps.Nodes.Get(c.Request.Context(), c.Param("nodeID"))
	if err != nil {
		failure(c, http.StatusNotFound, 20004, "Docker node not found")
		return nil, node.View{}, false
	}
	adapter, err := deps.Nodes.Runtime(c.Request.Context(), view.ID)
	if err != nil {
		failure(c, http.StatusServiceUnavailable, 20008, err.Error())
		return nil, view, false
	}
	return adapter, view, true
}

func recordNodeAudit(c *gin.Context, deps Dependencies, nodeID, nodeName, action, resourceType, resourceName, result string) {
	var userID *uint
	if value, exists := c.Get("user"); exists {
		user := value.(auth.User)
		userID = &user.ID
	}
	_ = deps.Audit.RecordForNode(c.Request.Context(), nodeID, nodeName, userID, action, resourceType, resourceName, c.ClientIP(), result)
}
