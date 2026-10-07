package compose

import (
	"context"
	"errors"
	"github.com/suma/suma/server/internal/task"
	"github.com/suma/suma/server/internal/testutil"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func configurationService(t *testing.T, runner Runner) *Service {
	t.Helper()
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewService(db, t.TempDir(), runner, task.NewService(db), emptyContainers{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestConfigurationConcurrentRevisionSaves(t *testing.T) {
	s := configurationService(t, nil)
	p, err := s.Create(context.Background(), "app", "services:\n  app:\n    image: nginx\n", "PASSWORD=not-logged\n")
	if err != nil {
		t.Fatal(err)
	}
	if p.Revision == "" {
		t.Fatal("missing revision")
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, image := range []string{"nginx:alpine", "nginx:stable"} {
		wg.Add(1)
		go func(image string) {
			defer wg.Done()
			_, err := s.SaveWithRevision(context.Background(), "app", "services:\n  app:\n    image: "+image+"\n", "PASSWORD=not-logged\n", p.Revision)
			results <- err
		}(image)
	}
	wg.Wait()
	close(results)
	var succeeded, conflicted int
	for err := range results {
		if err == nil {
			succeeded++
		} else if errors.Is(err, ErrRevisionConflict) {
			conflicted++
		} else {
			t.Fatal(err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("success=%d conflict=%d", succeeded, conflicted)
	}
}
func TestConfigurationPairPreparationFailurePreservesCompose(t *testing.T) {
	s := configurationService(t, nil)
	original := "services: {}\n"
	p, err := s.Create(context.Background(), "app", original, "OLD=1\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(p.Path, ".env")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(p.Path, ".env"), 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(context.Background(), "app", "services: {new: {image: nginx}}\n", "NEW=1\n"); err == nil {
		t.Fatal("expected failure")
	}
	content, err := os.ReadFile(filepath.Join(p.Path, "compose.yml"))
	if err != nil || string(content) != original {
		t.Fatal("Compose changed after pair preparation failure")
	}
}
func TestConfigurationSecondPublishFailureRollsBackPair(t *testing.T) {
	path := t.TempDir()
	if err := writeConfiguration(path, "services: {}\n", "OLD=1\n"); err != nil {
		t.Fatal(err)
	}
	err := publishConfiguration(path, "services: {app: {image: nginx}}\n", "NEW=2\n", func(from, to string) error {
		if filepath.Base(to) == ".env" {
			return errors.New("simulated second-file publish failure")
		}
		return os.Rename(from, to)
	})
	if err == nil {
		t.Fatal("expected publish failure")
	}
	content, err := readConfiguration(path)
	if err != nil || content != [2]string{"services: {}\n", "OLD=1\n"} {
		t.Fatalf("configuration pair was not restored: %v", err)
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 2 {
		t.Fatal("private temporary files were not cleaned")
	}
}
func TestConfigurationSaveAllowsSemanticDraftButChecksIdentity(t *testing.T) {
	s := configurationService(t, nil)
	p, err := s.Create(context.Background(), "app", "services: {}\n", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveWithRevision(context.Background(), "app", "services: {draft: {restart: invalid}}\n", "", p.Revision); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"services: [", "name: other\nservices: {}\n"} {
		if _, err := s.Save(context.Background(), "app", content, ""); err == nil {
			t.Fatal("invalid identity/source accepted")
		}
	}
	if _, err := s.Save(context.Background(), "app", "services: {}\n", "COMPOSE_PROJECT_NAME=other\n"); err == nil {
		t.Fatal("environment identity accepted")
	}
}

type configurationRunner struct {
	Runner
	spec     ExecutionSpec
	entered  chan struct{}
	release  chan struct{}
	failure  bool
	upOutput string
	upError  error
}

func (r *configurationRunner) ValidateRelease(_ context.Context, spec ExecutionSpec, out io.Writer) error {
	r.spec = spec
	if r.failure {
		_, _ = io.WriteString(out, "PASSWORD=hidden-secret")
		return errors.New("invalid")
	}
	return nil
}
func (r *configurationRunner) Up(ctx context.Context, _ string, out io.Writer) error {
	if r.upOutput != "" {
		middle := len(r.upOutput) / 2
		_, _ = io.WriteString(out, r.upOutput[:middle])
		_, _ = io.WriteString(out, r.upOutput[middle:])
	}
	if r.entered != nil {
		close(r.entered)
		select {
		case <-r.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return r.upError
}
func TestConfigurationValidationUsesRealIdentityAndPaths(t *testing.T) {
	r := &configurationRunner{}
	s := configurationService(t, r)
	if err := s.ValidateConfiguration(context.Background(), "newapp", "services: {app: {image: nginx}}\n", "PORT=8080\n"); err != nil {
		t.Fatal(err)
	}
	path, _ := s.safePath("newapp")
	if r.spec.ProjectName != "newapp" || r.spec.ProjectDir != path || len(r.spec.Files) != 1 || len(r.spec.EnvFiles) != 1 {
		t.Fatalf("unexpected validation spec %#v", r.spec)
	}
	if _, err := os.Stat(r.spec.Files[0]); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("validation files were not cleaned")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("validation created a managed Project")
	}
}
func TestConfigurationValidationHidesInlineSecrets(t *testing.T) {
	r := &configurationRunner{failure: true}
	s := configurationService(t, r)
	err := s.ValidateConfiguration(context.Background(), "app", "services:\n  app:\n    image: nginx\n    environment:\n      PASSWORD: hidden-secret\n", "")
	if err == nil || containsSecret(err.Error(), "hidden-secret") {
		t.Fatal("secret diagnostic exposed")
	}
	err = s.ValidateConfiguration(context.Background(), "app", "services: {app: {image: nginx}}\n", "PASSWORD='hidden-\nsecret'\n")
	if err == nil || containsSecret(err.Error(), "hidden-secret") {
		t.Fatal("multiline environment diagnostic exposed")
	}
	err = ValidateComposeBindMounts("services: [ PASSWORD: hidden-secret", false, false)
	if err == nil || containsSecret(err.Error(), "hidden-secret") {
		t.Fatal("policy syntax diagnostic exposed")
	}
}
func containsSecret(value, secret string) bool {
	for i := 0; i+len(secret) <= len(value); i++ {
		if value[i:i+len(secret)] == secret {
			return true
		}
	}
	return false
}
func TestConfigurationDeploymentRejectsConcurrentWritesAndReleasesReservation(t *testing.T) {
	r := &configurationRunner{entered: make(chan struct{}), release: make(chan struct{})}
	s := configurationService(t, r)
	p, err := s.Create(context.Background(), "app", "services: {app: {image: nginx}}\n", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ActionWithRevision(context.Background(), "app", "up", "stale"); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale deployment: %v", err)
	}
	if _, err := s.ActionWithRevision(context.Background(), "app", "up", p.Revision); err != nil {
		t.Fatal(err)
	}
	select {
	case <-r.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("task did not start")
	}
	if _, err := s.Save(context.Background(), "app", p.Compose, "NEW=1\n"); !errors.Is(err, ErrProjectBusy) {
		t.Fatalf("save during deployment: %v", err)
	}
	if _, err := s.Action(context.Background(), "app", "up"); !errors.Is(err, ErrProjectBusy) {
		t.Fatalf("duplicate operation: %v", err)
	}
	if err := s.Remove(context.Background(), "app"); !errors.Is(err, ErrProjectBusy) {
		t.Fatalf("delete during deployment: %v", err)
	}
	close(r.release)
	deadline := time.Now().Add(5 * time.Second)
	for s.operationActive("app") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := s.SaveWithRevision(context.Background(), "app", p.Compose, "NEW=1\n", p.Revision); err != nil {
		t.Fatal(err)
	}
}
func TestConfigurationFailedOrCanceledDeploymentReleasesEditing(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "validation failure", true: "cancellation"}[cancel], func(t *testing.T) {
			r := &configurationRunner{failure: !cancel}
			if cancel {
				r.entered = make(chan struct{})
				r.release = make(chan struct{})
			}
			s := configurationService(t, r)
			p, err := s.Create(context.Background(), "app", "services: {app: {image: nginx}}\n", "")
			if err != nil {
				t.Fatal(err)
			}
			operation, err := s.ActionWithRevision(context.Background(), "app", "up", p.Revision)
			if err != nil {
				t.Fatal(err)
			}
			if cancel {
				select {
				case <-r.entered:
				case <-time.After(5 * time.Second):
					t.Fatal("task not started")
				}
				if !s.tasks.Cancel(operation.ID) {
					t.Fatal("task cancellation failed")
				}
			}
			deadline := time.Now().Add(5 * time.Second)
			for s.operationActive("app") && time.Now().Before(deadline) {
				time.Sleep(5 * time.Millisecond)
			}
			if _, err := s.SaveWithRevision(context.Background(), "app", p.Compose, "NEW=1\n", p.Revision); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestConfigurationDeploymentRechecksNodeBindPolicy(t *testing.T) {
	s := configurationService(t, &configurationRunner{})
	socket := "services: {app: {image: nginx, volumes: [/var/run/docker.sock:/var/run/docker.sock]}}\n"
	p, err := s.Create(context.Background(), "app", socket, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ActionWithRevision(context.Background(), "app", "up", p.Revision); err == nil {
		t.Fatal("unconfirmed socket deployment accepted")
	}
	if _, err := s.ActionWithRevision(context.Background(), "app", "up", p.Revision, true); err != nil {
		t.Fatal(err)
	}
	remote := s.ForNode("remote", "Remote", &configurationRunner{}, emptyContainers{}, false)
	p, err = remote.Create(context.Background(), "remoteapp", "services: {app: {image: nginx, volumes: [./data:/data]}}\n", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := remote.ActionWithRevision(context.Background(), "remoteapp", "up", p.Revision); err == nil {
		t.Fatal("relative remote bind deployment accepted")
	}
}
func TestConfigurationTaskOutputAndErrorsRedactCandidateSecrets(t *testing.T) {
	r := &configurationRunner{upOutput: "credential inline-secret and dotenv-secret\n", upError: errors.New("failed with inline-secret and dotenv-secret")}
	s := configurationService(t, r)
	p, err := s.Create(context.Background(), "app", "services: {app: {image: nginx, environment: {PASSWORD: inline-secret}}}\n", "TOKEN='dotenv-secret' # retained comment\n")
	if err != nil {
		t.Fatal(err)
	}
	operation, err := s.ActionWithRevision(context.Background(), "app", "up", p.Revision)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		current, err := s.tasks.Get(context.Background(), operation.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status == task.StatusFailed {
			logs, err := s.tasks.Logs(context.Background(), operation.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, log := range logs {
				if containsSecret(log.Message, "inline-secret") || containsSecret(log.Message, "dotenv-secret") {
					t.Fatal("secret persisted in Task logs")
				}
			}
			if containsSecret(current.Message, "inline-secret") || containsSecret(current.Message, "dotenv-secret") {
				t.Fatal("secret persisted in Task state")
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("task did not finish")
}

func TestConfigurationRedactorHandlesNumericValuesAndInterpolatedCredentials(t *testing.T) {
	redact := configurationRedactor("services: {app: {environment: {PASSWORD: 12345}}}\n", "")
	if containsSecret(redact("failed with 12345"), "12345") {
		t.Fatal("numeric secret exposed")
	}
	redact = configurationRedactor("services: {app: {environment: {CREDENTIAL: '${BASE:-fallback-secret}'}}}\n", "BASE=indirect-secret\n")
	if containsSecret(redact("failed with indirect-secret"), "indirect-secret") {
		t.Fatal("interpolated credential exposed")
	}
}
