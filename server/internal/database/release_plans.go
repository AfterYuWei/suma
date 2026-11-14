package database

import "time"

// ReleasePlan is an expiring, encrypted confirmation input. Execution evidence
// remains in the owning CD records; TaskID is claimed with Task creation.
type ReleasePlan struct {
	ID                string    `gorm:"primaryKey;size:32" json:"id"`
	Kind              string    `gorm:"size:16;index;not null" json:"-"`
	OwnerID           uint      `gorm:"index;not null" json:"-"`
	Version           string    `json:"-"`
	PayloadCiphertext []byte    `json:"-"`
	PreviewJSON       string    `json:"-"`
	PreparationTaskID string    `json:"preparation_task_id"`
	TaskID            string    `gorm:"index" json:"task_id,omitempty"`
	RecordID          *uint     `json:"record_id,omitempty"`
	ExpiresAt         time.Time `json:"expires_at"`
	CreatedAt         time.Time `json:"created_at"`
}
