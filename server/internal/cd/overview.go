package cd

import (
	"context"
	"sync"
	"time"

	"github.com/suma/suma/server/internal/database"
)

// ReleaseSummary is the minimal release projection used by the control-plane
// overview. It avoids shipping worktree paths and credential material.
type ReleaseSummary struct {
	ID          uint      `json:"id"`
	Status      string    `json:"status"`
	CommitSHA   string    `json:"commit_sha"`
	TriggerType string    `json:"trigger_type"`
	CreatedAt   time.Time `json:"created_at"`
}

// ProjectOverview is the control-plane view of one delivery project. CD is a
// global feature: it aggregates releases, drift, and target nodes instead of
// belonging to a single node.
type ProjectOverview struct {
	Name             string          `json:"name"`
	Configured       bool            `json:"configured"`
	RepositoryURL    string          `json:"repository_url,omitempty"`
	GitRef           string          `json:"git_ref,omitempty"`
	ReconcileMode    string          `json:"reconcile_mode"`
	NodeIDs          []string        `json:"node_ids"`
	DesiredCommit    string          `json:"desired_commit,omitempty"`
	ActiveCommit     string          `json:"active_commit,omitempty"`
	ActiveReleaseID  *uint           `json:"active_release_id,omitempty"`
	Drifted          bool            `json:"drifted"`
	DriftStatus      string          `json:"drift_status"`
	RuntimeHealthy   bool            `json:"runtime_healthy"`
	DriftReason      string          `json:"drift_reason,omitempty"`
	ActiveRelease    *ReleaseSummary `json:"active_release,omitempty"`
	LatestRelease    *ReleaseSummary `json:"latest_release,omitempty"`
	AwaitingApproval bool            `json:"awaiting_approval"`
	Releasing        bool            `json:"releasing"`
}

type OverviewTotals struct {
	Projects         int `json:"projects"`
	Configured       int `json:"configured"`
	Releasing        int `json:"releasing"`
	AwaitingApproval int `json:"awaiting_approval"`
	Drifted          int `json:"drifted"`
	Healthy          int `json:"healthy"`
}

type Overview struct {
	Projects []ProjectOverview `json:"projects"`
	Totals   OverviewTotals    `json:"totals"`
}

// Overview aggregates every delivery project for the control plane. Drift is
// resolved per project with its own timeout so one slow runtime probe cannot
// stall the whole overview.
func (s *Service) Overview(ctx context.Context) (Overview, error) {
	var rows []database.DeliveryProject
	if err := s.db.WithContext(ctx).Order("name ASC").Find(&rows).Error; err != nil {
		return Overview{}, err
	}
	result := Overview{Projects: make([]ProjectOverview, len(rows))}
	var wait sync.WaitGroup
	for index := range rows {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			result.Projects[index] = s.projectOverview(ctx, rows[index])
		}(index)
	}
	wait.Wait()
	for _, item := range result.Projects {
		result.Totals.Projects++
		if item.Configured {
			result.Totals.Configured++
		}
		if item.Releasing {
			result.Totals.Releasing++
		}
		if item.AwaitingApproval {
			result.Totals.AwaitingApproval++
		}
		if item.Drifted {
			result.Totals.Drifted++
		}
		if item.Configured && item.ActiveRelease != nil && !item.Drifted && item.RuntimeHealthy {
			result.Totals.Healthy++
		}
	}
	return result, nil
}

func (s *Service) projectOverview(ctx context.Context, row database.DeliveryProject) ProjectOverview {
	item := ProjectOverview{
		Name: row.Name, Configured: row.GitCloneURL != "",
		RepositoryURL: row.GitCloneURL, GitRef: row.GitRef,
		ReconcileMode: row.ReconcileMode, NodeIDs: []string{},
		DesiredCommit: row.DesiredCommit, ActiveReleaseID: row.ActiveReleaseID,
	}
	if item.ReconcileMode == "" {
		item.ReconcileMode = ModeManual
	}
	if nodeIDs, err := s.projectNodeIDs(ctx, row.ID); err == nil {
		item.NodeIDs = nodeIDs
	}
	var releases []database.DeliveryRelease
	if err := s.db.WithContext(ctx).Where("project_id = ?", row.ID).Order("id DESC").Limit(50).Find(&releases).Error; err == nil && len(releases) > 0 {
		latest := releases[0]
		item.LatestRelease = &ReleaseSummary{ID: latest.ID, Status: latest.Status, CommitSHA: latest.CommitSHA, TriggerType: latest.TriggerType, CreatedAt: latest.CreatedAt}
		switch latest.Status {
		case StatusAwaitingApproval:
			item.AwaitingApproval = true
		case StatusValidating, StatusPulling, StatusDeploying, StatusVerifying, StatusRollingBack:
			item.Releasing = true
		}
		if row.ActiveReleaseID != nil {
			for index := range releases {
				if releases[index].ID == *row.ActiveReleaseID {
					item.ActiveRelease = &ReleaseSummary{ID: releases[index].ID, Status: releases[index].Status, CommitSHA: releases[index].CommitSHA, TriggerType: releases[index].TriggerType, CreatedAt: releases[index].CreatedAt}
					item.ActiveCommit = releases[index].CommitSHA
					break
				}
			}
		}
	}
	driftCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	drift, err := s.Drift(driftCtx, row.Name)
	cancel()
	if err == nil {
		item.Drifted = drift.Drifted
		item.DriftStatus = drift.Status
		item.RuntimeHealthy = drift.RuntimeHealthy
		item.DriftReason = drift.Reason
		item.ActiveReleaseID = drift.ActiveReleaseID
		item.ActiveCommit = drift.ActiveCommit
		item.ActiveRelease = nil
		if drift.ActiveReleaseID != nil {
			for index := range releases {
				if releases[index].ID == *drift.ActiveReleaseID {
					item.ActiveRelease = &ReleaseSummary{ID: releases[index].ID, Status: releases[index].Status, CommitSHA: releases[index].CommitSHA, TriggerType: releases[index].TriggerType, CreatedAt: releases[index].CreatedAt}
					break
				}
			}
		}
	} else if item.DriftReason == "" {
		item.DriftReason = "drift state unavailable"
	}
	return item
}
