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

const ProtocolResponses = "responses"

type Settings struct {
	Version           uint64   `json:"version"`
	Enabled           bool     `json:"enabled"`
	Protocol          string   `json:"protocol"`
	Endpoint          string   `json:"endpoint"`
	Model             string   `json:"model"`
	Models            []string `json:"models"`
	AllowPrivate      bool     `json:"allow_private"`
	AllowInsecure     bool     `json:"allow_insecure"`
	NodeIDs           []string `json:"node_ids"`
	AutoEvents        []string `json:"auto_events"`
	MaxConcurrent     int      `json:"max_concurrent"`
	DailyAutoLimit    int      `json:"daily_auto_limit"`
	MaxToolCalls      int      `json:"max_tool_calls"`
	MaxIterations     int      `json:"max_iterations"`
	MaxOperations     int      `json:"max_operations"`
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
type ModelsInput struct {
	Version       uint64 `json:"version"`
	Endpoint      string `json:"endpoint"`
	AllowPrivate  bool   `json:"allow_private"`
	AllowInsecure bool   `json:"allow_insecure"`
	APIKey        string `json:"api_key,omitempty"`
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
	ConversationID string `json:"conversation_id,omitempty"`
	NodeID         string `json:"node_id"`
	Model          string `json:"model,omitempty"`
	ResourceNodeID string `json:"resource_node_id,omitempty"`
	Question       string `json:"question"`
	ResourceType   string `json:"resource_type,omitempty"`
	ResourceID     string `json:"resource_id,omitempty"`
	EventID        string `json:"event_id,omitempty"`
}
type ToolArgs struct {
	NodeID string `json:"node_id,omitempty"`
	Kind   string `json:"kind,omitempty"`
	ID     string `json:"id,omitempty"`
	Query  string `json:"query,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}
type OperationRequest struct {
	StepID     string          `json:"step_id,omitempty"`
	NodeID     string          `json:"node_id,omitempty"`
	Action     string          `json:"action"`
	ResourceID string          `json:"resource_id"`
	Parameters json.RawMessage `json:"parameters"`
}
type Snapshot struct {
	RuntimeKey    string          `json:"runtime_key"`
	Fingerprint   string          `json:"fingerprint"`
	Description   string          `json:"description"`
	Impact        string          `json:"impact"`
	Details       json.RawMessage `json:"details"`
	Confirmations []Confirmation  `json:"confirmations,omitempty"`
}
type Evidence struct {
	NodeID      string    `json:"node_id,omitempty"`
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
	Result        Result       `json:"result"`
	TargetNodeIDs []string     `json:"target_node_ids"`
	Steps         []PlanStep   `json:"steps"`
	Interaction   *Interaction `json:"interaction,omitempty"`
}
type Operation struct {
	database.AIOperation
	Parameters    json.RawMessage `json:"parameters"`
	Snapshot      Snapshot        `json:"snapshot"`
	ReviewToken   string          `json:"review_token"`
	Confirmations []Confirmation  `json:"confirmations"`
	Verification  json.RawMessage `json:"verification"`
}
type Decision struct {
	ReviewToken   string            `json:"review_token"`
	Approve       bool              `json:"approve"`
	RequestID     string            `json:"request_id,omitempty"`
	Confirmations map[string]string `json:"confirmations,omitempty"`
}
type Dependencies struct {
	Check      func(context.Context, string, ToolArgs, Actor) (database.Task, error)
	Deliver    func(context.Context, Actor, WorkflowEvent, Run) error
	Audit      *audit.Service
	Now        func() time.Time
	Model      Model
	Read       func(context.Context, string, string, ToolArgs, int, int) (Evidence, error)
	Freeze     func(context.Context, string, OperationRequest) (Snapshot, error)
	Execute    func(context.Context, database.AIOperation, Snapshot, task.Reporter) error
	ActorValid func(context.Context, Actor) error
	Query      func(context.Context, string) (QuerySummary, error)
	Emit       event.Sink
	Resources  func(context.Context, string, ToolArgs) ([]ResourceOption, error)
	Verify     func(context.Context, database.AIOperation, Snapshot) (Verification, error)
	Draft      func(context.Context, string, DraftInput) (DraftResult, error)
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

// StreamingModel emits visible output-text deltas. Tool arguments and reasoning
// stay private until a complete, validated ModelReply is returned.
type StreamingModel interface {
	Stream(context.Context, Settings, string, []ModelMessage, []Tool, func(string) error) (ModelReply, error)
}

func DefaultSettings() Settings {
	return Settings{Protocol: ProtocolResponses, Endpoint: "https://api.openai.com/v1", Models: []string{}, NodeIDs: []string{}, AutoEvents: []string{}, MaxConcurrent: 2, DailyAutoLimit: 20, MaxToolCalls: 8, MaxIterations: 40, MaxOperations: 20, LogLines: 500, LogBytes: 64 << 10, ApprovalMinutes: 15}
}
