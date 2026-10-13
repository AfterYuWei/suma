package database

import "time"

type MonitoringPolicy struct {
	ID             int    `gorm:"primaryKey" json:"-"`
	PolicyJSON     string `gorm:"type:jsonb" json:"-"`
	Revision       int64  `json:"revision"`
	CleanupPending bool
	CleanupTaskID  string
	UpdatedAt      time.Time
}
type MonitoringInstance struct {
	ID          string    `gorm:"primaryKey;size:64" json:"id"`
	NodeID      string    `json:"node_id"`
	EngineID    string    `json:"engine_id"`
	ContainerID string    `json:"container_id"`
	NodeName    string    `json:"node_name"`
	Name        string    `json:"name"`
	Image       string    `json:"image"`
	Project     string    `json:"project"`
	Service     string    `json:"service"`
	FirstSeen   time.Time `json:"first_seen"`
	LastSeen    time.Time `json:"last_seen"`
}
type MonitoringSample struct {
	InstanceID      string    `gorm:"primaryKey" json:"instance_id"`
	RecordedAt      time.Time `gorm:"primaryKey" json:"recorded_at"`
	SampledAt       time.Time `json:"sampled_at"`
	StartedAt       string    `json:"started_at"`
	Session         string    `json:"session"`
	IntervalSeconds int       `json:"interval_seconds"`
	CPU             *float64  `json:"cpu"`
	Memory          *float64  `json:"memory"`
	MemoryLimit     *float64  `json:"memory_limit"`
	NetworkRX       *float64  `json:"network_rx"`
	NetworkTX       *float64  `json:"network_tx"`
	BlockRead       *float64  `json:"block_read"`
	BlockWrite      *float64  `json:"block_write"`
	PIDs            *float64  `gorm:"column:pids" json:"pids"`
	CountersJSON    string    `gorm:"type:jsonb" json:"-"`
}
type MonitoringEvent struct {
	ID           string    `gorm:"primaryKey" json:"id"`
	InstanceID   string    `json:"instance_id"`
	Time         time.Time `json:"time"`
	TimeNano     string    `json:"time_nano"`
	Action       string    `json:"action"`
	HealthStatus string    `json:"health_status,omitempty"`
	ExitCode     *int      `json:"exit_code"`
	Signal       string    `json:"signal,omitempty"`
}
type MonitoringGap struct {
	ID         uint64    `gorm:"primaryKey" json:"id"`
	NodeID     string    `json:"node_id"`
	EngineID   string    `json:"engine_id"`
	InstanceID string    `json:"instance_id,omitempty"`
	Since      time.Time `json:"since"`
	Until      time.Time `json:"until"`
	Reason     string    `json:"reason"`
}
type MonitoringCheckpoint struct {
	NodeID   string `gorm:"primaryKey"`
	EngineID string `gorm:"primaryKey"`
	Since    time.Time
}
