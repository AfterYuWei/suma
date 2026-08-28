package compose

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	containerdomain "github.com/suma/suma/server/internal/container"
	"github.com/suma/suma/server/internal/database"
	projectdomain "github.com/suma/suma/server/internal/project"
	"github.com/suma/suma/server/internal/task"
)

func TestAssessShadowPreviewAllowsIsolatedStatelessProject(t *testing.T) {
	assessment, err := AssessShadowPreview(`services:
  web:
    image: nginx:alpine
    healthcheck:
      test: ["CMD", "nginx", "-t"]
`)
	if err != nil {
		t.Fatal(err)
	}
	if !assessment.Eligible || len(assessment.Reasons) != 0 || len(assessment.Warnings) != 0 {
		t.Fatalf("assessment = %#v", assessment)
	}
}

func TestAssessShadowPreviewRejectsProductionCoupling(t *testing.T) {
	assessment, err := AssessShadowPreview(`services:
  web:
    image: app:v1
    container_name: production-web
    ports: ["8080:80"]
    volumes: ["production:/data"]
    privileged: true
    devices: ["/dev/kvm:/dev/kvm"]
    network_mode: host
volumes:
  production: {}
`)
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Eligible || len(assessment.Reasons) < 6 {
		t.Fatalf("assessment = %#v", assessment)
	}
	joined := strings.Join(assessment.Reasons, " ")
	for _, expected := range []string{"publishes ports", "mounts production data", "container_name", "privileged", "network_mode", "top-level volumes"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing %q in %#v", expected, assessment.Reasons)
		}
	}
}

func TestAssessShadowPreviewWarnsWithoutHealthcheck(t *testing.T) {
	assessment, err := AssessShadowPreview("services:\n  worker:\n    image: worker:v1\n")
	if err != nil {
		t.Fatal(err)
	}
	if !assessment.Eligible || len(assessment.Warnings) != 1 || !strings.Contains(assessment.Warnings[0], "no healthcheck") {
		t.Fatalf("assessment = %#v", assessment)
	}
}

func TestTakeoverDraftCapabilitiesReflectShadowEligibility(t *testing.T) {
	eligible := ProjectTakeoverDraft{Compose: "services:\n  web:\n    image: nginx:alpine\n"}
	setTakeoverDraftCapabilities(&eligible)
	if !projectdomain.HasCapability(eligible.Capabilities, projectdomain.CapabilityTakeover) || !projectdomain.HasCapability(eligible.Capabilities, projectdomain.CapabilityShadowPreview) {
		t.Fatalf("eligible capabilities = %#v", eligible.Capabilities)
	}

	ineligible := ProjectTakeoverDraft{Compose: "services:\n  web:\n    image: nginx:alpine\n    ports: [\"8080:80\"]\n"}
	setTakeoverDraftCapabilities(&ineligible)
	if !projectdomain.HasCapability(ineligible.Capabilities, projectdomain.CapabilityTakeover) || projectdomain.HasCapability(ineligible.Capabilities, projectdomain.CapabilityShadowPreview) {
		t.Fatalf("ineligible capabilities = %#v", ineligible.Capabilities)
	}
}

func TestPrepareShadowComposeReplacesOwnedNetworkName(t *testing.T) {
	content := "name: production\nservices:\n  web:\n    image: nginx:alpine\n    networks: [default]\nnetworks:\n  default:\n    name: production_default\n"
	assessment, err := AssessShadowPreview(content)
	if err != nil || !assessment.Eligible || len(assessment.Warnings) == 0 {
		t.Fatalf("assessment = %#v, err = %v", assessment, err)
	}
	isolated, err := prepareShadowCompose(content)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(isolated, "production_default") || strings.Contains(isolated, "name: production") {
		t.Fatalf("production identity remains in preview:\n%s", isolated)
	}
}

func TestShadowProjectNameIsIsolatedAndBounded(t *testing.T) {
	name := shadowProjectName("Shop / Production with a very long native project name", 12345)
	if !strings.HasPrefix(name, "suma-preview-") || len(name) > 64 || strings.ContainsAny(name, " /") || name != strings.ToLower(name) {
		t.Fatalf("preview name = %q", name)
	}
}

type shadowRunner struct {
	Runner
	started chan struct{}
	cleaned chan struct{}
	start   sync.Once
	clean   sync.Once
}

func (runner *shadowRunner) ValidateRelease(context.Context, ExecutionSpec, io.Writer) error {
	return nil
}
func (runner *shadowRunner) UpRelease(context.Context, ExecutionSpec, int, io.Writer) error {
	runner.start.Do(func() { close(runner.started) })
	return nil
}
func (runner *shadowRunner) ForceDownRelease(context.Context, ExecutionSpec, bool, io.Writer) error {
	runner.clean.Do(func() { close(runner.cleaned) })
	return nil
}
func (runner *shadowRunner) PS(context.Context, ExecutionSpec, io.Writer) (string, error) {
	return `[{"State":"running"}]`, nil
}
func (runner *shadowRunner) LogsRelease(_ context.Context, _ ExecutionSpec, output io.Writer) error {
	_, _ = io.WriteString(output, "ready\n")
	return nil
}

func TestShadowPreviewUsesTemporaryProjectAndCleanupTask(t *testing.T) {
	root := t.TempDir()
	db, err := database.Open(filepath.Join(root, "suma.db"))
	if err != nil {
		t.Fatal(err)
	}
	runner := &shadowRunner{started: make(chan struct{}), cleaned: make(chan struct{})}
	containers := observableContainers{
		staticContainers: staticContainers{rows: []containerdomain.Summary{{ID: "web", Labels: map[string]string{ProjectLabel: "shop"}}}},
		snapshot:         RuntimeProjectSnapshot{ProjectName: "shop", Containers: []RuntimeContainer{{ID: "web", Service: "web", ImageInspectOK: true, Config: RuntimeConfig{Image: "nginx:alpine"}}}},
	}
	service := &Service{root: filepath.Join(root, "compose"), runner: runner, tasks: task.NewService(db), containers: containers, nodeID: "tcp-node", nodeName: "TCP", instanceID: "test", projectLocks: &sync.Map{}}
	draft, err := service.BuildTakeoverDraft(context.Background(), "shop")
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.StartShadowPreview(context.Background(), "shop", draft.Fingerprint, draft.Compose, draft.Environment)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.started:
	case <-time.After(3 * time.Second):
		t.Fatal("preview did not start")
	}
	if !strings.HasPrefix(session.PreviewProject, "suma-preview-shop-") || session.PreviewProject == "shop" {
		t.Fatalf("session = %#v", session)
	}
	status, err := service.ShadowPreviewStatus(context.Background(), session.SessionID)
	if err != nil || !strings.Contains(status.Containers, "running") || !strings.Contains(status.Logs, "ready") {
		t.Fatalf("status = %#v, err = %v", status, err)
	}
	if _, err := service.StopShadowPreview(session.SessionID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.cleaned:
	case <-time.After(3 * time.Second):
		t.Fatal("preview was not cleaned")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, statErr := os.Stat(service.shadowSessionPath(session.SessionID))
		if os.IsNotExist(statErr) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("preview directory remains: %v", statErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
