package database

import "time"

type User struct {
	ID           uint   `gorm:"primaryKey"`
	Username     string `gorm:"uniqueIndex;size:64;not null"`
	PasswordHash string `gorm:"not null"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Session struct {
	ID        uint      `gorm:"primaryKey"`
	TokenHash string    `gorm:"uniqueIndex;size:64;not null"`
	UserID    uint      `gorm:"index;not null"`
	User      User      `gorm:"constraint:OnDelete:CASCADE"`
	ExpiresAt time.Time `gorm:"index;not null"`
	CreatedAt time.Time
}

type Setting struct {
	Key       string `gorm:"primaryKey;size:128"`
	Value     string `gorm:"not null"`
	UpdatedAt time.Time
}

// Node is a Docker Engine endpoint managed by this SUMA control plane.
// Runtime Docker state is never persisted here; the status fields only record
// the result of the most recent connectivity probe.
type Node struct {
	ID                   string     `gorm:"primaryKey;size:64" json:"id"`
	Name                 string     `gorm:"uniqueIndex;size:128;not null" json:"name"`
	ConnectionType       string     `gorm:"size:16;not null" json:"connection_type"`
	Endpoint             string     `gorm:"size:1024;not null" json:"endpoint"`
	TLSMode              string     `gorm:"size:16;not null;default:required" json:"tls_mode"`
	TLSCredentialID      *uint      `gorm:"index" json:"tls_credential_id,omitempty"`
	AllowedBindRootsJSON string     `gorm:"not null;default:'[]'" json:"-"`
	Enabled              bool       `gorm:"not null;default:true" json:"enabled"`
	EngineID             string     `gorm:"size:128;index" json:"engine_id,omitempty"`
	EngineVersion        string     `gorm:"size:64" json:"engine_version,omitempty"`
	Status               string     `gorm:"size:16;not null;default:unknown" json:"status"`
	LastError            string     `json:"last_error,omitempty"`
	LastLatencyMS        int64      `json:"last_latency_ms,omitempty"`
	LastCheckedAt        *time.Time `json:"last_checked_at,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

type DockerTLSCredential struct {
	ID                    uint       `gorm:"primaryKey" json:"id"`
	Name                  string     `gorm:"uniqueIndex;size:128;not null" json:"name"`
	CACiphertext          []byte     `json:"-"`
	CertificateCiphertext []byte     `json:"-"`
	PrivateKeyCiphertext  []byte     `json:"-"`
	Fingerprint           string     `gorm:"size:64" json:"fingerprint"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
	LastUsedAt            *time.Time `json:"last_used_at,omitempty"`
}

type DockerTLSCredentialNode struct {
	CredentialID uint   `gorm:"primaryKey"`
	NodeID       string `gorm:"primaryKey;size:64"`
}

type GitCredentialNode struct {
	CredentialID uint   `gorm:"primaryKey"`
	NodeID       string `gorm:"primaryKey;size:64"`
}

type RegistryCredentialNode struct {
	CredentialID uint   `gorm:"primaryKey"`
	NodeID       string `gorm:"primaryKey;size:64"`
}

type DeliveryProjectNode struct {
	ProjectID uint      `gorm:"primaryKey" json:"project_id"`
	NodeID    string    `gorm:"primaryKey;size:64" json:"node_id"`
	CreatedAt time.Time `json:"created_at"`
}

type DeliveryProjectRegistryCredential struct {
	ProjectID    uint `gorm:"primaryKey"`
	CredentialID uint `gorm:"primaryKey"`
}

type DeliveryReleaseDeployment struct {
	ID                uint       `gorm:"primaryKey" json:"id"`
	ReleaseID         uint       `gorm:"index;not null" json:"release_id"`
	NodeID            string     `gorm:"size:64;index;not null" json:"node_id"`
	NodeName          string     `gorm:"size:128;not null" json:"node_name"`
	TaskID            string     `gorm:"size:36;index" json:"task_id,omitempty"`
	Status            string     `gorm:"size:32;index;not null" json:"status"`
	PreviousReleaseID *uint      `gorm:"index" json:"previous_release_id,omitempty"`
	FailureReason     string     `json:"failure_reason,omitempty"`
	RollbackResult    string     `gorm:"size:32" json:"rollback_result,omitempty"`
	HealthSummary     string     `json:"health_summary,omitempty"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	FinishedAt        *time.Time `json:"finished_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type DeliveryTargetState struct {
	ProjectID       uint      `gorm:"primaryKey" json:"project_id"`
	NodeID          string    `gorm:"primaryKey;size:64" json:"node_id"`
	ActiveReleaseID *uint     `gorm:"index" json:"active_release_id,omitempty"`
	ObservedCommit  string    `gorm:"size:64" json:"observed_commit,omitempty"`
	HealthSummary   string    `json:"health_summary,omitempty"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// DeliveryProject is the aggregate root for continuous delivery. It is
// intentionally independent from the Docker-label Compose inventory: Compose
// is a deployment adapter used by a release, not the identity or lifecycle owner of CD.
type DeliveryProject struct {
	ID                  uint   `gorm:"primaryKey"`
	NodeID              string `gorm:"size:64;not null;default:local;index"`
	Name                string `gorm:"uniqueIndex;size:128;not null"`
	DeploymentName      string `gorm:"size:128;not null"`
	GitCloneURL         string
	GitCredentialID     *uint  `gorm:"index"`
	GitRefType          string `gorm:"size:16"`
	GitRef              string
	ComposeFilesJSON    string
	EnvironmentFile     string
	ReconcileMode       string `gorm:"size:16;not null;default:manual"`
	SyncIntervalSeconds int    `gorm:"not null;default:300"`
	DesiredCommit       string `gorm:"size:64"`
	ObservedCommit      string `gorm:"size:64"`
	ActiveReleaseID     *uint  `gorm:"index"`
	AutoRollback        bool
	DeploymentTimeout   int    `gorm:"not null;default:120"`
	WebhookID           string `gorm:"size:64;uniqueIndex:idx_delivery_webhook_id,where:webhook_id <> ''"`
	WebhookSecret       []byte `json:"-"`
	WebhookEnabled      bool
	LastSyncAt          *time.Time `gorm:"index"`
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type GitCredential struct {
	ID                   uint       `gorm:"primaryKey" json:"id"`
	Name                 string     `gorm:"uniqueIndex;size:128;not null" json:"name"`
	AuthType             string     `gorm:"size:32;not null" json:"auth_type"`
	Username             string     `gorm:"size:256" json:"username,omitempty"`
	SecretCiphertext     []byte     `json:"-"`
	PrivateKeyCiphertext []byte     `json:"-"`
	PassphraseCiphertext []byte     `json:"-"`
	KnownHostsCiphertext []byte     `json:"-"`
	CACiphertext         []byte     `json:"-"`
	Fingerprint          string     `gorm:"size:64" json:"fingerprint,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
	LastUsedAt           *time.Time `json:"last_used_at,omitempty"`
	UsedBy               int64      `gorm:"-" json:"used_by"`
	AuthorizedNodeIDs    []string   `gorm:"-" json:"authorized_node_ids"`
}

type DeliveryProjectGitCredential struct {
	ID                   uint      `gorm:"primaryKey" json:"-"`
	ProjectID            uint      `gorm:"uniqueIndex;not null" json:"-"`
	Name                 string    `gorm:"size:128;not null" json:"name"`
	AuthType             string    `gorm:"size:32;not null" json:"auth_type"`
	Username             string    `gorm:"size:256" json:"username,omitempty"`
	SecretCiphertext     []byte    `json:"-"`
	PrivateKeyCiphertext []byte    `json:"-"`
	PassphraseCiphertext []byte    `json:"-"`
	KnownHostsCiphertext []byte    `json:"-"`
	CACiphertext         []byte    `json:"-"`
	Fingerprint          string    `gorm:"size:64" json:"fingerprint,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type RegistryCredential struct {
	ID                uint       `gorm:"primaryKey" json:"id"`
	Name              string     `gorm:"uniqueIndex;size:128;not null" json:"name"`
	ServerAddress     string     `gorm:"size:512;not null" json:"server_address"`
	AuthType          string     `gorm:"size:32;not null" json:"auth_type"`
	Username          string     `gorm:"size:256" json:"username,omitempty"`
	SecretCiphertext  []byte     `json:"-"`
	Fingerprint       string     `gorm:"size:64" json:"fingerprint,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	LastUsedAt        *time.Time `json:"last_used_at,omitempty"`
	AuthorizedNodeIDs []string   `gorm:"-" json:"authorized_node_ids"`
}

type DeliveryRelease struct {
	ID                uint                        `gorm:"primaryKey" json:"id"`
	ProjectID         uint                        `gorm:"index;not null" json:"project_id"`
	RepositoryURL     string                      `json:"repository_url"`
	GitRef            string                      `json:"git_ref"`
	CommitSHA         string                      `gorm:"size:64;index;not null" json:"commit_sha"`
	CommitMessage     string                      `json:"commit_message"`
	CommitAuthor      string                      `json:"commit_author"`
	ConfigHash        string                      `gorm:"size:64;index" json:"config_hash"`
	ImageReferences   string                      `json:"image_references"`
	TaskID            string                      `gorm:"size:36;index" json:"task_id,omitempty"`
	Status            string                      `gorm:"size:32;index;not null" json:"status"`
	TriggerType       string                      `gorm:"size:32;not null" json:"trigger_type"`
	TriggerActor      string                      `json:"trigger_actor"`
	PreviousReleaseID *uint                       `gorm:"index" json:"previous_release_id,omitempty"`
	WorktreePath      string                      `json:"-"`
	ComposeFilesJSON  string                      `json:"compose_files"`
	EnvironmentFile   string                      `json:"environment_file,omitempty"`
	ApprovedBy        *uint                       `gorm:"index" json:"approved_by,omitempty"`
	ApprovedAt        *time.Time                  `json:"approved_at,omitempty"`
	StartedAt         *time.Time                  `json:"started_at,omitempty"`
	FinishedAt        *time.Time                  `json:"finished_at,omitempty"`
	FailureReason     string                      `json:"failure_reason,omitempty"`
	HealthSummary     string                      `json:"health_summary,omitempty"`
	CreatedAt         time.Time                   `json:"created_at"`
	UpdatedAt         time.Time                   `json:"updated_at"`
	Deployments       []DeliveryReleaseDeployment `gorm:"-" json:"deployments,omitempty"`
}

type GitWebhookDelivery struct {
	ID            uint   `gorm:"primaryKey"`
	Format        string `gorm:"size:16;not null"`
	HookID        string `gorm:"size:64;not null;index:idx_hook_delivery,unique"`
	DeliveryID    string `gorm:"size:128;not null;index:idx_hook_delivery,unique"`
	Event         string `gorm:"size:64"`
	Repository    string
	GitRef        string
	CommitSHA     string `gorm:"size:64"`
	Status        string `gorm:"size:32;not null"`
	FailureReason string
	CreatedAt     time.Time `gorm:"index"`
	ProcessedAt   *time.Time
}

type Task struct {
	ID         string     `gorm:"primaryKey;size:36" json:"id"`
	NodeID     string     `gorm:"size:64;not null;default:local;index" json:"node_id"`
	NodeName   string     `gorm:"size:128" json:"node_name,omitempty"`
	Type       string     `gorm:"size:64;not null" json:"type"`
	Name       string     `gorm:"not null" json:"name"`
	Status     string     `gorm:"size:16;not null;index" json:"status"`
	Progress   int        `gorm:"not null;default:0" json:"progress"`
	Message    string     `json:"message"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

type TaskLog struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	TaskID    string    `gorm:"size:36;not null;index" json:"task_id"`
	Level     string    `gorm:"size:16;not null;default:info" json:"level"`
	Message   string    `gorm:"not null" json:"message"`
	CreatedAt time.Time `gorm:"index" json:"created_at"`
}

type TaskStep struct {
	TaskID    string    `gorm:"primaryKey;size:36" json:"-"`
	StepID    string    `gorm:"primaryKey;size:128" json:"id"`
	Status    string    `gorm:"size:128;not null" json:"status"`
	Current   int64     `gorm:"not null;default:0" json:"current"`
	Total     int64     `gorm:"not null;default:0" json:"total"`
	Progress  int       `gorm:"not null;default:0" json:"progress"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type AuditLog struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	NodeID       string    `gorm:"size:64;not null;default:local;index" json:"node_id"`
	NodeName     string    `gorm:"size:128" json:"node_name,omitempty"`
	UserID       *uint     `gorm:"index" json:"user_id,omitempty"`
	Action       string    `gorm:"size:64;not null;index" json:"action"`
	ResourceType string    `gorm:"size:64" json:"resource_type"`
	ResourceName string    `json:"resource_name"`
	IP           string    `gorm:"size:64" json:"ip"`
	Result       string    `gorm:"size:16;not null" json:"result"`
	TaskID       string    `gorm:"size:36;index" json:"task_id,omitempty"`
	ReleaseID    *uint     `gorm:"index" json:"release_id,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type LoginLog struct {
	ID        uint   `gorm:"primaryKey"`
	Username  string `gorm:"size:64;not null"`
	IP        string `gorm:"size:64"`
	Success   bool
	CreatedAt time.Time
}
