package cd

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/compose"
	credentialrepo "github.com/suma/suma/server/internal/credential"
	"github.com/suma/suma/server/internal/database"
	gitrepo "github.com/suma/suma/server/internal/git"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/task"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	ModeObserve = "observe"
	ModeManual  = "manual"
	ModeAuto    = "auto"

	StatusValidating       = "validating"
	StatusAwaitingApproval = "awaiting_approval"
	StatusApproved         = "approved"
	StatusRejected         = "rejected"
	StatusPulling          = "pulling"
	StatusDeploying        = "deploying"
	StatusVerifying        = "verifying"
	StatusSucceeded        = "succeeded"
	StatusFailed           = "failed"
	StatusRollingBack      = "rolling_back"
	StatusRolledBack       = "rolled_back"
	StatusPartialFailed    = "partial_failed"
)

type ConfigureInput struct {
	Repository            gitrepo.Repository `json:"repository"`
	ReconcileMode         string             `json:"reconcile_mode"`
	SyncIntervalSeconds   int                `json:"sync_interval_seconds"`
	AutoRollback          bool               `json:"auto_rollback"`
	DeploymentTimeout     int                `json:"deployment_timeout"`
	WebhookEnabled        bool               `json:"webhook_enabled"`
	WebhookSecret         string             `json:"webhook_secret"`
	NodeIDs               []string           `json:"node_ids,omitempty"`
	RegistryCredentialIDs []uint             `json:"registry_credential_ids,omitempty"`
}

type Configuration struct {
	Configured            bool               `json:"configured"`
	Repository            gitrepo.Repository `json:"repository"`
	ReconcileMode         string             `json:"reconcile_mode"`
	SyncIntervalSeconds   int                `json:"sync_interval_seconds"`
	DesiredCommit         string             `json:"desired_commit"`
	ObservedCommit        string             `json:"observed_commit"`
	ActiveReleaseID       *uint              `json:"active_release_id,omitempty"`
	AutoRollback          bool               `json:"auto_rollback"`
	DeploymentTimeout     int                `json:"deployment_timeout"`
	WebhookEnabled        bool               `json:"webhook_enabled"`
	WebhookID             string             `json:"webhook_id,omitempty"`
	WebhookSecret         string             `json:"webhook_secret,omitempty"`
	NodeIDs               []string           `json:"node_ids"`
	RegistryCredentialIDs []uint             `json:"registry_credential_ids"`
}

