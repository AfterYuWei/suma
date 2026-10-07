package api

import (
	"context"
	"fmt"
	"github.com/suma/suma/server/internal/testutil"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/auth"
	"github.com/suma/suma/server/internal/task"
)

func TestInitializeRequiresDeploymentKeyAndOrigin(t *testing.T) {
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(db, time.Hour)
	token, _, err := authService.PrepareSetup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(Dependencies{Engine: &fakeEngine{}, Auth: authService, Audit: audit.NewService(db), Tasks: task.NewService(db)})
	forwardedAttempt := 0
	request := func(key, origin, contentType, body, remote string) *httptest.ResponseRecorder {
		if body == "" {
			body = fmt.Sprintf(`{"setup_token":%q,"username":"admin","email":"admin@example.test","password":"long-password","confirm_password":"long-password"}`, key)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/initialize", strings.NewReader(body))
		req.RemoteAddr = remote
		req.Header.Set("Origin", origin)
		req.Header.Set("Content-Type", contentType)
		forwardedAttempt++
		req.Header.Set("X-Forwarded-For", "198.51.100."+strconv.Itoa(forwardedAttempt))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	for _, row := range []struct {
		name, key, origin, contentType, body string
		want                                 int
	}{
		{"missing origin", token, "", "application/json", "", http.StatusForbidden},
		{"other host", token, "http://evil.example", "application/json", "", http.StatusForbidden},
		{"other port", token, "http://example.com:8080", "application/json", "", http.StatusForbidden},
		{"form body", token, "http://example.com", "application/x-www-form-urlencoded", "setup_token=anything", http.StatusUnsupportedMediaType},
		{"missing key", "", "http://example.com", "application/json", "", http.StatusBadRequest},
		{"wrong key", "bad-key", "http://example.com", "application/json", "", http.StatusForbidden},
		{"oversized body", token, "http://example.com", "application/json", `{"padding":"` + strings.Repeat("a", 17<<10) + `"}`, http.StatusBadRequest},
	} {
		response := request(row.key, row.origin, row.contentType, row.body, "192.0.2.10:1234")
		if response.Code != row.want {
			t.Errorf("%s: got %d %s, want %d", row.name, response.Code, response.Body.String(), row.want)
		}
	}
	for range 7 {
		response := request("bad-key", "http://example.com", "application/json", "", "192.0.2.10:1234")
		if response.Code != http.StatusForbidden {
			t.Fatalf("forwarded IP bypassed rate limit: %d %s", response.Code, response.Body.String())
		}
	}
	limited := request(token, "http://example.com", "application/json", "", "192.0.2.10:1234")
	if limited.Code != http.StatusTooManyRequests || limited.Header().Get("Retry-After") == "" {
		t.Fatalf("setup rate limit: %d %s", limited.Code, limited.Body.String())
	}
	created := request(token, "http://example.com", "application/json", "", "192.0.2.11:1234")
	if created.Code != http.StatusCreated {
		t.Fatalf("valid setup: %d %s", created.Code, created.Body.String())
	}
	if replay := request(token, "http://example.com", "application/json", "", "192.0.2.11:1234"); replay.Code != http.StatusConflict {
		t.Fatalf("replayed setup: %d %s", replay.Code, replay.Body.String())
	}
}
