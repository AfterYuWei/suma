package audit

import (
	"context"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/task"
	"github.com/suma/suma/server/internal/testutil"
	"strings"
	"testing"
)

func TestAIUsesGlobalAuditWithLinksAndPrivacy(t *testing.T) {
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s := NewService(db)
	db.Create(&database.Node{ID: "node", Name: "Node", Enabled: true})
	db.Create(&database.AIRun{ID: "run", NodeID: "node", UserID: 1, Status: "completed"})
	db.Create(&database.AIOperation{ID: "operation", RunID: "run", NodeID: "node", TaskID: "task", Status: "completed"})
	if err := s.RecordAI(ctx, db, database.AIAudit{RunID: "run", OperationID: "operation", UserID: 1, Action: "execution", Source: "chat", ExternalUserID: "platform-user", ChatID: "chat", Resource: "container", Result: "completed: TOKEN=private-value"}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.List(ctx, 100)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	r := rows[0]
	if r.Action != "ai.execution" || r.Scope != task.ScopeNode || r.NodeID != "node" || r.TaskID != "task" || r.RunID != "run" || r.OperationID != "operation" || strings.Contains(r.Details, "private-value") {
		t.Fatal("audit links/privacy lost", r)
	}
	if err := s.RecordControlPlane(ctx, nil, "login", "account", "admin", "127.0.0.1", "success"); err != nil {
		t.Fatal(err)
	}
	part, err := s.ListAI(ctx, 100)
	if err != nil || len(part) != 1 || part[0].ID != r.ID {
		t.Fatal("AI audit must be a global audit subset")
	}
	nodeRows, _ := s.ListForNode(ctx, 100, "node")
	controlRows, _ := s.ListControlPlane(ctx, 100)
	if len(nodeRows) != 1 || len(controlRows) != 1 {
		t.Fatal("audit scopes mixed")
	}
}
