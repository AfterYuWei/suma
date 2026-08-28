package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	dockercontainer "github.com/docker/docker/api/types/container"
	"github.com/gin-gonic/gin"
	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/auth"
	composeService "github.com/suma/suma/server/internal/compose"
	containerdomain "github.com/suma/suma/server/internal/container"
	credentialService "github.com/suma/suma/server/internal/credential"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/docker"
	gitService "github.com/suma/suma/server/internal/git"
	nodeService "github.com/suma/suma/server/internal/node"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/task"
	"gorm.io/gorm"
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

func TestHealthDoesNotDependOnDocker(t *testing.T) {
	response := httptest.NewRecorder()
	testRouter(t, &fakeEngine{pingErr: context.DeadlineExceeded}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("expected control plane health 200, got %d", response.Code)
	}
}

func TestDockerInfoRequiresAuthentication(t *testing.T) {
	response := httptest.NewRecorder()
	testRouter(t, &fakeEngine{}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/docker/info", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", response.Code)
	}
}

func TestGitDeliveryAPIsRequireAuthentication(t *testing.T) {
	router := testRouter(t, &fakeEngine{})
	requests := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/credentials/git"},
		{http.MethodPost, "/api/v1/credentials/git"},
		{http.MethodGet, "/api/v1/credentials/registries"},
		{http.MethodPost, "/api/v1/credentials/registries"},
		{http.MethodPost, "/api/v1/compose/batch"},
		{http.MethodGet, "/api/v1/delivery-projects"},
		{http.MethodPost, "/api/v1/delivery-projects"},
		{http.MethodGet, "/api/v1/delivery-projects/production/configuration"},
		{http.MethodPut, "/api/v1/delivery-projects/production/configuration"},
		{http.MethodPost, "/api/v1/delivery-projects/production/sync"},
		{http.MethodGet, "/api/v1/delivery-projects/production/drift"},
		{http.MethodGet, "/api/v1/delivery-projects/production/releases"},
		{http.MethodPost, "/api/v1/delivery-projects/production/releases/1/approve"},
		{http.MethodPost, "/api/v1/delivery-projects/production/releases/1/reject"},
		{http.MethodPost, "/api/v1/delivery-projects/production/releases/1/deploy"},
		{http.MethodPost, "/api/v1/delivery-projects/production/releases/1/rollback"},
	}
	for _, item := range requests {
		t.Run(item.method+" "+item.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(item.method, item.path, nil))
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d: %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestLegacyGitCredentialRouteIsRemoved(t *testing.T) {
	response := httptest.NewRecorder()
	testRouter(t, &fakeEngine{}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/git/credentials", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("expected removed route to return 404, got %d: %s", response.Code, response.Body.String())
	}
}

func TestAuthenticationCenterCredentialHTTP(t *testing.T) {
	root := t.TempDir()
	db, err := database.Open(filepath.Join(root, "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := secret.Open(filepath.Join(root, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(db, time.Hour)
	if _, err := authService.Initialize(context.Background(), "admin", "long-password"); err != nil {
		t.Fatal(err)
	}
	token, _, err := authService.Login(context.Background(), "admin", "long-password", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(Dependencies{Engine: &fakeEngine{}, Auth: authService, Audit: audit.NewService(db), Tasks: task.NewService(db), GitCredentials: gitService.NewCredentialService(db, store), RegistryCredentials: credentialService.NewRegistryService(db, store)})
	request := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	gitCreated := request(http.MethodPost, "/api/v1/credentials/git", `{"name":"deploy","auth_type":"http_token","secret":"top-secret"}`)
	if gitCreated.Code != http.StatusCreated || strings.Contains(gitCreated.Body.String(), "top-secret") {
		t.Fatalf("Git credential response: %d %s", gitCreated.Code, gitCreated.Body.String())
	}
	registryCreated := request(http.MethodPost, "/api/v1/credentials/registries", `{"name":"registry","server_address":"registry.example.test:5000","auth_type":"basic","username":"robot","secret":"registry-secret"}`)
	if registryCreated.Code != http.StatusCreated || strings.Contains(registryCreated.Body.String(), "registry-secret") {
		t.Fatalf("registry credential response: %d %s", registryCreated.Code, registryCreated.Body.String())
	}
	listed := request(http.MethodGet, "/api/v1/credentials/registries", "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), "registry.example.test:5000") || strings.Contains(listed.Body.String(), "registry-secret") {
		t.Fatalf("registry list response: %d %s", listed.Code, listed.Body.String())
	}
	duplicate := request(http.MethodPost, "/api/v1/credentials/git", `{"name":"deploy","auth_type":"http_token","secret":"another"}`)
	if duplicate.Code != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate name response: %d %s", duplicate.Code, duplicate.Body.String())
	}
}

func TestGitWebhookIsPublicButUnknownHookIsNotDisclosed(t *testing.T) {
	response := httptest.NewRecorder()
	testRouter(t, &fakeEngine{}).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/git/github/unknown", bytes.NewBufferString(`{}`)))
	if response.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", response.Code, response.Body.String())
	}
}

func TestUnknownAPIEndpointReturnsJSONInsteadOfSPA(t *testing.T) {
	response := httptest.NewRecorder()
	testRouter(t, &fakeEngine{}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/not-a-real-endpoint", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", response.Code, response.Body.String())
	}
	if contentType := response.Header().Get("Content-Type"); !strings.Contains(contentType, "application/json") {
		t.Fatalf("Content-Type = %q, want JSON", contentType)
	}
	var body envelope
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("unknown API response is not JSON: %v; body=%s", err, response.Body.String())
	}
	if body.Code == 0 || body.Message != "API endpoint not found" {
		t.Fatalf("unknown API response = %#v", body)
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

var projectDockerVersion = regexp.MustCompile(`^/v[0-9]+\.[0-9]+`)

type projectHTTPHarness struct {
	router  *gin.Engine
	cookie  *http.Cookie
	db      *gorm.DB
	compose *composeService.Service
}

type projectEmptyContainers struct{ containerdomain.Service }

func (projectEmptyContainers) List(context.Context) ([]containerdomain.Summary, error) {
	return nil, nil
}

func newProjectHTTPHarness(t *testing.T) projectHTTPHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)
	containerID := strings.Repeat("ab", 32)
	imageID := "sha256:" + strings.Repeat("cd", 32)
	labels := map[string]string{
		composeService.ProjectLabel:         "shop",
		composeService.ServiceLabel:         "web",
		composeService.ContainerNumberLabel: "1",
		composeService.ConfigHashLabel:      "runtime-hash",
	}
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		path := projectDockerVersion.ReplaceAllString(request.URL.Path, "")
		switch path {
		case "/_ping":
			w.Header().Set("Api-Version", "1.44")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, "OK")
		case "/info":
			writeProjectDockerJSON(w, map[string]any{"ID": "engine-project-tests", "Name": "project-tests", "ServerVersion": "29.0", "OSType": "linux", "Architecture": "amd64"})
		case "/containers/json":
			writeProjectDockerJSON(w, []dockercontainer.Summary{{ID: containerID, Names: []string{"/shop-web-1"}, Image: "example/web:v1", ImageID: imageID, Created: 1, State: "running", Status: "Up", Labels: labels}})
		case "/containers/" + containerID + "/json":
			writeProjectDockerJSON(w, map[string]any{
				"Id": containerID, "Name": "/shop-web-1", "Image": imageID, "Created": "2026-08-28T00:00:00Z",
				"Config":          map[string]any{"Image": "example/web:v1", "Env": []string{"BASE=image", "DATABASE_PASSWORD=http-secret"}, "Labels": labels},
				"HostConfig":      map[string]any{"RestartPolicy": map[string]any{"Name": "unless-stopped"}},
				"State":           map[string]any{"Status": "running", "Running": true},
				"NetworkSettings": map[string]any{"Networks": map[string]any{}},
				"Mounts":          []any{},
			})
		case "/images/" + imageID + "/json":
			writeProjectDockerJSON(w, map[string]any{"Id": imageID, "Config": map[string]any{"Env": []string{"BASE=image"}}})
		default:
			http.Error(w, "unexpected Docker API path "+path, http.StatusNotFound)
		}
	}))
	t.Cleanup(engine.Close)

	root := t.TempDir()
	db, err := database.Open(filepath.Join(root, "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := secret.Open(filepath.Join(root, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := nodeService.NewService(db, store, engine.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = nodes.Close() })
	runner, err := composeService.NewRunner("/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	tasks := task.NewService(db)
	projects, err := composeService.NewService(db, filepath.Join(root, "projects"), runner, tasks, projectEmptyContainers{})
	if err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(db, time.Hour)
	if _, err := authService.Initialize(context.Background(), "admin", "long-password"); err != nil {
		t.Fatal(err)
	}
	token, _, err := authService.Login(context.Background(), "admin", "long-password", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(Dependencies{Engine: &fakeEngine{}, Auth: authService, Audit: audit.NewService(db), Tasks: tasks, Compose: projects, ComposeRunner: runner, Nodes: nodes})
	return projectHTTPHarness{router: router, cookie: &http.Cookie{Name: sessionCookie, Value: token}, db: db, compose: projects}
}

func writeProjectDockerJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func (h projectHTTPHarness) request(method, path string, body []byte) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(h.cookie)
	response := httptest.NewRecorder()
	h.router.ServeHTTP(response, request)
	return response
}

func TestProjectSummaryHTTPDoesNotExposeManagedConfiguration(t *testing.T) {
	harness := newProjectHTTPHarness(t)
	if _, err := harness.compose.Create(context.Background(), "managed", "services:\n  web:\n    image: example/private:v1\n    environment:\n      PASSWORD: list-secret\n", "PASSWORD=list-secret\n"); err != nil {
		t.Fatal(err)
	}
	response := harness.request(http.MethodGet, "/api/v1/nodes/local/projects", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("list Projects: %d %s", response.Code, response.Body.String())
	}
	text := response.Body.String()
	for _, forbidden := range []string{"list-secret", "example/private:v1", `"compose":`, `"environment":`, `"path":`, `"config_files":`} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("Project Summary HTTP response contains %q: %s", forbidden, text)
		}
	}
}

