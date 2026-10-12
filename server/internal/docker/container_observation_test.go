package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	dockertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	domain "github.com/suma/suma/server/internal/container"
)

func TestContainerDiagnosticsMasksHealthAndDistinguishesExit(t *testing.T) {
	a := newAdapter(t, newDockerStub(t, map[string]http.HandlerFunc{"/containers/app/json": func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"Id": "app", "Created": "2026-10-12T00:00:00Z", "RestartCount": 3, "Config": map[string]any{"Env": []string{"DATABASE_PASSWORD=private-value"}, "Healthcheck": map[string]any{"Test": []string{"CMD-SHELL", "curl http://localhost?password=private-value"}, "Interval": int64(time.Second)}}, "HostConfig": map[string]any{"Memory": 0, "RestartPolicy": map[string]any{"Name": "unless-stopped"}}, "State": map[string]any{"Status": "exited", "Running": false, "OOMKilled": false, "ExitCode": 137, "StartedAt": "2026-10-12T00:00:00Z", "FinishedAt": "2026-10-12T01:00:00Z", "Error": "password=private-value", "Health": map[string]any{"Status": "unhealthy", "FailingStreak": 3, "Log": []map[string]any{{"Start": "2026-10-12T00:59:00Z", "End": "2026-10-12T00:59:01Z", "ExitCode": 1, "Output": "private-value token=another-secret " + strings.Repeat("界", 2000)}}}}})
	}}))
	d, err := domain.ReadDiagnostics(context.Background(), a, "app")
	if err != nil {
		t.Fatal(err)
	}
	if d.Runtime.ExitCode == nil || *d.Runtime.ExitCode != 137 || d.Runtime.OOMKilled == nil || *d.Runtime.OOMKilled || d.Runtime.MemoryLimitBytes == nil || *d.Runtime.MemoryLimitBytes != 0 {
		t.Fatal("exit and unlimited memory lost")
	}
	data, _ := json.Marshal(d)
	if strings.Contains(string(data), "private-value") || strings.Contains(string(data), "another-secret") {
		t.Fatal("diagnostic leaked a credential")
	}
	if len(d.Health.Logs) != 1 || !d.Health.Logs[0].OutputRedacted || !d.Health.Logs[0].OutputTruncated || len(d.Health.Logs[0].Output) > 4096 {
		t.Fatal("unbounded health output")
	}
}
func TestContainerEventHistoryFiniteAndWhitelisted(t *testing.T) {
	id := strings.Repeat("a", 64)
	now := time.Now().UTC()
	a := newAdapter(t, newDockerStub(t, map[string]http.HandlerFunc{"/events": func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("until") == "" || !strings.Contains(r.URL.Query().Get("filters"), id) {
			t.Error("events were not bounded/scoped")
		}
		w.Header().Set("Content-Type", "application/json")
		for i := range 3 {
			_ = json.NewEncoder(w).Encode(events.Message{Type: events.ContainerEventType, Action: "die", TimeNano: now.Add(time.Duration(i)).UnixNano(), Actor: events.Actor{ID: id, Attributes: map[string]string{"exitCode": fmt.Sprint(i), "password": "private-value", "execCommand": "token=private-value"}}})
		}
	}}))
	history, err := domain.ReadContainerEvents(context.Background(), a, "local", id, domain.EventQuery{Since: now.Add(-time.Hour), Until: now.Add(time.Second), Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Events) != 2 || !history.Truncated || history.Events[0].NodeID != "local" || history.Events[0].TimeNano == history.Events[1].TimeNano {
		t.Fatal("history truncation/precision failed")
	}
	bytes, _ := json.Marshal(history)
	if strings.Contains(string(bytes), "private-value") {
		t.Fatal("raw attributes leaked")
	}
}
func TestContainerEventCancellation(t *testing.T) {
	id := strings.Repeat("a", 64)
	canceled := make(chan struct{})
	a := newAdapter(t, newDockerStub(t, map[string]http.HandlerFunc{"/events": func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(canceled)
	}}))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := domain.WatchContainerEvents(ctx, a, "local", id, time.Now().Add(-time.Minute), func(domain.ContainerEvent) error { return nil })
	if err == nil {
		t.Fatal("stream did not cancel")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("Docker stream remained open")
	}
}
func TestStatsCacheNormalization(t *testing.T) {
	for _, key := range []string{"inactive_file", "total_inactive_file"} {
		var raw dockertypes.StatsResponse
		if err := json.Unmarshal([]byte(fmt.Sprintf(`{"read":"2026-10-12T00:00:00Z","memory_stats":{"usage":100,"stats":{"%s":40}},"cpu_stats":{"cpu_usage":{"total_usage":200},"system_cpu_usage":2000,"online_cpus":4},"precpu_stats":{"cpu_usage":{"total_usage":100},"system_cpu_usage":1800}}`, key)), &raw); err != nil {
			t.Fatal(err)
		}
		sample := normalizeStats(raw)
		if sample.MemoryBytes != 60 || sample.CPUPercent != 200 || !sample.CPUAvailable || !sample.MemoryAvailable {
			t.Fatalf("incorrect normalized stats: %#v", sample)
		}
	}
	missing := normalizeStats(dockertypes.StatsResponse{})
	if missing.CPUAvailable || missing.MemoryAvailable {
		t.Fatal("missing counters were represented as valid zero values")
	}
}

func TestContainerTopologyAndVolumeOwnership(t *testing.T) {
	generatedName := strings.Repeat("f", 64)
	a := newAdapter(t, newDockerStub(t, map[string]http.HandlerFunc{"/containers/app/json": func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{
			"Id": "app", "Config": map[string]any{"Volumes": map[string]any{"/anonymous": map[string]any{}}},
			"HostConfig":      map[string]any{"NetworkMode": "bridge", "Binds": []string{generatedName + ":/named:rw", "/srv/app:/app:ro"}},
			"Mounts":          []map[string]any{{"Type": "volume", "Name": generatedName, "Destination": "/named", "RW": true}, {"Type": "volume", "Name": "generated-anonymous", "Destination": "/anonymous", "RW": true}, {"Type": "bind", "Source": "/srv/app", "Destination": "/app", "RW": false}, {"Type": "tmpfs", "Destination": "/tmp", "RW": true}},
			"NetworkSettings": map[string]any{"Networks": map[string]any{"z-extra": map[string]string{"IPAddress": "172.21.0.2"}, "a-primary": map[string]string{"IPAddress": "172.20.0.2", "GlobalIPv6Address": "fd00::2", "IPv6Gateway": "fd00::1"}, "pending": nil}},
		})
	}}))
	detail, err := a.Get(context.Background(), "app")
	if err != nil {
		t.Fatal(err)
	}
	if detail.NetworkMode != "bridge" || len(detail.Networks) != 2 || detail.Networks[0].Name != "a-primary" || detail.Networks[0].IPv6Address != "fd00::2" || detail.Networks[0].IPv6Gateway != "fd00::1" {
		t.Fatal("topology was lost or unstable")
	}
	if len(detail.Mounts) != 4 || detail.Mounts[0].Anonymous == nil || *detail.Mounts[0].Anonymous || detail.Mounts[1].Anonymous == nil || !*detail.Mounts[1].Anonymous || detail.Mounts[2].ReadWrite || detail.Mounts[2].Anonymous != nil {
		t.Fatal("volume ownership was inferred from a generated-looking name or bind mode was lost")
	}
}
