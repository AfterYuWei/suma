package cd

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/compose"
	credentialrepo "github.com/suma/suma/server/internal/credential"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/deployment"
	"github.com/suma/suma/server/internal/event"
	gitrepo "github.com/suma/suma/server/internal/git"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/task"
	"github.com/suma/suma/server/internal/taskcenter"
	"gorm.io/gorm"
)

const (
	ModeObserve = "observe"
	ModeManual  = "manual"
	ModeAuto    = "auto"

	StatusValidating       = "validating"
	StatusAwaitingApproval = "ready"
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
	StatusInterrupted      = "interrupted"

	AttemptDeploy         = "deploy"
	AttemptRetry          = "retry"
	AttemptManualRollback = "manual_rollback"
	AttemptAutoRollback   = "auto_rollback"

	DriftHealthy  = "healthy"
	DriftDegraded = "degraded"
	DriftUnknown  = "unknown"
)

type syncResult struct {
	Project    string `json:"project"`
	Operation  string `json:"operation"`
	Trigger    string `json:"trigger"`
	ReleaseID  *uint  `json:"release_id,omitempty"`
	RecordID   *uint  `json:"record_id,omitempty"`
	Commit     string `json:"commit,omitempty"`
	ConfigHash string `json:"config_hash,omitempty"`
}

type ConfigureInput struct {
	ExpectedAutomationVersion *uint64               `json:"expected_automation_version,omitempty"`
	Environment               []EnvironmentVariable `json:"environment"`
	AutomationEnabled         *bool                 `json:"automation_enabled,omitempty"`
	Repository                gitrepo.Repository    `json:"repository"`
	ReconcileMode             string                `json:"reconcile_mode"`
	SyncIntervalSeconds       int                   `json:"sync_interval_seconds"`
	AutoRollback              bool                  `json:"auto_rollback"`
	DeploymentTimeout         int                   `json:"deployment_timeout"`
	WebhookEnabled            bool                  `json:"webhook_enabled"`
	WebhookSecret             string                `json:"webhook_secret"`
	NodeIDs                   []string              `json:"node_ids,omitempty"`
	RegistryCredentialIDs     []uint                `json:"registry_credential_ids,omitempty"`
}

type Configuration struct {
	AutomationVersion     uint64                `json:"automation_version,omitempty"`
	Environment           []EnvironmentVariable `json:"environment"`
	AutomationEnabled     bool                  `json:"automation_enabled"`
	Configured            bool                  `json:"configured"`
	Repository            gitrepo.Repository    `json:"repository"`
	ReconcileMode         string                `json:"reconcile_mode"`
	SyncIntervalSeconds   int                   `json:"sync_interval_seconds"`
	DesiredCommit         string                `json:"desired_commit"`
	ObservedCommit        string                `json:"observed_commit"`
	ActiveReleaseID       *uint                 `json:"active_release_id,omitempty"`
	AutoRollback          bool                  `json:"auto_rollback"`
	DeploymentTimeout     int                   `json:"deployment_timeout"`
	WebhookEnabled        bool                  `json:"webhook_enabled"`
	WebhookID             string                `json:"webhook_id,omitempty"`
	WebhookSecret         string                `json:"webhook_secret,omitempty"`
	NodeIDs               []string              `json:"node_ids"`
	RegistryCredentialIDs []uint                `json:"registry_credential_ids"`
}

type Project struct {
	AutomationVersion uint64    `json:"automation_version,omitempty"`
	AutomationEnabled bool      `json:"automation_enabled"`
	ID                uint      `json:"id"`
	Name              string    `json:"name"`
	DeploymentName    string    `json:"deployment_name"`
	Configured        bool      `json:"configured"`
	RepositoryURL     string    `json:"repository_url,omitempty"`
	GitRef            string    `json:"git_ref,omitempty"`
	DesiredCommit     string    `json:"desired_commit,omitempty"`
	ObservedCommit    string    `json:"observed_commit,omitempty"`
	ActiveCommit      string    `json:"active_commit,omitempty"`
	ActiveReleaseID   *uint     `json:"active_release_id,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	NodeIDs           []string  `json:"node_ids"`
}

type Drift struct {
	Drifted         bool        `json:"drifted"`
	Status          string      `json:"status"`
	DesiredCommit   string      `json:"desired_commit"`
	ObservedCommit  string      `json:"observed_commit"`
	ActiveCommit    string      `json:"active_commit"`
	ActiveReleaseID *uint       `json:"active_release_id,omitempty"`
	Reason          string      `json:"reason,omitempty"`
	ReasonCode      string      `json:"reason_code,omitempty"`
	RuntimeHealthy  bool        `json:"runtime_healthy"`
	CheckedAt       time.Time   `json:"checked_at"`
	Nodes           []NodeDrift `json:"nodes"`
}

type NodeDrift struct {
	NodeID          string    `json:"node_id"`
	NodeName        string    `json:"node_name"`
	Status          string    `json:"status"`
	Drifted         bool      `json:"drifted"`
	ActiveReleaseID *uint     `json:"active_release_id,omitempty"`
	ActiveCommit    string    `json:"active_commit,omitempty"`
	ReasonCode      string    `json:"reason_code,omitempty"`
	Reason          string    `json:"reason,omitempty"`
	HealthSummary   string    `json:"health_summary,omitempty"`
	CheckedAt       time.Time `json:"checked_at"`
}

type driftCacheEntry struct {
	value     Drift
	expiresAt time.Time
}

type Actor struct {
	UserID       *uint
	Name         string
	IP           string
	TriggerRunID uint
	SourceID     string
	CheckOnly    bool
}

type Service struct {
	automation  *taskcenter.Service
	emit        event.Sink
	db          *gorm.DB
	git         gitrepo.Client
	credentials *gitrepo.CredentialService
	compose     compose.Runner
	tasks       *task.Service
	audit       *audit.Service
	secrets     *secret.Store
	mu          sync.Mutex
	locks       map[uint]*sync.Mutex
	queued      map[uint]bool
	targets     TargetResolver
	registries  *credentialrepo.RegistryService
	driftMu     sync.Mutex
	driftCache  map[uint]driftCacheEntry
	driftFlight map[uint]chan struct{}
}

type TargetResolver interface {
	ResolveComposeTarget(context.Context, string) (compose.Target, error)
}
type TargetedRunner interface {
	Targeted(compose.Target) compose.Runner
}

func NewService(db *gorm.DB, git gitrepo.Client, credentials *gitrepo.CredentialService, runner compose.Runner, tasks *task.Service, audits *audit.Service, secrets *secret.Store) *Service {
	return &Service{db: db, git: git, credentials: credentials, compose: runner, tasks: tasks, audit: audits, secrets: secrets, locks: map[uint]*sync.Mutex{}, queued: map[uint]bool{}, driftCache: map[uint]driftCacheEntry{}, driftFlight: map[uint]chan struct{}{}}
}
func (s *Service) SetTargetResolver(resolver TargetResolver) { s.targets = resolver }
func (s *Service) SetRegistryCredentials(service *credentialrepo.RegistryService) {
	s.registries = service
}

var validProjectName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,127}$`)

