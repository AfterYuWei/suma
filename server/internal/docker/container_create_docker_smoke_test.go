//go:build dockersmoke

package docker

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/audit"
	domain "github.com/suma/suma/server/internal/container"
	"github.com/suma/suma/server/internal/credential"
	"github.com/suma/suma/server/internal/task"
	"github.com/suma/suma/server/internal/testutil"
)

func TestRealDockerContainerCreation(t *testing.T) {
	if os.Getenv("SUMA_RUN_DOCKER_SMOKE") != "1" {
		t.Fatal("set SUMA_RUN_DOCKER_SMOKE=1 for the dedicated smoke test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	adapter, err := New("unix:///var/run/docker.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	tasks := task.NewService(db)
	name := "suma-container-create-smoke-" + time.Now().Format("150405.000000000")
	service := domain.NewCreationService(adapter, tasks, audit.NewService(db))
	input := domain.CreateRequest{Name: name, Image: "alpine:3.24", PullPolicy: "never", Network: "bridge", RestartPolicy: "no", Command: []string{"sleep", "120"}, Environment: []domain.CreateEnvironment{{Key: "TOKEN", Value: "smoke-creation-private"}}, Ports: []domain.CreatePort{{ContainerPort: 8080, HostIP: "127.0.0.1"}}, Mounts: []domain.CreateMount{{Type: "bind", Source: t.TempDir(), Target: "/smoke-data", ReadOnly: true}}, MemoryMB: 64, CPUs: .25}
	// The test only removes the container that this task created, after checking
	// its unique smoke name. It does not prune inventory or delete any volumes.
	var createdID string
	t.Cleanup(func() {
		if createdID == "" {
			return
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		row, err := adapter.Get(cleanupCtx, createdID)
		if err != nil || row.Name != name || !strings.HasPrefix(row.Name, "suma-container-create-smoke-") {
			t.Error("smoke cleanup identity check failed")
			return
		}
		if err := adapter.ForceRemove(cleanupCtx, createdID, false); err != nil {
			t.Error(err)
		}
	})
	row, err := service.CreateForNode("local", "Smoke", input, domain.CreationActor{}, credential.RegistryMaterial{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := tasks.Wait(ctx, row.ID); err != nil {
		t.Fatal(err)
	}
	steps, err := tasks.Steps(ctx, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range steps {
		if strings.HasPrefix(step.StepID, "container:") {
			createdID = strings.TrimPrefix(step.StepID, "container:")
		}
	}
	result, err := tasks.GetForNode(ctx, "local", row.ID)
	if err != nil || result.Status != task.StatusSuccess || createdID == "" {
		t.Fatalf("creation task: %#v %v", result, err)
	}
	detail, err := adapter.Get(ctx, createdID)
	if err != nil || detail.Name != name || detail.State != "running" || len(detail.Ports) != 1 || detail.Ports[0].PublicPort == 0 || detail.Ports[0].IP != "127.0.0.1" || len(detail.Mounts) != 1 || detail.Mounts[0].ReadWrite {
		t.Fatalf("created container did not match submitted config: %#v %v", detail, err)
	}
	if len(detail.Environment) == 0 {
		t.Fatal("environment missing")
	}
	for _, value := range detail.Environment {
		if value.Key == "TOKEN" && (!value.Sensitive || value.Value != "") {
			t.Fatal("container detail exposed secret")
		}
	}
	inspect, err := adapter.client.ContainerInspect(ctx, createdID)
	if err != nil || inspect.HostConfig.Memory != 64*1024*1024 || inspect.HostConfig.NanoCPUs != 250_000_000 {
		t.Fatal("Docker resource limits differ", err)
	}
	t.Log("verified task-driven Docker create/start, dynamic loopback port, read-only bind, resource limits, secret masking and owned-container cleanup")
}
