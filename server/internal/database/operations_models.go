package database

import "time"

type NotificationChannel struct {
	ID               string     `gorm:"primaryKey;size:64" json:"id"`
	Name             string     `json:"name"`
	Provider         string     `json:"provider"`
	Enabled          bool       `json:"enabled"`
	Version          uint64     `json:"version"`
	ConfigJSON       string     `json:"-"`
	SecretCiphertext []byte     `json:"-"`
	LastError        string     `json:"last_error,omitempty"`
	LastSentAt       *time.Time `json:"last_sent_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}
type NotificationRule struct {
	ID         string    `gorm:"primaryKey;size:64" json:"id"`
	Name       string    `json:"name"`
	Enabled    bool      `json:"enabled"`
	Version    uint64    `json:"version"`
	ConfigJSON string    `json:"-"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// These rows are historical notification evidence, not current Docker state.
type NotificationEvent struct {
	ID        string    `gorm:"primaryKey;size:64" json:"id"`
	Type      string    `gorm:"index" json:"type"`
	Severity  string    `json:"severity"`
	NodeID    string    `gorm:"index" json:"node_id,omitempty"`
	DedupeKey string    `gorm:"index" json:"-"`
	DataJSON  string    `json:"-"`
	CreatedAt time.Time `gorm:"index" json:"created_at"`
}
type NotificationRead struct {
	UserID  uint   `gorm:"primaryKey"`
	EventID string `gorm:"primaryKey;size:64"`
	ReadAt  time.Time
}
type NotificationDelivery struct {
	ID                string     `gorm:"primaryKey;size:64" json:"id"`
	ChannelID         string     `gorm:"index" json:"channel_id"`
	RuleID            string     `json:"rule_id,omitempty"`
	BatchKey          string     `gorm:"index" json:"-"`
	EventIDsJSON      string     `json:"-"`
	Status            string     `gorm:"index" json:"status"`
	Reason            string     `json:"reason,omitempty"`
	Attempts          int        `json:"attempts"`
	DueAt             time.Time  `gorm:"index" json:"due_at"`
	LeaseUntil        *time.Time `gorm:"index" json:"-"`
	ProviderMessageID string     `json:"provider_message_id,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}
type NotificationBinding struct {
	ID             string    `gorm:"primaryKey;size:64" json:"id"`
	ChannelID      string    `gorm:"index" json:"channel_id"`
	UserID         uint      `gorm:"index" json:"user_id"`
	CodeHash       string    `gorm:"uniqueIndex;size:64" json:"-"`
	ExternalUserID string    `gorm:"index" json:"external_user_id,omitempty"`
	ExternalName   string    `json:"external_name,omitempty"`
	ChatID         string    `json:"chat_id,omitempty"`
	Status         string    `gorm:"index" json:"status"`
	ExpiresAt      time.Time `json:"expires_at"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}
type NotificationIncoming struct {
	Key       string    `gorm:"primaryKey;size:256"`
	CreatedAt time.Time `gorm:"index"`
}
type NotificationChat struct {
	ChannelID string    `gorm:"primaryKey;size:64" json:"channel_id"`
	ChatID    string    `gorm:"primaryKey;size:128" json:"chat_id"`
	Name      string    `json:"name"`
	Private   bool      `json:"private"`
	UpdatedAt time.Time `json:"updated_at"`
}
type NotificationAction struct {
	TokenHash   string `gorm:"primaryKey;size:64" json:"-"`
	OperationID string `gorm:"index"`
	BindingID   string
	ChannelID   string
	ChatID      string
	ExpiresAt   time.Time `gorm:"index"`
	ConsumedAt  *time.Time
}
type AIRun struct {
	TaskProgress   int        `gorm:"not null;default:0" json:"task_progress"`
	TaskStatus     string     `json:"task_status"`
	PredecessorID  string     `gorm:"size:64" json:"-"`
	ConversationID string     `gorm:"size:64;index" json:"conversation_id"`
	Revision       uint64     `gorm:"not null;default:1" json:"revision"`
	Phase          string     `gorm:"size:32" json:"phase"`
	TargetSource   string     `gorm:"size:32" json:"target_source"`
	ActorJSON      string     `gorm:"type:jsonb;not null;default:'{}'" json:"-"`
	InputJSON      string     `gorm:"type:jsonb;not null;default:'{}'" json:"-"`
	ResumeJSON     string     `gorm:"type:jsonb;not null;default:'{}'" json:"-"`
	ResumeTarget   string     `json:"-"`
	ConfigHash     string     `json:"-"`
	Iterations     int        `gorm:"not null;default:0" json:"iterations"`
	Reads          int        `gorm:"not null;default:0" json:"reads"`
	Operations     int        `gorm:"not null;default:0" json:"operations_count"`
	LeaseOwner     string     `gorm:"size:64" json:"-"`
	LeaseUntil     *time.Time `gorm:"index" json:"-"`
	ID             string     `gorm:"primaryKey;size:64" json:"id"`
	UserID         uint       `gorm:"index" json:"user_id"`
	NodeID         string     `gorm:"index" json:"node_id"`
	Model          string     `json:"model"`
	NodeIDsJSON    string     `gorm:"type:jsonb;not null;default:'[]'" json:"-"`
	Source         string     `json:"source"`
	EventID        string     `gorm:"index" json:"event_id,omitempty"`
	Question       string     `json:"question"`
	Status         string     `gorm:"index" json:"status"`
	ResultJSON     string     `gorm:"type:jsonb;not null;default:'{}'" json:"-"`
	Error          string     `json:"error,omitempty"`
	Tokens         int        `json:"tokens"`
	CreatedAt      time.Time  `gorm:"index" json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}
type AIOperation struct {
	StepID             string    `gorm:"size:64;index" json:"step_id"`
	ConfirmationsJSON  string    `gorm:"type:jsonb;not null;default:'[]'" json:"-"`
	ConfirmedJSON      string    `gorm:"type:jsonb;not null;default:'{}'" json:"-"`
	SnapshotCiphertext []byte    `json:"-"`
	DecisionKey        string    `gorm:"size:64" json:"-"`
	DecisionHash       string    `gorm:"size:64" json:"-"`
	VerificationJSON   string    `gorm:"type:jsonb;not null;default:'{}'" json:"-"`
	ID                 string    `gorm:"primaryKey;size:64" json:"id"`
	RunID              string    `gorm:"index" json:"run_id"`
	RequestedBy        uint      `json:"requested_by"`
	ApprovedBy         *uint     `json:"approved_by,omitempty"`
	NodeID             string    `gorm:"index" json:"node_id"`
	Action             string    `json:"action"`
	ResourceID         string    `json:"resource_id"`
	Title              string    `json:"title"`
	Impact             string    `json:"impact"`
	ParametersJSON     string    `gorm:"type:jsonb;not null;default:'{}'" json:"-"`
	SnapshotJSON       string    `gorm:"type:jsonb;not null;default:'{}'" json:"-"`
	SnapshotHash       string    `json:"-"`
	Status             string    `gorm:"index" json:"status"`
	TaskID             string    `gorm:"index" json:"task_id,omitempty"`
	Result             string    `json:"result,omitempty"`
	ApprovalSource     string    `json:"approval_source,omitempty"`
	ExpiresAt          time.Time `gorm:"index" json:"expires_at"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	BindingID          string    `json:"-"`
	ExternalUserID     string    `json:"-"`
	ChatID             string    `json:"-"`
}
type AIAudit struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	RunID          string    `gorm:"index" json:"run_id,omitempty"`
	OperationID    string    `gorm:"index" json:"operation_id,omitempty"`
	UserID         uint      `json:"user_id"`
	Action         string    `json:"action"`
	Source         string    `json:"source"`
	BindingID      string    `json:"binding_id,omitempty"`
	ExternalUserID string    `json:"external_user_id,omitempty"`
	ChatID         string    `json:"chat_id,omitempty"`
	Resource       string    `json:"resource,omitempty"`
	Result         string    `json:"result"`
	CreatedAt      time.Time `json:"created_at"`
	NodeID         string    `gorm:"-" json:"node_id,omitempty"`
	NodeName       string    `gorm:"-" json:"node_name,omitempty"`
	TaskID         string    `gorm:"-" json:"task_id,omitempty"`
	IP             string    `gorm:"-" json:"ip,omitempty"`
}
