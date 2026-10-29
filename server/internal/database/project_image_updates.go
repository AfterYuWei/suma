package database

import "time"

// ProjectImageUpdate stores SUMA policy and historical observations, never live Docker state.
type ProjectImageUpdate struct {
	NodeID        string     `gorm:"primaryKey;size:64" json:"-"`
	Project       string     `gorm:"primaryKey;size:128" json:"project"`
	Version       uint64     `json:"version"`
	Mode          string     `json:"mode"`
	Cron          string     `json:"cron"`
	Timezone      string     `json:"timezone"`
	NextRunAt     *time.Time `json:"next_run_at,omitempty"`
	LastCheckedAt *time.Time `json:"last_checked_at,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
	ResultsJSON   string     `json:"-"`
	RuntimeKey    string     `json:"-"`
}

type ProjectImageCandidate struct {
	ID                   string    `gorm:"primaryKey;size:64" json:"id"`
	NodeID               string    `gorm:"index;size:64" json:"node_id"`
	Project              string    `gorm:"size:128" json:"project"`
	RequiresDockerSocket bool      `json:"requires_docker_socket"`
	Fingerprint          string    `json:"-"`
	Revision             string    `json:"-"`
	RuntimeKey           string    `json:"-"`
	ServicesJSON         string    `json:"-"`
	Status               string    `json:"status"`
	TaskID               string    `json:"task_id,omitempty"`
	ActorJSON            string    `json:"-"`
	Message              string    `json:"message,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type ProjectImageAction struct {
	State              string
	TokenHash          string `gorm:"primaryKey;size:64"`
	CandidateID        string `gorm:"size:64;index"`
	DeliveryID         string `gorm:"size:64;index"`
	ChannelID          string `gorm:"size:64"`
	ChatID             string `gorm:"size:128"`
	DestinationVersion uint64
	CreatedAt          time.Time
}
