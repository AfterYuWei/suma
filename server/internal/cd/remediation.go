package cd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/task"
)

type RemediationResult struct {
	Action    string        `json:"action"`
	ReleaseID uint          `json:"release_id"`
	NodeIDs   []string      `json:"node_ids"`
	Task      database.Task `json:"task"`
}

func (s *Service) RetryFailedNodes(ctx context.Context, name string, releaseID uint, actor Actor) (RemediationResult, error) {
	return s.remediateFailedNodes(ctx, name, releaseID, AttemptRetry, actor)
}

func (s *Service) RollbackFailedNodes(ctx context.Context, name string, releaseID uint, actor Actor) (RemediationResult, error) {
	return s.remediateFailedNodes(ctx, name, releaseID, AttemptManualRollback, actor)
}

func (s *Service) remediateFailedNodes(ctx context.Context, name string, releaseID uint, operation string, actor Actor) (RemediationResult, error) {
	project, release, err := s.projectRelease(ctx, name, releaseID)
	if err != nil {
		return RemediationResult{}, err
	}
	if s.targets == nil {
		return RemediationResult{}, errors.New("node-targeted Compose execution is unavailable")
	}
	targeted, ok := s.compose.(TargetedRunner)
	if !ok {
		return RemediationResult{}, errors.New("Compose runner does not support node targets")
	}
	if !s.reserve(project.ID) {
		return RemediationResult{}, errors.New("another delivery operation is already queued for this project")
	}
	deployments, err := s.remediationDeployments(ctx, release.ID, operation)
	if err != nil {
		s.releaseReservation(project.ID)
		return RemediationResult{}, err
	}
	if len(deployments) == 0 {
		s.releaseReservation(project.ID)
		return RemediationResult{}, errors.New("release has no nodes eligible for this remediation")
	}
	for _, deployment := range deployments {
		if _, err := s.composeTargetForProject(ctx, project.ID, deployment.NodeID); err != nil {
			s.releaseReservation(project.ID)
			return RemediationResult{}, fmt.Errorf("node %s is blocked: %w", deployment.NodeName, err)
		}
		if operation == AttemptManualRollback {
			if deployment.PreviousReleaseID == nil {
				s.releaseReservation(project.ID)
				return RemediationResult{}, fmt.Errorf("node %s is blocked: no previous release", deployment.NodeName)
			}
			var previous database.DeliveryRelease
			if err := s.db.WithContext(ctx).Where("id = ? AND project_id = ?", *deployment.PreviousReleaseID, project.ID).First(&previous).Error; err != nil {
				s.releaseReservation(project.ID)
				return RemediationResult{}, fmt.Errorf("node %s is blocked: previous release is unavailable", deployment.NodeName)
			}
			if _, err := releaseExecutionSpec(runtimeName(project), previous); err != nil {
				s.releaseReservation(project.ID)
				return RemediationResult{}, fmt.Errorf("node %s is blocked: %w", deployment.NodeName, err)
			}
		}
	}
	action := "retry_failed"
	taskType := "cd.remediation.retry_failed"
	if operation == AttemptManualRollback {
		action = "rollback_failed"
		taskType = "cd.remediation.rollback_failed"
	}
	nodeIDs := make([]string, 0, len(deployments))
	for _, deployment := range deployments {
		nodeIDs = append(nodeIDs, deployment.NodeID)
	}
	parent, err := s.tasks.StartWithIDControlPlane(taskType, strings.ReplaceAll(action, "_", " ")+" for "+project.Name, func(parentCtx context.Context, parentTaskID string, report task.Reporter) (runErr error) {
		defer s.releaseReservation(project.ID)
		defer func() {
			s.recordFinal(actor, "cd.remediation."+action, project.Name, parentTaskID, &release.ID, runErr)
		}()
		lock := s.projectLock(project.ID)
		lock.Lock()
		defer lock.Unlock()
		current, err := s.remediationDeployments(parentCtx, release.ID, operation)
		if err != nil {
			return err
		}
		if len(current) == 0 {
			return errors.New("release has no nodes eligible for this remediation")
		}
		_ = s.db.WithContext(parentCtx).Model(&database.DeliveryRelease{}).Where("id = ?", release.ID).Updates(map[string]any{
			"status": StatusDeploying, "task_id": parentTaskID, "started_at": time.Now(), "finished_at": nil,
		}).Error
		report(5, fmt.Sprintf("Starting remediation on %d nodes", len(current)))
		results := make(chan targetResult, len(current))
		childIDs := make([]string, 0, len(current))
		for _, deploymentRow := range current {
			deployment := deploymentRow
			target, targetErr := s.composeTargetForProject(parentCtx, project.ID, deployment.NodeID)
			if targetErr != nil {
				targetReleaseID := release.ID
				if operation == AttemptManualRollback && deployment.PreviousReleaseID != nil {
					targetReleaseID = *deployment.PreviousReleaseID
				}
				if attempt, createErr := s.createAttempt(parentCtx, deployment.ID, operation, targetReleaseID, ""); createErr == nil {
					_ = s.finishAttempt(parentCtx, attempt.ID, StatusFailed, targetErr.Error(), "")
				}
				_ = s.failDeployment(parentCtx, deployment.ID, targetErr)
				s.recordNodeRemediation(actor, action, project.Name, deployment.NodeID, deployment.NodeName, "", release.ID, targetErr)
				results <- targetResult{err: targetErr}
				continue
			}
			targetRelease := release
			rollback := false
			if operation == AttemptManualRollback {
				rollback = true
				targetRelease = database.DeliveryRelease{}
				if deployment.PreviousReleaseID == nil || s.db.WithContext(parentCtx).Where("id = ? AND project_id = ?", deployment.PreviousReleaseID, project.ID).First(&targetRelease).Error != nil {
					previousErr := errors.New("previous release is unavailable")
					if attempt, createErr := s.createAttempt(parentCtx, deployment.ID, operation, release.ID, ""); createErr == nil {
						_ = s.finishAttempt(parentCtx, attempt.ID, StatusFailed, previousErr.Error(), "")
					}
					_ = s.failDeployment(parentCtx, deployment.ID, previousErr)
					s.recordNodeRemediation(actor, action, project.Name, deployment.NodeID, deployment.NodeName, "", release.ID, previousErr)
					results <- targetResult{err: previousErr}
					continue
				}
			}
			attempt, attemptErr := s.createAttempt(parentCtx, deployment.ID, operation, targetRelease.ID, "")
			if attemptErr != nil {
				results <- targetResult{err: attemptErr}
				continue
			}
			child, childErr := s.tasks.StartWithIDForNode(target.NodeID, target.NodeName, "cd.deploy.node", strings.ReplaceAll(action, "_", " ")+" "+project.Name+" on "+target.NodeName, func(childCtx context.Context, childID string, childReport task.Reporter) error {
				_ = s.db.Model(&database.DeliveryReleaseDeployment{}).Where("id = ?", deployment.ID).Update("task_id", childID).Error
				_ = s.db.Model(&database.DeliveryDeploymentAttempt{}).Where("id = ?", attempt.ID).Update("task_id", childID).Error
				err := s.deployOneTarget(childCtx, project, targetRelease, deployment, targeted.Targeted(target), attempt.ID, childID, rollback, childReport)
				var latest database.DeliveryReleaseDeployment
				_ = s.db.First(&latest, deployment.ID).Error
				s.recordNodeRemediation(actor, action, project.Name, target.NodeID, target.NodeName, childID, release.ID, err)
				results <- targetResult{success: err == nil, rolledBack: latest.Status == StatusRolledBack, err: err}
				return err
			})
			if childErr != nil {
				_ = s.finishAttempt(parentCtx, attempt.ID, StatusFailed, childErr.Error(), "")
				_ = s.failDeployment(parentCtx, deployment.ID, childErr)
				s.recordNodeRemediation(actor, action, project.Name, deployment.NodeID, deployment.NodeName, "", release.ID, childErr)
				results <- targetResult{err: childErr}
				continue
			}
			childIDs = append(childIDs, child.ID)
		}
		failures := make([]string, 0)
		for range current {
			select {
			case result := <-results:
				if result.err != nil && !result.rolledBack {
					failures = append(failures, result.err.Error())
				}
			case <-parentCtx.Done():
				for _, childID := range childIDs {
					s.tasks.Cancel(childID)
				}
				return parentCtx.Err()
			}
		}
		failureReason := strings.Join(failures, "; ")
		if _, err := s.recomputeReleaseAndProject(context.Background(), project.ID, release.ID, failureReason); err != nil {
			return err
		}
		s.invalidateDrift(project.ID)
		report(100, fmt.Sprintf("Remediation completed on %d nodes", len(current)))
		if len(failures) > 0 {
			return fmt.Errorf("remediation failed on %d of %d nodes", len(failures), len(current))
		}
		return nil
	})
	if err != nil {
		s.releaseReservation(project.ID)
		return RemediationResult{}, err
	}
	return RemediationResult{Action: action, ReleaseID: release.ID, NodeIDs: nodeIDs, Task: parent}, nil
}

func (s *Service) remediationDeployments(ctx context.Context, releaseID uint, operation string) ([]database.DeliveryReleaseDeployment, error) {
	var rows []database.DeliveryReleaseDeployment
	query := s.db.WithContext(ctx).Where("release_id = ?", releaseID)
	if operation == AttemptRetry {
		query = query.Where("status IN ?", []string{StatusFailed, StatusRolledBack})
	} else {
		query = query.Where("status = ? AND previous_release_id IS NOT NULL AND (rollback_result IS NULL OR rollback_result <> ?)", StatusFailed, "succeeded")
	}
	return rows, query.Order("node_id ASC").Find(&rows).Error
}

func (s *Service) recordNodeRemediation(actor Actor, action, projectName, nodeID, nodeName, taskID string, releaseID uint, result error) {
	if s.audit == nil {
		return
	}
	value := "success"
	if result != nil {
		value = "failed"
	}
	_ = s.audit.RecordLinkedForNode(context.Background(), nodeID, nodeName, actor.UserID, "cd.remediation."+action+".node", "delivery_project", projectName, actor.IP, value, taskID, &releaseID)
}
