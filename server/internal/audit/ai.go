package audit

import (
	"context"
	"errors"
	"strings"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/task"
	"gorm.io/gorm"
)

// RecordAI writes to the canonical audit log, including inside an approval's
// operation/Task transaction. AI audit entries are stored only in the global audit log.
func (s *Service) RecordAI(ctx context.Context, tx *gorm.DB, entry database.AIAudit) error {
	row, err := aiEntry(ctx, tx, entry)
	if err != nil {
		return err
	}
	return s.RecordTx(ctx, tx, &row)
}
func aiEntry(ctx context.Context, db *gorm.DB, entry database.AIAudit) (database.AuditLog, error) {
	row := database.AuditLog{Scope: task.ScopeControlPlane, NodeID: entry.NodeID, Source: entry.Source, Action: "ai." + entry.Action, ResourceType: "ai", ResourceName: entry.Resource, Details: entry.Result, Result: aiResult(entry.Result), RunID: entry.RunID, OperationID: entry.OperationID, BindingID: entry.BindingID, ExternalUserID: entry.ExternalUserID, ChatID: entry.ChatID, IP: entry.IP, CreatedAt: entry.CreatedAt}
	// Older decisions recorded only "recorded"; their action carries the outcome.
	if entry.Result == "recorded" {
		row.Result = aiResult(entry.Action)
	}
	if entry.UserID != 0 {
		user := entry.UserID
		row.UserID = &user
	}
	if entry.OperationID != "" {
		var op database.AIOperation
		err := db.WithContext(ctx).First(&op, "id = ?", entry.OperationID).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return row, err
		}
		row.NodeID, row.TaskID = op.NodeID, op.TaskID
	}
	if row.NodeID == "" && entry.RunID != "" {
		var run database.AIRun
		err := db.WithContext(ctx).First(&run, "id = ?", entry.RunID).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return row, err
		}
		row.NodeID = run.NodeID
	}
	if row.NodeID == "" && (entry.Action == "query" || entry.Action == "diagnosis") {
		var node database.Node
		err := db.WithContext(ctx).Select("id").First(&node, "id = ?", entry.Resource).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return row, err
		}
		row.NodeID = node.ID
	}
	if row.NodeID != "" {
		row.Scope = task.ScopeNode
		var node database.Node
		err := db.WithContext(ctx).Select("name").First(&node, "id = ?", row.NodeID).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return row, err
		}
		row.NodeName = node.Name
	}
	return row, nil
}
func aiResult(result string) string {
	first, _, _ := strings.Cut(result, ":")
	switch first {
	case "read", "success", "completed":
		return "success"
	case "failed", "unavailable":
		return "failed"
	case "denied", "rejected", "invalidated", "expired":
		return "denied"
	case "interrupted", "canceled":
		return "canceled"
	default:
		return "pending"
	}
}
func (s *Service) ListAI(ctx context.Context, limit int) ([]database.AIAudit, error) {
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	var rows []database.AuditLog
	err := s.db.WithContext(ctx).Where("action LIKE ?", "ai.%").Order("created_at DESC, id DESC").Limit(limit).Find(&rows).Error
	entries := make([]database.AIAudit, 0, len(rows))
	for _, row := range rows {
		entry := database.AIAudit{ID: row.ID, Action: strings.TrimPrefix(row.Action, "ai."), Source: row.Source, Resource: row.ResourceName, Result: row.Details, RunID: row.RunID, OperationID: row.OperationID, BindingID: row.BindingID, ExternalUserID: row.ExternalUserID, ChatID: row.ChatID, CreatedAt: row.CreatedAt, NodeID: row.NodeID, NodeName: row.NodeName, TaskID: row.TaskID, IP: row.IP}
		if row.UserID != nil {
			entry.UserID = *row.UserID
		}
		if entry.Result == "" {
			entry.Result = row.Result
		}
		entries = append(entries, entry)
	}
	return entries, err
}
