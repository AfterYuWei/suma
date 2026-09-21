package api

import (
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/suma/suma/server/internal/auth"
)

const webAuthnCeremonyHeader = "X-WebAuthn-Ceremony"

type beginPasskeyRequest struct {
	Name            string `json:"name" binding:"required"`
	CurrentPassword string `json:"current_password" binding:"required"`
	Code            string `json:"code"`
}

type renamePasskeyRequest struct {
	Name string `json:"name" binding:"required"`
}

type deletePasskeyRequest struct {
	CurrentPassword string `json:"current_password" binding:"required"`
	Code            string `json:"code"`
}

func registerPasskeyRoutes(v1, account *gin.RouterGroup, deps Dependencies) {
	v1.POST("/auth/passkey/options", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		origin, rpID, ok := passkeyOrigin(c)
		if !ok {
			failure(c, http.StatusBadRequest, 11201, "Passkeys require a secure same-origin connection")
			return
		}
		options, err := deps.Auth.BeginPasskeyLogin(c.Request.Context(), origin, rpID)
		if err != nil {
			failure(c, http.StatusInternalServerError, 11202, "Unable to start passkey login")
			return
		}
		success(c, options)
	})

	v1.POST("/auth/passkey", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		if _, _, ok := passkeyOrigin(c); !ok {
			failure(c, http.StatusBadRequest, 11201, "Passkeys require a secure same-origin connection")
			return
		}
		ceremonyToken := c.GetHeader(webAuthnCeremonyHeader)
		if ceremonyToken == "" {
			failure(c, http.StatusBadRequest, 11203, "Passkey ceremony token is required")
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128<<10)
		token, user, err := deps.Auth.FinishPasskeyLogin(c.Request.Context(), ceremonyToken, c.ClientIP(), c.Request)
		if err != nil {
			failure(c, http.StatusUnauthorized, 11204, "Passkey verification failed")
			return
		}
		setSessionCookie(c, token, deps.CookieSecure)
		_ = deps.Audit.Record(c.Request.Context(), &user.ID, "login", "passkey", user.Username, c.ClientIP(), "success")
		success(c, user)
	})

	account.GET("/passkeys", func(c *gin.Context) {
		passkeys, err := deps.Auth.ListPasskeys(c.Request.Context(), currentUser(c).ID)
		if err != nil {
			failure(c, http.StatusInternalServerError, 11205, "Unable to read passkeys")
			return
		}
		success(c, passkeys)
	})

	account.POST("/passkeys/options", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		user := currentUser(c)
		var input beginPasskeyRequest
		if c.ShouldBindJSON(&input) != nil {
			failure(c, http.StatusBadRequest, 11206, "Name and current password are required")
			recordAccountAudit(deps.Audit, c, user, "account.passkey.create", "failed")
			return
		}
		origin, rpID, ok := passkeyOrigin(c)
		if !ok {
			failure(c, http.StatusBadRequest, 11201, "Passkeys require a secure same-origin connection")
			return
		}
		options, err := deps.Auth.BeginPasskeyRegistration(c.Request.Context(), user.ID, input.CurrentPassword, input.Code, input.Name, origin, rpID)
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, auth.ErrCurrentPassword) || errors.Is(err, auth.ErrInvalidTwoFactor) {
				status = http.StatusForbidden
			}
			failure(c, status, 11207, err.Error())
			recordAccountAudit(deps.Audit, c, user, "account.passkey.create", "failed")
			return
		}
		success(c, options)
	})

	account.POST("/passkeys", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		user := currentUser(c)
		if _, _, ok := passkeyOrigin(c); !ok {
			failure(c, http.StatusBadRequest, 11201, "Passkeys require a secure same-origin connection")
			return
		}
		ceremonyToken := c.GetHeader(webAuthnCeremonyHeader)
		if ceremonyToken == "" {
			failure(c, http.StatusBadRequest, 11203, "Passkey ceremony token is required")
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128<<10)
		currentToken, _ := c.Cookie(sessionCookie)
		passkey, err := deps.Auth.FinishPasskeyRegistration(c.Request.Context(), user.ID, currentToken, ceremonyToken, c.Request)
		if err != nil {
			failure(c, http.StatusBadRequest, 11208, "Passkey registration failed")
			recordAccountAudit(deps.Audit, c, user, "account.passkey.create", "failed")
			return
		}
		recordAccountAudit(deps.Audit, c, user, "account.passkey.create", "success")
		c.JSON(http.StatusCreated, envelope{Code: 0, Message: "success", Data: passkey})
	})

	account.PATCH("/passkeys/:id", func(c *gin.Context) {
		user := currentUser(c)
		id, err := strconv.ParseUint(c.Param("id"), 10, 64)
		var input renamePasskeyRequest
		if err != nil || c.ShouldBindJSON(&input) != nil {
			failure(c, http.StatusBadRequest, 11209, "Valid passkey ID and name are required")
			return
		}
		if err := deps.Auth.RenamePasskey(c.Request.Context(), user.ID, uint(id), input.Name); err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, auth.ErrPasskeyNotFound) {
				status = http.StatusNotFound
			}
			failure(c, status, 11210, err.Error())
			recordAccountAudit(deps.Audit, c, user, "account.passkey.rename", "failed")
			return
		}
		recordAccountAudit(deps.Audit, c, user, "account.passkey.rename", "success")
		success(c, gin.H{})
	})

	account.DELETE("/passkeys/:id", func(c *gin.Context) {
		user := currentUser(c)
		id, err := strconv.ParseUint(c.Param("id"), 10, 64)
		var input deletePasskeyRequest
		if err != nil || c.ShouldBindJSON(&input) != nil {
			failure(c, http.StatusBadRequest, 11211, "Valid passkey ID and current password are required")
			return
		}
		currentToken, _ := c.Cookie(sessionCookie)
		err = deps.Auth.DeletePasskey(c.Request.Context(), user.ID, uint(id), currentToken, input.CurrentPassword, input.Code)
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, auth.ErrCurrentPassword) || errors.Is(err, auth.ErrInvalidTwoFactor) {
				status = http.StatusForbidden
			}
			if errors.Is(err, auth.ErrPasskeyNotFound) {
				status = http.StatusNotFound
			}
			failure(c, status, 11212, err.Error())
			recordAccountAudit(deps.Audit, c, user, "account.passkey.delete", "failed")
			return
		}
		recordAccountAudit(deps.Audit, c, user, "account.passkey.delete", "success")
		success(c, gin.H{})
	})
}

func passkeyOrigin(c *gin.Context) (string, string, bool) {
	origin, rpID, err := auth.NormalizeWebAuthnOrigin(c.GetHeader("Origin"))
	if err != nil {
		return "", "", false
	}
	host := strings.TrimSpace(c.Request.Host)
	if value, _, splitErr := net.SplitHostPort(host); splitErr == nil {
		host = value
	}
	host = strings.Trim(strings.ToLower(host), "[]")
	return origin, rpID, host == rpID
}
