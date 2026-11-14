package cd

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/deployment"
)

// Each failed node owns its previous successful snapshot. A recovery must not
// choose one node's version as the baseline for all other nodes.
func (s *Service) prepareFailedRestore(ctx context.Context, project database.DeliveryProject, input deployment.PrepareReleaseInput, actor Actor) (database.DeliveryRecord, database.DeliveryRelease, deployment.ReleasePlanView, error) {
	var release database.DeliveryRelease
	var view deployment.ReleasePlanView
	source, err := s.GetRecord(ctx, project.Name, input.SourceRecordID)
	if err != nil {
		return database.DeliveryRecord{}, release, view, err
	}
	_, release, err = s.projectRelease(ctx, project.Name, source.ReleaseID)
	if err != nil {
		return database.DeliveryRecord{}, release, view, err
	}
	var latest database.DeliveryRecord
	if err := s.db.WithContext(ctx).Select("id").Where("project_id = ? AND operation <> ?", project.ID, AttemptAutoRollback).Order("id DESC").First(&latest).Error; err != nil {
		return database.DeliveryRecord{}, release, view, err
	}
	if latest.ID != source.ID {
		return database.DeliveryRecord{}, release, view, deployment.ErrPlanChanged
	}
	var attempts []database.DeliveryDeploymentAttempt
	if err := s.db.WithContext(ctx).Where("record_id = ? AND status IN ?", source.ID, []string{StatusFailed, StatusInterrupted}).Find(&attempts).Error; err != nil {
		return database.DeliveryRecord{}, release, view, err
	}
	oldTargets := map[string]uint{}
	if input.Operation == "retry" {
		if err := json.Unmarshal([]byte(source.RestoreRecordsJSON), &oldTargets); err != nil {
			return database.DeliveryRecord{}, release, view, err
		}
	}
	operation := "rollback_failed"
	if input.Operation == "retry" {
		operation = AttemptRetry
	}
	record := copyRecordSnapshot(source, operation, actor)
	record.RestoreRecordsJSON = ""
	record.ConfigHash = ""
	record.ComposeFilesJSON = "[]"
	record.EnvironmentFile = ""
	record.EnvironmentCiphertext = nil
	record.WorktreePath = ""
	record.RegistryCredentialIDsJSON = "[]"
	record.GitCredentialID = nil
	record.FrozenInputCiphertext = nil
	record.ImagesPinned = true
	record.AutoRollback = false
	record.SnapshotComplete = false
	record.SnapshotNote = "Each node restores its own previous configuration record"
	view = deployment.ReleasePlanView{ReleaseID: release.ID, Operation: input.Operation, SourceRecordID: source.ID, ImagesPinned: true, CommitMessage: "Restore failed nodes to their own successful snapshots", ComposeFiles: []string{}, Nodes: []deployment.ReleaseNodeView{}}
	targets := map[string]uint{}
	nodes := []string{}
	for _, attempt := range attempts {
		targetID := attempt.PreviousRecordID
		if input.Operation == "retry" {
			id, ok := oldTargets[attempt.NodeID]
			if !ok {
				return record, release, view, errors.New("recovery snapshot is unavailable for a failed node")
			}
			targetID = &id
		}
		if targetID == nil {
			view.Warnings = append(view.Warnings, attempt.NodeName+": no previous successful snapshot; excluded")
			continue
		}
		previous, err := s.GetRecord(ctx, project.Name, *targetID)
		if err != nil {
			return record, release, view, err
		}
		if !previous.SnapshotComplete || !previous.ImagesPinned {
			view.Warnings = append(view.Warnings, attempt.NodeName+": historical image identity unavailable; excluded")
			continue
		}
		frozen, err := s.frozenInput(previous)
		if err != nil {
			return record, release, view, err
		}
		if _, ok := frozen.Nodes[attempt.NodeID]; !ok {
			return record, release, view, errors.New("historical snapshot does not include this node")
		}
		_, version, err := s.projectRelease(ctx, project.Name, previous.ReleaseID)
		if err != nil {
			return record, release, view, err
		}
		raw, _ := json.Marshal([]string{attempt.NodeID})
		previous.NodeIDsJSON = string(raw)
		preview, err := s.releasePlanView(ctx, project, version, previous, frozen)
		if err != nil {
			return record, release, view, err
		}
		preview.Nodes[0].TargetCommit = version.CommitSHA
		view.Nodes = append(view.Nodes, preview.Nodes[0])
		nodes = append(nodes, attempt.NodeID)
		targets[attempt.NodeID] = previous.ID
	}
	if len(nodes) == 0 {
		return record, release, view, errors.New("no failed node has a reliable recovery baseline")
	}
	raw, _ := json.Marshal(nodes)
	record.NodeIDsJSON = string(raw)
	raw, _ = json.Marshal(targets)
	record.RestoreRecordsJSON = string(raw)
	return record, release, view, nil
}
