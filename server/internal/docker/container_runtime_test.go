package docker

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	dockercontainer "github.com/docker/docker/api/types/container"
)

func TestRuntimeMetadataHealthLimitsAndPartialFailure(t *testing.T) {
	healths := []string{"healthy", "unhealthy", "starting", "none", "unknown", "failed"}
	rows := make([]dockercontainer.Summary, len(healths))
	handlers := map[string]http.HandlerFunc{}
	for index, health := range healths {
		id := health
		rows[index] = dockercontainer.Summary{ID: id}
		handlers["/containers/"+id+"/json"] = func(w http.ResponseWriter, r *http.Request) {
			if health == "failed" {
				http.Error(w, "unavailable", 500)
				return
			}
			state := map[string]any{"Status": "running"}
			config := map[string]any{}
			if health != "none" {
				config["Healthcheck"] = map[string]any{"Test": []string{"CMD", "check"}}
			}
			if health != "none" && health != "unknown" {
				state["Health"] = map[string]any{"Status": health}
			}
			if health == "none" {
				state["Status"] = "exited"
				config["Healthcheck"] = map[string]any{"Test": []string{"NONE"}}
			}
			writeJSON(w, 200, map[string]any{"State": state, "Config": config, "RestartCount": index, "HostConfig": map[string]any{"Memory": int64(index%2) * 536870912}})
		}
	}
	handlers["/containers/json"] = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("all") != "1" {
			t.Error("metadata must include stopped containers")
		}
		writeJSON(w, 200, rows)
	}
	adapter := newAdapter(t, newDockerStub(t, handlers))
	values, err := adapter.RuntimeMetadata(context.Background())
	if err != nil || len(values) != len(rows) {
		t.Fatalf("metadata: %#v %v", values, err)
	}
	for index, value := range values {
		if value.ID != healths[index] {
			t.Fatal("IDs or order changed")
		}
		if value.ID == "failed" {
			if value.HealthStatus != nil || value.RestartCount != nil || value.MemoryLimitBytes != nil {
				t.Fatal("failed inspection fabricated values")
			}
			continue
		}
		if value.HealthStatus == nil || *value.HealthStatus != value.ID || value.RestartCount == nil || *value.RestartCount != index || value.MemoryLimitBytes == nil || *value.MemoryLimitBytes != int64(index%2)*536870912 {
			t.Fatalf("unexpected metadata: %#v", value)
		}
	}
}

func TestRuntimeMetadataLimitsInspectionConcurrency(t *testing.T) {
	rows := make([]dockercontainer.Summary, 18)
	var mu sync.Mutex
	active, maximum := 0, 0
	handlers := map[string]http.HandlerFunc{}
	for index := range rows {
		rows[index].ID = fmt.Sprintf("app-%d", index)
		handlers["/containers/"+rows[index].ID+"/json"] = func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			active++
			maximum = max(maximum, active)
			mu.Unlock()
			time.Sleep(20 * time.Millisecond)
			mu.Lock()
			active--
			mu.Unlock()
			writeJSON(w, 200, map[string]any{"State": map[string]any{}, "Config": map[string]any{}, "HostConfig": map[string]any{}})
		}
	}
	handlers["/containers/json"] = func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, rows) }
	adapter := newAdapter(t, newDockerStub(t, handlers))
	values, err := adapter.RuntimeMetadata(context.Background())
	if err != nil || len(values) != len(rows) || maximum > 6 || maximum < 2 {
		t.Fatalf("rows=%d, maximum=%d, error=%v", len(values), maximum, err)
	}
}

func TestRuntimeMetadataCancelsEngineReads(t *testing.T) {
	reached := make(chan struct{})
	stub := newDockerStub(t, map[string]http.HandlerFunc{
		"/containers/json": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, []dockercontainer.Summary{{ID: "app"}})
		},
		"/containers/app/json": func(w http.ResponseWriter, r *http.Request) { close(reached); <-r.Context().Done() },
	})
	adapter := newAdapter(t, stub)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := adapter.RuntimeMetadata(ctx); result <- err }()
	select {
	case <-reached:
	case <-ctx.Done():
		t.Fatal("Engine read did not start")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Engine read was not canceled promptly")
	}
}
