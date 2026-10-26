package cd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/task"
)

type bindTargetResolver map[string]compose.Target

func (r bindTargetResolver) ResolveComposeTarget(_ context.Context, id string) (compose.Target, error) {
	target, ok := r[id]
	if !ok {
		return compose.Target{}, errors.New("unknown test target")
	}
	return target, nil
}

func TestServiceHostBindPolicyAcrossNodeTransports(t *testing.T) {
	tests := []struct {
		name, source, want string
		transports         []string
	}{
		{name: "implicit local", source: "/Data/Docker/DockerData/eizhu/application.yml"},
		{name: "unix", source: "/Data/Docker/DockerData/eizhu/application.yml", transports: []string{"unix"}},
		{name: "tcp", source: "/Data/Docker/DockerData/eizhu/application.yml", transports: []string{"tcp"}},
		{name: "agent", source: "/Data/Docker/DockerData/eizhu/application.yml", transports: []string{"agent"}},
		{name: "mixed", source: "/Data/Docker/DockerData/eizhu/application.yml", transports: []string{"unix", "tcp", "agent"}},
		{name: "unix relative", source: "./application.yml", transports: []string{"unix"}},
		{name: "tcp relative", source: "./application.yml", transports: []string{"tcp"}, want: "must be absolute"},
		{name: "agent relative", source: "./application.yml", transports: []string{"agent"}, want: "must be absolute"},
		{name: "tcp interpolation", source: "${CONFIG_FILE}", transports: []string{"tcp"}, want: "cannot be interpolated"},
		{name: "agent interpolation", source: "$CONFIG_FILE", transports: []string{"agent"}, want: "cannot be interpolated"},
		{name: "mixed relative", source: "./application.yml", transports: []string{"unix", "agent"}, want: "must be absolute"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newCDHarness(t, ModeManual)
			worktree := h.git.revision.WorktreePath
			// Simulate Compose's resolved JSON; no host configuration is present
			// in the control-plane worktree or filesystem.
			renderedSource := filepath.Join(t.TempDir(), "DockerData", "eizhu", "application.yml")
			rawSource := test.source
			if filepath.IsAbs(rawSource) {
				rawSource = renderedSource
			}
			if strings.HasPrefix(test.source, ".") {
				renderedSource = filepath.Join(worktree, "application.yml")
			}
			writeTestFile(t, filepath.Join(worktree, "compose.yml"), "services:\n  app:\n    image: example/app:1\n    volumes:\n      - "+rawSource+":/app/application.yml:ro\n")
			if _, err := os.Lstat(renderedSource); !os.IsNotExist(err) {
				t.Fatalf("expected bind source to be absent from control plane: %v", err)
			}
			h.runner.SetRendered(renderedBind(renderedSource, "/app/application.yml", true))
			if len(test.transports) > 0 {
				resolver := bindTargetResolver{}
				runners := map[string]compose.Runner{}
				for i, transport := range test.transports {
					id := fmt.Sprintf("target-%d", i)
					host := map[string]string{"unix": "unix:///ali.sock", "tcp": "tcp://aliyun:2376", "agent": "unix:///agent-proxy.sock"}[transport]
					row := database.Node{ID: id, Name: transport, ConnectionType: transport, Endpoint: host, TLSMode: "disabled", AllowedBindRootsJSON: "[]", Enabled: true}
					if err := h.db.Create(&row).Error; err != nil {
						t.Fatal(err)
					}
					if err := h.db.Create(&database.DeliveryProjectNode{ProjectID: h.project.ID, NodeID: id}).Error; err != nil {
						t.Fatal(err)
					}
					resolver[id] = compose.Target{NodeID: id, NodeName: transport, Host: host, RemoteSources: transport != "unix"}
					runners[id] = h.runner
				}
				h.service.SetTargetResolver(resolver)
				h.service.compose = &targetedFakeRunner{fakeComposeRunner: h.runner, runners: runners}
			}
			if test.want == "" {
				release := syncAndDeploy(t, h)
				if release.Status != StatusSucceeded || len(release.Deployments) != len(test.transports) {
					t.Fatalf("deployment result = %#v", release)
				}
				assertCallSubsequence(t, h.runner.Calls(), []string{"validate", "render", "pull", "up", "ps"})
				return
			}
			syncTask, err := h.service.Sync(context.Background(), h.project.Name, "manual", Actor{Name: "operator"})
			if err != nil {
				t.Fatal(err)
			}
			waitTask(t, h.db, syncTask.ID, task.StatusFailed)
			var result database.Task
			if err := h.db.First(&result, "id = ?", syncTask.ID).Error; err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(result.Message, test.want) {
				t.Fatalf("task error = %q, want %q", result.Message, test.want)
			}
			for _, call := range h.runner.Calls() {
				if call == "pull" || call == "up" {
					t.Fatalf("rejected release executed %s", call)
				}
			}
		})
	}
}

func TestServiceHostBindEngineFailureIsReported(t *testing.T) {
	h := newCDHarness(t, ModeAuto)
	source := "/Data/Docker/DockerData/eizhu/application.yml"
	writeTestFile(t, filepath.Join(h.git.revision.WorktreePath, "compose.yml"), "services:\n  app:\n    image: example/app:1\n    volumes:\n      - "+source+":/app/application.yml:ro\n")
	h.runner.SetRendered(renderedBind(source, "/app/application.yml", true))
	h.runner.up = errors.New("Docker Engine: bind source path does not exist on target host")
	syncTask, err := h.service.Sync(context.Background(), h.project.Name, "manual", Actor{Name: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, h.db, syncTask.ID, task.StatusFailed)
	releases, err := h.service.ListReleases(context.Background(), h.project.Name)
	if err != nil || len(releases) != 1 || releases[0].Status != StatusFailed || !strings.Contains(releases[0].FailureReason, "Docker Engine: bind source path") {
		t.Fatalf("Engine failure was not retained: %#v, %v", releases, err)
	}
	assertCallSubsequence(t, h.runner.Calls(), []string{"validate", "render", "pull", "up"})
}
