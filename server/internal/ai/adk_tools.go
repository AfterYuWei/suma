package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	"github.com/suma/suma/server/internal/database"
	"gorm.io/gorm"
)

var operationActions = []string{"container.start", "container.stop", "container.restart", "container.pause", "container.unpause", "container.kill", "container.rename", "container.remove", "image.pull", "image.tag", "image.remove", "network.create", "network.remove", "volume.create", "volume.remove", "project.create", "project.save", "project.up", "project.start", "project.stop", "project.restart", "project.down", "project.pull", "project.build", "project.update", "project.remove", "project.takeover", "project.cleanup", "cd.deploy", "cd.retry", "cd.rollback", "cleanup.protected", "cleanup.cache"}

// Bad model parameters can be corrected without weakening the actual actor,
// configuration, runtime or resource authorization checks (which use ErrScope).
var errToolNode = errors.New("invalid tool node ID")

func object(properties map[string]any, required ...string) map[string]any {
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}
func str(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}
func workflowTools() []Tool {
	scope := map[string]any{"node_id": str("Exact verified node ID; empty only with a single task target"), "kind": map[string]any{"type": "string", "enum": []string{"node", "container", "image", "network", "volume", "project", "task", "cd", "cleanup"}}, "id": str("Exact full resource ID or empty for a node"), "query": str("Optional resource name to search"), "cursor": str("Optional paging cursor")}
	tools := []Tool{
		{Name: "list_nodes", Description: "Read the allowed node directory. Does not establish a Docker target.", Parameters: object(map[string]any{})},
		{Name: "list_resources", Description: "Locate resources within the verified target; use full returned IDs. Ambiguous names require request_input.", Parameters: object(scope, "node_id", "kind", "id", "query", "cursor")},
	}
	for _, name := range []string{"read_status", "read_logs", "read_image", "read_project", "read_task", "read_cd", "read_cleanup"} {
		tools = append(tools, Tool{Name: name, Description: "Read bounded, redacted runtime evidence within the verified target", Parameters: object(scope, "node_id", "kind", "id", "query", "cursor")})
	}
	tools = append(tools, Tool{Name: "resolve_image", Description: "Resolve a tag to a fixed registry digest through an audited metadata Task. No layers are downloaded. Waits for the Task and returns its result.", Parameters: object(map[string]any{"node_id": str("Verified node ID"), "reference": str("Image tag reference")}, "node_id", "reference")}, Tool{Name: "check_image", Description: "Run the existing image detection Task for an exact local image and return its result", Parameters: object(map[string]any{"node_id": str("Verified node ID"), "image_id": str("Full local immutable image ID")}, "node_id", "image_id")})
	tools = append(tools,
		Tool{Name: "request_input", Description: "Wait for missing business parameters or a verified resource selection", Parameters: object(map[string]any{"kind": map[string]any{"type": "string", "enum": []string{"resource", "parameter"}}, "prompt": str("Question in the user's language"), "node_id": str("Verified node ID"), "resource_kind": str("Resource kind, or empty for parameter"), "query": str("Resource name query, or empty for parameter"), "multiple": map[string]any{"type": "boolean"}}, "kind", "prompt", "node_id", "resource_kind", "query", "multiple")},
		Tool{Name: "update_plan", Description: "Save the ordered plan; completed steps cannot be changed. All mutations remain individually reviewed.", Parameters: object(map[string]any{"steps": map[string]any{"type": "array", "maxItems": 100, "items": object(map[string]any{"title": str("Step title"), "node_id": str("Verified node ID"), "action": str("Catalog action or evidence"), "resource_id": str("Full resource ID"), "parameters": map[string]any{"type": "object"}, "depends_on": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}}, "expected": str("Expected observable result")}, "title", "node_id", "action", "resource_id", "parameters", "depends_on", "expected")}}, "steps")},
		Tool{Name: "create_proposal", Description: "Freeze ONE catalog action and wait for human review, then return actual execution and verification. Use empty parameters for lifecycle/deletion/CD; container rename uses name; removal optionally force (volumes are preserved); image tag uses reference; create network/volume uses its typed creation parameters; project create/save/takeover uses draft_id; project remove optionally force; cleanup uses frozen candidates or fixed until/reserved_bytes. This tool never executes writes itself.", Parameters: object(map[string]any{"step_id": str("Plan step ID, empty for a single-step task"), "node_id": str("Verified node ID"), "action": map[string]any{"type": "string", "enum": operationActions}, "resource_id": str("Full resource ID. For project create/save/takeover use the Project name and parameters.draft_id; never use the draft ID as the resource ID. Image pulls require a fixed digest reference."), "parameters": operationParametersSchema()}, "step_id", "node_id", "action", "resource_id", "parameters")},
		Tool{Name: "draft_compose", Description: "Generate a redacted, validated, encrypted Compose draft. Preserve all server secret references. Does not save or deploy configuration.", Parameters: object(map[string]any{"node_id": str("Verified node ID"), "project": str("Project name"), "compose": str("Proposed configuration, using preserved secret placeholders"), "base_revision": str("Current configuration revision or empty for new project")}, "node_id", "project", "compose", "base_revision")},
		Tool{Name: "validate_compose", Description: "Retrieve the saved draft's validation and redacted difference", Parameters: object(map[string]any{"draft_id": str("Draft ID returned by draft_compose")}, "draft_id")},
	)
	return tools
}

