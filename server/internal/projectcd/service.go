// Package projectcd implements the CD deployment mode of one node Project.
// Multi-node Continuous Delivery shares internal/deployment and owns its batch orchestration.
package projectcd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/credential"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/deployment"
	gitrepo "github.com/suma/suma/server/internal/git"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/task"
	"gorm.io/gorm"
)

var ErrConflict = errors.New("project CD configuration changed; check the project again")
var ErrPaused = errors.New("project CD automatic synchronization is paused")
var errDeploymentFailed = errors.New("single-node delivery failed; inspect task and Docker state before retrying (private diagnostic hidden)")

type Actor struct {
	UserID   *uint
	Name, IP string
}
type Dependencies struct {
	Inspect func(context.Context, string, string) (compose.RuntimeProjectSnapshot, error)
	Project func(context.Context, string) (*compose.Service, error)
	Target  func(context.Context, string) (compose.Target, error)
}
type Service struct {
	db          *gorm.DB
	git         gitrepo.Client
	credentials *gitrepo.CredentialService
	registries  *credential.RegistryService
	runner      *compose.CLIRunner
	tasks       *task.Service
	audit       *audit.Service
	secrets     *secret.Store
	deps        Dependencies
	mu          sync.Mutex
	cancel      context.CancelFunc
	wg          sync.WaitGroup
}

func New(db *gorm.DB, git gitrepo.Client, credentials *gitrepo.CredentialService, registries *credential.RegistryService, runner *compose.CLIRunner, tasks *task.Service, audits *audit.Service, secrets *secret.Store, deps Dependencies) *Service {
	return &Service{db: db, git: git, credentials: credentials, registries: registries, runner: runner, tasks: tasks, audit: audits, secrets: secrets, deps: deps}
}

type ConfigureInput struct {
	Repository            gitrepo.Repository               `json:"repository"`
	Environment           []deployment.EnvironmentVariable `json:"environment,omitempty"`
	RegistryCredentialIDs []uint                           `json:"registry_credential_ids"`
	DeploymentTimeout     int                              `json:"deployment_timeout"`
	SyncIntervalSeconds   int                              `json:"sync_interval_seconds"`
	WebhookEnabled        bool                             `json:"webhook_enabled"`
	WebhookSecret         string                           `json:"webhook_secret"`
	ReconcileMode         string                           `json:"reconcile_mode"`
	AutoRollback          bool                             `json:"auto_rollback"`
	AutomationEnabled     *bool                            `json:"automation_enabled"`
	ExpectedRevision      uint64                           `json:"expected_revision"`
	ReviewedEnvironment   bool                             `json:"reviewed_environment"`
}
type Configuration struct {
	Configured                 bool                             `json:"configured"`
	Enabled                    bool                             `json:"enabled"`
	Revision                   uint64                           `json:"revision"`
	Repository                 gitrepo.Repository               `json:"repository"`
	Environment                []deployment.EnvironmentVariable `json:"environment"`
	RegistryCredentialIDs      []uint                           `json:"registry_credential_ids"`
	DeploymentTimeout          int                              `json:"deployment_timeout"`
	SyncIntervalSeconds        int                              `json:"sync_interval_seconds"`
	AutomationEnabled          bool                             `json:"automation_enabled"`
	ReconcileMode              string                           `json:"reconcile_mode"`
	AutoRollback               bool                             `json:"auto_rollback"`
	InitialApplicationRequired bool                             `json:"initial_application_required"`
	WebhookEnabled             bool                             `json:"webhook_enabled"`
	WebhookID                  string                           `json:"webhook_id,omitempty"`
	WebhookSecret              string                           `json:"webhook_secret,omitempty"`
	Candidate                  *database.ProjectCDRecord        `json:"candidate,omitempty"`
	Applied                    *database.ProjectCDRecord        `json:"applied,omitempty"`
}
type SourceFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}
type Preview struct {
	RecordID              uint         `json:"record_id"`
	CommitSHA             string       `json:"commit_sha"`
	Applied               bool         `json:"applied"`
	Compose               string       `json:"compose"`
	Files                 []SourceFile `json:"files"`
	RepositoryEnvironment string       `json:"repository_environment"`
}
type snapshot struct {
	Repository            gitrepo.Repository
	Worktree              string
	Environment           map[string]string
	RegistryCredentialIDs []uint
	DeploymentTimeout     int
	GitCredentialID       *uint
	CredentialCiphertext  []byte
	RuntimeIdentity       string
	Rendered              string
	AutoRollback          bool
}

