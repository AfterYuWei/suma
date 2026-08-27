package cd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/database"
	gitrepo "github.com/suma/suma/server/internal/git"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/task"
	"gorm.io/gorm"
)

func TestExecutionSpecConfinesComposeFilesToGitWorktree(t *testing.T) {
	worktree := t.TempDir()
	production := filepath.Join(worktree, "deploy", "production")
	if err := os.MkdirAll(production, 0o750); err != nil {
		t.Fatal(err)
	}
	composeFile := filepath.Join(production, "compose.yml")
	overrideDir := filepath.Join(worktree, "environments")
	if err := os.MkdirAll(overrideDir, 0o750); err != nil {
		t.Fatal(err)
	}
	overrideFile := filepath.Join(overrideDir, "production.yml")
	environmentFile := filepath.Join(production, "production.env")
	if err := os.WriteFile(composeFile, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(environmentFile, []byte("APP_ENV=production\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(overrideFile, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	project := database.DeliveryProject{Name: "production"}
	repository := gitrepo.Repository{ComposeFiles: []string{"deploy/production/compose.yml", "environments/production.yml"}, EnvironmentFile: "deploy/production/production.env"}
	spec, err := executionSpec(project, repository, worktree)
	if err != nil {
		t.Fatal(err)
	}
	if spec.ProjectName != "suma-cd-0" || spec.ProjectDir != production || !reflect.DeepEqual(spec.Files, []string{composeFile, overrideFile}) || !reflect.DeepEqual(spec.EnvFiles, []string{environmentFile}) {
		t.Fatalf("unexpected execution spec: %#v", spec)
	}

	outside := filepath.Join(t.TempDir(), "outside.yml")
	if err := os.WriteFile(outside, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(production, "escaped.yml")); err != nil {
		t.Fatal(err)
	}
	repository.ComposeFiles = []string{"deploy/production/escaped.yml"}
	if _, err := executionSpec(project, repository, worktree); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("executionSpec() symlink escape error = %v", err)
	}
}

func TestUnconfiguredProjectReturnsUsableDefaults(t *testing.T) {
	harness := newCDHarness(t, ModeManual)
	if err := harness.db.Model(&database.DeliveryProject{}).Where("id = ?", harness.project.ID).Updates(map[string]any{
		"git_clone_url": "", "git_ref_type": "", "git_ref": "", "compose_files_json": "",
	}).Error; err != nil {
		t.Fatal(err)
	}
	configuration, err := harness.service.GetConfiguration(context.Background(), harness.project.Name)
	if err != nil {
		t.Fatal(err)
	}
	if configuration.Configured || configuration.Repository.RefType != gitrepo.RefBranch || configuration.Repository.Ref != "main" || !reflect.DeepEqual(configuration.Repository.ComposeFiles, []string{"compose.yml"}) {
		t.Fatalf("unconfigured defaults = %#v", configuration)
	}
}

func TestServiceManualSyncDeployDriftAndIdempotency(t *testing.T) {
	harness := newCDHarness(t, ModeManual)
	syncTask, err := harness.service.Sync(context.Background(), harness.project.Name, "manual", Actor{Name: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, harness.db, syncTask.ID, task.StatusSuccess)

	releases, err := harness.service.ListReleases(context.Background(), harness.project.Name)
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 1 || releases[0].Status != StatusAwaitingApproval || releases[0].CommitSHA != harness.git.revision.CommitSHA {
		t.Fatalf("unexpected releases after sync: %#v", releases)
	}
	var images []string
	if err := json.Unmarshal([]byte(releases[0].ImageReferences), &images); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(images, []string{"example/web:1", "example/worker:1"}) {
		t.Fatalf("release images = %#v", images)
	}
	if releases[0].ConfigHash == "" || releases[0].TaskID != syncTask.ID || releases[0].TriggerType != "manual" {
		t.Fatalf("release traceability fields are incomplete: %#v", releases[0])
	}

	drift, err := harness.service.Drift(context.Background(), harness.project.Name)
	if err != nil {
		t.Fatal(err)
	}
	if !drift.Drifted || drift.Reason != "no release is active" {
		t.Fatalf("drift before deployment = %#v", drift)
	}

	userID := uint(42)
	if _, err := harness.service.Approve(context.Background(), harness.project.Name, releases[0].ID, Actor{UserID: &userID, Name: "operator"}); err != nil {
		t.Fatal(err)
	}
	deployTask, err := harness.service.Deploy(context.Background(), harness.project.Name, releases[0].ID, Actor{UserID: &userID, Name: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, harness.db, deployTask.ID, task.StatusSuccess)
	release, err := harness.service.GetRelease(context.Background(), harness.project.Name, releases[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if release.Status != StatusSucceeded || release.ApprovedBy == nil || *release.ApprovedBy != userID || release.ApprovedAt == nil || release.StartedAt == nil || release.FinishedAt == nil || release.HealthSummary == "" {
		t.Fatalf("unexpected deployed release: %#v", release)
	}
	drift, err = harness.service.Drift(context.Background(), harness.project.Name)
	if err != nil {
		t.Fatal(err)
	}
	if drift.Drifted || drift.ActiveCommit != harness.git.revision.CommitSHA || drift.ActiveReleaseID == nil || *drift.ActiveReleaseID != release.ID {
		t.Fatalf("drift after deployment = %#v", drift)
	}
	assertCallSubsequence(t, harness.runner.Calls(), []string{"validate", "render", "pull", "up", "ps"})

	secondSync, err := harness.service.Sync(context.Background(), harness.project.Name, "poll", Actor{Name: "scheduler"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, harness.db, secondSync.ID, task.StatusSuccess)
	releases, err = harness.service.ListReleases(context.Background(), harness.project.Name)
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 1 {
		t.Fatalf("idempotent sync created %d releases: %#v", len(releases), releases)
	}
}

func TestManualReleaseRequiresExplicitApprovalAndSupportsRejection(t *testing.T) {
	harness := newCDHarness(t, ModeManual)
	syncTask, err := harness.service.Sync(context.Background(), harness.project.Name, "manual", Actor{Name: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, harness.db, syncTask.ID, task.StatusSuccess)
	releases, err := harness.service.ListReleases(context.Background(), harness.project.Name)
	if err != nil || len(releases) != 1 {
		t.Fatalf("ListReleases() = %#v, %v", releases, err)
	}
	release := releases[0]
	if _, err := harness.service.Deploy(context.Background(), harness.project.Name, release.ID, Actor{Name: "operator"}); err == nil || !strings.Contains(err.Error(), StatusAwaitingApproval) {
		t.Fatalf("Deploy(unapproved) error = %v", err)
	}
	if _, err := harness.service.Approve(context.Background(), harness.project.Name, release.ID, Actor{Name: "anonymous"}); err == nil || !strings.Contains(err.Error(), "authenticated") {
		t.Fatalf("Approve(anonymous) error = %v", err)
	}
	userID := uint(7)
	approved, err := harness.service.Approve(context.Background(), harness.project.Name, release.ID, Actor{UserID: &userID, Name: "reviewer"})
	if err != nil || approved.Status != StatusApproved {
		t.Fatalf("Approve() = %#v, %v", approved, err)
	}
	rejected, err := harness.service.Reject(context.Background(), harness.project.Name, release.ID, Actor{UserID: &userID, Name: "reviewer"})
	if err != nil || rejected.Status != StatusRejected || !strings.Contains(rejected.FailureReason, "reviewer") {
		t.Fatalf("Reject() = %#v, %v", rejected, err)
	}
	if _, err := harness.service.Deploy(context.Background(), harness.project.Name, release.ID, Actor{UserID: &userID, Name: "operator"}); err == nil || !strings.Contains(err.Error(), StatusRejected) {
		t.Fatalf("Deploy(rejected) error = %v", err)
	}
}

func TestObserveModeBlocksApprovalAndDeployment(t *testing.T) {
	harness := newCDHarness(t, ModeObserve)
	syncTask, err := harness.service.Sync(context.Background(), harness.project.Name, "manual", Actor{Name: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, harness.db, syncTask.ID, task.StatusSuccess)
	releases, err := harness.service.ListReleases(context.Background(), harness.project.Name)
	if err != nil || len(releases) != 1 {
		t.Fatalf("ListReleases() = %#v, %v", releases, err)
	}
	userID := uint(7)
	if _, err := harness.service.Approve(context.Background(), harness.project.Name, releases[0].ID, Actor{UserID: &userID, Name: "reviewer"}); err == nil || !strings.Contains(err.Error(), "observe") {
		t.Fatalf("Approve(observe) error = %v", err)
	}
	if _, err := harness.service.Deploy(context.Background(), harness.project.Name, releases[0].ID, Actor{UserID: &userID, Name: "reviewer"}); err == nil || !strings.Contains(err.Error(), "observe") {
		t.Fatalf("Deploy(observe) error = %v", err)
	}
}

func TestAutoDeliveryFailsClosedOnUnhealthyRuntime(t *testing.T) {
	harness := newCDHarness(t, ModeAuto)
	harness.runner.health = `[{"Name":"production-web-1","State":"running","Health":"unhealthy"}]`
	syncTask, err := harness.service.Sync(context.Background(), harness.project.Name, "poll", Actor{Name: "scheduler"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, harness.db, syncTask.ID, task.StatusFailed)
	releases, err := harness.service.ListReleases(context.Background(), harness.project.Name)
	if err != nil || len(releases) != 1 {
		t.Fatalf("ListReleases() = %#v, %v", releases, err)
	}
	if releases[0].Status != StatusFailed || !strings.Contains(releases[0].FailureReason, "healthy") {
		t.Fatalf("unhealthy release = %#v", releases[0])
	}
	var project database.DeliveryProject
	if err := harness.db.First(&project, harness.project.ID).Error; err != nil {
		t.Fatal(err)
	}
	if project.ActiveReleaseID != nil {
		t.Fatalf("unhealthy release became active: %#v", project)
	}
}

func TestMultiNodeDeploymentRecordsPartialFailurePerNode(t *testing.T) {
	harness := newCDHarness(t, ModeManual)
	for _, id := range []string{"node-a", "node-b"} {
		if err := harness.db.Create(&database.Node{ID: id, Name: id, ConnectionType: "unix", Endpoint: "unix:///" + id + ".sock", TLSMode: "disabled", AllowedBindRootsJSON: "[]", Enabled: true}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := harness.db.Create(&database.DeliveryProjectNode{ProjectID: harness.project.ID, NodeID: "node-a"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := harness.db.Create(&database.DeliveryProjectNode{ProjectID: harness.project.ID, NodeID: "node-b"}).Error; err != nil {
		t.Fatal(err)
	}
	failing := &fakeComposeRunner{rendered: harness.runner.rendered, health: harness.runner.health, up: errors.New("node-b unavailable")}
	targeted := &targetedFakeRunner{fakeComposeRunner: harness.runner, runners: map[string]compose.Runner{"node-a": harness.runner, "node-b": failing}}
	harness.service.compose = targeted
	harness.service.SetTargetResolver(fakeTargetResolver{})

	syncTask, err := harness.service.Sync(context.Background(), harness.project.Name, "manual", Actor{Name: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, harness.db, syncTask.ID, task.StatusSuccess)
	releases, err := harness.service.ListReleases(context.Background(), harness.project.Name)
	if err != nil || len(releases) != 1 {
		t.Fatalf("releases = %#v, %v", releases, err)
	}
	userID := uint(1)
	if _, err := harness.service.Approve(context.Background(), harness.project.Name, releases[0].ID, Actor{UserID: &userID, Name: "operator"}); err != nil {
		t.Fatal(err)
	}
	deployTask, err := harness.service.Deploy(context.Background(), harness.project.Name, releases[0].ID, Actor{UserID: &userID, Name: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, harness.db, deployTask.ID, task.StatusFailed)
	release, err := harness.service.GetRelease(context.Background(), harness.project.Name, releases[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if release.Status != StatusPartialFailed || len(release.Deployments) != 2 {
		t.Fatalf("release = %#v", release)
	}
	statuses := map[string]string{}
	for _, deployment := range release.Deployments {
		statuses[deployment.NodeID] = deployment.Status
	}
	if statuses["node-a"] != StatusSucceeded || statuses["node-b"] != StatusFailed {
		t.Fatalf("deployment statuses = %#v", statuses)
	}
}

func TestServiceRollbackRestoresPreviousSuccessfulRelease(t *testing.T) {
	harness := newCDHarness(t, ModeManual)
	first := syncAndDeploy(t, harness)

	secondWorktree := makeWorktree(t, "example/web:2")
	harness.git.SetRevision(gitrepo.Revision{CommitSHA: strings.Repeat("b", 40), CommitAuthor: "Developer", CommitMessage: "second release", WorktreePath: secondWorktree})
	harness.runner.SetRendered(`{"services":{"web":{"image":"example/web:2"}}}`)
	second := syncAndDeploy(t, harness)
	if second.ID == first.ID {
		t.Fatal("second Git revision did not create a new release")
	}

	rollbackTask, err := harness.service.Rollback(context.Background(), harness.project.Name, first.ID, Actor{Name: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, harness.db, rollbackTask.ID, task.StatusSuccess)
	releases, err := harness.service.ListReleases(context.Background(), harness.project.Name)
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 3 {
		t.Fatalf("rollback should create an immutable release record, got %#v", releases)
	}
	rolledBack := releases[0]
	if rolledBack.Status != StatusRolledBack || rolledBack.TaskID != rollbackTask.ID || rolledBack.TriggerType != "rollback" || rolledBack.CommitSHA != first.CommitSHA || rolledBack.ID == first.ID {
		t.Fatalf("rollback release = %#v", rolledBack)
	}
	drift, err := harness.service.Drift(context.Background(), harness.project.Name)
	if err != nil {
		t.Fatal(err)
	}
	if !drift.Drifted || drift.ActiveReleaseID == nil || *drift.ActiveReleaseID != rolledBack.ID || drift.ActiveCommit != first.CommitSHA || drift.DesiredCommit != second.CommitSHA {
		t.Fatalf("drift after rollback = %#v", drift)
	}
}

func TestServiceSerializesConcurrentSyncAndDeduplicatesRelease(t *testing.T) {
	harness := newCDHarness(t, ModeManual)
	harness.git.delay = 75 * time.Millisecond
	first, err := harness.service.Sync(context.Background(), harness.project.Name, "manual", Actor{Name: "first"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := harness.service.Sync(context.Background(), harness.project.Name, "webhook", Actor{Name: "second"})
	if err == nil || !strings.Contains(err.Error(), "already queued") || second.ID != "" {
		t.Fatalf("second concurrent Sync() = %#v, %v; want explicit queued-reconciliation rejection", second, err)
	}
	waitTask(t, harness.db, first.ID, task.StatusSuccess)
	third, err := harness.service.Sync(context.Background(), harness.project.Name, "poll", Actor{Name: "scheduler"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, harness.db, third.ID, task.StatusSuccess)
	if got := harness.git.MaxConcurrent(); got != 1 {
		t.Fatalf("concurrent Git operations = %d, want project-level serialization", got)
	}
	var count int64
	if err := harness.db.Model(&database.DeliveryRelease{}).Where("project_id = ?", harness.project.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("concurrent identical syncs created %d releases", count)
	}
}

func TestServiceAutoModeDeploysDuringSync(t *testing.T) {
	harness := newCDHarness(t, ModeAuto)
	row, err := harness.service.Sync(context.Background(), harness.project.Name, "webhook", Actor{Name: "github webhook"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, harness.db, row.ID, task.StatusSuccess)
	releases, err := harness.service.ListReleases(context.Background(), harness.project.Name)
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 1 || releases[0].Status != StatusSucceeded {
		t.Fatalf("auto delivery releases = %#v", releases)
	}
	drift, err := harness.service.Drift(context.Background(), harness.project.Name)
	if err != nil || drift.Drifted {
		t.Fatalf("auto delivery drift = %#v, %v", drift, err)
	}
}

type cdHarness struct {
	db      *gorm.DB
	service *Service
	project database.DeliveryProject
	git     *fakeGitClient
	runner  *fakeComposeRunner
	secrets *secret.Store
}

func newCDHarness(t *testing.T, mode string) *cdHarness {
	t.Helper()
	root := t.TempDir()
	db, err := database.Open(filepath.Join(root, "suma.db"))
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := secret.Open(filepath.Join(root, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	worktree := makeWorktree(t, "example/web:1")
	gitClient := &fakeGitClient{revision: gitrepo.Revision{CommitSHA: strings.Repeat("a", 40), CommitAuthor: "Developer", CommitMessage: "first release", WorktreePath: worktree}}
	runner := &fakeComposeRunner{rendered: `{"services":{"worker":{"image":"example/worker:1"},"web":{"image":"example/web:1"}}}`, health: `[{"Name":"production-web-1","State":"running","Health":"healthy"}]`}
	project := database.DeliveryProject{
		Name:        "production",
		GitCloneURL: "https://git.example.test/team/deploy.git",
		GitRefType:  gitrepo.RefBranch, GitRef: "main", ComposeFilesJSON: `["compose.yml"]`,
		ReconcileMode: mode, SyncIntervalSeconds: 300, DeploymentTimeout: 60,
	}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	credentialService := gitrepo.NewCredentialService(db, secrets)
	service := NewService(db, gitClient, credentialService, runner, task.NewService(db), nil, secrets)
	return &cdHarness{db: db, service: service, project: project, git: gitClient, runner: runner, secrets: secrets}
}

func syncAndDeploy(t *testing.T, harness *cdHarness) database.DeliveryRelease {
	t.Helper()
	syncTask, err := harness.service.Sync(context.Background(), harness.project.Name, "manual", Actor{Name: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, harness.db, syncTask.ID, task.StatusSuccess)
	releases, err := harness.service.ListReleases(context.Background(), harness.project.Name)
	if err != nil || len(releases) == 0 {
		t.Fatalf("ListReleases() = %#v, %v", releases, err)
	}
	release := releases[0]
	userID := uint(42)
	if _, err := harness.service.Approve(context.Background(), harness.project.Name, release.ID, Actor{UserID: &userID, Name: "operator"}); err != nil {
		t.Fatal(err)
	}
	deployTask, err := harness.service.Deploy(context.Background(), harness.project.Name, release.ID, Actor{Name: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, harness.db, deployTask.ID, task.StatusSuccess)
	release, err = harness.service.GetRelease(context.Background(), harness.project.Name, release.ID)
	if err != nil {
		t.Fatal(err)
	}
	return release
}

func makeWorktree(t *testing.T, image string) string {
	t.Helper()
	worktree := t.TempDir()
	value := "services:\n  web:\n    image: " + image + "\n"
	if err := os.WriteFile(filepath.Join(worktree, "compose.yml"), []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
	return worktree
}

func waitTask(t *testing.T, db *gorm.DB, id, want string) database.Task {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var row database.Task
		if err := db.First(&row, "id = ?", id).Error; err != nil {
			t.Fatal(err)
		}
		if row.Status == task.StatusSuccess || row.Status == task.StatusFailed || row.Status == task.StatusCanceled {
			if row.Status != want {
				var logs []database.TaskLog
				_ = db.Where("task_id = ?", id).Order("id ASC").Find(&logs).Error
				t.Fatalf("task %s status = %q, want %q; message = %q; logs = %#v", id, row.Status, want, row.Message, logs)
			}
			return row
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("task %s did not finish", id)
	return database.Task{}
}

func assertCallSubsequence(t *testing.T, calls, want []string) {
	t.Helper()
	position := 0
	for _, call := range calls {
		if position < len(want) && call == want[position] {
			position++
		}
	}
	if position != len(want) {
		t.Fatalf("calls %v do not contain subsequence %v", calls, want)
	}
}

type fakeGitClient struct {
	mu         sync.Mutex
	revision   gitrepo.Revision
	delay      time.Duration
	active     int
	maxActive  int
	syncErrors []error
	calls      int
}

func (f *fakeGitClient) Sync(ctx context.Context, _ gitrepo.SyncRequest, output io.Writer) (gitrepo.Revision, error) {
	f.mu.Lock()
	f.active++
	if f.active > f.maxActive {
		f.maxActive = f.active
	}
	f.calls++
	call := f.calls
	revision := f.revision
	delay := f.delay
	var syncErr error
	if call <= len(f.syncErrors) {
		syncErr = f.syncErrors[call-1]
	}
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.active--
		f.mu.Unlock()
	}()
	if output != nil {
		_, _ = io.WriteString(output, "fetch complete\n")
	}
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return gitrepo.Revision{}, ctx.Err()
		}
	}
	return revision, syncErr
}

func (f *fakeGitClient) SetRevision(revision gitrepo.Revision) {
	f.mu.Lock()
	f.revision = revision
	f.mu.Unlock()
}

func (f *fakeGitClient) Verify(_ context.Context, worktree, commit string) error {
	if worktree == "" || commit == "" {
		return errors.New("worktree and commit are required")
	}
	return nil
}

func (f *fakeGitClient) Cleanup(uint) error { return nil }

func (f *fakeGitClient) MaxConcurrent() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxActive
}

type fakeComposeRunner struct {
	mu       sync.Mutex
	calls    []string
	rendered string
	health   string
	validate error
	pull     error
	up       error
	ps       error
}

type fakeTargetResolver struct{}

func (fakeTargetResolver) ResolveComposeTarget(_ context.Context, id string) (compose.Target, error) {
	return compose.Target{NodeID: id, NodeName: id, Host: "unix:///" + id + ".sock"}, nil
}

type targetedFakeRunner struct {
	*fakeComposeRunner
	runners map[string]compose.Runner
}

func (f *targetedFakeRunner) Targeted(target compose.Target) compose.Runner {
	return f.runners[target.NodeID]
}

func (f *fakeComposeRunner) record(call string) {
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()
}

func (f *fakeComposeRunner) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeComposeRunner) SetRendered(value string) {
	f.mu.Lock()
	f.rendered = value
	f.mu.Unlock()
}

func (f *fakeComposeRunner) Up(context.Context, string, io.Writer) error              { return nil }
func (f *fakeComposeRunner) Down(context.Context, string, io.Writer) error            { return nil }
func (f *fakeComposeRunner) ForceDown(context.Context, string, bool, io.Writer) error { return nil }
func (f *fakeComposeRunner) Start(context.Context, string, io.Writer) error           { return nil }
func (f *fakeComposeRunner) Stop(context.Context, string, io.Writer) error            { return nil }
func (f *fakeComposeRunner) Restart(context.Context, string, io.Writer) error         { return nil }
func (f *fakeComposeRunner) Pull(context.Context, string, io.Writer) error            { return nil }
func (f *fakeComposeRunner) Build(context.Context, string, io.Writer) error           { return nil }
func (f *fakeComposeRunner) Validate(context.Context, string, io.Writer) error {
	return nil
}
func (f *fakeComposeRunner) Logs(context.Context, string, io.Writer) error { return nil }

func (f *fakeComposeRunner) LogsRelease(context.Context, compose.ExecutionSpec, io.Writer) error {
	f.record("logs")
	return nil
}

func (f *fakeComposeRunner) Render(context.Context, compose.ExecutionSpec, io.Writer) (string, error) {
	f.record("render")
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rendered, nil
}

func (f *fakeComposeRunner) ValidateRelease(context.Context, compose.ExecutionSpec, io.Writer) error {
	f.record("validate")
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.validate
}

func (f *fakeComposeRunner) PullRelease(context.Context, compose.ExecutionSpec, io.Writer) error {
	f.record("pull")
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pull
}

func (f *fakeComposeRunner) UpRelease(context.Context, compose.ExecutionSpec, int, io.Writer) error {
	f.record("up")
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.up
}

func (f *fakeComposeRunner) DownRelease(context.Context, compose.ExecutionSpec, io.Writer) error {
	f.record("down")
	return nil
}
func (f *fakeComposeRunner) ForceDownRelease(context.Context, compose.ExecutionSpec, bool, io.Writer) error {
	f.record("force-down")
	return nil
}

func (f *fakeComposeRunner) PS(context.Context, compose.ExecutionSpec, io.Writer) (string, error) {
	f.record("ps")
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.health, f.ps
}

var _ gitrepo.Client = (*fakeGitClient)(nil)
var _ compose.Runner = (*fakeComposeRunner)(nil)
