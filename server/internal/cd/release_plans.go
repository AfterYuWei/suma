package cd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"time"

	"github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/deployment"
	"github.com/suma/suma/server/internal/imageupdate"
	"github.com/suma/suma/server/internal/registry"
	"github.com/suma/suma/server/internal/task"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Private fields excluded from record HTTP responses are explicitly included
// only inside the encrypted plan payload.
type batchPlanPayload struct {
	Record      database.DeliveryRecord
	Worktree    string
	Environment []byte
	Nodes       string
	Registries  string
	Frozen      []byte
	Restores    string
}

func (s *Service) SetImageResolver(resolver imageupdate.Resolver) { s.imageResolver = resolver }

func (s *Service) configurationVersion(ctx context.Context, db *gorm.DB, project database.DeliveryProject) (string, error) {
	var nodes []string
	var registries []uint
	if err := db.WithContext(ctx).Model(&database.DeliveryProjectNode{}).Where("project_id = ?", project.ID).Order("node_id").Pluck("node_id", &nodes).Error; err != nil {
		return "", err
	}
	if err := db.WithContext(ctx).Model(&database.DeliveryProjectRegistryCredential{}).Where("project_id = ?", project.ID).Order("credential_id").Pluck("credential_id", &registries).Error; err != nil {
		return "", err
	}
	repository, err := repositoryFromProject(project)
	if err != nil {
		return "", err
	}
	// Pausing future triggers does not change the input of already started work.
	raw, err := json.Marshal([]any{repository, project.EnvironmentCiphertext, nodes, registries, project.ReconcileMode, project.AutoRollback, project.DeploymentTimeout})
	if err != nil {
		return "", err
	}
	return deployment.ConfigurationHash(string(raw), nil), nil
}

func (s *Service) frozenInput(record database.DeliveryRecord) (deployment.FrozenInput, error) {
	var input deployment.FrozenInput
	if !record.ImagesPinned || len(record.FrozenInputCiphertext) == 0 {
		return input, errors.New("historical images were not pinned; prepare a new release or explicitly reapply historical configuration")
	}
	raw, err := s.secrets.Decrypt(record.FrozenInputCiphertext)
	if err != nil {
		return input, err
	}
	err = json.Unmarshal([]byte(raw), &input)
	return input, err
}

func (s *Service) freezeRecord(ctx context.Context, project database.DeliveryProject, release database.DeliveryRelease, record *database.DeliveryRecord) (deployment.FrozenInput, error) {
	if record.ImagesPinned {
		return s.frozenInput(*record)
	}
	input := deployment.FrozenInput{Nodes: map[string]deployment.FrozenNode{}}
	version, err := s.configurationVersion(ctx, s.db, project)
	if err != nil {
		return input, err
	}
	input.ConfigurationVersion = version
	resolver, ok := s.targets.(interface {
		ResolveDeploymentTarget(context.Context, string) (compose.Target, error)
	})
	if !ok {
		return input, errors.New("node platform resolution is unavailable")
	}
	if err := json.Unmarshal([]byte(record.NodeIDsJSON), &record.NodeIDs); err != nil {
		return input, err
	}
	var credentials []uint
	if err := json.Unmarshal([]byte(record.RegistryCredentialIDsJSON), &credentials); err != nil {
		return input, err
	}
	spec, err := s.releaseSpec(runtimeName(project), recordRelease(release, *record))
	if err != nil {
		return input, err
	}
	imageResolver := s.imageResolver
	if imageResolver == nil {
		imageResolver = registry.Adapter{}
	}
	identities := map[string]string{}
	for _, id := range record.NodeIDs {
		target, err := resolver.ResolveDeploymentTarget(ctx, id)
		if err != nil {
			return input, err
		}
		runner, err := s.recordRunner(ctx, *record, id)
		if err != nil {
			return input, err
		}
		rendered, err := runner.Render(ctx, spec, io.Discard)
		if err != nil {
			return input, errors.New("unable to render release snapshot (private diagnostic hidden)")
		}
		if err := s.validateDeploymentNodeTargets(ctx, []string{id}, spec, rendered); err != nil {
			return input, errors.New("deployment policy rejected the release")
		}
		frozen, err := deployment.FreezeNode(ctx, imageResolver, s.registries, credentials, target, spec, rendered)
		if err != nil {
			return input, err
		}
		for _, image := range frozen.Images {
			key := image.Declared + "\x00" + image.Platform
			if previous, ok := identities[key]; ok && previous != image.Reference {
				return input, errors.New("image tag changed while preparing target nodes; prepare again")
			}
			identities[key] = image.Reference
		}
		input.Nodes[id] = frozen
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return input, err
	}
	record.FrozenInputCiphertext, err = s.secrets.Encrypt(string(raw))
	if err != nil {
		return input, err
	}
	record.ImagesPinned = true
	return input, nil
}

