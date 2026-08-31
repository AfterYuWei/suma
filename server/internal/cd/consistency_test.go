package cd

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/task"
)

type delayedPSRunner struct {
	*fakeComposeRunner
	delay time.Duration
}

func (r *delayedPSRunner) PS(ctx context.Context, spec compose.ExecutionSpec, output io.Writer) (string, error) {
	timer := time.NewTimer(r.delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return r.fakeComposeRunner.PS(ctx, spec, output)
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func TestDriftAggregatesNodesConcurrentlyAndCaches(t *testing.T) {
	harness := newCDHarness(t, ModeManual)
	for _, id := range []string{"node-a", "node-b"} {
		if err := harness.db.Create(&database.Node{ID: id, Name: strings.ToUpper(id), ConnectionType: "unix", Endpoint: "unix:///" + id + ".sock", TLSMode: "disabled", AllowedBindRootsJSON: "[]", Enabled: true}).Error; err != nil {
			t.Fatal(err)
		}
		if err := harness.db.Create(&database.DeliveryProjectNode{ProjectID: harness.project.ID, NodeID: id}).Error; err != nil {
			t.Fatal(err)
		}
	}
	commit := strings.Repeat("a", 40)
	release := database.DeliveryRelease{ProjectID: harness.project.ID, CommitSHA: commit, Status: StatusSucceeded, WorktreePath: harness.git.revision.WorktreePath, ComposeFilesJSON: `["compose.yml"]`}
	if err := harness.db.Create(&release).Error; err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"node-a", "node-b"} {
		releaseID := release.ID
		if err := harness.db.Create(&database.DeliveryTargetState{ProjectID: harness.project.ID, NodeID: id, ActiveReleaseID: &releaseID, ObservedCommit: commit}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := harness.db.Model(&database.DeliveryProject{}).Where("id = ?", harness.project.ID).Update("desired_commit", commit).Error; err != nil {
		t.Fatal(err)
	}
	healthyA := &fakeComposeRunner{health: `[{"State":"running","Health":"healthy"}]`}
	healthyB := &fakeComposeRunner{health: `[{"State":"running","Health":"healthy"}]`}
	runnerA := &delayedPSRunner{fakeComposeRunner: healthyA, delay: 150 * time.Millisecond}
	runnerB := &delayedPSRunner{fakeComposeRunner: healthyB, delay: 150 * time.Millisecond}
	harness.service.compose = &targetedFakeRunner{fakeComposeRunner: harness.runner, runners: map[string]compose.Runner{"node-a": runnerA, "node-b": runnerB}}
	harness.service.SetTargetResolver(fakeTargetResolver{})
	started := time.Now()
	drift, err := harness.service.Drift(context.Background(), harness.project.Name)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= 260*time.Millisecond {
		t.Fatalf("node probes were not concurrent: %s", elapsed)
	}
	if drift.Status != DriftHealthy || drift.Drifted || !drift.RuntimeHealthy || len(drift.Nodes) != 2 {
		t.Fatalf("healthy drift = %#v", drift)
	}
	beforeA, beforeB := len(healthyA.Calls()), len(healthyB.Calls())
	if _, err := harness.service.Drift(context.Background(), harness.project.Name); err != nil {
		t.Fatal(err)
	}
	if len(healthyA.Calls()) != beforeA || len(healthyB.Calls()) != beforeB {
		t.Fatal("five-second drift cache did not suppress duplicate probes")
	}
	healthyA.mu.Lock()
	healthyA.health = `[{"State":"exited","Health":"unhealthy"}]`
	healthyA.mu.Unlock()
	healthyB.mu.Lock()
	healthyB.ps = errors.New("node unavailable")
	healthyB.mu.Unlock()
	harness.service.invalidateDrift(harness.project.ID)
	drift, err = harness.service.Drift(context.Background(), harness.project.Name)
	if err != nil {
		t.Fatal(err)
	}
	if drift.Status != DriftDegraded || !drift.Drifted || drift.RuntimeHealthy || drift.Nodes[0].Status != DriftDegraded || drift.Nodes[1].Status != DriftUnknown {
		t.Fatalf("failure must take precedence over unknown: %#v", drift)
	}
}

func TestRecoverInterruptsNodeHistoryAndIsIdempotent(t *testing.T) {
	harness := newCDHarness(t, ModeManual)
	for _, id := range []string{"node-a", "node-b"} {
		if err := harness.db.Create(&database.DeliveryProjectNode{ProjectID: harness.project.ID, NodeID: id}).Error; err != nil {
			t.Fatal(err)
		}
	}
	old := database.DeliveryRelease{ProjectID: harness.project.ID, CommitSHA: strings.Repeat("a", 40), Status: StatusSucceeded}
	if err := harness.db.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	current := database.DeliveryRelease{ProjectID: harness.project.ID, CommitSHA: strings.Repeat("b", 40), Status: StatusDeploying, PreviousReleaseID: &old.ID}
	if err := harness.db.Create(&current).Error; err != nil {
		t.Fatal(err)
	}
	succeeded := database.DeliveryReleaseDeployment{ReleaseID: current.ID, NodeID: "node-a", NodeName: "A", Status: StatusSucceeded, PreviousReleaseID: &old.ID}
	interrupted := database.DeliveryReleaseDeployment{ReleaseID: current.ID, NodeID: "node-b", NodeName: "B", Status: StatusDeploying, PreviousReleaseID: &old.ID}
	if err := harness.db.Create(&succeeded).Error; err != nil {
		t.Fatal(err)
	}
	if err := harness.db.Create(&interrupted).Error; err != nil {
		t.Fatal(err)
	}
	attempts := []database.DeliveryDeploymentAttempt{
		{DeploymentID: succeeded.ID, Operation: AttemptDeploy, TargetReleaseID: current.ID, Status: StatusSucceeded},
		{DeploymentID: interrupted.ID, Operation: AttemptDeploy, TargetReleaseID: current.ID, Status: StatusDeploying},
	}
	if err := harness.db.Create(&attempts).Error; err != nil {
		t.Fatal(err)
	}
	if err := harness.db.Create(&[]database.Task{{ID: "parent", Scope: task.ScopeControlPlane, Type: "cd.deploy", Name: "parent", Status: task.StatusRunning}, {ID: "child", Scope: task.ScopeNode, NodeID: "node-b", NodeName: "B", Type: "cd.deploy.node", Name: "child", Status: task.StatusPending}}).Error; err != nil {
		t.Fatal(err)
	}
	currentID, oldID := current.ID, old.ID
	if err := harness.db.Create(&[]database.DeliveryTargetState{{ProjectID: harness.project.ID, NodeID: "node-a", ActiveReleaseID: &currentID, ObservedCommit: current.CommitSHA}, {ProjectID: harness.project.ID, NodeID: "node-b", ActiveReleaseID: &oldID, ObservedCommit: old.CommitSHA}}).Error; err != nil {
		t.Fatal(err)
	}
	if err := harness.service.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	var recoveredAttempt database.DeliveryDeploymentAttempt
	if err := harness.db.First(&recoveredAttempt, attempts[1].ID).Error; err != nil || recoveredAttempt.Status != StatusInterrupted {
		t.Fatalf("attempt = %#v, %v", recoveredAttempt, err)
	}
	var recoveredDeployment database.DeliveryReleaseDeployment
	_ = harness.db.First(&recoveredDeployment, interrupted.ID).Error
	var recoveredRelease database.DeliveryRelease
	_ = harness.db.First(&recoveredRelease, current.ID).Error
	var recoveredProject database.DeliveryProject
	_ = harness.db.First(&recoveredProject, harness.project.ID).Error
	var recoveredTasks []database.Task
	_ = harness.db.Order("id").Find(&recoveredTasks, "id IN ?", []string{"parent", "child"}).Error
	if recoveredDeployment.Status != StatusFailed || recoveredRelease.Status != StatusPartialFailed || recoveredProject.ActiveReleaseID != nil || recoveredTasks[0].Status != task.StatusCanceled || recoveredTasks[1].Status != task.StatusCanceled {
		t.Fatalf("recovered state: deployment=%#v release=%#v project=%#v tasks=%#v", recoveredDeployment, recoveredRelease, recoveredProject, recoveredTasks)
	}
	finishedAt := recoveredAttempt.FinishedAt
	if err := harness.service.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := harness.db.First(&recoveredAttempt, attempts[1].ID).Error; err != nil || !reflect.DeepEqual(recoveredAttempt.FinishedAt, finishedAt) {
		t.Fatalf("second recovery changed terminal state: %#v, %v", recoveredAttempt, err)
	}
}

func TestRetryIncludesAutoRolledBackNode(t *testing.T) {
	harness := newCDHarness(t, ModeManual)
	if err := harness.db.Create(&database.Node{ID: "node-a", Name: "A", ConnectionType: "unix", Endpoint: "unix:///a.sock", TLSMode: "disabled", AllowedBindRootsJSON: "[]", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := harness.db.Create(&database.DeliveryProjectNode{ProjectID: harness.project.ID, NodeID: "node-a"}).Error; err != nil {
		t.Fatal(err)
	}
	old := database.DeliveryRelease{ProjectID: harness.project.ID, CommitSHA: strings.Repeat("a", 40), Status: StatusSucceeded, WorktreePath: harness.git.revision.WorktreePath, ComposeFilesJSON: `["compose.yml"]`}
	current := database.DeliveryRelease{ProjectID: harness.project.ID, CommitSHA: strings.Repeat("b", 40), Status: StatusRolledBack, WorktreePath: harness.git.revision.WorktreePath, ComposeFilesJSON: `["compose.yml"]`}
	if err := harness.db.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	current.PreviousReleaseID = &old.ID
	if err := harness.db.Create(&current).Error; err != nil {
		t.Fatal(err)
	}
	deployment := database.DeliveryReleaseDeployment{ReleaseID: current.ID, NodeID: "node-a", NodeName: "A", Status: StatusRolledBack, PreviousReleaseID: &old.ID, RollbackResult: "succeeded"}
	if err := harness.db.Create(&deployment).Error; err != nil {
		t.Fatal(err)
	}
	if err := harness.db.Create(&database.DeliveryDeploymentAttempt{DeploymentID: deployment.ID, Operation: AttemptAutoRollback, TargetReleaseID: old.ID, Status: StatusRolledBack}).Error; err != nil {
		t.Fatal(err)
	}
	oldID := old.ID
	if err := harness.db.Create(&database.DeliveryTargetState{ProjectID: harness.project.ID, NodeID: "node-a", ActiveReleaseID: &oldID, ObservedCommit: old.CommitSHA}).Error; err != nil {
		t.Fatal(err)
	}
	healthy := &fakeComposeRunner{health: `[{"State":"running","Health":"healthy"}]`}
	harness.service.compose = &targetedFakeRunner{fakeComposeRunner: harness.runner, runners: map[string]compose.Runner{"node-a": healthy}}
	harness.service.SetTargetResolver(fakeTargetResolver{})
	result, err := harness.service.RetryFailedNodes(context.Background(), harness.project.Name, current.ID, Actor{Name: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.NodeIDs, []string{"node-a"}) {
		t.Fatalf("retry targets = %#v", result.NodeIDs)
	}
	waitTask(t, harness.db, result.Task.ID, task.StatusSuccess)
	var retry database.DeliveryDeploymentAttempt
	if err := harness.db.Where("deployment_id = ? AND operation = ?", deployment.ID, AttemptRetry).First(&retry).Error; err != nil || retry.Status != StatusSucceeded {
		t.Fatalf("retry attempt = %#v, %v", retry, err)
	}
	var state database.DeliveryTargetState
	_ = harness.db.Where("project_id = ? AND node_id = ?", harness.project.ID, "node-a").First(&state).Error
	if state.ActiveReleaseID == nil || *state.ActiveReleaseID != current.ID {
		t.Fatalf("target state = %#v", state)
	}
}

func TestRollbackFailedNodesRestoresEachNodesPreviousRelease(t *testing.T) {
	harness := newCDHarness(t, ModeManual)
	for _, id := range []string{"node-a", "node-b"} {
		if err := harness.db.Create(&database.Node{ID: id, Name: strings.ToUpper(id), ConnectionType: "unix", Endpoint: "unix:///" + id + ".sock", TLSMode: "disabled", AllowedBindRootsJSON: "[]", Enabled: true}).Error; err != nil {
			t.Fatal(err)
		}
		if err := harness.db.Create(&database.DeliveryProjectNode{ProjectID: harness.project.ID, NodeID: id}).Error; err != nil {
			t.Fatal(err)
		}
	}
	oldA := database.DeliveryRelease{ProjectID: harness.project.ID, CommitSHA: strings.Repeat("a", 40), Status: StatusSucceeded, WorktreePath: harness.git.revision.WorktreePath, ComposeFilesJSON: `["compose.yml"]`}
	oldB := database.DeliveryRelease{ProjectID: harness.project.ID, CommitSHA: strings.Repeat("b", 40), Status: StatusSucceeded, WorktreePath: harness.git.revision.WorktreePath, ComposeFilesJSON: `["compose.yml"]`}
	current := database.DeliveryRelease{ProjectID: harness.project.ID, CommitSHA: strings.Repeat("c", 40), Status: StatusFailed, WorktreePath: harness.git.revision.WorktreePath, ComposeFilesJSON: `["compose.yml"]`}
	for _, release := range []*database.DeliveryRelease{&oldA, &oldB, &current} {
		if err := harness.db.Create(release).Error; err != nil {
			t.Fatal(err)
		}
	}
	deployments := []database.DeliveryReleaseDeployment{
		{ReleaseID: current.ID, NodeID: "node-a", NodeName: "NODE-A", Status: StatusFailed, PreviousReleaseID: &oldA.ID},
		{ReleaseID: current.ID, NodeID: "node-b", NodeName: "NODE-B", Status: StatusFailed, PreviousReleaseID: &oldB.ID},
	}
	if err := harness.db.Create(&deployments).Error; err != nil {
		t.Fatal(err)
	}
	oldAID, oldBID := oldA.ID, oldB.ID
	if err := harness.db.Create(&[]database.DeliveryTargetState{{ProjectID: harness.project.ID, NodeID: "node-a", ActiveReleaseID: &oldAID, ObservedCommit: oldA.CommitSHA}, {ProjectID: harness.project.ID, NodeID: "node-b", ActiveReleaseID: &oldBID, ObservedCommit: oldB.CommitSHA}}).Error; err != nil {
		t.Fatal(err)
	}
	healthyA := &fakeComposeRunner{health: `[{"State":"running","Health":"healthy"}]`}
	healthyB := &fakeComposeRunner{health: `[{"State":"running","Health":"healthy"}]`}
	harness.service.compose = &targetedFakeRunner{fakeComposeRunner: harness.runner, runners: map[string]compose.Runner{"node-a": healthyA, "node-b": healthyB}}
	harness.service.SetTargetResolver(fakeTargetResolver{})
	result, err := harness.service.RollbackFailedNodes(context.Background(), harness.project.Name, current.ID, Actor{Name: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, harness.db, result.Task.ID, task.StatusSuccess)
	var states []database.DeliveryTargetState
	if err := harness.db.Where("project_id = ?", harness.project.ID).Order("node_id").Find(&states).Error; err != nil {
		t.Fatal(err)
	}
	if len(states) != 2 || states[0].ActiveReleaseID == nil || *states[0].ActiveReleaseID != oldA.ID || states[1].ActiveReleaseID == nil || *states[1].ActiveReleaseID != oldB.ID {
		t.Fatalf("per-node rollback states = %#v", states)
	}
	var attempts []database.DeliveryDeploymentAttempt
	if err := harness.db.Where("operation = ?", AttemptManualRollback).Order("deployment_id").Find(&attempts).Error; err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 || attempts[0].TargetReleaseID != oldA.ID || attempts[1].TargetReleaseID != oldB.ID {
		t.Fatalf("rollback attempts = %#v", attempts)
	}
	var aggregate database.DeliveryRelease
	_ = harness.db.First(&aggregate, current.ID).Error
	if aggregate.Status != StatusRolledBack {
		t.Fatalf("release status = %s", aggregate.Status)
	}
}
