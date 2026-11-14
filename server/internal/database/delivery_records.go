package database

import "time"

// DeliveryRecord owns immutable operation settings. Validation adds their
// fingerprint; execution updates progress and outcome. Release owns Git identity.
type DeliveryRecord struct {
	FrozenInputCiphertext     []byte                       `json:"-"`
	ImagesPinned              bool                         `json:"images_pinned"`
	ID                        uint                         `gorm:"primaryKey" json:"id"`
	ProjectID                 uint                         `gorm:"index;not null" json:"project_id"`
	ReleaseID                 uint                         `gorm:"index;not null" json:"release_id"`
	RestoreRecordsJSON        string                       `json:"-"`
	SourceRecordID            *uint                        `gorm:"index" json:"source_record_id,omitempty"`
	LegacyAttemptID           *uint                        `json:"legacy_attempt_id,omitempty"`
	LegacyReleaseID           *uint                        `json:"legacy_release_id,omitempty"`
	Operation                 string                       `gorm:"size:32;not null" json:"operation"`
	ActorIP                   string                       `gorm:"-" json:"-"`
	ActorID                   *uint                        `json:"actor_id,omitempty"`
	ActorName                 string                       `json:"actor_name"`
	TriggerType               string                       `json:"trigger_type"`
	TaskID                    string                       `gorm:"size:36;index" json:"task_id,omitempty"`
	Status                    string                       `gorm:"size:32;index" json:"status"`
	FailureReason             string                       `json:"failure_reason,omitempty"`
	HealthSummary             string                       `json:"health_summary,omitempty"`
	SnapshotComplete          bool                         `json:"snapshot_complete"`
	SnapshotNote              string                       `json:"snapshot_note,omitempty"`
	ConfigHash                string                       `json:"config_hash"`
	ImageReferences           string                       `json:"image_references"`
	ComposeFilesJSON          string                       `json:"compose_files"`
	EnvironmentFile           string                       `json:"environment_file"`
	EnvironmentCiphertext     []byte                       `json:"-"`
	WorktreePath              string                       `json:"-"`
	NodeIDsJSON               string                       `json:"-"`
	RegistryCredentialIDsJSON string                       `json:"-"`
	GitCredentialID           *uint                        `json:"git_credential_id,omitempty"`
	DeploymentTimeout         int                          `json:"deployment_timeout"`
	AutoRollback              bool                         `json:"auto_rollback"`
	StartedAt                 *time.Time                   `json:"started_at,omitempty"`
	FinishedAt                *time.Time                   `json:"finished_at,omitempty"`
	CreatedAt                 time.Time                    `json:"created_at"`
	UpdatedAt                 time.Time                    `json:"updated_at"`
	CommitSHA                 string                       `gorm:"-" json:"commit_sha"`
	CommitMessage             string                       `gorm:"-" json:"commit_message"`
	NodeIDs                   []string                     `gorm:"-" json:"node_ids"`
	RegistryCredentialIDs     []uint                       `gorm:"-" json:"registry_credential_ids"`
	Environment               []DeliveryEnvironmentSummary `gorm:"-" json:"environment"`
	Attempts                  []DeliveryDeploymentAttempt  `gorm:"-" json:"attempts"`
}

type DeliveryEnvironmentSummary struct {
	Name     string `json:"name"`
	HasValue bool   `json:"has_value"`
}
