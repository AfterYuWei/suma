//go:build dockersmoke

package api

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	dockercontainer "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/gin-gonic/gin"
	"github.com/suma/suma/server/internal/agenthub"
	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/auth"
	"github.com/suma/suma/server/internal/cd"
	"github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/database"
	gitrepo "github.com/suma/suma/server/internal/git"
	"github.com/suma/suma/server/internal/node"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/task"
)

func TestRealDockerAgentInPlaceComposeReconnectAndRevoke(t *testing.T) {
	if os.Getenv("SUMA_RUN_DOCKER_SMOKE") != "1" {
		t.Skip("set SUMA_RUN_DOCKER_SMOKE=1")
	}
	image := os.Getenv("SUMA_AGENT_SMOKE_IMAGE")
	if image == "" {
		image = "suma-agent:dev"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	docker := func(args ...string) string {
		t.Helper()
		command := exec.CommandContext(ctx, "docker", args...)
		value, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("docker %s: %v: %s", strings.Join(args, " "), err, value)
		}
		return strings.TrimSpace(string(value))
	}
	docker("image", "inspect", image)
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	db, err := database.Open(filepath.Join(root, "suma.db"))
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
	defer nodes.Close()
	hub, err := agenthub.New()
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Close()
	nodes.SetAgentHub(hub)
	issued, err := nodes.IssueAgentEnrollment(ctx, node.AgentEnrollmentInput{Name: "New Agent node"})
	if err != nil {
		t.Fatal(err)
	}
	certFile, keyFile := smokeCertificate(t, root)
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	publicURL := "https://host.docker.internal:" + strconv.Itoa(port)
	authService := auth.NewService(db, time.Hour)
	router := NewRouter(Dependencies{Nodes: nodes, Agents: hub, Auth: authService, Audit: audit.NewService(db), AgentPublicURL: publicURL})
	server := &http.Server{Handler: router}
	go func() { _ = server.ServeTLS(listener, certFile, keyFile) }()
	defer server.Shutdown(context.Background())
	identityDir := filepath.Join(root, "identity")
	if err := os.Mkdir(identityDir, 0o700); err != nil {
		t.Fatal(err)
	}
	environmentFile := filepath.Join(root, "agent.env")
	if err := os.WriteFile(environmentFile, []byte("SUMA_AGENT_TOKEN="+issued.Token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("suma-agent-smoke-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
	startAgent := func() {
		t.Helper()
		docker("run", "-d", "--name", name, "--add-host", "host.docker.internal:host-gateway", "--env-file", environmentFile, "-e", "SUMA_AGENT_SERVER_URL="+publicURL,
			"-e", "SUMA_AGENT_CA_FILE=/run/secrets/ca.pem",
			"-v", "/var/run/docker.sock:/var/run/docker.sock:ro", "-v", certFile+":/run/secrets/ca.pem:ro",
			"-v", identityDir+":/var/lib/suma-agent", image)
	}
	startAgent()
	await := func(nodeID, want string) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			view, err := nodes.Get(ctx, nodeID)
			if err == nil && view.Status == want && (want != "online" || view.ConnectionType == node.ConnectionAgent) {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		view, _ := nodes.Get(ctx, nodeID)
		t.Fatalf("node did not become %s: %+v; Agent logs: %s", want, view, docker("logs", name))
	}
	await(issued.NodeID, "online")
	newNode, err := nodes.Get(ctx, issued.NodeID)
	if err != nil || newNode.EngineID == "" || !newNode.Enabled {
		t.Fatalf("new Agent node did not activate: %+v, %v", newNode, err)
	}
	if err := nodes.Delete(ctx, issued.NodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := nodes.Test(ctx, "local"); err != nil {
		t.Fatal(err)
	}
	before, err := nodes.Get(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	issued, err = nodes.IssueAgentEnrollment(ctx, node.AgentEnrollmentInput{NodeID: "local", Name: "Local"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(environmentFile, []byte("SUMA_AGENT_TOKEN="+issued.Token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	docker("rm", "-f", name)
	startAgent()
	await("local", "online")
	after, err := nodes.Get(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if after.ID != before.ID || after.EngineID != before.EngineID || after.ConnectionType != node.ConnectionAgent {
		t.Fatalf("in-place migration changed identity: before=%+v after=%+v", before, after)
	}
	if _, err := authService.Initialize(ctx, "smoke-admin", "smoke@example.test", "", "TestAgentSmoke123!"); err != nil {
		t.Fatal(err)
	}
	sessionToken, _, err := authService.Login(ctx, "smoke-admin", "TestAgentSmoke123!", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/docker/info", "/api/v1/containers"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionToken})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != 200 {
			t.Fatalf("legacy local route %s failed after Agent migration: %d %s", path, response.Code, response.Body.String())
		}
	}
	adapter, err := nodes.Runtime(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	info, err := adapter.Info(ctx)
	if err != nil || info.ID != before.EngineID {
		t.Fatalf("Agent Docker info: %+v, %v", info, err)
	}
	pullCtx, stopPull := context.WithTimeout(ctx, 45*time.Second)
	pull, err := adapter.PullImage(pullCtx, "alpine:3.24")
	if err != nil {
		stopPull()
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, pull); err != nil {
		_ = pull.Close()
		stopPull()
		t.Fatal(err)
	}
	_ = pull.Close()
	stopPull()
	proxyEndpoint, err := hub.Endpoint("local")
	if err != nil {
		t.Fatal(err)
	}
	largeClient, err := client.NewClientWithOpts(client.WithHost(proxyEndpoint), client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatal(err)
	}
	defer largeClient.Close()
	largeContainer, err := largeClient.ContainerCreate(ctx, &dockercontainer.Config{Image: "alpine:3.24", Env: []string{"AGENT_LARGE_BODY=" + strings.Repeat("x", 512<<10)}}, nil, nil, nil, fmt.Sprintf("suma-agent-large-%d", time.Now().UnixNano()))
	if err != nil {
		t.Fatalf("large Docker request through Agent: %v", err)
	}
	defer adapter.ForceRemove(context.Background(), largeContainer.ID, false)
	if err := adapter.ForceRemove(ctx, largeContainer.ID, false); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(root, "agentsmoke")
	if err := os.Mkdir(project, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "compose.yml"), []byte("services:\n  test:\n    image: alpine:3.24\n    command: [\"/bin/sh\", \"-c\", \"echo agent-stream-ready; sleep 300\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := compose.NewRunner("docker compose")
	if err != nil {
		t.Fatal(err)
	}
	target, _, err := nodes.ComposeTarget(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	runner := base.ForTarget(target)
	if err := runner.Up(ctx, project, io.Discard); err != nil {
		t.Fatal(err)
	}
	defer runner.Down(context.Background(), project, io.Discard)
	containers, err := adapter.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	containerID := ""
	for _, item := range containers {
		if item.Labels["com.docker.compose.project"] == "agentsmoke" && item.Labels["com.docker.compose.service"] == "test" {
			containerID = item.ID
			break
		}
	}
	if containerID == "" {
		t.Fatal("Compose container missing over Agent connection")
	}
	streamCtx, stopStreams := context.WithTimeout(ctx, 15*time.Second)
	defer stopStreams()
	logs, err := adapter.Logs(streamCtx, containerID, "", "10")
	if err != nil {
		t.Fatal(err)
	}
	readAgentSmokeStream(t, logs, "agent-stream-ready")
	stats, err := adapter.Stats(streamCtx, containerID)
	if err != nil {
		t.Fatal(err)
	}
	readAgentSmokeStream(t, stats, "cpu_stats")
	terminal, err := adapter.Terminal(streamCtx, containerID, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := terminal.Write([]byte("echo agent-terminal-ready\n")); err != nil {
		_ = terminal.Close()
		t.Fatal(err)
	}
	readAgentSmokeStream(t, terminal, "agent-terminal-ready")
	stopStreams()
	if err := compose.ValidateComposeBindMounts("services:\n  test:\n    volumes:\n      - ./relative:/data\n", after.ConnectionType != node.ConnectionUnix, false); err == nil {
		t.Fatal("Agent accepted a relative remote bind source")
	}
	baseProjects, err := compose.NewService(db, filepath.Join(root, "managed"), runner, task.NewService(db), adapter)
	if err != nil {
		t.Fatal(err)
	}
	agentProjects := baseProjects.ForNode("local", "Local", runner, adapter, false)
	draft, err := agentProjects.BuildTakeoverDraft(ctx, "agentsmoke")
	if err != nil || draft.Source != "runtime" {
		t.Fatalf("Agent takeover did not use runtime fallback: %+v, %v", draft, err)
	}
	managed, err := agentProjects.Takeover(ctx, "agentsmoke", compose.TakeoverInput{Fingerprint: draft.Fingerprint, ConfirmationName: "agentsmoke", Compose: draft.Compose, Environment: draft.Environment})
	if err != nil || !managed.Managed || managed.Metadata == nil || managed.Metadata.TakeoverSource != "runtime" {
		t.Fatalf("Agent takeover failed: %+v, %v", managed, err)
	}
	if err := runner.Down(ctx, project, io.Discard); err != nil {
		t.Fatal(err)
	}
	// CD uses the same target resolver and ComposeRunner as every other node.
	firstTree := filepath.Join(root, "release-one")
	secondTree := filepath.Join(root, "release-two")
	thirdTree := filepath.Join(root, "release-three")
	for _, item := range []struct{ path, label string }{{firstTree, "one"}, {secondTree, "two"}, {thirdTree, "three"}} {
		if err := os.Mkdir(item.path, 0o750); err != nil {
			t.Fatal(err)
		}
		content := fmt.Sprintf("services:\n  test:\n    image: alpine:3.24\n    command: [\"sleep\", \"300\"]\n    labels:\n      io.suma.smoke.release: %q\n", item.label)
		if err := os.WriteFile(filepath.Join(item.path, "compose.yml"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gitClient := &agentSmokeGit{revision: gitrepo.Revision{CommitSHA: strings.Repeat("a", 40), CommitAuthor: "Agent smoke", CommitMessage: "one", WorktreePath: firstTree}}
	delivery := cd.NewService(db, gitClient, gitrepo.NewCredentialService(db, store), base, task.NewService(db), audit.NewService(db), store)
	delivery.SetTargetResolver(nodes)
	cdName := fmt.Sprintf("agent-smoke-%d", time.Now().UnixNano())
	if _, err := delivery.CreateProject(ctx, cdName); err != nil {
		t.Fatal(err)
	}
	var cdProject database.DeliveryProject
	if err := db.Where("name = ?", cdName).First(&cdProject).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&cdProject).Updates(map[string]any{"git_clone_url": "https://git.example.test/deploy.git", "git_ref_type": gitrepo.RefBranch, "git_ref": "main", "compose_files_json": `["compose.yml"]`, "reconcile_mode": cd.ModeAuto, "deployment_timeout": 60}).Error; err != nil {
		t.Fatal(err)
	}
	cleanupSpec := compose.ExecutionSpec{ProjectName: cdProject.DeploymentName, ProjectDir: firstTree, Files: []string{filepath.Join(firstTree, "compose.yml")}}
	defer func() {
		if cleanupSpec.ProjectName == "" {
			return
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		_ = base.Targeted(target).DownRelease(cleanupCtx, cleanupSpec, io.Discard)
	}()
	waitTask := func(id, want string) {
		t.Helper()
		for {
			if ctx.Err() != nil {
				t.Fatalf("CD task timed out: %v", ctx.Err())
			}
			var row database.Task
			if err := db.Where("id = ?", id).First(&row).Error; err != nil {
				if strings.Contains(err.Error(), "SQLITE_BUSY") || strings.Contains(err.Error(), "database is locked") {
					time.Sleep(100 * time.Millisecond)
					continue
				}
				t.Fatal(err)
			}
			if row.Status == want {
				return
			}
			if row.Status == task.StatusFailed || row.Status == task.StatusCanceled {
				var logs []database.TaskLog
				_ = db.Where("task_id = ?", id).Find(&logs).Error
				t.Fatalf("CD task failed: %s, %s, %+v", row.Status, row.Message, logs)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	firstTask, err := delivery.Sync(ctx, cdName, "agent_smoke", cd.Actor{Name: "Agent smoke"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(firstTask.ID, task.StatusSuccess)
	firstReleases, err := delivery.ListReleases(ctx, cdName)
	if err != nil || len(firstReleases) != 1 || firstReleases[0].Status != cd.StatusSucceeded {
		t.Fatalf("first CD release: %+v, %v", firstReleases, err)
	}
	gitClient.set(gitrepo.Revision{CommitSHA: strings.Repeat("b", 40), CommitAuthor: "Agent smoke", CommitMessage: "two", WorktreePath: secondTree})
	secondTask, err := delivery.Sync(ctx, cdName, "agent_smoke", cd.Actor{Name: "Agent smoke"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(secondTask.ID, task.StatusSuccess)
	if err := db.Model(&database.DeliveryProject{}).Where("id = ?", cdProject.ID).Update("reconcile_mode", cd.ModeManual).Error; err != nil {
		t.Fatal(err)
	}
	gitClient.set(gitrepo.Revision{CommitSHA: strings.Repeat("c", 40), CommitAuthor: "Agent smoke", CommitMessage: "three", WorktreePath: thirdTree})
	thirdTask, err := delivery.Sync(ctx, cdName, "agent_smoke", cd.Actor{Name: "Agent smoke"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(thirdTask.ID, task.StatusSuccess)
	thirdReleases, err := delivery.ListReleases(ctx, cdName)
	if err != nil || len(thirdReleases) < 3 {
		t.Fatalf("third CD release: %+v, %v", thirdReleases, err)
	}
	third := thirdReleases[0]
	adminID := uint(1)
	if _, err := delivery.Approve(ctx, cdName, third.ID, cd.Actor{UserID: &adminID, Name: "Agent smoke"}); err != nil {
		t.Fatal(err)
	}
	docker("stop", name)
	await("local", "offline")
	failedTask, err := delivery.Deploy(ctx, cdName, third.ID, cd.Actor{Name: "Agent smoke"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(failedTask.ID, task.StatusFailed)
	docker("start", name)
	await("local", "online")
	retry, err := delivery.RetryFailedNodes(ctx, cdName, third.ID, cd.Actor{Name: "Agent smoke"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(retry.Task.ID, task.StatusSuccess)
	rollback, err := delivery.Rollback(ctx, cdName, firstReleases[0].ID, cd.Actor{Name: "Agent smoke"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(rollback.ID, task.StatusSuccess)
	rolledBack, err := delivery.ListReleases(ctx, cdName)
	if err != nil || len(rolledBack) < 3 || rolledBack[0].Status != cd.StatusRolledBack {
		t.Fatalf("Agent CD rollback: %+v, %v", rolledBack, err)
	}
	if err := base.Targeted(target).DownRelease(ctx, cleanupSpec, io.Discard); err != nil {
		t.Fatal(err)
	}
	cleanupSpec.ProjectName = ""
	docker("stop", name)
	await("local", "offline")
	docker("start", name)
	await("local", "online")
	if err := nodes.RevokeAgent(ctx, "local"); err != nil {
		t.Fatal(err)
	}
	await("local", "offline")
	newEnrollment, err := nodes.IssueAgentEnrollment(ctx, node.AgentEnrollmentInput{NodeID: "local", Name: "Local"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(environmentFile, []byte("SUMA_AGENT_TOKEN="+newEnrollment.Token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	docker("rm", "-f", name)
	startAgent()
	await("local", "online")
	direct, err := nodes.Update(ctx, "local", node.Input{Name: "Local", ConnectionType: node.ConnectionUnix, Endpoint: "unix:///var/run/docker.sock", TLSMode: node.TLSDisabled, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if direct.ConnectionType != node.ConnectionUnix || direct.EngineID != before.EngineID || direct.Status != "online" || direct.AgentVersion != "" || direct.AgentConnectedAt != nil || direct.AgentEnrollment != nil {
		t.Fatalf("switching Agent back to Unix changed the node: %+v", direct)
	}
	var revoked database.AgentCredential
	if err := db.Where("node_id = ?", "local").First(&revoked).Error; err != nil || revoked.RevokedAt == nil {
		t.Fatalf("switching back to Unix did not revoke Agent credentials: %+v, %v", revoked, err)
	}
}

func readAgentSmokeStream(t *testing.T, stream io.ReadCloser, marker string) {
	t.Helper()
	defer stream.Close()
	result := make(chan error, 1)
	go func() {
		buffer := make([]byte, 4096)
		var seen strings.Builder
		for seen.Len() < 1<<20 {
			n, err := stream.Read(buffer)
			seen.Write(buffer[:n])
			if strings.Contains(seen.String(), marker) {
				result <- nil
				return
			}
			if err != nil {
				result <- fmt.Errorf("waiting for %q: %w; output: %s", marker, err, seen.String())
				return
			}
		}
		result <- fmt.Errorf("stream exceeded 1 MiB before %q", marker)
	}()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("timed out waiting for %q over Agent stream", marker)
	}
}

type agentSmokeGit struct {
	mu       sync.Mutex
	revision gitrepo.Revision
}

func (g *agentSmokeGit) Sync(context.Context, gitrepo.SyncRequest, io.Writer) (gitrepo.Revision, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.revision, nil
}
func (g *agentSmokeGit) Verify(context.Context, string, string) error { return nil }
func (g *agentSmokeGit) Cleanup(uint) error                           { return nil }
func (g *agentSmokeGit) set(revision gitrepo.Revision) {
	g.mu.Lock()
	g.revision = revision
	g.mu.Unlock()
}

func smokeCertificate(t *testing.T, root string) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "host.docker.internal"}, DNSNames: []string{"host.docker.internal"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	encodedKey, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certFile, keyFile := filepath.Join(root, "ca.pem"), filepath.Join(root, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: encodedKey}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}
