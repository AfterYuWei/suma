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
	"strconv"
	"strings"
	"sync"
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
	"github.com/suma/suma/server/internal/taskcenter"
	"gorm.io/gorm"
)

// Fixture commands exclusively target the disposable Engine. Every production
// read/pull below goes through the selected Unix, mTLS TCP or WSS runtime.
func operationsTransportSmoke(t *testing.T, ctx context.Context, root, unixHost, nodeID, transport string, db *gorm.DB, nodes *node.Service, adapter *docker.Adapter, tasks *task.Service, authentication *auth.Service, current *compose.Service, cookie *http.Cookie) {
	t.Helper()
	if os.Getenv("SUMA_RUN_OPERATIONS_SMOKE") != "1" {
		return
	}
	gateway := smokeHostGateway(t, ctx)
	registryTLS := filepath.Join(root, "registry-tls")
	if err := os.MkdirAll(registryTLS, 0700); err != nil {
		t.Fatal(err)
	}
	// Keep the Agent's already-mounted CA unchanged if its WSS link reconnects.
	cert, key := smokeCertificate(t, registryTLS, gateway)
	pair, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	var gateMu sync.Mutex
	blockedConfig := ""
	pullEntered := make(chan struct{}, 1)
	releasePull := make(chan struct{})
	var releaseOnce sync.Once
	unblockPull := func() { releaseOnce.Do(func() { close(releasePull) }) }
	registryHandler := registryfixture.New(registryfixture.Logger(log.New(io.Discard, "", 0)))
	reg := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gateMu.Lock()
		blocked := blockedConfig
		gateMu.Unlock()
		// Metadata checks use SUMA's verified HTTPS adapter. Hold only the real
		// Engine's config download so cancellation occurs inside the pull child.
		if blocked != "" && r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/blobs/"+blocked) && !strings.Contains(r.UserAgent(), "SUMA image-update") {
			select {
			case pullEntered <- struct{}{}:
			default:
			}
			select {
			case <-releasePull:
			case <-r.Context().Done():
				return
			}
		}
		registryHandler.ServeHTTP(w, r)
	}))
	reg.Listener.Close()
	reg.Listener, err = net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	reg.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	reg.StartTLS()
	defer reg.Close()
	defer unblockPull()
	host := net.JoinHostPort(gateway, strconv.Itoa(reg.Listener.Addr().(*net.TCPAddr).Port))
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
	publish := func(version string) imageupdate.Remote {
		config, err := base.ConfigFile()
		if err != nil {
			t.Fatal(err)
		}
		config = config.DeepCopy()
		config.Config.Labels = map[string]string{"suma.smoke.version": version, "suma.smoke.fixture": ref.Name()}
		img, err := mutate.ConfigFile(base, config)
		if err != nil {
			t.Fatal(err)
		}
		if err := remote.Write(ref, img, remote.WithContext(ctx), remote.WithTransport(trusted)); err != nil {
			t.Fatal(err)
		}
		digest, err := img.Digest()
		if err != nil {
			t.Fatal(err)
		}
		manifest, err := img.Manifest()
		if err != nil {
			t.Fatal(err)
		}
		return imageupdate.Remote{ManifestDigest: digest.String(), ConfigDigest: manifest.Config.Digest.String()}
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
	logs := projectlogs.NewService(projectlogs.Dependencies{Runtime: func(ctx context.Context, id string) (projectlogs.Runtime, error) { return nodes.Runtime(ctx, id) }, Exists: func(ctx context.Context, id, n string) error { _, err := current.Get(ctx, n); return err }})
	logs.ReconcileInterval = 100 * time.Millisecond
	defer logs.Stop()
	handler := NewRouter(Dependencies{Engine: &fakeEngine{}, Nodes: nodes, Auth: authentication, Audit: audit.NewService(db), Tasks: tasks, ProjectLogs: logs})
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
	web := start("web", "1", false, false, false)
	start("web", "2", false, false, false)
	start("api", "1", false, false, true)
	start("tty", "1", true, false, false)
	start("job", "1", false, true, true)
	publish("two")
	pull()
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

	// Exercise the unified image kind against the same real Registry and node
	// transport. A SUMA-generated digest override must continue tracking latest.
	managed := project + "-managed"
	content := fmt.Sprintf("services:\n  app:\n    image: %s\n    command: [sh, -c, 'exec sleep 300']\n", ref.Name())
	if _, err := current.Create(ctx, managed, content, ""); err != nil {
		t.Fatal(err)
	}
	kinds := taskcenter.NewRegistry()
	center := taskcenter.New(db, kinds, taskcenter.Dependencies{NodeName: func(ctx context.Context, id string) (string, error) {
		view, err := nodes.Get(ctx, id)
		return view.Name, err
	}})
	defer center.Stop()
	liveTaskID := ""
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if liveTaskID != "" {
			tasks.Cancel(liveTaskID)
			unblockPull()
			if err := tasks.Wait(cleanup, liveTaskID); err != nil {
				t.Errorf("stop isolated image Task: %v", err)
			}
		}
		if err := center.DeleteSchedules(cleanup, imageupdate.KindImageUpdate, nodeID, managed); err != nil {
			t.Errorf("archive isolated image rule: %v", err)
		}
		if err := current.ForceRemove(cleanup, managed, false); err != nil {
			t.Errorf("remove isolated image project: %v", err)
		}
	}()
	updates := imageupdate.NewService(db, tasks, imageupdate.Dependencies{
		Automation: center, Resolver: registry.Adapter{Transport: trusted},
		Node: func(ctx context.Context, id string) (imageupdate.Node, error) {
			view, err := nodes.Get(ctx, id)
			return imageupdate.Node{ID: view.ID, Name: view.Name, Enabled: view.Enabled}, err
		},
		Runtime: func(ctx context.Context, id string) (imageupdate.Runtime, error) { return nodes.Runtime(ctx, id) },
		Compose: func(context.Context, string) (*compose.Service, error) { return current, nil },
		ConfigRevision: func(_ context.Context, _ string, params imageupdate.Params) (string, error) {
			config, err := current.ManagedConfiguration(params.Project)
			return config.Revision, err
		},
	})
	if err := kinds.Register(updates.Kind()); err != nil {
		t.Fatal(err)
	}
	first := publish("one")
	pull()
	initial, err := current.Action(ctx, managed, "up")
	if err != nil {
		t.Fatal(err)
	}
	liveTaskID = initial.ID
	awaitTask(initial)
	before, err := current.Services(ctx, managed)
	if err != nil || len(before) != 1 || before[0].ImageID != first.ConfigDigest {
		t.Fatal("initial fixture did not use D1 on selected Engine", err)
	}
	params, err := json.Marshal(imageupdate.Params{Project: managed, AutoApply: true})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := center.Create(ctx, taskcenter.ScheduleInput{Name: managed, Kind: imageupdate.KindImageUpdate, NodeID: nodeID, Subject: managed, Params: params, Cron: "0 0 * * *", Timezone: "UTC"}, "Docker smoke")
	if err != nil {
		t.Fatal(err)
	}
	for index, version := range []string{"two", "three"} {
		expected := publish(version)
		var run taskcenter.RunView
		if index == 0 {
			admitted, err := center.RunNow(ctx, rule.ID)
			if err != nil {
				t.Fatal(err)
			}
			run.TaskCenterScheduleRun = admitted
		} else {
			if err := db.Model(&database.TaskCenterSchedule{}).Where("id = ?", rule.ID).Update("next_run_at", time.Now().Add(-time.Second)).Error; err != nil {
				t.Fatal(err)
			}
			if err := center.RunDue(ctx); err != nil {
				t.Fatal(err)
			}
			page, err := center.RunsPage(ctx, rule.ID, 1, 0)
			if err != nil || len(page.Items) != 1 {
				t.Fatal("missing periodic image trigger", err)
			}
			run = page.Items[0]
			if run.TriggeredBy != taskcenter.ReasonScheduled || run.ScheduledFor == nil {
				t.Fatal("image update did not pass the unique scanner")
			}
		}
		liveTaskID = run.TaskID
		if err := tasks.Wait(ctx, run.TaskID); err != nil {
			t.Fatal(err)
		}
		result, err := tasks.GetForNode(ctx, nodeID, run.TaskID)
		var summary imageupdate.Summary
		if err != nil || result.Status != task.StatusSuccess || json.Unmarshal(result.Result, &summary) != nil || len(summary.Updated) != 1 || summary.Updated[0] != "app" || len(summary.Services) != 1 || summary.Services[0].Reference != ref.Name() || summary.Services[0].RemoteManifestDigest != expected.ManifestDigest {
			t.Fatalf("continuous update %s failed: %+v %v", version, summary, err)
		}
		children, err := tasks.Children(ctx, run.TaskID)
		if err != nil || len(children) != 1 || children[0].ID != summary.UpdateTaskID || children[0].NodeID != nodeID || children[0].Status != task.StatusSuccess {
			t.Fatalf("image update lost child Task evidence: %+v %v", children, err)
		}
		after, err := current.Services(ctx, managed)
		if err != nil || len(after) != 1 || after[0].ID == before[0].ID || after[0].ImageID != expected.ConfigDigest {
			t.Fatal("image update did not recreate selected Engine container", version, err)
		}
		before = after
	}
	awaitCanceled := func(parentID, childID string) {
		t.Helper()
		if !tasks.Cancel(parentID) {
			t.Fatal("parent image Task was not cancelable during its child operation")
		}
		shutdown, done := context.WithTimeout(ctx, 15*time.Second)
		defer done()
		if err := tasks.Wait(shutdown, parentID); err != nil {
			t.Fatal("canceled parent did not wait for child cleanup", err)
		}
		parent, err := tasks.Get(ctx, parentID)
		if err != nil || parent.Status != task.StatusCanceled || parent.FinishedAt == nil || !json.Valid(parent.Result) {
			t.Fatalf("canceled parent lost its terminal state or partial result: %+v %v", parent, err)
		}
		child, err := tasks.Get(ctx, childID)
		if err != nil || child.Status != task.StatusCanceled || child.FinishedAt == nil || parent.FinishedAt.Before(*child.FinishedAt) {
			t.Fatalf("parent finished before canceled child cleanup: %+v %v", child, err)
		}
	}
	fourth := publish("four")
	gateMu.Lock()
	blockedConfig = fourth.ConfigDigest
	gateMu.Unlock()
	cancelRun, err := center.RunNow(ctx, rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	liveTaskID = cancelRun.TaskID
	select {
	case <-pullEntered:
	case <-time.After(20 * time.Second):
		t.Fatal("image update never entered the real Engine pull")
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	children, err := tasks.Children(ctx, cancelRun.TaskID)
	if err != nil || len(children) != 1 {
		t.Fatal("pull admission did not create its child Task", err)
	}
	awaitCanceled(cancelRun.TaskID, children[0].ID)
	unblockPull()
	after, err := current.Services(ctx, managed)
	if err != nil || len(after) != 1 || after[0].ID != before[0].ID || after[0].ImageID != before[0].ImageID {
		t.Fatal("canceled pull entered container recreation", err)
	}
	// Hold Compose's real --wait phase with an unhealthy replacement. Canceling
	// must reap the child before the parent finishes, retaining the changed Engine
	// state as partial evidence rather than claiming that cancellation rolls back.
	currentConfig, err := current.ManagedConfiguration(managed)
	if err != nil {
		t.Fatal(err)
	}
	waitingContent := content + "    healthcheck:\n      test: [CMD, 'false']\n      interval: 1s\n      start_period: 60s\n      retries: 120\n"
	if _, err := current.SaveWithRevision(ctx, managed, waitingContent, "", currentConfig.Revision); err != nil {
		t.Fatal(err)
	}
	fifth := publish("five")
	rebuild, err := center.RunNow(ctx, rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	liveTaskID = rebuild.TaskID
	deadline := time.Now().Add(20 * time.Second)
	replacementID := ""
	for time.Now().Before(deadline) {
		rows, err := current.Services(ctx, managed)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 1 && rows[0].ID != before[0].ID && rows[0].ImageID == fifth.ConfigDigest && rows[0].State == "running" {
			replacementID = rows[0].ID
			break
		}
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if replacementID == "" {
		t.Fatal("update did not enter the real Compose recreation/wait phase")
	}
	children, err = tasks.Children(ctx, rebuild.TaskID)
	if err != nil || len(children) != 1 {
		t.Fatal("recreation lost its child Task", err)
	}
	awaitCanceled(rebuild.TaskID, children[0].ID)
	after, err = current.Services(ctx, managed)
	if err != nil || len(after) != 1 || after[0].ID != replacementID || after[0].ImageID != fifth.ConfigDigest {
		t.Fatal("cancellation changed the already-recreated Engine resource", err)
	}
	t.Log("verified HTTPS Registry and Unix/mTLS TCP/WSS Agent, retained stdout/stderr/TTY, global tail, D1/D2/D3 updates, cancellation during pull and recreation, and parent/child cleanup ordering")
}
