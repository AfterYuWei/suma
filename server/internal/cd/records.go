package cd

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/deployment"
	"github.com/suma/suma/server/internal/task"
	"gorm.io/gorm"
)

type OperationResult struct {
	database.Task `json:"task"`
	RecordID      uint `json:"record_id"`
}
type RecordQuery struct {
	ReleaseID         uint
	Operation, Status string
	Page, PageSize    int
}
type RecordPage struct {
	Items    []database.DeliveryRecord `json:"items"`
	Total    int64                     `json:"total"`
	Page     int                       `json:"page"`
	PageSize int                       `json:"page_size"`
	Counts   map[string]int64          `json:"counts"`
}

func (s *Service) ListRecords(ctx context.Context, name string, q RecordQuery) (RecordPage, error) {
	project, err := s.project(ctx, name)
	if err != nil {
		return RecordPage{}, err
	}
	if q.Page < 1 {
		q.Page = 1
	}
	if q.PageSize < 1 {
		q.PageSize = 20
	}
	if q.PageSize > 100 {
		q.PageSize = 100
	}
	result := RecordPage{Items: []database.DeliveryRecord{}, Page: q.Page, PageSize: q.PageSize, Counts: map[string]int64{}}
	base := s.db.WithContext(ctx).Model(&database.DeliveryRecord{}).Where("project_id = ?", project.ID)
	if q.ReleaseID != 0 {
		_, release, e := s.projectRelease(ctx, name, q.ReleaseID)
		if e != nil {
			return result, e
		}
		base = base.Where("release_id = ?", release.ID)
	}
	var counts []struct {
		Operation string
		Count     int64
	}
	if err := base.Session(&gorm.Session{}).Select("operation, count(*) AS count").Group("operation").Scan(&counts).Error; err != nil {
		return result, err
	}
	for _, c := range counts {
		result.Counts[c.Operation] = c.Count
	}
	if q.Operation != "" {
		base = base.Where("operation = ?", q.Operation)
	}
	if q.Status != "" {
		base = base.Where("status = ?", q.Status)
	}
	if err := base.Session(&gorm.Session{}).Count(&result.Total).Error; err != nil {
		return result, err
	}
	if err := base.Order("id DESC").Offset((q.Page - 1) * q.PageSize).Limit(q.PageSize).Find(&result.Items).Error; err != nil {
		return result, err
	}
	for i := range result.Items {
		if err := s.loadRecordDetails(ctx, &result.Items[i]); err != nil {
			return result, err
		}
	}
	return result, nil
}
func (s *Service) GetRecord(ctx context.Context, name string, id uint) (database.DeliveryRecord, error) {
	project, err := s.project(ctx, name)
	if err != nil {
		return database.DeliveryRecord{}, err
	}
	var record database.DeliveryRecord
	err = s.db.WithContext(ctx).Where("id = ? AND project_id = ?", id, project.ID).First(&record).Error
	if err == nil {
		err = s.loadRecordDetails(ctx, &record)
	}
	return record, err
}
func (s *Service) loadRecordDetails(ctx context.Context, r *database.DeliveryRecord) error {
	var release database.DeliveryRelease
	if err := s.db.WithContext(ctx).First(&release, r.ReleaseID).Error; err != nil {
		return err
	}
	r.CommitSHA, r.CommitMessage = release.CommitSHA, release.CommitMessage
	r.NodeIDs = []string{}
	r.RegistryCredentialIDs = []uint{}
	r.Environment = []database.DeliveryEnvironmentSummary{}
	r.Attempts = []database.DeliveryDeploymentAttempt{}
	if r.NodeIDsJSON != "" {
		if err := json.Unmarshal([]byte(r.NodeIDsJSON), &r.NodeIDs); err != nil {
			return errors.New("invalid record node snapshot")
		}
	}
	if r.RegistryCredentialIDsJSON != "" {
		if err := json.Unmarshal([]byte(r.RegistryCredentialIDsJSON), &r.RegistryCredentialIDs); err != nil {
			return errors.New("invalid record credential snapshot")
		}
	}
	values, err := s.decryptEnvironment(r.EnvironmentCiphertext)
	if err != nil {
		return err
	}
	for name := range values {
		r.Environment = append(r.Environment, database.DeliveryEnvironmentSummary{Name: name, HasValue: true})
	}
	sort.Slice(r.Environment, func(i, j int) bool { return r.Environment[i].Name < r.Environment[j].Name })
	if err := s.db.WithContext(ctx).Where("record_id = ?", r.ID).Order("id ASC").Find(&r.Attempts).Error; err != nil {
		return err
	}
	for i := range r.Attempts {
		if r.Attempts[i].TaskID != "" {
			var t database.Task
			if err := s.db.WithContext(ctx).First(&t, "id = ?", r.Attempts[i].TaskID).Error; err == nil {
				r.Attempts[i].Progress, r.Attempts[i].Message = t.Progress, t.Message
			}
		}
	}
	return nil
}
func (s *Service) RecordEnvironmentValues(ctx context.Context, name string, id uint) (map[string]string, error) {
	project, err := s.project(ctx, name)
	if err != nil {
		return nil, err
	}
	var r database.DeliveryRecord
	if err := s.db.WithContext(ctx).Where("id = ? AND project_id = ?", id, project.ID).First(&r).Error; err != nil {
		return nil, err
	}
	return s.decryptEnvironment(r.EnvironmentCiphertext)
}
func (s *Service) snapshotRecord(ctx context.Context, p database.DeliveryProject, r database.DeliveryRelease, operation string, actor Actor) (database.DeliveryRecord, error) {
	var record database.DeliveryRecord
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current database.DeliveryProject
		if err := tx.First(&current, p.ID).Error; err != nil {
			return err
		}
		if current.GitCloneURL != r.RepositoryURL {
			return errors.New("release repository differs from current settings; use a recorded rollback snapshot")
		}
		var nodes []string
		var registries []uint
		if err := tx.Model(&database.DeliveryProjectNode{}).Where("project_id = ?", p.ID).Order("node_id ASC").Pluck("node_id", &nodes).Error; err != nil {
			return err
		}
		if len(nodes) == 0 {
			nodes = []string{"local"}
		}
		if err := tx.Model(&database.DeliveryProjectRegistryCredential{}).Where("project_id = ?", p.ID).Order("credential_id ASC").Pluck("credential_id", &registries).Error; err != nil {
			return err
		}
		if registries == nil {
			registries = []uint{}
		}
		nodesJSON, _ := json.Marshal(nodes)
		registriesJSON, _ := json.Marshal(registries)
		record = database.DeliveryRecord{ProjectID: p.ID, ReleaseID: r.ID, Operation: operation, ActorIP: actor.IP, ActorID: actor.UserID, ActorName: actor.Name, TriggerType: "manual", Status: task.StatusPending, SnapshotComplete: true,
			ImageReferences: "[]", ComposeFilesJSON: current.ComposeFilesJSON, EnvironmentFile: current.EnvironmentFile, EnvironmentCiphertext: append([]byte(nil), current.EnvironmentCiphertext...), WorktreePath: r.WorktreePath,
			NodeIDsJSON: string(nodesJSON), RegistryCredentialIDsJSON: string(registriesJSON), GitCredentialID: current.GitCredentialID, DeploymentTimeout: current.DeploymentTimeout, AutoRollback: current.AutoRollback}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return record, err
}
func copyRecordSnapshot(source database.DeliveryRecord, operation string, actor Actor) database.DeliveryRecord {
	id := source.ID
	return database.DeliveryRecord{ProjectID: source.ProjectID, ReleaseID: source.ReleaseID, SourceRecordID: &id, Operation: operation, ActorIP: actor.IP, ActorID: actor.UserID, ActorName: actor.Name, TriggerType: "manual", Status: task.StatusPending,
		RestoreRecordsJSON: source.RestoreRecordsJSON, SnapshotComplete: source.SnapshotComplete, SnapshotNote: source.SnapshotNote, ConfigHash: source.ConfigHash, ImageReferences: source.ImageReferences, ComposeFilesJSON: source.ComposeFilesJSON, EnvironmentFile: source.EnvironmentFile, EnvironmentCiphertext: append([]byte(nil), source.EnvironmentCiphertext...), WorktreePath: source.WorktreePath,
		NodeIDsJSON: source.NodeIDsJSON, RegistryCredentialIDsJSON: source.RegistryCredentialIDsJSON, GitCredentialID: source.GitCredentialID, DeploymentTimeout: source.DeploymentTimeout, AutoRollback: source.AutoRollback}
}
func recordRelease(release database.DeliveryRelease, record database.DeliveryRecord) database.DeliveryRelease {
	release.ConfigHash = record.ConfigHash
	release.ImageReferences = record.ImageReferences
	release.ComposeFilesJSON = record.ComposeFilesJSON
	release.EnvironmentFile = record.EnvironmentFile
	release.EnvironmentCiphertext = record.EnvironmentCiphertext
	release.WorktreePath = record.WorktreePath
	return release
}
func (s *Service) Deploy(ctx context.Context, name string, releaseID uint, actor Actor) (OperationResult, error) {
	return s.startRecordOperation(ctx, name, releaseID, 0, nil, AttemptDeploy, actor)
}
func (s *Service) Rollback(ctx context.Context, name string, releaseID, recordID uint, nodeIDs []string, actor Actor) (OperationResult, error) {
	if recordID == 0 {
		return OperationResult{}, errors.New("a configuration record is required for rollback")
	}
	return s.startRecordOperation(ctx, name, releaseID, recordID, nodeIDs, AttemptManualRollback, actor)
}
func (s *Service) startRecordOperation(ctx context.Context, name string, releaseID, sourceID uint, nodeIDs []string, operation string, actor Actor) (OperationResult, error) {
	project, release, err := s.projectRelease(ctx, name, releaseID)
	if err != nil {
		return OperationResult{}, err
	}
	if !s.reserve(project.ID) {
		return OperationResult{}, errors.New("another delivery operation is already queued for this project")
	}
	queued := false
	defer func() {
		if !queued {
			s.releaseReservation(project.ID)
		}
	}()
	lock := s.projectLock(project.ID)
	lock.Lock()
	defer lock.Unlock()
	project, release, err = s.projectRelease(ctx, name, release.ID)
	if err != nil {
		return OperationResult{}, err
	}
	if project.ReconcileMode == ModeObserve {
		return OperationResult{}, errors.New("observe mode does not allow delivery operations")
	}
	if operation == AttemptDeploy && release.Status == StatusRejected {
		return OperationResult{}, fmt.Errorf("release cannot be deployed from status %s", release.Status)
	}
	var record database.DeliveryRecord
	if sourceID == 0 {
		if project.GitCloneURL != release.RepositoryURL {
			return OperationResult{}, errors.New("release repository differs from current settings; use a recorded rollback snapshot")
		}
		record, err = s.snapshotRecord(ctx, project, release, operation, actor)
	} else {
		var source database.DeliveryRecord
		err = s.db.WithContext(ctx).Where("id = ? AND project_id = ? AND release_id = ?", sourceID, project.ID, release.ID).First(&source).Error
		if err == nil && !source.SnapshotComplete && !(operation == AttemptRetry && source.RestoreRecordsJSON != "") {
			err = errors.New("this historical record has no complete configuration snapshot")
		}
		if err == nil {
			record = copyRecordSnapshot(source, operation, actor)
		}
		if (operation == AttemptRetry || operation == "rollback_failed") && err == nil {
			if release.LatestRecordID == nil || *release.LatestRecordID != sourceID {
				err = errors.New("the failure record is no longer current; refresh the release")
			}
			var attempts []database.DeliveryDeploymentAttempt
			if err == nil {
				err = s.db.WithContext(ctx).Where("record_id = ? AND status IN ?", sourceID, []string{StatusFailed, StatusInterrupted}).Find(&attempts).Error
			}
			nodeIDs = []string{}
			restores := map[string]uint{}
			for _, a := range attempts {
				if operation == "rollback_failed" {
					if a.PreviousRecordID == nil {
						continue
					}
					var previous database.DeliveryRecord
					if s.db.WithContext(ctx).First(&previous, *a.PreviousRecordID).Error != nil || !previous.SnapshotComplete {
						continue
					}
					restores[a.NodeID] = previous.ID
				}
				nodeIDs = append(nodeIDs, a.NodeID)
			}
			if operation == "rollback_failed" {
				raw, _ := json.Marshal(restores)
				record.RestoreRecordsJSON = string(raw)
				record.SnapshotComplete = false
				record.SnapshotNote = "Each node restores its own previous configuration record"
				record.AutoRollback = false
				record.ConfigHash = ""
				record.ImageReferences = "[]"
				record.ComposeFilesJSON = ""
				record.EnvironmentFile = ""
				record.EnvironmentCiphertext = nil
				record.WorktreePath = ""
				record.RegistryCredentialIDsJSON = "[]"
				record.GitCredentialID = nil
				record.DeploymentTimeout = 0
			}
			if err == nil && len(nodeIDs) == 0 {
				err = errors.New("record has no nodes eligible for retry")
			}
		}
	}
	if err != nil {
		return OperationResult{}, err
	}
	if nodeIDs != nil {
		raw, _ := json.Marshal(nodeIDs)
		record.NodeIDsJSON = string(raw)
	}
	if err := json.Unmarshal([]byte(record.NodeIDsJSON), &record.NodeIDs); err != nil {
		return OperationResult{}, err
	}
	if err := s.validateNodeIDs(ctx, record.NodeIDs); err != nil {
		return OperationResult{}, err
	}
	if err := s.validateRecordCredentials(ctx, record); err != nil {
		return OperationResult{}, err
	}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if (operation == AttemptManualRollback || operation == "rollback_failed") && project.ReconcileMode == ModeAuto {
			if err := tx.Model(&project).Update("reconcile_mode", ModeManual).Error; err != nil {
				return err
			}
		}
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		values := map[string]any{"latest_record_id": record.ID, "status": StatusValidating, "failure_reason": "", "finished_at": nil}
		if operation == AttemptDeploy {
			now := time.Now()
			values["approved_by"] = actor.UserID
			values["approved_at"] = now
		}
		return tx.Model(&release).Updates(values).Error
	}); err != nil {
		return OperationResult{}, err
	}
	row, err := s.tasks.StartWithIDControlPlane(recordAction(operation), "Deliver "+name, func(runCtx context.Context, taskID string, report task.Reporter) (runErr error) {
		defer s.releaseReservation(project.ID)
		lock := s.projectLock(project.ID)
		lock.Lock()
		defer lock.Unlock()
		runErr = s.executeRecord(runCtx, project, release, record, taskID, report, true)
		s.recordFinal(actor, recordAction(operation), name, taskID, &release.ID, runErr)
		return runErr
	})
	if err != nil {
		return OperationResult{}, errors.Join(err, s.finishRecord(context.Background(), release, record, StatusFailed, err.Error(), ""))
	}
	queued = true
	return OperationResult{Task: row, RecordID: record.ID}, nil
}
func (s *Service) validateRecordCredentials(ctx context.Context, r database.DeliveryRecord) error {
	var ids []uint
	if err := json.Unmarshal([]byte(r.RegistryCredentialIDsJSON), &ids); err != nil {
		return errors.New("invalid registry credential snapshot")
	}
	if r.GitCredentialID != nil {
		if err := s.credentials.AuthorizedForNodes(ctx, *r.GitCredentialID, r.NodeIDs); err != nil {
			return err
		}
	}
	for _, id := range ids {
		if s.registries == nil {
			return errors.New("registry credential service is unavailable")
		}
		for _, node := range r.NodeIDs {
			if err := s.registries.AuthorizedForNode(ctx, id, node); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *Service) recordRunner(ctx context.Context, r database.DeliveryRecord, nodeID string) (compose.Runner, error) {
	if s.targets == nil {
		return s.compose, nil
	}
	var ids []uint
	if err := json.Unmarshal([]byte(r.RegistryCredentialIDsJSON), &ids); err != nil {
		return nil, err
	}
	target, err := s.composeTargetWithCredentials(ctx, nodeID, ids)
	if err != nil {
		return nil, err
	}
	targeted, ok := s.compose.(TargetedRunner)
	if !ok {
		return nil, errors.New("Compose runner does not support node targets")
	}
	return targeted.Targeted(target), nil
}
func (s *Service) executeRecord(ctx context.Context, p database.DeliveryProject, release database.DeliveryRelease, r database.DeliveryRecord, taskID string, report task.Reporter, allowAuto bool) (runErr error) {
	now := time.Now()
	r.TaskID = taskID
	if err := json.Unmarshal([]byte(r.NodeIDsJSON), &r.NodeIDs); err != nil {
		return err
	}
	status := StatusSucceeded
	health := ""
	defer func() {
		if runErr != nil {
			if status == StatusSucceeded {
				status = StatusFailed
			}
			if errors.Is(runErr, context.Canceled) {
				status = StatusInterrupted
			}
		}
		runErr = errors.Join(runErr, s.finishRecord(context.Background(), release, r, status, errorMessage(runErr), health))
	}()
	if err := s.db.WithContext(ctx).Model(&r).Updates(map[string]any{"task_id": taskID, "status": StatusValidating, "started_at": now}).Error; err != nil {
		return err
	}
	if err := s.validateNodeIDs(ctx, r.NodeIDs); err != nil {
		return err
	}
	if err := s.validateRecordCredentials(ctx, r); err != nil {
		return err
	}
	if r.RestoreRecordsJSON == "" {
		snapshot := recordRelease(release, r)
		if err := s.git.Verify(ctx, r.WorktreePath, release.CommitSHA); err != nil {
			return err
		}
		spec, err := s.releaseSpec(runtimeName(p), snapshot)
		if err != nil {
			return err
		}
		if err := validateComposeSources(spec, r.WorktreePath); err != nil {
			return err
		}
		runner, err := s.recordRunner(ctx, r, r.NodeIDs[0])
		if err != nil {
			return err
		}
		report(5, "Validating configuration snapshot")
		if err := runner.ValidateRelease(ctx, spec, io.Discard); err != nil {
			return err
		}
		rendered, err := runner.Render(ctx, spec, io.Discard)
		if err != nil {
			return err
		}
		if err := s.validateDeploymentNodeTargets(ctx, r.NodeIDs, spec, rendered); err != nil {
			if len(spec.Environment) > 0 {
				return errors.New("deployment policy rejected configuration (details hidden for private environment)")
			}
			return err
		}
		computedHash := releaseConfigurationHash(rendered, spec.Environment)
		if r.ConfigHash != "" && r.ConfigHash != computedHash {
			return errors.New("configuration snapshot no longer matches its recorded hash")
		}
		r.ConfigHash = computedHash
		images := imageReferences(rendered)
		for i := range images {
			images[i] = compose.RedactEnvironment(images[i], spec.Environment)
		}
		raw, _ := json.Marshal(images)
		r.ImageReferences = string(raw)
		if err := s.db.WithContext(ctx).Model(&r).Updates(map[string]any{"config_hash": r.ConfigHash, "image_references": r.ImageReferences, "status": StatusDeploying}).Error; err != nil {
			return err
		}
	}
	if err := s.db.WithContext(ctx).Model(&database.DeliveryRelease{}).Where("id = ?", release.ID).Updates(map[string]any{"status": StatusDeploying, "task_id": taskID, "started_at": now}).Error; err != nil {
		return err
	}
	type nodeResult struct {
		node     string
		previous *uint
		err      error
	}
	type runningNode struct {
		node      string
		previous  *uint
		attemptID uint
		taskID    string
	}
	children := []runningNode{}
	defer func() {
		for _, child := range children {
			s.tasks.Cancel(child.taskID)
		}
		for _, child := range children {
			if err := s.tasks.Wait(context.Background(), child.taskID); err != nil && runErr == nil {
				runErr = err
			}
		}
	}()
	failures := []string{}
	failed := []nodeResult{}
	successes := 0
	for _, nodeID := range r.NodeIDs {
		node := nodeID
		var deployment database.DeliveryReleaseDeployment
		if err := s.db.WithContext(ctx).Where("release_id = ? AND node_id = ?", release.ID, node).FirstOrCreate(&deployment, database.DeliveryReleaseDeployment{ReleaseID: release.ID, NodeID: node, NodeName: node, Status: task.StatusPending}).Error; err != nil {
			return err
		}
		var n database.Node
		if e := s.db.WithContext(ctx).First(&n, "id = ?", node).Error; e == nil {
			deployment.NodeName = n.Name
		}
		var previous database.DeliveryTargetState
		if err := s.db.WithContext(ctx).Where("project_id = ? AND node_id = ?", p.ID, node).First(&previous).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		nodeRecord, nodeRelease := r, release
		if r.RestoreRecordsJSON != "" {
			var restores map[string]uint
			if err := json.Unmarshal([]byte(r.RestoreRecordsJSON), &restores); err != nil {
				return err
			}
			var source database.DeliveryRecord
			if err := s.db.WithContext(ctx).First(&source, restores[node]).Error; err != nil {
				return err
			}
			nodeRecord = source
			nodeRelease = database.DeliveryRelease{}
			if err := s.db.WithContext(ctx).First(&nodeRelease, source.ReleaseID).Error; err != nil {
				return err
			}
			nodeRecord.Operation = r.Operation
			nodeRecord.RestoreRecordsJSON = r.RestoreRecordsJSON
			nodeRecord.NodeIDs = []string{node}
		}
		targetRecordID := nodeRecord.ID
		attempt := database.DeliveryDeploymentAttempt{TargetRecordID: &targetRecordID, DeploymentID: deployment.ID, RecordID: &r.ID, PreviousRecordID: previous.ActiveRecordID, NodeID: node, NodeName: deployment.NodeName, Operation: r.Operation, TargetReleaseID: nodeRelease.ID, Status: task.StatusPending}
		if err := s.db.WithContext(ctx).Create(&attempt).Error; err != nil {
			return err
		}
		child, err := s.tasks.StartWithIDForNode(node, deployment.NodeName, "cd.deploy.node", "Deliver "+p.Name+" to "+deployment.NodeName, func(childCtx context.Context, childID string, childReport task.Reporter) error {
			_, nodeErr := s.executeRecordNode(childCtx, p, nodeRelease, nodeRecord, deployment, attempt, previous, childID, childReport)
			s.recordNodeFinal(Actor{UserID: r.ActorID, Name: r.ActorName, IP: r.ActorIP}, recordAction(r.Operation)+".node", p.Name, node, deployment.NodeName, childID, release.ID, nodeErr)
			return nodeErr
		})
		if err != nil {
			if finishErr := s.finishAttempt(ctx, attempt.ID, StatusFailed, err.Error(), ""); finishErr != nil {
				return errors.Join(err, finishErr)
			}
			if updateErr := s.db.WithContext(ctx).Model(&deployment).Updates(map[string]any{"status": StatusFailed, "failure_reason": err.Error(), "finished_at": time.Now()}).Error; updateErr != nil {
				return errors.Join(err, updateErr)
			}
			failures = append(failures, node+": "+err.Error())
			failed = append(failed, nodeResult{node: node, previous: previous.ActiveRecordID, err: err})
		} else {
			children = append(children, runningNode{node: node, previous: previous.ActiveRecordID, attemptID: attempt.ID, taskID: child.ID})
		}
	}
	for _, child := range children {
		if err := s.tasks.Wait(ctx, child.taskID); err != nil {
			return err
		}
		var attempt database.DeliveryDeploymentAttempt
		if err := s.db.WithContext(ctx).First(&attempt, child.attemptID).Error; err != nil {
			return err
		}
		childTask, err := s.tasks.Get(ctx, child.taskID)
		if err != nil {
			return err
		}
		if attempt.Status != StatusSucceeded || childTask.Status != task.StatusSuccess {
			reason := attempt.FailureReason
			if reason == "" {
				reason = "node task did not complete successfully"
			}
			failures = append(failures, child.node+": "+reason)
			failed = append(failed, nodeResult{node: child.node, previous: child.previous, err: errors.New(reason)})
			if attempt.Status == task.StatusPending {
				if err := s.finishAttempt(ctx, attempt.ID, StatusFailed, reason, ""); err != nil {
					return err
				}
			}
		} else {
			successes++
			health = attempt.HealthSummary
		}
	}

	if successes > 0 && len(failures) > 0 {
		status = StatusPartialFailed
	}
	if len(failures) > 0 {
		// A successful automatic recovery is its own record and never rewrites this failure.
		if allowAuto && r.AutoRollback && (r.Operation == AttemptDeploy || r.Operation == AttemptRetry) {
			if err := s.db.WithContext(ctx).Model(&database.DeliveryProject{}).Where("id = ?", p.ID).Update("reconcile_mode", ModeManual).Error; err != nil {
				return err
			}
			groups := map[uint][]string{}
			for _, f := range failed {
				if f.previous != nil {
					groups[*f.previous] = append(groups[*f.previous], f.node)
				}
			}
			for previousID, nodes := range groups {
				var source database.DeliveryRecord
				if err := s.db.WithContext(ctx).First(&source, previousID).Error; err != nil {
					failures = append(failures, "automatic rollback snapshot is unavailable")
					continue
				}
				if !source.SnapshotComplete {
					failures = append(failures, "automatic rollback snapshot is incomplete")
					continue
				}
				restore := copyRecordSnapshot(source, AttemptAutoRollback, Actor{Name: "SUMA"})
				restore.SourceRecordID = &r.ID
				restore.TriggerType = "automatic_rollback"
				encoded, _ := json.Marshal(nodes)
				restore.NodeIDsJSON = string(encoded)
				var target database.DeliveryRelease
				if err := s.db.WithContext(ctx).First(&target, restore.ReleaseID).Error; err != nil {
					failures = append(failures, "automatic rollback release is unavailable")
					continue
				}
				if err := s.db.WithContext(ctx).Create(&restore).Error; err != nil {
					failures = append(failures, err.Error())
					continue
				}
				autoErr := s.executeRecord(ctx, p, target, restore, taskID, report, false)
				s.recordFinal(Actor{Name: "SUMA"}, recordAction(AttemptAutoRollback), p.Name, taskID, &target.ID, autoErr)
				if autoErr != nil {
					failures = append(failures, "automatic rollback: "+autoErr.Error())
				}
			}
		}
		// Keep partial outcome while returning an error to Task.
		status = StatusFailed
		if successes > 0 {
			status = StatusPartialFailed
		}
		failure := strings.Join(failures, "; ")
		return errors.New(failure)
	}
	report(100, "Delivery completed")
	return nil
}

func errorMessage(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}
func (s *Service) finishRecord(ctx context.Context, release database.DeliveryRelease, r database.DeliveryRecord, status, reason, health string) error {
	finished := time.Now()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&database.DeliveryRecord{}).Where("id = ?", r.ID).Updates(map[string]any{"status": status, "failure_reason": reason, "health_summary": health, "finished_at": finished}).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.DeliveryRelease{}).Where("id = ?", release.ID).Updates(map[string]any{"latest_record_id": r.ID, "status": status, "failure_reason": reason, "health_summary": health, "finished_at": finished}).Error; err != nil {
			return err
		}
		return s.recomputeProjectActiveReleaseDB(ctx, tx, r.ProjectID)
	})
	s.invalidateDrift(r.ProjectID)
	return err
}
func (s *Service) executeRecordNode(ctx context.Context, p database.DeliveryProject, release database.DeliveryRelease, r database.DeliveryRecord, d database.DeliveryReleaseDeployment, a database.DeliveryDeploymentAttempt, previous database.DeliveryTargetState, taskID string, report task.Reporter) (health string, runErr error) {
	now := time.Now()
	defer func() {
		status := StatusSucceeded
		if runErr != nil {
			status = StatusFailed
		}
		if errors.Is(runErr, context.Canceled) {
			status = StatusInterrupted
		}
		finished := time.Now()
		e := s.db.WithContext(context.Background()).Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&a).Updates(map[string]any{"status": status, "failure_reason": errorMessage(runErr), "health_summary": health, "finished_at": finished, "task_id": taskID}).Error; err != nil {
				return err
			}
			return tx.Model(&d).Updates(map[string]any{"status": status, "failure_reason": errorMessage(runErr), "health_summary": health, "finished_at": finished, "task_id": taskID, "node_name": a.NodeName, "previous_release_id": previous.ActiveReleaseID}).Error
		})
		runErr = errors.Join(runErr, e)
	}()
	runner, err := s.recordRunner(ctx, r, d.NodeID)
	if err != nil {
		return "", err
	}
	spec, err := s.releaseSpec(runtimeName(p), recordRelease(release, r))
	if err != nil {
		return "", err
	}
	if r.RestoreRecordsJSON != "" {
		if err := s.git.Verify(ctx, r.WorktreePath, release.CommitSHA); err != nil {
			return "", err
		}
		if err := validateComposeSources(spec, r.WorktreePath); err != nil {
			return "", err
		}
		if err := s.validateRecordCredentials(ctx, r); err != nil {
			return "", err
		}
		if err := runner.ValidateRelease(ctx, spec, io.Discard); err != nil {
			return "", err
		}
		rendered, err := runner.Render(ctx, spec, io.Discard)
		if err != nil {
			return "", err
		}
		if err := s.validateDeploymentNodeTargets(ctx, []string{d.NodeID}, spec, rendered); err != nil {
			if len(spec.Environment) > 0 {
				return "", errors.New("deployment policy rejected configuration (details hidden for private environment)")
			}
			return "", err
		}
	}
	spec.ForceRecreate = r.Operation != AttemptDeploy || (previous.ActiveReleaseID != nil && *previous.ActiveReleaseID == release.ID)
	if err := s.claimDeploymentNamespace(ctx, p, d.NodeID, spec); err != nil {
		return "", err
	}
	health, err = deployment.DeployNode(ctx, runner, spec, r.DeploymentTimeout, func(stage string) (io.Writer, error) {
		if err := s.db.WithContext(ctx).Model(&a).Updates(map[string]any{"status": stage, "task_id": taskID, "started_at": now}).Error; err != nil {
			return nil, err
		}
		if err := s.db.WithContext(ctx).Model(&d).Updates(map[string]any{"status": stage, "task_id": taskID, "started_at": now, "failure_reason": "", "rollback_result": ""}).Error; err != nil {
			return nil, err
		}
		progress := 30
		switch stage {
		case StatusPulling:
			report(15, "Pulling deployment images")
		case StatusDeploying:
			report(55, "Applying configuration snapshot")
			progress = 70
		case StatusVerifying:
			report(85, "Checking service health")
		}
		return &reportWriter{report: report, progress: progress}, nil
	}, nil)
	if err != nil {
		return health, err
	}
	state := database.DeliveryTargetState{ProjectID: p.ID, NodeID: d.NodeID}
	err = s.db.WithContext(ctx).Where("project_id = ? AND node_id = ?", p.ID, d.NodeID).Assign(map[string]any{"active_release_id": release.ID, "active_record_id": r.ID, "observed_commit": release.CommitSHA, "health_summary": health}).FirstOrCreate(&state).Error
	return health, err
}

// Preserve established Task/Audit names while records expose operation types.
func recordAction(operation string) string {
	switch operation {
	case AttemptRetry:
		return "cd.remediation.retry_failed"
	case AttemptManualRollback:
		return "cd.rollback"
	case "rollback_failed":
		return "cd.remediation.rollback_failed"
	case AttemptAutoRollback:
		return "cd.rollback.auto"
	default:
		return "cd.deploy"
	}
}