type Project struct {
	ID              uint      `json:"id"`
	Name            string    `json:"name"`
	Configured      bool      `json:"configured"`
	RepositoryURL   string    `json:"repository_url,omitempty"`
	GitRef          string    `json:"git_ref,omitempty"`
	DesiredCommit   string    `json:"desired_commit,omitempty"`
	ObservedCommit  string    `json:"observed_commit,omitempty"`
	ActiveReleaseID *uint     `json:"active_release_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	NodeIDs         []string  `json:"node_ids"`
}

type Drift struct {
	Drifted         bool   `json:"drifted"`
	DesiredCommit   string `json:"desired_commit"`
	ObservedCommit  string `json:"observed_commit"`
	ActiveCommit    string `json:"active_commit"`
	ActiveReleaseID *uint  `json:"active_release_id,omitempty"`
	Reason          string `json:"reason,omitempty"`
	RuntimeHealthy  bool   `json:"runtime_healthy"`
}

type Actor struct {
	UserID *uint
	Name   string
	IP     string
}

type Service struct {
	db               *gorm.DB
	git              gitrepo.Client
	credentials      *gitrepo.CredentialService
	compose          compose.Runner
	tasks            *task.Service
	audit            *audit.Service
	secrets          *secret.Store
	mu               sync.Mutex
	locks            map[uint]*sync.Mutex
	queued           map[uint]bool
	backgroundMu     sync.Mutex
	backgroundCancel context.CancelFunc
	backgroundWG     sync.WaitGroup
	targets          TargetResolver
	registries       *credentialrepo.RegistryService
}

type TargetResolver interface {
	ResolveComposeTarget(context.Context, string) (compose.Target, error)
}
type TargetedRunner interface {
	Targeted(compose.Target) compose.Runner
}

func NewService(db *gorm.DB, git gitrepo.Client, credentials *gitrepo.CredentialService, runner compose.Runner, tasks *task.Service, audits *audit.Service, secrets *secret.Store) *Service {
	return &Service{db: db, git: git, credentials: credentials, compose: runner, tasks: tasks, audit: audits, secrets: secrets, locks: map[uint]*sync.Mutex{}, queued: map[uint]bool{}}
}
func (s *Service) SetTargetResolver(resolver TargetResolver) { s.targets = resolver }
func (s *Service) SetRegistryCredentials(service *credentialrepo.RegistryService) {
	s.registries = service
}

var validProjectName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

func (s *Service) CreateProject(ctx context.Context, name string) (Project, error) {
	return s.CreateProjectOnNodes(ctx, name, []string{"local"})
}
func (s *Service) CreateProjectOnNodes(ctx context.Context, name string, nodeIDs []string) (Project, error) {
	if !validProjectName.MatchString(name) {
		return Project{}, errors.New("invalid project name")
	}
	row := database.DeliveryProject{NodeID: "local", Name: name, ReconcileMode: ModeManual, SyncIntervalSeconds: 300, DeploymentTimeout: 120}
	if err := s.validateNodeIDs(ctx, nodeIDs); err != nil {
		return Project{}, err
	}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return Project{}, err
	}
	row.DeploymentName = deliveryRuntimeName(row.ID)
	if err := s.db.WithContext(ctx).Model(&row).Update("deployment_name", row.DeploymentName).Error; err != nil {
		return Project{}, err
	}
	if err := s.replaceTargets(ctx, s.db, row.ID, nodeIDs); err != nil {
		return Project{}, err
	}
	value := projectSummary(row)
	value.NodeIDs = append([]string(nil), nodeIDs...)
	return value, nil
}

func (s *Service) ListProjects(ctx context.Context) ([]Project, error) {
	var rows []database.DeliveryProject
	if err := s.db.WithContext(ctx).Order("updated_at DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	projects := make([]Project, 0, len(rows))
	for _, row := range rows {
		value := projectSummary(row)
		value.NodeIDs, _ = s.projectNodeIDs(ctx, row.ID)
		projects = append(projects, value)
	}
	return projects, nil
}

func (s *Service) GetProject(ctx context.Context, name string) (Project, error) {
	row, err := s.project(ctx, name)
	if err != nil {
		return Project{}, err
	}
	value := projectSummary(row)
	value.NodeIDs, err = s.projectNodeIDs(ctx, row.ID)
	return value, err
}

func projectSummary(row database.DeliveryProject) Project {
	return Project{ID: row.ID, Name: row.Name, Configured: row.GitCloneURL != "", RepositoryURL: row.GitCloneURL, GitRef: row.GitRef, DesiredCommit: row.DesiredCommit, ObservedCommit: row.ObservedCommit, ActiveReleaseID: row.ActiveReleaseID, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

func (s *Service) Configure(ctx context.Context, name string, input ConfigureInput) (Configuration, error) {
	if err := gitrepo.ValidateRepository(input.Repository); err != nil {
		return Configuration{}, err
	}
	if input.ReconcileMode != ModeObserve && input.ReconcileMode != ModeManual && input.ReconcileMode != ModeAuto {
		return Configuration{}, errors.New("reconcile mode must be observe, manual, or auto")
	}
	if input.SyncIntervalSeconds < 30 || input.SyncIntervalSeconds > 86400 {
		return Configuration{}, errors.New("sync interval must be between 30 and 86400 seconds")
	}
	if input.DeploymentTimeout < 10 || input.DeploymentTimeout > 3600 {
		return Configuration{}, errors.New("deployment timeout must be between 10 and 3600 seconds")
	}
	if input.WebhookSecret != "" && (len(input.WebhookSecret) < 16 || len(input.WebhookSecret) > 4096) {
		return Configuration{}, errors.New("webhook secret must be between 16 and 4096 bytes")
	}
	files, _ := json.Marshal(input.Repository.ComposeFiles)
	var current database.DeliveryProject
	if err := s.db.WithContext(ctx).Where("name = ?", name).First(&current).Error; err != nil {
		return Configuration{}, err
	}
	nodeIDs := input.NodeIDs
	if nodeIDs == nil {
		nodeIDs, _ = s.projectNodeIDs(ctx, current.ID)
	}
	if err := s.validateNodeIDs(ctx, nodeIDs); err != nil {
		return Configuration{}, err
	}
	registryIDs := input.RegistryCredentialIDs
	if registryIDs == nil {
		registryIDs, _ = s.projectRegistryCredentialIDs(ctx, current.ID)
	}
	if len(registryIDs) > 0 && s.registries == nil {
		return Configuration{}, errors.New("registry credential service is unavailable")
	}
	for _, credentialID := range registryIDs {
		for _, nodeID := range nodeIDs {
			if err := s.registries.AuthorizedForNode(ctx, credentialID, nodeID); err != nil {
				return Configuration{}, fmt.Errorf("registry credential %d: %w", credentialID, err)
			}
		}
	}
	authentication := input.Repository.Authentication
	var centerCredential database.GitCredential
	var projectCredential database.DeliveryProjectGitCredential
	switch authentication.Source {
	case gitrepo.CredentialSourceNone:
		if authentication.CredentialID != nil || authentication.Credential != nil || authentication.Summary != nil || authentication.SaveToCenter {
			return Configuration{}, errors.New("no-authentication source cannot include a credential")
		}
	case gitrepo.CredentialSourceCenter:
		if authentication.CredentialID == nil || authentication.Credential != nil || authentication.Summary != nil || authentication.SaveToCenter {
			return Configuration{}, errors.New("authentication center credential ID is required")
		}
		if err := s.db.WithContext(ctx).First(&centerCredential, *authentication.CredentialID).Error; err != nil {
			return Configuration{}, errors.New("Git credential not found")
		}
		if err := validateCredentialTransport(input.Repository.CloneURL, centerCredential.AuthType); err != nil {
			return Configuration{}, err
		}
		if err := s.credentials.AuthorizedForNodes(ctx, centerCredential.ID, nodeIDs); err != nil {
			return Configuration{}, err
		}
	case gitrepo.CredentialSourceProject:
		if authentication.CredentialID != nil || authentication.Summary != nil {
			return Configuration{}, errors.New("project authentication cannot reference an authentication center credential")
		}
		if authentication.SaveToCenter && authentication.Credential == nil {
			return Configuration{}, errors.New("a new project credential is required before saving it to the authentication center")
		}
		if authentication.Credential != nil {
			if err := validateCredentialTransport(input.Repository.CloneURL, authentication.Credential.AuthType); err != nil {
				return Configuration{}, err
			}
		} else {
			if err := s.db.WithContext(ctx).Where("project_id = ?", current.ID).First(&projectCredential).Error; err != nil {
				return Configuration{}, errors.New("project credential is required")
			}
			if err := validateCredentialTransport(input.Repository.CloneURL, projectCredential.AuthType); err != nil {
				return Configuration{}, err
			}
		}
	default:
		return Configuration{}, errors.New("authentication source must be none, center, or project")
	}
	webhookID := current.WebhookID
	generatedSecret := ""
	if input.WebhookEnabled && webhookID == "" {
		generatedID, generateErr := randomHex(16)
		if generateErr != nil {
			return Configuration{}, generateErr
		}
		webhookID = generatedID
	}
	updates := map[string]any{
		"git_clone_url": input.Repository.CloneURL,
		"git_ref_type":  input.Repository.RefType, "git_ref": input.Repository.Ref,
		"compose_files_json": string(files),
		"environment_file":   input.Repository.EnvironmentFile, "reconcile_mode": input.ReconcileMode,
		"sync_interval_seconds": input.SyncIntervalSeconds, "auto_rollback": input.AutoRollback,
		"deployment_timeout": input.DeploymentTimeout,
		"webhook_enabled":    input.WebhookEnabled, "webhook_id": webhookID,
	}
	if current.GitCloneURL != input.Repository.CloneURL || current.GitRefType != input.Repository.RefType || current.GitRef != input.Repository.Ref || current.ComposeFilesJSON != string(files) || current.EnvironmentFile != input.Repository.EnvironmentFile {
		updates["desired_commit"] = ""
		updates["observed_commit"] = ""
		updates["last_sync_at"] = nil
	}
	if input.WebhookEnabled && input.WebhookSecret == "" && len(current.WebhookSecret) == 0 {
		value, generateErr := randomHex(32)
		if generateErr != nil {
			return Configuration{}, generateErr
		}
		generatedSecret = value
		input.WebhookSecret = generatedSecret
	}
	if input.WebhookSecret != "" {
		ciphertext, err := s.secrets.Encrypt(input.WebhookSecret)
		if err != nil {
			return Configuration{}, err
		}
		updates["webhook_secret"] = ciphertext
	}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var credentialID *uint
		switch authentication.Source {
		case gitrepo.CredentialSourceNone:
			if err := s.credentials.DeleteProject(ctx, tx, current.ID); err != nil {
				return err
			}
		case gitrepo.CredentialSourceCenter:
			credentialID = authentication.CredentialID
			if err := s.credentials.DeleteProject(ctx, tx, current.ID); err != nil {
				return err
			}
		case gitrepo.CredentialSourceProject:
			if authentication.SaveToCenter {
				created, err := s.credentials.CreateWithDB(ctx, tx, *authentication.Credential)
				if err != nil {
					return err
				}
				credentialID = &created.ID
				if err := s.credentials.DeleteProject(ctx, tx, current.ID); err != nil {
					return err
				}
			} else if authentication.Credential != nil {
				if _, err := s.credentials.UpsertProject(ctx, tx, current.ID, *authentication.Credential); err != nil {
					return err
				}
			}
		}
		updates["git_credential_id"] = credentialID
		if err := s.replaceTargets(ctx, tx, current.ID, nodeIDs); err != nil {
			return err
		}
		if err := s.replaceRegistryCredentials(ctx, tx, current.ID, registryIDs); err != nil {
			return err
		}
		result := tx.WithContext(ctx).Model(&database.DeliveryProject{}).Where("id = ?", current.ID).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	}); err != nil {
		return Configuration{}, err
	}
	configuration, err := s.GetConfiguration(ctx, name)
	configuration.WebhookSecret = generatedSecret
	return configuration, err
}

func validateCredentialTransport(cloneURL, authType string) error {
	transport, err := gitrepo.CloneTransport(cloneURL)
	if err != nil {
		return err
	}
	if transport == "ssh" && authType != gitrepo.AuthSSHKey && authType != gitrepo.AuthNone {
		return errors.New("SSH repositories require an SSH credential")
	}
	if transport == "https" && authType == gitrepo.AuthSSHKey {
		return errors.New("HTTPS repositories require an HTTP credential")
	}
	return nil
}

func (s *Service) GetConfiguration(ctx context.Context, name string) (Configuration, error) {
	project, err := s.project(ctx, name)
	if err != nil {
		return Configuration{}, err
	}
	if project.GitCloneURL == "" {
		nodeIDs, _ := s.projectNodeIDs(ctx, project.ID)
		return Configuration{
			Configured:          false,
			Repository:          gitrepo.Repository{RefType: gitrepo.RefBranch, Ref: "main", Authentication: gitrepo.Authentication{Source: gitrepo.CredentialSourceNone}, ComposeFiles: []string{"compose.yml"}},
			ReconcileMode:       ModeManual,
			SyncIntervalSeconds: 300,
			DeploymentTimeout:   120,
			NodeIDs:             nodeIDs,
		}, nil
	}
	files := []string{}
	if project.ComposeFilesJSON != "" {
		if err := json.Unmarshal([]byte(project.ComposeFilesJSON), &files); err != nil {
			return Configuration{}, fmt.Errorf("decode Compose file list: %w", err)
		}
	}
	authentication := gitrepo.Authentication{Source: gitrepo.CredentialSourceNone}
	if project.GitCredentialID != nil {
		authentication = gitrepo.Authentication{Source: gitrepo.CredentialSourceCenter, CredentialID: project.GitCredentialID}
	} else if row, err := s.credentials.ProjectSummary(ctx, project.ID); err == nil {
		authentication = gitrepo.Authentication{Source: gitrepo.CredentialSourceProject, Summary: &gitrepo.CredentialSummary{Name: row.Name, AuthType: row.AuthType, Username: row.Username, Fingerprint: row.Fingerprint}}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return Configuration{}, err
	}
	nodeIDs, err := s.projectNodeIDs(ctx, project.ID)
	if err != nil {
		return Configuration{}, err
	}
	registryIDs, err := s.projectRegistryCredentialIDs(ctx, project.ID)
	if err != nil {
		return Configuration{}, err
	}
	return Configuration{
		Configured:    true,
		Repository:    gitrepo.Repository{CloneURL: project.GitCloneURL, RefType: project.GitRefType, Ref: project.GitRef, CredentialID: project.GitCredentialID, Authentication: authentication, ComposeFiles: files, EnvironmentFile: project.EnvironmentFile},
		ReconcileMode: project.ReconcileMode, SyncIntervalSeconds: project.SyncIntervalSeconds,
		DesiredCommit: project.DesiredCommit, ObservedCommit: project.ObservedCommit, ActiveReleaseID: project.ActiveReleaseID,
		AutoRollback: project.AutoRollback, DeploymentTimeout: project.DeploymentTimeout,
		WebhookEnabled: project.WebhookEnabled, WebhookID: project.WebhookID,
		NodeIDs: nodeIDs, RegistryCredentialIDs: registryIDs,
	}, nil
}

func (s *Service) Sync(ctx context.Context, name, trigger string, actor Actor) (database.Task, error) {
	project, err := s.project(ctx, name)
	if err != nil {
		return database.Task{}, err
	}
	if project.GitCloneURL == "" {
		return database.Task{}, errors.New("delivery project is not configured")
	}
	if !s.reserve(project.ID) {
		return database.Task{}, errors.New("a reconciliation is already queued for this project")
	}
	row, err := s.tasks.StartWithID("cd.sync", "Sync "+name, func(ctx context.Context, taskID string, report task.Reporter) error {
		defer s.releaseReservation(project.ID)
		lock := s.projectLock(project.ID)
		lock.Lock()
		defer lock.Unlock()
		err := s.syncLocked(ctx, project.ID, taskID, trigger, actor, report)
		s.recordFinal(actor, "cd.sync", name, taskID, nil, err)
		return err
	})
	if err != nil {
		s.releaseReservation(project.ID)
	}
	return row, err
}

func (s *Service) syncLocked(ctx context.Context, projectID uint, taskID, trigger string, actor Actor, report task.Reporter) error {
	var project database.DeliveryProject
	if err := s.db.WithContext(ctx).First(&project, projectID).Error; err != nil {
		return err
	}
	repository, err := repositoryFromProject(project)
	if err != nil {
		return err
	}
	credential := gitrepo.CredentialMaterial{AuthType: gitrepo.AuthNone}
	if project.GitCredentialID != nil {
		credential, err = s.credentials.Material(ctx, *project.GitCredentialID)
		if err != nil {
			return fmt.Errorf("load Git credential: %w", err)
		}
	} else if credential, err = s.credentials.ProjectMaterial(ctx, project.ID); errors.Is(err, gorm.ErrRecordNotFound) {
		credential, err = gitrepo.CredentialMaterial{AuthType: gitrepo.AuthNone}, nil
	} else if err != nil {
		return fmt.Errorf("load project Git credential: %w", err)
	}
	report(5, "Fetching Git repository")
	revision, err := s.git.Sync(ctx, gitrepo.SyncRequest{ProjectID: project.ID, Repository: repository, Credential: credential}, &reportWriter{report: report, progress: 15})
	if err != nil {
		return err
	}
	if err := s.git.Verify(ctx, revision.WorktreePath, revision.CommitSHA); err != nil {
		return err
	}
	if err := s.db.WithContext(ctx).Model(&project).Update("desired_commit", revision.CommitSHA).Error; err != nil {
		return err
	}
	now := time.Now()
	if err := s.db.WithContext(ctx).Model(&project).Update("last_sync_at", now).Error; err != nil {
		return err
	}
	spec, err := executionSpec(project, repository, revision.WorktreePath)
	if err != nil {
		return err
	}
	if err := validateComposeSources(spec, revision.WorktreePath); err != nil {
		return fmt.Errorf("deployment source policy rejected the release: %w", err)
	}
	report(30, "Validating Compose configuration")
	validationRunner, err := s.runnerForProject(ctx, project.ID)
	if err != nil {
		return err
	}
	if err := validationRunner.ValidateRelease(ctx, spec, io.Discard); err != nil {
		return err
	}
	rendered, err := validationRunner.Render(ctx, spec, io.Discard)
	if err != nil {
		return err
	}
	if err := s.validateDeploymentTargets(ctx, project.ID, revision.WorktreePath, rendered); err != nil {
		return fmt.Errorf("deployment policy rejected the release: %w", err)
	}
	hash := sha256.Sum256([]byte(rendered))
	configHash := hex.EncodeToString(hash[:])
	images := imageReferences(rendered)
	var existing database.DeliveryRelease
	err = s.db.WithContext(ctx).Where("project_id = ? AND commit_sha = ? AND config_hash = ? AND status IN ?", project.ID, revision.CommitSHA, configHash, []string{StatusAwaitingApproval, StatusApproved, StatusSucceeded, StatusRolledBack}).Order("id DESC").First(&existing).Error
	if err == nil {
		if err := s.db.WithContext(ctx).Model(&project).Update("observed_commit", revision.CommitSHA).Error; err != nil {
			return err
		}
		if project.ReconcileMode != ModeAuto || (project.ActiveReleaseID != nil && *project.ActiveReleaseID == existing.ID) {
			report(100, "Commit already reconciled")
			return nil
		}
		if existing.Status == StatusAwaitingApproval || existing.Status == StatusApproved {
			return s.deployLocked(ctx, project, existing, taskID, false, report)
		}
		reconcileRelease := database.DeliveryRelease{ProjectID: existing.ProjectID, RepositoryURL: existing.RepositoryURL, GitRef: existing.GitRef, CommitSHA: existing.CommitSHA, CommitMessage: existing.CommitMessage, CommitAuthor: existing.CommitAuthor, ConfigHash: existing.ConfigHash, ImageReferences: existing.ImageReferences, TaskID: taskID, Status: StatusAwaitingApproval, TriggerType: "reconcile", TriggerActor: actor.Name, PreviousReleaseID: project.ActiveReleaseID, WorktreePath: existing.WorktreePath, ComposeFilesJSON: existing.ComposeFilesJSON, EnvironmentFile: existing.EnvironmentFile}
		if err := s.db.WithContext(ctx).Create(&reconcileRelease).Error; err != nil {
			return err
		}
		if err := s.snapshotReleaseTargets(ctx, project.ID, reconcileRelease.ID); err != nil {
			return err
		}
		return s.deployLocked(ctx, project, reconcileRelease, taskID, false, report)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	filesJSON, _ := json.Marshal(repository.ComposeFiles)
	imagesJSON, _ := json.Marshal(images)
	status := StatusAwaitingApproval
	if project.ReconcileMode == ModeObserve {
		status = StatusAwaitingApproval
	}
	release := database.DeliveryRelease{
		ProjectID: project.ID, RepositoryURL: project.GitCloneURL,
		GitRef: project.GitRef, CommitSHA: revision.CommitSHA, CommitMessage: revision.CommitMessage,
		CommitAuthor: revision.CommitAuthor, ConfigHash: configHash, ImageReferences: string(imagesJSON),
		TaskID: taskID, Status: status, TriggerType: trigger, TriggerActor: actor.Name,
		PreviousReleaseID: project.ActiveReleaseID, WorktreePath: revision.WorktreePath, ComposeFilesJSON: string(filesJSON), EnvironmentFile: repository.EnvironmentFile,
	}
	if err := s.db.WithContext(ctx).Create(&release).Error; err != nil {
		return err
	}
	if err := s.snapshotReleaseTargets(ctx, project.ID, release.ID); err != nil {
		return err
	}
	if err := s.db.WithContext(ctx).Model(&project).Update("observed_commit", revision.CommitSHA).Error; err != nil {
		return err
	}
	if project.ReconcileMode == ModeAuto {
		return s.deployLocked(ctx, project, release, taskID, false, report)
	}
	report(100, "Release is ready for approval")
	return nil
}

func (s *Service) Deploy(ctx context.Context, name string, releaseID uint, actor Actor) (database.Task, error) {
	project, release, err := s.projectRelease(ctx, name, releaseID)
	if err != nil {
		return database.Task{}, err
	}
	if project.ReconcileMode == ModeObserve {
		return database.Task{}, errors.New("observe mode does not allow deployments")
	}
	if release.Status != StatusApproved && release.Status != StatusFailed {
		return database.Task{}, fmt.Errorf("release cannot be deployed from status %s", release.Status)
	}
	if !s.reserve(project.ID) {
		return database.Task{}, errors.New("another delivery operation is already queued for this project")
	}
	row, err := s.tasks.StartWithID("cd.deploy", "Deploy "+name, func(ctx context.Context, taskID string, report task.Reporter) error {
		defer s.releaseReservation(project.ID)
		lock := s.projectLock(project.ID)
		lock.Lock()
		defer lock.Unlock()
		if err := s.db.WithContext(ctx).First(&project, project.ID).Error; err != nil {
			return err
		}
		if err := s.db.WithContext(ctx).First(&release, release.ID).Error; err != nil {
			return err
		}
		err := s.deployLocked(ctx, project, release, taskID, false, report)
		s.recordFinal(actor, "cd.deploy", name, taskID, &release.ID, err)
		return err
	})
	if err != nil {
		s.releaseReservation(project.ID)
	}
	return row, err
}

func (s *Service) Approve(ctx context.Context, name string, releaseID uint, actor Actor) (database.DeliveryRelease, error) {
	project, release, err := s.projectRelease(ctx, name, releaseID)
	if err != nil {
		return release, err
	}
	if project.ReconcileMode == ModeObserve {
		return release, errors.New("observe mode does not allow approvals")
	}
	if actor.UserID == nil {
		return release, errors.New("an authenticated user is required to approve a release")
	}
	if !s.reserve(project.ID) {
		return release, errors.New("another delivery operation is already queued for this project")
	}
	defer s.releaseReservation(project.ID)
	lock := s.projectLock(project.ID)
	lock.Lock()
	defer lock.Unlock()
	if err := s.db.WithContext(ctx).First(&release, release.ID).Error; err != nil {
		return release, err
	}
	if release.Status != StatusAwaitingApproval {
		return release, fmt.Errorf("release cannot be approved from status %s", release.Status)
	}
	now := time.Now()
	if err := s.db.WithContext(ctx).Model(&release).Updates(map[string]any{"status": StatusApproved, "approved_by": *actor.UserID, "approved_at": now}).Error; err != nil {
		return release, err
	}
	s.recordFinal(actor, "cd.approve", name, release.TaskID, &release.ID, nil)
	return release, s.db.WithContext(ctx).First(&release, release.ID).Error
}

func (s *Service) Reject(ctx context.Context, name string, releaseID uint, actor Actor) (database.DeliveryRelease, error) {
	project, release, err := s.projectRelease(ctx, name, releaseID)
	if err != nil {
		return release, err
	}
	if !s.reserve(project.ID) {
		return release, errors.New("another delivery operation is already queued for this project")
	}
	defer s.releaseReservation(project.ID)
	lock := s.projectLock(project.ID)
	lock.Lock()
	defer lock.Unlock()
	if err := s.db.WithContext(ctx).First(&release, release.ID).Error; err != nil {
		return release, err
	}
	if release.Status != StatusAwaitingApproval && release.Status != StatusApproved {
		return release, fmt.Errorf("release cannot be rejected from status %s", release.Status)
	}
	now := time.Now()
	if err := s.db.WithContext(ctx).Model(&release).Updates(map[string]any{"status": StatusRejected, "finished_at": now, "failure_reason": "Rejected by " + actor.Name}).Error; err != nil {
		return release, err
	}
	s.recordFinal(actor, "cd.reject", name, release.TaskID, &release.ID, nil)
	return release, s.db.WithContext(ctx).First(&release, release.ID).Error
}

func (s *Service) Rollback(ctx context.Context, name string, releaseID uint, actor Actor) (database.Task, error) {
	project, release, err := s.projectRelease(ctx, name, releaseID)
	if err != nil {
		return database.Task{}, err
	}
	if release.Status != StatusSucceeded && release.Status != StatusRolledBack {
		return database.Task{}, errors.New("only a previously successful release can be restored")
	}
	if project.ReconcileMode == ModeObserve {
		return database.Task{}, errors.New("observe mode does not allow rollbacks")
	}
	if !s.reserve(project.ID) {
		return database.Task{}, errors.New("another delivery operation is already queued for this project")
	}
	if project.ReconcileMode == ModeAuto {
		if err := s.db.WithContext(ctx).Model(&project).Update("reconcile_mode", ModeManual).Error; err != nil {
			s.releaseReservation(project.ID)
			return database.Task{}, err
		}
		project.ReconcileMode = ModeManual
	}
	rollbackRelease := database.DeliveryRelease{
		ProjectID: release.ProjectID, RepositoryURL: release.RepositoryURL,
		GitRef: release.GitRef, CommitSHA: release.CommitSHA, CommitMessage: release.CommitMessage,
		CommitAuthor: release.CommitAuthor, ConfigHash: release.ConfigHash, ImageReferences: release.ImageReferences,
		Status: StatusAwaitingApproval, TriggerType: "rollback", TriggerActor: actor.Name,
		PreviousReleaseID: project.ActiveReleaseID, WorktreePath: release.WorktreePath, ComposeFilesJSON: release.ComposeFilesJSON, EnvironmentFile: release.EnvironmentFile,
	}
	if err := s.db.WithContext(ctx).Create(&rollbackRelease).Error; err != nil {
		s.releaseReservation(project.ID)
		return database.Task{}, err
	}
	if err := s.snapshotReleaseTargets(ctx, project.ID, rollbackRelease.ID); err != nil {
		s.releaseReservation(project.ID)
		return database.Task{}, err
	}
	row, err := s.tasks.StartWithID("cd.rollback", "Roll back "+name, func(ctx context.Context, taskID string, report task.Reporter) error {
		defer s.releaseReservation(project.ID)
		lock := s.projectLock(project.ID)
		lock.Lock()
		defer lock.Unlock()
		if err := s.db.WithContext(ctx).First(&project, project.ID).Error; err != nil {
			return err
		}
		if err := s.db.WithContext(ctx).First(&rollbackRelease, rollbackRelease.ID).Error; err != nil {
			return err
		}
		err := s.deployLocked(ctx, project, rollbackRelease, taskID, true, report)
		s.recordFinal(actor, "cd.rollback", name, taskID, &rollbackRelease.ID, err)
		return err
	})
	if err != nil {
		s.releaseReservation(project.ID)
		_ = s.db.WithContext(ctx).Delete(&rollbackRelease).Error
	}
	return row, err
}

func (s *Service) deployLocked(ctx context.Context, project database.DeliveryProject, release database.DeliveryRelease, taskID string, rollback bool, report task.Reporter) error {
	if s.targets != nil {
		return s.deployTargetsLocked(ctx, project, release, taskID, rollback, report)
	}
	if err := s.git.Verify(ctx, release.WorktreePath, release.CommitSHA); err != nil {
		return s.failRelease(ctx, release.ID, err)
	}
	spec, err := releaseExecutionSpec(runtimeName(project), release)
	if err != nil {
		return err
	}
	now := time.Now()
	status := StatusPulling
	if rollback {
		status = StatusRollingBack
	}
	if err := s.db.WithContext(ctx).Model(&release).Updates(map[string]any{"status": status, "task_id": taskID, "started_at": now, "failure_reason": ""}).Error; err != nil {
		return err
	}
	report(15, "Pulling deployment images")
	if err := s.compose.PullRelease(ctx, spec, &reportWriter{report: report, progress: 30}); err != nil {
		return s.failRelease(ctx, release.ID, err)
	}
	if err := s.db.WithContext(ctx).Model(&release).Update("status", StatusDeploying).Error; err != nil {
		return err
	}
	report(55, "Applying Compose release")
	if err := s.compose.UpRelease(ctx, spec, project.DeploymentTimeout, &reportWriter{report: report, progress: 70}); err != nil {
		failure := s.failRelease(ctx, release.ID, err)
		if project.AutoRollback && !rollback && release.PreviousReleaseID != nil {
			_ = s.db.WithContext(ctx).Model(&project).Update("reconcile_mode", ModeManual).Error
			project.ReconcileMode = ModeManual
			var previous database.DeliveryRelease
			if loadErr := s.db.WithContext(ctx).First(&previous, *release.PreviousReleaseID).Error; loadErr == nil {
				report(80, "Deployment failed; restoring previous release")
				restore := database.DeliveryRelease{ProjectID: previous.ProjectID, RepositoryURL: previous.RepositoryURL, GitRef: previous.GitRef, CommitSHA: previous.CommitSHA, CommitMessage: previous.CommitMessage, CommitAuthor: previous.CommitAuthor, ConfigHash: previous.ConfigHash, ImageReferences: previous.ImageReferences, TaskID: taskID, Status: StatusAwaitingApproval, TriggerType: "automatic_rollback", TriggerActor: "SUMA", PreviousReleaseID: &release.ID, WorktreePath: previous.WorktreePath, ComposeFilesJSON: previous.ComposeFilesJSON, EnvironmentFile: previous.EnvironmentFile}
				if createErr := s.db.WithContext(ctx).Create(&restore).Error; createErr == nil {
					_ = s.deployLocked(ctx, project, restore, taskID, true, report)
				}
			}
		}
		return failure
	}
	if err := s.db.WithContext(ctx).Model(&release).Update("status", StatusVerifying).Error; err != nil {
		return err
	}
	report(85, "Reading deployed service health")
	health, err := s.compose.PS(ctx, spec, io.Discard)
	if err != nil {
		return s.failRelease(ctx, release.ID, err)
	}
	if !runtimeIsHealthy(health) {
		return s.failRelease(ctx, release.ID, errors.New("Compose services did not reach a running and healthy state"))
	}
	finished := time.Now()
	finalStatus := StatusSucceeded
	if rollback {
		finalStatus = StatusRolledBack
	}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&release).Updates(map[string]any{"status": finalStatus, "health_summary": health, "finished_at": finished, "failure_reason": ""}).Error; err != nil {
			return err
		}
		return tx.Model(&project).Updates(map[string]any{"active_release_id": release.ID, "observed_commit": release.CommitSHA}).Error
	}); err != nil {
		return err
	}
	report(100, "Compose release is healthy")
	return nil
}

type targetResult struct {
	success    bool
	rolledBack bool
	err        error
}

func (s *Service) deployTargetsLocked(ctx context.Context, project database.DeliveryProject, release database.DeliveryRelease, taskID string, rollback bool, report task.Reporter) error {
	if err := s.git.Verify(ctx, release.WorktreePath, release.CommitSHA); err != nil {
		return s.failRelease(ctx, release.ID, err)
	}
	targeted, ok := s.compose.(TargetedRunner)
	if !ok {
		return s.failRelease(ctx, release.ID, errors.New("Compose runner does not support node targets"))
	}
	if err := s.snapshotReleaseTargets(ctx, project.ID, release.ID); err != nil {
		return s.failRelease(ctx, release.ID, err)
	}
	var deployments []database.DeliveryReleaseDeployment
	if err := s.db.WithContext(ctx).Where("release_id = ?", release.ID).Order("node_id ASC").Find(&deployments).Error; err != nil {
		return s.failRelease(ctx, release.ID, err)
	}
	if len(deployments) == 0 {
		return s.failRelease(ctx, release.ID, errors.New("release has no deployment targets"))
	}
	now := time.Now()
	startStatus := StatusPulling
	if rollback {
		startStatus = StatusRollingBack
	}
	if err := s.db.WithContext(ctx).Model(&release).Updates(map[string]any{"status": startStatus, "task_id": taskID, "started_at": now, "failure_reason": ""}).Error; err != nil {
		return err
	}
	report(10, fmt.Sprintf("Deploying to %d Docker nodes", len(deployments)))
	results := make(chan targetResult, len(deployments))
	childIDs := make([]string, 0, len(deployments))
	for index := range deployments {
		deployment := deployments[index]
		target, err := s.composeTargetForProject(ctx, project.ID, deployment.NodeID)
		if err != nil {
			_ = s.failDeployment(ctx, deployment.ID, err)
			results <- targetResult{err: err}
			continue
		}
		var state database.DeliveryTargetState
		if err := s.db.WithContext(ctx).Where("project_id = ? AND node_id = ?", project.ID, deployment.NodeID).First(&state).Error; err == nil {
			deployment.PreviousReleaseID = state.ActiveReleaseID
			_ = s.db.Model(&deployment).Update("previous_release_id", state.ActiveReleaseID).Error
		}
		child, err := s.tasks.StartWithIDForNode(target.NodeID, target.NodeName, "cd.deploy.node", "Deploy "+project.Name+" to "+target.NodeName, func(childCtx context.Context, childID string, childReport task.Reporter) error {
			_ = s.db.Model(&database.DeliveryReleaseDeployment{}).Where("id = ?", deployment.ID).Update("task_id", childID).Error
			err := s.deployOneTarget(childCtx, project, release, deployment, targeted.Targeted(target), rollback, childReport)
			var current database.DeliveryReleaseDeployment
			_ = s.db.First(&current, deployment.ID).Error
			results <- targetResult{success: err == nil, rolledBack: current.Status == StatusRolledBack, err: err}
			return err
		})
		if err != nil {
			_ = s.failDeployment(ctx, deployment.ID, err)
			results <- targetResult{err: err}
			continue
		}
		childIDs = append(childIDs, child.ID)
		_ = s.db.Model(&deployment).Update("task_id", child.ID).Error
	}
	successes, rolledBack := 0, 0
	failures := make([]string, 0)
	for range deployments {
		select {
		case result := <-results:
			if result.success {
				successes++
			} else if result.rolledBack {
				rolledBack++
			} else if result.err != nil {
				failures = append(failures, result.err.Error())
			}
		case <-ctx.Done():
			for _, id := range childIDs {
				s.tasks.Cancel(id)
			}
			return s.failRelease(context.Background(), release.ID, ctx.Err())
		}
	}
	finished := time.Now()
	finalStatus := StatusSucceeded
	if rollback && successes == len(deployments) {
		finalStatus = StatusRolledBack
	} else if rolledBack == len(deployments) {
		finalStatus = StatusRolledBack
	} else if successes == 0 {
		finalStatus = StatusFailed
	} else if successes != len(deployments) {
		finalStatus = StatusPartialFailed
	}
	failureReason := strings.Join(failures, "; ")
	if err := s.db.WithContext(ctx).Model(&release).Updates(map[string]any{"status": finalStatus, "finished_at": finished, "failure_reason": failureReason}).Error; err != nil {
		return err
	}
	if successes == len(deployments) {
		_ = s.db.WithContext(ctx).Model(&project).Updates(map[string]any{"active_release_id": release.ID, "observed_commit": release.CommitSHA}).Error
	}
	report(100, fmt.Sprintf("Deployment complete: %d succeeded, %d rolled back, %d failed", successes, rolledBack, len(deployments)-successes-rolledBack))
	if finalStatus == StatusRolledBack {
		return nil
	}
	if successes != len(deployments) {
		return fmt.Errorf("release completed on %d of %d nodes", successes, len(deployments))
	}
	return nil
}

func (s *Service) deployOneTarget(ctx context.Context, project database.DeliveryProject, release database.DeliveryRelease, deployment database.DeliveryReleaseDeployment, runner compose.Runner, rollback bool, report task.Reporter) error {
	spec, err := releaseExecutionSpec(runtimeName(project), release)
	if err != nil {
		return s.failDeployment(ctx, deployment.ID, err)
	}
	now := time.Now()
	status := StatusPulling
	if rollback {
		status = StatusRollingBack
	}
	_ = s.db.WithContext(ctx).Model(&deployment).Updates(map[string]any{"status": status, "started_at": now, "failure_reason": ""}).Error
	report(15, "Pulling deployment images")
	if err := runner.PullRelease(ctx, spec, &reportWriter{report: report, progress: 30}); err != nil {
		return s.failAndMaybeRollbackTarget(ctx, project, release, deployment, runner, rollback, err, report)
	}
	_ = s.db.WithContext(ctx).Model(&deployment).Update("status", StatusDeploying).Error
	report(55, "Applying Compose release")
	if err := runner.UpRelease(ctx, spec, project.DeploymentTimeout, &reportWriter{report: report, progress: 70}); err != nil {
		return s.failAndMaybeRollbackTarget(ctx, project, release, deployment, runner, rollback, err, report)
	}
	_ = s.db.WithContext(ctx).Model(&deployment).Update("status", StatusVerifying).Error
	health, err := runner.PS(ctx, spec, io.Discard)
	if err != nil {
		return s.failAndMaybeRollbackTarget(ctx, project, release, deployment, runner, rollback, err, report)
	}
	if !runtimeIsHealthy(health) {
		return s.failAndMaybeRollbackTarget(ctx, project, release, deployment, runner, rollback, errors.New("Compose services did not reach a running and healthy state"), report)
	}
	finished := time.Now()
	final := StatusSucceeded
	if rollback {
		final = StatusRolledBack
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&database.DeliveryReleaseDeployment{}).Where("id = ?", deployment.ID).Updates(map[string]any{"status": final, "health_summary": health, "finished_at": finished, "failure_reason": ""}).Error; err != nil {
			return err
		}
		state := database.DeliveryTargetState{ProjectID: project.ID, NodeID: deployment.NodeID, ActiveReleaseID: &release.ID, ObservedCommit: release.CommitSHA, HealthSummary: health}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "project_id"}, {Name: "node_id"}}, DoUpdates: clause.AssignmentColumns([]string{"active_release_id", "observed_commit", "health_summary", "updated_at"})}).Create(&state).Error
	})
}

func (s *Service) failAndMaybeRollbackTarget(ctx context.Context, project database.DeliveryProject, release database.DeliveryRelease, deployment database.DeliveryReleaseDeployment, runner compose.Runner, rollback bool, deployErr error, report task.Reporter) error {
	_ = s.failDeployment(ctx, deployment.ID, deployErr)
	if !project.AutoRollback || rollback || deployment.PreviousReleaseID == nil {
		return deployErr
	}
	var previous database.DeliveryRelease
	if err := s.db.WithContext(ctx).First(&previous, *deployment.PreviousReleaseID).Error; err != nil {
		return deployErr
	}
	spec, err := releaseExecutionSpec(runtimeName(project), previous)
	if err != nil {
		return deployErr
	}
	report(80, "Deployment failed; restoring this node's previous release")
	_ = s.db.WithContext(ctx).Model(&database.DeliveryReleaseDeployment{}).Where("id = ?", deployment.ID).Update("rollback_result", "running").Error
	if err := runner.PullRelease(ctx, spec, io.Discard); err != nil {
		_ = s.db.WithContext(ctx).Model(&database.DeliveryReleaseDeployment{}).Where("id = ?", deployment.ID).Update("rollback_result", "failed").Error
		return deployErr
	}
	if err := runner.UpRelease(ctx, spec, project.DeploymentTimeout, io.Discard); err != nil {
		_ = s.db.WithContext(ctx).Model(&database.DeliveryReleaseDeployment{}).Where("id = ?", deployment.ID).Update("rollback_result", "failed").Error
		return deployErr
	}
	health, err := runner.PS(ctx, spec, io.Discard)
	if err != nil || !runtimeIsHealthy(health) {
		_ = s.db.WithContext(ctx).Model(&database.DeliveryReleaseDeployment{}).Where("id = ?", deployment.ID).Update("rollback_result", "failed").Error
		return deployErr
	}
	finished := time.Now()
	_ = s.db.WithContext(ctx).Model(&database.DeliveryReleaseDeployment{}).Where("id = ?", deployment.ID).Updates(map[string]any{"status": StatusRolledBack, "rollback_result": "succeeded", "health_summary": health, "finished_at": finished, "failure_reason": deployErr.Error()}).Error
	return deployErr
}

func (s *Service) failDeployment(ctx context.Context, id uint, err error) error {
	now := time.Now()
	_ = s.db.WithContext(ctx).Model(&database.DeliveryReleaseDeployment{}).Where("id = ?", id).Updates(map[string]any{"status": StatusFailed, "failure_reason": err.Error(), "finished_at": now}).Error
	return err
}

func (s *Service) snapshotReleaseTargets(ctx context.Context, projectID, releaseID uint) error {
	if s.targets == nil {
		return nil
	}
	var count int64
	if err := s.db.WithContext(ctx).Model(&database.DeliveryReleaseDeployment{}).Where("release_id = ?", releaseID).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	ids, err := s.projectNodeIDs(ctx, projectID)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, id := range ids {
			var row database.Node
			if err := tx.Where("id = ?", id).First(&row).Error; err != nil {
				return err
			}
			deployment := database.DeliveryReleaseDeployment{ReleaseID: releaseID, NodeID: id, NodeName: row.Name, Status: StatusAwaitingApproval}
			if err := tx.Create(&deployment).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Service) RemoveProject(ctx context.Context, name string, force, preserveVolumes bool) error {
	project, err := s.project(ctx, name)
	if err != nil {
		return err
	}
	if !s.reserve(project.ID) {
		return errors.New("another delivery operation is already queued for this project")
	}
	defer s.releaseReservation(project.ID)
	lock := s.projectLock(project.ID)
	lock.Lock()
	defer lock.Unlock()
	if force && project.ActiveReleaseID != nil {
		var release database.DeliveryRelease
		if err := s.db.WithContext(ctx).First(&release, *project.ActiveReleaseID).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		} else if err == nil {
			spec, specErr := releaseExecutionSpec(runtimeName(project), release)
			if specErr != nil {
				return specErr
			}
			if err := s.compose.ForceDownRelease(ctx, spec, preserveVolumes, io.Discard); err != nil {
				return fmt.Errorf("force down delivery release: %w", err)
			}
		}
	}
	if err := s.git.Cleanup(project.ID); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var releaseIDs []uint
		if err := tx.Model(&database.DeliveryRelease{}).Where("project_id = ?", project.ID).Pluck("id", &releaseIDs).Error; err != nil {
			return err
		}
		if len(releaseIDs) > 0 {
			if err := tx.Where("release_id IN ?", releaseIDs).Delete(&database.DeliveryReleaseDeployment{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("project_id = ?", project.ID).Delete(&database.DeliveryRelease{}).Error; err != nil {
			return err
		}
		if err := tx.Where("project_id = ?", project.ID).Delete(&database.DeliveryProjectGitCredential{}).Error; err != nil {
			return err
		}
		if err := tx.Where("project_id = ?", project.ID).Delete(&database.DeliveryProjectNode{}).Error; err != nil {
			return err
		}
		if err := tx.Where("project_id = ?", project.ID).Delete(&database.DeliveryProjectRegistryCredential{}).Error; err != nil {
			return err
		}
		if err := tx.Where("project_id = ?", project.ID).Delete(&database.DeliveryTargetState{}).Error; err != nil {
			return err
		}
		return tx.Delete(&project).Error
	})
}

func (s *Service) ListReleases(ctx context.Context, name string) ([]database.DeliveryRelease, error) {
	project, err := s.project(ctx, name)
	if err != nil {
		return nil, err
	}
	var releases []database.DeliveryRelease
	if err := s.db.WithContext(ctx).Where("project_id = ?", project.ID).Order("id DESC").Limit(100).Find(&releases).Error; err != nil {
		return nil, err
	}
	for index := range releases {
		_ = s.db.WithContext(ctx).Where("release_id = ?", releases[index].ID).Order("node_id ASC").Find(&releases[index].Deployments).Error
	}
	return releases, nil
}

func (s *Service) GetRelease(ctx context.Context, name string, id uint) (database.DeliveryRelease, error) {
	_, release, err := s.projectRelease(ctx, name, id)
	if err == nil {
		err = s.db.WithContext(ctx).Where("release_id = ?", release.ID).Order("node_id ASC").Find(&release.Deployments).Error
	}
	return release, err
}

func (s *Service) Drift(ctx context.Context, name string) (Drift, error) {
	project, err := s.project(ctx, name)
	if err != nil {
		return Drift{}, err
	}
	drift := Drift{DesiredCommit: project.DesiredCommit, ObservedCommit: project.ObservedCommit, ActiveReleaseID: project.ActiveReleaseID}
	if project.ActiveReleaseID != nil {
		var release database.DeliveryRelease
		if err := s.db.WithContext(ctx).First(&release, *project.ActiveReleaseID).Error; err == nil {
			drift.ActiveCommit = release.CommitSHA
			if spec, specErr := releaseExecutionSpec(runtimeName(project), release); specErr == nil {
				if runtime, runtimeErr := s.compose.PS(ctx, spec, io.Discard); runtimeErr == nil {
					drift.RuntimeHealthy = runtimeIsHealthy(runtime)
				} else if drift.Reason == "" {
					drift.Reason = "unable to read active release runtime state"
				}
			}
		}
	}
	switch {
	case drift.DesiredCommit == "":
		drift.Reason = "repository has not been synchronized"
	case drift.ActiveCommit == "":
		drift.Reason = "no release is active"
	case drift.DesiredCommit != drift.ActiveCommit:
		drift.Reason = "active release differs from desired Git commit"
	case !drift.RuntimeHealthy:
		drift.Reason = "active release has missing or unhealthy containers"
	}
	drift.Drifted = drift.Reason != ""
	return drift, nil
}

func runtimeIsHealthy(value string) bool {
	type service struct {
		State  string `json:"State"`
		Health string `json:"Health"`
	}
	rows := []service{}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	if strings.HasPrefix(trimmed, "[") {
		if json.Unmarshal([]byte(trimmed), &rows) != nil {
			return false
		}
	} else {
		for _, line := range strings.Split(trimmed, "\n") {
			var row service
			if json.Unmarshal([]byte(line), &row) != nil {
				return false
			}
			rows = append(rows, row)
		}
	}
	if len(rows) == 0 {
		return false
	}
	for _, row := range rows {
		if !strings.EqualFold(row.State, "running") || (row.Health != "" && !strings.EqualFold(row.Health, "healthy")) {
			return false
		}
	}
	return true
}

func (s *Service) project(ctx context.Context, name string) (database.DeliveryProject, error) {
	var project database.DeliveryProject
	return project, s.db.WithContext(ctx).Where("name = ?", name).First(&project).Error
}

func (s *Service) projectNodeIDs(ctx context.Context, projectID uint) ([]string, error) {
	var rows []database.DeliveryProjectNode
	if err := s.db.WithContext(ctx).Where("project_id = ?", projectID).Order("node_id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.NodeID)
	}
	if len(ids) == 0 {
		ids = []string{"local"}
	}
	return ids, nil
}

func (s *Service) runnerForProject(ctx context.Context, projectID uint) (compose.Runner, error) {
	if s.targets == nil {
		return s.compose, nil
	}
	targeted, ok := s.compose.(TargetedRunner)
	if !ok {
		return nil, errors.New("Compose runner does not support node targets")
	}
	ids, err := s.projectNodeIDs(ctx, projectID)
	if err != nil {
		return nil, err
	}
	target, err := s.composeTargetForProject(ctx, projectID, ids[0])
	if err != nil {
		return nil, err
	}
	return targeted.Targeted(target), nil
}

func (s *Service) projectRegistryCredentialIDs(ctx context.Context, projectID uint) ([]uint, error) {
	var rows []database.DeliveryProjectRegistryCredential
	if err := s.db.WithContext(ctx).Where("project_id = ?", projectID).Order("credential_id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	ids := make([]uint, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.CredentialID)
	}
	return ids, nil
}

func (s *Service) replaceRegistryCredentials(ctx context.Context, db *gorm.DB, projectID uint, ids []uint) error {
	if err := db.WithContext(ctx).Where("project_id = ?", projectID).Delete(&database.DeliveryProjectRegistryCredential{}).Error; err != nil {
		return err
	}
	seen := map[uint]bool{}
	for _, id := range ids {
		if id == 0 || seen[id] {
			continue
		}
		if err := db.WithContext(ctx).Create(&database.DeliveryProjectRegistryCredential{ProjectID: projectID, CredentialID: id}).Error; err != nil {
			return err
		}
		seen[id] = true
	}
	return nil
}

func (s *Service) composeTargetForProject(ctx context.Context, projectID uint, nodeID string) (compose.Target, error) {
	target, err := s.targets.ResolveComposeTarget(ctx, nodeID)
	if err != nil {
		return target, err
	}
	ids, err := s.projectRegistryCredentialIDs(ctx, projectID)
	if err != nil || len(ids) == 0 {
		return target, err
	}
	if s.registries == nil {
		return target, errors.New("registry credential service is unavailable")
	}
	auths := map[string]map[string]string{}
	for _, id := range ids {
		if err := s.registries.AuthorizedForNode(ctx, id, nodeID); err != nil {
			return target, err
		}
		material, err := s.registries.Material(ctx, id)
		if err != nil {
			return target, err
		}
		entry := map[string]string{}
		if material.AuthType == credentialrepo.RegistryToken {
			entry["identitytoken"] = material.Secret
		} else {
			entry["auth"] = base64.StdEncoding.EncodeToString([]byte(material.Username + ":" + material.Secret))
		}
		auths[material.ServerAddress] = entry
	}
	encoded, err := json.Marshal(map[string]any{"auths": auths})
	if err != nil {
		return target, err
	}
	target.DockerConfig = string(encoded)
	return target, nil
}

func (s *Service) validateDeploymentTargets(ctx context.Context, projectID uint, worktree, rendered string) error {
	if s.targets == nil {
		return validateDeploymentPolicy(worktree, rendered)
	}
	ids, err := s.projectNodeIDs(ctx, projectID)
	if err != nil {
		return err
	}
	validatedLocal := false
	for _, id := range ids {
		var row database.Node
		if err := s.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
			return err
		}
		if row.ConnectionType == "unix" {
			if !validatedLocal {
				if err := validateDeploymentPolicy(worktree, rendered); err != nil {
					return err
				}
				validatedLocal = true
			}
			continue
		}
		if err := validateRemoteDeploymentPolicy(rendered); err != nil {
			return fmt.Errorf("node %s: %w", row.Name, err)
		}
	}
	return nil
}

func (s *Service) validateNodeIDs(ctx context.Context, nodeIDs []string) error {
	if len(nodeIDs) == 0 {
		return errors.New("at least one deployment node is required")
	}
	seen := map[string]bool{}
	for _, nodeID := range nodeIDs {
		if nodeID == "" || seen[nodeID] {
			return errors.New("deployment node IDs must be non-empty and unique")
		}
		var count int64
		if err := s.db.WithContext(ctx).Model(&database.Node{}).Where("id = ? AND enabled = ?", nodeID, true).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			// Unit-test databases created before node bootstrapping continue to use
			// the historical implicit local target.
			var all int64
			_ = s.db.Model(&database.Node{}).Count(&all).Error
			if !(all == 0 && nodeID == "local") {
				return fmt.Errorf("enabled Docker node %s not found", nodeID)
			}
		}
		seen[nodeID] = true
	}
	return nil
}

func (s *Service) replaceTargets(ctx context.Context, db *gorm.DB, projectID uint, nodeIDs []string) error {
	if err := db.WithContext(ctx).Where("project_id = ?", projectID).Delete(&database.DeliveryProjectNode{}).Error; err != nil {
		return err
	}
	for _, nodeID := range nodeIDs {
		if err := db.WithContext(ctx).Create(&database.DeliveryProjectNode{ProjectID: projectID, NodeID: nodeID}).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) projectRelease(ctx context.Context, name string, id uint) (database.DeliveryProject, database.DeliveryRelease, error) {
	project, err := s.project(ctx, name)
	if err != nil {
		return project, database.DeliveryRelease{}, err
	}
	var release database.DeliveryRelease
	err = s.db.WithContext(ctx).Where("id = ? AND project_id = ?", id, project.ID).First(&release).Error
	return project, release, err
}

func (s *Service) projectLock(id uint) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locks[id] == nil {
		s.locks[id] = &sync.Mutex{}
	}
	return s.locks[id]
}

func (s *Service) reserve(id uint) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.queued[id] {
		return false
	}
	s.queued[id] = true
	return true
}

func (s *Service) releaseReservation(id uint) { s.mu.Lock(); delete(s.queued, id); s.mu.Unlock() }

func (s *Service) failRelease(ctx context.Context, id uint, failure error) error {
	finished := time.Now()
	_ = s.db.WithContext(ctx).Model(&database.DeliveryRelease{}).Where("id = ?", id).Updates(map[string]any{"status": StatusFailed, "failure_reason": failure.Error(), "finished_at": finished}).Error
	return failure
}

func (s *Service) recordFinal(actor Actor, action, name, taskID string, releaseID *uint, result error) {
	if s.audit == nil {
		return
	}
	value := "success"
	if result != nil {
		value = "failed"
	}
	_ = s.audit.RecordLinked(context.Background(), actor.UserID, action, "delivery_project", name, actor.IP, value, taskID, releaseID)
}

func repositoryFromProject(project database.DeliveryProject) (gitrepo.Repository, error) {
	files := []string{}
	if err := json.Unmarshal([]byte(project.ComposeFilesJSON), &files); err != nil {
		return gitrepo.Repository{}, err
	}
	repository := gitrepo.Repository{CloneURL: project.GitCloneURL, RefType: project.GitRefType, Ref: project.GitRef, CredentialID: project.GitCredentialID, ComposeFiles: files, EnvironmentFile: project.EnvironmentFile}
	return repository, gitrepo.ValidateRepository(repository)
}

func executionSpec(project database.DeliveryProject, repository gitrepo.Repository, worktree string) (compose.ExecutionSpec, error) {
	files, _ := json.Marshal(repository.ComposeFiles)
	release := database.DeliveryRelease{WorktreePath: worktree, ComposeFilesJSON: string(files), EnvironmentFile: repository.EnvironmentFile}
	return releaseExecutionSpec(runtimeName(project), release)
}

func deliveryRuntimeName(projectID uint) string { return fmt.Sprintf("suma-cd-%d", projectID) }

func runtimeName(project database.DeliveryProject) string {
	if project.DeploymentName != "" {
		return project.DeploymentName
	}
	return deliveryRuntimeName(project.ID)
}

func releaseExecutionSpec(projectName string, release database.DeliveryRelease) (compose.ExecutionSpec, error) {
	files := []string{}
	if err := json.Unmarshal([]byte(release.ComposeFilesJSON), &files); err != nil {
		return compose.ExecutionSpec{}, fmt.Errorf("decode release Compose files: %w", err)
	}
	if len(files) == 0 {
		return compose.ExecutionSpec{}, errors.New("release has no Compose files")
	}
	worktree := release.WorktreePath
	realRoot, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		return compose.ExecutionSpec{}, fmt.Errorf("resolve Git worktree: %w", err)
	}
	environmentFile := release.EnvironmentFile
	spec := compose.ExecutionSpec{ProjectName: projectName}
	for _, value := range files {
		path, err := safeFile(realRoot, value)
		if err != nil {
			return compose.ExecutionSpec{}, err
		}
		spec.Files = append(spec.Files, path)
	}
	spec.ProjectDir = filepath.Dir(spec.Files[0])
	if environmentFile != "" {
		path, err := safeFile(realRoot, environmentFile)
		if err != nil {
			return compose.ExecutionSpec{}, err
		}
		spec.EnvFiles = append(spec.EnvFiles, path)
	}
	return spec, nil
}

func safeFile(root, value string) (string, error) {
	path, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(value)))
	if err != nil {
		return "", fmt.Errorf("resolve deployment file %q: %w", value, err)
	}
	if err := below(root, path); err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("deployment file %q is not a regular file", value)
	}
	if info.Size() > maxDeploymentFileSize {
		return "", fmt.Errorf("deployment file %q exceeds the %d-byte limit", value, maxDeploymentFileSize)
	}
	return path, nil
}

func below(root, value string) error {
	relative, err := filepath.Rel(root, value)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return errors.New("deployment path escapes the Git worktree")
	}
	return nil
}

func imageReferences(rendered string) []string {
	var document struct {
		Services map[string]struct {
			Image string `json:"image"`
		} `json:"services"`
	}
	if json.Unmarshal([]byte(rendered), &document) != nil {
		return nil
	}
	set := map[string]struct{}{}
	for _, service := range document.Services {
		if service.Image != "" {
			set[service.Image] = struct{}{}
		}
	}
	images := make([]string, 0, len(set))
	for value := range set {
		images = append(images, value)
	}
	sort.Strings(images)
	return images
}

type reportWriter struct {
	report   task.Reporter
	progress int
	buffer   strings.Builder
}

func randomHex(bytesCount int) (string, error) {
	value := make([]byte, bytesCount)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func (w *reportWriter) Write(value []byte) (int, error) {
	w.buffer.Write(value)
	for {
		text := w.buffer.String()
		index := strings.IndexByte(text, '\n')
		if index < 0 {
			break
		}
		line := strings.TrimSpace(text[:index])
		w.buffer.Reset()
		w.buffer.WriteString(text[index+1:])
		if line != "" {
			w.report(w.progress, line)
		}
	}
	return len(value), nil
}
