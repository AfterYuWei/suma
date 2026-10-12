//go:build dockersmoke

package docker

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	dockertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	domain "github.com/suma/suma/server/internal/container"
)

func TestRealDockerContainerObservation(t *testing.T) {
	if os.Getenv("SUMA_RUN_DOCKER_SMOKE") != "1" {
		t.Fatal("set SUMA_RUN_DOCKER_SMOKE=1 for this dedicated smoke test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	a := &Adapter{client: cli}
	since := time.Now().UTC().Add(-time.Second)
	result, err := cli.ContainerCreate(ctx, &dockertypes.Config{Image: "alpine:3.24", Cmd: []string{"sh", "-c", "while true; do sleep 1; done"}, Env: []string{"PASSWORD=smoke-private-secret"}, Healthcheck: &dockertypes.HealthConfig{Test: []string{"CMD-SHELL", "echo password=$PASSWORD; exit 1"}, Interval: time.Second, Timeout: time.Second, Retries: 1}}, &dockertypes.HostConfig{}, nil, nil, "suma-observation-smoke-"+time.Now().Format("150405.000000"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := cli.ContainerRemove(cleanupCtx, result.ID, dockertypes.RemoveOptions{Force: true}); err != nil {
			t.Error(err)
		}
	})
	if err := cli.ContainerStart(ctx, result.ID, dockertypes.StartOptions{}); err != nil {
		t.Fatal(err)
	}
	var diagnostics domain.Diagnostics
	for {
		diagnostics, err = domain.ReadDiagnostics(ctx, a, result.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(diagnostics.Health.Logs) > 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("health check never ran")
		case <-time.After(250 * time.Millisecond):
		}
	}
	if !diagnostics.Health.Logs[0].OutputRedacted || strings.Contains(diagnostics.Health.Logs[0].Output, "smoke-private-secret") {
		t.Fatal("real health diagnostic leaked secret")
	}
	streamCtx, streamCancel := context.WithCancel(ctx)
	stream, err := domain.OpenNormalizedStats(streamCtx, a, result.ID)
	if err != nil {
		streamCancel()
		t.Fatal(err)
	}
	data := make([]byte, 4096)
	if n, err := stream.Read(data); err != nil || n == 0 {
		streamCancel()
		stream.Close()
		t.Fatal("normalized stats unavailable")
	}
	streamCancel()
	stream.Close()
	if err := cli.ContainerStop(ctx, result.ID, dockertypes.StopOptions{}); err != nil {
		t.Fatal(err)
	}
	history, err := domain.ReadContainerEvents(ctx, a, "local", result.ID, domain.EventQuery{Since: since, Until: time.Now().UTC(), Limit: 256})
	if err != nil && err != io.EOF {
		t.Fatal(err)
	}
	actions := map[string]bool{}
	for _, e := range history.Events {
		actions[e.Action] = true
	}
	if !actions["create"] || !actions["start"] || !actions["die"] || !actions["stop"] {
		t.Fatalf("real lifecycle events missing: %v", actions)
	}
	t.Logf("verified masked health, normalized stats, cancellation and %d real events", len(history.Events))
}
