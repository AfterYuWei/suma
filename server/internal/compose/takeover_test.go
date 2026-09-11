package compose

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	containerdomain "github.com/suma/suma/server/internal/container"
)

type takeoverRunner struct {
	Runner
	validateError  error
	validateOutput string
	validated      bool
	deployed       bool
}

func (runner *takeoverRunner) Validate(_ context.Context, _ string, output io.Writer) error {
	runner.validated = true
	_, _ = io.WriteString(output, runner.validateOutput)
	return runner.validateError
}

func (runner *takeoverRunner) Up(context.Context, string, io.Writer) error {
	runner.deployed = true
	return nil
}

func takeoverHarness(t *testing.T, runner *takeoverRunner) (*Service, ProjectTakeoverDraft) {
	t.Helper()
	containers := observableContainers{
		staticContainers: staticContainers{rows: []containerdomain.Summary{{ID: "web", Labels: map[string]string{ProjectLabel: "shop"}}}},
		snapshot:         RuntimeProjectSnapshot{ProjectName: "shop", Containers: []RuntimeContainer{{ID: "web", Service: "web", ImageInspectOK: true, Config: RuntimeConfig{Image: "app:v1"}}}},
	}
	service := &Service{root: t.TempDir(), containers: containers, runner: runner, nodeID: "tcp-node", projectLocks: &sync.Map{}}
	draft, err := service.BuildTakeoverDraft(context.Background(), "shop")
	if err != nil {
		t.Fatal(err)
	}
	return service, draft
}

func TestTakeoverAtomicallyClaimsProjectWithoutDeploying(t *testing.T) {
	runner := &takeoverRunner{}
	service, draft := takeoverHarness(t, runner)
	project, err := service.Takeover(context.Background(), "shop", TakeoverInput{Fingerprint: draft.Fingerprint, ConfirmationName: "shop", Compose: draft.Compose, Environment: draft.Environment})
	if err != nil {
		t.Fatal(err)
	}
	if !runner.validated || runner.deployed {
		t.Fatalf("runner state = %#v", runner)
	}
	if !project.Managed || project.Metadata == nil || project.Metadata.Origin != "takeover" || project.Metadata.TakeoverSource != "runtime" || project.Metadata.LastDeployedAt != nil {
		t.Fatalf("managed Project = %#v", project)
	}
	if _, err := os.Stat(filepath.Join(project.Path, "compose.yml")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(project.Path, ".suma", "project.json")); err != nil {
		t.Fatal(err)
	}
}

func TestTakeoverRequiresExactProjectNameAndCurrentFingerprint(t *testing.T) {
	service, draft := takeoverHarness(t, &takeoverRunner{})
	input := TakeoverInput{Fingerprint: draft.Fingerprint, ConfirmationName: "wrong", Compose: draft.Compose}
	if _, err := service.Takeover(context.Background(), "shop", input); err == nil {
		t.Fatal("expected confirmation rejection")
	}
	input.ConfirmationName, input.Fingerprint = "shop", "stale"
	if _, err := service.Takeover(context.Background(), "shop", input); err == nil {
		t.Fatal("expected fingerprint rejection")
	}
}

func TestValidateDraftDoesNotRequireManagedProject(t *testing.T) {
	runner := &takeoverRunner{}
	service := &Service{root: t.TempDir(), runner: runner}
	if err := service.ValidateDraft(context.Background(), "services:\n  web:\n    image: nginx:alpine\n", ""); err != nil {
		t.Fatal(err)
	}
	if !runner.validated {
		t.Fatal("Compose runner was not called")
	}
	if _, err := os.Stat(filepath.Join(service.root, "web")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("draft validation created a managed Project: %v", err)
	}
}

func TestValidateDraftReturnsRedactedComposeDiagnostic(t *testing.T) {
	runner := &takeoverRunner{
		validateError:  errors.New("docker compose config --quiet: exit status 1"),
		validateOutput: "service web has invalid configuration near secret-value",
	}
	service := &Service{root: t.TempDir(), runner: runner}
	err := service.ValidateDraft(context.Background(), "services:\n  web:\n    image: nginx:alpine\n", "PASSWORD='secret-value'\n")
	if err == nil || !strings.Contains(err.Error(), "service web has invalid configuration") {
		t.Fatalf("validation diagnostic missing: %v", err)
	}
	if strings.Contains(err.Error(), "secret-value") || !strings.Contains(err.Error(), "***") {
		t.Fatalf("validation diagnostic leaked environment value: %v", err)
	}
}