type workflowTool struct {
	frame      *executionFrame
	definition Tool
}

func (t *workflowTool) Info(context.Context) (*schema.ToolInfo, error) {
	data, err := json.Marshal(t.definition.Parameters)
	if err != nil {
		return nil, err
	}
	var spec jsonschema.Schema
	if err = json.Unmarshal(data, &spec); err != nil {
		return nil, err
	}
	return &schema.ToolInfo{Name: t.definition.Name, Desc: t.definition.Description, ParamsOneOf: schema.NewParamsOneOfByJSONSchema(&spec)}, nil
}
func (t *workflowTool) InvokableRun(ctx context.Context, args string, _ ...tool.Option) (string, error) {
	f := t.frame
	if err := f.guard(ctx); err != nil {
		return "", err
	}
	if len(args) > 128<<10 || !json.Valid([]byte(args)) {
		return marshal(map[string]string{"error": "Invalid tool parameters"}), nil
	}
	interrupted, hasState, state := tool.GetInterruptState[waitState](ctx)
	if interrupted && hasState {
		return t.resume(ctx, state)
	}
	if len(f.pending) > 0 {
		return `{"waiting":true,"message":"Wait for the current input or operation result before another tool call"}`, nil
	}
	callKey := compose.GetToolCallID(ctx)
	if callKey == "" {
		return "", ErrInvalid
	}
	var prior database.AIToolCall
	if err := f.s.db.WithContext(ctx).Where("run_id = ? AND call_key = ?", f.row.ID, callKey).First(&prior).Error; err == nil {
		if prior.ArgumentsHash != digest(json.RawMessage(args)) || prior.Name != t.definition.Name {
			return "", ErrConflict
		}
		return prior.Result, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", err
	}
	// A model's general-question classification is advisory. If it later asks
	// for a real resource, require a human target before any adapter is called.
	if len(f.targets) == 0 && t.definition.Name != "list_nodes" {
		options, err := f.nodeOptions(ctx)
		if err != nil {
			return "", err
		}
		if len(options) == 0 {
			return "", errNoAvailableTarget
		}
		state := f.makeInput("node", "查询实际资源前，请确认目标节点；若列表中没有所需节点，请先在设置 → AI 运维中授权。 / Select the target node before querying real resources. If it is missing, authorize it in Settings → AI operations.", options, false, waitState{CallKey: callKey, ArgumentsHash: digest(json.RawMessage(args)), ArgumentsJSON: f.clean(args, 128<<10)})
		return "", tool.StatefulInterrupt(ctx, f.prompt(state), state)
	}
	if err := f.progress(ctx, "evidence", "tool.started", map[string]string{"tool": t.definition.Name}); err != nil {
		return "", err
	}
	output, err := t.invoke(ctx, args, callKey)
	status := "completed"
	if err != nil {
		if _, isInterrupt := compose.IsInterruptRerunError(err); isInterrupt {
			return "", err
		}
		if errors.Is(err, ErrScope) || errors.Is(err, ErrConflict) || ctx.Err() != nil {
			return "", err
		}
		status = "rejected"
		if errors.Is(err, errToolNode) {
			output = marshal(map[string]any{"error": "invalid_node_id", "message": "No resource was accessed. Use an exact ID from target_node_ids, not a display name or an inferred ID. Do not broaden this task's target set.", "target_node_ids": f.targets})
		} else {
			output = marshal(map[string]string{"error": f.clean(err.Error(), 1024)})
		}
	}
	if err = f.guard(ctx); err != nil {
		return "", err
	}
	if err = f.progress(ctx, "evidence", "tool.completed", map[string]string{"tool": t.definition.Name}); err != nil {
		return "", err
	}
	f.calls[callKey] = database.AIToolCall{ID: id(), RunID: f.row.ID, CallKey: callKey, Name: t.definition.Name, ArgumentsHash: digest(json.RawMessage(args)), ArgumentsJSON: f.clean(args, 128<<10), Result: f.clean(output, 128<<10), Status: status}
	return output, nil
}
func (t *workflowTool) node(node string) (string, error) {
	f := t.frame
	if node == "" && len(f.targets) == 1 {
		node = f.targets[0]
	}
	if node == "" || !has(f.targets, node) {
		return "", errToolNode
	}
	return node, nil
}
func (t *workflowTool) invoke(ctx context.Context, args, callKey string) (string, error) {
	f := t.frame
	name := t.definition.Name
	if name == "list_nodes" {
		options, err := f.nodeOptions(ctx)
		return marshal(options), err
	}
	if name == "resolve_image" || name == "check_image" {
		var in struct {
			NodeID    string `json:"node_id"`
			Reference string `json:"reference,omitempty"`
			ImageID   string `json:"image_id,omitempty"`
		}
		if strict(json.RawMessage(args), &in) != nil || f.s.deps.Check == nil {
			return "", ErrInvalid
		}
		node, err := t.node(in.NodeID)
		if err != nil {
			return "", err
		}
		if f.row.Reads >= f.cfg.MaxToolCalls {
			return "", ErrBudget
		}
		f.row.Reads++
		row, err := f.s.deps.Check(ctx, node, ToolArgs{Kind: "image", ID: in.ImageID, Query: in.Reference}, f.actor)
		if err != nil {
			return "", err
		}
		state := f.makeInput("task", "正在检测镜像并等待实际结果 / Checking image metadata", []ResourceOption{}, false, waitState{CallKey: callKey, TaskID: row.ID, ArgumentsHash: digest(json.RawMessage(args)), ArgumentsJSON: f.clean(args, 128<<10)})
		return "", tool.StatefulInterrupt(ctx, f.prompt(state), state)
	}
	if name == "create_proposal" {
		var req OperationRequest
		if strict(json.RawMessage(args), &req) != nil {
			return "", ErrInvalid
		}
		return t.proposal(ctx, req, callKey, args)
	}
	if name == "request_input" {
		var in struct {
			Kind         string `json:"kind"`
			Prompt       string `json:"prompt"`
			NodeID       string `json:"node_id"`
			ResourceKind string `json:"resource_kind"`
			Query        string `json:"query"`
			Multiple     bool   `json:"multiple"`
		}
		if strict(json.RawMessage(args), &in) != nil || !has([]string{"resource", "parameter"}, in.Kind) || in.Prompt == "" {
			return "", ErrInvalid
		}
		options := []ResourceOption{}
		if in.Kind == "resource" {
			node, err := t.node(in.NodeID)
			if err != nil {
				return "", err
			}
			if f.s.deps.Resources == nil {
				return "", ErrInvalid
			}
			options, err = f.s.deps.Resources(ctx, node, ToolArgs{Kind: in.ResourceKind, Query: in.Query})
			if err != nil {
				return "", err
			}
			if len(options) == 0 {
				return marshal(map[string]string{"error": "No matching resource; request another name"}), nil
			}
		}
		state := f.makeInput(in.Kind, f.clean(in.Prompt, 2000), options, in.Multiple, waitState{CallKey: callKey, ArgumentsHash: digest(json.RawMessage(args)), ArgumentsJSON: f.clean(args, 128<<10)})
		return "", tool.StatefulInterrupt(ctx, f.prompt(state), state)
	}
	if name == "update_plan" {
		var in struct {
			Steps []PlanStepInput `json:"steps"`
		}
		if strict(json.RawMessage(args), &in) != nil || len(in.Steps) == 0 || len(in.Steps) > 100 {
			return "", ErrInvalid
		}
		return t.plan(ctx, in.Steps)
	}
	if name == "draft_compose" {
		var in struct {
			NodeID string `json:"node_id"`
			DraftInput
		}
		if strict(json.RawMessage(args), &in) != nil || f.s.deps.Draft == nil {
			return "", ErrInvalid
		}
		node, err := t.node(in.NodeID)
		if err != nil {
			return "", err
		}
		draft, err := f.s.deps.Draft(ctx, node, in.DraftInput)
		if err != nil {
			return "", err
		}
		cipher, err := f.s.secrets.Encrypt(draft.Compose)
		if err != nil {
			return "", err
		}
		row := database.AIComposeDraft{ID: id(), RunID: f.row.ID, NodeID: node, Project: in.Project, BaseRevision: draft.BaseRevision, ContentHash: digest(draft.Compose), Ciphertext: cipher, PreviewJSON: f.clean(string(draft.Preview), 128<<10)}
		if err = f.s.db.WithContext(ctx).Create(&row).Error; err != nil {
			return "", err
		}
		return marshal(map[string]any{"draft_id": row.ID, "base_revision": row.BaseRevision, "preview": json.RawMessage(row.PreviewJSON)}), nil
	}
	if name == "validate_compose" {
		var in struct {
			DraftID string `json:"draft_id"`
		}
		if strict(json.RawMessage(args), &in) != nil {
			return "", ErrInvalid
		}
		var draft database.AIComposeDraft
		if f.s.db.WithContext(ctx).Where("id = ? AND run_id = ?", in.DraftID, f.row.ID).First(&draft).Error != nil {
			return "", ErrScope
		}
		if _, err := t.node(draft.NodeID); err != nil {
			return "", err
		}
		return draft.PreviewJSON, nil
	}
	var in ToolArgs
	if strict(json.RawMessage(args), &in) != nil {
		return "", ErrInvalid
	}
	node, err := t.node(in.NodeID)
	if err != nil {
		return "", err
	}
	if f.row.Reads >= f.cfg.MaxToolCalls {
		return "", errors.New("evidence read budget exhausted")
	}
	f.row.Reads++
	if name == "list_resources" {
		if f.s.deps.Resources == nil {
			return "", ErrInvalid
		}
		resources, err := f.s.deps.Resources(ctx, node, in)
		if err != nil {
			return "", err
		}
		return f.clean(marshal(resources), 128<<10), nil
	}
	if f.s.deps.Read == nil {
		return "", ErrInvalid
	}
	evidence, err := f.s.deps.Read(ctx, node, name, in, f.cfg.LogLines, f.cfg.LogBytes)
	if err != nil {
		return "", err
	}
	evidence.NodeID = node
	evidence.Content = f.clean(evidence.Content, f.cfg.LogBytes)
	if lines := strings.Split(evidence.Content, "\n"); len(lines) > f.cfg.LogLines {
		evidence.Content = strings.Join(lines[:f.cfg.LogLines], "\n")
	}
	f.result.Evidence = append(f.result.Evidence, evidence)
	if evidence.Unavailable {
		f.result.Missing = append(f.result.Missing, evidence.Source)
	}
	return marshal(evidence), nil
}
func (t *workflowTool) resume(ctx context.Context, state waitState) (string, error) {
	f := t.frame
	if state.Kind == "task" {
		target, _, _ := tool.GetResumeContext[map[string]string](ctx)
		if !target {
			return "", ErrConflict
		}
		var row database.Task
		if f.s.db.WithContext(ctx).Where("id = ? AND node_id IN ?", state.TaskID, f.targets).First(&row).Error != nil || row.Status != "success" {
			return "", ErrConflict
		}
		logs, err := f.s.tasks.RecentLogsForNode(ctx, row.NodeID, row.ID, row.CreatedAt, f.cfg.LogLines, f.cfg.LogBytes)
		if err != nil {
			return "", err
		}
		output := f.clean(marshal(map[string]any{"task": row, "logs": logs}), f.cfg.LogBytes)
		t.recordResume(state, output)
		return output, nil
	}
	if state.Kind == "approval" {
		target, _, _ := tool.GetResumeContext[map[string]string](ctx)
		if !target {
			return "", ErrConflict
		}
		var op database.AIOperation
		if f.s.db.WithContext(ctx).Where("id = ? AND run_id = ?", state.OperationID, f.row.ID).First(&op).Error != nil {
			return "", ErrScope
		}
		if op.Status != "completed" {
			return "", errors.New("previous operation failed or is uncertain; execution is paused")
		}
		output := marshal(map[string]any{"operation_id": op.ID, "status": op.Status, "result": op.Result, "verification": json.RawMessage(op.VerificationJSON)})
		f.calls[state.CallKey] = database.AIToolCall{ID: id(), RunID: f.row.ID, CallKey: state.CallKey, Name: t.definition.Name, ArgumentsHash: state.ArgumentsHash, ArgumentsJSON: state.ArgumentsJSON, Result: output, Status: "completed"}
		return output, nil
	}
	target, data, answer := tool.GetResumeContext[InputAnswer](ctx)
	if !target || !data || answer.InteractionID != state.InteractionID {
		return "", ErrConflict
	}
	if state.Kind == "node" {
		if len(answer.Values) != 1 {
			return "", ErrInvalid
		}
		if err := f.setTargets(answer.Values, "selection"); err != nil {
			return "", err
		}
		// Never replay the original call with unverified IDs, resources or write
		// parameters. Let the model build a fresh call using the confirmed scope.
		output := marshal(map[string]any{"target_node_ids": f.targets, "message": "The user confirmed the task target. The original tool call was not executed. Discard its unverified parameters and issue a fresh call using the confirmed node ID."})
		t.recordResume(state, output)
		call := f.calls[state.CallKey]
		call.Status = "clarified"
		f.calls[state.CallKey] = call
		return output, nil
	}
	output := marshal(map[string]any{"values": answer.Values, "text": answer.Text})
	t.recordResume(state, output)
	return output, nil
}
func (t *workflowTool) plan(ctx context.Context, steps []PlanStepInput) (string, error) {
	f := t.frame
	var prior []database.AIPlanStep
	if err := f.s.db.WithContext(ctx).Where("run_id = ?", f.row.ID).Order("position ASC").Find(&prior).Error; err != nil {
		return "", err
	}
	out := []PlanStep{}
	for i, in := range steps {
		node, err := t.node(in.NodeID)
		if err != nil {
			return "", err
		}
		if in.Title == "" || len(in.Parameters) > 8192 || !json.Valid(in.Parameters) {
			return "", ErrInvalid
		}
		for _, dependency := range in.DependsOn {
			if dependency < 1 || dependency > i {
				return "", ErrInvalid
			}
		}
		row := database.AIPlanStep{ID: id(), RunID: f.row.ID, Position: i + 1, Title: f.clean(in.Title, 200), NodeID: node, Action: in.Action, ResourceID: in.ResourceID, ParametersJSON: string(in.Parameters), DependsJSON: marshal(in.DependsOn), Expected: f.clean(in.Expected, 1024), Status: "planned"}
		if i < len(prior) {
			old := prior[i]
			if old.Status != "planned" {
				if old.Action != row.Action || old.ResourceID != row.ResourceID || old.NodeID != row.NodeID || digest(json.RawMessage(old.ParametersJSON)) != digest(in.Parameters) {
					return "", ErrConflict
				}
				row = old
			} else {
				row.ID = old.ID
			}
		}
		f.steps[row.ID] = row
		out = append(out, PlanStep{AIPlanStep: row, Parameters: json.RawMessage(row.ParametersJSON), DependsOn: in.DependsOn})
	}
	if len(prior) > len(steps) {
		for _, old := range prior[len(steps):] {
			if old.Status != "planned" {
				return "", ErrConflict
			}
		}
		return "", errors.New("adjust the task with a new message to replace its plan")
	}
	return marshal(out), nil
}
func (t *workflowTool) proposal(ctx context.Context, req OperationRequest, callKey string, args string) (string, error) {
	f := t.frame
	node, err := t.node(req.NodeID)
	if err != nil {
		return "", err
	}
	req.NodeID = node
	if !has(operationActions, req.Action) || req.ResourceID == "" || len(req.Parameters) > 8192 || !json.Valid(req.Parameters) || f.s.deps.Freeze == nil {
		return "", ErrInvalid
	}
	if f.row.Operations >= f.cfg.MaxOperations {
		return "", errors.New("operation budget exhausted")
	}
	var active int64
	if err = f.s.db.WithContext(ctx).Model(&database.AIOperation{}).Where("run_id = ? AND status IN ?", f.row.ID, []string{"awaiting_approval", "queued", "running", "failed", "interrupted", "rejected", "expired", "invalidated"}).Count(&active).Error; err != nil {
		return "", err
	}
	if active > 0 {
		return "", errors.New("a previous operation requires a new user decision before continuing")
	}
	if strings.HasPrefix(req.Action, "project.") {
		var params struct {
			DraftID string `json:"draft_id"`
		}
		_ = json.Unmarshal(req.Parameters, &params)
		if params.DraftID != "" {
			var count int64
			if f.s.db.WithContext(ctx).Model(&database.AIComposeDraft{}).Where("id = ? AND run_id = ? AND node_id = ?", params.DraftID, f.row.ID, node).Count(&count).Error != nil || count != 1 {
				return "", ErrScope
			}
		}
	}
	var step database.AIPlanStep
	if req.StepID != "" {
		if staged, ok := f.steps[req.StepID]; ok {
			step = staged
		} else if f.s.db.WithContext(ctx).Where("id = ? AND run_id = ?", req.StepID, f.row.ID).First(&step).Error != nil {
			return "", ErrScope
		}
		if step.Status != "planned" || step.NodeID != node || step.Action != req.Action || step.ResourceID != req.ResourceID || digest(json.RawMessage(step.ParametersJSON)) != digest(req.Parameters) {
			return "", ErrConflict
		}
		var dependencies []int
		_ = json.Unmarshal([]byte(step.DependsJSON), &dependencies)
		for _, position := range dependencies {
			var count int64
			if f.s.db.WithContext(ctx).Model(&database.AIPlanStep{}).Where("run_id = ? AND position = ? AND status = ?", f.row.ID, position, "completed").Count(&count).Error != nil || count != 1 {
				return "", ErrConflict
			}
		}
	}
	snap, err := f.s.deps.Freeze(ctx, node, req)
	if err != nil {
		return "", err
	}
	if snap.RuntimeKey == "" || snap.Fingerprint == "" {
		return "", ErrInvalid
	}
	if err = f.guard(ctx); err != nil {
		return "", err
	}
	private, err := f.s.secrets.Encrypt(marshal(snap))
	if err != nil {
		return "", err
	}
	preview := snap
	preview.RuntimeKey = ""
	preview.Description = f.clean(preview.Description, 500)
	preview.Impact = f.clean(preview.Impact, 2000)
	preview.Details = json.RawMessage(f.clean(string(preview.Details), 128<<10))
	if !json.Valid(preview.Details) {
		return "", errors.New("operation preview exceeded limits or could not be safely redacted")
	}
	op := database.AIOperation{ID: id(), RunID: f.row.ID, StepID: req.StepID, RequestedBy: f.actor.UserID, NodeID: node, Action: req.Action, ResourceID: req.ResourceID, Title: preview.Description, Impact: preview.Impact, ParametersJSON: f.clean(string(req.Parameters), 8192), SnapshotJSON: marshal(preview), SnapshotCiphertext: private, SnapshotHash: operationDigest(req, snap), ConfirmationsJSON: marshal(append([]Confirmation{}, snap.Confirmations...)), Status: "prepared", ExpiresAt: f.s.deps.Now().Add(time.Duration(f.cfg.ApprovalMinutes) * time.Minute)}
	if req.StepID == "" {
		var max int
		f.s.db.WithContext(ctx).Model(&database.AIPlanStep{}).Where("run_id = ?", f.row.ID).Select("COALESCE(MAX(position), 0)").Scan(&max)
		for _, staged := range f.steps {
			if staged.Position > max {
				max = staged.Position
			}
		}
		step = database.AIPlanStep{ID: id(), RunID: f.row.ID, Position: max + 1, Title: preview.Description, NodeID: node, Action: req.Action, ResourceID: req.ResourceID, ParametersJSON: string(req.Parameters), DependsJSON: "[]", Expected: preview.Description}
		op.StepID = step.ID
	}
	step.Status = "awaiting_approval"
	step.OperationID = op.ID
	f.steps[step.ID] = step
	f.proposals[op.ID] = op
	f.row.Operations++
	f.result.OperationIDs = append(f.result.OperationIDs, op.ID)
	state := f.makeInput("approval", fmt.Sprintf("审核 %s / Review %s", op.Title, op.Action), []ResourceOption{}, false, waitState{OperationID: op.ID, CallKey: callKey, ArgumentsHash: digest(json.RawMessage(args)), ArgumentsJSON: f.clean(args, 128<<10)})
	return "", tool.StatefulInterrupt(ctx, f.prompt(state), state)
}

