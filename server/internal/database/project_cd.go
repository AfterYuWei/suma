package database

import "time"

// ProjectCD stores a Project's single-node CD deployment mode; batch orchestration stays in DeliveryProject.
type ProjectCD struct {
	ID                         uint       `gorm:"primaryKey" json:"id"`
	NodeID                     string     `gorm:"size:64;uniqueIndex:idx_project_cd_identity;not null" json:"node_id"`
	ProjectName                string     `gorm:"size:128;uniqueIndex:idx_project_cd_identity;not null" json:"project_name"`
	Enabled                    bool       `json:"enabled"`
	Revision                   uint64     `json:"revision"`
	RepositoryJSON             string     `json:"-"`
	GitCredentialID            *uint      `json:"-"`
	CredentialCiphertext       []byte     `json:"-"`
	EnvironmentCiphertext      []byte     `json:"-"`
	RegistryCredentialIDsJSON  string     `json:"-"`
	DeploymentTimeout          int        `json:"deployment_timeout"`
	AutomationEnabled          bool       `json:"automation_enabled"`
	ReconcileMode              string     `gorm:"default:manual" json:"reconcile_mode"`
	AutoRollback               bool       `json:"auto_rollback"`
	InitialApplicationRequired bool       `json:"initial_application_required"`
	SyncIntervalSeconds        int        `json:"sync_interval_seconds"`
	WebhookEnabled             bool       `json:"webhook_enabled"`
	WebhookID                  string     `json:"webhook_id"`
	WebhookSecret              []byte     `json:"-"`
	CandidateRecordID          *uint      `json:"candidate_record_id"`
	AppliedRecordID            *uint      `json:"applied_record_id"`
	FailedFingerprint          string     `json:"-"`
	LastCheckAt                *time.Time `json:"last_check_at"`
	CreatedAt                  time.Time  `json:"created_at"`
	UpdatedAt                  time.Time  `json:"updated_at"`
}

type ProjectCDRecord struct {
	ID                 uint       `gorm:"primaryKey" json:"id"`
	ProjectCDID        uint       `gorm:"index;not null" json:"project_cd_id"`
	SourceRecordID     *uint      `json:"source_record_id,omitempty"`
	Operation          string     `json:"operation"`
	Trigger            string     `json:"trigger"`
	Status             string     `json:"status"`
	TaskID             string     `json:"task_id"`
	Actor              string     `json:"actor"`
	Revision           uint64     `json:"revision"`
	CommitSHA          string     `json:"commit_sha"`
	CommitMessage      string     `json:"commit_message"`
	ConfigHash         string     `json:"config_hash"`
	SnapshotCiphertext []byte     `json:"-"`
	FailureReason      string     `json:"failure_reason"`
	CreatedAt          time.Time  `json:"created_at"`
	FinishedAt         *time.Time `json:"finished_at"`
}