func TestTakeoverValidationFailureLeavesNoManagedDirectory(t *testing.T) {
	runner := &takeoverRunner{validateError: errors.New("invalid")}
	service, draft := takeoverHarness(t, runner)
	_, err := service.Takeover(context.Background(), "shop", TakeoverInput{Fingerprint: draft.Fingerprint, ConfirmationName: "shop", Compose: draft.Compose})
	if err == nil {
		t.Fatal("expected validation failure")
	}
	if _, statErr := os.Stat(filepath.Join(service.nodeRoot(), "shop")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("partial Project remains: %v", statErr)
	}
}

func TestTakeoverRejectsFileBackedSecret(t *testing.T) {
	service, draft := takeoverHarness(t, &takeoverRunner{})
	content := "services:\n  web:\n    image: app:v1\n    secrets: [token]\nsecrets:\n  token:\n    file: ./token.txt\n"
	_, err := service.Takeover(context.Background(), "shop", TakeoverInput{Fingerprint: draft.Fingerprint, ConfirmationName: "shop", Compose: content})
	if err == nil {
		t.Fatal("expected file-backed secret rejection")
	}
}

func TestManualTakeoverSavesUserComposeAndRecordsManualSource(t *testing.T) {
	runner := &takeoverRunner{}
	service, draft := takeoverHarness(t, runner)
	content := "name: shop\nservices:\n  web:\n    image: app:v2\n    environment:\n      MODE: manual\n"
	project, err := service.Takeover(context.Background(), "shop", TakeoverInput{Mode: TakeoverModeManual, Fingerprint: draft.Fingerprint, ConfirmationName: "shop", Compose: content, Environment: "MODE='manual'\n"})
	if err != nil {
		t.Fatal(err)
	}
	if !runner.validated || runner.deployed {
		t.Fatalf("runner state = %#v", runner)
	}
	if !project.Managed || project.Metadata == nil || project.Metadata.Origin != "takeover" || project.Metadata.TakeoverSource != TakeoverModeManual || project.Metadata.LastDeployedAt != nil {
		t.Fatalf("managed Project = %#v", project)
	}
	if project.Compose != content || project.Environment != "MODE='manual'\n" {
		t.Fatalf("manual takeover rewrote user content: %#v", project)
	}
}

func TestManualTakeoverStillRequiresCurrentFingerprint(t *testing.T) {
	service, _ := takeoverHarness(t, &takeoverRunner{})
	_, err := service.Takeover(context.Background(), "shop", TakeoverInput{Mode: TakeoverModeManual, Fingerprint: "stale", ConfirmationName: "shop", Compose: "services:\n  web:\n    image: app:v2\n"})
	if err == nil {
		t.Fatal("expected fingerprint rejection")
	}
	if _, statErr := os.Stat(filepath.Join(service.nodeRoot(), "shop")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("stale manual takeover created a managed Project: %v", statErr)
	}
}

func TestTakeoverRejectsUnknownMode(t *testing.T) {
	service, draft := takeoverHarness(t, &takeoverRunner{})
	_, err := service.Takeover(context.Background(), "shop", TakeoverInput{Mode: "wizard", Fingerprint: draft.Fingerprint, ConfirmationName: "shop", Compose: draft.Compose})
	if err == nil || !strings.Contains(err.Error(), "takeover mode") {
		t.Fatalf("unknown mode error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(service.nodeRoot(), "shop")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("unknown mode created a managed Project: %v", statErr)
	}
}

func TestTakeoverRejectsMismatchedProjectIdentity(t *testing.T) {
	service, draft := takeoverHarness(t, &takeoverRunner{})
	cases := []struct{ name, compose, environment string }{
		{"top-level name", "name: other\nservices:\n  web:\n    image: app:v1\n", ""},
		{"COMPOSE_PROJECT_NAME", "services:\n  web:\n    image: app:v1\n", "export COMPOSE_PROJECT_NAME=\"other\"\n"},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			_, err := service.Takeover(context.Background(), "shop", TakeoverInput{Mode: TakeoverModeManual, Fingerprint: draft.Fingerprint, ConfirmationName: "shop", Compose: item.compose, Environment: item.environment})
			if err == nil || !strings.Contains(err.Error(), `"other"`) {
				t.Fatalf("identity mismatch error = %v", err)
			}
			if _, statErr := os.Stat(filepath.Join(service.nodeRoot(), "shop")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("identity mismatch created a managed Project: %v", statErr)
			}
			if err := service.ValidateTakeoverDraft(context.Background(), "shop", item.compose, item.environment); err == nil {
				t.Fatal("draft validation accepted a mismatched Project identity")
			}
		})
	}
	if err := service.ValidateTakeoverDraft(context.Background(), "shop", "name: shop\nservices:\n  web:\n    image: app:v1\n", "COMPOSE_PROJECT_NAME=shop\n"); err != nil {
		t.Fatal(err)
	}
}
