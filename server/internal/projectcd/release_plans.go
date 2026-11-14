package projectcd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/deployment"
	gitrepo "github.com/suma/suma/server/internal/git"
	"github.com/suma/suma/server/internal/imageupdate"
	"github.com/suma/suma/server/internal/registry"
	"github.com/suma/suma/server/internal/task"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type projectPlanPayload struct {
	SourceID      uint
	CommitSHA     string
	CommitMessage string
	ConfigHash    string
	Snapshot      []byte
}

func (s *Service) SetImageResolver(resolver imageupdate.Resolver) { s.imageResolver = resolver }

func (s *Service) snapshotCredential(ctx context.Context, row database.ProjectCD, value snapshot) error {
	if value.Repository.Authentication.Source == "project" {
		var current gitrepo.Repository
		if err := json.Unmarshal([]byte(row.RepositoryJSON), &current); err != nil {
			return err
		}
		if current.CloneURL != value.Repository.CloneURL || current.Authentication.Source != "project" {
			return errors.New("historical project Git credential is no longer configured")
		}
	}
	repository := value.Repository
	repository.Authentication.Credential = nil
	_, _, err := s.credential(ctx, row.NodeID, repository, row.CredentialCiphertext)
	return err
}

func projectVersion(row database.ProjectCD) string {
	raw, _ := json.Marshal([]any{row.Revision, row.Enabled, row.RepositoryJSON, row.EnvironmentCiphertext, row.RegistryCredentialIDsJSON, row.ReconcileMode, row.AutomationEnabled, row.AutoRollback, row.DeploymentTimeout})
	return deployment.ConfigurationHash(string(raw), nil)
}

func (s *Service) freezeSnapshot(ctx context.Context, row database.ProjectCD, record *database.ProjectCDRecord, value *snapshot) error {
	if value.Frozen != nil {
		record.ImagesPinned = true
		return nil
	}
	resolve := s.deps.PrepareTarget
	if resolve == nil {
		resolve = s.deps.Target
	}
	target, err := resolve(ctx, row.NodeID)
	if err != nil {
		return err
	}
	if target.RuntimeIdentity != value.RuntimeIdentity {
		return errors.New("node runtime changed; prepare a new release")
	}
	spec, err := deployment.Spec(row.ProjectName, value.Worktree, value.Repository)
	if err != nil {
		return err
	}
	spec.Environment = value.Environment
	runner := s.runner.ForTarget(target)
	rendered, err := runner.Render(ctx, spec, io.Discard)
	if err != nil {
		return errors.New("cannot render release snapshot (private diagnostic hidden)")
	}
	if deployment.ConfigurationHash(rendered, spec.Environment) != record.ConfigHash {
		return errors.New("snapshot content changed; prepare again")
	}
	if err := deployment.ValidateTarget(spec, rendered, target.RemoteSources); err != nil {
		return errors.New("deployment policy rejected configuration")
	}
	resolver := s.imageResolver
	if resolver == nil {
		resolver = registry.Adapter{}
	}
	frozen, err := deployment.FreezeNode(ctx, resolver, s.registries, value.RegistryCredentialIDs, target, spec, rendered)
	if err != nil {
		return err
	}
	value.Frozen = &frozen
	record.ImagesPinned = true
	return nil
}

