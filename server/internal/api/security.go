package api

import (
	"context"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/suma/suma/server/internal/settings"
)

type securityContextKey struct{}

var emptySecurityPolicy = &settings.SecurityPolicy{}

func policyFromRequest(request *http.Request) *settings.SecurityPolicy {
	if policy, ok := request.Context().Value(securityContextKey{}).(*settings.SecurityPolicy); ok && policy != nil {
		return policy
	}
	return emptySecurityPolicy
}

func requestClientIP(c *gin.Context) string {
	return policyFromRequest(c.Request).ClientIP(c.Request)
}

func securityBoundary(service *settings.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		policy := emptySecurityPolicy
		if service != nil {
			if current := service.Security(); current != nil {
				policy = current
			}
		}
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), securityContextKey{}, policy))
		if !unsafeMethod(c.Request.Method) || strings.HasPrefix(c.Request.URL.Path, "/api/v1/webhooks/git/") || c.Request.URL.Path == "/api/v1/auth/initialize" {
			c.Next()
			return
		}
		if _, err := c.Cookie(sessionCookie); err == nil {
			if !policy.AllowsOrigin(c.Request) {
				failure(c, http.StatusForbidden, 10007, "Request origin is not allowed")
				c.Abort()
				return
			}
		}
		if hasRequestBody(c.Request) && c.Request.URL.Path != "/api/v1/account/avatar" {
			mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
			if err != nil || mediaType != "application/json" {
				failure(c, http.StatusUnsupportedMediaType, 10008, "JSON content type is required")
				c.Abort()
				return
			}
		}
		c.Next()
	}
}

func unsafeMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func hasRequestBody(request *http.Request) bool {
	return request.ContentLength != 0 || len(request.TransferEncoding) > 0
}

type attemptWindow struct {
	expires time.Time
	count   int
}

type loginLimiter struct {
	mu      sync.Mutex
	windows map[string]attemptWindow
	now     func() time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{windows: make(map[string]attemptWindow), now: time.Now}
}

func (l *loginLimiter) admitIP(ip string) time.Duration {
	return l.admit("ip:"+ip, 10, time.Minute)
}

func (l *loginLimiter) admitAccount(account string) time.Duration {
	return l.admit("account:"+account, 20, 15*time.Minute)
}

// admit counts attempts before password hashing, including successful attempts.
func (l *loginLimiter) admit(key string, limit int, period time.Duration) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.windows) >= 10000 {
		for key, window := range l.windows {
			if !now.Before(window.expires) {
				delete(l.windows, key)
			}
		}
		if len(l.windows) >= 10000 {
			return time.Minute
		}
	}
	window := l.windows[key]
	if !now.Before(window.expires) {
		window = attemptWindow{expires: now.Add(period)}
	}
	if window.count >= limit {
		return window.expires.Sub(now)
	}
	window.count++
	l.windows[key] = window
	return 0
}

func retryAfter(value time.Duration) string {
	seconds := int((value + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	return strconv.Itoa(seconds)
}