func (s *Service) row(ctx context.Context, node, name string) (database.ProjectCD, error) {
	var row database.ProjectCD
	err := s.db.WithContext(ctx).Where("node_id = ? AND project_name = ?", node, name).First(&row).Error
	return row, err
}
func (s *Service) Get(ctx context.Context, node, name string) (Configuration, error) {
	row, err := s.row(ctx, node, name)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		project, lookupErr := s.deps.Project(ctx, node)
		if lookupErr != nil {
			return Configuration{}, lookupErr
		}
		if _, lookupErr := project.ManagedConfiguration(name); lookupErr != nil {
			return Configuration{}, lookupErr
		}
		return Configuration{Repository: gitrepo.Repository{RefType: "branch", Ref: "main", ComposeFiles: []string{"compose.yml"}, Authentication: gitrepo.Authentication{Source: "none"}}, Environment: []deployment.EnvironmentVariable{}, RegistryCredentialIDs: []uint{}, ReconcileMode: "manual", DeploymentTimeout: 120, SyncIntervalSeconds: 300, InitialApplicationRequired: true}, nil
	}
	if err != nil {
		return Configuration{}, err
	}
	var repo gitrepo.Repository
	if err := json.Unmarshal([]byte(row.RepositoryJSON), &repo); err != nil {
		return Configuration{}, err
	}
	env, err := deployment.EnvironmentSummary(s.secrets, row.EnvironmentCiphertext)
	if err != nil {
		return Configuration{}, err
	}
	var ids []uint
	if err := json.Unmarshal([]byte(row.RegistryCredentialIDsJSON), &ids); err != nil {
		return Configuration{}, err
	}
	result := Configuration{Configured: true, Enabled: row.Enabled, Revision: row.Revision, Repository: repo, Environment: env, RegistryCredentialIDs: ids, DeploymentTimeout: row.DeploymentTimeout, SyncIntervalSeconds: row.SyncIntervalSeconds, AutomationEnabled: row.AutomationEnabled, InitialApplicationRequired: row.InitialApplicationRequired, WebhookEnabled: row.WebhookEnabled, WebhookID: row.WebhookID}
	result.ReconcileMode, result.AutoRollback = row.ReconcileMode, row.AutoRollback
	if row.CandidateRecordID != nil {
		record, err := s.record(ctx, row, *row.CandidateRecordID)
		if err != nil {
			return result, err
		}
		result.Candidate = &record
	}
	if row.AppliedRecordID != nil {
		record, err := s.record(ctx, row, *row.AppliedRecordID)
		if err != nil {
			return result, err
		}
		result.Applied = &record
	}

	return result, nil
}
func (s *Service) credential(ctx context.Context, node string, repo gitrepo.Repository, current []byte) (gitrepo.CredentialMaterial, []byte, error) {
	switch repo.Authentication.Source {
	case "", gitrepo.CredentialSourceNone:
		return gitrepo.CredentialMaterial{AuthType: gitrepo.AuthNone}, nil, nil
	case gitrepo.CredentialSourceCenter:
		if repo.Authentication.CredentialID == nil {
			return gitrepo.CredentialMaterial{}, nil, errors.New("Git credential is required")
		}
		if err := s.credentials.AuthorizedForNodes(ctx, *repo.Authentication.CredentialID, []string{node}); err != nil {
			return gitrepo.CredentialMaterial{}, nil, err
		}
		value, err := s.credentials.Material(ctx, *repo.Authentication.CredentialID)
		if err == nil {
			err = gitrepo.ValidateCredentialTransport(repo.CloneURL, value.AuthType)
		}
		return value, nil, err
	case gitrepo.CredentialSourceProject:
		value, encrypted, err := s.credentials.InlineMaterial(current, repo.Authentication.Credential)
		if err == nil {
			err = gitrepo.ValidateCredentialTransport(repo.CloneURL, value.AuthType)
		}
		return value, encrypted, err
	default:
		return gitrepo.CredentialMaterial{}, nil, errors.New("invalid Git credential source")
	}
}
func token() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}
func (s *Service) Configure(ctx context.Context, node, name string, input ConfigureInput) (Configuration, error) {
	project, err := s.deps.Project(ctx, node)
	if err != nil {
		return Configuration{}, err
	}
	release, err := project.ReserveProject(name)
	if err != nil {
		return Configuration{}, err
	}
	defer release()
	ordinary, err := project.ManagedConfiguration(name)
	if err != nil {
		return Configuration{}, err
	}
	var conflicts int64
	if err := s.db.WithContext(ctx).Table("delivery_projects p").Where("p.id IN (SELECT project_id FROM delivery_project_nodes WHERE node_id = ? UNION SELECT project_id FROM delivery_target_states WHERE node_id = ?) AND COALESCE(NULLIF(p.deployment_name, ''), 'suma-cd-' || p.id::text) = ?", node, node, name).Count(&conflicts).Error; err != nil {
		return Configuration{}, err
	}
	if conflicts > 0 {
		return Configuration{}, errors.New("project belongs to global continuous delivery")
	}
	if err := gitrepo.ValidateRepository(input.Repository); err != nil {
		return Configuration{}, err
	}
	if input.SyncIntervalSeconds == 0 {
		input.SyncIntervalSeconds = 300
	}
	if input.SyncIntervalSeconds < 30 || input.SyncIntervalSeconds > 86400 {
		return Configuration{}, errors.New("sync interval must be between 30 and 86400 seconds")
	}
	if input.DeploymentTimeout == 0 {
		input.DeploymentTimeout = 120
	}
	if input.DeploymentTimeout < 10 || input.DeploymentTimeout > 3600 {
		return Configuration{}, errors.New("deployment timeout must be between 10 and 3600 seconds")
	}
	row, err := s.row(ctx, node, name)
	fresh := errors.Is(err, gorm.ErrRecordNotFound)
	if err != nil && !fresh {
		return Configuration{}, err
	}
	if input.ExpectedRevision != row.Revision {
		return Configuration{}, ErrConflict
	}
	if input.ReconcileMode == "" {
		input.ReconcileMode = row.ReconcileMode
		if input.ReconcileMode == "" {
			input.ReconcileMode = "manual"
		}
	}
	if input.ReconcileMode != "observe" && input.ReconcileMode != "manual" && input.ReconcileMode != "auto" {
		return Configuration{}, errors.New("reconcile mode must be observe, manual, or auto")
	}
	activating := fresh || !row.Enabled
	if activating && ordinary.Environment != "" && !input.ReviewedEnvironment {
		return Configuration{}, errors.New("review and import the existing .env before enabling CD mode")
	}
	material, encrypted, err := s.credential(ctx, node, input.Repository, row.CredentialCiphertext)
	if err != nil {
		return Configuration{}, err
	}
	environment, err := deployment.ConfigureEnvironment(s.secrets, row.EnvironmentCiphertext, input.Environment)
	if err != nil {
		return Configuration{}, err
	}
	seenRegistryIDs := map[uint]bool{}
	for _, id := range input.RegistryCredentialIDs {
		if id == 0 || seenRegistryIDs[id] {
			return Configuration{}, errors.New("registry credential IDs must be nonzero and unique")
		}
		seenRegistryIDs[id] = true
		if err := s.registries.AuthorizedForNode(ctx, id, node); err != nil {
			return Configuration{}, err
		}
	}
	repository := input.Repository
	repository.Authentication.Credential = nil
	repository.Authentication.Summary = nil
	if repository.Authentication.Source == gitrepo.CredentialSourceProject {
		repository.Authentication.Summary = &gitrepo.CredentialSummary{Name: "Project Git credential", AuthType: material.AuthType, Username: material.Username}
	}
	if repository.Authentication.Source != gitrepo.CredentialSourceCenter {
		repository.Authentication.CredentialID = nil
	}
	repository.Authentication.SaveToCenter = false
	raw, _ := json.Marshal(repository)
	ids, _ := json.Marshal(input.RegistryCredentialIDs)
	if input.RegistryCredentialIDs == nil {
		ids = []byte("[]")
	}
	sourceChanged := activating
	if !fresh {
		var old gitrepo.Repository
		if err := json.Unmarshal([]byte(row.RepositoryJSON), &old); err != nil {
			return Configuration{}, err
		}
		before, _ := json.Marshal(struct {
			URL, Type, Ref, Env string
			Files               []string
		}{old.CloneURL, old.RefType, old.Ref, old.EnvironmentFile, old.ComposeFiles})
		after, _ := json.Marshal(struct {
			URL, Type, Ref, Env string
			Files               []string
		}{repository.CloneURL, repository.RefType, repository.Ref, repository.EnvironmentFile, repository.ComposeFiles})
		sourceChanged = sourceChanged || string(before) != string(after)
	}
	revealed := ""
	if fresh {
		hook, err := token()
		if err != nil {
			return Configuration{}, err
		}
		revealed, err = token()
		if err != nil {
			return Configuration{}, err
		}
		cipher, err := s.secrets.Encrypt(revealed)
		if err != nil {
			return Configuration{}, err
		}
		row = database.ProjectCD{NodeID: node, ProjectName: name, WebhookID: hook, WebhookSecret: cipher, InitialApplicationRequired: true}
	}
	if activating {
		row.AppliedRecordID = nil
	}
	row.Enabled = true
	row.Revision++
	row.RepositoryJSON = string(raw)
	row.GitCredentialID = repository.Authentication.CredentialID
	row.CredentialCiphertext = encrypted
	row.EnvironmentCiphertext = environment
	row.RegistryCredentialIDsJSON = string(ids)
	row.DeploymentTimeout = input.DeploymentTimeout
	row.SyncIntervalSeconds = input.SyncIntervalSeconds
	row.WebhookEnabled = input.WebhookEnabled
	row.CandidateRecordID = nil
	row.FailedFingerprint = ""
	if sourceChanged {
		row.InitialApplicationRequired = true
	}
	row.ReconcileMode, row.AutoRollback = input.ReconcileMode, input.AutoRollback
	if input.AutomationEnabled != nil {
		row.AutomationEnabled = *input.AutomationEnabled
	}
	if input.WebhookSecret != "" {
		row.WebhookSecret, err = s.secrets.Encrypt(input.WebhookSecret)
		if err != nil {
			return Configuration{}, err
		}
		revealed = ""
	}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if input.Repository.Authentication.SaveToCenter {
			if input.Repository.Authentication.Source != gitrepo.CredentialSourceProject || input.Repository.Authentication.Credential == nil {
				return errors.New("a project credential is required before saving to the authentication center")
			}
			// InlineMaterial has already merged preserved private fields. Save that
			// complete material, and only grant the route's fixed project node.
			credential := *input.Repository.Authentication.Credential
			credential.Secret, credential.PrivateKey, credential.Passphrase = material.Secret, material.PrivateKey, material.Passphrase
			credential.KnownHosts, credential.CustomCA = material.KnownHosts, material.CustomCA
			credential.AuthorizedNodeIDs = []string{node}
			created, err := s.credentials.CreateWithDB(ctx, tx, credential)
			if err != nil {
				return err
			}
			repository.Authentication = gitrepo.Authentication{Source: gitrepo.CredentialSourceCenter, CredentialID: &created.ID}
			raw, err := json.Marshal(repository)
			if err != nil {
				return err
			}
			row.RepositoryJSON, row.GitCredentialID, row.CredentialCiphertext = string(raw), &created.ID, nil
		}
		return tx.WithContext(ctx).Save(&row).Error
	}); err != nil {
		return Configuration{}, err
	}
	result, err := s.Get(ctx, node, name)
	result.WebhookSecret = revealed
	return result, err
}
func (s *Service) TestConnection(ctx context.Context, node, name string, repository gitrepo.Repository) (gitrepo.ConnectionResult, error) {
	row, err := s.row(ctx, node, name)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return gitrepo.ConnectionResult{}, err
	}
	material, _, err := s.credential(ctx, node, repository, row.CredentialCiphertext)
	if err != nil {
		return gitrepo.ConnectionResult{}, err
	}
	return s.git.TestConnection(ctx, gitrepo.SyncRequest{Repository: repository, Credential: material})
}
func (s *Service) Environment(ctx context.Context, node, name string) (map[string]string, error) {
	row, err := s.row(ctx, node, name)
	if err != nil {
		return nil, err
	}
	return deployment.DecryptEnvironment(s.secrets, row.EnvironmentCiphertext)
}
func (s *Service) SetEnvironment(ctx context.Context, node, name string, revision uint64, variables []deployment.EnvironmentVariable) (Configuration, error) {
	current, err := s.Get(ctx, node, name)
	if err != nil {
		return current, err
	}
	if !current.Enabled {
		return current, errors.New("project CD mode is not enabled")
	}
	return s.Configure(ctx, node, name, ConfigureInput{Repository: current.Repository, Environment: variables, RegistryCredentialIDs: current.RegistryCredentialIDs, DeploymentTimeout: current.DeploymentTimeout, SyncIntervalSeconds: current.SyncIntervalSeconds, WebhookEnabled: current.WebhookEnabled, ReconcileMode: current.ReconcileMode, AutoRollback: current.AutoRollback, ExpectedRevision: revision})
}
func (s *Service) SetAutomation(ctx context.Context, node, name string, enabled bool, revision uint64) (Configuration, error) {
	project, err := s.deps.Project(ctx, node)
	if err != nil {
		return Configuration{}, err
	}
	release, err := project.ReserveProject(name)
	if err != nil {
		return Configuration{}, err
	}
	defer release()
	row, err := s.row(ctx, node, name)
	if err != nil {
		return Configuration{}, err
	}
	if row.Revision != revision {
		return Configuration{}, ErrConflict
	}
	if !row.Enabled {
		return Configuration{}, errors.New("project CD mode is not enabled")
	}
	if err := s.db.WithContext(ctx).Model(&row).Updates(map[string]any{"automation_enabled": enabled, "revision": row.Revision + 1}).Error; err != nil {
		return Configuration{}, err
	}
	return s.Get(ctx, node, name)
}
func (s *Service) decode(record database.ProjectCDRecord) (snapshot, error) {
	var value snapshot
	raw, err := s.secrets.Decrypt(record.SnapshotCiphertext)
	if err != nil {
		return value, err
	}
	err = json.Unmarshal([]byte(raw), &value)
	return value, err
}
func (s *Service) encrypt(value snapshot) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return s.secrets.Encrypt(string(raw))
}
func (s *Service) record(ctx context.Context, row database.ProjectCD, id uint) (database.ProjectCDRecord, error) {
	var value database.ProjectCDRecord
	err := s.db.WithContext(ctx).Where("project_cd_id = ? AND id = ?", row.ID, id).First(&value).Error
	return value, err
}
func (s *Service) Records(ctx context.Context, node, name string, page int) ([]database.ProjectCDRecord, int64, error) {
	row, err := s.row(ctx, node, name)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return []database.ProjectCDRecord{}, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	query := s.db.WithContext(ctx).Model(&database.ProjectCDRecord{}).Where("project_cd_id = ?", row.ID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if page < 1 {
		page = 1
	}
	var records []database.ProjectCDRecord
	err = query.Order("id DESC").Offset((page - 1) * 20).Limit(20).Find(&records).Error
	return records, total, err
}
func (s *Service) Preview(ctx context.Context, node, name string) (Preview, error) {
	row, err := s.row(ctx, node, name)
	if err != nil {
		return Preview{}, err
	}
	id := row.CandidateRecordID
	if id == nil {
		id = row.AppliedRecordID
	}
	if id == nil {
		return Preview{}, gorm.ErrRecordNotFound
	}
	record, err := s.record(ctx, row, *id)
	if err != nil {
		return Preview{}, err
	}
	value, err := s.decode(record)
	if err != nil {
		return Preview{}, err
	}
	spec, err := deployment.Spec(name, value.Worktree, value.Repository)
	if err != nil {
		return Preview{}, err
	}
	redacted, err := deployment.RedactConfiguration(value.Rendered, value.Environment)
	if err != nil {
		return Preview{}, err
	}
	result := Preview{RecordID: record.ID, CommitSHA: record.CommitSHA, Applied: row.AppliedRecordID != nil && record.CommitSHA != "", Compose: redacted, Files: []SourceFile{}}
	if row.AppliedRecordID != nil {
		active, err := s.record(ctx, row, *row.AppliedRecordID)
		if err != nil {
			return result, err
		}
		result.Applied = active.CommitSHA == record.CommitSHA && active.ConfigHash == record.ConfigHash
	}
	for i, file := range spec.Files {
		raw, err := deployment.ReadDeploymentFile(file)
		if err != nil {
			return result, err
		}
		result.Files = append(result.Files, SourceFile{Path: value.Repository.ComposeFiles[i], Content: string(raw)})
	}
	envPath := filepath.Join(spec.ProjectDir, ".env")
	if len(spec.EnvFiles) > 0 {
		envPath = spec.EnvFiles[0]
	}
	raw, err := os.ReadFile(envPath)
	if err != nil && !os.IsNotExist(err) {
		return result, err
	}
	result.RepositoryEnvironment = string(raw)
	return result, nil
}

func (s *Service) submit(ctx context.Context, node, name, operation, trigger string, actor Actor, work func(context.Context, *database.ProjectCDRecord, database.ProjectCD, task.Reporter) error) (database.Task, error) {
	project, err := s.deps.Project(ctx, node)
	if err != nil {
		return database.Task{}, err
	}
	release, err := project.ReserveProject(name)
	if err != nil {
		return database.Task{}, err
	}
	row, err := s.row(ctx, node, name)
	if err != nil || !row.Enabled {
		release()
		if err == nil {
			err = errors.New("project CD mode is not enabled")
		}
		return database.Task{}, err
	}
	if trigger != "manual" && !row.AutomationEnabled {
		release()
		return database.Task{}, ErrPaused
	}
	target, err := s.deps.Target(ctx, node)
	if err != nil {
		release()
		return database.Task{}, err
	}
	result, err := s.tasks.StartDefinition(context.Background(), task.Definition{
		Type: "project.cd." + operation, Name: operation + " " + name,
		NodeID: node, NodeName: target.NodeName,
		Params: map[string]any{"project": name, "operation": operation, "trigger": trigger, "revision": row.Revision},
	}, func(ctx context.Context, id string, report task.Reporter) (_ any, workErr error) {
		defer release()
		current, err := s.row(ctx, node, name)
		if err != nil {
			return nil, err
		}
		if !current.Enabled || current.Revision != row.Revision {
			return nil, ErrConflict
		}
		if trigger != "manual" && !current.AutomationEnabled {
			return nil, ErrPaused
		}
		currentTarget, err := s.deps.Target(ctx, node)
		if err != nil {
			return nil, err
		}
		if currentTarget.RuntimeIdentity != target.RuntimeIdentity {
			return nil, errors.New("node runtime changed")
		}
		record := database.ProjectCDRecord{ProjectCDID: row.ID, Operation: operation, Trigger: trigger, Status: "running", TaskID: id, Actor: actor.Name, Revision: row.Revision}
		if err := s.db.WithContext(ctx).Create(&record).Error; err != nil {
			return nil, err
		}
		workErr = work(ctx, &record, current, report)
		now := time.Now().UTC()
		status := "succeeded"
		reason := ""
		if workErr != nil {
			status = "failed"
			reason = workErr.Error()
		}
		finishCtx := context.WithoutCancel(ctx)
		if err := s.db.WithContext(finishCtx).Model(&record).Updates(map[string]any{"status": status, "failure_reason": reason, "finished_at": now}).Error; err != nil {
			return nil, errors.Join(workErr, err)
		}
		if s.audit != nil {
			if err := s.audit.RecordLinkedForNode(finishCtx, node, target.NodeName, actor.UserID, "project.cd."+operation, "project", name, actor.IP, status, id, nil); err != nil {
				return nil, errors.Join(workErr, err)
			}
		}
		return nil, workErr
	})
	if err != nil {
		release()
	}
	return result, err
}
func (s *Service) Check(ctx context.Context, node, name, trigger string, actor Actor) (database.Task, error) {
	return s.submit(ctx, node, name, "check", trigger, actor, func(ctx context.Context, record *database.ProjectCDRecord, row database.ProjectCD, report task.Reporter) error {
		now := time.Now().UTC()
		if err := s.db.WithContext(ctx).Model(&row).Update("last_check_at", now).Error; err != nil {
			return err
		}
		var repository gitrepo.Repository
		if err := json.Unmarshal([]byte(row.RepositoryJSON), &repository); err != nil {
			return err
		}
		material, _, err := s.credential(ctx, node, repository, row.CredentialCiphertext)
		if err != nil {
			return err
		}
		report(5, "Fetching Git repository")
		revision, err := s.git.Sync(ctx, gitrepo.SyncRequest{ProjectID: row.ID, Repository: repository, Credential: material}, io.Discard)
		if err != nil {
			return err
		}
		if err := s.git.Verify(ctx, revision.WorktreePath, revision.CommitSHA); err != nil {
			return err
		}
		spec, err := deployment.Spec(name, revision.WorktreePath, repository)
		if err != nil {
			return err
		}
		spec.Environment, err = deployment.DecryptEnvironment(s.secrets, row.EnvironmentCiphertext)
		if err != nil {
			return err
		}
		target, err := s.deps.Target(ctx, node)
		if err != nil {
			return err
		}
		runner := s.runner.ForTarget(target)
		report(25, "Validating Compose configuration")
		if err := runner.ValidateRelease(ctx, spec, io.Discard); err != nil {
			return errors.New("Compose validation failed; private diagnostic hidden")
		}
		rendered, err := runner.Render(ctx, spec, io.Discard)
		if err != nil {
			return errors.New("Compose rendering failed; private diagnostic hidden")
		}
		if err := deployment.ValidateTarget(spec, rendered, target.RemoteSources); err != nil {
			return fmt.Errorf("Git deployment policy rejected configuration: %s", compose.RedactEnvironment(err.Error(), spec.Environment))
		}
		var ids []uint
		if err := json.Unmarshal([]byte(row.RegistryCredentialIDsJSON), &ids); err != nil {
			return err
		}
		value := snapshot{Repository: repository, Worktree: revision.WorktreePath, Environment: spec.Environment, RegistryCredentialIDs: ids, DeploymentTimeout: row.DeploymentTimeout, GitCredentialID: row.GitCredentialID, CredentialCiphertext: row.CredentialCiphertext, RuntimeIdentity: target.RuntimeIdentity, Rendered: rendered}
		value.AutoRollback = row.AutoRollback
		cipher, err := s.encrypt(value)
		if err != nil {
			return err
		}
		record.CommitSHA = revision.CommitSHA
		record.CommitMessage = revision.CommitMessage
		record.ConfigHash = deployment.ConfigurationHash(rendered, spec.Environment)
		record.SnapshotCiphertext = cipher
		if err := s.db.WithContext(ctx).Save(record).Error; err != nil {
			return err
		}
		if err := s.db.WithContext(ctx).Model(&row).Update("candidate_record_id", record.ID).Error; err != nil {
			return err
		}
		if row.ReconcileMode == "auto" && row.AutomationEnabled && !row.InitialApplicationRequired {
			if row.FailedFingerprint == record.CommitSHA+record.ConfigHash {
				report(100, "Previous application failed; manual retry required")
				return nil
			}
			if row.AppliedRecordID != nil {
				applied, err := s.record(ctx, row, *row.AppliedRecordID)
				if err != nil {
					return err
				}
				if applied.CommitSHA == record.CommitSHA && applied.ConfigHash == record.ConfigHash {
					report(100, "Already synchronized")
					return nil
				}
			}
			apply := database.ProjectCDRecord{ProjectCDID: row.ID, Operation: "apply", Trigger: trigger, Status: "running", TaskID: record.TaskID, Actor: actor.Name, Revision: row.Revision, CommitSHA: record.CommitSHA, CommitMessage: record.CommitMessage, ConfigHash: record.ConfigHash, SnapshotCiphertext: record.SnapshotCiphertext}
			apply.SourceRecordID = &record.ID
			if err := s.db.WithContext(ctx).Create(&apply).Error; err != nil {
				return err
			}
			applyErr := s.applyWithRollback(ctx, row, &apply, report)
			finished := time.Now().UTC()
			status := "succeeded"
			reason := ""
			if applyErr != nil {
				status = "failed"
				reason = applyErr.Error()
			}
			if err := s.db.WithContext(context.WithoutCancel(ctx)).Model(&apply).Updates(map[string]any{"status": status, "failure_reason": reason, "finished_at": finished}).Error; err != nil {
				return errors.Join(applyErr, err)
			}
			if s.audit != nil {
				if err := s.audit.RecordLinkedForNode(context.WithoutCancel(ctx), node, target.NodeName, actor.UserID, "project.cd.apply", "project", name, actor.IP, status, record.TaskID, nil); err != nil {
					return errors.Join(applyErr, err)
				}
			}
			return applyErr
		}
		report(100, "Git configuration checked; ready for review")
		return nil
	})
}
func (s *Service) Apply(ctx context.Context, node, name string, id uint, revision uint64, rollback bool, actor Actor) (database.Task, error) {
	row, err := s.row(ctx, node, name)
	if err != nil {
		return database.Task{}, err
	}
	if row.Revision != revision {
		return database.Task{}, ErrConflict
	}
	if row.ReconcileMode == "observe" {
		return database.Task{}, errors.New("observe mode does not allow delivery operations")
	}
	source, err := s.record(ctx, row, id)
	if err != nil {
		return database.Task{}, err
	}
	if source.Status != "succeeded" || len(source.SnapshotCiphertext) == 0 || rollback && source.Operation == "check" {
		return database.Task{}, errors.New("choose a successful application record")
	}
	if !rollback && (source.Revision != row.Revision || row.CandidateRecordID == nil || *row.CandidateRecordID != id) {
		return database.Task{}, ErrConflict
	}
	operation := "apply"
	if rollback {
		operation = "rollback"
	}
	return s.submit(ctx, node, name, operation, "manual", actor, func(ctx context.Context, record *database.ProjectCDRecord, current database.ProjectCD, report task.Reporter) error {
		if current.Revision != revision {
			return ErrConflict
		}
		if rollback {
			if err := s.db.WithContext(ctx).Model(&current).Update("reconcile_mode", "manual").Error; err != nil {
				return err
			}
		}
		record.CommitSHA = source.CommitSHA
		record.CommitMessage = source.CommitMessage
		record.ConfigHash = source.ConfigHash
		record.SnapshotCiphertext = source.SnapshotCiphertext
		record.SourceRecordID = &source.ID
		if err := s.db.WithContext(ctx).Save(record).Error; err != nil {
			return err
		}
		if rollback {
			return s.apply(ctx, current, record, true, report)
		}
		return s.applyWithRollback(ctx, current, record, report)
	})
}
func (s *Service) apply(ctx context.Context, row database.ProjectCD, record *database.ProjectCDRecord, force bool, report task.Reporter) (result error) {
	var batchOwners int64
	if err := s.db.WithContext(ctx).Table("delivery_projects p").Where("p.id IN (SELECT project_id FROM delivery_project_nodes WHERE node_id = ? UNION SELECT project_id FROM delivery_target_states WHERE node_id = ?) AND COALESCE(NULLIF(p.deployment_name, ''), 'suma-cd-' || p.id::text) = ?", row.NodeID, row.NodeID, row.ProjectName).Count(&batchOwners).Error; err != nil {
		return err
	}
	if batchOwners > 0 {
		return errors.New("project namespace belongs to batch Continuous Delivery")
	}

	defer func() {
		if result != nil {
			if err := s.db.WithContext(context.WithoutCancel(ctx)).Model(&row).Update("failed_fingerprint", record.CommitSHA+record.ConfigHash).Error; err != nil {
				result = errors.Join(result, err)
			}
		}
	}()
	value, err := s.decode(*record)
	if err != nil {
		return err
	}
	if err := s.git.Verify(ctx, value.Worktree, record.CommitSHA); err != nil {
		return err
	}
	if _, _, err := s.credential(ctx, row.NodeID, value.Repository, value.CredentialCiphertext); err != nil {
		return err
	}
	target, err := s.deps.Target(ctx, row.NodeID)
	if err != nil {
		return err
	}
	if target.RuntimeIdentity != value.RuntimeIdentity {
		return errors.New("node runtime changed; check Git again")
	}
	spec, err := deployment.Spec(row.ProjectName, value.Worktree, value.Repository)
	if err != nil {
		return err
	}
	spec.Environment = value.Environment
	spec.ForceRecreate = force
	runner := s.runner.ForTarget(target)
	rendered, err := runner.Render(ctx, spec, io.Discard)
	if err != nil {
		return errors.New("Compose validation failed; private diagnostic hidden")
	}
	if deployment.ConfigurationHash(rendered, spec.Environment) != record.ConfigHash {
		return errors.New("snapshot content changed; check Git again")
	}
	if err := deployment.ValidateTarget(spec, rendered, target.RemoteSources); err != nil {
		return errors.New("Git deployment policy rejected configuration")
	}
	config, err := deployment.RegistryConfiguration(ctx, s.registries, row.NodeID, value.RegistryCredentialIDs)
	if err != nil {
		return err
	}
	var execute compose.Runner = runner
	if config != "" {
		execute = runner.WithDockerConfig(config)
	}

	_, err = deployment.DeployNode(ctx, execute, spec, value.DeploymentTimeout, func(stage string) (io.Writer, error) {
		switch stage {
		case "pulling":
			report(45, "Pulling declared images")
		case "deploying":
			report(70, "Applying Compose configuration")
		case "verifying":
			report(90, "Checking service health")
		}
		return io.Discard, nil
	}, func(ctx context.Context) error {
		current, err := s.deps.Target(ctx, row.NodeID)
		if err != nil {
			return err
		}
		if current.RuntimeIdentity != target.RuntimeIdentity {
			return errors.New("node runtime changed during image pull")
		}
		return nil
	})
	if err != nil {
		return errDeploymentFailed
	}
	// Commit the successful evidence and active pointer together. A restart
	// cannot leave an interrupted record presented as the last successful apply.
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(record).Updates(map[string]any{"status": "succeeded", "finished_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
		return tx.Model(&row).Updates(map[string]any{"applied_record_id": record.ID, "initial_application_required": false, "failed_fingerprint": ""}).Error
	}); err != nil {
		return err
	}
	report(100, "Git configuration applied")
	return nil
}

func (s *Service) Lifecycle(ctx context.Context, node, name, action string) (database.Task, error) {
	return s.submit(ctx, node, name, action, "manual", Actor{Name: "Project operator"}, func(ctx context.Context, record *database.ProjectCDRecord, row database.ProjectCD, report task.Reporter) error {
		project, err := s.deps.Project(ctx, node)
		if err != nil {
			return err
		}
		ordinary, err := project.ManagedConfiguration(name)
		if err != nil {
			return err
		}
		spec := compose.ExecutionSpec{ProjectName: name, ProjectDir: ordinary.Path}
		if row.AppliedRecordID != nil {
			active, err := s.record(ctx, row, *row.AppliedRecordID)
			if err != nil {
				return err
			}
			value, err := s.decode(active)
			if err != nil {
				return err
			}
			spec, err = deployment.Spec(name, value.Worktree, value.Repository)
			if err != nil {
				return err
			}
			spec.Environment = value.Environment
		}
		target, err := s.deps.Target(ctx, node)
		if err != nil {
			return err
		}
		report(10, "Applying runtime lifecycle action")
		if err := s.runner.ForTarget(target).ProjectLifecycle(ctx, spec, action); err != nil {
			return errors.New("project lifecycle action failed; private diagnostic hidden")
		}
		report(100, "Runtime action completed; Git configuration retained")
		return nil
	})
}
