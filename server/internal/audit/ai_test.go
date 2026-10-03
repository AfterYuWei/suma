package audit

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/task"
)

func TestLegacyAIImportPreservesLinksAndNeverDuplicates(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s := NewService(db)
	db.Create(&database.Node{ID: "node", Name: "Node", Enabled: true})
	db.Create(&database.AIRun{ID: "run", NodeID: "node", UserID: 1, Status: "completed"})
	db.Create(&database.AIOperation{ID: "operation", RunID: "run", NodeID: "node", TaskID: "task", Status: "completed"})
	at := time.Date(2026, 10, 2, 20, 0, 0, 0, time.FixedZone("UTC+8", 8*3600))
	old := database.AIAudit{RunID: "run", OperationID: "operation", UserID: 1, Action: "execution", Source: "chat", ExternalUserID: "platform-user", ChatID: "chat", Resource: "container", Result: "completed: TOKEN=old-private-value", CreatedAt: at}
	if err := db.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := s.ImportLegacyAI(ctx); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.List(ctx, 100)
	if err != nil || len(rows) != 1 {
		t.Fatal("historical record duplicated or lost", rows, err)
	}
	r := rows[0]
	if r.Action != "ai.execution" || r.Source != "chat" || r.NodeID != "node" || r.Scope != task.ScopeNode || r.TaskID != "task" || r.RunID != "run" || r.OperationID != "operation" || !r.CreatedAt.Equal(at) || r.ExternalUserID != "platform-user" || strings.Contains(r.Details, "old-private-value") {
		t.Fatal("import lost correlation/privacy", r)
	}
	if err := s.RecordControlPlane(ctx, nil, "login", "account", "admin", "127.0.0.1", "success"); err != nil {
		t.Fatal(err)
	}
	part, err := s.ListAI(ctx, 100)
	if err != nil || len(part) != 1 || part[0].ID != r.ID || part[0].TaskID != "task" {
		t.Fatal("AI view is not the same audit subset", part, err)
	}
	nodeRows, _ := s.ListForNode(ctx, 100, "node")
	controlRows, _ := s.ListControlPlane(ctx, 100)
	if len(nodeRows) != 1 || len(controlRows) != 1 || controlRows[0].Action != "login" {
		t.Fatal("node/control-plane audit filtering mixed")
	}
	// Existing installations stored decision outcomes in the action field.
	rejected := database.AIAudit{RunID: "run", OperationID: "operation", UserID: 1, Action: "rejected", Source: "site", Result: "recorded"}
	if err := db.Create(&rejected).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.ImportLegacyAI(ctx); err != nil {
		t.Fatal(err)
	}
	nodeRows, err = s.ListForNode(ctx, 100, "node")
	if err != nil || len(nodeRows) != 2 || nodeRows[0].Action != "ai.rejected" || nodeRows[0].Result != "denied" {
		t.Fatal("historical rejection was reported as pending", nodeRows, err)
	}
}
