package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/dockport/dockport/server/internal/audit"
	"github.com/dockport/dockport/server/internal/auth"
	"github.com/dockport/dockport/server/internal/database"
	"github.com/dockport/dockport/server/internal/docker"
	"github.com/dockport/dockport/server/internal/task"
	"github.com/gin-gonic/gin"
)

type fakeEngine struct{ pingErr error }

func (f *fakeEngine) Ping(context.Context) error { return f.pingErr }
func (f *fakeEngine) Info(context.Context) (docker.Info, error) {
	return docker.Info{Name: "test-host", ServerVersion: "29.0"}, nil
}
func (f *fakeEngine) Close() error { return nil }

func testRouter(t *testing.T, engine docker.Engine) *gin.Engine {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	return NewRouter(Dependencies{Engine: engine, Auth: auth.NewService(db, time.Hour), Audit: audit.NewService(db), Tasks: task.NewService(db)})
}

func TestHealth(t *testing.T) {
	response := httptest.NewRecorder()
	testRouter(t, &fakeEngine{}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
}

func TestHealthDegraded(t *testing.T) {
	response := httptest.NewRecorder()
	testRouter(t, &fakeEngine{pingErr: errors.New("offline")}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", response.Code)
	}
}

func TestDockerInfoRequiresAuthentication(t *testing.T) {
	response := httptest.NewRecorder()
	testRouter(t, &fakeEngine{}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/docker/info", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", response.Code)
	}
}

func TestAuthenticationLifecycle(t *testing.T) {
	router := testRouter(t, &fakeEngine{})
	initialize := httptest.NewRecorder()
	router.ServeHTTP(initialize, httptest.NewRequest(http.MethodPost, "/api/v1/auth/initialize", bytes.NewBufferString(`{"username":"admin","password":"long-password","confirm_password":"long-password"}`)))
	if initialize.Code != http.StatusCreated {
		t.Fatalf("initialize: %d %s", initialize.Code, initialize.Body.String())
	}

	login := httptest.NewRecorder()
	router.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"username":"admin","password":"long-password"}`)))
	if login.Code != http.StatusOK {
		t.Fatalf("login: %d %s", login.Code, login.Body.String())
	}
	cookies := login.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly {
		t.Fatal("expected HttpOnly session cookie")
	}

	sessionRequest := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	sessionRequest.AddCookie(cookies[0])
	session := httptest.NewRecorder()
	router.ServeHTTP(session, sessionRequest)
	if session.Code != http.StatusOK {
		t.Fatalf("session: %d %s", session.Code, session.Body.String())
	}

	logoutRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logoutRequest.AddCookie(cookies[0])
	logout := httptest.NewRecorder()
	router.ServeHTTP(logout, logoutRequest)
	if logout.Code != http.StatusOK {
		t.Fatalf("logout: %d", logout.Code)
	}

	invalidRequest := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	invalidRequest.AddCookie(cookies[0])
	invalid := httptest.NewRecorder()
	router.ServeHTTP(invalid, invalidRequest)
	if invalid.Code != http.StatusUnauthorized {
		t.Fatalf("expected invalidated session, got %d", invalid.Code)
	}
}
