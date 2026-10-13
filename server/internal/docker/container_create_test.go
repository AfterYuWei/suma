package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	dockercontainer "github.com/docker/docker/api/types/container"
	dockernetwork "github.com/docker/docker/api/types/network"
	domain "github.com/suma/suma/server/internal/container"
)

func TestCreateContainerMapsEngineConfiguration(t *testing.T) {
	stub := newDockerStub(t, map[string]http.HandlerFunc{
		"/containers/create": func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				dockercontainer.Config
				HostConfig       dockercontainer.HostConfig
				NetworkingConfig dockernetwork.NetworkingConfig
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if r.URL.Query().Get("name") != "my-app" || body.Image != "nginx:alpine" || len(body.Env) != 1 || body.Env[0] != "TOKEN=private" || body.Cmd[1] != "3600" || body.User != "1000:1000" || body.WorkingDir != "/app" {
				t.Fatalf("container config: %#v", body.Config)
			}
			host := body.HostConfig
			if host.Memory != 128*1024*1024 || host.NanoCPUs != 500_000_000 || !host.ReadonlyRootfs || host.RestartPolicy.Name != "unless-stopped" || host.NetworkMode != "custom" {
				t.Fatalf("host config: %#v", host)
			}
			if binding := host.PortBindings["80/tcp"]; len(binding) != 2 || binding[0].HostIP != "127.0.0.1" || binding[0].HostPort != "8080" || binding[1].HostPort != "" {
				t.Fatal("multiple/dynamic port bindings lost")
			}
			if len(host.Mounts) != 2 || host.Mounts[0].Type != "volume" || host.Mounts[1].Type != "bind" || !host.Mounts[1].ReadOnly || body.NetworkingConfig.EndpointsConfig["custom"] == nil {
				t.Fatal("mount or network configuration lost")
			}
			writeJSON(w, 201, map[string]any{"Id": strings.Repeat("ab", 32), "Warnings": []string{"controlled warning"}})
		},
	})
	adapter := newAdapter(t, stub)
	input := domain.CreateRequest{Name: "my-app", Image: "nginx:alpine", Environment: []domain.CreateEnvironment{{Key: "TOKEN", Value: "private"}}, Command: []string{"sleep", "3600"}, Network: "custom", User: "1000:1000", WorkingDirectory: "/app", ReadOnlyRootFS: true, MemoryMB: 128, CPUs: .5, RestartPolicy: "unless-stopped", Ports: []domain.CreatePort{{ContainerPort: 80, HostPort: 8080}, {ContainerPort: 80, HostPort: 0}}, Mounts: []domain.CreateMount{{Type: "volume", Source: "data", Target: "/data"}, {Type: "bind", Source: "/srv/app", Target: "/app", ReadOnly: true}}}
	if err := domain.ValidateCreateRequest(&input, false); err != nil {
		t.Fatal(err)
	}
	created, err := adapter.CreateContainer(context.Background(), input)
	if err != nil || len(created.Warnings) != 1 || created.ID != strings.Repeat("ab", 32) {
		t.Fatal(created, err)
	}
}

func TestContainerCreationImageInspectionDistinguishesMissingAndFailure(t *testing.T) {
	for _, code := range []int{200, 404, 503} {
		stub := newDockerStub(t, map[string]http.HandlerFunc{"/images/nginx:alpine/json": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, code, map[string]any{"Id": "image", "message": "controlled response"})
		}})
		exists, err := newAdapter(t, stub).ContainerImageExists(context.Background(), "nginx:alpine")
		if exists != (code == 200) || (err != nil) != (code == 503) {
			t.Fatalf("inspection code %d: exists=%v err=%v", code, exists, err)
		}
	}
}
