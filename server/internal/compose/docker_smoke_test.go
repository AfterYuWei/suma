//go:build dockersmoke

package compose_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	compose "github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/database"
	dockerruntime "github.com/suma/suma/server/internal/docker"
	"github.com/suma/suma/server/internal/task"
	"gorm.io/gorm"
)

// TestRealDockerProjectTakeover is opt-in because it creates two temporary
// Compose Projects on the local Docker engine. It covers multi-file mapped
// source, whole-Project runtime fallback, scale aggregation, isolated preview,
// cleanup, and takeover without deployment.
func TestRealDockerProjectTakeover(t *testing.T) {
	if os.Getenv("SUMA_RUN_DOCKER_SMOKE") != "1" {
		t.Skip("set SUMA_RUN_DOCKER_SMOKE=1 to use the local Docker engine")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	root := t.TempDir()
	db, err := database.Open(filepath.Join(root, "suma.db"))
	if err != nil {
		t.Fatal(err)
	}
	runner, err := compose.NewRunner("docker compose")
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := dockerruntime.New("unix:///var/run/docker.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	name := fmt.Sprintf("suma-takeover-smoke-%d", time.Now().UnixNano())
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(source, 0o750); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(source, "compose.yml")
	override := filepath.Join(source, "compose.override.yml")
	if err := os.WriteFile(base, []byte("services:\n  web:\n    image: nginx:alpine\n    environment:\n      SUMA_SMOKE: mapped\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(override, []byte("services:\n  web:\n    scale: 2\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	spec := compose.ExecutionSpec{ProjectName: name, ProjectDir: source, Files: []string{base, override}}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		_ = runner.ForceDownRelease(cleanupCtx, spec, false, io.Discard)
	}()
	if err := runner.UpRelease(ctx, spec, 60, io.Discard); err != nil {
		t.Fatal(err)
	}
	service, err := compose.NewService(db, filepath.Join(root, "managed"), runner, task.NewService(db), adapter)
	if err != nil {
		t.Fatal(err)
	}
	mapped, err := service.BuildTakeoverDraft(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	if mapped.Source != "mapped" || mapped.Confidence != "high" || len(mapped.Observation.Services) != 1 || len(mapped.Observation.Services[0].Instances) != 2 {
		t.Fatalf("mapped draft = %#v", mapped)
	}
	originalIDs := instanceIDs(mapped)
	missing := base + ".missing"
	if err := os.Rename(base, missing); err != nil {
		t.Fatal(err)
	}
	runtimeDraft, err := service.BuildTakeoverDraft(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	if runtimeDraft.Source != "runtime" || !containsWarning(runtimeDraft.Warnings, "rebuilt from runtime") {
		t.Fatalf("runtime fallback = %#v", runtimeDraft)
	}
	if err := os.Rename(missing, base); err != nil {
		t.Fatal(err)
	}
	mapped, err = service.BuildTakeoverDraft(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	assessment, err := compose.AssessShadowPreview(mapped.Compose)
	if err != nil || !assessment.Eligible {
		t.Fatalf("assessment = %#v, err = %v", assessment, err)
	}
	session, err := service.StartShadowPreview(ctx, name, mapped.Fingerprint, mapped.Compose, mapped.Environment)
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, ctx, db, session.Task.ID, task.StatusSuccess)
	status, err := service.ShadowPreviewStatus(ctx, session.SessionID)
	if err != nil || !strings.Contains(status.Containers, "running") {
		t.Fatalf("preview status = %#v, err = %v", status, err)
	}
	cleanup, err := service.StopShadowPreview(session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, ctx, db, cleanup.ID, task.StatusSuccess)
	managed, err := service.Takeover(ctx, name, compose.TakeoverInput{Fingerprint: mapped.Fingerprint, ConfirmationName: name, Compose: mapped.Compose, Environment: mapped.Environment})
	if err != nil {
		t.Fatal(err)
	}
	if !managed.Managed || managed.Metadata == nil || managed.Metadata.Origin != "takeover" || managed.Metadata.LastDeployedAt != nil {
		t.Fatalf("managed Project = %#v", managed)
	}
	after, err := service.Services(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	afterIDs := make([]string, 0, len(after))
	for _, container := range after {
		afterIDs = append(afterIDs, container.ID)
	}
	sort.Strings(afterIDs)
	if strings.Join(originalIDs, ",") != strings.Join(afterIDs, ",") {
		t.Fatalf("takeover changed runtime containers: before=%v after=%v", originalIDs, afterIDs)
	}
}

// TestRealDockerExternalProjectCleanup verifies the destructive boundary with
// uniquely named disposable Compose Projects. The default path preserves the
// named volume; the explicit high-risk path removes it. Both preserve the bind
// source directory and remove only Project-labelled runtime resources.
func TestRealDockerExternalProjectCleanup(t *testing.T) {
	if os.Getenv("SUMA_RUN_DOCKER_SMOKE") != "1" {
		t.Skip("set SUMA_RUN_DOCKER_SMOKE=1 to use the local Docker engine")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	root := t.TempDir()
	db, err := database.Open(filepath.Join(root, "cleanup.db"))
	if err != nil {
		t.Fatal(err)
	}
	runner, err := compose.NewRunner("docker compose")
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := dockerruntime.New("unix:///var/run/docker.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	service, err := compose.NewService(db, filepath.Join(root, "managed"), runner, task.NewService(db), adapter)
	if err != nil {
		t.Fatal(err)
	}

	run := func(removeVolumes bool) {
		t.Helper()
		name := fmt.Sprintf("suma-cleanup-smoke-%t-%d", removeVolumes, time.Now().UnixNano())
		projectDir := filepath.Join(root, name)
		bindDir := filepath.Join(projectDir, "bind-source")
		if err := os.MkdirAll(bindDir, 0o750); err != nil {
			t.Fatal(err)
		}
		marker := filepath.Join(bindDir, "keep")
		if err := os.WriteFile(marker, []byte("keep"), 0o640); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(projectDir, "compose.yml")
		content := fmt.Sprintf("services:\n  web:\n    image: nginx:alpine\n    volumes:\n      - %s:/suma-bind:ro\n      - data:/suma-data\nvolumes:\n  data: {}\n", bindDir)
		if err := os.WriteFile(file, []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
		spec := compose.ExecutionSpec{ProjectName: name, ProjectDir: projectDir, Files: []string{file}}
		defer func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
			defer cleanupCancel()
			_ = runner.ForceDownRelease(cleanupCtx, spec, false, io.Discard)
			_ = adapter.RemoveVolume(cleanupCtx, name+"_data")
		}()
		if err := runner.UpRelease(ctx, spec, 60, io.Discard); err != nil {
			t.Fatal(err)
		}
		row, err := service.CleanupExternalProject(ctx, name, name, removeVolumes)
		if err != nil {
			t.Fatal(err)
		}
		waitTask(t, ctx, db, row.ID, task.StatusSuccess)
		if _, err := os.Stat(marker); err != nil {
			t.Fatalf("cleanup changed bind source: %v", err)
		}
		containers, err := adapter.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, container := range containers {
			if container.Labels[compose.ProjectLabel] == name {
				t.Fatalf("Project container remains after cleanup: %s", container.ID)
			}
		}
		networks, err := adapter.ListNetworks(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, network := range networks {
			if network.Labels[compose.ProjectLabel] == name {
				t.Fatalf("Project network remains after cleanup: %s", network.Name)
			}
		}
		_, volumeErr := adapter.InspectVolume(ctx, name+"_data")
		if removeVolumes && volumeErr == nil {
			t.Fatal("high-risk cleanup preserved the Project-owned named volume")
		}
		if !removeVolumes && volumeErr != nil {
			t.Fatalf("default cleanup removed the named volume: %v", volumeErr)
		}
	}

	run(false)
	run(true)
}

// TestRealDockerTCPProjectTakeover verifies that an mTLS Docker TCP node never
// treats Compose label paths as locally readable source, even when the control
// process happens to have a same-named local path.
func TestRealDockerTCPProjectTakeover(t *testing.T) {
	host := os.Getenv("SUMA_SMOKE_TCP_HOST")
	certDir := os.Getenv("SUMA_SMOKE_TLS_DIR")
	if host == "" || certDir == "" {
		t.Skip("set SUMA_SMOKE_TCP_HOST and SUMA_SMOKE_TLS_DIR for the mTLS Docker smoke test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	read := func(paths ...string) string {
		for _, name := range paths {
			value, err := os.ReadFile(filepath.Join(certDir, name))
			if err == nil {
				return string(value)
			}
		}
		t.Fatalf("missing TLS material in %s: %v", certDir, paths)
		return ""
	}
	ca := read("ca.pem", filepath.Join("client", "ca.pem"))
	certificate := read("cert.pem", filepath.Join("client", "cert.pem"))
	privateKey := read("key.pem", filepath.Join("client", "key.pem"))
	adapter, err := dockerruntime.NewTLS(host, ca, certificate, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	if err := adapter.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	baseRunner, err := compose.NewRunner("docker compose")
	if err != nil {
		t.Fatal(err)
	}
	runner := baseRunner.ForTarget(compose.Target{NodeID: "tcp-smoke", NodeName: "TCP Smoke", Host: host, TLSRequired: true, CA: ca, Certificate: certificate, PrivateKey: privateKey})
	root := t.TempDir()
	db, err := database.Open(filepath.Join(root, "suma.db"))
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(source, 0o750); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(source, "compose.yml")
	if err := os.WriteFile(file, []byte("services:\n  web:\n    image: nginx:alpine\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("suma-tcp-takeover-%d", time.Now().UnixNano())
	spec := compose.ExecutionSpec{ProjectName: name, ProjectDir: source, Files: []string{file}}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		_ = runner.ForceDownRelease(cleanupCtx, spec, false, io.Discard)
	}()
	if err := runner.UpRelease(ctx, spec, 60, io.Discard); err != nil {
		t.Fatal(err)
	}
	baseService, err := compose.NewService(db, filepath.Join(root, "managed"), runner, task.NewService(db), adapter)
	if err != nil {
		t.Fatal(err)
	}
	service := baseService.ForNode("tcp-smoke", "TCP Smoke", runner, adapter, false)
	draft, err := service.BuildTakeoverDraft(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Source != "runtime" {
		t.Fatalf("TCP node unexpectedly read label source: %#v", draft)
	}
	validation := filepath.Join(root, "validation")
	if err := os.MkdirAll(validation, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(validation, "compose.yml"), []byte(draft.Compose), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(validation, ".env"), []byte(draft.Environment), 0o600); err != nil {
		t.Fatal(err)
	}
	var validationOutput strings.Builder
	if err := runner.Validate(ctx, validation, &validationOutput); err != nil {
		t.Fatalf("runtime draft validation: %v: %s", err, validationOutput.String())
	}
	before := instanceIDs(draft)
	managed, err := service.Takeover(ctx, name, compose.TakeoverInput{Fingerprint: draft.Fingerprint, ConfirmationName: name, Compose: draft.Compose, Environment: draft.Environment})
	if err != nil {
		t.Fatal(err)
	}
	if !managed.Managed || managed.Metadata == nil || managed.Metadata.TakeoverSource != "runtime" {
		t.Fatalf("managed Project = %#v", managed)
	}
	after, err := service.Services(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	afterIDs := make([]string, 0, len(after))
	for _, container := range after {
		afterIDs = append(afterIDs, container.ID)
	}
	sort.Strings(afterIDs)
	if strings.Join(before, ",") != strings.Join(afterIDs, ",") {
		t.Fatalf("TCP takeover changed containers: before=%v after=%v", before, afterIDs)
	}
}

func instanceIDs(draft compose.ProjectTakeoverDraft) []string {
	values := []string{}
	for _, service := range draft.Observation.Services {
		for _, instance := range service.Instances {
			values = append(values, instance.ContainerID)
		}
	}
	sort.Strings(values)
	return values
}

func containsWarning(values []string, expected string) bool {
	for _, value := range values {
		if strings.Contains(value, expected) {
			return true
		}
	}
	return false
}

func waitTask(t *testing.T, ctx context.Context, db *gorm.DB, id, wanted string) {
	t.Helper()
	for {
		var row database.Task
		if err := db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
			t.Fatal(err)
		}
		if row.Status == wanted {
			return
		}
		if row.Status == task.StatusFailed || row.Status == task.StatusCanceled {
			t.Fatalf("task %s: %s", row.Status, row.Message)
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}
