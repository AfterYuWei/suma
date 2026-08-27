package compose

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	containerdomain "github.com/suma/suma/server/internal/container"
)

type takeoverRunner struct {
	Runner
	validateError error
	validated     bool
	deployed      bool
}

func (runner *takeoverRunner) Validate(context.Context, string, io.Writer) error {
	runner.validated = true
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
