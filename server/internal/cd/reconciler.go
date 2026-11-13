package cd

import (
	"context"
	"fmt"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/task"
	"gorm.io/gorm"
)

func (s *Service) Recover(ctx context.Context) error {
	finished := time.Now()
	reason := "SUMA restarted before the deployment completed"
	active := []string{task.StatusPending, StatusPulling, StatusDeploying, StatusVerifying, StatusRollingBack, StatusValidating}
	var releaseIDs []uint
	var projectIDs []uint
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&database.DeliveryRecord{}).Where("status IN ?", active).Updates(map[string]any{"status": StatusInterrupted, "failure_reason": reason, "finished_at": finished}).Error; err != nil {
			return err
		}
		var attempts []database.DeliveryDeploymentAttempt
		if err := tx.Where("status IN ?", active).Find(&attempts).Error; err != nil {
			return err
		}
		deploymentIDs := make([]uint, 0, len(attempts))
		for _, attempt := range attempts {
			deploymentIDs = append(deploymentIDs, attempt.DeploymentID)
		}
		if len(attempts) > 0 {
			if err := tx.Model(&database.DeliveryDeploymentAttempt{}).Where("status IN ?", active).Updates(map[string]any{
				"status": StatusInterrupted, "failure_reason": reason, "finished_at": finished,
			}).Error; err != nil {
				return err
			}
		}
		if len(deploymentIDs) > 0 {
			if err := tx.Model(&database.DeliveryReleaseDeployment{}).Where("id IN ?", deploymentIDs).Updates(map[string]any{
				"status": StatusFailed, "failure_reason": reason, "finished_at": finished,
			}).Error; err != nil {
				return err
			}
			if err := tx.Model(&database.DeliveryReleaseDeployment{}).Where("id IN ?", deploymentIDs).Distinct().Pluck("release_id", &releaseIDs).Error; err != nil {
				return err
			}
		}
		var activeReleases []database.DeliveryRelease
		if err := tx.Where("status IN ?", []string{StatusPulling, StatusDeploying, StatusVerifying, StatusRollingBack, StatusValidating}).Find(&activeReleases).Error; err != nil {
			return err
		}
		for _, release := range activeReleases {
			releaseIDs = appendUniqueUint(releaseIDs, release.ID)
			projectIDs = appendUniqueUint(projectIDs, release.ProjectID)
		}
		if len(activeReleases) > 0 {
			if err := tx.Model(&database.DeliveryRelease{}).Where("status IN ?", []string{StatusPulling, StatusDeploying, StatusVerifying, StatusRollingBack, StatusValidating}).Updates(map[string]any{
				"status": StatusFailed, "failure_reason": reason, "finished_at": finished,
			}).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&database.Task{}).Where("status IN ?", []string{task.StatusPending, task.StatusRunning}).Updates(map[string]any{
			"status": task.StatusCanceled, "progress": 0, "message": reason, "finished_at": finished,
		}).Error; err != nil {
			return err
		}
		for _, releaseID := range releaseIDs {
			var release database.DeliveryRelease
			if err := tx.First(&release, releaseID).Error; err != nil {
				return err
			}
			projectIDs = appendUniqueUint(projectIDs, release.ProjectID)
			if release.LatestRecordID != nil {
				var record database.DeliveryRecord
				if err := tx.First(&record, *release.LatestRecordID).Error; err != nil {
					return err
				}
				if err := tx.Model(&release).Updates(map[string]any{"status": record.Status, "failure_reason": record.FailureReason, "finished_at": record.FinishedAt}).Error; err != nil {
					return err
				}
				continue
			}
			var count int64
			if err := tx.Model(&database.DeliveryReleaseDeployment{}).Where("release_id = ?", release.ID).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				if _, err := s.recomputeReleaseAndProjectDB(ctx, tx, release.ProjectID, release.ID, reason); err != nil {
					return err
				}
			}
		}
		for _, projectID := range projectIDs {
			if err := s.recomputeProjectActiveReleaseDB(ctx, tx, projectID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("recover interrupted delivery state: %w", err)
	}
	for _, projectID := range projectIDs {
		s.invalidateDrift(projectID)
	}
	return nil
}

func appendUniqueUint(values []uint, value uint) []uint {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
