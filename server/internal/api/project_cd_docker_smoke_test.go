//go:build dockersmoke

package api

import (
	"context"
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
	"github.com/suma/suma/server/internal/node"
	"github.com/suma/suma/server/internal/projectcd"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/task"
	"github.com/suma/suma/server/internal/taskcenter"
	"gorm.io/gorm"
)

// Only Git fetch is controlled. Compose, health, node transport, volumes and
// container IDs are verified against the fixture's disposable Docker engine.
type projectCDSmokeGit struct{ root, commit string }

func (g projectCDSmokeGit) TestConnection(context.Context, gitrepo.SyncRequest) (gitrepo.ConnectionResult, error) {
	return gitrepo.ConnectionResult{CommitSHA: g.commit, RefChecked: true}, nil
}
func (g projectCDSmokeGit) Sync(context.Context, gitrepo.SyncRequest, io.Writer) (gitrepo.Revision, error) {
	return gitrepo.Revision{CommitSHA: g.commit, WorktreePath: g.root}, nil
}
func (g projectCDSmokeGit) Verify(context.Context, string, string) error { return nil }
func (g projectCDSmokeGit) Cleanup(uint) error                           { return nil }

func projectCDTransportSmoke(t *testing.T, ctx context.Context, root, nodeID, transport string, db *gorm.DB, nodes *node.Service, current *compose.Service, tasks *task.Service, store *secret.Store) {
	t.Helper()
	name := "project-cd-" + transport + "-" + time.Now().UTC().Format("150405")
	source := "services:\n  app:\n    image: alpine:3.24\n    command: [sh, -c, 'exec sleep 300']\n    environment:\n      CD_VALUE: ${CD_VALUE}\n    volumes:\n      - data:/data\n    healthcheck:\n      test: [CMD, 'true']\n      interval: 1s\nvolumes:\n  data: {}\n"
	if _, err := current.Create(ctx, name, source, ""); err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(root, name+"-git")
	if err := os.MkdirAll(worktree, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "compose.yml"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	runner, err := compose.NewRunner("docker compose")
	if err != nil {
		t.Fatal(err)
	}
	git := &projectCDSmokeGit{root: worktree, commit: strings.Repeat("c", 40)}
	service := projectcd.New(db, git, gitrepo.NewCredentialService(db, store), credential.NewRegistryService(db, store), runner, tasks, audit.NewService(db), store, projectcd.Dependencies{Project: func(context.Context, string) (*compose.Service, error) { return current, nil }, Target: func(ctx context.Context, id string) (compose.Target, error) {
		target, _, err := nodes.ComposeTarget(ctx, id)
		return target, err
	}})
	kinds := taskcenter.NewRegistry()
	center := taskcenter.New(db, kinds, taskcenter.Dependencies{NodeName: func(ctx context.Context, id string) (string, error) {
		view, err := nodes.Get(ctx, id)
		return view.Name, err
	}})
	service.SetTaskCenter(center)
	if err := kinds.Register(service.AutomationKind()); err != nil {
		t.Fatal(err)
	}
	defer center.Stop()
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		config, err := service.Get(cleanup, nodeID, name)
		if err == nil && config.Enabled {
			operation, err := service.Detach(cleanup, nodeID, name, config.Revision, projectcd.Actor{Name: "Smoke cleanup"})
			if err == nil {
				_ = tasks.Wait(cleanup, operation.ID)
			}
		}
		_ = current.ForceRemove(cleanup, name, false)
	})
	variable := "private-smoke-value"
	enabled := true
	config, err := service.Configure(ctx, nodeID, name, projectcd.ConfigureInput{Repository: gitrepo.Repository{CloneURL: "https://git.example.test/deployment.git", RefType: "branch", Ref: "main", ComposeFiles: []string{"compose.yml"}, Authentication: gitrepo.Authentication{Source: "none"}}, Environment: []deployment.EnvironmentVariable{{Name: "CD_VALUE", Value: &variable}}, ReconcileMode: "auto", AutoRollback: true, AutomationEnabled: &enabled, DeploymentTimeout: 10})
	if err != nil {
		t.Fatal(err)
	}
	join := func(operation database.Task, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		if err := tasks.Wait(ctx, operation.ID); err != nil {
			t.Fatal(err)
		}
		result, err := tasks.Get(ctx, operation.ID)
		if err != nil || result.Status != task.StatusSuccess {
			t.Fatalf("CD transport %s: %s (%v)", transport, result.Message, err)
		}
	}
	checked, err := service.Check(ctx, nodeID, name, "manual", projectcd.Actor{Name: "Smoke"})
	join(checked, err)
	config, err = service.Get(ctx, nodeID, name)
	if err != nil || config.Candidate == nil {
		t.Fatal("missing checked candidate", err)
	}
	if rows, err := current.Services(ctx, name); err != nil || len(rows) != 0 {
		t.Fatal("manual center check published before application confirmation", err)
	}
	var manual database.TaskCenterScheduleRun
	if err := db.Where("task_id = ?", checked.ID).First(&manual).Error; err != nil || manual.Status != "started" || manual.TriggeredBy != taskcenter.ReasonManual || manual.ScheduleID == nil {
		t.Fatalf("project CD check bypassed unified admission: %+v %v", manual, err)
	}
	applied, err := service.Apply(ctx, nodeID, name, config.Candidate.ID, config.Revision, false, projectcd.Actor{Name: "Smoke"})
	join(applied, err)
	before, err := current.Services(ctx, name)
	if err != nil || len(before) != 1 || before[0].State != "running" {
		t.Fatal("CD did not apply to selected node", err)
	}
	failedWorktree := filepath.Join(root, name+"-unhealthy-git")
	if err := os.MkdirAll(failedWorktree, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(failedWorktree, "compose.yml"), []byte(strings.Replace(source, "test: [CMD, 'true']", "test: [CMD, 'false']", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	git.root, git.commit = failedWorktree, strings.Repeat("d", 40)
	if err := db.Model(&database.TaskCenterSchedule{}).Where("id = ?", *manual.ScheduleID).Update("next_run_at", time.Now().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	if err := center.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	var scheduled database.TaskCenterScheduleRun
	if err := db.Where("schedule_id = ? AND triggered_by = ?", *manual.ScheduleID, taskcenter.ReasonScheduled).Order("id DESC").First(&scheduled).Error; err != nil || scheduled.TaskID == "" || scheduled.ScheduledFor == nil || scheduled.Status != "started" {
		t.Fatalf("center did not dispatch real project CD check: %+v %v", scheduled, err)
	}
	if err := tasks.Wait(ctx, scheduled.TaskID); err != nil {
		t.Fatal(err)
	}
	failedTask, err := tasks.Get(ctx, scheduled.TaskID)
	if err != nil || failedTask.Status != task.StatusFailed {
		t.Fatal("unhealthy delivery was not retained as failed", err)
	}
	config, err = service.Get(ctx, nodeID, name)
	if err != nil || config.ReconcileMode != "manual" || config.Applied == nil || config.Applied.Operation != "auto_rollback" || config.Applied.CommitSHA != strings.Repeat("c", 40) {
		t.Fatal("automatic recovery did not restore the prior successful snapshot", err)
	}
	restored, err := current.Services(ctx, name)
	if err != nil || len(restored) != 1 || restored[0].State != "running" || restored[0].ID == before[0].ID {
		t.Fatal("automatic recovery did not recreate the container", err)
	}
	before = restored
	detached, err := service.Detach(ctx, nodeID, name, config.Revision, projectcd.Actor{Name: "Smoke"})
	join(detached, err)
	after, err := current.Services(ctx, name)
	if err != nil || len(after) != 1 || after[0].ID != before[0].ID {
		t.Fatal("manual conversion changed running containers", err)
	}
}
