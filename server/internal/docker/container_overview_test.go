package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	domain "github.com/suma/suma/server/internal/container"
)

func TestContainerOverviewSingleReadAndSafeProjection(t *testing.T) {
	reads := 0
	a := newAdapter(t, newDockerStub(t, map[string]http.HandlerFunc{"/containers/app/json": func(w http.ResponseWriter, r *http.Request) {
		reads++
		writeJSON(w, 200, map[string]any{
			"Id": "app", "Name": "/app", "Image": "sha256:abcd", "Created": "2026-10-12T00:00:00Z", "RestartCount": 2,
			"Config":          map[string]any{"Image": "app:latest", "User": "1000:1000", "WorkingDir": "/app", "Cmd": []string{"serve", "--token", "command-private", "--password=argument-private", "env-private"}, "Entrypoint": []string{"/entrypoint"}, "Env": []string{"DATABASE_PASSWORD=env-private", "MODE=production", "DATABASE_URL=postgres://user:credential-private@db/app"}, "Labels": map[string]string{"secret": "label-private", "example.copy": "env-private", "example.mode": "production"}, "ExposedPorts": map[string]any{"443/tcp": map[string]any{}}},
			"HostConfig":      map[string]any{"NanoCpus": int64(2500000000), "Memory": 536870912, "MemoryReservation": 268435456, "MemorySwap": -1, "PidsLimit": 64, "ShmSize": 67108864, "ReadonlyRootfs": true, "NetworkMode": "bridge", "RestartPolicy": map[string]any{"Name": "on-failure", "MaximumRetryCount": 3}, "LogConfig": map[string]any{"Type": "json-file", "Config": map[string]string{"token": "logging-private"}}, "PortBindings": map[string]any{"8080/tcp": []map[string]string{{"HostIp": "0.0.0.0", "HostPort": ""}}}},
			"State":           map[string]any{"Status": "running", "Running": true, "Pid": 123, "StartedAt": "2026-10-12T00:01:00Z", "FinishedAt": "0001-01-01T00:00:00Z", "Health": map[string]any{"Status": "healthy"}},
			"NetworkSettings": map[string]any{"Ports": map[string]any{"8080/tcp": []map[string]string{{"HostIp": "0.0.0.0", "HostPort": "32780"}, {"HostIp": "::", "HostPort": "32780"}}}, "Networks": map[string]any{"app_net": map[string]string{"IPAddress": "172.20.0.2", "GlobalIPv6Address": "fd00::2"}}},
			"Mounts":          []map[string]any{{"Type": "bind", "Source": "/host/data", "Destination": "/data", "RW": true}},
		})
	}}))
	d, err := domain.ReadOverview(context.Background(), a, "app")
	if err != nil {
		t.Fatal(err)
	}
	if reads != 1 || d.ID != "app" {
		t.Fatal("overview did extra inspections or lost identity")
	}
	if len(d.Ports) != 3 || d.Ports[0].PrivatePort != 443 || d.Ports[0].PublicPort != 0 || d.Ports[1].PublicPort != 32780 || d.Ports[2].IP != "::" {
		t.Fatal("complete/allocated/exposed port mappings lost")
	}
	if len(d.Networks) != 1 || d.Networks[0].IPv6Address != "fd00::2" || len(d.Mounts) != 1 {
		t.Fatal("topology or running state lost")
	}
	encoded, _ := json.Marshal(d)
	for _, secret := range []string{"command-private", "argument-private", "env-private", "credential-private", "label-private", "logging-private"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("overview exposed sensitive content")
		}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 6 {
		t.Fatal("quick overview contains unused top-level fields")
	}
	for _, key := range []string{"id", "network_mode", "ports", "networks", "mounts", "counts"} {
		if _, ok := fields[key]; !ok {
			t.Fatal("quick overview field missing")
		}
	}
	if d.Counts.Ports != 3 || d.Counts.Networks != 1 || d.Counts.Mounts != 1 || !d.Ports[1].HostBinding || d.Ports[0].HostBinding {
		t.Fatal("topology totals or binding types lost")
	}

}

