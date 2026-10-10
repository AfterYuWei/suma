//go:build dockersmoke

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/suma/suma/server/internal/testutil"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/agenthub"
	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/auth"
	"github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/node"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/task"
)

// All transports target a disposable Engine, never the host Engine inventory.
func TestRealDockerProjectConfigurationTransports(t *testing.T) {
	if os.Getenv("SUMA_RUN_PROJECT_SMOKE") != "1" {
		t.Skip("set SUMA_RUN_PROJECT_SMOKE=1")
	}
	unixHost := os.Getenv("SUMA_PROJECT_SMOKE_UNIX")
	if !strings.HasPrefix(unixHost, "unix:///tmp/suma-project-live.") || !strings.HasPrefix(os.Getenv("SUMA_PROJECT_SMOKE_DIND"), "suma-project-isolated-") {
		t.Fatal("a disposable Project smoke Engine is required")
	}
	for _, transport := range []string{"unix", "tcp", "agent"} {
		t.Run(transport, func(t *testing.T) { projectConfigurationTransportSmoke(t, transport, unixHost) })
	}
}

func projectConfigurationTransportSmoke(t *testing.T, transport, unixHost string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	root := t.TempDir()
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	store, err := secret.Open(filepath.Join(root, "key"))
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := node.NewService(db, store, unixHost)
	if err != nil {
		t.Fatal(err)
	}
	defer nodes.Close()
	nodeID := "local"
	authentication := auth.NewService(db, time.Hour)
	if transport == "tcp" {
		read := func(name string) string {
			value, err := os.ReadFile(filepath.Join(os.Getenv("SUMA_PROJECT_SMOKE_CERTS"), name))
			if err != nil {
				t.Fatal(err)
			}
			return string(value)
		}
		credential, err := nodes.CreateTLSCredential(ctx, node.TLSCredentialInput{Name: "Project isolated TLS", CA: read("ca.pem"), Certificate: read("cert.pem"), PrivateKey: read("key.pem")})
		if err != nil {
			t.Fatal(err)
		}
		view, err := nodes.Create(ctx, node.Input{Name: "Project isolated TCP", ConnectionType: node.ConnectionTCP, Endpoint: os.Getenv("SUMA_PROJECT_SMOKE_TCP"), TLSMode: node.TLSRequired, TLSCredentialID: &credential.ID, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		nodeID = view.ID
	}
	if transport == "agent" {
		hub, err := agenthub.New()
		if err != nil {
			t.Fatal(err)
		}
		defer hub.Close()
		nodes.SetAgentHub(hub)
		if _, err := nodes.Test(ctx, "local"); err != nil {
			t.Fatal(err)
		}
		enrollment, err := nodes.IssueAgentEnrollment(ctx, node.AgentEnrollmentInput{NodeID: "local", Name: "Local"})
		if err != nil {
			t.Fatal(err)
		}
		cert, key := smokeCertificate(t, root)
		listener, err := net.Listen("tcp", "0.0.0.0:0")
		if err != nil {
			t.Fatal(err)
		}
		publicURL := fmt.Sprintf("https://host.docker.internal:%d", listener.Addr().(*net.TCPAddr).Port)
		server := &http.Server{Handler: NewRouter(Dependencies{Engine: &fakeEngine{}, Nodes: nodes, Agents: hub, Auth: authentication, Audit: audit.NewService(db), AgentPublicURL: publicURL})}
		go func() { _ = server.ServeTLS(listener, cert, key) }()
		defer server.Shutdown(context.Background())
		env := filepath.Join(root, "agent.env")
		if err := os.WriteFile(env, []byte("SUMA_AGENT_TOKEN="+enrollment.Token+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		name := fmt.Sprintf("suma-project-agent-%d", time.Now().UnixNano())
		t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", "-v", name).Run() })
		image := os.Getenv("SUMA_AGENT_SMOKE_IMAGE")
		if image == "" {
			image = "suma-agent:env-only-smoke"
		}
		command := exec.CommandContext(ctx, "docker", "run", "-d", "--name", name, "--add-host", "host.docker.internal:host-gateway", "--env-file", env, "-e", "SUMA_AGENT_SERVER_URL="+publicURL, "-e", "SUMA_AGENT_CA_FILE=/run/secrets/ca.pem", "-v", strings.TrimPrefix(unixHost, "unix://")+":/var/run/docker.sock:ro", "-v", cert+":/run/secrets/ca.pem:ro", image)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("isolated Agent startup: %v %s", err, output)
		}
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			view, err := nodes.Get(ctx, nodeID)
			if err == nil && view.ConnectionType == node.ConnectionAgent && view.Status == "online" {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		view, err := nodes.Get(ctx, nodeID)
		if err != nil || view.ConnectionType != node.ConnectionAgent || view.Status != "online" {
			t.Fatal("isolated Agent did not connect")
		}
	}
	adapter, err := nodes.Runtime(ctx, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := compose.NewRunner("docker compose")
	if err != nil {
		t.Fatal(err)
	}
	tasks := task.NewService(db)
	projects, err := compose.NewService(db, filepath.Join(root, "projects"), runner, tasks, adapter)
	if err != nil {
		t.Fatal(err)
	}
	target, _, err := nodes.ComposeTarget(ctx, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	current := projects.ForNode(nodeID, transport, runner.ForTarget(target), adapter, transport == "unix")
	if _, err := authentication.Initialize(ctx, "admin", "admin@example.test", "", "smoke-long-password"); err != nil {
		t.Fatal(err)
	}
	token, _, err := authentication.Login(ctx, "admin", "smoke-long-password", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	h := projectHTTPHarness{router: NewRouter(Dependencies{Engine: &fakeEngine{}, Nodes: nodes, Auth: authentication, Audit: audit.NewService(db), Tasks: tasks, Compose: projects, ComposeRunner: runner}), cookie: &http.Cookie{Name: sessionCookie, Value: token}, db: db, compose: projects}
	operationsTransportSmoke(t, ctx, root, unixHost, nodeID, transport, db, nodes, adapter, tasks, authentication, current, h.cookie)
	notificationTransportSmoke(t, ctx, root, nodeID, transport, db, adapter, tasks, current)
	name := fmt.Sprintf("visual-%s-%d", transport, time.Now().UnixNano())
	path := "/api/v1/nodes/" + nodeID + "/projects"
	content := "# visual source\nservices:\n  app:\n    image: alpine:3.24\n    command: [sleep, '300']\n    environment:\n      APP_VALUE: ${APP_VALUE}\n    volumes:\n      - data:/data\n    healthcheck:\n      test: [CMD, 'true']\n      interval: 1s\n    cpus: 0.25\n    mem_limit: 32M\n    x-preserved: value\nvolumes:\n  data: {}\n"
	body, _ := json.Marshal(map[string]string{"backend": "compose", "name": name, "compose": content, "environment": "APP_VALUE=project-smoke\n"})
	validated := h.request(http.MethodPost, path+"/validate", body)
	if validated.Code != 200 {
		t.Fatalf("new validation %d: %s", validated.Code, validated.Body)
	}
	created := h.request(http.MethodPost, path, body)
	if created.Code != 201 {
		t.Fatalf("create %d: %s", created.Code, created.Body)
	}
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 30*time.Second)
		defer done()
		_ = current.ForceRemove(cleanup, name, false)
	})
	var response struct {
		Data compose.Project `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.Compose != content || response.Data.Revision == "" {
		t.Fatal("source/revision mismatch")
	}
	rows, err := current.Services(ctx, name)
	if err != nil || len(rows) != 0 {
		t.Fatal("saving started containers")
	}
	update, _ := json.Marshal(map[string]string{"compose": content, "environment": "APP_VALUE=updated-smoke\n", "expected_revision": response.Data.Revision})
	saved := h.request(http.MethodPut, path+"/compose/"+name, update)
	if saved.Code != 200 {
		t.Fatalf("save %d: %s", saved.Code, saved.Body)
	}
	stale, _ := json.Marshal(map[string]string{"expected_revision": response.Data.Revision})
	if result := h.request(http.MethodPost, path+"/compose/"+name+"/actions/up", stale); result.Code != 409 {
		t.Fatal("stale deployment accepted")
	}
	if err := json.Unmarshal(saved.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	deployment, _ := json.Marshal(map[string]string{"expected_revision": response.Data.Revision})
	started := h.request(http.MethodPost, path+"/compose/"+name+"/actions/up", deployment)
	if started.Code != 202 {
		t.Fatalf("deployment %d: %s", started.Code, started.Body)
	}
	var result struct {
		Data database.Task `json:"data"`
	}
	if err := json.Unmarshal(started.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	for {
		work, err := tasks.Get(ctx, result.Data.ID)
		if err != nil {
			t.Fatal(err)
		}
		if work.Status == task.StatusSuccess {
			break
		}
		if work.Status == task.StatusFailed || work.Status == task.StatusCanceled {
			t.Fatalf("deployment: %s", work.Message)
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	rows, err = current.Services(ctx, name)
	if err != nil || len(rows) != 1 || rows[0].State != "running" {
		t.Fatalf("runtime mismatch: %v", err)
	}
	if transport != "unix" {
		unsafe, _ := json.Marshal(map[string]string{"name": name, "compose": "services: {app: {image: alpine:3.24, volumes: ['./relative:/data']}}\n", "environment": ""})
		if result := h.request(http.MethodPost, path+"/validate", unsafe); result.Code != 422 {
			t.Fatal("remote relative bind accepted")
		}
	}
}