func TestConcurrentProjectTakeoverHTTPAllowsOneWinnerWithoutAuditSecrets(t *testing.T) {
	harness := newProjectHTTPHarness(t)
	preview := harness.request(http.MethodPost, "/api/v1/nodes/local/projects/compose/shop/takeover/preview", nil)
	if preview.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", preview.Code, preview.Body.String())
	}
	var value struct {
		Data composeService.ProjectTakeoverDraft `json:"data"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	if value.Data.Fingerprint == "" || !strings.Contains(value.Data.Environment, "http-secret") {
		t.Fatalf("preview data = %#v", value.Data)
	}
	payload, err := json.Marshal(composeService.TakeoverInput{Fingerprint: value.Data.Fingerprint, ConfirmationName: "shop", Compose: value.Data.Compose, Environment: value.Data.Environment})
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	responses := make(chan *httptest.ResponseRecorder, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			responses <- harness.request(http.MethodPost, "/api/v1/nodes/local/projects/compose/shop/takeover", payload)
		}()
	}
	close(start)
	wait.Wait()
	close(responses)
	statuses := map[int]int{}
	for response := range responses {
		statuses[response.Code]++
	}
	if statuses[http.StatusCreated] != 1 || statuses[http.StatusConflict] != 1 {
		t.Fatalf("concurrent takeover statuses = %#v", statuses)
	}

	var audits []database.AuditLog
	if err := harness.db.Where("action = ?", "project.takeover").Find(&audits).Error; err != nil {
		t.Fatal(err)
	}
	content, err := json.Marshal(audits)
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 || strings.Contains(string(content), "http-secret") {
		t.Fatalf("takeover audits = %s", content)
	}
}
