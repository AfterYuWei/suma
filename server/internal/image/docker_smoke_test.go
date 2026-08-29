//go:build dockersmoke

package image_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/database"
	dockeradapter "github.com/suma/suma/server/internal/docker"
	image "github.com/suma/suma/server/internal/image"
	"github.com/suma/suma/server/internal/task"
)

func TestRealDockerPullPersistsLayerProgress(t *testing.T) {
	if os.Getenv("SUMA_RUN_DOCKER_SMOKE") != "1" {
		t.Skip("set SUMA_RUN_DOCKER_SMOKE=1 to use the local Docker engine")
	}
	adapter, err := dockeradapter.New("unix:///var/run/docker.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := adapter.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(filepath.Join(t.TempDir(), "suma.db"))
	if err != nil {
		t.Fatal(err)
	}
	tasks := task.NewService(db)
	service := image.NewService(adapter, tasks)
	reference := strings.TrimSpace(os.Getenv("SUMA_SMOKE_LAYER_IMAGE"))
	if reference == "" {
		reference = "alpine:3.20.3"
	}
	_, inspectErr := adapter.InspectImage(ctx, reference)
	if inspectErr != nil {
		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cleanupCancel()
			_ = adapter.RemoveImage(cleanupCtx, reference, false)
		})
	}
	row, err := service.Pull(reference)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		var current database.Task
		if err := db.First(&current, "id = ?", row.ID).Error; err != nil {
			t.Fatal(err)
		}
		if current.Status == task.StatusSuccess {
			steps, err := tasks.Steps(context.Background(), row.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(steps) == 0 {
				t.Fatalf("Docker returned no Layer events for %s", reference)
			}
			for _, step := range steps {
				if step.Progress != 100 {
					t.Fatalf("Layer did not complete: %+v", step)
				}
			}
			return
		}
		if current.Status == task.StatusFailed || current.Status == task.StatusCanceled {
			t.Fatalf("pull ended with %s: %s", current.Status, current.Message)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("timed out waiting for real Docker pull")
}
