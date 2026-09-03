package compose

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	containerdomain "github.com/suma/suma/server/internal/container"
	"github.com/suma/suma/server/internal/database"
	projectdomain "github.com/suma/suma/server/internal/project"
	"github.com/suma/suma/server/internal/task"
	"gorm.io/gorm"
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

func TestListSummariesOmitsComposeContentAndSourcePaths(t *testing.T) {
	root := t.TempDir()
	db, err := database.Open(filepath.Join(root, "suma.db"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(db, filepath.Join(root, "compose"), nil, nil, emptyContainers{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(context.Background(), "production", "services:\n  web:\n    image: example:v1\n    environment:\n      PASSWORD: secret-value\n", "PASSWORD=secret-value\n"); err != nil {
		t.Fatal(err)
	}

	rows, err := service.ListSummaries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "production" {
		t.Fatalf("summaries = %#v", rows)
	}
	payload, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	for _, forbidden := range []string{"secret-value", "example:v1", `"compose":`, `"environment":`, `"path":`, `"config_files":`} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("summary payload contains %q: %s", forbidden, text)
		}
	}
}

type emptyContainers struct{ containerdomain.Service }

func (emptyContainers) List(context.Context) ([]containerdomain.Summary, error) { return nil, nil }

func TestDecorateMatchesComposeProjectLabel(t *testing.T) {
	projectPath := filepath.Join(t.TempDir(), "SUMA.Project")
	project := Project{Summary: projectdomain.ComposeSummary("local", "SUMA.Project", "external", "stopped", false), Path: projectPath}
	containers := []containerdomain.Summary{
		{ID: "matching", State: "running", Labels: map[string]string{
			"com.docker.compose.project":             project.Name,
			"com.docker.compose.project.working_dir": projectPath,
			"com.docker.compose.service":             "web",
		}},
		{ID: "other", State: "running", Labels: map[string]string{
			"com.docker.compose.project":             "other-project",
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
	projectName := "production"
	container := containerdomain.Summary{ID: "container-id", Labels: map[string]string{
		"com.docker.compose.project": projectName,
	}}
	service := &Service{root: t.TempDir(), containers: staticContainers{rows: []containerdomain.Summary{container}}}
	rows, err := service.Services(context.Background(), projectName)
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

type cleanupCall struct {
	projectName  string
	removeVolume bool
}

type cleanupContainers struct {
	staticContainers
	calls chan cleanupCall
}

func (service *cleanupContainers) CleanupComposeProject(_ context.Context, name string, removeVolumes bool, report task.Reporter) error {
	service.calls <- cleanupCall{projectName: name, removeVolume: removeVolumes}
	report(100, "clean")
	return nil
}

func TestCleanupExternalProjectRequiresExactNameAndRunsAsTask(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "suma.db"))
	if err != nil {
		t.Fatal(err)
	}
	cleaner := &cleanupContainers{
		staticContainers: staticContainers{rows: []containerdomain.Summary{{ID: "web", Labels: map[string]string{composeProjectLabel: "blocked", composeServiceLabel: "web"}}}},
		calls:            make(chan cleanupCall, 1),
	}
	service, err := NewService(db, filepath.Join(t.TempDir(), "compose"), nil, task.NewService(db), cleaner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CleanupExternalProject(context.Background(), "blocked", "wrong", true); err == nil {
		t.Fatal("cleanup accepted an incorrect Project confirmation")
	}
	row, err := service.CleanupExternalProject(context.Background(), "blocked", "blocked", true)
	if err != nil {
		t.Fatal(err)
	}
	waitForTaskCompletion(t, db, row.ID)
	select {
	case call := <-cleaner.calls:
		if call.projectName != "blocked" || !call.removeVolume {
			t.Fatalf("cleanup call = %#v", call)
		}
	default:
		t.Fatal("runtime cleanup was not called")
	}
	if _, err := service.Create(context.Background(), "managed", "services: {}\n", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CleanupExternalProject(context.Background(), "managed", "managed", false); err == nil {
		t.Fatal("external cleanup accepted a managed Project")
	}
}

type observableContainers struct {
	staticContainers
	snapshot RuntimeProjectSnapshot
}

func (service observableContainers) InspectComposeProject(context.Context, string) (RuntimeProjectSnapshot, error) {
	return service.snapshot, nil
}

func TestObserveBuildsProjectAggregateThroughRuntimeInspector(t *testing.T) {
	containers := observableContainers{
		staticContainers: staticContainers{rows: []containerdomain.Summary{{ID: "one", Labels: map[string]string{composeProjectLabel: "shop"}}}},
		snapshot:         RuntimeProjectSnapshot{ProjectName: "shop", Containers: []RuntimeContainer{{ID: "one", Service: "web", Config: RuntimeConfig{Image: "web:v1"}}}},
	}
	service := &Service{root: t.TempDir(), containers: containers}
	value, err := service.Observe(context.Background(), "shop")
	if err != nil {
		t.Fatal(err)
	}
	if value.Name != "shop" || len(value.Services) != 1 || value.Services[0].Name != "web" {
		t.Fatalf("observation = %#v", value)
	}
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
	projectPath := filepath.Join(root, "managed")
	if err := os.MkdirAll(projectPath, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectPath, "compose.yml"), []byte("services: {}\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	runner := &forceRemoveRunner{}
	service := &Service{root: root, runner: runner}
	if err := service.ForceRemove(context.Background(), "managed", true); err != nil {
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
	root := t.TempDir()
	for _, name := range []string{"api", "worker"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(path, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "compose.yml"), []byte("services: {}\n"), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	service := &Service{root: root, runner: batchRunner{}, tasks: task.NewService(db)}
	results := service.BatchAction(context.Background(), []string{"api", "worker", "missing"}, "up")
	if len(results) != 3 || !results[0].Success || results[0].TaskID == "" || !results[1].Success || results[1].TaskID == "" || results[2].Success {
		t.Fatalf("batch results = %#v", results)
	}
	for _, result := range results[:2] {
		waitForTaskCompletion(t, db, result.TaskID)
	}
}

func TestReportWriterStreamsCarriageReturnAndPercentageProgress(t *testing.T) {
	type update struct {
		progress int
		message  string
	}
	var updates []update
	writer := newReportWriter(func(progress int, message string) {
		updates = append(updates, update{progress: progress, message: message})
	}, 5, 95)

	if _, err := writer.Write([]byte("image Pulling\rimage Downloading 40%\rfinal fragment")); err != nil {
		t.Fatal(err)
	}
	writer.Flush()

	if len(updates) != 3 {
		t.Fatalf("updates = %#v", updates)
	}
	if updates[0].message != "image Pulling" || updates[0].progress <= 5 {
		t.Fatalf("first update = %#v", updates[0])
	}
	if updates[1].message != "image Downloading 40%" || updates[1].progress != 41 {
		t.Fatalf("percentage update = %#v", updates[1])
	}
	if updates[2].message != "final fragment" || updates[2].progress <= updates[1].progress {
		t.Fatalf("flushed update = %#v", updates[2])
	}
}

func waitForTaskCompletion(t *testing.T, db *gorm.DB, id string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var row database.Task
		if err := db.First(&row, "id = ?", id).Error; err != nil {
			t.Fatal(err)
		}
		if row.Status == task.StatusSuccess {
			return
		}
		if row.Status == task.StatusFailed || row.Status == task.StatusCanceled {
			t.Fatalf("task %s ended with %s: %s", id, row.Status, row.Message)
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s did not finish: %#v", id, row)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestListDiscoversComposeProjectsFromContainerLabels(t *testing.T) {
	root := t.TempDir()
	containers := staticContainers{rows: []containerdomain.Summary{
		{ID: "web", State: "running", Created: time.Unix(200, 0), Labels: map[string]string{
			composeProjectLabel: "external", composeServiceLabel: "web",
			composeWorkingDirLabel: "/srv/external", composeConfigFilesLabel: "/srv/external/compose.yml",
		}},
		{ID: "worker", State: "exited", Created: time.Unix(100, 0), Labels: map[string]string{
			composeProjectLabel: "external", composeServiceLabel: "worker",
		}},
	}}
	service := &Service{root: filepath.Join(root, "compose"), containers: containers}
	rows, err := service.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "external" || rows[0].Source != "external" || rows[0].CanManage {
		t.Fatalf("projects = %#v", rows)
	}
	if rows[0].Status != "degraded" || rows[0].Services != 2 || rows[0].Containers != 2 {
		t.Fatalf("runtime decoration = %#v", rows[0])
	}
}

func TestListKeepsManagedProjectWithoutContainers(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "idle")
	if err := os.MkdirAll(path, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "compose.yml"), []byte("services: {}\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	service := &Service{root: root, containers: emptyContainers{}}
	rows, err := service.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "idle" || !rows[0].CanManage || rows[0].Source != "managed" {
		t.Fatalf("projects = %#v", rows)
	}
	if rows[0].Metadata == nil || rows[0].Metadata.Origin != "legacy" {
		t.Fatalf("legacy metadata = %#v", rows[0].Metadata)
	}
}

func TestCreateWritesManagedProjectIdentityMetadata(t *testing.T) {
	root := t.TempDir()
	service := &Service{root: root, containers: emptyContainers{}, nodeID: "node-a"}
	project, err := service.Create(context.Background(), "shop", "services: {}\n", "")
	if err != nil {
		t.Fatal(err)
	}
	if project.Backend != projectdomain.BackendCompose || project.Scope.Kind != projectdomain.ScopeEngine || project.Scope.ID != "node-a" || !project.Managed {
		t.Fatalf("Project identity = %#v", project.Summary)
	}
	if project.Metadata == nil || project.Metadata.Origin != "created" || project.Metadata.NativeName != "shop" {
		t.Fatalf("metadata = %#v", project.Metadata)
	}
	if _, err := os.Stat(filepath.Join(root, "node-a", "shop", ".suma", "project.json")); err != nil {
		t.Fatal(err)
	}
}

type batchRunner struct{ Runner }

func (batchRunner) Up(context.Context, string, io.Writer) error { return nil }

type validateRunner struct{ Runner }

func (validateRunner) Validate(context.Context, string, io.Writer) error { return nil }

func (r *forceRemoveRunner) ForceDown(_ context.Context, _ string, preserveVolumes bool, _ io.Writer) error {
	r.managed = true
	r.preserveVolumes = preserveVolumes
	return nil
}
