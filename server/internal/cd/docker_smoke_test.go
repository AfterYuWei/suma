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

	"github.com/dockport/dockport/server/internal/compose"
	"github.com/dockport/dockport/server/internal/database"
	gitrepo "github.com/dockport/dockport/server/internal/git"
	"github.com/dockport/dockport/server/internal/secret"
	"github.com/dockport/dockport/server/internal/task"
	"gorm.io/gorm"
)

// TestRealDockerCDDeliveryRollback is intentionally opt-in. It exercises the
// real docker compose CLI against the local engine while keeping Git transport
// deterministic (the real Git adapter has separate local-repository tests).
func TestRealDockerCDDeliveryRollback(t *testing.T) {
	if os.Getenv("DOCKPORT_RUN_DOCKER_SMOKE") != "1" {
		t.Skip("set DOCKPORT_RUN_DOCKER_SMOKE=1 to use the local Docker engine")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	root := t.TempDir()
	db, err := database.Open(filepath.Join(root, "dockport.db"))
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
	projectName := fmt.Sprintf("dockport-cd-smoke-%d", time.Now().UnixNano())
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
      io.dockport.smoke.release: "` + release + `"
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