func (t *workflowTool) recordResume(state waitState, result string) {
	t.frame.calls[state.CallKey] = database.AIToolCall{ID: id(), RunID: t.frame.row.ID, CallKey: state.CallKey, Name: t.definition.Name, ArgumentsHash: state.ArgumentsHash, ArgumentsJSON: state.ArgumentsJSON, Result: result, Status: "completed"}
}

func operationParametersSchema() map[string]any {
	return map[string]any{"anyOf": []any{
		object(map[string]any{}),
		object(map[string]any{"force": map[string]any{"type": "boolean"}}),
		object(map[string]any{"name": str("New container name")}, "name"),
		object(map[string]any{"reference": str("Destination image tag reference")}, "reference"),
		object(map[string]any{"name": str("Network name"), "driver": map[string]any{"type": "string", "enum": []string{"bridge", "macvlan", "ipvlan"}}, "subnet": str("CIDR subnet"), "gateway": str("Gateway inside subnet"), "ipv6": map[string]any{"type": "boolean"}}, "name", "driver", "subnet", "gateway", "ipv6"),
		object(map[string]any{"name": str("Volume name"), "driver": str("Volume driver"), "labels": map[string]any{"type": "object", "maxProperties": 20, "additionalProperties": map[string]any{"type": "string"}}, "options": map[string]any{"type": "object", "maxProperties": 20, "additionalProperties": map[string]any{"type": "string"}}}, "name", "driver", "labels", "options"),
		object(map[string]any{"draft_id": str("Validated encrypted Compose draft ID")}, "draft_id"),
		object(map[string]any{"candidates": map[string]any{"type": "array", "maxItems": 200, "items": object(map[string]any{"kind": map[string]any{"type": "string", "enum": []string{"container", "image", "network"}}, "id": str("Full immutable resource ID")}, "kind", "id")}}, "candidates"),
		object(map[string]any{"until": map[string]any{"type": "string", "format": "date-time"}, "reserved_bytes": map[string]any{"type": "integer", "minimum": 0}}, "until", "reserved_bytes"),
	}}
}
