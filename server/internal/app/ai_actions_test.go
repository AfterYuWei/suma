package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/ai"
	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/cleanup"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/node"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/task"
	"github.com/suma/suma/server/internal/testutil"
)

type actionVolumeRuntime struct{ cleanup.Runtime }

func (actionVolumeRuntime) CleanupResource(_ context.Context, kind cleanup.Kind, name string) (cleanup.Resource, error) {
	return cleanup.Resource{Kind: kind, ID: name, Name: name, Labels: map[string]string{}, CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}, nil
}
func TestAIExtendedActionsFreezeExecuteAndVerifyWithoutDaemon(t *testing.T) {
	for _, check := range []struct{ action, resource, params string }{
		{"container.pause", "container", "{}"}, {"container.unpause", "container", "{}"}, {"container.kill", "container", "{}"}, {"container.rename", "container", `{"name":"renamed"}`}, {"container.remove", "container", `{"force":true}`},
		{"image.tag", "image", `{"reference":"example/app:reviewed"}`}, {"image.remove", "image", `{"force":true}`},
		{"network.create", "new-network", `{"name":"new-network","driver":"bridge","subnet":"10.33.0.0/24","gateway":"10.33.0.1","ipv6":false}`}, {"network.remove", "network", "{}"},
		{"volume.create", "new-volume", `{"name":"new-volume","driver":"local","labels":{},"options":{}}`}, {"volume.remove", "volume", "{}"},
	} {
		t.Run(check.action, func(t *testing.T) {
			ctx := context.Background()
			db, err := testutil.Open(t)
			if err != nil {
				t.Fatal(err)
			}
			store, err := secret.Open(filepath.Join(t.TempDir(), "key"))
			if err != nil {
				t.Fatal(err)
			}
			containerID := strings.Repeat("c", 64)
			imageID := "sha256:" + strings.Repeat("a", 64)
			networkID := strings.Repeat("b", 64)
			var mu sync.Mutex
			state, name := "running", "original"
			if check.action == "container.unpause" {
				state = "paused"
			}
			containerPresent, imagePresent, tagged := true, true, false
			networkPresent := check.action != "network.create"
			volumePresent := check.action != "volume.create"
			networkName, volumeName := "network", "volume"
			version := regexp.MustCompile(`^/v[0-9.]+`)
			engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				path := version.ReplaceAllString(req.URL.Path, "")
				w.Header().Set("Content-Type", "application/json")
				write := func(value any) { _ = json.NewEncoder(w).Encode(value) }
				container := func() map[string]any {
					return map[string]any{"Id": containerID, "Name": "/" + name, "Image": imageID, "Created": "2026-01-01T00:00:00Z", "Config": map[string]any{"Image": "example/app:original", "Env": []string{}, "Labels": map[string]string{}}, "State": map[string]any{"Status": state, "StartedAt": "2026-01-01T00:00:01Z", "FinishedAt": "2026-01-01T00:00:02Z"}, "HostConfig": map[string]any{}, "NetworkSettings": map[string]any{}}
				}
				image := func() map[string]any {
					tags := []string{"example/app:original"}
					if tagged {
						tags = append(tags, "example/app:reviewed")
					}
					return map[string]any{"Id": imageID, "RepoTags": tags, "RepoDigests": []string{}, "Created": "2026-01-01T00:00:00Z", "Architecture": "amd64", "Os": "linux", "Config": map[string]any{}, "RootFS": map[string]any{"Type": "layers", "Layers": []string{}}}
				}
				network := func() map[string]any {
					return map[string]any{"Id": networkID, "Name": networkName, "Driver": "bridge", "Scope": "local", "Containers": map[string]any{}, "IPAM": map[string]any{"Config": []any{}}}
				}
				volume := func() map[string]any {
					return map[string]any{"Name": volumeName, "Driver": "local", "Mountpoint": "/volumes/" + volumeName, "CreatedAt": "2026-01-01T00:00:00Z", "Labels": map[string]string{}, "Options": map[string]string{}}
				}
				switch {
				case path == "/_ping":
					w.Header().Set("Api-Version", "1.44")
					w.Write([]byte("OK"))
				case path == "/info":
					write(map[string]any{"ID": "action-engine", "OSType": "linux", "Architecture": "amd64", "ServerVersion": "27.0"})
				case path == "/containers/json":
					rows := []any{}
					if containerPresent && !strings.Contains(req.URL.Query().Get("filters"), "volume") {
						rows = append(rows, map[string]any{"Id": containerID, "Names": []string{"/" + name}, "Image": imageID, "ImageID": imageID, "State": state, "Labels": map[string]string{}, "Mounts": []any{}})
					}
					write(rows)
				case path == "/containers/"+containerID+"/json":
					write(container())
				case strings.HasPrefix(path, "/containers/") && req.Method == "POST":
					switch {
					case strings.HasSuffix(path, "/pause"):
						state = "paused"
					case strings.HasSuffix(path, "/unpause"):
						state = "running"
					case strings.HasSuffix(path, "/kill"):
						state = "exited"
					case strings.HasSuffix(path, "/rename"):
						name = req.URL.Query().Get("name")
					}
					w.WriteHeader(204)
				case path == "/containers/"+containerID && req.Method == "DELETE":
					if req.URL.Query().Get("v") == "1" {
						t.Error("container action deleted volumes")
					}
					containerPresent = false
					w.WriteHeader(204)
				case path == "/images/json":
					rows := []any{}
					if imagePresent {
						summary := image()
						summary["Created"] = time.Now().Unix()
						rows = append(rows, summary)
					}
					write(rows)
				case strings.HasPrefix(path, "/images/") && strings.HasSuffix(path, "/tag"):
					tagged = true
					w.WriteHeader(201)
				case strings.HasPrefix(path, "/images/") && req.Method == "DELETE":
					imagePresent = false
					write([]any{map[string]any{"Deleted": imageID}})
				case strings.HasPrefix(path, "/images/") && strings.HasSuffix(path, "/json"):
					if strings.Contains(path, "example/app:reviewed") && !tagged {
						w.WriteHeader(404)
						write(map[string]string{"message": "No such image"})
					} else {
						write(image())
					}
				case path == "/networks":
					rows := []any{}
					if networkPresent {
						rows = append(rows, network())
					}
					write(rows)
				case path == "/networks/create":
					var in map[string]any
					_ = json.NewDecoder(req.Body).Decode(&in)
					networkName, _ = in["Name"].(string)
					networkPresent = true
					w.WriteHeader(201)
					write(map[string]string{"Id": networkID})
				case strings.HasPrefix(path, "/networks/"):
					if req.Method == "DELETE" {
						networkPresent = false
						w.WriteHeader(204)
					} else {
						write(network())
					}
				case path == "/volumes":
					rows := []any{}
					if volumePresent {
						rows = append(rows, volume())
					}
					write(map[string]any{"Volumes": rows, "Warnings": []string{}})
				case path == "/volumes/create":
					var in map[string]any
					_ = json.NewDecoder(req.Body).Decode(&in)
					volumeName, _ = in["Name"].(string)
					volumePresent = true
					w.WriteHeader(201)
					write(volume())
				case strings.HasPrefix(path, "/volumes/"):
					if req.Method == "DELETE" {
						volumePresent = false
						w.WriteHeader(204)
					} else {
						write(volume())
					}
				default:
					http.NotFound(w, req)
				}
			}))
			defer engine.Close()
			nodes, err := node.NewService(db, store, "unix:///tmp/suma-unopened-action-test.sock")
			if err != nil {
				t.Fatal(err)
			}
			defer nodes.Close()
			if err = db.Model(&database.Node{}).Where("id = ?", "local").Updates(map[string]any{"endpoint": strings.Replace(engine.URL, "http://", "tcp://", 1), "connection_type": node.ConnectionTCP, "tls_mode": node.TLSDisabled, "status": "online"}).Error; err != nil {
				t.Fatal(err)
			}
			tasks := task.NewService(db)
			cleaner := cleanup.NewService(db, tasks, audit.NewService(db), cleanup.Dependencies{Node: func(context.Context, string) (cleanup.Node, error) {
				return cleanup.Node{ID: "local", Name: "Local", Enabled: true, RuntimeKey: "action-engine"}, nil
			}, Runtime: func(context.Context, string) (cleanup.Runtime, error) { return actionVolumeRuntime{}, nil }, Protection: func(context.Context, string) (cleanup.Protection, error) { return cleanup.Protection{}, nil }})
			defer cleaner.Stop()
			runtime := aiRuntime{db: db, nodes: nodes, tasks: tasks, secrets: store, cleanup: cleaner}
			resource := check.resource
			switch resource {
			case "container":
				resource = containerID
			case "image":
				resource = imageID
			case "network":
				resource = networkID
			}
			request := ai.OperationRequest{NodeID: "local", Action: check.action, ResourceID: resource, Parameters: json.RawMessage(check.params)}
			snapshot, err := runtime.Freeze(ctx, "local", request)
			if err != nil {
				t.Fatal(err)
			}
			confirmations := map[string]string{}
			for _, item := range snapshot.Confirmations {
				value := item.Expected
				if item.Checkbox {
					value = "true"
				}
				confirmations[item.Key] = value
			}
			op := database.AIOperation{NodeID: "local", Action: request.Action, ResourceID: resource, ParametersJSON: check.params, ConfirmedJSON: jsonText(confirmations)}
			if err = runtime.Execute(ctx, op, snapshot, func(int, string) {}); err != nil {
				t.Fatal(err)
			}
			verification, err := runtime.Verify(ctx, op, snapshot)
			if err != nil || !verification.Satisfied {
				t.Fatal(fmt.Sprintf("actual state not verified: %+v", verification), err)
			}
		})
	}
}
