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

type ComposeProject struct {
	ID        uint   `gorm:"primaryKey"`
	NodeID    string `gorm:"size:64;not null;default:local;index"`
	Name      string `gorm:"uniqueIndex;size:128;not null"`
	Path      string `gorm:"not null"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Task struct {
	ID         string     `gorm:"primaryKey;size:36" json:"id"`
	NodeID     string     `gorm:"size:64;not null;default:local;index" json:"node_id"`
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

type AuditLog struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	NodeID       string    `gorm:"size:64;not null;default:local;index" json:"node_id"`
	UserID       *uint     `gorm:"index" json:"user_id,omitempty"`
	Action       string    `gorm:"size:64;not null;index" json:"action"`
	ResourceType string    `gorm:"size:64" json:"resource_type"`
	ResourceName string    `json:"resource_name"`
	IP           string    `gorm:"size:64" json:"ip"`
	Result       string    `gorm:"size:16;not null" json:"result"`
	CreatedAt    time.Time `json:"created_at"`
}

type LoginLog struct {
	ID        uint   `gorm:"primaryKey"`
	Username  string `gorm:"size:64;not null"`
	IP        string `gorm:"size:64"`
	Success   bool
	CreatedAt time.Time
}