func (s *Service) PrepareRelease(ctx context.Context, node, name string, input deployment.PrepareReleaseInput, actor Actor) (database.Task, error) {
	if input.Operation == "" {
		input.Operation = "deploy"
	}
	if !slices.Contains([]string{"deploy", "retry", "restore", "reapply"}, input.Operation) {
		return database.Task{}, errors.New("invalid release operation")
	}
	actor.CheckOnly = true
	return s.submit(ctx, node, name, "prepare", "manual", actor, func(ctx context.Context, record *database.ProjectCDRecord, row database.ProjectCD, report task.Reporter) error {
		if input.Operation == "deploy" {
			if err := s.check(ctx, record, row, actor, report); err != nil {
				return err
			}
		} else {
			source, err := s.record(ctx, row, input.SourceRecordID)
			if err != nil {
				return err
			}
			if input.Operation == "retry" {
				if !slices.Contains([]string{"failed", "interrupted"}, source.Status) || !slices.Contains([]string{"apply", "retry", "rollback"}, source.Operation) {
					return errors.New("choose a failed publication record")
				}
				var latest database.ProjectCDRecord
				if err := s.db.WithContext(ctx).Where("project_cd_id = ? AND operation NOT IN ?", row.ID, []string{"check", "prepare", "auto_rollback"}).Order("id DESC").First(&latest).Error; err != nil {
					return err
				}
				if latest.ID != source.ID {
					return deployment.ErrPlanChanged
				}
			} else if source.Status != "succeeded" || !slices.Contains([]string{"apply", "retry", "rollback", "auto_rollback"}, source.Operation) {
				return errors.New("choose a successful publication record")
			}
			if len(source.SnapshotCiphertext) == 0 {
				return errors.New("historical configuration snapshot is incomplete")
			}
			if input.Operation != "reapply" && !source.ImagesPinned {
				return errors.New("historical images were not fixed; explicitly reapply configuration instead")
			}
			record.CommitSHA, record.CommitMessage, record.ConfigHash = source.CommitSHA, source.CommitMessage, source.ConfigHash
			record.SnapshotCiphertext = source.SnapshotCiphertext
			record.SourceRecordID = &source.ID
			record.ImagesPinned = source.ImagesPinned
		}
		value, err := s.decode(*record)
		if err != nil {
			return err
		}
		if input.Operation == "reapply" {
			value.Frozen = nil
			record.ImagesPinned = false
		}
		report(65, "Resolving fixed deployment images")
		if err := s.freezeSnapshot(ctx, row, record, &value); err != nil {
			return err
		}
		record.SnapshotCiphertext, err = s.encrypt(value)
		if err != nil {
			return err
		}
		if err := s.db.WithContext(ctx).Save(record).Error; err != nil {
			return err
		}
		view, err := s.releasePlanView(ctx, row, *record, value)
		if err != nil {
			return err
		}
		view.Operation, view.SourceRecordID = input.Operation, input.SourceRecordID
		payload := projectPlanPayload{SourceID: record.ID, CommitSHA: record.CommitSHA, CommitMessage: record.CommitMessage, ConfigHash: record.ConfigHash, Snapshot: record.SnapshotCiphertext}
		view, err = deployment.SaveReleasePlan(ctx, s.db, s.secrets, "project", row.ID, projectVersion(row), record.TaskID, payload, view)
		if err != nil {
			return err
		}
		// The original prepare record is the immutable source; confirmation creates
		// a separate publication so a check never becomes a success claim.
		report(100, "Release plan ready for confirmation")
		return s.db.WithContext(ctx).Model(&database.Task{}).Where("id = ?", record.TaskID).Update("message", "Release plan "+view.ID+" ready").Error
	})
}

func (s *Service) releasePlanView(ctx context.Context, row database.ProjectCD, record database.ProjectCDRecord, value snapshot) (deployment.ReleasePlanView, error) {
	view := deployment.ReleasePlanView{CommitSHA: record.CommitSHA, CommitMessage: record.CommitMessage, ComposeFiles: value.Repository.ComposeFiles, ImagesPinned: record.ImagesPinned, AutoRollback: value.AutoRollback, DeploymentTimeout: value.DeploymentTimeout, Nodes: []deployment.ReleaseNodeView{}}
	if err := s.snapshotCredential(ctx, row, value); err != nil {
		return view, err
	}
	if _, err := deployment.RegistryConfiguration(ctx, s.registries, row.NodeID, value.RegistryCredentialIDs); err != nil {
		return view, err
	}
	target, err := s.deps.Target(ctx, row.NodeID)
	if err != nil {
		return view, err
	}
	if value.Frozen == nil || target.RuntimeIdentity != value.Frozen.RuntimeIdentity {
		return view, deployment.ErrPlanChanged
	}
	resolver := s.imageResolver
	if resolver == nil {
		resolver = registry.Adapter{}
	}
	if err := deployment.VerifyFrozenImages(ctx, resolver, s.registries, value.RegistryCredentialIDs, row.NodeID, *value.Frozen); err != nil {
		return view, err
	}
	var previous *deployment.FrozenNode
	var oldEnvironment map[string]string
	previousCommit := ""
	canRestore := false
	if row.AppliedRecordID != nil {
		old, err := s.record(ctx, row, *row.AppliedRecordID)
		if err != nil {
			return view, err
		}
		previousCommit = old.CommitSHA
		before, err := s.decode(old)
		if err != nil {
			return view, err
		}
		previous = before.Frozen
		oldEnvironment = before.Environment
		canRestore = old.ImagesPinned && before.Frozen != nil && before.Frozen.RuntimeIdentity == target.RuntimeIdentity
	}
	node, err := deployment.NodeChanges(target, *value.Frozen, previous, value.Environment, oldEnvironment)
	if err != nil {
		return view, err
	}
	node.PreviousRecordID = row.AppliedRecordID
	node.TargetCommit = record.CommitSHA
	node.PreviousCommit = previousCommit
	node.CanRestore = canRestore
	node.Changed = node.Changed || previousCommit != record.CommitSHA
	spec, err := deployment.Spec(row.ProjectName, value.Worktree, value.Repository)
	if err != nil {
		return view, err
	}
	spec.Environment = value.Environment
	health, healthErr := s.runner.ForTarget(target).PS(ctx, value.Frozen.Apply(spec), io.Discard)
	healthy, checks, _ := deployment.RuntimeHealth(health)
	node.RuntimeHealthy = healthErr == nil && healthy
	node.HealthChecked = healthErr == nil && checks > 0
	node.Changed = node.Changed || !node.RuntimeHealthy
	view.Nodes = append(view.Nodes, node)
	return view, nil
}

