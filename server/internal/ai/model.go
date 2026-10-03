package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/suma/suma/server/internal/outbound"
)

// HTTPModel only translates the two supported tool protocols. It never executes
// tools or interprets model text as approval.
type HTTPModel struct{ Client func(bool) *http.Client }

func (m HTTPModel) Complete(ctx context.Context, cfg Settings, key string, messages []ModelMessage, registered []Tool) (ModelReply, error) {
	payload := map[string]any{"model": cfg.Model}
	path := "/responses"
	definitions := []any{}
	if cfg.Protocol == "chat_completions" {
		path = "/chat/completions"
		wire := []any{}
		for _, msg := range messages {
			item := map[string]any{"role": msg.Role, "content": msg.Text}
			if msg.CallID != "" {
				item["tool_call_id"] = msg.CallID
			}
			if len(msg.Calls) > 0 {
				calls := []any{}
				for _, c := range msg.Calls {
					calls = append(calls, map[string]any{"id": c.ID, "type": "function", "function": map[string]any{"name": c.Name, "arguments": string(c.Arguments)}})
				}
				item["tool_calls"] = calls
			}
			wire = append(wire, item)
		}
		payload["messages"] = wire
		payload["max_completion_tokens"] = 4096
		for _, t := range registered {
			definitions = append(definitions, map[string]any{"type": "function", "function": map[string]any{"name": t.Name, "description": t.Description, "parameters": t.Parameters, "strict": false}})
		}
	} else {
		wire := []any{}
		for _, msg := range messages {
			if msg.Role == "tool" {
				wire = append(wire, map[string]any{"type": "function_call_output", "call_id": msg.CallID, "output": msg.Text})
				continue
			}
			if msg.Text != "" {
				wire = append(wire, map[string]any{"role": msg.Role, "content": msg.Text})
			}
			for _, c := range msg.Calls {
				wire = append(wire, map[string]any{"type": "function_call", "call_id": c.ID, "name": c.Name, "arguments": string(c.Arguments)})
			}
		}
		payload["input"] = wire
		payload["store"] = false
		payload["max_output_tokens"] = 4096
		for _, t := range registered {
			definitions = append(definitions, map[string]any{"type": "function", "name": t.Name, "description": t.Description, "parameters": t.Parameters, "strict": false})
		}
	}
	if len(definitions) > 0 {
		payload["tools"] = definitions
		payload["parallel_tool_calls"] = false
	}
	address := strings.TrimRight(cfg.Endpoint, "/") + path
	if err := outbound.Validate(address, cfg.AllowPrivate); err != nil {
		return ModelReply{}, err
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", address, bytes.NewReader(body))
	if err != nil {
		return ModelReply{}, errors.New("invalid model request")
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	factory := m.Client
	if factory == nil {
		factory = outbound.Client
	}
	res, err := factory(cfg.AllowPrivate).Do(req)
	if err != nil {
		return ModelReply{}, errors.New("model connection failed")
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return ModelReply{}, errors.New("model response exceeded the size limit")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return ModelReply{}, fmt.Errorf("model returned HTTP %d", res.StatusCode)
	}
	var data struct {
		Status  string `json:"status"`
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			}
		} `json:"choices"`
		Output []struct {
			Type      string `json:"type"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Content   []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(raw, &data) != nil {
		return ModelReply{}, errors.New("invalid model response")
	}
	if data.Status == "failed" || data.Status == "incomplete" {
		return ModelReply{}, errors.New("model response did not complete")
	}
	reply := ModelReply{Calls: []ToolCall{}, Tokens: data.Usage.TotalTokens}
	if cfg.Protocol == "chat_completions" {
		if len(data.Choices) == 0 {
			return reply, errors.New("model response contains no choices")
		}
		msg := data.Choices[0].Message
		reply.Text = msg.Content
		for _, c := range msg.ToolCalls {
			reply.Calls = append(reply.Calls, ToolCall{ID: c.ID, Name: c.Function.Name, Arguments: json.RawMessage(c.Function.Arguments)})
		}
	}
	for _, o := range data.Output {
		if o.Type == "function_call" {
			reply.Calls = append(reply.Calls, ToolCall{ID: o.CallID, Name: o.Name, Arguments: json.RawMessage(o.Arguments)})
		}
		for _, c := range o.Content {
			if c.Type == "output_text" {
				reply.Text += c.Text
			}
		}
	}
	if len(reply.Calls) > 16 {
		return ModelReply{}, errors.New("model returned too many tool calls")
	}
	if len(reply.Calls) == 0 && strings.TrimSpace(reply.Text) == "" {
		return ModelReply{}, errors.New("model returned no response")
	}
	return reply, nil
}
