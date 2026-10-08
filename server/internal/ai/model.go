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

// HTTPModel only translates the Responses tool protocol. It never executes
// tools or interprets model text as approval.
type HTTPModel struct{ Client func(bool) *http.Client }

func (m HTTPModel) Complete(ctx context.Context, cfg Settings, key string, messages []ModelMessage, registered []Tool) (ModelReply, error) {
	return m.complete(ctx, cfg, key, messages, registered, nil)
}

func (m HTTPModel) Stream(ctx context.Context, cfg Settings, key string, messages []ModelMessage, registered []Tool, emit func(string) error) (ModelReply, error) {
	if emit == nil {
		emit = func(string) error { return nil }
	}
	return m.complete(ctx, cfg, key, messages, registered, emit)
}

func (m HTTPModel) complete(ctx context.Context, cfg Settings, key string, messages []ModelMessage, registered []Tool, emit func(string) error) (ModelReply, error) {
	if cfg.Protocol != ProtocolResponses {
		return ModelReply{}, fmt.Errorf("%w: only Responses protocol is supported", ErrInvalid)
	}
	payload := map[string]any{"model": cfg.Model, "store": false, "max_output_tokens": 4096}
	if emit != nil {
		payload["stream"] = true
	}
	definitions := []any{}
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
	for _, t := range registered {
		definitions = append(definitions, map[string]any{"type": "function", "name": t.Name, "description": t.Description, "parameters": t.Parameters, "strict": false})
	}
	if len(definitions) > 0 {
		payload["tools"] = definitions
		payload["parallel_tool_calls"] = false
	}
	address := strings.TrimRight(cfg.Endpoint, "/") + "/responses"
	if err := validateEndpoint(cfg.Endpoint, cfg.AllowInsecure); err != nil {
		return ModelReply{}, err
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", address, bytes.NewReader(body))
	if err != nil {
		return ModelReply{}, errors.New("invalid model request")
	}
	req.Header.Set("Content-Type", "application/json")
	if emit != nil {
		req.Header.Set("Accept", "text/event-stream")
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	res, err := m.client(cfg).Do(req)
	if err != nil {
		return ModelReply{}, errors.New("model connection failed")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return ModelReply{}, modelHTTPError(res.StatusCode)
	}
	if emit != nil && strings.Contains(strings.ToLower(res.Header.Get("Content-Type")), "text/event-stream") {
		return readResponsesStream(ctx, res.Body, emit)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return ModelReply{}, errors.New("model response exceeded the size limit")
	}
	return parseModelReply(raw)
}

func parseModelReply(raw []byte) (ModelReply, error) {
	var data struct {
		Status string `json:"status"`
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

func (m HTTPModel) client(cfg Settings) *http.Client {
	if m.Client != nil {
		return m.Client(cfg.AllowPrivate)
	}
	return outbound.ModelClient(cfg.AllowPrivate, cfg.AllowInsecure)
}

func modelHTTPError(status int) error {
	switch status {
	case http.StatusUnauthorized:
		return errors.New("model service returned HTTP 401; check the API key")
	case http.StatusForbidden:
		return errors.New("model service returned HTTP 403; check API key permissions and gateway access rules (the request is sent by the SUMA server)")
	case http.StatusNotFound:
		return errors.New("model service returned HTTP 404; check the API base URL and whether the provider requires /v1")
	default:
		return fmt.Errorf("model service returned HTTP %d", status)
	}
}

func (m HTTPModel) ListModels(ctx context.Context, cfg Settings, key string) ([]string, error) {
	if err := validateEndpoint(cfg.Endpoint, cfg.AllowInsecure); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cfg.Endpoint, "/")+"/models", nil)
	if err != nil {
		return nil, errors.New("invalid model discovery request")
	}
	req.Header.Set("Accept", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	res, err := m.client(cfg).Do(req)
	if err != nil {
		return nil, errors.New("model discovery connection failed; check HTTP/private-network permissions and TLS certificates")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, modelHTTPError(res.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return nil, errors.New("model list exceeded the size limit")
	}
	var data struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &data) != nil || data.Data == nil || len(data.Data) > 2000 {
		return nil, errors.New("model service returned an invalid model list; add model IDs manually if /models is unsupported")
	}
	models := []string{}
	for _, item := range data.Data {
		id := strings.TrimSpace(item.ID)
		if validModel(id) && !has(models, id) {
			models = append(models, id)
		}
	}
	return models, nil
}