func TestContainerOverviewStoppedPortsKeepConfiguredBindings(t *testing.T) {
	a := newAdapter(t, newDockerStub(t, map[string]http.HandlerFunc{"/containers/stopped/json": func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"Id": "stopped", "Config": map[string]any{}, "State": map[string]any{"Status": "exited", "ExitCode": 0, "FinishedAt": "2026-10-12T01:00:00Z"}, "HostConfig": map[string]any{"CpuQuota": 50000, "CpuPeriod": 0, "Memory": 0, "PortBindings": map[string]any{"5432/tcp": []map[string]string{{"HostIp": "127.0.0.1", "HostPort": "5433"}}}}, "NetworkSettings": map[string]any{"Ports": map[string]any{"5432/tcp": nil}}})
	}}))
	d, err := a.ContainerOverview(context.Background(), "stopped")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Ports) != 1 || d.Ports[0].PublicPort != 5433 || !d.Ports[0].HostBinding {
		t.Fatal("stopped port bindings lost")
	}

}

func TestContainerOverviewUnknownHasEmptyTopology(t *testing.T) {
	a := newAdapter(t, newDockerStub(t, map[string]http.HandlerFunc{"/containers/unknown/json": func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]any{}) }}))
	d, err := a.ContainerOverview(context.Background(), "unknown")
	if err != nil {
		t.Fatal(err)
	}
	if d.NetworkMode != nil || d.Counts != (domain.OverviewCounts{}) || d.ID != "unknown" {
		t.Fatal("missing topology became known values")
	}
	encoded, _ := json.Marshal(d)
	if strings.Contains(string(encoded), `"networks":null`) || strings.Contains(string(encoded), `"mounts":null`) {
		t.Fatal("empty topology arrays were null")
	}
}

func TestContainerOverviewBoundsPayloadAndOmitsVolumeHostPaths(t *testing.T) {
	a := newAdapter(t, newDockerStub(t, map[string]http.HandlerFunc{"/containers/many/json": func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"Id": "many", "Config": map[string]any{}, "HostConfig": map[string]any{"PortBindings": map[string]any{"8080/tcp": []map[string]string{{"HostIp": "127.0.0.1", "HostPort": ""}}}},
			"NetworkSettings": map[string]any{"Networks": map[string]any{"a": map[string]string{"IPAddress": "172.20.0.2", "Gateway": "gateway-private", "MacAddress": "mac-private"}, "b": map[string]any{}, "c": map[string]any{}, "d": map[string]any{}, "ignored": nil}, "Ports": map[string]any{"80/tcp": nil, "81/tcp": nil, "82/tcp": nil, "83/tcp": nil}},
			"Mounts":          []map[string]any{{"Type": "volume", "Name": "app_data", "Source": "/var/lib/docker/volumes/internal-private", "Destination": "/a", "RW": true}, {"Type": "bind", "Source": "/host/data", "Destination": "/b", "RW": false}, {"Type": "tmpfs", "Destination": "/c"}, {"Type": "bind", "Source": "/host/extra", "Destination": "/d"}},
		})
	}}))
	d, err := a.ContainerOverview(context.Background(), "many")
	if err != nil {
		t.Fatal(err)
	}
	if d.Counts != (domain.OverviewCounts{Networks: 4, Mounts: 4, Ports: 5}) || len(d.Networks) != 3 || len(d.Mounts) != 3 || len(d.Ports) != 4 {
		t.Fatal("overview payload was not bounded with correct totals")
	}
	if d.Mounts[0].Name != "app_data" || d.Mounts[0].Source != "" || d.Mounts[1].Source != "/host/data" {
		t.Fatal("useful mount paths were removed or internal volume paths exposed")
	}
	encoded, _ := json.Marshal(d)
	for _, unused := range []string{"internal-private", "gateway-private", "mac-private"} {
		if strings.Contains(string(encoded), unused) {
			t.Fatal("unused topology fields exposed")
		}
	}
}

func TestContainerOverviewUnallocatedBindingIsNotAnExposedOnlyPort(t *testing.T) {
	a := newAdapter(t, newDockerStub(t, map[string]http.HandlerFunc{"/containers/pending/json": func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"Id": "pending", "HostConfig": map[string]any{"PortBindings": map[string]any{"8080/tcp": []map[string]string{{"HostIp": "", "HostPort": ""}}}}})
	}}))
	d, err := a.ContainerOverview(context.Background(), "pending")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Ports) != 1 || !d.Ports[0].HostBinding || d.Ports[0].PublicPort != 0 {
		t.Fatal("pending host binding lost")
	}
}
