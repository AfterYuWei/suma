package ai

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// ResponsesChatModel keeps SUMA's transport, probes and outbound policy. All
// provider/tool bindings are per execution frame, never mutable shared state.
type ResponsesChatModel struct {
	frame *executionFrame
	tools []*schema.ToolInfo
}

func (m *ResponsesChatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	copy := *m
	copy.tools = append([]*schema.ToolInfo{}, tools...)
	return &copy, nil
}
func (m *ResponsesChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	f := m.frame
	if err := f.guard(ctx); err != nil {
		return nil, err
	}
	options := model.GetCommonOptions(&model.Options{}, opts...)
	infos := options.Tools
	if infos == nil {
		infos = m.tools
	}
	defs := []Tool{}
	for _, info := range infos {
		params := map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
		if info.ParamsOneOf != nil {
			js, err := info.ParamsOneOf.ToJSONSchema()
			if err != nil {
				return nil, ErrInvalid
			}
			raw, err := json.Marshal(js)
			if err != nil || json.Unmarshal(raw, &params) != nil {
				return nil, ErrInvalid
			}
		}
		defs = append(defs, Tool{Name: info.Name, Description: info.Desc, Parameters: params})
	}
	messages := []ModelMessage{}
	for _, message := range input {
		if message == nil {
			continue
		}
		row := ModelMessage{Role: string(message.Role), Text: f.clean(message.Content, 16000), CallID: message.ToolCallID}
		for _, call := range message.ToolCalls {
			row.Calls = append(row.Calls, ToolCall{ID: call.ID, Name: call.Function.Name, Arguments: json.RawMessage(call.Function.Arguments)})
		}
		messages = append(messages, row)
	}
	var reply ModelReply
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if err = f.guard(ctx); err != nil {
			return nil, err
		}
		if f.row.Iterations >= f.cfg.MaxIterations {
			return nil, ErrBudget
		}
		f.row.Iterations++
		reply, err = f.s.deps.Model.Complete(ctx, f.cfg, f.key, messages, defs)
		if err == nil || !transientModelError(err) || attempt == 2 {
			break
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	if err != nil {
		return nil, errors.New(f.clean(err.Error(), 1024))
	}
	if err = f.guard(ctx); err != nil {
		return nil, err
	}
	f.row.Tokens += max(0, reply.Tokens)
	calls := []schema.ToolCall{}
	seen := map[string]bool{}
	for _, call := range reply.Calls {
		if call.ID == "" || len(call.ID) > 128 || seen[call.ID] || len(call.Arguments) > 1<<20 || !json.Valid(call.Arguments) {
			return nil, ErrInvalid
		}
		seen[call.ID] = true
		calls = append(calls, schema.ToolCall{ID: call.ID, Type: "function", Function: schema.FunctionCall{Name: call.Name, Arguments: f.clean(string(call.Arguments), 1<<20)}})
	}
	out := schema.AssistantMessage(f.clean(reply.Text, 16000), calls)
	out.ResponseMeta = &schema.ResponseMeta{Usage: &schema.TokenUsage{TotalTokens: max(0, reply.Tokens)}}
	return out, nil
}
func (m *ResponsesChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	// Generate-only providers return one complete frame, not fabricated deltas.
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}
func transientModelError(err error) bool {
	for _, code := range []string{"HTTP 429", "HTTP 502", "HTTP 503", "HTTP 504"} {
		if strings.Contains(err.Error(), code) {
			return true
		}
	}
	return false
}
