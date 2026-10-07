package database

import "time"

type AIConversation struct {
	ChatSentSeq    uint64     `gorm:"not null;default:0" json:"-"`
	ChatLeaseUntil *time.Time `gorm:"index" json:"-"`
	ID             string     `gorm:"primaryKey;size:64" json:"id"`
	UserID         uint       `gorm:"index;not null" json:"user_id"`
	Title          string     `json:"title"`
	Source         string     `gorm:"size:16;not null" json:"source"`
	ChatKey        string     `gorm:"size:64;uniqueIndex:idx_ai_chat_key,where:chat_key <> ''" json:"-"`
	BindingID      string     `gorm:"size:64" json:"-"`
	ChatID         string     `json:"-"`
	CurrentRunID   string     `gorm:"size:64" json:"current_run_id"`
	ContextJSON    string     `gorm:"type:jsonb;not null;default:'{}'" json:"-"`
	Revision       uint64     `gorm:"not null;default:1" json:"revision"`
	EventSeq       uint64     `gorm:"not null;default:0" json:"event_seq"`
	CreatedAt      time.Time  `gorm:"index" json:"created_at"`
	UpdatedAt      time.Time  `gorm:"index" json:"updated_at"`
}

type AIMessage struct {
	ID             string    `gorm:"primaryKey;size:64" json:"id"`
	ConversationID string    `gorm:"size:64;not null;index;uniqueIndex:idx_ai_message_request,where:request_key <> ''" json:"conversation_id"`
	RunID          string    `gorm:"size:64;index" json:"run_id"`
	Role           string    `gorm:"size:16" json:"role"`
	Content        string    `json:"content"`
	RequestKey     string    `gorm:"size:64;uniqueIndex:idx_ai_message_request,where:request_key <> ''" json:"-"`
	RequestHash    string    `gorm:"size:64" json:"-"`
	MetadataJSON   string    `gorm:"type:jsonb;not null;default:'{}'" json:"-"`
	CreatedAt      time.Time `gorm:"index" json:"created_at"`
}

type AIPlanStep struct {
	ID             string    `gorm:"primaryKey;size:64" json:"id"`
	RunID          string    `gorm:"size:64;not null;index;uniqueIndex:idx_ai_plan_position" json:"run_id"`
	Position       int       `gorm:"not null;uniqueIndex:idx_ai_plan_position" json:"position"`
	Title          string    `json:"title"`
	NodeID         string    `gorm:"size:64" json:"node_id"`
	Action         string    `gorm:"size:64" json:"action"`
	ResourceID     string    `json:"resource_id"`
	ParametersJSON string    `gorm:"type:jsonb;not null;default:'{}'" json:"-"`
	DependsJSON    string    `gorm:"type:jsonb;not null;default:'[]'" json:"-"`
	Expected       string    `json:"expected"`
	Status         string    `gorm:"size:32;index" json:"status"`
	OperationID    string    `gorm:"size:64" json:"operation_id"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type AIInteraction struct {
	ID          string    `gorm:"primaryKey;size:64" json:"id"`
	RunID       string    `gorm:"size:64;index;not null" json:"run_id"`
	Revision    uint64    `json:"revision"`
	Kind        string    `gorm:"size:32" json:"kind"`
	Prompt      string    `json:"prompt"`
	OptionsJSON string    `gorm:"type:jsonb;not null;default:'[]'" json:"-"`
	Multiple    bool      `json:"multiple"`
	InterruptID string    `json:"-"`
	StateJSON   string    `gorm:"type:jsonb;not null;default:'{}'" json:"-"`
	Status      string    `gorm:"size:16;index" json:"status"`
	AnswerJSON  string    `gorm:"type:jsonb;not null;default:'{}'" json:"-"`
	RequestKey  string    `gorm:"size:64" json:"-"`
	ExpiresAt   time.Time `gorm:"index" json:"expires_at"`
	CreatedAt   time.Time `json:"created_at"`
}

type AICheckpoint struct {
	ID               string    `gorm:"primaryKey;size:128" json:"-"`
	RunID            string    `gorm:"size:64;index" json:"-"`
	Revision         uint64    `json:"-"`
	FrameworkVersion string    `gorm:"size:32" json:"-"`
	WorkflowVersion  string    `gorm:"size:32" json:"-"`
	FormatVersion    int       `json:"-"`
	Ciphertext       []byte    `json:"-"`
	UpdatedAt        time.Time `json:"-"`
}

type AIToolCall struct {
	ID            string    `gorm:"primaryKey;size:64" json:"id"`
	RunID         string    `gorm:"size:64;not null;index;uniqueIndex:idx_ai_tool_call" json:"run_id"`
	CallKey       string    `gorm:"size:128;not null;uniqueIndex:idx_ai_tool_call" json:"-"`
	Name          string    `gorm:"size:64" json:"name"`
	ArgumentsHash string    `gorm:"size:64" json:"-"`
	ArgumentsJSON string    `gorm:"type:jsonb;not null;default:'{}'" json:"-"`
	Result        string    `json:"result"`
	Status        string    `gorm:"size:32" json:"status"`
	CreatedAt     time.Time `json:"created_at"`
}

type AIWorkflowEvent struct {
	ID             uint64    `gorm:"primaryKey" json:"-"`
	ConversationID string    `gorm:"size:64;not null;index;uniqueIndex:idx_ai_event_seq" json:"conversation_id"`
	Seq            uint64    `gorm:"not null;uniqueIndex:idx_ai_event_seq" json:"seq"`
	RunID          string    `gorm:"size:64;index" json:"run_id"`
	Type           string    `gorm:"size:64" json:"type"`
	PayloadJSON    string    `gorm:"type:jsonb;not null;default:'{}'" json:"-"`
	CreatedAt      time.Time `json:"created_at"`
}

type AIComposeDraft struct {
	ID           string    `gorm:"primaryKey;size:64" json:"id"`
	RunID        string    `gorm:"size:64;index" json:"run_id"`
	NodeID       string    `gorm:"size:64" json:"node_id"`
	Project      string    `json:"project"`
	BaseRevision string    `json:"base_revision"`
	ContentHash  string    `gorm:"size:64" json:"content_hash"`
	Ciphertext   []byte    `json:"-"`
	PreviewJSON  string    `gorm:"type:jsonb;not null;default:'{}'" json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}
