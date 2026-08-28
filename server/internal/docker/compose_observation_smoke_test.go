//go:build dockersmoke

package docker

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	dockercontainer "github.com/docker/docker/api/types/container"
	dockerimage "github.com/docker/docker/api/types/image"
	compose "github.com/suma/suma/server/internal/compose"
)

// TestRealDockerComposeObservationScenarios creates synthetic Compose-labelled
// containers through the adapter boundary. It verifies real Engine inspection
// for config variants, partial recreate evidence, one-off containers, and
// containers that cannot be assigned to a Service. Source-config orphan and
// stopped-Service correlation remains covered by deterministic Compose tests.
func TestRealDockerComposeObservationScenarios(t *testing.T) {
	if os.Getenv("SUMA_RUN_DOCKER_SMOKE") != "1" {
		t.Skip("set SUMA_RUN_DOCKER_SMOKE=1 to use the local Docker engine")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	adapter, err := New("unix:///var/run/docker.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	if err := adapter.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	image := strings.TrimSpace(os.Getenv("SUMA_SMOKE_IMAGE"))
	if image == "" {
		image = "nginx:alpine"
	}
	if _, err := adapter.client.ImageInspect(ctx, image); err != nil {
		stream, pullErr := adapter.client.ImagePull(ctx, image, dockerimage.PullOptions{})
		if pullErr != nil {
			t.Fatalf("pull smoke image %s: %v", image, pullErr)
		}
		_, copyErr := io.Copy(io.Discard, stream)
		closeErr := stream.Close()
		if copyErr != nil || closeErr != nil {
			t.Fatalf("read smoke image pull: copy=%v close=%v", copyErr, closeErr)
		}
	}

	projectName := fmt.Sprintf("suma-observation-smoke-%d", time.Now().UnixNano())
	created := []string{}
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		for _, id := range created {
			_ = adapter.client.ContainerRemove(cleanupContext, id, dockercontainer.RemoveOptions{Force: true, RemoveVolumes: true})
		}
	})
	create := func(suffix, service, configHash string, oneOff bool, command, environment []string) {
		t.Helper()
		labels := map[string]string{compose.ProjectLabel: projectName, compose.ConfigHashLabel: configHash}
		if service != "" {
			labels[compose.ServiceLabel] = service
		}
		if oneOff {
			labels[compose.OneOffLabel] = "True"
		}
		labels[compose.ContainerNumberLabel] = fmt.Sprintf("%d", len(created)+1)
		response, createErr := adapter.client.ContainerCreate(ctx, &dockercontainer.Config{Image: image, Cmd: command, Env: environment, Labels: labels}, nil, nil, nil, projectName+"-"+suffix)
		if createErr != nil {
			t.Fatalf("create %s: %v", suffix, createErr)
		}
		created = append(created, response.ID)
	}
	create("web-old", "web", "hash-old", false, []string{"nginx", "-g", "daemon off;"}, []string{"MODE=old"})
	create("web-new", "web", "hash-new", false, []string{"nginx", "-v"}, []string{"MODE=new"})
	create("job-run", "job", "hash-job", true, []string{"nginx", "-v"}, nil)
	create("unassigned", "", "hash-orphan", false, []string{"nginx", "-v"}, nil)

	snapshot, err := adapter.InspectComposeProject(ctx, projectName)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Containers) != 4 {
		t.Fatalf("snapshot containers = %#v", snapshot.Containers)
	}
	observed := compose.ObserveRuntimeProject(snapshot)
	if len(observed.Services) != 1 || observed.Services[0].Name != "web" {
		t.Fatalf("services = %#v", observed.Services)
	}
	service := observed.Services[0]
	if service.DriftStatus != "runtime_drift" || len(service.ConfigVariants) != 2 || !containsSmokeValue(service.DriftReasons, "partial_recreate") {
		t.Fatalf("drift = %#v", service)
	}
	if !containsSmokeValue(service.DriftFields, "command") || !containsSmokeValue(service.DriftFields, "environment") {
		t.Fatalf("drift fields = %#v", service.DriftFields)
	}
	if len(observed.OneOffContainers) != 1 || !observed.OneOffContainers[0].OneOff {
		t.Fatalf("one-off containers = %#v", observed.OneOffContainers)
	}
	if len(observed.OrphanContainers) != 1 || observed.OrphanContainers[0].ContainerID == "" {
		t.Fatalf("unassigned containers = %#v", observed.OrphanContainers)
	}
}

func containsSmokeValue(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
