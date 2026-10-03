package cd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/task"
	"gorm.io/gorm"
)

type ReviewedRelease struct {
	ReleaseID       uint                   `json:"release_id"`
	TargetReleaseID uint                   `json:"target_release_id"`
	NodeID          string                 `json:"node_id"`
	ProjectID       uint                   `json:"project_id"`
	StateHash       string                 `json:"state_hash"`
	Config          compose.ReviewedConfig `json:"config"`
	Rollback        bool                   `json:"rollback"`
}

func (s *Service) reviewRelease(ctx context.Context, nodeID string, id uint, rollback bool, resolve compose.ImageResolver) (ReviewedRelease, error) {
	var release database.DeliveryRelease
	if err := s.db.WithContext(ctx).First(&release, id).Error; err != nil {
		return ReviewedRelease{}, err
	}
	var project database.DeliveryProject
	if err := s.db.WithContext(ctx).First(&project, release.ProjectID).Error; err != nil {
		return ReviewedRelease{}, err
	}
	if project.ReconcileMode == "observe" {
		return ReviewedRelease{}, errors.New("observe mode does not allow changes")
	}
	var deployment database.DeliveryReleaseDeployment
	if err := s.db.WithContext(ctx).First(&deployment, "release_id = ? AND node_id = ?", id, nodeID).Error; err != nil {
		return ReviewedRelease{}, err
	}
	var state database.DeliveryTargetState
	if err := s.db.WithContext(ctx).First(&state, "project_id = ? AND node_id = ?", project.ID, nodeID).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return ReviewedRelease{}, err
	}
	targetRelease := release
	if rollback {
		if deployment.PreviousReleaseID == nil {
			return ReviewedRelease{}, errors.New("previous release unavailable")
		}
		if err := s.db.WithContext(ctx).First(&targetRelease, "id = ? AND project_id = ?", *deployment.PreviousReleaseID, project.ID).Error; err != nil {
			return ReviewedRelease{}, err
		}
	}
	if deployment.Status == StatusDeploying || deployment.Status == StatusPulling || deployment.Status == StatusVerifying || deployment.Status == StatusRollingBack {
		return ReviewedRelease{}, errors.New("release target is busy")
	}
	target, err := s.composeTargetForProject(ctx, project.ID, nodeID)
	if err != nil {
		return ReviewedRelease{}, err
	}
	targeted, ok := s.compose.(TargetedRunner)
	if !ok {
		return ReviewedRelease{}, errors.New("targeted runner unavailable")
	}
	runner, ok := targeted.Targeted(target).(compose.ReviewedRunner)
	if !ok {
		return ReviewedRelease{}, errors.New("reviewed runner unavailable")
	}
	spec, err := releaseExecutionSpec(runtimeName(project), targetRelease)
	if err != nil {
		return ReviewedRelease{}, err
	}
	if err := validateComposeSources(spec, targetRelease.WorktreePath); err != nil {
		return ReviewedRelease{}, err
	}
	if err := s.git.Verify(ctx, targetRelease.WorktreePath, targetRelease.CommitSHA); err != nil {
		return ReviewedRelease{}, err
	}
	review, err := runner.Review(ctx, spec, resolve)
	if err != nil {
		return ReviewedRelease{}, err
	}
	if review.ConfigHash != targetRelease.ConfigHash {
		return ReviewedRelease{}, errors.New("release configuration differs from its recorded snapshot")
	}
	raw, _ := json.Marshal([]any{release.ID, release.ConfigHash, release.UpdatedAt, project.UpdatedAt, deployment.ID, deployment.Status, deployment.PreviousReleaseID, state.ActiveReleaseID, state.UpdatedAt, targetRelease.ID, targetRelease.ConfigHash, targetRelease.UpdatedAt})
	sum := sha256.Sum256(raw)
	return ReviewedRelease{ReleaseID: id, TargetReleaseID: targetRelease.ID, NodeID: nodeID, ProjectID: project.ID, StateHash: hex.EncodeToString(sum[:]), Config: review, Rollback: rollback}, nil
}
func (s *Service) ReviewRelease(ctx context.Context, nodeID string, id uint, rollback bool, resolve compose.ImageResolver) (ReviewedRelease, error) {
	var release database.DeliveryRelease
	if err := s.db.WithContext(ctx).First(&release, id).Error; err != nil {
		return ReviewedRelease{}, err
	}
	lock := s.projectLock(release.ProjectID)
	lock.Lock()
	defer lock.Unlock()
	return s.reviewRelease(ctx, nodeID, id, rollback, resolve)
}
func (s *Service) ApplyReleaseReviewed(ctx context.Context, review ReviewedRelease, resolve compose.ImageResolver, taskID string, report task.Reporter) error {
	if !s.reserve(review.ProjectID) {
		return errors.New("delivery project is busy")
	}
	defer s.releaseReservation(review.ProjectID)
	lock := s.projectLock(review.ProjectID)
	lock.Lock()
	defer lock.Unlock()
	current, err := s.reviewRelease(ctx, review.NodeID, review.ReleaseID, review.Rollback, resolve)
	if err != nil {
		return err
	}
	a, _ := json.Marshal(current)
	b, _ := json.Marshal(review)
	if string(a) != string(b) {
		return errors.New("reviewed release changed")
	}
	var release database.DeliveryRelease
	if err := s.db.WithContext(ctx).First(&release, review.TargetReleaseID).Error; err != nil {
		return err
	}
	var project database.DeliveryProject
	if err := s.db.WithContext(ctx).First(&project, review.ProjectID).Error; err != nil {
		return err
	}
	var deployment database.DeliveryReleaseDeployment
	if err := s.db.WithContext(ctx).First(&deployment, "release_id = ? AND node_id = ?", review.ReleaseID, review.NodeID).Error; err != nil {
		return err
	}
	target, err := s.composeTargetForProject(ctx, project.ID, review.NodeID)
	if err != nil {
		return err
	}
	runner := s.compose.(TargetedRunner).Targeted(target)
	reviewed := runner.(compose.ReviewedRunner)
	spec, err := releaseExecutionSpec(runtimeName(project), release)
	if err != nil {
		return err
	}
	operation := "ai_reviewed_deploy"
	if review.Rollback {
		operation = "ai_reviewed_rollback"
	}
	attempt, err := s.createAttempt(ctx, deployment.ID, operation, release.ID, taskID)
	if err != nil {
		return err
	}
	started := time.Now()
	if err := s.db.WithContext(ctx).Model(&deployment).Updates(map[string]any{"status": StatusDeploying, "task_id": taskID, "started_at": started, "failure_reason": ""}).Error; err != nil {
		return err
	}
	report(30, "Applying reviewed release; pulling, building and automatic rollback are disabled")
	err = reviewed.ApplyReviewed(ctx, spec, review.Config, project.DeploymentTimeout, io.Discard)
	health := ""
	if err == nil {
		health, err = runner.PS(ctx, spec, io.Discard)
		if err == nil && !runtimeIsHealthy(health) {
			err = errors.New("services did not become healthy")
		}
	}
	if err != nil {
		s.failDeployment(context.Background(), deployment.ID, err)
		s.finishAttempt(context.Background(), attempt.ID, StatusFailed, err.Error(), health)
		s.recomputeReleaseAndProject(context.Background(), project.ID, review.ReleaseID, err.Error())
		return err
	}
	final := StatusSucceeded
	if review.Rollback {
		final = StatusRolledBack
	}
	if err := s.db.Model(&deployment).Updates(map[string]any{"status": final, "finished_at": time.Now(), "health_summary": health}).Error; err != nil {
		return err
	}
	s.finishAttempt(context.Background(), attempt.ID, final, "", health)
	if err = upsertTargetState(s.db, project.ID, review.NodeID, release.ID, release.CommitSHA, health); err != nil {
		return err
	}
	_, err = s.recomputeReleaseAndProject(ctx, project.ID, review.ReleaseID, "")
	s.invalidateDrift(project.ID)
	return err
}
