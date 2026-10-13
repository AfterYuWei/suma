package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/suma/suma/server/internal/auth"
	containerdomain "github.com/suma/suma/server/internal/container"
	"github.com/suma/suma/server/internal/credential"
)

func registerContainerCreationRoutes(routes *gin.RouterGroup, deps Dependencies) {
	routes.POST("", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256<<10)
		var input containerdomain.CreateRequest
		if c.ShouldBindJSON(&input) != nil {
			failure(c, http.StatusBadRequest, 20260, "Invalid container configuration or request too large")
			return
		}
		allowSocket := strings.EqualFold(strings.TrimSpace(c.GetHeader(containerdomain.DockerSocketConfirmationHeader)), "true")
		if err := containerdomain.ValidateCreateRequest(&input, allowSocket); err != nil {
			failure(c, http.StatusBadRequest, 20260, err.Error())
			return
		}
		adapter, view, ok := resolveNode(c, deps)
		if !ok {
			return
		}
		var material credential.RegistryMaterial
		if input.CredentialID != nil {
			if deps.RegistryCredentials == nil {
				failure(c, http.StatusUnprocessableEntity, 20261, "Registry credentials are unavailable")
				return
			}
			if err := deps.RegistryCredentials.AuthorizedForNode(c.Request.Context(), *input.CredentialID, view.ID); err != nil {
				failure(c, http.StatusForbidden, 20261, "Registry credential is not authorized for the selected node")
				return
			}
			var err error
			material, err = deps.RegistryCredentials.Material(c.Request.Context(), *input.CredentialID)
			if err != nil {
				failure(c, http.StatusUnprocessableEntity, 20261, "Registry credential is unavailable")
				return
			}
		}
		actor := containerdomain.CreationActor{IP: requestClientIP(c)}
		if value, exists := c.Get("user"); exists {
			user := value.(auth.User)
			actor.UserID = &user.ID
		}
		row, err := containerdomain.NewCreationService(adapter, deps.Tasks, deps.Audit).CreateForNode(view.ID, view.Name, input, actor, material, allowSocket)
		if err != nil {
			if errors.Is(err, containerdomain.ErrInvalidCreate) {
				failure(c, http.StatusBadRequest, 20260, err.Error())
			} else {
				failure(c, http.StatusInternalServerError, 20262, "Unable to start container creation task")
			}
			return
		}
		c.JSON(http.StatusAccepted, envelope{Code: 0, Message: "success", Data: row})
	})
}
