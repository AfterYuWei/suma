package api

import (
	"context"
	"github.com/suma/suma/server/internal/testutil"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/auth"
	"github.com/suma/suma/server/internal/node"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/task"
)

func TestContainerFileRoutesAuthenticateAndResolveNode(t *testing.T) {
	root := t.TempDir()
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	store, err := secret.Open(filepath.Join(root, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := node.NewService(db, store, "unix:///var/run/docker.sock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = nodes.Close() })
	authService := auth.NewService(db, time.Hour)
	if _, err := authService.Initialize(context.Background(), "admin", "admin@example.test", "", "long-password"); err != nil {
		t.Fatal(err)
	}
	token, _, err := authService.Login(context.Background(), "admin", "long-password", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(Dependencies{Engine: &fakeEngine{}, Auth: authService, Audit: audit.NewService(db), Tasks: task.NewService(db), Nodes: nodes})
	for _, item := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/nodes/local/containers/example/files?path=/"},
		{http.MethodGet, "/api/v1/nodes/local/containers/example/files/content?path=/data/app.yaml"},
		{http.MethodPut, "/api/v1/nodes/local/containers/example/files/content"},
		{http.MethodGet, "/api/v1/nodes/local/containers/example/files/history?path=/data/app.yaml"},
		{http.MethodPost, "/api/v1/nodes/local/containers/example/files/restore"},
		{http.MethodPost, "/api/v1/nodes/local/containers/example/files/actions"},
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(item.method, item.path, nil))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s: %d %s", item.method, item.path, response.Code, response.Body.String())
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/nodes/missing/containers/example/files?path=/", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing node: %d %s", response.Code, response.Body.String())
	}
}
