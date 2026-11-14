package projectcd

import (
	"context"
	"errors"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/task"
	"gorm.io/gorm"
)

// The failed application and its automatic recovery keep independent evidence.
// Only a Docker deployment failure may initiate recovery; authorization and
// snapshot validation errors never invoke another deployment.
func (s *Service) applyWithRollback(ctx context.Context, row database.ProjectCD, record *database.ProjectCDRecord, report task.Reporter) error {
	applyErr := s.apply(ctx, row, record, false, report)
	if !errors.Is(applyErr, errDeploymentFailed) || ctx.Err() != nil {
		return applyErr
	}
	value, err := s.decode(*record)
	if err != nil {
		return errors.Join(applyErr, err)
	}
	if !value.AutoRollback {
		return applyErr
	}
	if err := s.db.WithContext(ctx).Model(&row).Update("reconcile_mode", "manual").Error; err != nil {
		return errors.Join(applyErr, err)
	}
	if row.AppliedRecordID == nil {
		return errors.Join(applyErr, errors.New("no successful configuration is available for automatic rollback"))
	}
	source, err := s.record(ctx, row, *row.AppliedRecordID)
	if err != nil {
		return errors.Join(applyErr, err)
	}
	if source.Status != "succeeded" || len(source.SnapshotCiphertext) == 0 || !source.ImagesPinned {
		return errors.Join(applyErr, errors.New("automatic rollback requires a successful configuration snapshot"))
	}
	restore := database.ProjectCDRecord{ImagesPinned: true, ProjectCDID: row.ID, SourceRecordID: &record.ID, Operation: "auto_rollback", Trigger: "automatic_rollback", Status: "running", TaskID: record.TaskID, Actor: "SUMA", Revision: row.Revision, CommitSHA: source.CommitSHA, CommitMessage: source.CommitMessage, ConfigHash: source.ConfigHash, SnapshotCiphertext: source.SnapshotCiphertext}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(record).Updates(map[string]any{"status": "failed", "failure_reason": applyErr.Error(), "finished_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
		return tx.Create(&restore).Error
	}); err != nil {
		return errors.Join(applyErr, err)
	}
	report(35, "Restoring previous successful configuration")
	rollbackErr := s.apply(ctx, row, &restore, true, report)
	status, reason := "succeeded", ""
	if rollbackErr != nil {
		status, reason = "failed", rollbackErr.Error()
	}
	finishCtx := context.WithoutCancel(ctx)
	persistErr := s.db.WithContext(finishCtx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&restore).Updates(map[string]any{"status": status, "failure_reason": reason, "finished_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
		// Recovery must not turn the failed input into an automatic retry loop.
		return tx.Model(&row).Update("failed_fingerprint", record.CommitSHA+record.ConfigHash).Error
	})
	var auditErr error
	if s.audit != nil {
		auditErr = s.audit.RecordLinkedForNode(finishCtx, row.NodeID, "", nil, "project.cd.auto_rollback", "project", row.ProjectName, "", status, record.TaskID, nil)
	}
	if rollbackErr != nil || persistErr != nil || auditErr != nil {
		return errors.Join(applyErr, rollbackErr, persistErr, auditErr)
	}
	return errors.Join(applyErr, errors.New("previous successful configuration restored; delivery policy is now manual"))
}
