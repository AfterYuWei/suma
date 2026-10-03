package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/suma/suma/server/internal/ai"
	"github.com/suma/suma/server/internal/notification"
	"gorm.io/gorm"
)

func operationsFailure(c *gin.Context, err error) {
	status := http.StatusUnprocessableEntity
	if errors.Is(err, gorm.ErrRecordNotFound) {
		status = 404
	}
	if errors.Is(err, notification.ErrConflict) || errors.Is(err, ai.ErrConflict) {
		status = 409
	}
	if errors.Is(err, ai.ErrDisabled) || errors.Is(err, ai.ErrScope) {
		status = 403
	}
	if errors.Is(err, ai.ErrBusy) || errors.Is(err, ai.ErrBudget) {
		status = 429
	}
	failure(c, status, 20801, err.Error())
}
func registerNotificationRoutes(router *gin.Engine, v1 *gin.RouterGroup, deps Dependencies) {
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
			ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
			defer cancel()
			if err := service.Test(ctx, c.Param("id")); err != nil {
				operationsFailure(c, err)
				return
			}
			success(c, gin.H{"sent": true})
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
		bindings := v1.Group("/notification-bindings", requireAuth(deps.Auth))
		bindings.GET("", func(c *gin.Context) {
			result, err := service.Bindings(c.Request.Context(), currentUser(c).ID)
			if err != nil {
				operationsFailure(c, err)
				return
			}
			success(c, result)
		})
		bindings.POST("", func(c *gin.Context) {
			var in struct {
				ChannelID string `json:"channel_id"`
			}
			if !bindCleanup(c, &in) {
				return
			}
			row, code, err := service.BeginBinding(c.Request.Context(), currentUser(c).ID, in.ChannelID)
			if err != nil {
				operationsFailure(c, err)
				return
			}
			success(c, gin.H{"binding": row, "code": code})
		})
		bindings.POST("/:id/confirm", func(c *gin.Context) {
			err := service.ConfirmBinding(c.Request.Context(), currentUser(c).ID, c.Param("id"))
			if err != nil {
				operationsFailure(c, err)
				return
			}
			success(c, gin.H{"confirmed": true})
		})
		bindings.DELETE("/:id", func(c *gin.Context) {
			if err := service.RevokeBinding(c.Request.Context(), currentUser(c).ID, c.Param("id")); err != nil {
				operationsFailure(c, err)
				return
			}
			recordAudit(c, deps.Audit, "notification.binding.revoke", "notification_binding", c.Param("id"), "success")
			success(c, gin.H{"revoked": true})
		})
	}
	if deps.AI == nil {
		return
	}
	service := deps.AI
	routes := v1.Group("/ai", requireAuth(deps.Auth))
	actor := func(c *gin.Context) ai.Actor {
		return ai.Actor{UserID: currentUser(c).ID, Source: "site", IP: c.ClientIP()}
	}
	routes.GET("/settings", func(c *gin.Context) { success(c, service.Settings()) })
	routes.PUT("/settings", func(c *gin.Context) {
		var in ai.SettingsInput
		if !bindCleanup(c, &in) {
			return
		}
		row, err := service.SaveSettings(c.Request.Context(), in, actor(c))
		if err != nil {
			operationsFailure(c, err)
			return
		}
		recordAudit(c, deps.Audit, "ai.settings.save", "ai_settings", "AI operations", "success")
		success(c, row)
	})
	routes.POST("/settings/test", func(c *gin.Context) {
		result, err := service.TestModel(c.Request.Context())
		if err != nil {
			operationsFailure(c, err)
			return
		}
		success(c, result)
	})
	routes.GET("/runs", func(c *gin.Context) {
		result, err := service.Runs(c.Request.Context())
		if err != nil {
			operationsFailure(c, err)
			return
		}
		success(c, result)
	})
	routes.POST("/runs", func(c *gin.Context) {
		var in ai.RunInput
		if !bindCleanup(c, &in) {
			return
		}
		row, err := service.Start(c.Request.Context(), in, actor(c))
		if err != nil {
			operationsFailure(c, err)
			return
		}
		c.JSON(202, envelope{Code: 0, Message: "success", Data: row})
	})
	routes.GET("/runs/:id", func(c *gin.Context) {
		row, err := service.Run(c.Request.Context(), c.Param("id"))
		if err != nil {
			operationsFailure(c, err)
			return
		}
		success(c, row)
	})
	routes.GET("/operations", func(c *gin.Context) {
		result, err := service.Operations(c.Request.Context())
		if err != nil {
			operationsFailure(c, err)
			return
		}
		success(c, result)
	})
	routes.GET("/operations/:id", func(c *gin.Context) {
		result, err := service.Operation(c.Request.Context(), c.Param("id"))
		if err != nil {
			operationsFailure(c, err)
			return
		}
		success(c, result)
	})
	routes.POST("/operations/:id/decision", func(c *gin.Context) {
		var in ai.Decision
		if !bindCleanup(c, &in) {
			return
		}
		row, err := service.Decide(c.Request.Context(), c.Param("id"), in, actor(c))
		if err != nil {
			operationsFailure(c, err)
			return
		}
		success(c, row)
	})
	routes.GET("/audit", func(c *gin.Context) {
		result, err := service.Audits(c.Request.Context())
		if err != nil {
			operationsFailure(c, err)
			return
		}
		success(c, result)
	})
	router.GET("/ws/ai/runs/:id", requireAuth(deps.Auth), func(c *gin.Context) {
		if _, err := service.Run(c.Request.Context(), c.Param("id")); err != nil {
			operationsFailure(c, err)
			return
		}
		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		ctx, cancel := context.WithCancel(c.Request.Context())
		defer cancel()
		go func() {
			defer cancel()
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		}()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			run, err := service.Run(ctx, c.Param("id"))
			if err != nil {
				return
			}
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if conn.WriteJSON(run) != nil {
				return
			}
			if run.Status != "running" {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
}
