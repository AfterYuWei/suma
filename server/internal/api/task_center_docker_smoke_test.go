//go:build dockersmoke

package api

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/cd"
	"github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/deployment"
	gitrepo "github.com/suma/suma/server/internal/git"
	"github.com/suma/suma/server/internal/node"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/task"
	"github.com/suma/suma/server/internal/taskcenter"
	"gorm.io/gorm"
)

// The caller provisions the disposable Engine and verified transport. Only Git
// fetch is controlled; all publication, runtime reads and Task evidence are real.
func globalCDTaskCenterSmoke(t *testing.T, ctx context.Context, root, nodeID, transport string, db *gorm.DB, nodes *node.Service, current *compose.Service, tasks *task.Service, store *secret.Store) {
	t.Helper()
	name := "global-cd-" + transport + "-" + time.Now().UTC().Format("150405")
	worktree := filepath.Join(root, name+"-git")
	if err := os.MkdirAll(worktree, 0700); err != nil {
		t.Fatal(err)
	}
	source := "services:\n  app:\n    image: alpine:3.24\n    command: [sh, -c, 'exec sleep 300']\n    environment:\n      VERSION: one\n    healthcheck:\n      test: [CMD, 'true']\n      interval: 1s\n"
	if err := os.WriteFile(filepath.Join(worktree, "compose.yml"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	runner, err := compose.NewRunner("docker compose")
	if err != nil {
		t.Fatal(err)
	}
	git := &projectCDSmokeGit{root: worktree, commit: strings.Repeat("e", 40)}
	service := cd.NewService(db, git, gitrepo.NewCredentialService(db, store), runner, tasks, audit.NewService(db), store)
	service.SetTargetResolver(nodes)
	kinds := taskcenter.NewRegistry()
	center := taskcenter.New(db, kinds, taskcenter.Dependencies{})
	service.SetTaskCenter(center)
	if err := kinds.Register(service.AutomationKind()); err != nil {
		t.Fatal(err)
	}
	defer center.Stop()
	project, err := service.CreateProjectOnNodes(ctx, name, []string{nodeID})
	if err != nil {
		t.Fatal(err)
	}
	removed := false
	defer func() {
		if !removed {
			cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := service.RemoveProject(cleanup, name, true, true); err != nil {
				t.Errorf("remove isolated CD fixture: %v", err)
			}
		}
	}()
	enabled := true
	if _, err := service.Configure(ctx, name, cd.ConfigureInput{Repository: gitrepo.Repository{CloneURL: "https://git.example.test/global.git", RefType: "branch", Ref: "main", ComposeFiles: []string{"compose.yml"}, Authentication: gitrepo.Authentication{Source: "none"}}, ReconcileMode: cd.ModeAuto, SyncIntervalSeconds: 30, DeploymentTimeout: 15, AutomationEnabled: &enabled, NodeIDs: []string{nodeID}}); err != nil {
		t.Fatal(err)
	}
	wait := func(id string) database.Task {
		t.Helper()
		if err := tasks.Wait(ctx, id); err != nil {
			t.Fatal(err)
		}
		row, err := tasks.Get(ctx, id)
		if err != nil || row.Status != task.StatusSuccess || row.Scope != task.ScopeControlPlane {
			t.Fatalf("global CD Task failed: %+v %v", row, err)
		}
		return row
	}
	checked, err := service.Sync(ctx, name, "manual", cd.Actor{Name: "Task Center smoke"})
	if err != nil {
		t.Fatal(err)
	}
	wait(checked.ID)
	var manual database.TaskCenterScheduleRun
	if err := db.Where("task_id = ?", checked.ID).First(&manual).Error; err != nil || manual.ScheduleID == nil || manual.TriggeredBy != taskcenter.ReasonManual || manual.Status != "started" {
		t.Fatalf("global check bypassed unified admission: %+v %v", manual, err)
	}
	releases, err := service.ListReleases(ctx, name)
	if err != nil || len(releases) != 1 || releases[0].Status != cd.StatusAwaitingApproval {
		t.Fatal("manual check did not produce a candidate", err)
	}
	adapter, err := nodes.Runtime(ctx, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	containers, err := adapter.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, container := range containers {
		if container.Labels["com.docker.compose.project"] == project.DeploymentName {
			t.Fatal("manual check implicitly published a candidate")
		}
	}
	prepared, err := service.PrepareRelease(ctx, name, deployment.PrepareReleaseInput{}, cd.Actor{Name: "Confirmed smoke publication"})
	if err != nil {
		t.Fatal(err)
	}
	wait(prepared.ID)
	plan, err := service.PreparedRelease(ctx, name, prepared.ID)
	if err != nil || !plan.ImagesPinned || len(plan.Nodes) != 1 || plan.Nodes[0].NodeID != nodeID {
		t.Fatal("missing global pinned release plan", err)
	}
	containers, err = adapter.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, container := range containers {
		if container.Labels["com.docker.compose.project"] == project.DeploymentName {
			t.Fatal("preparing a global release changed containers")
		}
	}
	publication, err := service.ConfirmRelease(ctx, name, plan.ID, deployment.ConfirmReleaseInput{}, cd.Actor{Name: "Confirmed smoke publication"})
	if err != nil {
		t.Fatal(err)
	}
	wait(publication.Task.ID)
	replayed, err := service.ConfirmRelease(ctx, name, plan.ID, deployment.ConfirmReleaseInput{}, cd.Actor{Name: "Confirmed smoke publication"})
	if err != nil || replayed.Task.ID != publication.Task.ID || replayed.RecordID != publication.RecordID {
		t.Fatal("global release confirmation was not idempotent", err)
	}
	children, err := tasks.Children(ctx, publication.Task.ID)
	if err != nil || len(children) != 1 || children[0].NodeID != nodeID || children[0].Status != task.StatusSuccess {
		t.Fatalf("publication lost node Task evidence: %+v %v", children, err)
	}
	original, err := service.ReleasesForTask(ctx, checked.ID)
	if err != nil || len(original) != 1 || original[0].ID != releases[0].ID {
		t.Fatal("publication erased original candidate association", err)
	}
	history, err := service.ListHistory(ctx, cd.HistoryQuery{TaskID: children[0].ID})
	if err != nil || history.Total != 1 || history.Items[0].Source != "continuous_delivery" {
		t.Fatalf("node Task cannot resolve original delivery record: %+v %v", history, err)
	}
	before, err := current.Services(ctx, project.DeploymentName)
	if err != nil || len(before) != 1 || before[0].State != "running" {
		t.Fatal("confirmed publication did not reach selected Engine", err)
	}
	git.commit = strings.Repeat("f", 40)
	if err := os.WriteFile(filepath.Join(worktree, "compose.yml"), []byte(strings.Replace(source, "VERSION: one", "VERSION: two", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	rule, err := center.Schedule(ctx, *manual.ScheduleID)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&database.TaskCenterSchedule{}).Where("id = ?", rule.ID).Update("next_run_at", time.Now().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	if err := center.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	var scheduled database.TaskCenterScheduleRun
	if err := db.Where("schedule_id = ? AND triggered_by = ?", rule.ID, taskcenter.ReasonScheduled).Order("id DESC").First(&scheduled).Error; err != nil || scheduled.TaskID == "" || scheduled.ScheduledFor == nil || scheduled.Status != "started" {
		t.Fatalf("center did not dispatch global CD interval: %+v %v", scheduled, err)
	}
	wait(scheduled.TaskID)
	after, err := current.Services(ctx, project.DeploymentName)
	if err != nil || len(after) != 1 || after[0].State != "running" || after[0].ID == before[0].ID {
		t.Fatal("interval publication did not recreate selected Engine container", err)
	}
	advanced, err := center.Schedule(ctx, rule.ID)
	if err != nil || advanced.Version != rule.Version || advanced.NextRunAt == nil {
		t.Fatal("normal interval changed configuration version or lost cadence", err)
	}
	if err := service.RemoveProject(ctx, name, true, true); err != nil {
		t.Fatal(err)
	}
	removed = true
	archived, err := center.Schedule(ctx, rule.ID)
	if err != nil || archived.ArchivedAt == nil {
		t.Fatal("business deletion did not archive its rule", err)
	}
	retained, err := tasks.Record(ctx, checked.ID)
	if err != nil || retained.ScheduleID == nil || *retained.ScheduleID != rule.ID || retained.TriggeredBy != taskcenter.ReasonManual {
		t.Fatal("business deletion erased Task/trigger association", err)
	}
}
