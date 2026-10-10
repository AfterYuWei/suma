package ai

import (
	"encoding/json"
	"github.com/suma/suma/server/internal/database"
)

const FrameworkVersion = "v0.9.21"
const WorkflowVersion = "suma-operations-v1"
const CheckpointFormat = 1

type TargetContext struct {
	NodeIDs        []string `json:"node_ids,omitempty"`
	ResourceNodeID string   `json:"resource_node_id,omitempty"`
	ResourceKind   string   `json:"resource_kind,omitempty"`
	ResourceID     string   `json:"resource_id,omitempty"`
}
type ConversationInput struct {
	Title string `json:"title,omitempty"`
}
type MessageInput struct {
	Question         string        `json:"question"`
	Model            string        `json:"model,omitempty"`
	RequestID        string        `json:"request_id"`
	ExpectedRevision uint64        `json:"expected_revision,omitempty"`
	Context          TargetContext `json:"context,omitempty"`
}
type Conversation struct {
	database.AIConversation
	Context    TargetContext        `json:"context"`
	Messages   []database.AIMessage `json:"messages"`
	Runs       []Run                `json:"runs"`
	CurrentRun *Run                 `json:"current_run,omitempty"`
}
type ResourceOption struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	NodeID    string `json:"node_id"`
	Kind      string `json:"kind"`
	Detail    string `json:"detail,omitempty"`
	Suggested bool   `json:"suggested,omitempty"`
}
type Interaction struct {
	database.AIInteraction
	Options []ResourceOption `json:"options"`
}
type InputAnswer struct {
	InteractionID    string   `json:"interaction_id"`
	ExpectedRevision uint64   `json:"expected_revision"`
	RequestID        string   `json:"request_id"`
	Values           []string `json:"values,omitempty"`
	Text             string   `json:"text,omitempty"`
}
type Confirmation struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Expected string `json:"expected"`
	Warning  string `json:"warning,omitempty"`
	Checkbox bool   `json:"checkbox,omitempty"`
}
type Verification struct {
	Satisfied bool       `json:"satisfied"`
	Summary   string     `json:"summary"`
	Evidence  []Evidence `json:"evidence"`
}
type PlanStep struct {
	database.AIPlanStep
	Parameters json.RawMessage `json:"parameters"`
	DependsOn  []int           `json:"depends_on"`
}
type PlanStepInput struct {
	Title      string          `json:"title"`
	NodeID     string          `json:"node_id"`
	Action     string          `json:"action"`
	ResourceID string          `json:"resource_id"`
	Parameters json.RawMessage `json:"parameters"`
	DependsOn  []int           `json:"depends_on,omitempty"`
	Expected   string          `json:"expected,omitempty"`
}
type DraftInput struct {
	Project      string `json:"project"`
	Compose      string `json:"compose"`
	BaseRevision string `json:"base_revision,omitempty"`
}
type DraftResult struct {
	Compose      string          `json:"-"`
	BaseRevision string          `json:"base_revision"`
	Preview      json.RawMessage `json:"preview"`
}
type WorkflowEvent struct {
	database.AIWorkflowEvent
	Payload json.RawMessage `json:"payload"`
}

// StreamOutput contains only the current redacted answer snapshot, never
// reasoning or incomplete tool arguments.
type StreamOutput struct {
	Text string `json:"text"`
}
