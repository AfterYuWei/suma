//go:build dockersmoke

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dockercontainer "github.com/docker/docker/api/types/container"
	dockerimage "github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	dockernetwork "github.com/docker/docker/api/types/network"
	dockervolume "github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/gin-gonic/gin"
	"github.com/suma/suma/server/internal/agenthub"
	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/auth"
	"github.com/suma/suma/server/internal/cleanup"
	"github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/node"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/task"
)

// This suite refuses the normal host Engine. All resources and cache operations
// run in the explicitly provisioned disposable Docker-in-Docker daemon.
func TestRealDockerCleanupTransports(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if os.Getenv("SUMA_RUN_CLEANUP_SMOKE") != "1" {
		t.Skip("set SUMA_RUN_CLEANUP_SMOKE=1 with isolated daemon endpoints")
	}
	unixHost := os.Getenv("SUMA_CLEANUP_SMOKE_UNIX")
	if !strings.HasPrefix(os.Getenv("SUMA_CLEANUP_SMOKE_DIND"), "suma-cleanup-isolated-") || !strings.HasPrefix(unixHost, "unix:///tmp/suma-cleanup-live.") {
		t.Fatal("isolated daemon is required")
	}
	for _, transport := range []string{"unix", "tcp", "agent"} {
		t.Run(transport, func(t *testing.T) { cleanupTransportSmoke(t, transport, unixHost) })
	}
}
func cleanupTransportSmoke(t *testing.T, transport, unixHost string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cli, err := client.NewClientWithOpts(client.WithHost(unixHost), client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	marker := fmt.Sprintf("cleanup-%s-%d", transport, time.Now().UnixNano())
	volume := func(suffix string) string {
		row, e := cli.VolumeCreate(ctx, dockervolume.CreateOptions{Name: marker + suffix})
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = cli.VolumeRemove(context.Background(), row.Name, true) })
		return row.Name
	}
	network := func(suffix string) string {
		row, e := cli.NetworkCreate(ctx, marker+suffix, dockernetwork.CreateOptions{Driver: "bridge"})
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = cli.NetworkRemove(context.Background(), row.ID) })
		return row.ID
	}
	unusedVolume := volume("-unused")
	usedVolume := volume("-used")
	protectedVolume := volume("-protected")
	unusedNetwork := network("-unused")
	raceNetwork := network("-race")
	usedNetwork := network("-used")
	createContainer := func(suffix string, labels map[string]string, running bool, volumeName, networkID string) string {
		config := &dockercontainer.Config{Image: "alpine:3.24", Cmd: []string{"true"}, Labels: labels}
		if running {
			config.Cmd = []string{"sleep", "300"}
		}
		host := &dockercontainer.HostConfig{}
		if volumeName != "" {
			host.Mounts = []mount.Mount{{Type: mount.TypeVolume, Source: volumeName, Target: "/data"}}
		}
		networking := &dockernetwork.NetworkingConfig{}
		if networkID != "" {
			networking.EndpointsConfig = map[string]*dockernetwork.EndpointSettings{networkID: {NetworkID: networkID}}
		}
		row, e := cli.ContainerCreate(ctx, config, host, networking, nil, marker+suffix)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() {
			_ = cli.ContainerRemove(context.Background(), row.ID, dockercontainer.RemoveOptions{Force: true})
		})
		if e = cli.ContainerStart(ctx, row.ID, dockercontainer.StartOptions{}); e != nil {
			t.Fatal(e)
		}
		if !running {
			done, failed := cli.ContainerWait(ctx, row.ID, dockercontainer.WaitConditionNotRunning)
			select {
			case <-done:
			case e := <-failed:
				t.Fatal(e)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
		return row.ID
	}
	stopped := createContainer("-stopped", nil, false, usedVolume, usedNetwork)
	protectedContainer := createContainer("-compose", map[string]string{"com.docker.compose.project": "external"}, false, "", "")
	running := createContainer("-running", nil, true, "", "")
	commit := func(repository string) string {
		row, e := cli.ContainerCommit(ctx, stopped, dockercontainer.CommitOptions{Reference: repository})
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() {
			_, _ = cli.ImageRemove(context.Background(), row.ID, dockerimage.RemoveOptions{Force: true, PruneChildren: false})
		})
		return row.ID
	}
	dangling := commit("")
	tagged := commit(marker + ":retained")
	root := t.TempDir()
	db, err := database.Open(filepath.Join(root, "suma.db"))
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := secret.Open(filepath.Join(root, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := node.NewService(db, secrets, unixHost)
	if err != nil {
		t.Fatal(err)
	}
	defer nodes.Close()
	nodeID := "local"
	if transport == "tcp" {
		certDir := os.Getenv("SUMA_CLEANUP_SMOKE_CERTS")
		read := func(name string) string {
			v, e := os.ReadFile(filepath.Join(certDir, name))
			if e != nil {
				t.Fatal(e)
			}
			return string(v)
		}
		credential, e := nodes.CreateTLSCredential(ctx, node.TLSCredentialInput{Name: "isolated", CA: read("ca.pem"), Certificate: read("cert.pem"), PrivateKey: read("key.pem")})
		if e != nil {
			t.Fatal(e)
		}
		n, e := nodes.Create(ctx, node.Input{Name: "Isolated mTLS", ConnectionType: node.ConnectionTCP, Endpoint: os.Getenv("SUMA_CLEANUP_SMOKE_TCP"), TLSMode: node.TLSRequired, TLSCredentialID: &credential.ID, Enabled: true})
		if e != nil {
			t.Fatal(e)
		}
		nodeID = n.ID
	}
	if transport == "agent" {
		hub, e := agenthub.New()
		if e != nil {
			t.Fatal(e)
		}
		defer hub.Close()
		nodes.SetAgentHub(hub)
		if _, e = nodes.Test(ctx, "local"); e != nil {
			t.Fatal(e)
		}
		issued, e := nodes.IssueAgentEnrollment(ctx, node.AgentEnrollmentInput{NodeID: "local", Name: "Local"})
		if e != nil {
			t.Fatal(e)
		}
		cert, key := smokeCertificate(t, root)
		listener, e := net.Listen("tcp", "0.0.0.0:0")
		if e != nil {
			t.Fatal(e)
		}
		publicURL := fmt.Sprintf("https://host.docker.internal:%d", listener.Addr().(*net.TCPAddr).Port)
		authService := auth.NewService(db, time.Hour)
		server := &http.Server{Handler: NewRouter(Dependencies{Engine: &fakeEngine{}, Nodes: nodes, Agents: hub, Auth: authService, Audit: audit.NewService(db), AgentPublicURL: publicURL})}
		go func() { _ = server.ServeTLS(listener, cert, key) }()
		defer server.Shutdown(context.Background())
		agentName := marker + "-agent"
		image := os.Getenv("SUMA_AGENT_SMOKE_IMAGE")
		if image == "" {
			image = "suma-agent:env-only-smoke"
		}
		t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", "-v", agentName).Run() })
		envFile := filepath.Join(root, "agent.env")
		if e = os.WriteFile(envFile, []byte("SUMA_AGENT_TOKEN="+issued.Token+"\n"), 0600); e != nil {
			t.Fatal(e)
		}
		socket := strings.TrimPrefix(unixHost, "unix://")
		command := exec.CommandContext(ctx, "docker", "run", "-d", "--name", agentName, "--add-host", "host.docker.internal:host-gateway", "--env-file", envFile, "-e", "SUMA_AGENT_SERVER_URL="+publicURL, "-e", "SUMA_AGENT_CA_FILE=/run/secrets/ca.pem", "-v", socket+":/var/run/docker.sock:ro", "-v", cert+":/run/secrets/ca.pem:ro", image)
		if output, e := command.CombinedOutput(); e != nil {
			t.Fatalf("start isolated Agent: %v %s", e, output)
		}
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			n, e := nodes.Get(ctx, "local")
			if e == nil && n.ConnectionType == node.ConnectionAgent && n.Status == "online" {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		n, e := nodes.Get(ctx, "local")
		if e != nil || n.ConnectionType != node.ConnectionAgent || n.Status != "online" {
			t.Fatalf("Agent did not connect: %+v %v", n, e)
		}
	}
	adapter, err := nodes.Runtime(ctx, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	// A fresh, unshared cache fixture verifies Engine-reported reclamation over
	// each transport. Production policies retain at least one day; zero days
	// is used only here to make the disposable cache immediately eligible.
	buildTag := marker + ":cache-fixture"
	script := "set -eu; dir=$(mktemp -d /tmp/cleanup-cache.XXXXXX); trap 'rm -rf \"$dir\"' EXIT; printf 'FROM alpine:3.24\\nRUN dd if=/dev/zero of=/cache-payload bs=1048576 count=2\\n' > \"$dir/Dockerfile\"; DOCKER_BUILDKIT=1 docker build --no-cache --network=none -t \"$1\" \"$dir\""
	build := exec.CommandContext(ctx, "docker", "exec", os.Getenv("SUMA_CLEANUP_SMOKE_DIND"), "sh", "-c", script, "sh", buildTag)
	if output, e := build.CombinedOutput(); e != nil {
		t.Fatalf("isolated cache fixture: %v %s", e, output)
	}
	if _, err = cli.ImageRemove(ctx, buildTag, dockerimage.RemoveOptions{Force: false, PruneChildren: false}); err != nil {
		t.Fatal(err)
	}
	// Engine's duration filter is resolved to whole seconds; let the just-used
	// cache cross that boundary before checking reclamation.
	select {
	case <-time.After(2 * time.Second):
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	cacheReport, err := adapter.CleanupPruneCache(ctx, cleanup.CacheOptions{RetentionDays: 0, ReservedBytes: 0})
	if err != nil || cacheReport.ReclaimedBytes == 0 {
		t.Fatalf("real cache reclamation = %+v %v", cacheReport, err)
	}
	t.Logf("%s: Engine reported %d reclaimed cache bytes", transport, cacheReport.ReclaimedBytes)
	inv, err := adapter.CleanupInventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !inv.Capabilities.BuildCache {
		t.Fatal("missing cache capability")
	}
	tasks := task.NewService(db)
	audits := audit.NewService(db)
	runner, err := compose.NewRunner("docker compose")
	if err != nil {
		t.Fatal(err)
	}
	target, n, err := nodes.ComposeTarget(ctx, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	projects, err := compose.NewService(db, filepath.Join(root, "projects"), runner, tasks, adapter)
	if err != nil {
		t.Fatal(err)
	}
	projects = projects.ForNode(nodeID, n.Name, runner.ForTarget(target), adapter, transport == "unix")
	content := fmt.Sprintf("services:\n  reserved:\n    image: %s\n    volumes:\n      - data:/data\nvolumes:\n  data:\n    name: %s\n    external: true\n", marker+":retained", protectedVolume)
	if _, err = projects.Create(ctx, "reserved", content, ""); err != nil {
		t.Fatal(err)
	}
	clock := time.Now().Add(8 * 24 * time.Hour)
	service := cleanup.NewService(db, tasks, audits, cleanup.Dependencies{Now: func() time.Time { return clock }, Node: func(ctx context.Context, id string) (cleanup.Node, error) {
		view, e := nodes.Get(ctx, id)
		return cleanup.Node{ID: view.ID, Name: view.Name, Enabled: view.Enabled}, e
	}, Runtime: func(ctx context.Context, id string) (cleanup.Runtime, error) { return nodes.Runtime(ctx, id) }, Protection: func(ctx context.Context, id string) (cleanup.Protection, error) {
		refs, e := projects.CleanupResourceReferences(ctx)
		return cleanup.Protection{cleanup.Image: refs.Images, cleanup.Volume: refs.Volumes}, e
	}})
	defer service.Stop()
	authentication := auth.NewService(db, time.Hour)
	if _, err = authentication.Initialize(ctx, "admin", "admin@example.test", "", "long-password"); err != nil {
		t.Fatal(err)
	}
	token, _, err := authentication.Login(ctx, "admin", "long-password", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(Dependencies{Engine: &fakeEngine{}, Nodes: nodes, Auth: authentication, Tasks: tasks, Audit: audits, Cleanup: service})
	request := func(method, path string, input any) *httptest.ResponseRecorder {
		var body string
		if input != nil {
			v, _ := json.Marshal(input)
			body = string(v)
		}
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Origin", "http://example.com")
		if input != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	path := "/api/v1/nodes/" + nodeID
	config := cleanup.DefaultConfig("UTC")
	config.Containers.Enabled = true
	config.Networks.Enabled = true
	out := request("PUT", path+"/cleanup/policy", cleanup.Update{Config: config})
	if out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
	out = request("POST", path+"/cleanup/preview", map[string]any{})
	if out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
	var payload struct {
		Data cleanup.Preview `json:"data"`
	}
	if err = json.Unmarshal(out.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	candidates := map[string]bool{}
	reasons := map[string]string{}
	for _, r := range payload.Data.Resources {
		candidates[r.ID] = r.Candidate
		reasons[r.ID] = r.Reason
	}
	if !candidates[stopped] || !candidates[dangling] || !candidates[unusedNetwork] || candidates[protectedContainer] || candidates[running] || candidates[tagged] || reasons[protectedVolume] != "protected_reference" {
		t.Fatalf("unexpected protection/candidates: %v %v", candidates, reasons)
	}
	lateImage := commit(marker + ":late")
	if err = cli.NetworkConnect(ctx, raceNetwork, running, nil); err != nil {
		t.Fatal(err)
	}
	if out = request("DELETE", path+"/volumes/"+unusedVolume+"?confirm=wrong", nil); out.Code != 400 {
		t.Fatal("volume confirmation bypassed", out.Code)
	}
	if out = request("DELETE", path+"/volumes/"+protectedVolume+"?confirm="+protectedVolume, nil); out.Code != 409 {
		t.Fatal("declarative volume protection bypassed", out.Code, out.Body.String())
	}
	out = request("POST", path+"/cleanup/run", map[string]any{"preview_id": payload.Data.ID, "confirmation_name": n.Name})
	if out.Code != 202 {
		t.Fatal(out.Code, out.Body.String())
	}
	var started struct {
		Data database.Task `json:"data"`
	}
	if err = json.Unmarshal(out.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(60 * time.Second)
	var run database.CleanupRun
	for time.Now().Before(deadline) {
		db.Where("task_id = ?", started.Data.ID).First(&run)
		if run.Status == "success" || run.Status == "failed" || run.Status == "partial_failed" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if run.Status != "success" {
		t.Fatalf("cleanup failed: %+v", run)
	}
	if _, err = cli.ContainerInspect(ctx, stopped); err == nil {
		t.Fatal("stopped fixture retained")
	}
	if _, err = cli.ImageInspect(ctx, dangling); err == nil {
		t.Fatal("dangling fixture retained")
	}
	if _, err = cli.NetworkInspect(ctx, unusedNetwork, dockernetwork.InspectOptions{}); err == nil {
		t.Fatal("unused network retained")
	}
	for _, id := range []string{protectedContainer, running} {
		if _, err = cli.ContainerInspect(ctx, id); err != nil {
			t.Fatal("protected container removed", err)
		}
	}
	for _, id := range []string{tagged, lateImage} {
		if _, err = cli.ImageInspect(ctx, id); err != nil {
			t.Fatal("protected/non-preview image removed", err)
		}
	}
	for _, id := range []string{raceNetwork, usedNetwork} {
		if _, err = cli.NetworkInspect(ctx, id, dockernetwork.InspectOptions{}); err != nil {
			t.Fatal("newly referenced/non-preview network removed", err)
		}
	}
	for _, name := range []string{unusedVolume, usedVolume, protectedVolume} {
		if _, err = cli.VolumeInspect(ctx, name); err != nil {
			t.Fatal("volume automatically deleted", err)
		}
	}
	if out = request("DELETE", path+"/volumes/"+unusedVolume+"?confirm="+unusedVolume, nil); out.Code != 200 {
		t.Fatal("confirmed volume removal failed", out.Code, out.Body.String())
	}
	if _, err = cli.VolumeInspect(ctx, unusedVolume); err == nil {
		t.Fatal("confirmed volume still exists")
	}
	t.Logf("%s: isolated cleanup, cache API, stopped/active references, declarative protection, frozen candidates, manual volume deletion, tasks and audits passed", transport)
}
