package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/suma/suma/server/internal/notification"
	"gorm.io/gorm"
)

func operationsFailure(c *gin.Context, err error) {
	if errors.Is(err, gorm.ErrInvalidField) {
		failure(c, http.StatusServiceUnavailable, 20802, "Database schema is incompatible with this server version; initialize a fresh PostgreSQL database for this version")
		return
	}
	status := http.StatusUnprocessableEntity
	if errors.Is(err, gorm.ErrRecordNotFound) {
		status = 404
	}
	if errors.Is(err, notification.ErrConflict) {
		status = 409
	}
	failure(c, status, 20801, err.Error())
}
func registerNotificationRoutes(v1 *gin.RouterGroup, deps Dependencies) {
	if deps.Notifications != nil {
		service := deps.Notifications
		routes := v1.Group("/notifications", requireAuth(deps.Auth))
		routes.GET("/catalog", func(c *gin.Context) {
			success(c, gin.H{"events": notification.Catalog, "presets": notification.Presets})
		})
		routes.GET("/channels", func(c *gin.Context) {
			rows, err := service.Channels(c.Request.Context())
			if err != nil {
				operationsFailure(c, err)
				return
			}
			success(c, rows)
		})
		save := func(c *gin.Context) {
			var in notification.ChannelInput
			if !bindCleanup(c, &in) {
				return
			}
			row, err := service.SaveChannel(c.Request.Context(), c.Param("id"), in)
			if err != nil {
				operationsFailure(c, err)
				return
			}
			recordAudit(c, deps.Audit, "notification.channel.save", "notification_channel", row.Name, "success")
			success(c, row)
		}
		routes.POST("/channels", save)
		routes.PUT("/channels/:id", save)
		routes.DELETE("/channels/:id", func(c *gin.Context) {
			if err := service.DeleteChannel(c.Request.Context(), c.Param("id")); err != nil {
				operationsFailure(c, err)
				return
			}
			success(c, gin.H{"deleted": true})
			recordAudit(c, deps.Audit, "notification.channel.delete", "notification_channel", c.Param("id"), "success")
		})
		routes.POST("/channels/:id/test", func(c *gin.Context) {
			var in struct {
				ChatID string `json:"chat_id"`
			}
			if c.Request.ContentLength != 0 && !bindCleanup(c, &in) {
				return
			}
			ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
			defer cancel()
			if err := service.Test(ctx, c.Param("id"), in.ChatID); err != nil {
				recordAudit(c, deps.Audit, "notification.channel.test", "notification_channel", c.Param("id"), "failed")
				operationsFailure(c, err)
				return
			}
			success(c, gin.H{"sent": true, "chat_id": in.ChatID})
			recordAudit(c, deps.Audit, "notification.channel.test", "notification_channel", c.Param("id"), "success")
		})
		routes.POST("/channels/:id/check", func(c *gin.Context) {
			result, err := service.Check(c.Request.Context(), c.Param("id"))
			if err != nil {
				operationsFailure(c, err)
				return
			}
			success(c, result)
		})
		routes.GET("/channels/:id/chats", func(c *gin.Context) {
			result, err := service.Chats(c.Request.Context(), c.Param("id"))
			if err != nil {
				operationsFailure(c, err)
				return
			}
			success(c, result)
		})
		routes.GET("/channels/:id/connection", func(c *gin.Context) {
			result, err := service.Connection(c.Request.Context(), c.Param("id"))
			if err != nil {
				operationsFailure(c, err)
				return
			}
			success(c, result)
		})
		routes.GET("/rules", func(c *gin.Context) {
			rows, err := service.Rules(c.Request.Context())
			if err != nil {
				operationsFailure(c, err)
				return
			}
			success(c, rows)
		})
		saveRule := func(c *gin.Context) {
			var in notification.RuleInput
			if !bindCleanup(c, &in) {
				return
			}
			row, err := service.SaveRule(c.Request.Context(), c.Param("id"), in)
			if err != nil {
				operationsFailure(c, err)
				return
			}
			recordAudit(c, deps.Audit, "notification.rule.save", "notification_rule", row.Name, "success")
			success(c, row)
		}
		routes.POST("/rules", saveRule)
		routes.PUT("/rules/:id", saveRule)
		routes.DELETE("/rules/:id", func(c *gin.Context) {
			if err := service.DeleteRule(c.Request.Context(), c.Param("id")); err != nil {
				operationsFailure(c, err)
				return
			}
			recordAudit(c, deps.Audit, "notification.rule.delete", "notification_rule", c.Param("id"), "success")
			success(c, gin.H{"deleted": true})
		})
		routes.GET("/inbox", func(c *gin.Context) {
			result, err := service.Inbox(c.Request.Context(), currentUser(c).ID)
			if err != nil {
				operationsFailure(c, err)
				return
			}
			success(c, result)
		})
		routes.POST("/inbox/:id/read", func(c *gin.Context) {
			if err := service.Read(c.Request.Context(), currentUser(c).ID, c.Param("id")); err != nil {
				operationsFailure(c, err)
				return
			}
			success(c, gin.H{"read": true})
		})
		routes.GET("/deliveries", func(c *gin.Context) {
			result, err := service.Deliveries(c.Request.Context())
			if err != nil {
				operationsFailure(c, err)
				return
			}
			success(c, result)
		})
		routes.POST("/deliveries/:id/resend", func(c *gin.Context) {
			err := service.Resend(c.Request.Context(), c.Param("id"))
			if err != nil {
				operationsFailure(c, err)
				return
			}
			success(c, gin.H{"queued": true})
		})
	}
}
