package task

import (
	"context"
	"github.com/dockport/dockport/server/internal/database"
	"path/filepath"
	"testing"
	"time"
)

func TestTaskLifecycle(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(db)
	row, err := service.Start("test", "Test task", func(_ context.Context, report Reporter) error { report(50, "halfway"); return nil })
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var current database.Task
		db.First(&current, "id = ?", row.ID)
		if current.Status == StatusSuccess {
			logs, _ := service.Logs(context.Background(), row.ID)
			if len(logs) < 2 {
				t.Fatal("expected task logs")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("task did not complete")
}
