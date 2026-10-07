package task

import (
	"context"
	"github.com/suma/suma/server/internal/testutil"
	"strings"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/database"
)

func TestRecentLogsRespectNodeTimeAndBounds(t *testing.T) {
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(db)
	now := time.Now().UTC()
	if err := db.Create(&database.Task{ID: "task", Scope: ScopeNode, NodeID: "local", Name: "task", Type: "test", Status: StatusFailed}).Error; err != nil {
		t.Fatal(err)
	}
	for _, log := range []database.TaskLog{
		{TaskID: "task", Message: "old", CreatedAt: now.Add(-time.Hour)},
		{TaskID: "task", Message: "first", CreatedAt: now.Add(-time.Minute)},
		{TaskID: "task", Message: "second", CreatedAt: now.Add(-30 * time.Second)},
		{TaskID: "task", Message: strings.Repeat("x", 100), CreatedAt: now},
		{TaskID: "other", Message: "other task", CreatedAt: now},
	} {
		if err := db.Create(&log).Error; err != nil {
			t.Fatal(err)
		}
	}
	logs, err := s.RecentLogsForNode(context.Background(), "local", "task", now.Add(-15*time.Minute), 2, 8)
	if err != nil || len(logs) != 2 || logs[0].Message != "second" || logs[1].Message != "xxxxxxxx" {
		t.Fatal("recent logs exceeded bounds or order", logs, err)
	}
	if _, err := s.RecentLogsForNode(context.Background(), "other", "task", now.Add(-time.Hour), 500, 65536); err == nil {
		t.Fatal("cross-node logs were disclosed")
	}
}
