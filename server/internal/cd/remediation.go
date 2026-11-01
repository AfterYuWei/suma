package cd

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/suma/suma/server/internal/database"
)

type RemediationResult struct {
	Action    string        `json:"action"`
	ReleaseID uint          `json:"release_id"`
	NodeIDs   []string      `json:"node_ids"`
	Task      database.Task `json:"task"`
	RecordID  uint          `json:"record_id"`
}

func (s *Service) RetryFailedNodes(ctx context.Context, name string, releaseID, sourceID uint, actor Actor) (RemediationResult, error) {
	if sourceID == 0 {
		return RemediationResult{}, errors.New("a failure record is required")
	}
	result, err := s.startRecordOperation(ctx, name, releaseID, sourceID, nil, AttemptRetry, actor)
	if err != nil {
		return RemediationResult{}, err
	}
	var record database.DeliveryRecord
	if err := s.db.WithContext(ctx).First(&record, result.RecordID).Error; err != nil {
		return RemediationResult{}, err
	}
	nodes := []string{}
	if err := json.Unmarshal([]byte(record.NodeIDsJSON), &nodes); err != nil {
		return RemediationResult{}, err
	}
	return RemediationResult{Action: "retry_failed", ReleaseID: record.ReleaseID, NodeIDs: nodes, Task: result.Task, RecordID: result.RecordID}, nil
}
func (s *Service) RollbackFailedNodes(ctx context.Context, name string, releaseID, sourceID uint, actor Actor) (RemediationResult, error) {
	if sourceID == 0 {
		return RemediationResult{}, errors.New("a failure record is required")
	}
	result, err := s.startRecordOperation(ctx, name, releaseID, sourceID, nil, "rollback_failed", actor)
	if err != nil {
		return RemediationResult{}, err
	}
	var record database.DeliveryRecord
	if err := s.db.WithContext(ctx).First(&record, result.RecordID).Error; err != nil {
		return RemediationResult{}, err
	}
	nodes := []string{}
	if err := json.Unmarshal([]byte(record.NodeIDsJSON), &nodes); err != nil {
		return RemediationResult{}, err
	}
	return RemediationResult{Action: "rollback_failed", ReleaseID: record.ReleaseID, NodeIDs: nodes, Task: result.Task, RecordID: result.RecordID}, nil
}