func (s *Service) PrepareRelease(ctx context.Context, name string, input deployment.PrepareReleaseInput, actor Actor) (database.Task, error) {
	if input.Operation == "" {
		input.Operation = "deploy"
	}
	if !slices.Contains([]string{"deploy", "retry", "restore", "restore-failed", "reapply"}, input.Operation) {
		return database.Task{}, errors.New("invalid release operation")
	}
	project, err := s.project(ctx, name)
	if err != nil {
		return database.Task{}, err
	}
	if !s.reserve(project.ID) {
		return database.Task{}, deployment.ErrPlanBusy
	}
	row, err := s.tasks.StartDefinition(ctx, task.Definition{Type: "cd.prepare", Name: "Prepare release " + name, Params: map[string]any{"project": name, "operation": input.Operation, "source_record_id": input.SourceRecordID}}, func(ctx context.Context, taskID string, report task.Reporter) (any, error) {
		defer s.releaseReservation(project.ID)
		lock := s.projectLock(project.ID)
		lock.Lock()
		defer lock.Unlock()
		if input.Operation == "deploy" && input.ReleaseID == 0 {
			actor.CheckOnly = true
			result, err := s.syncLocked(ctx, project.ID, taskID, "manual", actor, report)
			if err != nil {
				return nil, err
			}
			if result.ReleaseID == nil {
				return nil, errors.New("Git check returned no release")
			}
			input.ReleaseID = *result.ReleaseID
		}
		project, err := s.project(ctx, name)
		if err != nil {
			return nil, err
		}
		var record database.DeliveryRecord
		var release database.DeliveryRelease
		var view deployment.ReleasePlanView
		mosaic := input.Operation == "restore-failed"
		if input.Operation == "retry" {
			var source database.DeliveryRecord
			if err := s.db.WithContext(ctx).Where("id = ? AND project_id = ?", input.SourceRecordID, project.ID).First(&source).Error; err != nil {
				return nil, err
			}
			mosaic = source.RestoreRecordsJSON != ""
		}
		if mosaic {
			record, release, view, err = s.prepareFailedRestore(ctx, project, input, actor)
		} else {
			if input.Operation == "deploy" {
				_, release, err = s.projectRelease(ctx, name, input.ReleaseID)
				if err != nil {
					return nil, err
				}
				if release.Status == StatusRejected {
					return nil, errors.New("this Git version was rejected")
				}
				record, err = s.snapshotRecord(ctx, project, release, AttemptDeploy, actor)
			} else {
				var source database.DeliveryRecord
				source, err = s.GetRecord(ctx, name, input.SourceRecordID)
				if err != nil {
					return nil, err
				}
				_, release, err = s.projectRelease(ctx, name, source.ReleaseID)
				if err != nil {
					return nil, err
				}
				operation := AttemptManualRollback
				if input.Operation == "retry" {
					operation = AttemptRetry
				}
				record = copyRecordSnapshot(source, operation, actor)
				var attempts []database.DeliveryDeploymentAttempt
				statuses := []string{StatusSucceeded}
				if input.Operation == "retry" {
					statuses = []string{StatusFailed, StatusInterrupted}
					var latest database.DeliveryRecord
					if err := s.db.WithContext(ctx).Select("id").Where("project_id = ? AND operation <> ?", project.ID, AttemptAutoRollback).Order("id DESC").First(&latest).Error; err != nil {
						return nil, err
					}
					if latest.ID != source.ID {
						return nil, deployment.ErrPlanChanged
					}
				}
				if err = s.db.WithContext(ctx).Where("record_id = ? AND status IN ?", source.ID, statuses).Find(&attempts).Error; err != nil {
					return nil, err
				}
				nodes := []string{}
				for _, attempt := range attempts {
					nodes = append(nodes, attempt.NodeID)
				}
				if len(nodes) == 0 {
					return nil, errors.New("record has no eligible target nodes")
				}
				raw, _ := json.Marshal(nodes)
				record.NodeIDsJSON = string(raw)
				if input.Operation != "reapply" && !record.ImagesPinned {
					return nil, errors.New("historical record has no fixed image identity; use reapply configuration explicitly")
				}
			}
			if err != nil {
				return nil, err
			}
			report(65, "Resolving fixed deployment images")
			if input.Operation == "reapply" {
				record.FrozenInputCiphertext = nil
				record.ImagesPinned = false
			}
			frozen, err := s.freezeRecord(ctx, project, release, &record)
			if err != nil {
				return nil, err
			}
			view, err = s.releasePlanView(ctx, project, release, record, frozen)
			if err != nil {
				return nil, err
			}

		}
		if err != nil {
			return nil, err
		}
		view.Operation, view.SourceRecordID = input.Operation, input.SourceRecordID
		version, err := s.configurationVersion(ctx, s.db, project)
		if err != nil {
			return nil, err
		}
		payload := batchPlanPayload{Record: record, Worktree: record.WorktreePath, Environment: record.EnvironmentCiphertext, Nodes: record.NodeIDsJSON, Registries: record.RegistryCredentialIDsJSON, Frozen: record.FrozenInputCiphertext, Restores: record.RestoreRecordsJSON}
		view, err = deployment.SaveReleasePlan(ctx, s.db, s.secrets, "batch", project.ID, version, taskID, payload, view)
		report(100, "Release plan ready for confirmation")
		return map[string]any{"plan_id": view.ID, "project": name}, err
	})
	if err != nil {
		s.releaseReservation(project.ID)
	}
	return row, err
}

