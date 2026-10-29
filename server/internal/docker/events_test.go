package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/docker/docker/api/types/events"
	"github.com/suma/suma/server/internal/event"
)

func TestNotificationEventsPreferContainerNamesAndRecoverMissingMetadata(t *testing.T) {
	for _, scenario := range []string{"event metadata", "inspect fallback", "already removed"} {
		t.Run(scenario, func(t *testing.T) {
			attributes := map[string]string{"exitCode": "137"}
			if scenario == "event metadata" {
				attributes["name"] = "redis-main"
				attributes["com.docker.compose.project"] = "homelab"
			}
			stub := newDockerStub(t, map[string]http.HandlerFunc{
				"/events": func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(events.Message{Type: events.ContainerEventType, Action: events.ActionDie, Actor: events.Actor{ID: "container-id", Attributes: attributes}})
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				},
				"/containers/container-id/json": func(w http.ResponseWriter, r *http.Request) {
					if scenario == "already removed" {
						writeJSON(w, 404, map[string]string{"message": "removed"})
						return
					}
					writeJSON(w, 200, map[string]any{"Id": "container-id", "Name": "/redis-main", "Config": map[string]any{"Labels": map[string]string{"com.docker.compose.project": "homelab"}}})
				},
			})
			adapter := newAdapter(t, stub)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var observed event.Event
			_ = adapter.WatchEvents(ctx, func(e event.Event) { observed = e; cancel() })
			if observed.Type != "container.exited" || observed.ResourceID != "container-id" || observed.Message != "die exit=137" {
				t.Fatal(observed)
			}
			if scenario != "already removed" && (observed.Title != "redis-main" || observed.Project != "homelab") {
				t.Fatal("resource identity lost", observed)
			}
			if scenario == "already removed" && observed.Title != "" {
				t.Fatal("invented a resource name", observed)
			}
			inspects := stub.find(t, func(r stubRequest) bool { return r.Path == "/containers/container-id/json" })
			if scenario == "event metadata" && len(inspects) != 0 {
				t.Fatal("unnecessary inspect")
			}
			if scenario != "event metadata" && len(inspects) != 1 {
				t.Fatal("metadata was not recovered")
			}
		})
	}
}
