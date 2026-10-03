package api

import (
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/suma/suma/server/internal/projectlogs"
	"net/http"
	"strings"
	"time"
)

func projectLogQuery(c *gin.Context, follow bool) (projectlogs.Query, bool) {
	tail, ok := requestedLogTail(c)
	if !ok {
		return projectlogs.Query{}, false
	}
	split := func(value string) []string {
		if value == "" {
			return nil
		}
		return strings.Split(value, ",")
	}
	q := projectlogs.Query{Tail: tail, Since: c.Query("since"), Until: c.Query("until"), Services: split(c.Query("services")), Containers: split(c.Query("containers")), IncludeOneOff: c.Query("include_one_off") == "true", Follow: follow}
	if err := q.Validate(); err != nil {
		projectLogFailure(c, err)
		return q, false
	}
	return q, true
}
func projectLogFailure(c *gin.Context, err error) {
	status := http.StatusServiceUnavailable
	message := "Unable to read project logs"
	if errors.Is(err, projectlogs.ErrInvalid) {
		status = http.StatusBadRequest
		message = err.Error()
	}
	if errors.Is(err, projectlogs.ErrTooManySources) {
		status = http.StatusUnprocessableEntity
		message = err.Error()
	}
	failure(c, status, 20801, message)
}
func registerProjectLogRoutes(router *gin.Engine, v1 *gin.RouterGroup, deps Dependencies) {
	if deps.ProjectLogs == nil {
		return
	}
	routes := v1.Group("/nodes/:nodeID/projects/compose/:name", requireAuth(deps.Auth))
	routes.GET("/log-sources", func(c *gin.Context) {
		sources, err := deps.ProjectLogs.Sources(c.Request.Context(), c.Param("nodeID"), c.Param("name"))
		if err != nil {
			projectLogFailure(c, err)
			return
		}
		success(c, sources)
	})
	routes.GET("/logs/history", func(c *gin.Context) {
		q, ok := projectLogQuery(c, false)
		if !ok {
			return
		}
		snapshot, err := deps.ProjectLogs.History(c.Request.Context(), c.Param("nodeID"), c.Param("name"), q)
		if err != nil {
			projectLogFailure(c, err)
			return
		}
		success(c, snapshot)
	})
	router.GET("/ws/nodes/:nodeID/projects/compose/:name/logs", requireAuth(deps.Auth), func(c *gin.Context) {
		q, ok := projectLogQuery(c, true)
		if !ok {
			return
		}
		// Validate authorization, project existence and source selection before
		// upgrading so invalid requests retain meaningful HTTP status codes.
		sources, err := deps.ProjectLogs.Sources(c.Request.Context(), c.Param("nodeID"), c.Param("name"))
		if err != nil {
			projectLogFailure(c, err)
			return
		}
		if _, err := projectlogs.SelectSources(sources, q); err != nil {
			projectLogFailure(c, err)
			return
		}
		connection, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		ctx, cancel := context.WithCancel(c.Request.Context())
		defer cancel()
		connection.SetReadDeadline(time.Now().Add(60 * time.Second))
		connection.SetPongHandler(func(string) error { return connection.SetReadDeadline(time.Now().Add(60 * time.Second)) })
		go watchDisconnect(connection, cancel)
		heartbeat := time.NewTicker(20 * time.Second)
		defer heartbeat.Stop()
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-heartbeat.C:
					if connection.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)) != nil {
						cancel()
						return
					}
				}
			}
		}()
		err = deps.ProjectLogs.Follow(ctx, c.Param("nodeID"), c.Param("name"), q, func(event projectlogs.Event) error {
			connection.SetWriteDeadline(time.Now().Add(10 * time.Second))
			return connection.WriteJSON(event)
		})
		if err != nil && ctx.Err() == nil {
			_ = connection.WriteJSON(projectlogs.Event{Type: "source_error", Errors: []projectlogs.SourceError{{Message: "Project log connection ended; review selection or reconnect"}}})
		}
	})
}
