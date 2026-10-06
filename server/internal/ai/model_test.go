package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestResponsesModelTextAndToolConversation(t *testing.T) {
	for _, output := range []string{
		`{"output":[{"type":"function_call","call_id":"call","name":"read_status","arguments":"{\"kind\":\"node\",\"id\":\"local\"}"}],"usage":{"total_tokens":10}}`,
		`{"output":[{"type":"message","content":[{"type":"output_text","text":"Node "},{"type":"output_text","text":"is online."}]}],"usage":{"total_tokens":10}}`,
	} {
		m := HTTPModel{Client: func(bool) *http.Client {
			return &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodPost || req.URL.Path != "/v1/responses" || req.Header.Get("Authorization") != "Bearer KEY" {
					t.Fatal("wrong request path or authorization")
				}
				var payload struct {
					Model           string           `json:"model"`
					Store           bool             `json:"store"`
					MaxOutputTokens int              `json:"max_output_tokens"`
					Input           []map[string]any `json:"input"`
					Tools           []map[string]any `json:"tools"`
				}
				if json.NewDecoder(req.Body).Decode(&payload) != nil || payload.Model != "test" || payload.Store || payload.MaxOutputTokens != 4096 || len(payload.Input) != 3 || len(payload.Tools) == 0 {
					t.Fatal("invalid Responses payload", payload)
				}
				if payload.Input[1]["type"] != "function_call" || payload.Input[1]["call_id"] != "prior-call" || payload.Input[2]["type"] != "function_call_output" || payload.Input[2]["call_id"] != "prior-call" || payload.Input[2]["output"] != "online" {
					t.Fatal("Responses tool conversation lost its correlation", payload.Input)
				}
				if payload.Tools[0]["type"] != "function" || payload.Tools[0]["name"] != "read_status" || payload.Tools[0]["parameters"] == nil {
					t.Fatal("Responses tool definition is invalid", payload.Tools)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(output))}, nil
			})}
		}}
		messages := []ModelMessage{
			{Role: "user", Text: "diagnose"},
			{Role: "assistant", Calls: []ToolCall{{ID: "prior-call", Name: "read_status", Arguments: json.RawMessage(`{"kind":"node","id":"local"}`)}}},
			{Role: "tool", CallID: "prior-call", Text: "online"},
		}
		reply, err := m.Complete(context.Background(), Settings{Protocol: ProtocolResponses, Endpoint: "https://example.com/v1", Model: "test"}, "KEY", messages, tools())
		if err != nil || reply.Tokens != 10 {
			t.Fatal(reply, err)
		}
		if strings.Contains(output, `"function_call"`) {
			if len(reply.Calls) != 1 || reply.Calls[0].Name != "read_status" || reply.Calls[0].ID != "call" {
				t.Fatal("tool response lost", reply)
			}
		} else if reply.Text != "Node is online." {
			t.Fatal("text response lost", reply)
		}
	}
}

func TestHTTPModelRejectsUnsupportedProtocolsBeforeNetworkAccess(t *testing.T) {
	m := HTTPModel{Client: func(bool) *http.Client { t.Fatal("unsupported protocol reached the network"); return nil }}
	for _, protocol := range []string{"chat_completions", "unknown", ""} {
		_, err := m.Complete(context.Background(), Settings{Protocol: protocol, Endpoint: "https://example.com/v1"}, "KEY", nil, nil)
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "only Responses") {
			t.Fatal("unsupported protocol accepted", protocol, err)
		}
	}
}

func TestResponsesModelRejectsChatCompletionsResponses(t *testing.T) {
	for _, output := range []string{
		`{"choices":[{"message":{"content":"Connected"}}]}`,
		`{"choices":[{"message":{"tool_calls":[{"id":"call","function":{"name":"create_proposal","arguments":"{}"}}]}}]}`,
	} {
		m := HTTPModel{Client: func(bool) *http.Client {
			return &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(output))}, nil
			})}
		}}
		if reply, err := m.Complete(context.Background(), Settings{Protocol: ProtocolResponses, Endpoint: "https://example.com/v1", Model: "test"}, "KEY", nil, nil); err == nil || len(reply.Calls) != 0 {
			t.Fatal("removed response format was accepted", reply, err)
		}
	}
}
