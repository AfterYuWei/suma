//go:build dockersmoke

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	containerdomain "github.com/suma/suma/server/internal/container"
	"github.com/suma/suma/server/internal/node"
)

// Only reads the real Engine. SUMA accounts and node configuration exist
// exclusively in the automatically cleaned PostgreSQL test schema.
func TestRealDockerContainerRuntimeReads(t *testing.T) {
	if os.Getenv("SUMA_RUN_DOCKER_SMOKE") != "1" {
		t.Skip("set SUMA_RUN_DOCKER_SMOKE=1 for real Docker reads")
	}
	_, nodes, request := overviewHTTPFixture(t, http.NotFoundHandler())
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if _, err := nodes.Update(ctx, "local", node.Input{Name: "Runtime read smoke", ConnectionType: node.ConnectionUnix, Endpoint: "unix:///var/run/docker.sock", TLSMode: node.TLSDisabled, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/nodes/local/containers"
	if response := request(ctx, base+"/runtime", false); response.Code != 401 {
		t.Fatal("runtime metadata was exposed without authentication")
	}
	read := func(path string, destination any) {
		t.Helper()
		response := request(ctx, path, true)
		if response.Code != 200 {
			t.Fatalf("read %s: HTTP %d", path, response.Code)
		}
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(envelope.Data, destination); err != nil {
			t.Fatal(err)
		}
	}
	var containers []containerdomain.Summary
	var metadata []containerdomain.RuntimeMetadata
	read(base, &containers)
	read(base+"/runtime", &metadata)
	if len(containers) == 0 || len(metadata) != len(containers) {
		t.Fatalf("cannot verify runtime metadata: containers=%d metadata=%d", len(containers), len(metadata))
	}
	var overview containerdomain.Overview
	read(base+"/"+containers[0].ID+"/overview", &overview)
	if overview.ID != containers[0].ID || overview.Counts.Networks < len(overview.Networks) || overview.Counts.Mounts < len(overview.Mounts) || overview.Counts.Ports < len(overview.Ports) || len(overview.Networks) > 3 || len(overview.Mounts) > 3 || len(overview.Ports) > 4 {
		t.Fatal("real Docker overview was incomplete or had the wrong identity")
	}
	ids := map[string]bool{}
	for _, row := range containers {
		ids[row.ID] = true
	}
	for _, row := range metadata {
		if !ids[row.ID] || row.HealthStatus == nil || row.RestartCount == nil || *row.RestartCount < 0 || row.MemoryLimitBytes == nil || *row.MemoryLimitBytes < 0 {
			t.Fatalf("incomplete or invalid metadata for container %.12s", row.ID)
		}
	}
	var first, second []containerdomain.Metrics
	read(base+"/metrics", &first)
	select {
	case <-time.After(5 * time.Second):
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	read(base+"/metrics", &second)
	previous := map[string]containerdomain.Metrics{}
	for _, row := range first {
		previous[row.ID] = row
	}
	verified := 0
	for _, row := range second {
		before, ok := previous[row.ID]
		if !ok || !row.Available || !before.Available || row.StartedAt == nil || before.StartedAt == nil || !row.StartedAt.Equal(*before.StartedAt) {
			continue
		}
		if row.SampledAt == nil || before.SampledAt == nil {
			t.Fatal("real Docker samples omitted their timestamps")
		}
		seconds := row.SampledAt.Sub(*before.SampledAt).Seconds()
		if seconds <= 0 || seconds > 15 || row.NetworkRXBytes < before.NetworkRXBytes || row.NetworkTXBytes < before.NetworkTXBytes {
			t.Fatal("real Docker counters could not form a valid network rate")
		}
		verified++
	}
	if verified == 0 {
		t.Fatal("no running container had two valid network samples")
	}
	t.Logf("verified %d runtime metadata rows and %d pairs of live network samples", len(metadata), verified)
}
