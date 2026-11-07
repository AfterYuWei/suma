package database

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// JSONB stores a raw JSON document in a jsonb column. It persists through a
// text value and re-emits the document verbatim in API responses so nested
// objects never double-encode.
type JSONB json.RawMessage

// Value implements driver.Valuer.
func (j JSONB) Value() (driver.Value, error) {
	if len(j) == 0 {
		return nil, nil
	}
	if !json.Valid(j) {
		return nil, errors.New("invalid JSON document")
	}
	return string(j), nil
}

// Scan implements sql.Scanner.
func (j *JSONB) Scan(src any) error {
	if src == nil {
		*j = nil
		return nil
	}
	switch value := src.(type) {
	case []byte:
		*j = append((*j)[:0], value...)
		return nil
	case string:
		*j = append((*j)[:0], value...)
		return nil
	default:
		return fmt.Errorf("scan jsonb column: unsupported source %T", src)
	}
}

// MarshalJSON re-emits the stored document verbatim; an empty value is null.
func (j JSONB) MarshalJSON() ([]byte, error) {
	if len(j) == 0 {
		return []byte("null"), nil
	}
	return j, nil
}

// UnmarshalJSON stores any JSON document verbatim.
func (j *JSONB) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*j = nil
		return nil
	}
	*j = append((*j)[:0], data...)
	return nil
}

// TaskCenterSchedule is a generic scheduled-task plan keyed by task kind. The
// Task Center owns the scheduler; features register kinds and write plans.
// An empty Subject is allowed only for kinds that keep a single plan per node.
type TaskCenterSchedule struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	Name      string `gorm:"size:120;not null" json:"name"`
	Kind      string `gorm:"size:64;not null;index" json:"kind"`
	NodeID    string `gorm:"size:64;not null;default:'';uniqueIndex:idx_task_center_schedule_target" json:"node_id,omitempty"`
	NodeName  string `gorm:"size:128;not null;default:''" json:"node_name,omitempty"`
	Subject   string `gorm:"size:200;not null;default:'';uniqueIndex:idx_task_center_schedule_target" json:"subject"`
	Params    JSONB  `gorm:"type:jsonb" json:"params"`
	Cron      string `gorm:"size:64;not null" json:"cron"`
	Timezone  string `gorm:"size:64;not null" json:"timezone"`
	Enabled   bool   `gorm:"not null;default:true" json:"enabled"`
	Version   uint64 `gorm:"not null;default:1" json:"version"`
	NextRunAt *time.Time `json:"next_run_at,omitempty"`
	LastRunAt *time.Time `json:"last_run_at,omitempty"`
	LastError string `json:"last_error,omitempty"`
	CreatedBy string `gorm:"size:120;not null;default:''" json:"created_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TaskCenterScheduleRun records every dispatch attempt of a plan, including
// failures to start the task (empty TaskID plus Error). Task state itself
// always lives in tasks.
type TaskCenterScheduleRun struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	ScheduleID  uint      `gorm:"index;not null" json:"schedule_id"`
	TaskID      string    `gorm:"size:36;not null;default:''" json:"task_id"`
	TriggeredBy string    `gorm:"size:16;not null;default:scheduled" json:"triggered_by"`
	Error       string    `json:"error,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// TaskCenterArtifact holds the structured result of one finished task run, so
// views can render the latest outcome of a kind without replaying task logs.
type TaskCenterArtifact struct {
	TaskID    string    `gorm:"primaryKey;size:36" json:"task_id"`
	Kind      string    `gorm:"size:64;not null;index" json:"kind"`
	NodeID    string    `gorm:"size:64;not null;default:'';index" json:"node_id,omitempty"`
	Subject   string    `gorm:"size:200;not null;default:''" json:"subject"`
	Data      JSONB     `gorm:"type:jsonb" json:"data"`
	CreatedAt time.Time `json:"created_at"`
}