func (s *Service) ReleasePlan(ctx context.Context, node, name, id string) (deployment.ReleasePlanView, error) {
	row, err := s.row(ctx, node, name)
	if err != nil {
		return deployment.ReleasePlanView{}, err
	}
	_, view, err := deployment.LoadReleasePlan(ctx, s.db, s.secrets, "project", row.ID, id, nil)
	return view, err
}

// PreparedRelease returns a plan by its preparation Task, so task.result can
// remain owned by the existing submit lifecycle.
func (s *Service) PreparedRelease(ctx context.Context, node, name, taskID string) (deployment.ReleasePlanView, error) {
	row, err := s.row(ctx, node, name)
	if err != nil {
		return deployment.ReleasePlanView{}, err
	}
	var plan database.ReleasePlan
	if err := s.db.WithContext(ctx).Where("kind = 'project' AND owner_id = ? AND preparation_task_id = ?", row.ID, taskID).First(&plan).Error; err != nil {
		return deployment.ReleasePlanView{}, err
	}
	return s.ReleasePlan(ctx, node, name, plan.ID)
}

func (s *Service) ConfirmRelease(ctx context.Context, node, name, id string, input deployment.ConfirmReleaseInput, actor Actor) (database.Task, error) {
	row, err := s.row(ctx, node, name)
	if err != nil {
		return database.Task{}, err
	}
	var payload projectPlanPayload
	plan, view, err := deployment.LoadReleasePlan(ctx, s.db, s.secrets, "project", row.ID, id, &payload)
	if err != nil {
		return database.Task{}, err
	}
	if plan.TaskID != "" {
		return s.tasks.Get(ctx, plan.TaskID)
	}
	if row.ReconcileMode == "observe" {
		return database.Task{}, errors.New("observe mode does not allow publishing")
	}
	if projectVersion(row) != plan.Version || !time.Now().Before(plan.ExpiresAt) {
		return database.Task{}, deployment.ErrPlanChanged
	}
	if _, err := deployment.SelectedReleaseNodes(view, input.NodeIDs); err != nil {
		return database.Task{}, err
	}
	if view.Operation == "deploy" && !view.Nodes[0].Changed && !input.ForceRecreate {
		return database.Task{}, errors.New("no pending changes; explicitly confirm recreation if required")
	}
	operation := "apply"
	if view.Operation == "retry" {
		operation = "retry"
	} else if view.Operation == "restore" || view.Operation == "reapply" {
		operation = "rollback"
	}
	return s.submit(ctx, node, name, operation, "manual", actor, func(ctx context.Context, record *database.ProjectCDRecord, current database.ProjectCD, report task.Reporter) error {
		record.CommitSHA, record.CommitMessage, record.ConfigHash = payload.CommitSHA, payload.CommitMessage, payload.ConfigHash
		record.SnapshotCiphertext = payload.Snapshot
		value, err := s.decode(*record)
		if err != nil {
			return err
		}
		value.ForceRecreate = input.ForceRecreate
		record.SnapshotCiphertext, err = s.encrypt(value)
		if err != nil {
			return err
		}
		record.SourceRecordID = &payload.SourceID
		record.ImagesPinned = true
		if view.SourceRecordID != 0 {
			record.SourceRecordID = &view.SourceRecordID
		}
		if err := s.db.WithContext(ctx).Save(record).Error; err != nil {
			return err
		}
		if err := s.db.WithContext(ctx).Model(&database.ReleasePlan{}).Where("id = ?", id).Update("record_id", record.ID).Error; err != nil {
			return err
		}
		if operation == "rollback" {
			return s.apply(ctx, current, record, true, report)
		}
		return s.applyWithRollback(ctx, current, record, report)
	}, func(tx *gorm.DB, task database.Task) error {
		var current database.ProjectCD
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, row.ID).Error; err != nil {
			return err
		}
		if projectVersion(current) != plan.Version {
			return deployment.ErrPlanChanged
		}
		before, now := view.Nodes[0].PreviousRecordID, current.AppliedRecordID
		if (before == nil) != (now == nil) || before != nil && now != nil && *before != *now {
			return deployment.ErrPlanChanged
		}
		if view.Operation == "retry" {
			var latest database.ProjectCDRecord
			if err := tx.Where("project_cd_id = ? AND operation NOT IN ?", row.ID, []string{"check", "prepare", "auto_rollback"}).Order("id DESC").First(&latest).Error; err != nil {
				return err
			}
			if latest.ID != view.SourceRecordID {
				return deployment.ErrPlanChanged
			}
		}
		if err := deployment.ClaimReleasePlan(tx, id, plan.Version, task.ID); err != nil {
			return err
		}
		if operation == "rollback" {
			return tx.Model(&current).Update("reconcile_mode", "manual").Error
		}
		return nil
	})
}
