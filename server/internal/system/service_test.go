package system

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/task"
	"gorm.io/gorm"
)

type recordingPruner struct {
	ctx        context.Context
	reported   []string
	pruneError error
}

func (p *recordingPruner) Prune(ctx context.Context, report task.Reporter) error {
	p.ctx = ctx
	report(10, "Pruning stopped containers")
	if p.pruneError != nil {
		return p.pruneError
	}
	report(85, "Pruning unused anonymous volumes")
	return nil
}

func newTestService(t *testing.T, pruner Adapter) (*Service, *gorm.DB) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "system.db"))
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(pruner, task.NewService(db))
	return service, db
}

func waitTask(t *testing.T, db *gorm.DB, id string) database.Task {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var row database.Task
		if err := db.First(&row, "id = ?", id).Error; err != nil {
			t.Fatal(err)
		}
		if row.Status != task.StatusPending && row.Status != task.StatusRunning {
			return row
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for prune task")
	return database.Task{}
}

func logMessages(t *testing.T, db *gorm.DB, id string) []database.TaskLog {
	t.Helper()
	var logs []database.TaskLog
	if err := db.Where("task_id = ?", id).Find(&logs).Error; err != nil {
		t.Fatal(err)
	}
	return logs
}

func TestPruneForNodeRunsAdapterWithReporter(t *testing.T) {
	pruner := &recordingPruner{}
	service, db := newTestService(t, pruner)

	row, err := service.PruneForNode("edge", "Edge Node")
	if err != nil {
		t.Fatal(err)
	}
	if row.Type != "system.prune" || row.NodeID != "edge" || row.NodeName != "Edge Node" {
		t.Fatalf("unexpected task metadata: %+v", row)
	}

	finished := waitTask(t, db, row.ID)
	if finished.Status != task.StatusSuccess {
		t.Fatalf("expected successful prune task, got %q (%s)", finished.Status, finished.Message)
	}
	if pruner.ctx == nil {
		t.Fatal("adapter must receive a context")
	}
	logs := logMessages(t, db, row.ID)
	messages := make([]string, 0, len(logs))
	for _, entry := range logs {
		messages = append(messages, entry.Message)
	}
	found := func(want string) bool {
		for _, message := range messages {
			if message == want {
				return true
			}
		}
		return false
	}
	// The adapter's reporter output must land in the task log stream.
	if !found("Pruning stopped containers") || !found("Pruning unused anonymous volumes") {
		t.Fatalf("expected reporter messages in task logs: %v", messages)
	}
}

func TestPruneDefaultsToLocalNode(t *testing.T) {
	service, db := newTestService(t, &recordingPruner{})
	row, err := service.Prune()
	if err != nil {
		t.Fatal(err)
	}
	if row.NodeID != "local" || row.NodeName != "Local" || row.Type != "system.prune" ||
		row.Name != "Prune unused Docker resources" {
		t.Fatalf("unexpected default prune metadata: %+v", row)
	}
	waitTask(t, db, row.ID)
}

func TestPruneFailureIsRecordedOnTask(t *testing.T) {
	pruner := &recordingPruner{pruneError: errors.New("prune volumes: denied by policy")}
	service, db := newTestService(t, pruner)

	row, err := service.PruneForNode("edge", "Edge Node")
	if err != nil {
		t.Fatal(err)
	}
	finished := waitTask(t, db, row.ID)
	if finished.Status != task.StatusFailed {
		t.Fatalf("expected failed task, got %q", finished.Status)
	}
	if finished.Message != "prune volumes: denied by policy" {
		t.Fatalf("expected the adapter error on the task, got %q", finished.Message)
	}
}
