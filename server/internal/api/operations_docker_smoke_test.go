//go:build dockersmoke

package api

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	registryfixture "github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/gorilla/websocket"
	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/auth"
	"github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/docker"
	"github.com/suma/suma/server/internal/imageupdate"
	"github.com/suma/suma/server/internal/node"
	"github.com/suma/suma/server/internal/projectlogs"
	"github.com/suma/suma/server/internal/registry"
	"github.com/suma/suma/server/internal/task"
	"gorm.io/gorm"
)

// Fixture commands exclusively target the disposable Engine. Every production
// read/pull below goes through the selected Unix, mTLS TCP or WSS runtime.
func operationsTransportSmoke(t *testing.T, ctx context.Context, root, unixHost, nodeID, transport string, db *gorm.DB, nodes *node.Service, adapter *docker.Adapter, tasks *task.Service, authentication *auth.Service, current *compose.Service, cookie *http.Cookie) {
	t.Helper()
	if os.Getenv("SUMA_RUN_OPERATIONS_SMOKE") != "1" {
		return
	}
	cert, key := smokeCertificate(t, root)
	pair, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	reg := httptest.NewUnstartedServer(registryfixture.New(registryfixture.Logger(log.New(io.Discard, "", 0))))
	reg.Listener.Close()
	reg.Listener, err = net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	reg.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	reg.StartTLS()
	defer reg.Close()
	host := fmt.Sprintf("172.17.0.1:%d", reg.Listener.Addr().(*net.TCPAddr).Port)
	pem, _ := os.ReadFile(cert)
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(pem)
	trusted := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	defer trusted.CloseIdleConnections()
	install := exec.CommandContext(ctx, "docker", "exec", "-i", os.Getenv("SUMA_PROJECT_SMOKE_DIND"), "sh", "-c", "mkdir -p /etc/docker/certs.d/"+host+" && cat > /etc/docker/certs.d/"+host+"/ca.crt")
	install.Stdin = strings.NewReader(string(pem))
	if output, err := install.CombinedOutput(); err != nil {
		t.Fatalf("install fixture CA: %v %s", err, output)
	}
	base, err := tarball.ImageFromPath(filepath.Join(filepath.Dir(filepath.Dir(strings.TrimPrefix(unixHost, "unix://"))), "alpine.tar"), nil)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := name.NewTag(host + "/suma/" + transport + ":latest")
	if err != nil {
		t.Fatal(err)
	}
	publish := func(version string) {
		config, err := base.ConfigFile()
		if err != nil {
			t.Fatal(err)
		}
		config = config.DeepCopy()
		config.Config.Labels = map[string]string{"suma.smoke.version": version}
		img, err := mutate.ConfigFile(base, config)
		if err != nil {
			t.Fatal(err)
		}
		if err := remote.Write(ref, img, remote.WithContext(ctx), remote.WithTransport(trusted)); err != nil {
			t.Fatal(err)
		}
	}
	awaitTask := func(row database.Task) {
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			result, err := tasks.GetForNode(ctx, nodeID, row.ID)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status == "success" {
				return
			}
			if result.Status == "failed" || result.Status == "canceled" {
				t.Fatalf("fixture task %s: %s", result.Type, result.Message)
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("fixture task timeout")
	}
	updates := imageupdate.NewService(db, tasks, audit.NewService(db), imageupdate.Dependencies{Node: func(ctx context.Context, id string) (imageupdate.Node, error) {
		view, err := nodes.Get(ctx, id)
		return imageupdate.Node{ID: id, Name: view.Name, Enabled: view.Enabled, Available: true, RuntimeKey: transport}, err
	}, Runtime: func(ctx context.Context, id string) (imageupdate.Runtime, error) { return nodes.Runtime(ctx, id) }, Resolver: registry.Adapter{Transport: trusted}})
	defer updates.Stop()
	logs := projectlogs.NewService(projectlogs.Dependencies{Runtime: func(ctx context.Context, id string) (projectlogs.Runtime, error) { return nodes.Runtime(ctx, id) }, Exists: func(ctx context.Context, id, n string) error { _, err := current.Get(ctx, n); return err }})
	logs.ReconcileInterval = 100 * time.Millisecond
	defer logs.Stop()
	handler := NewRouter(Dependencies{Engine: &fakeEngine{}, Nodes: nodes, Auth: authentication, Audit: audit.NewService(db), Tasks: tasks, ImageUpdates: updates, ProjectLogs: logs})
	h := projectHTTPHarness{router: handler, cookie: cookie, db: db}
	prefix := "/api/v1/nodes/" + nodeID
	pull := func() {
		body, _ := json.Marshal(map[string]string{"reference": ref.Name()})
		response := h.request(http.MethodPost, prefix+"/images/pull", body)
		if response.Code != 202 {
			t.Fatalf("pull HTTP %d %s", response.Code, response.Body)
		}
		var data struct {
			Data database.Task `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		awaitTask(data.Data)
	}
	fixture := func(args ...string) string {
		command := exec.CommandContext(ctx, "docker", append([]string{"--host", unixHost}, args...)...)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("isolated fixture command: %v %s", err, output)
		}
		return strings.TrimSpace(string(output))
	}
	project := fmt.Sprintf("ops-%s-%d", transport, time.Now().UnixNano())
	ids := []string{}
	defer func() {
		for _, id := range ids {
			_ = exec.Command("docker", "--host", unixHost, "rm", "-f", "-v", id).Run()
		}
	}()
	start := func(service, number string, tty, oneOff, stopped bool) string {
		args := []string{"run", "-d", "--label", "com.docker.compose.project=" + project, "--label", "com.docker.compose.service=" + service, "--label", "com.docker.compose.container-number=" + number, "--name", project + "-" + service + "-" + number}
		if tty {
			args = append(args, "-t")
		}
		if oneOff {
			args = append(args, "--label", "com.docker.compose.oneoff=True")
		}
		script := "while true; do printf 'hello stdout\\n\\n'; echo hello-stderr >&2; sleep 1; done"
		if stopped {
			script = "echo retained-stopped; echo retained-stderr >&2"
		}
		args = append(args, ref.Name(), "sh", "-c", script)
		id := fixture(args...)
		ids = append(ids, id)
		return id
	}
	publish("one")
	pull()
	old, err := adapter.InspectImage(ctx, ref.Name())
	if err != nil {
		t.Fatal(err)
	}
	web := start("web", "1", false, false, false)
	start("web", "2", false, false, false)
	start("api", "1", false, false, true)
	start("tty", "1", true, false, false)
	start("job", "1", false, true, true)
	check := func(expected string, pullRequired bool) {
		body, _ := json.Marshal(map[string]string{"project_name": project})
		r := h.request(http.MethodPost, prefix+"/image-updates/check", body)
		if r.Code != 202 {
			t.Fatalf("check HTTP %d %s", r.Code, r.Body)
		}
		var taskData struct {
			Data database.Task `json:"data"`
		}
		_ = json.Unmarshal(r.Body.Bytes(), &taskData)
		awaitTask(taskData.Data)
		view, err := updates.View(ctx, nodeID, project)
		if err != nil || len(view.Results) != 1 || view.Results[0].Status != expected || view.Results[0].PullRequired != pullRequired {
			t.Fatalf("update result: %+v %v", view, err)
		}
	}
	check("current", false)
	publish("two")
	check("update_available", true)
	pull()
	newImage, err := adapter.InspectImage(ctx, ref.Name())
	if err != nil || old.ID == newImage.ID {
		t.Fatal("same tag did not change config ID")
	}
	view, err := updates.View(ctx, nodeID, project)
	if err != nil || view.Results[0].PullRequired || !view.Results[0].RecreateRequired {
		t.Fatalf("pending recreate: %+v %v", view, err)
	}
	time.Sleep(300 * time.Millisecond)
	history, err := logs.History(ctx, nodeID, project, projectlogs.Query{Tail: 200, Since: time.Now().Add(-time.Minute).Format(time.RFC3339Nano), Until: time.Now().Add(time.Second).Format(time.RFC3339Nano)})
	if err != nil || len(history.Sources) != 4 || len(history.Entries) == 0 || len(history.Errors) != 0 {
		t.Fatalf("retained multi-source logs: %+v %v", history, err)
	}
	streams := map[string]bool{}
	blank, stopped := false, false
	for _, r := range history.Entries {
		streams[r.Stream] = true
		blank = blank || r.Text == ""
		stopped = stopped || r.Text == "retained-stopped"
	}
	if !streams["stdout"] || !streams["stderr"] || !streams["tty"] || !blank || !stopped {
		t.Fatalf("stream fidelity: %v blank=%v stopped=%v", streams, blank, stopped)
	}
	one, err := logs.History(ctx, nodeID, project, projectlogs.Query{Tail: 1})
	if err != nil || len(one.Entries) != 1 || !one.Truncated {
		t.Fatal("aggregate tail limit failed")
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	header := http.Header{"Cookie": []string{cookie.String()}, "Origin": []string{server.URL}}
	ws, response, err := websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http://", "ws://", 1)+"/ws/nodes/"+nodeID+"/projects/compose/"+project+"/logs?services=web&tail=200", header)
	if err != nil {
		if response != nil {
			t.Fatal(response.Status)
		}
		t.Fatal(err)
	}
	defer ws.Close()
	ws.SetReadDeadline(time.Now().Add(10 * time.Second))
	var event projectlogs.Event
	if err := ws.ReadJSON(&event); err != nil || event.Type != "snapshot" {
		t.Fatalf("live snapshot: %v", err)
	}
	fixture("rm", "-f", "-v", web)
	replacement := start("web", "3", false, false, false)
	found := false
	for !found {
		if err := ws.ReadJSON(&event); err != nil {
			t.Fatal(err)
		}
		for _, r := range event.Entries {
			if r.ContainerID == replacement {
				found = true
			}
		}
	}
	_ = ws.Close()
	t.Log("verified HTTPS same-tag metadata, authenticated pull, pending rebuild, retained stdout/stderr/TTY, global tail, external-project WebSocket and rebuild following")
}
