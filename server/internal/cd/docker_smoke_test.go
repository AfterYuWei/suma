//go:build dockersmoke

package cd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/database"
	gitrepo "github.com/suma/suma/server/internal/git"
	noderepo "github.com/suma/suma/server/internal/node"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/task"
	"gorm.io/gorm"
)

// TestRealDockerCDDeliveryRollback is intentionally opt-in. It exercises the
// real docker compose CLI against the local engine while keeping Git transport
// deterministic (the real Git adapter has separate local-repository tests).
func TestRealDockerCDDeliveryRollback(t *testing.T) {
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
	secrets, err := secret.Open(filepath.Join(root, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	runner, err := compose.NewRunner("docker compose")
	if err != nil {
		t.Fatal(err)
	}
	projectName := fmt.Sprintf("suma-cd-smoke-%d", time.Now().UnixNano())
	firstTree := smokeWorktree(t, "one")
	firstCommit := strings.Repeat("a", 40)
	gitClient := &fakeGitClient{revision: gitrepo.Revision{CommitSHA: firstCommit, CommitAuthor: "Smoke Test", CommitMessage: "first", WorktreePath: firstTree}}
	project := database.DeliveryProject{
		Name:        projectName,
		GitCloneURL: "https://git.example.test/team/deploy.git",
		GitRefType:  gitrepo.RefBranch, GitRef: "main", ComposeFilesJSON: `["compose.yml"]`,
		ReconcileMode: ModeAuto, SyncIntervalSeconds: 300, DeploymentTimeout: 60,
	}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	runtimeProjectName := runtimeName(project)
	service := NewService(db, gitClient, gitrepo.NewCredentialService(db, secrets), runner, task.NewService(db), nil, secrets)

	cleanupSpec := compose.ExecutionSpec{ProjectName: runtimeProjectName, ProjectDir: firstTree, Files: []string{filepath.Join(firstTree, "compose.yml")}}
	defer func() {
		if cleanupSpec.ProjectName != "" {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
			defer cleanupCancel()
			_ = runner.DownRelease(cleanupCtx, cleanupSpec, io.Discard)
		}
	}()

	first := smokeSync(t, ctx, db, service, projectName)
	cleanupSpec, err = releaseExecutionSpec(runtimeProjectName, first)
	if err != nil {
		t.Fatal(err)
	}
	assertSmokeRuntime(t, ctx, runner, cleanupSpec)

	secondCommit := strings.Repeat("b", 40)
	gitClient.SetRevision(gitrepo.Revision{CommitSHA: secondCommit, CommitAuthor: "Smoke Test", CommitMessage: "second", WorktreePath: smokeWorktree(t, "two")})
	second := smokeSync(t, ctx, db, service, projectName)
	if second.CommitSHA != secondCommit || second.Status != StatusSucceeded {
		t.Fatalf("second release = %#v", second)
	}

	rollbackTask, err := service.Rollback(ctx, projectName, first.ID, Actor{Name: "Docker smoke"})
	if err != nil {
		t.Fatal(err)
	}
	smokeWaitTask(t, ctx, db, rollbackTask.ID, task.StatusSuccess)
	releases, err := service.ListReleases(ctx, projectName)
	if err != nil || len(releases) < 3 {
		t.Fatalf("releases after rollback = %#v, %v", releases, err)
	}
	rolledBack := releases[0]
	if rolledBack.Status != StatusRolledBack || rolledBack.CommitSHA != firstCommit {
		t.Fatalf("rollback release = %#v", rolledBack)
	}
	cleanupSpec, err = releaseExecutionSpec(runtimeProjectName, rolledBack)
	if err != nil {
		t.Fatal(err)
	}
	assertSmokeRuntime(t, ctx, runner, cleanupSpec)
}

// TestRealDockerMultiNodeConsistency requires two independent engines: the
// local Unix socket and an mTLS TCP engine. It exercises the complete
// multi-target drift, retry, and node-specific rollback path.
func TestRealDockerMultiNodeConsistency(t *testing.T) {
	host, certDir := os.Getenv("SUMA_SMOKE_TCP_HOST"), os.Getenv("SUMA_SMOKE_TLS_DIR")
	if os.Getenv("SUMA_RUN_DOCKER_SMOKE") != "1" || host == "" || certDir == "" {
		t.Skip("set SUMA_RUN_DOCKER_SMOKE=1, SUMA_SMOKE_TCP_HOST, and SUMA_SMOKE_TLS_DIR for the dual-node smoke test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	root := t.TempDir()
	db, err := database.Open(filepath.Join(root, "suma.db"))
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := secret.Open(filepath.Join(root, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := noderepo.NewService(db, secrets, "unix:///var/run/docker.sock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = nodes.Close() })
	read := func(name string) string {
		value, err := os.ReadFile(filepath.Join(certDir, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(value)
	}
	credential, err := nodes.CreateTLSCredential(ctx, noderepo.TLSCredentialInput{Name: "CD smoke mTLS", CA: read("ca.pem"), Certificate: read("cert.pem"), PrivateKey: read("key.pem")})
	if err != nil {
		t.Fatal(err)
	}
	remote, err := nodes.Create(ctx, noderepo.Input{Name: "CD smoke remote", ConnectionType: noderepo.ConnectionTCP, Endpoint: host, TLSMode: noderepo.TLSRequired, TLSCredentialID: &credential.ID, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := compose.NewRunner("docker compose")
	if err != nil {
		t.Fatal(err)
	}
	projectName := fmt.Sprintf("suma-cd-multi-smoke-%d", time.Now().UnixNano())
	firstTree := smokeWorktree(t, "multi-one")
	gitClient := &fakeGitClient{revision: gitrepo.Revision{CommitSHA: strings.Repeat("a", 40), CommitAuthor: "Smoke Test", CommitMessage: "first", WorktreePath: firstTree}}
	project := database.DeliveryProject{Name: projectName, GitCloneURL: "https://git.example.test/team/deploy.git", GitRefType: gitrepo.RefBranch, GitRef: "main", ComposeFilesJSON: `["compose.yml"]`, ReconcileMode: ModeAuto, SyncIntervalSeconds: 300, DeploymentTimeout: 60}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	for _, nodeID := range []string{"local", remote.ID} {
		if err := db.Create(&database.DeliveryProjectNode{ProjectID: project.ID, NodeID: nodeID}).Error; err != nil {
			t.Fatal(err)
		}
	}
	service := NewService(db, gitClient, gitrepo.NewCredentialService(db, secrets), runner, task.NewService(db), nil, secrets)
	service.SetTargetResolver(nodes)
	cleanup := func() {
		var releases []database.DeliveryRelease
		_ = db.Where("project_id = ?", project.ID).Order("id DESC").Find(&releases).Error
		if len(releases) == 0 {
			return
		}
		spec, specErr := releaseExecutionSpec(runtimeName(project), releases[0])
		if specErr != nil {
			return
		}
		for _, nodeID := range []string{"local", remote.ID} {
			target, targetErr := nodes.ResolveComposeTarget(context.Background(), nodeID)
			if targetErr == nil {
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
				_ = runner.Targeted(target).DownRelease(cleanupCtx, spec, io.Discard)
				cleanupCancel()
			}
		}
	}
	defer cleanup()
	first := smokeSync(t, ctx, db, service, projectName)
	drift, err := service.Drift(ctx, projectName)
	if err != nil || drift.Status != DriftHealthy || len(drift.Nodes) != 2 {
		t.Fatalf("initial multi-node drift = %#v, %v", drift, err)
	}
	if err := db.Model(&database.Node{}).Where("id = ?", remote.ID).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	service.invalidateDrift(project.ID)
	drift, err = service.Drift(ctx, projectName)
	if err != nil || drift.Status != DriftUnknown || drift.Drifted {
		t.Fatalf("disconnected node drift = %#v, %v", drift, err)
	}
	if err := db.Model(&database.Node{}).Where("id = ?", remote.ID).Update("enabled", true).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&database.DeliveryProject{}).Where("id = ?", project.ID).Update("reconcile_mode", ModeManual).Error; err != nil {
		t.Fatal(err)
	}
	gitClient.SetRevision(gitrepo.Revision{CommitSHA: strings.Repeat("b", 40), CommitAuthor: "Smoke Test", CommitMessage: "second", WorktreePath: smokeWorktree(t, "multi-two")})
	secondTask, err := service.Sync(ctx, projectName, "docker_smoke", Actor{Name: "Docker smoke"})
	if err != nil {
		t.Fatal(err)
	}
	smokeWaitTask(t, ctx, db, secondTask.ID, task.StatusSuccess)
	secondReleases, _ := service.ListReleases(ctx, projectName)
	second := secondReleases[0]
	userID := uint(1)
	if _, err := service.Approve(ctx, projectName, second.ID, Actor{UserID: &userID, Name: "Docker smoke"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&database.Node{}).Where("id = ?", remote.ID).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	secondDeploy, err := service.Deploy(ctx, projectName, second.ID, Actor{Name: "Docker smoke"})
	if err != nil {
		t.Fatal(err)
	}
	smokeWaitTask(t, ctx, db, secondDeploy.ID, task.StatusFailed)
	second, _ = service.GetRelease(ctx, projectName, second.ID)
	if second.Status != StatusPartialFailed {
		t.Fatalf("partial release = %#v", second)
	}
	if err := db.Model(&database.Node{}).Where("id = ?", remote.ID).Update("enabled", true).Error; err != nil {
		t.Fatal(err)
	}
	retry, err := service.RetryFailedNodes(ctx, projectName, second.ID, Actor{Name: "Docker smoke"})
	if err != nil {
		t.Fatal(err)
	}
	smokeWaitTask(t, ctx, db, retry.Task.ID, task.StatusSuccess)
	gitClient.SetRevision(gitrepo.Revision{CommitSHA: strings.Repeat("c", 40), CommitAuthor: "Smoke Test", CommitMessage: "third", WorktreePath: smokeWorktree(t, "multi-three")})
	thirdTask, err := service.Sync(ctx, projectName, "docker_smoke", Actor{Name: "Docker smoke"})
	if err != nil {
		t.Fatal(err)
	}
	smokeWaitTask(t, ctx, db, thirdTask.ID, task.StatusSuccess)
	thirdReleases, _ := service.ListReleases(ctx, projectName)
	third := thirdReleases[0]
	if _, err := service.Approve(ctx, projectName, third.ID, Actor{UserID: &userID, Name: "Docker smoke"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&database.Node{}).Where("id = ?", "local").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	deploy, err := service.Deploy(ctx, projectName, third.ID, Actor{Name: "Docker smoke"})
	if err != nil {
		t.Fatal(err)
	}
	smokeWaitTask(t, ctx, db, deploy.ID, task.StatusFailed)
	if err := db.Model(&database.Node{}).Where("id = ?", "local").Update("enabled", true).Error; err != nil {
		t.Fatal(err)
	}
	rollback, err := service.RollbackFailedNodes(ctx, projectName, third.ID, Actor{Name: "Docker smoke"})
	if err != nil {
		t.Fatal(err)
	}
	smokeWaitTask(t, ctx, db, rollback.Task.ID, task.StatusSuccess)
	var states []database.DeliveryTargetState
	if err := db.Where("project_id = ?", project.ID).Order("node_id").Find(&states).Error; err != nil {
		t.Fatal(err)
	}
	stateByNode := map[string]*uint{}
	for _, state := range states {
		stateByNode[state.NodeID] = state.ActiveReleaseID
	}
	if len(states) != 2 || stateByNode["local"] == nil || *stateByNode["local"] != second.ID || stateByNode[remote.ID] == nil || *stateByNode[remote.ID] != third.ID {
		t.Fatalf("node-specific rollback states = %#v (first=%d second=%d third=%d)", states, first.ID, second.ID, third.ID)
	}
}

func smokeSync(t *testing.T, ctx context.Context, db *gorm.DB, service *Service, projectName string) database.DeliveryRelease {
	t.Helper()
	row, err := service.Sync(ctx, projectName, "docker_smoke", Actor{Name: "Docker smoke"})
	if err != nil {
		t.Fatal(err)
	}
	smokeWaitTask(t, ctx, db, row.ID, task.StatusSuccess)
	releases, err := service.ListReleases(ctx, projectName)
	if err != nil || len(releases) == 0 {
		t.Fatalf("releases = %#v, %v", releases, err)
	}
	return releases[0]
}

func smokeWaitTask(t *testing.T, ctx context.Context, db *gorm.DB, id, want string) database.Task {
	t.Helper()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("task %s did not finish: %v", id, ctx.Err())
		case <-ticker.C:
			var row database.Task
			if err := db.First(&row, "id = ?", id).Error; err != nil {
				t.Fatal(err)
			}
			if row.Status != task.StatusSuccess && row.Status != task.StatusFailed && row.Status != task.StatusCanceled {
				continue
			}
			if row.Status != want {
				var logs []database.TaskLog
				_ = db.Where("task_id = ?", id).Order("created_at ASC").Find(&logs).Error
				t.Fatalf("task %s status = %s, want %s; message=%q logs=%#v", id, row.Status, want, row.Message, logs)
			}
			return row
		}
	}
}

func smokeWorktree(t *testing.T, release string) string {
	t.Helper()
	root := t.TempDir()
	value := `services:
  web:
    image: nginx:alpine
    labels:
      io.suma.smoke.release: "` + release + `"
    healthcheck:
      test: ["CMD-SHELL", "wget -q -O - http://127.0.0.1/ >/dev/null"]
      interval: 1s
      timeout: 2s
      retries: 20
`
	if err := os.WriteFile(filepath.Join(root, "compose.yml"), []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func assertSmokeRuntime(t *testing.T, ctx context.Context, runner compose.Runner, spec compose.ExecutionSpec) {
	t.Helper()
	value, err := runner.PS(ctx, spec, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !runtimeIsHealthy(value) {
		t.Fatalf("Compose runtime is not healthy: %s", value)
	}
}