func (s *Service) CreateProject(ctx context.Context, name string) (Project, error) {
	return s.CreateProjectOnNodes(ctx, name, []string{"local"})
}

// CreateConfiguredProject saves the project, targets and encrypted credentials
// together. No scheduler can observe an incompletely configured new project.
func (s *Service) CreateConfiguredProject(ctx context.Context, name string, input ConfigureInput) (Project, Configuration, error) {
	var project Project
	var configuration Configuration
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		credentials := gitrepo.NewCredentialService(tx, s.secrets)
		service := NewService(tx, s.git, credentials, s.compose, s.tasks, s.audit, s.secrets)
		service.SetTaskCenter(s.automation)
		if s.registries != nil {
			service.SetRegistryCredentials(credentialrepo.NewRegistryService(tx, s.secrets))
		}
		created, err := service.CreateProjectOnNodes(ctx, name, input.NodeIDs)
		if err != nil {
			return err
		}
		configuration, err = service.Configure(ctx, created.Name, input)
		if err != nil {
			return err
		}
		project, err = service.GetProject(ctx, created.Name)
		return err
	})
	if err != nil {
		return Project{}, Configuration{}, err
	}
	return project, configuration, nil
}

func (s *Service) CreateProjectOnNodes(ctx context.Context, name string, nodeIDs []string) (Project, error) {
	if !validProjectName.MatchString(name) {
		return Project{}, errors.New("project name must use 1-128 lowercase letters, numbers, hyphens or underscores, starting with a letter or number")
	}
	row := database.DeliveryProject{NodeID: "local", Name: name, DeploymentName: name, ReconcileMode: ModeManual, SyncIntervalSeconds: 300, DeploymentTimeout: 120}
	if err := s.validateNodeIDs(ctx, nodeIDs); err != nil {
		return Project{}, err
	}
	var reserved int64
	if err := s.db.WithContext(ctx).Model(&database.DeliveryProject{}).
		Where("COALESCE(NULLIF(deployment_name, ''), 'suma-cd-' || id::text) = ? AND name <> ?", name, name).Count(&reserved).Error; err != nil {
		return Project{}, err
	}
	if reserved > 0 {
		return Project{}, fmt.Errorf("Compose project name %q is already reserved by an existing delivery project", name)
	}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
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
	versions := map[string]uint64{}
	if s.automation != nil {
		var rules []database.TaskCenterSchedule
		if err := s.db.WithContext(ctx).Select("subject", "version").Where("kind = 'cd.sync' AND node_id = '' AND archived_at IS NULL").Find(&rules).Error; err != nil {
			return nil, err
		}
		for _, rule := range rules {
			versions[rule.Subject] = rule.Version
		}
	}
	for _, row := range rows {
		value := projectSummary(row)
		value.AutomationVersion = versions[row.Name]
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
	if s.automation != nil {
		var rule database.TaskCenterSchedule
		err := s.db.WithContext(ctx).Select("version").Where("kind = 'cd.sync' AND node_id = '' AND subject = ? AND archived_at IS NULL", name).First(&rule).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return value, err
		}
		value.AutomationVersion = rule.Version
	}
	value.NodeIDs, err = s.projectNodeIDs(ctx, row.ID)
	return value, err
}

