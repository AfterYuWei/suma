//go:build dockersmoke

package docker

import (
	"context"
	"os"
	"testing"
	"time"

	dockercontainer "github.com/docker/docker/api/types/container"
)

func TestRealDockerImageUsage(t *testing.T) {
	if os.Getenv("SUMA_RUN_DOCKER_SMOKE") != "1" {
		t.Skip("set SUMA_RUN_DOCKER_SMOKE=1 to use the local Docker engine")
	}
	adapter, err := New("unix:///var/run/docker.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	images, err := adapter.ListImages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	containers, err := adapter.client.ContainerList(ctx, dockercontainer.ListOptions{All: true})
	if err != nil {
		t.Fatal(err)
	}
	counts := make(map[string]int64, len(containers))
	for _, container := range containers {
		counts[container.ImageID]++
	}
	used := false
	for _, image := range images {
		used = used || counts[image.ID] > 0
		if image.Containers != counts[image.ID] {
			t.Errorf("image %s has %d references, want %d", image.ID, image.Containers, counts[image.ID])
		}
	}
	if !used {
		t.Skip("no listed images are referenced by containers")
	}
}