func (s *Service) releasePlanView(ctx context.Context, project database.DeliveryProject, release database.DeliveryRelease, record database.DeliveryRecord, frozen deployment.FrozenInput) (deployment.ReleasePlanView, error) {
	view := deployment.ReleasePlanView{ReleaseID: release.ID, CommitSHA: release.CommitSHA, CommitMessage: release.CommitMessage, ImagesPinned: record.ImagesPinned, AutoRollback: record.AutoRollback, DeploymentTimeout: record.DeploymentTimeout, Nodes: []deployment.ReleaseNodeView{}}
	if err := json.Unmarshal([]byte(record.ComposeFilesJSON), &view.ComposeFiles); err != nil {
		return view, err
	}
	nodes := []string{}
	if err := json.Unmarshal([]byte(record.NodeIDsJSON), &nodes); err != nil {
		return view, err
	}
	record.NodeIDs = nodes
	if err := s.validateRecordCredentials(ctx, record); err != nil {
		return view, err
	}
	var registries []uint
	if err := json.Unmarshal([]byte(record.RegistryCredentialIDsJSON), &registries); err != nil {
		return view, err
	}
	resolver := s.imageResolver
	if resolver == nil {
		resolver = registry.Adapter{}
	}
	environment, err := s.decryptEnvironment(record.EnvironmentCiphertext)
	if err != nil {
		return view, err
	}
	for _, id := range nodes {
		target, err := s.targets.ResolveComposeTarget(ctx, id)
		if err != nil {
			return view, err
		}
		next, ok := frozen.Nodes[id]
		if !ok || next.RuntimeIdentity != target.RuntimeIdentity {
			return view, deployment.ErrPlanChanged
		}
		if err := deployment.VerifyFrozenImages(ctx, resolver, s.registries, registries, id, next); err != nil {
			return view, err
		}
		var state database.DeliveryTargetState
		var previous *deployment.FrozenNode
		var oldEnvironment map[string]string
		previousCommit := ""
		canRestore := false
		if err := s.db.WithContext(ctx).Where("project_id = ? AND node_id = ?", project.ID, id).First(&state).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return view, err
		}
		if state.ActiveRecordID != nil {
			var source database.DeliveryRecord
			if err := s.db.WithContext(ctx).First(&source, *state.ActiveRecordID).Error; err != nil {
				return view, err
			}
			var version database.DeliveryRelease
			if err := s.db.WithContext(ctx).First(&version, source.ReleaseID).Error; err != nil {
				return view, err
			}
			previousCommit = version.CommitSHA
			oldEnvironment, err = s.decryptEnvironment(source.EnvironmentCiphertext)
			if err != nil {
				return view, err
			}
			if source.ImagesPinned {
				old, err := s.frozenInput(source)
				if err != nil {
					return view, err
				}
				if node, ok := old.Nodes[id]; ok {
					previous = &node
					canRestore = node.RuntimeIdentity == target.RuntimeIdentity
				}
			}
		}
		node, err := deployment.NodeChanges(target, next, previous, environment, oldEnvironment)
		if err != nil {
			return view, err
		}
		node.PreviousRecordID = state.ActiveRecordID
		node.TargetCommit = release.CommitSHA
		node.PreviousCommit = previousCommit
		node.CanRestore = canRestore
		node.Changed = node.Changed || previousCommit != release.CommitSHA
		runner, err := s.recordRunner(ctx, record, id)
		if err != nil {
			return view, err
		}
		spec, err := s.releaseSpec(runtimeName(project), recordRelease(release, record))
		if err != nil {
			return view, err
		}
		health, healthErr := runner.PS(ctx, next.Apply(spec), io.Discard)
		healthy, checks, _ := deployment.RuntimeHealth(health)
		node.RuntimeHealthy = healthErr == nil && healthy
		node.HealthChecked = healthErr == nil && checks > 0
		node.Changed = node.Changed || !node.RuntimeHealthy
		view.Nodes = append(view.Nodes, node)
	}
	return view, nil
}