func projectSummary(row database.DeliveryProject) Project {
	return Project{ID: row.ID, Name: row.Name, DeploymentName: runtimeName(row), Configured: row.GitCloneURL != "", AutomationEnabled: row.AutomationEnabled, RepositoryURL: row.GitCloneURL, GitRef: row.GitRef, DesiredCommit: row.DesiredCommit, ObservedCommit: row.ObservedCommit, ActiveCommit: row.ActiveCommit, ActiveReleaseID: row.ActiveReleaseID, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

// SetAutomation changes only automatic triggers, preserving policy, credentials
// and release history. Operations that already started may finish normally.
func (s *Service) SetAutomation(ctx context.Context, name string, enabled bool, expected ...uint64) (Project, error) {
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if s.automation != nil {
			if err := s.automation.LockOwned(tx, "cd.sync", "", name, expected...); err != nil {
				return err
			}
			if len(expected) == 0 {
				var rule database.TaskCenterSchedule
				if err := tx.Where("kind = 'cd.sync' AND node_id = '' AND subject = ? AND archived_at IS NULL", name).First(&rule).Error; err == nil && rule.Enabled != enabled {
					return taskcenter.ErrVersionConflict
				} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
			}
		}
		result := tx.Model(&database.DeliveryProject{}).Where("name = ?", name).Update("automation_enabled", enabled)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		var row database.DeliveryProject
		if err := tx.Where("name = ?", name).First(&row).Error; err != nil {
			return err
		}
		return s.syncAutomation(tx, row)
	}); err != nil {
		return Project{}, err
	}
	return s.GetProject(ctx, name)
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
	environment, err := s.configureEnvironment(current.EnvironmentCiphertext, input.Environment)
	if err != nil {
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
		if err := gitrepo.ValidateCredentialTransport(input.Repository.CloneURL, centerCredential.AuthType); err != nil {
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
			if err := gitrepo.ValidateCredentialTransport(input.Repository.CloneURL, authentication.Credential.AuthType); err != nil {
				return Configuration{}, err
			}
		} else {
			if err := s.db.WithContext(ctx).Where("project_id = ?", current.ID).First(&projectCredential).Error; err != nil {
				return Configuration{}, errors.New("project credential is required")
			}
			if err := gitrepo.ValidateCredentialTransport(input.Repository.CloneURL, projectCredential.AuthType); err != nil {
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
		"environment_ciphertext": environment,
		"git_clone_url":          input.Repository.CloneURL,
		"git_ref_type":           input.Repository.RefType, "git_ref": input.Repository.Ref,
		"compose_files_json": string(files),
		"environment_file":   input.Repository.EnvironmentFile, "reconcile_mode": input.ReconcileMode,
		"sync_interval_seconds": input.SyncIntervalSeconds, "auto_rollback": input.AutoRollback,
		"deployment_timeout": input.DeploymentTimeout,
		"webhook_enabled":    input.WebhookEnabled, "webhook_id": webhookID,
	}
	if input.AutomationEnabled != nil {
		updates["automation_enabled"] = *input.AutomationEnabled
	}
	if !bytes.Equal(current.EnvironmentCiphertext, environment) || current.GitCloneURL != input.Repository.CloneURL || current.GitRefType != input.Repository.RefType || current.GitRef != input.Repository.Ref || current.ComposeFilesJSON != string(files) || current.EnvironmentFile != input.Repository.EnvironmentFile {
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
		if s.automation != nil {
			expected := []uint64{}
			if input.ExpectedAutomationVersion != nil {
				expected = append(expected, *input.ExpectedAutomationVersion)
			}
			if err := s.automation.LockOwned(tx, "cd.sync", "", name, expected...); err != nil {
				return err
			}
			if input.ExpectedAutomationVersion == nil {
				var rule database.TaskCenterSchedule
				err := tx.Where("kind = 'cd.sync' AND node_id = '' AND subject = ? AND archived_at IS NULL", name).First(&rule).Error
				if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
				if err == nil && ((input.AutomationEnabled != nil && *input.AutomationEnabled != rule.Enabled) || input.SyncIntervalSeconds != rule.IntervalSeconds || input.WebhookEnabled != rule.WebhookEnabled) {
					return taskcenter.ErrVersionConflict
				}
			}
		}
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
		var saved database.DeliveryProject
		if err := tx.First(&saved, current.ID).Error; err != nil {
			return err
		}
		return s.syncAutomation(tx, saved)
	}); err != nil {
		return Configuration{}, err
	}
	_ = s.recomputeProjectActiveRelease(ctx, current.ID)
	s.invalidateDrift(current.ID)
	configuration, err := s.GetConfiguration(ctx, name)
	configuration.WebhookSecret = generatedSecret
	return configuration, err
}

func (s *Service) GetConfiguration(ctx context.Context, name string) (Configuration, error) {
	project, err := s.project(ctx, name)
	if err != nil {
		return Configuration{}, err
	}
	var automationVersion uint64
	if s.automation != nil {
		var rule database.TaskCenterSchedule
		err := s.db.WithContext(ctx).Select("version").Where("kind = 'cd.sync' AND node_id = '' AND subject = ? AND archived_at IS NULL", name).First(&rule).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return Configuration{}, err
		}
		automationVersion = rule.Version
	}
	if project.GitCloneURL == "" {
		nodeIDs, _ := s.projectNodeIDs(ctx, project.ID)
		return Configuration{
			AutomationVersion:   automationVersion,
			AutomationEnabled:   project.AutomationEnabled,
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
	environment, err := s.environmentSummary(project.EnvironmentCiphertext)
	if err != nil {
		return Configuration{}, err
	}
	return Configuration{
		AutomationVersion: automationVersion,
		Environment:       environment,
		AutomationEnabled: project.AutomationEnabled,
		Configured:        true,
		Repository:        gitrepo.Repository{CloneURL: project.GitCloneURL, RefType: project.GitRefType, Ref: project.GitRef, CredentialID: project.GitCredentialID, Authentication: authentication, ComposeFiles: files, EnvironmentFile: project.EnvironmentFile},
		ReconcileMode:     project.ReconcileMode, SyncIntervalSeconds: project.SyncIntervalSeconds,
		DesiredCommit: project.DesiredCommit, ObservedCommit: project.ObservedCommit, ActiveReleaseID: project.ActiveReleaseID,
		AutoRollback: project.AutoRollback, DeploymentTimeout: project.DeploymentTimeout,
		WebhookEnabled: project.WebhookEnabled, WebhookID: project.WebhookID,
		NodeIDs: nodeIDs, RegistryCredentialIDs: registryIDs,
	}, nil
}

func (s *Service) Sync(ctx context.Context, name, trigger string, actor Actor) (database.Task, error) {
	if s.automation != nil && actor.TriggerRunID == 0 {
		reason := taskcenter.ReasonManual
		if trigger == "webhook" {
			reason = taskcenter.ReasonWebhook
		} else if trigger == "poll" {
			reason = taskcenter.ReasonScheduled
		}
		run, err := s.automation.DispatchSubject(ctx, "cd.sync", "", name, taskcenter.DispatchRequest{Reason: reason, SourceID: actor.SourceID, Action: "check", Actor: taskcenter.Actor{ID: actor.UserID, Name: actor.Name, IP: actor.IP}})
		if err != nil {
			if errors.Is(err, taskcenter.ErrPaused) {
				return database.Task{}, ErrAutomationPaused
			}
			return database.Task{}, err
		}
		return s.tasks.Get(ctx, run.TaskID)
	}
	project, err := s.project(ctx, name)
	if err != nil {
		return database.Task{}, err
	}
	if project.GitCloneURL == "" {
		return database.Task{}, errors.New("delivery project is not configured")
	}
	if automaticTrigger(trigger) && !project.AutomationEnabled {
		return database.Task{}, ErrAutomationPaused
	}
	if !s.reserve(project.ID) {
		return database.Task{}, errors.New("a reconciliation is already queued for this project")
	}
	row, err := s.tasks.StartDefinition(context.Background(), task.Definition{
		Type: "cd.sync", Name: "Sync " + name,
		Params:       map[string]any{"project": name, "trigger": trigger},
		TriggerRunID: actor.TriggerRunID,
	}, func(ctx context.Context, taskID string, report task.Reporter) (any, error) {
		defer s.releaseReservation(project.ID)
		lock := s.projectLock(project.ID)
		lock.Lock()
		defer lock.Unlock()
		if s.automation != nil {
			if err := s.automation.ValidateRun(ctx, actor.TriggerRunID); err != nil {
				return nil, err
			}
		}
		result, err := s.syncLocked(ctx, project.ID, taskID, trigger, actor, report)
		result.Project = name
		s.recordFinal(actor, "cd.sync", name, taskID, nil, err)
		return result, err
	})
	if err != nil {
		s.releaseReservation(project.ID)
	}
	return row, err
}

func (s *Service) syncLocked(ctx context.Context, projectID uint, taskID, trigger string, actor Actor, report task.Reporter) (syncResult, error) {
	result := syncResult{Operation: "check", Trigger: trigger}
	var project database.DeliveryProject
	if err := s.db.WithContext(ctx).First(&project, projectID).Error; err != nil {
		return result, err
	}
	// Recheck after acquiring the project lock: a queued trigger may have been paused.
	if automaticTrigger(trigger) && !project.AutomationEnabled {
		return result, ErrAutomationPaused
	}
	autoDeploy := project.ReconcileMode == ModeAuto && project.AutomationEnabled && !actor.CheckOnly
	repository, err := repositoryFromProject(project)
	if err != nil {
		return result, err
	}
	credential := gitrepo.CredentialMaterial{AuthType: gitrepo.AuthNone}
	if project.GitCredentialID != nil {
		credential, err = s.credentials.Material(ctx, *project.GitCredentialID)
		if err != nil {
			return result, fmt.Errorf("load Git credential: %w", err)
		}
	} else if credential, err = s.credentials.ProjectMaterial(ctx, project.ID); errors.Is(err, gorm.ErrRecordNotFound) {
		credential, err = gitrepo.CredentialMaterial{AuthType: gitrepo.AuthNone}, nil
	} else if err != nil {
		return result, fmt.Errorf("load project Git credential: %w", err)
	}
	report(5, "Fetching Git repository")
	revision, err := s.git.Sync(ctx, gitrepo.SyncRequest{ProjectID: project.ID, Repository: repository, Credential: credential}, &reportWriter{report: report, progress: 15})
	if err != nil {
		return result, err
	}
	if err := s.git.Verify(ctx, revision.WorktreePath, revision.CommitSHA); err != nil {
		return result, err
	}
	if err := s.db.WithContext(ctx).Model(&project).Update("desired_commit", revision.CommitSHA).Error; err != nil {
		return result, err
	}
	now := time.Now()
	if err := s.db.WithContext(ctx).Model(&project).Update("last_sync_at", now).Error; err != nil {
		return result, err
	}
	spec, err := executionSpec(project, repository, revision.WorktreePath)
	if err != nil {
		return result, err
	}
	spec.Environment, err = s.decryptEnvironment(project.EnvironmentCiphertext)
	if err != nil {
		return result, err
	}
	if err := validateComposeSources(spec, revision.WorktreePath); err != nil {
		return result, fmt.Errorf("deployment source policy rejected the release: %w", err)
	}
	report(30, "Validating Compose configuration")
	validationRunner, err := s.runnerForProject(ctx, project.ID)
	if err != nil {
		return result, err
	}
	if err := validationRunner.ValidateRelease(ctx, spec, io.Discard); err != nil {
		return result, err
	}
	rendered, err := validationRunner.Render(ctx, spec, io.Discard)
	if err != nil {
		return result, err
	}
	if err := s.validateDeploymentTargets(ctx, project.ID, spec, rendered); err != nil {
		if len(spec.Environment) > 0 {
			return result, errors.New("deployment policy rejected the release (details hidden for private environment)")
		}
		return result, fmt.Errorf("deployment policy rejected the release: %w", err)
	}
	configHash := releaseConfigurationHash(rendered, spec.Environment)
	images := imageReferences(rendered)
	for i := range images {
		images[i] = compose.RedactEnvironment(images[i], spec.Environment)
	}
	var existing database.DeliveryRelease
	err = s.db.WithContext(ctx).Where("project_id = ? AND repository_url = ? AND commit_sha = ? AND canonical_release_id IS NULL", project.ID, project.GitCloneURL, revision.CommitSHA).First(&existing).Error
	if err == nil {
		result.ReleaseID, result.Commit, result.ConfigHash = &existing.ID, existing.CommitSHA, configHash
		filesJSON, _ := json.Marshal(repository.ComposeFiles)
		imagesJSON, _ := json.Marshal(images)
		if err := s.db.WithContext(ctx).Model(&existing).Updates(map[string]any{"config_hash": configHash, "image_references": string(imagesJSON), "compose_files_json": string(filesJSON), "environment_file": repository.EnvironmentFile, "environment_ciphertext": project.EnvironmentCiphertext, "worktree_path": revision.WorktreePath}).Error; err != nil {
			return result, err
		}
		if err := s.db.WithContext(ctx).Model(&project).Update("observed_commit", revision.CommitSHA).Error; err != nil {
			return result, err
		}
		if !autoDeploy || existing.Status == StatusRejected {
			report(100, "Commit already synchronized")
			return result, nil
		}
		record, err := s.snapshotRecord(ctx, project, existing, AttemptDeploy, actor)
		if err != nil {
			return result, err
		}
		var activeCount int64
		var nodeIDs []string
		if err := json.Unmarshal([]byte(record.NodeIDsJSON), &nodeIDs); err != nil {
			return result, err
		}
		if err := s.db.WithContext(ctx).Table("delivery_target_states AS state").Joins("JOIN delivery_records AS record ON record.id = state.active_record_id").Where("state.project_id = ? AND state.node_id IN ? AND state.active_release_id = ? AND record.config_hash = ?", project.ID, nodeIDs, existing.ID, configHash).Count(&activeCount).Error; err != nil {
			return result, err
		}
		if activeCount == int64(len(nodeIDs)) {
			report(100, "Commit already reconciled")
			return result, nil
		}

		record.TriggerType = trigger
		if err := s.db.WithContext(ctx).Create(&record).Error; err != nil {
			return result, err
		}
		result.RecordID, result.Operation = &record.ID, "deploy"
		return result, s.executeRecord(ctx, project, existing, record, taskID, report, true)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return result, err
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
		PreviousReleaseID: project.ActiveReleaseID, WorktreePath: revision.WorktreePath, ComposeFilesJSON: string(filesJSON), EnvironmentFile: repository.EnvironmentFile, EnvironmentCiphertext: project.EnvironmentCiphertext,
	}
	if err := s.db.WithContext(ctx).Create(&release).Error; err != nil {
		return result, err
	}
	result.ReleaseID, result.Commit, result.ConfigHash = &release.ID, release.CommitSHA, configHash
	if err := s.snapshotReleaseTargets(ctx, project.ID, release.ID); err != nil {
		return result, err
	}
	if err := s.db.WithContext(ctx).Model(&project).Update("observed_commit", revision.CommitSHA).Error; err != nil {
		return result, err
	}
	if autoDeploy {
		record, err := s.snapshotRecord(ctx, project, release, AttemptDeploy, actor)
		if err != nil {
			return result, err
		}
		record.TriggerType = trigger
		if err := s.db.WithContext(ctx).Create(&record).Error; err != nil {
			return result, err
		}
		result.RecordID, result.Operation = &record.ID, "deploy"
		return result, s.executeRecord(ctx, project, release, record, taskID, report, true)
	}
	if s.emit != nil {
		s.emit(event.Event{Type: "cd.awaiting_approval", Severity: "warning", Scope: "control_plane", Project: project.Name, ResourceType: "release", ResourceID: fmt.Sprint(release.ID), ReleaseID: &release.ID, TaskID: taskID, Title: project.Name, Message: "Release " + release.CommitSHA + " is ready for approval"})
	}
	report(100, "Release is ready for approval")
	return result, nil
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

func (s *Service) finishAttempt(ctx context.Context, id uint, status, failureReason, healthSummary string) error {
	finished := time.Now()
	return s.db.WithContext(ctx).Model(&database.DeliveryDeploymentAttempt{}).Where("id = ?", id).Updates(map[string]any{
		"status": status, "failure_reason": failureReason, "health_summary": healthSummary, "finished_at": finished,
	}).Error
}

func (s *Service) recomputeReleaseAndProject(ctx context.Context, projectID, releaseID uint, failureReason string) (string, error) {
	return s.recomputeReleaseAndProjectDB(ctx, s.db.WithContext(ctx), projectID, releaseID, failureReason)
}

func (s *Service) recomputeReleaseAndProjectDB(ctx context.Context, db *gorm.DB, projectID, releaseID uint, failureReason string) (string, error) {
	var deployments []database.DeliveryReleaseDeployment
	if err := db.WithContext(ctx).Where("release_id = ?", releaseID).Find(&deployments).Error; err != nil {
		return "", err
	}
	succeeded, rolledBack, failed := 0, 0, 0
	for _, deployment := range deployments {
		switch deployment.Status {
		case StatusSucceeded:
			succeeded++
		case StatusRolledBack:
			rolledBack++
		default:
			failed++
		}
	}
	status := StatusFailed
	switch {
	case len(deployments) > 0 && succeeded == len(deployments):
		status = StatusSucceeded
		failureReason = ""
	case len(deployments) > 0 && rolledBack == len(deployments):
		status = StatusRolledBack
	case succeeded+rolledBack > 0 && failed > 0:
		status = StatusPartialFailed
	case succeeded > 0 && rolledBack > 0:
		status = StatusPartialFailed
	}
	finished := time.Now()
	if err := db.WithContext(ctx).Model(&database.DeliveryRelease{}).Where("id = ? AND project_id = ?", releaseID, projectID).Updates(map[string]any{
		"status": status, "failure_reason": failureReason, "finished_at": finished,
	}).Error; err != nil {
		return "", err
	}
	if err := s.recomputeProjectActiveReleaseDB(ctx, db, projectID); err != nil {
		return "", err
	}
	return status, nil
}

func (s *Service) recomputeProjectActiveRelease(ctx context.Context, projectID uint) error {
	return s.recomputeProjectActiveReleaseDB(ctx, s.db.WithContext(ctx), projectID)
}

func (s *Service) recomputeProjectActiveReleaseDB(ctx context.Context, db *gorm.DB, projectID uint) error {
	var targetRows []database.DeliveryProjectNode
	if err := db.WithContext(ctx).Where("project_id = ?", projectID).Order("node_id ASC").Find(&targetRows).Error; err != nil {
		return err
	}
	nodeIDs := make([]string, 0, len(targetRows))
	for _, targetRow := range targetRows {
		nodeIDs = append(nodeIDs, targetRow.NodeID)
	}
	if len(nodeIDs) == 0 {
		nodeIDs = []string{"local"}
	}
	var activeID *uint
	uniform := len(nodeIDs) > 0
	for _, nodeID := range nodeIDs {
		var state database.DeliveryTargetState
		if err := db.WithContext(ctx).Where("project_id = ? AND node_id = ?", projectID, nodeID).First(&state).Error; err != nil || state.ActiveReleaseID == nil {
			uniform = false
			continue
		}
		if activeID == nil {
			id := *state.ActiveReleaseID
			activeID = &id
		} else if *activeID != *state.ActiveReleaseID {
			uniform = false
		}
	}
	values := map[string]any{"active_release_id": nil, "active_commit": ""}
	if uniform && activeID != nil {
		var release database.DeliveryRelease
		if err := db.WithContext(ctx).Where("id = ? AND project_id = ?", *activeID, projectID).First(&release).Error; err == nil {
			values["active_release_id"] = *activeID
			values["active_commit"] = release.CommitSHA
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	return db.WithContext(ctx).Model(&database.DeliveryProject{}).Where("id = ?", projectID).Updates(values).Error
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
	if force {
		var states []database.DeliveryTargetState
		if err := s.db.WithContext(ctx).Where("project_id = ? AND active_release_id IS NOT NULL", project.ID).Order("node_id ASC").Find(&states).Error; err != nil {
			return err
		}
		for _, state := range states {
			var release database.DeliveryRelease
			if err := s.db.WithContext(ctx).First(&release, *state.ActiveReleaseID).Error; err != nil {
				return err
			}
			runner := s.compose
			if state.ActiveRecordID != nil {
				var record database.DeliveryRecord
				if err := s.db.WithContext(ctx).First(&record, *state.ActiveRecordID).Error; err != nil {
					return err
				}
				release = recordRelease(release, record)
				var err error
				runner, err = s.recordRunner(ctx, record, state.NodeID)
				if err != nil {
					return err
				}
			} else if s.targets != nil {
				target, err := s.composeTargetForProject(ctx, project.ID, state.NodeID)
				if err != nil {
					return err
				}
				targeted, ok := s.compose.(TargetedRunner)
				if !ok {
					return errors.New("Compose runner does not support node targets")
				}
				runner = targeted.Targeted(target)
			}
			spec, err := s.releaseSpec(runtimeName(project), release)
			if err != nil {
				return err
			}
			if err := runner.ForceDownRelease(ctx, spec, preserveVolumes, io.Discard); err != nil {
				return fmt.Errorf("force down delivery on node %s: %w", state.NodeID, err)
			}
		}
		// Older single-node deployments may predate per-node target state.
		if len(states) == 0 && project.ActiveReleaseID != nil {
			var release database.DeliveryRelease
			if err := s.db.WithContext(ctx).First(&release, *project.ActiveReleaseID).Error; err != nil {
				return err
			}
			spec, err := s.releaseSpec(runtimeName(project), release)
			if err != nil {
				return err
			}
			if err := s.compose.ForceDownRelease(ctx, spec, preserveVolumes, io.Discard); err != nil {
				return err
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
			var deploymentIDs []uint
			if err := tx.Model(&database.DeliveryReleaseDeployment{}).Where("release_id IN ?", releaseIDs).Pluck("id", &deploymentIDs).Error; err != nil {
				return err
			}
			if len(deploymentIDs) > 0 {
				if err := tx.Where("deployment_id IN ?", deploymentIDs).Delete(&database.DeliveryDeploymentAttempt{}).Error; err != nil {
					return err
				}
			}
			if err := tx.Where("release_id IN ?", releaseIDs).Delete(&database.DeliveryReleaseDeployment{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("project_id = ?", project.ID).Delete(&database.DeliveryRecord{}).Error; err != nil {
			return err
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
		if s.automation != nil {
			if err := s.automation.ArchiveOwned(tx, "cd.sync", "", name); err != nil {
				return err
			}
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
	if err := s.db.WithContext(ctx).Where("project_id = ? AND canonical_release_id IS NULL", project.ID).Order("id DESC").Limit(100).Find(&releases).Error; err != nil {
		return nil, err
	}
	for index := range releases {
		if err := s.loadReleaseDetails(ctx, &releases[index]); err != nil {
			return nil, err
		}
	}
	return releases, nil
}

func (s *Service) GetRelease(ctx context.Context, name string, id uint) (database.DeliveryRelease, error) {
	_, release, err := s.projectRelease(ctx, name, id)
	if err == nil {
		err = s.loadReleaseDetails(ctx, &release)
	}
	return release, err
}

func (s *Service) Drift(ctx context.Context, name string) (Drift, error) {
	project, err := s.project(ctx, name)
	if err != nil {
		return Drift{}, err
	}
	s.driftMu.Lock()
	if cached, ok := s.driftCache[project.ID]; ok && time.Now().Before(cached.expiresAt) {
		s.driftMu.Unlock()
		return cached.value, nil
	}
	if flight, ok := s.driftFlight[project.ID]; ok {
		s.driftMu.Unlock()
		select {
		case <-flight:
			return s.Drift(ctx, name)
		case <-ctx.Done():
			return Drift{}, ctx.Err()
		}
	}
	flight := make(chan struct{})
	s.driftFlight[project.ID] = flight
	s.driftMu.Unlock()
	value, err := s.probeProjectDrift(ctx, project)
	s.driftMu.Lock()
	if err == nil {
		s.driftCache[project.ID] = driftCacheEntry{value: value, expiresAt: time.Now().Add(5 * time.Second)}
	}
	delete(s.driftFlight, project.ID)
	close(flight)
	s.driftMu.Unlock()
	if err == nil && s.emit != nil {
		for _, n := range value.Nodes {
			if n.Drifted {
				s.emit(event.Event{Type: "cd.drift", Severity: "warning", NodeID: n.NodeID, NodeName: n.NodeName, Project: project.Name, ResourceType: "project", ResourceID: project.Name, Title: project.Name, Message: n.ReasonCode})
			} else if n.Status == DriftHealthy && project.DesiredCommit != "" {
				s.emit(event.Event{Type: "cd.recovered", Severity: "info", NodeID: n.NodeID, NodeName: n.NodeName, Project: project.Name, ResourceType: "project", ResourceID: project.Name, Title: project.Name, Message: "Deployment is healthy and matches the desired state", Time: n.CheckedAt})
			}
		}
	}
	if err != nil {
		return Drift{}, err
	}
	return value, nil
}

func (s *Service) probeProjectDrift(ctx context.Context, project database.DeliveryProject) (Drift, error) {
	checked := time.Now()
	ids, err := s.projectNodeIDs(ctx, project.ID)
	if err != nil {
		return Drift{}, err
	}
	result := Drift{Status: DriftHealthy, DesiredCommit: project.DesiredCommit, ObservedCommit: project.ObservedCommit, RuntimeHealthy: true, CheckedAt: checked, Nodes: make([]NodeDrift, len(ids))}
	targeted, targetedOK := s.compose.(TargetedRunner)
	semaphore := make(chan struct{}, 8)
	var wait sync.WaitGroup
	for index, nodeID := range ids {
		wait.Add(1)
		go func(index int, nodeID string) {
			defer wait.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			nodeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			result.Nodes[index] = s.probeNodeDrift(nodeCtx, project, nodeID, targeted, targetedOK, checked)
		}(index, nodeID)
	}
	wait.Wait()
	var uniformRelease *uint
	uniform := len(result.Nodes) > 0
	for index := range result.Nodes {
		node := result.Nodes[index]
		if node.Status == DriftDegraded {
			result.Status = DriftDegraded
			result.Drifted = result.Drifted || node.Drifted
			if result.ReasonCode == "" {
				result.ReasonCode, result.Reason = node.ReasonCode, node.Reason
			}
		} else if node.Status == DriftUnknown && result.Status != DriftDegraded {
			result.Status = DriftUnknown
			if result.ReasonCode == "" {
				result.ReasonCode, result.Reason = node.ReasonCode, node.Reason
			}
		}
		if node.ActiveReleaseID == nil {
			uniform = false
		} else if uniformRelease == nil {
			id := *node.ActiveReleaseID
			uniformRelease = &id
		} else if *uniformRelease != *node.ActiveReleaseID {
			uniform = false
		}
	}
	if project.DesiredCommit == "" {
		result.Status, result.Drifted, result.ReasonCode, result.Reason = DriftDegraded, true, "repository_not_synchronized", "repository has not been synchronized"
	}
	if uniform && uniformRelease != nil {
		result.ActiveReleaseID = uniformRelease
		var release database.DeliveryRelease
		if err := s.db.WithContext(ctx).First(&release, *uniformRelease).Error; err == nil {
			result.ActiveCommit = release.CommitSHA
		}
	} else if len(result.Nodes) > 1 && result.ReasonCode == "" {
		result.ReasonCode, result.Reason = "mixed_active_releases", "target nodes do not share one active release"
	}
	result.RuntimeHealthy = result.Status == DriftHealthy
	activeValue := any(nil)
	if result.ActiveReleaseID != nil {
		activeValue = *result.ActiveReleaseID
	}
	_ = s.db.WithContext(ctx).Model(&database.DeliveryProject{}).Where("id = ?", project.ID).Updates(map[string]any{"active_release_id": activeValue, "active_commit": result.ActiveCommit}).Error
	return result, nil
}

func (s *Service) probeNodeDrift(ctx context.Context, project database.DeliveryProject, nodeID string, targeted TargetedRunner, targetedOK bool, checked time.Time) NodeDrift {
	result := NodeDrift{NodeID: nodeID, NodeName: nodeID, Status: DriftHealthy, CheckedAt: checked}
	var node database.Node
	if err := s.db.WithContext(ctx).First(&node, "id = ?", nodeID).Error; err == nil {
		result.NodeName = node.Name
	}
	var state database.DeliveryTargetState
	if err := s.db.WithContext(ctx).Where("project_id = ? AND node_id = ?", project.ID, nodeID).First(&state).Error; err != nil || state.ActiveReleaseID == nil {
		result.Status, result.Drifted, result.ReasonCode, result.Reason = DriftDegraded, true, "no_active_release", "no release is active"
		return result
	}
	result.ActiveReleaseID = state.ActiveReleaseID
	var release database.DeliveryRelease
	if err := s.db.WithContext(ctx).First(&release, *state.ActiveReleaseID).Error; err != nil {
		result.Status, result.ReasonCode, result.Reason = DriftUnknown, "active_release_unavailable", "active release state is unavailable"
		return result
	}
	result.ActiveCommit = release.CommitSHA
	if project.DesiredCommit != "" && release.CommitSHA != project.DesiredCommit {
		result.Status, result.Drifted, result.ReasonCode, result.Reason = DriftDegraded, true, "desired_commit_mismatch", "active release differs from desired Git commit"
	}
	if state.ActiveRecordID != nil {
		var record database.DeliveryRecord
		if err := s.db.WithContext(ctx).First(&record, *state.ActiveRecordID).Error; err != nil {
			result.Status, result.ReasonCode, result.Reason = DriftUnknown, "release_spec_unavailable", "active configuration record is unavailable"
			return result
		}
		release = recordRelease(release, record)
	}
	spec, err := s.releaseSpec(runtimeName(project), release)
	if err != nil {
		result.Status, result.ReasonCode, result.Reason = DriftUnknown, "release_spec_unavailable", err.Error()
		return result
	}
	runner := s.compose
	if s.targets != nil {
		if !targetedOK {
			result.Status, result.ReasonCode, result.Reason = DriftUnknown, "target_runner_unavailable", "Compose runner does not support node targets"
			return result
		}
		target, err := s.targets.ResolveComposeTarget(ctx, nodeID)
		if err != nil {
			if !result.Drifted {
				result.Status, result.ReasonCode, result.Reason = DriftUnknown, "runtime_unavailable", err.Error()
			}
			return result
		}
		runner = targeted.Targeted(target)
	}
	runtime, err := runner.PS(ctx, spec, io.Discard)
	if err != nil {
		if !result.Drifted {
			result.Status, result.ReasonCode, result.Reason = DriftUnknown, "runtime_unavailable", err.Error()
		}
		return result
	}
	result.HealthSummary = runtime
	if !runtimeIsHealthy(runtime) {
		result.Status, result.Drifted, result.ReasonCode, result.Reason = DriftDegraded, true, "runtime_unhealthy", "active release has missing or unhealthy containers"
	}
	return result
}

func (s *Service) loadReleaseDetails(ctx context.Context, release *database.DeliveryRelease) error {
	var latestRecord *database.DeliveryRecord
	deploymentReleaseID := release.ID
	if release.LatestRecordID != nil {
		var record database.DeliveryRecord
		if err := s.db.WithContext(ctx).First(&record, *release.LatestRecordID).Error; err != nil {
			return err
		}
		latestRecord = &record
		if record.LegacyReleaseID != nil {
			deploymentReleaseID = *record.LegacyReleaseID
		}
	}

	if release.ApprovedBy != nil {
		if err := s.db.WithContext(ctx).Model(&database.User{}).Where("id = ?", *release.ApprovedBy).Pluck("username", &release.ApprovedByName).Error; err != nil {
			return err
		}
	}
	if err := s.db.WithContext(ctx).Where("release_id = ?", deploymentReleaseID).Order("node_id ASC").Find(&release.Deployments).Error; err != nil {
		return err
	}
	release.Remediation.RetryFailedNodeIDs = []string{}
	release.Remediation.RollbackFailedNodeIDs = []string{}
	for index := range release.Deployments {
		deployment := &release.Deployments[index]
		if err := s.db.WithContext(ctx).Where("deployment_id = ?", deployment.ID).Order("id ASC").Find(&deployment.Attempts).Error; err != nil {
			return err
		}
		for attemptIndex := range deployment.Attempts {
			attempt := &deployment.Attempts[attemptIndex]
			if attempt.TaskID == "" {
				continue
			}
			var row database.Task
			if err := s.db.WithContext(ctx).First(&row, "id = ?", attempt.TaskID).Error; err == nil {
				attempt.Progress, attempt.Message = row.Progress, row.Message
				deployment.Progress, deployment.Message = row.Progress, row.Message
			}
		}
	}
	if latestRecord != nil {
		if !latestRecord.SnapshotComplete && latestRecord.RestoreRecordsJSON == "" {
			return nil
		}
		release.Remediation.SourceRecordID = release.LatestRecordID
		var attempts []database.DeliveryDeploymentAttempt
		if err := s.db.WithContext(ctx).Where("record_id = ? AND status IN ?", *release.LatestRecordID, []string{StatusFailed, StatusInterrupted}).Find(&attempts).Error; err != nil {
			return err
		}
		for _, a := range attempts {
			release.Remediation.RetryFailedNodeIDs = append(release.Remediation.RetryFailedNodeIDs, a.NodeID)
			if a.PreviousRecordID != nil {
				var previous database.DeliveryRecord
				if s.db.WithContext(ctx).First(&previous, *a.PreviousRecordID).Error == nil && previous.SnapshotComplete {
					release.Remediation.RollbackFailedNodeIDs = append(release.Remediation.RollbackFailedNodeIDs, a.NodeID)
				}
			}
		}
	}

	return nil
}

func (s *Service) invalidateDrift(projectID uint) {
	s.driftMu.Lock()
	delete(s.driftCache, projectID)
	s.driftMu.Unlock()
}

var runtimeIsHealthy = deployment.RuntimeIsHealthy

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
	ids, err := s.projectRegistryCredentialIDs(ctx, projectID)
	if err != nil {
		return compose.Target{}, err
	}
	return s.composeTargetWithCredentials(ctx, nodeID, ids)
}
func (s *Service) composeTargetWithCredentials(ctx context.Context, nodeID string, ids []uint) (compose.Target, error) {
	target, err := s.targets.ResolveComposeTarget(ctx, nodeID)
	if err != nil || len(ids) == 0 {
		return target, err
	}
	config, err := deployment.RegistryConfiguration(ctx, s.registries, nodeID, ids)
	if err != nil {
		return target, err
	}
	target.DockerConfig = config
	return target, nil
}

func (s *Service) validateDeploymentTargets(ctx context.Context, projectID uint, spec compose.ExecutionSpec, rendered string) error {
	ids, err := s.projectNodeIDs(ctx, projectID)
	if err != nil {
		return err
	}
	return s.validateDeploymentNodeTargets(ctx, ids, spec, rendered)
}
func (s *Service) validateDeploymentNodeTargets(ctx context.Context, ids []string, spec compose.ExecutionSpec, rendered string) error {
	var gitopsOwners int64
	if err := s.db.WithContext(ctx).Model(&database.ProjectCD{}).Where("node_id IN ? AND project_name = ? AND enabled = true", ids, spec.ProjectName).Count(&gitopsOwners).Error; err != nil {
		return err
	}
	if gitopsOwners > 0 {
		return errors.New("Compose namespace is managed by node GitOps")
	}

	if err := validateDeploymentPolicy(rendered); err != nil {
		return err
	}
	remote := false
	if s.targets != nil {
		for _, id := range ids {
			var row database.Node
			if err := s.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
				return err
			}
			remote = remote || row.ConnectionType != "unix"
		}
	}
	// Rendering resolves relative paths and interpolation. Check original bind
	// declarations as well, so TCP/Agent targets require explicit host paths.
	for _, file := range spec.Files {
		content, err := readDeploymentFile(file)
		if err != nil {
			return fmt.Errorf("read Compose file %q: %w", file, err)
		}
		if err := compose.ValidateComposeBindMounts(string(content), remote, false); err != nil {
			return fmt.Errorf("Compose file %q: %w", file, err)
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
	if err == nil && release.CanonicalReleaseID != nil {
		canonicalID := *release.CanonicalReleaseID
		release = database.DeliveryRelease{}
		err = s.db.WithContext(ctx).Where("id = ? AND project_id = ?", canonicalID, project.ID).First(&release).Error
	}
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

func (s *Service) recordNodeFinal(actor Actor, action, name, nodeID, nodeName, taskID string, releaseID uint, result error) {
	if s.audit == nil {
		return
	}
	value := "success"
	if result != nil {
		value = "failed"
	}
	_ = s.audit.RecordLinkedForNode(context.Background(), nodeID, nodeName, actor.UserID, action, "delivery_project", name, actor.IP, value, taskID, &releaseID)
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
	var files []string
	if err := json.Unmarshal([]byte(release.ComposeFilesJSON), &files); err != nil {
		return compose.ExecutionSpec{}, fmt.Errorf("decode release Compose files: %w", err)
	}
	return deployment.ResolveSpec(projectName, release.WorktreePath, files, release.EnvironmentFile)
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

func (s *Service) SetEventSink(sink event.Sink) { s.emit = sink }
