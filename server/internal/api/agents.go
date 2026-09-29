package api

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/suma/suma/server/internal/node"
	"github.com/suma/suma/server/internal/settings"
)

func registerAgentRoutes(router *gin.Engine, v1 *gin.RouterGroup, deps Dependencies) {
	enrollments := v1.Group("/agent-enrollments", requireAuth(deps.Auth))
	enrollments.POST("", func(c *gin.Context) {
		publicURL, ok := agentPublicURL(c, deps)
		if !ok {
			failure(c, http.StatusServiceUnavailable, 20501, "Open SUMA over HTTPS or set SUMA_AGENT_PUBLIC_URL to a valid reachable HTTPS origin before pairing Agents")
			return
		}
		var input node.AgentEnrollmentInput
		if c.ShouldBindJSON(&input) != nil {
			failure(c, 400, 20502, "Invalid Agent enrollment")
			return
		}
		value, err := deps.Nodes.IssueAgentEnrollment(c.Request.Context(), input)
		if err != nil {
			failure(c, 422, 20503, err.Error())
			return
		}
		c.Header("Cache-Control", "no-store")
		recordAudit(c, deps.Audit, "agent.enrollment.create", "node", value.NodeID, "success")
		c.JSON(http.StatusCreated, envelope{Code: 0, Message: "success", Data: gin.H{"node_id": value.NodeID, "token": value.Token, "expires_at": value.ExpiresAt, "public_url": publicURL}})
	})
	enrollments.GET("/:nodeID", func(c *gin.Context) {
		value, err := deps.Nodes.GetAgentEnrollment(c.Request.Context(), c.Param("nodeID"))
		if err != nil {
			failure(c, 404, 20504, "Agent enrollment not found")
			return
		}
		success(c, value)
	})
	enrollments.POST("/:nodeID/reissue", func(c *gin.Context) {
		publicURL, ok := agentPublicURL(c, deps)
		if !ok {
			failure(c, http.StatusServiceUnavailable, 20501, "Open SUMA over HTTPS or set SUMA_AGENT_PUBLIC_URL to a valid reachable HTTPS origin before pairing Agents")
			return
		}
		view, err := deps.Nodes.Get(c.Request.Context(), c.Param("nodeID"))
		if err != nil {
			failure(c, 404, 20504, "Node not found")
			return
		}
		value, err := deps.Nodes.IssueAgentEnrollment(c.Request.Context(), node.AgentEnrollmentInput{NodeID: view.ID, Name: view.Name})
		if err != nil {
			failure(c, 422, 20503, err.Error())
			return
		}
		c.Header("Cache-Control", "no-store")
		recordAudit(c, deps.Audit, "agent.enrollment.reissue", "node", value.NodeID, "success")
		success(c, gin.H{"node_id": value.NodeID, "token": value.Token, "expires_at": value.ExpiresAt, "public_url": publicURL})
	})
	enrollments.DELETE("/:nodeID", func(c *gin.Context) {
		if err := deps.Nodes.CancelAgentEnrollment(c.Request.Context(), c.Param("nodeID")); err != nil {
			failure(c, 409, 20505, err.Error())
			return
		}
		recordAudit(c, deps.Audit, "agent.enrollment.cancel", "node", c.Param("nodeID"), "success")
		success(c, gin.H{"node_id": c.Param("nodeID")})
	})
	v1.POST("/nodes/:nodeID/agent/revoke", requireAuth(deps.Auth), func(c *gin.Context) {
		view, err := deps.Nodes.Get(c.Request.Context(), c.Param("nodeID"))
		if err != nil {
			failure(c, 404, 20504, "Node not found")
			return
		}
		if err := deps.Nodes.RevokeAgent(c.Request.Context(), view.ID); err != nil {
			failure(c, 500, 20506, "Unable to revoke Agent")
			return
		}
		recordAudit(c, deps.Audit, "agent.revoke", "node", view.ID, "success")
		success(c, gin.H{"node_id": view.ID})
	})
	registerAgentTransport(router, v1, deps)
}

