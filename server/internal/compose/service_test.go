package compose

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	containerdomain "github.com/suma/suma/server/internal/container"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/task"
)

func TestCreateComposeDoesNotCreateDeliveryProject(t *testing.T) {
	root := t.TempDir()
	db, err := database.Open(filepath.Join(root, "suma.db"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(db, filepath.Join(root, "compose"), nil, nil, emptyContainers{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(context.Background(), "production", "services: {}\n", ""); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&database.DeliveryProject{}).Where("name = ?", "production").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("Compose creation also created %d delivery projects", count)
	}
}

type emptyContainers struct{ containerdomain.Service }

func (emptyContainers) List(context.Context) ([]containerdomain.Summary, error) { return nil, nil }

func TestDecorateMatchesComposeWorkingDirectoryWhenRuntimeNameDiffers(t *testing.T) {
	projectPath := filepath.Join(t.TempDir(), "SUMA.Project")
	project := Project{Name: "SUMA.Project", Path: projectPath, Status: "stopped"}
	containers := []containerdomain.Summary{
		{ID: "matching", State: "running", Labels: map[string]string{
			"com.docker.compose.project":             "custom-runtime-name",
			"com.docker.compose.project.working_dir": projectPath,
			"com.docker.compose.service":             "web",
		}},
		{ID: "other", State: "running", Labels: map[string]string{
			"com.docker.compose.project":             project.Name,
			"com.docker.compose.project.working_dir": filepath.Join(t.TempDir(), "other"),
			"com.docker.compose.service":             "worker",
		}},
	}

	decorated := decorate(project, containers)
	if decorated.Status != "running" || decorated.Services != 1 || decorated.Containers != 1 {
		t.Fatalf("decorated project = %#v", decorated)
	}
}

func TestServicesMatchesComposeWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	db, err := database.Open(filepath.Join(root, "suma.db"))
	if err != nil {
		t.Fatal(err)
	}
	projectPath := filepath.Join(root, "production")
	project := database.ComposeProject{Name: "production", Path: projectPath}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	container := containerdomain.Summary{ID: "container-id", Labels: map[string]string{
		"com.docker.compose.project":             "overridden-name",
		"com.docker.compose.project.working_dir": projectPath,
	}}
	service := &Service{db: db, containers: staticContainers{rows: []containerdomain.Summary{container}}}
	rows, err := service.Services(context.Background(), project.Name)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != container.ID {
		t.Fatalf("services = %#v", rows)
	}
}

type staticContainers struct {
	containerdomain.Service
	rows []containerdomain.Summary
}

func (service staticContainers) List(context.Context) ([]containerdomain.Summary, error) {
	return service.rows, nil
}

func TestReleaseEnvironmentDoesNotForwardSumaSecrets(t *testing.T) {
	t.Setenv("SUMA_SECRET_KEY_FILE", "/sensitive/key")
	t.Setenv("SUMA_GIT_PASSWORD", "sensitive-token")
	t.Setenv("DOCKER_HOST", "unix:///var/run/docker.sock")
	values := strings.Join(releaseEnvironment(), "\n")
	if strings.Contains(values, "SUMA_SECRET") || strings.Contains(values, "SUMA_GIT_PASSWORD") {
		t.Fatalf("release environment leaked SUMA secrets: %s", values)
	}
	if !strings.Contains(values, "DOCKER_HOST=unix:///var/run/docker.sock") {
		t.Fatalf("release environment omitted Docker connectivity: %s", values)
	}
}

func TestForceRemoveCanPreserveProjectVolumes(t *testing.T) {
	root := t.TempDir()
	db, err := database.Open(filepath.Join(root, "suma.db"))
	if err != nil {
		t.Fatal(err)
	}
	projectPath := filepath.Join(root, "managed")
	if err := os.MkdirAll(projectPath, 0o750); err != nil {
		t.Fatal(err)
	}
	project := database.ComposeProject{Name: "managed", Path: projectPath}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	runner := &forceRemoveRunner{}
	service := &Service{db: db, root: root, runner: runner}
	if err := service.ForceRemove(context.Background(), project.Name, true); err != nil {
		t.Fatal(err)
	}
	if !runner.managed || !runner.preserveVolumes {
		t.Fatalf("force removal did not preserve volumes: %#v", runner)
	}
}

type forceRemoveRunner struct {
	Runner
	managed         bool
	preserveVolumes bool
}

func TestBatchActionReturnsPerProjectTasks(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "suma.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"api", "worker"} {
		if err := db.Create(&database.ComposeProject{Name: name, Path: filepath.Join(t.TempDir(), name)}).Error; err != nil {
			t.Fatal(err)
		}
	}
	service := &Service{db: db, runner: batchRunner{}, tasks: task.NewService(db)}
	results := service.BatchAction(context.Background(), []string{"api", "worker", "missing"}, "start")
	if len(results) != 3 || !results[0].Success || results[0].TaskID == "" || !results[1].Success || results[1].TaskID == "" || results[2].Success {
		t.Fatalf("batch results = %#v", results)
	}
}

type batchRunner struct{ Runner }

func (batchRunner) Start(context.Context, string, io.Writer) error { return nil }

func (r *forceRemoveRunner) ForceDown(_ context.Context, _ string, preserveVolumes bool, _ io.Writer) error {
	r.managed = true
	r.preserveVolumes = preserveVolumes
	return nil
}