func (s *Service) ReleasePlan(ctx context.Context, name, id string) (deployment.ReleasePlanView, error) {
	project, err := s.project(ctx, name)
	if err != nil {
		return deployment.ReleasePlanView{}, err
	}
	_, view, err := deployment.LoadReleasePlan(ctx, s.db, s.secrets, "batch", project.ID, id, nil)
	return view, err
}

func (s *Service) PreparedRelease(ctx context.Context, name, taskID string) (deployment.ReleasePlanView, error) {
	project, err := s.project(ctx, name)
	if err != nil {
		return deployment.ReleasePlanView{}, err
	}
	var plan database.ReleasePlan
	if err := s.db.WithContext(ctx).Where("kind = 'batch' AND owner_id = ? AND preparation_task_id = ?", project.ID, taskID).First(&plan).Error; err != nil {
		return deployment.ReleasePlanView{}, err
	}
	return s.ReleasePlan(ctx, name, plan.ID)
}

func (s *Service) ConfirmRelease(ctx context.Context, name, id string, input deployment.ConfirmReleaseInput, actor Actor) (OperationResult, error) {
	project, err := s.project(ctx, name)
	if err != nil {
		return OperationResult{}, err
	}
	var payload batchPlanPayload
	plan, view, err := deployment.LoadReleasePlan(ctx, s.db, s.secrets, "batch", project.ID, id, &payload)
	if err != nil {
		return OperationResult{}, err
	}
	if plan.TaskID != "" {
		row, err := s.tasks.Get(ctx, plan.TaskID)
		recordID := uint(0)
		if plan.RecordID != nil {
			recordID = *plan.RecordID
		}
		return OperationResult{Task: row, RecordID: recordID}, err
	}
	if !s.reserve(project.ID) {
		return OperationResult{}, deployment.ErrPlanBusy
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
	project, release, err := s.projectRelease(ctx, name, payload.Record.ReleaseID)
	if err != nil {
		return OperationResult{}, err
	}
	if project.ReconcileMode == ModeObserve {
		return OperationResult{}, errors.New("observe mode does not allow publishing")
	}
	version, err := s.configurationVersion(ctx, s.db, project)
	if err != nil {
		return OperationResult{}, err
	}
	if version != plan.Version || !time.Now().Before(plan.ExpiresAt) {
		return OperationResult{}, deployment.ErrPlanChanged
	}
	nodes, err := deployment.SelectedReleaseNodes(view, input.NodeIDs)
	if err != nil {
		return OperationResult{}, err
	}
	hasChanges := false
	for _, node := range view.Nodes {
		if slices.Contains(nodes, node.NodeID) && node.Changed {
			hasChanges = true
		}
	}
	if view.Operation == "deploy" && !hasChanges && !input.ForceRecreate {
		return OperationResult{}, errors.New("no pending changes; explicitly confirm recreation if required")
	}
	record := payload.Record
	record.WorktreePath = payload.Worktree
	record.EnvironmentCiphertext = payload.Environment
	record.RegistryCredentialIDsJSON = payload.Registries
	record.FrozenInputCiphertext = payload.Frozen
	record.RestoreRecordsJSON = payload.Restores
	if input.ForceRecreate && record.RestoreRecordsJSON == "" {
		frozen, err := s.frozenInput(record)
		if err != nil {
			return OperationResult{}, err
		}
		frozen.ForceRecreate = true
		raw, err := json.Marshal(frozen)
		if err != nil {
			return OperationResult{}, err
		}
		record.FrozenInputCiphertext, err = s.secrets.Encrypt(string(raw))
		if err != nil {
			return OperationResult{}, err
		}
	}
	record.ActorID, record.ActorName, record.ActorIP = actor.UserID, actor.Name, actor.IP
	raw, _ := json.Marshal(nodes)
	record.NodeIDsJSON = string(raw)
	record.NodeIDs = nodes
	if err := s.validateNodeIDs(ctx, nodes); err != nil {
		return OperationResult{}, err
	}
	if err := s.validateRecordCredentials(ctx, record); err != nil {
		return OperationResult{}, err
	}
	result, err := s.tasks.StartDefinition(ctx, task.Definition{Type: recordAction(record.Operation), Name: "Publish " + name, Params: map[string]any{"project": name, "operation": record.Operation, "release_id": release.ID, "plan_id": id}, Admit: func(tx *gorm.DB, row database.Task) error {
		var current database.DeliveryProject
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, project.ID).Error; err != nil {
			return err
		}
		currentVersion, err := s.configurationVersion(ctx, tx, current)
		if err != nil {
			return err
		}
		if currentVersion != plan.Version {
			return deployment.ErrPlanChanged
		}
		for _, node := range view.Nodes {
			if !slices.Contains(nodes, node.NodeID) {
				continue
			}
			var state database.DeliveryTargetState
			err := tx.Where("project_id = ? AND node_id = ?", project.ID, node.NodeID).First(&state).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			before, now := node.PreviousRecordID, state.ActiveRecordID
			if (before == nil) != (now == nil) || before != nil && now != nil && *before != *now {
				return deployment.ErrPlanChanged
			}
		}
		var currentRelease database.DeliveryRelease
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&currentRelease, release.ID).Error; err != nil {
			return err
		}
		if view.Operation == "deploy" && currentRelease.Status == StatusRejected {
			return deployment.ErrPlanChanged
		}
		if view.Operation == "retry" || view.Operation == "restore-failed" {
			var latest database.DeliveryRecord
			if err := tx.Select("id").Where("project_id = ? AND operation <> ?", project.ID, AttemptAutoRollback).Order("id DESC").First(&latest).Error; err != nil {
				return err
			}
			if latest.ID != view.SourceRecordID {
				return deployment.ErrPlanChanged
			}
		}
		if err := deployment.ClaimReleasePlan(tx, id, plan.Version, row.ID); err != nil {
			return err
		}
		if view.Operation == "restore" || view.Operation == "restore-failed" || view.Operation == "reapply" {
			if err := tx.Model(&current).Update("reconcile_mode", ModeManual).Error; err != nil {
				return err
			}
		}
		record.TaskID = row.ID
		record.Status = StatusValidating
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.ReleasePlan{}).Where("id = ?", id).Update("record_id", record.ID).Error; err != nil {
			return err
		}
		values := map[string]any{"latest_record_id": record.ID, "task_id": row.ID, "status": StatusValidating, "failure_reason": "", "finished_at": nil}
		if view.Operation == "deploy" {
			values["approved_by"] = actor.UserID
			values["approved_at"] = time.Now()
		}
		return tx.Model(&release).Updates(values).Error
	}}, func(ctx context.Context, taskID string, report task.Reporter) (any, error) {
		defer s.releaseReservation(project.ID)
		lock := s.projectLock(project.ID)
		lock.Lock()
		defer lock.Unlock()
		err := s.executeRecord(ctx, project, release, record, taskID, report, true)
		s.recordFinal(actor, recordAction(record.Operation), name, taskID, &release.ID, err)
		return map[string]any{"project": name, "record_id": record.ID, "release_id": release.ID, "plan_id": id}, err
	})
	if err != nil {
		return OperationResult{}, err
	}
	queued = true
	return OperationResult{Task: result, RecordID: record.ID}, nil
}
