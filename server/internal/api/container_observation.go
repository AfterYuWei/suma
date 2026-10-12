package api

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/containerd/errdefs"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	domain "github.com/suma/suma/server/internal/container"
)

func observationFailure(c *gin.Context, err error) {
	if errors.Is(err, domain.ErrObservationInput) {
		failure(c, 400, 20230, "Invalid container observation request")
		return
	}
	if errdefs.IsNotFound(err) {
		failure(c, 404, 20231, "Container not found")
		return
	}
	failure(c, 503, 20232, "Unable to read Docker observation")
}
func eventQuery(c *gin.Context, live bool) (domain.EventQuery, bool) {
	now := time.Now().UTC()
	q := domain.EventQuery{Since: now.Add(-time.Hour), Limit: 200}
	if !live {
		q.Until = now
	}
	var err error
	if value := c.Query("since"); value != "" {
		q.Since, err = time.Parse(time.RFC3339Nano, value)
		if err != nil {
			observationFailure(c, domain.ErrObservationInput)
			return q, false
		}
	}
	if value := c.Query("until"); value != "" {
		if live {
			observationFailure(c, domain.ErrObservationInput)
			return q, false
		}
		q.Until, err = time.Parse(time.RFC3339Nano, value)
		if err != nil {
			observationFailure(c, domain.ErrObservationInput)
			return q, false
		}
	}
	if value := c.Query("limit"); value != "" {
		q.Limit, err = strconv.Atoi(value)
		if err != nil {
			observationFailure(c, domain.ErrObservationInput)
			return q, false
		}
	}
	if err = domain.ValidateEventQuery(c.Param("id"), q, live); err != nil {
		observationFailure(c, err)
		return q, false
	}
	return q, true
}
func registerContainerObservationRoutes(containers *gin.RouterGroup, deps Dependencies) {
	containers.GET("/:id/overview", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), domain.RuntimeReadTimeout)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		if err := domain.ValidateOverviewID(c.Param("id")); err != nil {
			observationFailure(c, err)
			return
		}
		adapter, _, ok := resolveNode(c, deps)
		if !ok {
			return
		}
		result, err := domain.ReadOverview(ctx, adapter, c.Param("id"))
		if err != nil {
			observationFailure(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		success(c, result)
	})
	containers.GET("/:id/runtime", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), domain.RuntimeReadTimeout)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		adapter, _, ok := resolveNode(c, deps)
		if !ok {
			return
		}
		result, err := domain.ReadDiagnostics(c.Request.Context(), adapter, c.Param("id"))
		if err != nil {
			observationFailure(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		success(c, result)
	})
	containers.GET("/:id/events", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), domain.RuntimeReadTimeout)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		q, ok := eventQuery(c, false)
		if !ok {
			return
		}
		adapter, view, ok := resolveNode(c, deps)
		if !ok {
			return
		}
		result, err := domain.ReadContainerEvents(c.Request.Context(), adapter, view.ID, c.Param("id"), q)
		if err != nil {
			observationFailure(c, err)
			return
		}
		success(c, result)
	})
}
func streamContainerEvents(c *gin.Context, p domain.ObservationProvider, nodeID string) {
	q, ok := eventQuery(c, true)
	if !ok {
		return
	}
	connection, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	defer connection.Close()
	id := c.Param("id")
	rows := make(chan domain.ContainerEvent, 256)
	done := make(chan error, 1)
	connection.SetReadDeadline(time.Now().Add(60 * time.Second))
	connection.SetPongHandler(func(string) error { return connection.SetReadDeadline(time.Now().Add(60 * time.Second)) })
	go watchDisconnect(connection, cancel)
	go func() {
		done <- domain.WatchContainerEvents(ctx, p, nodeID, id, q.Since, func(e domain.ContainerEvent) error {
			select {
			case rows <- e:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			default:
				return errors.New("event subscriber is too slow")
			}
		})
	}()
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	write := func(value any) error {
		connection.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return connection.WriteJSON(value)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-rows:
			if write(gin.H{"type": "events", "entries": []domain.ContainerEvent{e}}) != nil {
				return
			}
		case <-ticker.C:
			if connection.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)) != nil {
				return
			}
		case <-done:
			for {
				select {
				case e := <-rows:
					if write(gin.H{"type": "events", "entries": []domain.ContainerEvent{e}}) != nil {
						return
					}
				default:
					_ = write(gin.H{"type": "error", "code": "stream_closed", "message": "Docker event stream disconnected"})
					return
				}
			}
		}
	}
}
