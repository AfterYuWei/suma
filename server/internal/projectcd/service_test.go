package projectcd

import (
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/credential"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/deployment"
	gitrepo "github.com/suma/suma/server/internal/git"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/task"
	"github.com/suma/suma/server/internal/testutil"
)

type fixtureGit struct{ root, commit string }

func (g *fixtureGit) TestConnection(context.Context, gitrepo.SyncRequest) (gitrepo.ConnectionResult, error) {
	return gitrepo.ConnectionResult{RefChecked: true, CommitSHA: g.commit}, nil
}
func (g *fixtureGit) Sync(context.Context, gitrepo.SyncRequest, io.Writer) (gitrepo.Revision, error) {
	return gitrepo.Revision{CommitSHA: g.commit, WorktreePath: g.root}, nil
}
func (g *fixtureGit) Verify(context.Context, string, string) error { return nil }
func (g *fixtureGit) Cleanup(uint) error                           { return nil }

type projectCDFixture struct {
	db                                         *gorm.DB
	root, worktree, calls, projectPath, source string
	tasks                                      *task.Service
	ordinary                                   *compose.Service
	service                                    *Service
	git                                        *fixtureGit
	input                                      ConfigureInput
}

func newProjectCDFixture(t *testing.T) *projectCDFixture {
	t.Helper()
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	worktree := t.TempDir()
	script := filepath.Join(root, "compose-fixture")
	calls := filepath.Join(root, "calls")
	command := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + calls + "'\ncase \" $* \" in *' config '*) printf '%s' '{\"services\":{\"app\":{\"image\":\"busybox:stable\"}}}' ;; *' up '*) if [ -f '" + filepath.Join(root, "up-fail") + "' ]; then exit 1; fi ;; *' ps '*) printf '%s' '[{\"State\":\"running\",\"Health\":\"healthy\"}]' ;; esac\n"
	if err := os.WriteFile(script, []byte(command), 0700); err != nil {
		t.Fatal(err)
	}
	runner, err := compose.NewRunner(script)
	if err != nil {
		t.Fatal(err)
	}
	projectPath := filepath.Join(root, "app")
	if err := os.MkdirAll(projectPath, 0700); err != nil {
		t.Fatal(err)
	}
	source := "services: {app: {image: 'busybox:stable'}}\n"
	if err := os.WriteFile(filepath.Join(projectPath, "compose.yml"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "compose.yml"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	tasks := task.NewService(db)
	ordinary, err := compose.NewService(db, root, runner, tasks, nil)
	if err != nil {
		t.Fatal(err)
	}
	store, err := secret.Open(filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	git := &fixtureGit{root: worktree, commit: strings.Repeat("a", 40)}
	service := New(db, git, gitrepo.NewCredentialService(db, store), credential.NewRegistryService(db, store), runner, tasks, audit.NewService(db), store, Dependencies{Project: func(context.Context, string) (*compose.Service, error) { return ordinary, nil }, Target: func(context.Context, string) (compose.Target, error) {
		return compose.Target{NodeID: "local", NodeName: "Local", RuntimeIdentity: "fixture", Host: "unix:///fixture.sock"}, nil
	}})
	ordinary.SetProjectCDView(service.ProjectView)
	input := ConfigureInput{WebhookEnabled: true, Repository: gitrepo.Repository{CloneURL: "https://example.test/repo.git", RefType: "branch", Ref: "main", ComposeFiles: []string{"compose.yml"}, Authentication: gitrepo.Authentication{Source: "none"}}}
	return &projectCDFixture{db: db, root: root, worktree: worktree, calls: calls, projectPath: projectPath, source: source, tasks: tasks, ordinary: ordinary, service: service, git: git, input: input}
}

func TestProjectCDFirstApplicationSnapshotsRollbackAndDetach(t *testing.T) {
	f := newProjectCDFixture(t)
	db, root, calls, projectPath, source, tasks, ordinary, service, git, input := f.db, f.root, f.calls, f.projectPath, f.source, f.tasks, f.ordinary, f.service, f.git, f.input
	config, err := service.Configure(context.Background(), "local", "app", input)
	if err != nil {
		t.Fatal(err)
	}
	if !config.Enabled || !config.InitialApplicationRequired || config.AutomationEnabled {
		t.Fatal("incorrect first-application gate")
	}
	if _, err := service.Apply(context.Background(), "local", "app", 1, config.Revision, false, Actor{}); err == nil {
		t.Fatal("applied without a checked snapshot")
	}
	if _, err := ordinary.Save(context.Background(), "app", source, ""); !errors.Is(err, compose.ErrProjectCDManaged) {
		t.Fatal("old save bypassed GitOps", err)
	}
	if _, err := ordinary.Action(context.Background(), "app", "update"); !errors.Is(err, compose.ErrProjectCDManaged) {
		t.Fatal("old update bypassed GitOps", err)
	}
	wait := func(row database.Task, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := tasks.Wait(ctx, row.ID); err != nil {
			t.Fatal(err)
		}
		finished, err := tasks.Get(ctx, row.ID)
		if err != nil || finished.Status != task.StatusSuccess {
			t.Fatalf("task failed: %+v %v", finished, err)
		}
	}
	check, err := service.Check(context.Background(), "local", "app", "manual", Actor{Name: "Tester"})
	wait(check, err)
	config, err = service.Get(context.Background(), "local", "app")
	if err != nil || config.Candidate == nil {
		t.Fatal("no candidate", err)
	}
	raw, _ := os.ReadFile(calls)
	if strings.Contains(string(raw), " up ") {
		t.Fatal("check deployed containers")
	}
	applied, err := service.Apply(context.Background(), "local", "app", config.Candidate.ID, config.Revision, false, Actor{Name: "Tester"})
	wait(applied, err)
	config, err = service.Get(context.Background(), "local", "app")
	if err != nil || config.Applied == nil || config.InitialApplicationRequired {
		t.Fatal("missing successful application", err)
	}
	successID := config.Applied.ID
	value := "kept-private"
	updated, err := service.SetEnvironment(context.Background(), "local", "app", config.Revision, []deployment.EnvironmentVariable{{Name: "APP_TOKEN", Value: &value}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(updated)
	if strings.Contains(string(encoded), value) {
		t.Fatal("configuration leaked private value")
	}
	if _, err := service.Apply(context.Background(), "local", "app", config.Candidate.ID, config.Revision, false, Actor{}); !errors.Is(err, ErrConflict) {
		t.Fatal("stale configuration applied", err)
	}
	updatedInput := input
	updatedInput.ExpectedRevision = updated.Revision
	updatedInput.ReconcileMode = "auto"
	updatedInput.Environment = updated.Environment
	enabled := true
	updatedInput.AutomationEnabled = &enabled
	config, err = service.Configure(context.Background(), "local", "app", updatedInput)
	if err != nil {
		t.Fatal(err)
	}
	// A failed automatic snapshot is attempted once, not on every polling cycle.
	git.commit = strings.Repeat("b", 40)
	if err := os.WriteFile(filepath.Join(root, "up-fail"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	failed, err := service.Check(context.Background(), "local", "app", "poll", Actor{Name: "Poll"})
	if err != nil {
		t.Fatal(err)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := tasks.Wait(waitCtx, failed.ID); err != nil {
		t.Fatal(err)
	}
	failedTask, err := tasks.Get(waitCtx, failed.ID)
	if err != nil || failedTask.Status != task.StatusFailed {
		t.Fatal("failed application was not recorded", err)
	}
	callsBefore, _ := os.ReadFile(calls)
	repeat, err := service.Check(context.Background(), "local", "app", "poll", Actor{Name: "Poll"})
	wait(repeat, err)
	callsAfter, _ := os.ReadFile(calls)
	if strings.Count(string(callsBefore), " up ") != strings.Count(string(callsAfter), " up ") {
		t.Fatal("same failed snapshot was applied repeatedly")
	}
	if err := os.Remove(filepath.Join(root, "up-fail")); err != nil {
		t.Fatal(err)
	}
	rolled, err := service.Apply(context.Background(), "local", "app", successID, config.Revision, true, Actor{Name: "Tester"})
	wait(rolled, err)
	config, err = service.Get(context.Background(), "local", "app")
	if err != nil || config.ReconcileMode != "manual" {
		t.Fatal("rollback did not restore manual delivery policy", err)
	}
	detached, err := service.Detach(context.Background(), "local", "app", config.Revision, Actor{Name: "Tester"})
	wait(detached, err)
	config, err = service.Get(context.Background(), "local", "app")
	if err != nil || config.Enabled {
		t.Fatal("manual conversion failed", err)
	}
	raw, _ = os.ReadFile(filepath.Join(projectPath, ".env"))
	if strings.Contains(string(raw), value) {
		t.Fatal("unapplied variable entered the manual export")
	}
	// A second CD session must not export an older session over new manual edits.
	manual := "services: {manual: {image: 'busybox:stable'}}\n"
	if err := os.WriteFile(filepath.Join(projectPath, "compose.yml"), []byte(manual), 0600); err != nil {
		t.Fatal(err)
	}
	input.ExpectedRevision = config.Revision
	input.ReviewedEnvironment = true
	input.Environment = []deployment.EnvironmentVariable{}
	reenabled, err := service.Configure(context.Background(), "local", "app", input)
	if err != nil {
		t.Fatal(err)
	}
	if reenabled.Applied != nil || !reenabled.InitialApplicationRequired {
		t.Fatal("new CD session inherited an old applied pointer")
	}
	detachedAgain, err := service.Detach(context.Background(), "local", "app", reenabled.Revision, Actor{Name: "Tester"})
	wait(detachedAgain, err)
	original, err := os.ReadFile(filepath.Join(projectPath, "compose.yml"))
	if err != nil || string(original) != manual {
		t.Fatal("unapplied second session overwrote manual configuration", err)
	}
	var retained int64
	if err := db.Model(&database.ProjectCDRecord{}).Count(&retained).Error; err != nil || retained < 4 {
		t.Fatal("history was lost", err)
	}
}

func TestExportDependenciesPreservesFilesAndRejectsEscapes(t *testing.T) {
	root := t.TempDir()
	destination := t.TempDir()
	source := filepath.Join(root, "secret.txt")
	if err := os.WriteFile(source, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	raw := `{"services":{"app":{"image":"app:${VERSION}","env_file":[{"path":"secret.txt","required":true}]}},"secrets":{"key":{"file":"secret.txt"}}}`
	rendered := `{"services":{"app":{"image":"app:1"}},"secrets":{"key":{"file":` + strconvQuote(source) + `}}}`
	exported, expected, err := exportDependencies(raw, rendered, root, root, destination)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(exported, "${VERSION}") || !strings.Contains(expected, destination) {
		t.Fatal("lost expressions or references")
	}
	content, err := os.ReadFile(filepath.Join(destination, "secret.txt"))
	if err != nil || string(content) != "private" {
		t.Fatal("dependency lost", err)
	}
	if err := os.Symlink(filepath.Join(destination, "secret.txt"), filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := exportDependencies(`{"secrets":{"key":{"file":"escape"}}}`, `{}`, root, root, destination); err == nil {
		t.Fatal("escaped dependency exported")
	}
}
func strconvQuote(value string) string { raw, _ := json.Marshal(value); return string(raw) }

func TestProjectCDRuntimeDriftIsReadOnlyEvidence(t *testing.T) {
	desired := map[string]int{"app": 1}
	hashes := map[string]string{"app": "expected"}
	good := compose.RuntimeProjectSnapshot{Containers: []compose.RuntimeContainer{{Service: "app", ConfigHash: "expected", State: "running"}}}
	if reason := runtimeDifference(desired, hashes, good, true); reason != "" {
		t.Fatal(reason)
	}
	for _, example := range []struct {
		name     string
		snapshot compose.RuntimeProjectSnapshot
		healthy  bool
		want     string
	}{
		{"stopped", compose.RuntimeProjectSnapshot{Containers: []compose.RuntimeContainer{{Service: "app", ConfigHash: "expected", State: "exited"}}}, true, "service_not_running"},
		{"missing", compose.RuntimeProjectSnapshot{}, true, "service_count_changed"},
		{"config changed", compose.RuntimeProjectSnapshot{Containers: []compose.RuntimeContainer{{Service: "app", ConfigHash: "different", State: "running"}}}, true, "compose_hash_mismatch"},
		{"unhealthy", good, false, "runtime_unhealthy"},
	} {
		t.Run(example.name, func(t *testing.T) {
			if reason := runtimeDifference(desired, hashes, example.snapshot, example.healthy); reason != example.want {
				t.Fatal(reason)
			}
		})
	}
}
