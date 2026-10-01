package cd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/suma/suma/server/internal/database"
)

func (s *Service) CleanupImageReferences(ctx context.Context, nodeID string) ([]string, error) {
	ids := map[uint]bool{}
	var states []database.DeliveryTargetState
	if err := s.db.WithContext(ctx).Where("node_id = ?", nodeID).Find(&states).Error; err != nil {
		return nil, err
	}
	for _, state := range states {
		if state.ActiveReleaseID != nil {
			ids[*state.ActiveReleaseID] = true
		}
	}
	var deployments []database.DeliveryReleaseDeployment
	if err := s.db.WithContext(ctx).Where("node_id = ?", nodeID).Find(&deployments).Error; err != nil {
		return nil, err
	}
	for _, deployment := range deployments {
		switch deployment.Status {
		case StatusValidating, StatusAwaitingApproval, StatusApproved, StatusPulling, StatusDeploying, StatusVerifying, StatusRollingBack:
			ids[deployment.ReleaseID] = true
		}
	}
	var targets []database.DeliveryProjectNode
	if err := s.db.WithContext(ctx).Where("node_id = ?", nodeID).Find(&targets).Error; err != nil {
		return nil, err
	}
	projectIDs := map[uint]bool{}
	for _, target := range targets {
		projectIDs[target.ProjectID] = true
	}
	var projects []database.DeliveryProject
	if err := s.db.WithContext(ctx).Find(&projects).Error; err != nil {
		return nil, err
	}
	for _, p := range projects {
		if p.NodeID == nodeID {
			projectIDs[p.ID] = true
		}
		if (projectIDs[p.ID] || p.NodeID == nodeID) && p.ActiveReleaseID != nil {
			ids[*p.ActiveReleaseID] = true
		}
	}
	var releases []database.DeliveryRelease
	if err := s.db.WithContext(ctx).Find(&releases).Error; err != nil {
		return nil, err
	}
	for _, release := range releases {
		if projectIDs[release.ProjectID] {
			switch release.Status {
			case StatusValidating, StatusAwaitingApproval, StatusApproved, StatusPulling, StatusDeploying, StatusVerifying, StatusRollingBack:
				ids[release.ID] = true
			}
		}
	}
	// Only one generation of rollback references is retained, not all ancestry.
	activeIDs := map[uint]bool{}
	for value := range ids {
		activeIDs[value] = true
	}
	for _, release := range releases {
		if activeIDs[release.ID] && release.PreviousReleaseID != nil {
			ids[*release.PreviousReleaseID] = true
		}
	}
	for _, deployment := range deployments {
		if activeIDs[deployment.ReleaseID] && deployment.PreviousReleaseID != nil {
			ids[*deployment.PreviousReleaseID] = true
		}
	}
	found := map[uint]bool{}
	result := []string{}
	for _, release := range releases {
		if !ids[release.ID] {
			continue
		}
		found[release.ID] = true
		var refs []string
		if err := json.Unmarshal([]byte(release.ImageReferences), &refs); err != nil {
			return nil, fmt.Errorf("unable to read release image protection")
		}
		result = append(result, refs...)
	}
	for releaseID := range ids {
		if !found[releaseID] {
			return nil, fmt.Errorf("release protection reference is missing")
		}
	}
	return result, nil
}
