package api

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/suma/suma/server/internal/node"
)

type envelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

func success(c *gin.Context, data any) {
	c.JSON(200, envelope{Code: 0, Message: "success", Data: data})
}
func failure(c *gin.Context, status, code int, message string) {
	c.JSON(status, envelope{Code: code, Message: message, Data: nil})
}

// Legacy aliases must follow the current local node after an in-place Agent
// migration. Existing direct-node installations keep their prior responses.
func deprecatedDefaultNodeFor(deps Dependencies, router *gin.Engine) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Deprecation", "true")
		c.Header("Link", `</api/v1/nodes>; rel="successor-version"`)
		if deps.Nodes != nil {
			view, err := deps.Nodes.Get(c.Request.Context(), "local")
			if err == nil && view.ConnectionType == node.ConnectionAgent {
				path := c.Request.URL.Path
				if strings.HasPrefix(path, "/api/v1/") {
					c.Request.URL.Path = "/api/v1/nodes/local/" + strings.TrimPrefix(path, "/api/v1/")
				} else if strings.HasPrefix(path, "/ws/containers/") {
					c.Request.URL.Path = "/ws/nodes/local/containers/" + strings.TrimPrefix(path, "/ws/containers/")
				}
				router.HandleContext(c)
				c.Abort()
				return
			}
		}
		c.Next()
	}
}