func agentPublicURL(c *gin.Context, deps Dependencies) (string, bool) {
	if deps.AgentPublicURL == "" {
		origin, err := settings.ParseOrigin(c.GetHeader("Origin"))
		if err != nil || !strings.HasPrefix(origin, "https://") || !policyFromRequest(c.Request).AllowsOrigin(c.Request) {
			return "", false
		}
		return origin, true
	}
	parsed, err := url.Parse(deps.AgentPublicURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return "", false
	}
	return strings.TrimRight(deps.AgentPublicURL, "/"), true
}

func registerAgentTransport(router *gin.Engine, v1 *gin.RouterGroup, deps Dependencies) {
	limiter := newLoginLimiter()
	v1.POST("/agents/enroll", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024)
		if delay := limiter.admit("agent-enroll:"+requestClientIP(c), 20, time.Minute); delay > 0 {
			c.Header("Retry-After", retryAfter(delay))
			failure(c, 429, 20507, "Too many Agent enrollment attempts")
			return
		}
		var input struct {
			Protocol int    `json:"protocol"`
			Version  string `json:"version"`
		}
		if c.ShouldBindJSON(&input) != nil {
			failure(c, 400, 20508, "Invalid Agent enrollment request")
			return
		}
		secret := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
		if c.GetHeader("Authorization") == "" || secret == c.GetHeader("Authorization") {
			failure(c, 401, 20509, "Agent enrollment token required")
			return
		}
		id, credential, err := deps.Nodes.ClaimAgentEnrollment(c.Request.Context(), secret, input.Protocol)
		if err != nil {
			failure(c, 401, 20510, err.Error())
			return
		}
		success(c, gin.H{"node_id": id, "credential": credential})
	})
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "" }, ReadBufferSize: 32 << 10, WriteBufferSize: 32 << 10}
	authenticate := func(c *gin.Context, stream bool) (string, bool) {
		id := c.GetHeader("X-SUMA-Agent-Node-ID")
		version, _ := strconv.Atoi(c.GetHeader("X-SUMA-Agent-Protocol"))
		secret := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
		if c.GetHeader("Authorization") == "" || secret == c.GetHeader("Authorization") {
			failure(c, 401, 20511, "Agent authentication failed")
			return "", false
		}
		var err error
		if stream {
			err = deps.Nodes.AuthenticateAgentStream(c.Request.Context(), id, secret, version)
		} else {
			err = deps.Nodes.AuthenticateAgent(c.Request.Context(), id, secret, version)
		}
		if err != nil {
			if errors.Is(err, node.ErrAgentProtocol) {
				failure(c, http.StatusUpgradeRequired, 20512, err.Error())
			} else {
				failure(c, 401, 20511, "Agent authentication failed")
			}
			return "", false
		}
		return id, true
	}
	router.GET("/ws/agents/control", func(c *gin.Context) {
		id, ok := authenticate(c, false)
		if !ok {
			return
		}
		ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		done, err := deps.Agents.Attach(id, ws)
		if err != nil {
			_ = ws.Close()
			return
		}
		if err := deps.Nodes.ActivateAgent(c.Request.Context(), id, c.GetHeader("X-SUMA-Agent-Version"), done); err != nil {
			slog.Warn("agent activation failed", "node_id", id, "error", err)
			_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "Agent activation failed; check the node error in SUMA"), time.Now().Add(time.Second))
			deps.Agents.Detach(id, done)
			return
		}
		<-done
	})
	router.GET("/ws/agents/streams/:streamID", func(c *gin.Context) {
		id, ok := authenticate(c, true)
		if !ok {
			return
		}
		ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		_ = deps.Agents.AttachStream(c.Request.Context(), id, c.Param("streamID"), ws)
	})
}
