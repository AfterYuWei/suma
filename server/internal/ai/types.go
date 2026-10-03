package ai

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/event"
	"github.com/suma/suma/server/internal/task"
)

var ErrDisabled = errors.New("AI operations are disabled")
var ErrScope = errors.New("resource is outside the authorized AI scope")
var ErrConflict = errors.New("approval is expired, changed or already decided; reload the operation")
var ErrBusy = errors.New("AI diagnosis concurrency limit reached")
var ErrBudget = errors.New("automatic diagnosis daily limit reached")
var ErrInvalid = errors.New("invalid AI request")

type Settings struct {
	Version           uint64   `json:"version"`
	Enabled           bool     `json:"enabled"`
	Protocol          string   `json:"protocol"`
	Endpoint          string   `json:"endpoint"`
	Model             string   `json:"model"`
	AllowPrivate      bool     `json:"allow_private"`
	NodeIDs           []string `json:"node_ids"`
	AutoEvents        []string `json:"auto_events"`
	MaxConcurrent     int      `json:"max_concurrent"`
	DailyAutoLimit    int      `json:"daily_auto_limit"`
	MaxToolCalls      int      `json:"max_tool_calls"`
	LogLines          int      `json:"log_lines"`
	LogBytes          int      `json:"log_bytes"`
	ApprovalMinutes   int      `json:"approval_minutes"`
	HasSecret         bool     `json:"has_secret"`
	ToolCapable       bool     `json:"tool_capable"`
	TestedFingerprint string   `json:"-"`
	AuthorizedBy      uint     `json:"authorized_by"`
}
type SettingsInput struct {
	Settings
	APIKey string `json:"api_key,omitempty"`
}
type Actor struct {
	UserID         uint
	Source         string
	BindingID      string
	ExternalUserID string
	ChatID         string
	IP             string
}
type RunInput struct {
	NodeID       string `json:"node_id"`
	Question     string `json:"question"`
	ResourceType string `json:"resource_type,omitempty"`
	ResourceID   string `json:"resource_id,omitempty"`
	ParentID     string `json:"parent_id,omitempty"`
	EventID      string `json:"event_id,omitempty"`
}
type ToolArgs struct {
	Kind string `json:"kind,omitempty"`
	ID   string `json:"id,omitempty"`
}
type OperationRequest struct {
	Action     string          `json:"action"`
	ResourceID string          `json:"resource_id"`
	Parameters json.RawMessage `json:"parameters"`
}
type Snapshot struct {
	RuntimeKey  string          `json:"runtime_key"`
	Fingerprint string          `json:"fingerprint"`
	Description string          `json:"description"`
	Impact      string          `json:"impact"`
	Details     json.RawMessage `json:"details"`
}
type Evidence struct {
	Source      string    `json:"source"`
	Resource    string    `json:"resource"`
	Time        time.Time `json:"time"`
	Content     string    `json:"content"`
	Unavailable bool      `json:"unavailable"`
}
type Result struct {
	Summary      string     `json:"summary"`
	Evidence     []Evidence `json:"evidence"`
	OperationIDs []string   `json:"operation_ids"`
	Missing      []string   `json:"missing"`
}
type Run struct {
	database.AIRun
	Result Result `json:"result"`
}
type Operation struct {
	database.AIOperation
	Parameters  json.RawMessage `json:"parameters"`
	Snapshot    Snapshot        `json:"snapshot"`
	ReviewToken string          `json:"review_token"`
}
type Decision struct {
	ReviewToken string `json:"review_token"`
	Approve     bool   `json:"approve"`
}
type Dependencies struct {
	Audit      *audit.Service
	Now        func() time.Time
	Model      Model
	Read       func(context.Context, string, string, ToolArgs, int, int) (Evidence, error)
	Freeze     func(context.Context, string, OperationRequest) (Snapshot, error)
	Execute    func(context.Context, database.AIOperation, Snapshot, task.Reporter) error
	ActorValid func(context.Context, Actor) error
	Query      func(context.Context, string) (QuerySummary, error)
	Emit       event.Sink
}
type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}
type ModelReply struct {
	Text   string
	Calls  []ToolCall
	Tokens int
}
type ModelMessage struct {
	Role   string
	Text   string
	CallID string
	Calls  []ToolCall
}
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
}
type Model interface {
	Complete(context.Context, Settings, string, []ModelMessage, []Tool) (ModelReply, error)
}

func DefaultSettings() Settings {
	return Settings{Protocol: "responses", Endpoint: "https://api.openai.com/v1", NodeIDs: []string{}, AutoEvents: []string{}, MaxConcurrent: 2, DailyAutoLimit: 20, MaxToolCalls: 8, LogLines: 500, LogBytes: 64 << 10, ApprovalMinutes: 15}
}
