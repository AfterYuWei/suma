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

func TestConnectionResultExplainsToolFailuresAndRedactsDetails(t *testing.T) {
	for _, row := range []struct {
		name, failure string
		reply         ModelReply
		err           error
	}{
		{"passed", "", ModelReply{Calls: []ToolCall{{ID: "probe", Name: "connection_probe", Arguments: json.RawMessage(`{"message":"suma_connection_test"}`)}}}, nil},
		{"no call", "not_called", ModelReply{Text: "Connected"}, nil},
		{"wrong tool", "unexpected_call", ModelReply{Calls: []ToolCall{{ID: "probe", Name: "other_tool", Arguments: json.RawMessage(`{}`)}}}, nil},
		{"multiple calls", "unexpected_call", ModelReply{Calls: []ToolCall{{Name: "connection_probe"}, {Name: "connection_probe"}}}, nil},
		{"missing call ID", "unexpected_call", ModelReply{Calls: []ToolCall{{Name: "connection_probe", Arguments: json.RawMessage(`{"message":"suma_connection_test"}`)}}}, nil},
		{"invalid JSON", "invalid_arguments", ModelReply{Calls: []ToolCall{{ID: "probe", Name: "connection_probe", Arguments: json.RawMessage(`not-json`)}}}, nil},
		{"null", "invalid_arguments", ModelReply{Calls: []ToolCall{{ID: "probe", Name: "connection_probe", Arguments: json.RawMessage(`null`)}}}, nil},
		{"empty object", "invalid_arguments", ModelReply{Calls: []ToolCall{{ID: "probe", Name: "connection_probe", Arguments: json.RawMessage(`{}`)}}}, nil},
		{"wrong marker", "invalid_arguments", ModelReply{Calls: []ToolCall{{ID: "probe", Name: "connection_probe", Arguments: json.RawMessage(`{"message":"other"}`)}}}, nil},
		{"wrong type", "invalid_arguments", ModelReply{Calls: []ToolCall{{ID: "probe", Name: "connection_probe", Arguments: json.RawMessage(`{"message":true}`)}}}, nil},
		{"schema keywords", "invalid_arguments", ModelReply{Calls: []ToolCall{{ID: "probe", Name: "connection_probe", Arguments: json.RawMessage(`{"additionalProperties":false}`)}}}, nil},
		{"extra arguments", "invalid_arguments", ModelReply{Calls: []ToolCall{{ID: "probe", Name: "connection_probe", Arguments: json.RawMessage(`{"message":"suma_connection_test","action":"restart"}`)}}}, nil},
		{"trailing JSON", "invalid_arguments", ModelReply{Calls: []ToolCall{{ID: "probe", Name: "connection_probe", Arguments: json.RawMessage(`{"message":"suma_connection_test"} {}`)}}}, nil},
		{"request rejected", "request_failed", ModelReply{}, errors.New("model service returned HTTP 400: model-secret password=private-error-value")},
	} {
		t.Run(row.name, func(t *testing.T) {
			s, executions, _ := aiFixture(t)
			calls := 0
			s.deps.Model = modelFunc(func(_ context.Context, cfg Settings, key string, _ []ModelMessage, tools []Tool) (ModelReply, error) {
				calls++
				if cfg.Model != "test" || key != "model-secret" {
					t.Fatal("wrong saved connection")
				}
				if len(tools) == 0 {
					return ModelReply{Text: "Connected model-secret password=private-reply-value " + strings.Repeat("x", 2048)}, nil
				}
				return row.reply, row.err
			})
			result, err := s.TestModel(context.Background())
			passed := row.failure == ""
			if err != nil || result["text"] != true || result["tool_capable"] != passed || result["summary_only"] != !passed || result["model"] != "test" || calls != 2 {
				t.Fatal(result, err, calls)
			}
			if passed {
				if _, exists := result["tool_failure"]; exists {
					t.Fatal("successful test has a failure reason")
				}
			} else if result["tool_failure"] != row.failure {
				t.Fatal("missing failure reason", result)
			}
			if result["duration_ms"].(int64) < 0 || len(result["text_response"].(string)) > 1024 {
				t.Fatal("invalid result limits", result)
			}
			raw, _ := json.Marshal(result)
			for _, secret := range []string{"model-secret", "private-error-value", "private-reply-value"} {
				if strings.Contains(string(raw), secret) {
					t.Fatal("secret exposed in test result")
				}
			}
			if row.err != nil && !strings.Contains(result["tool_error"].(string), "HTTP 400") {
				t.Fatal("safe upstream error lost", result)
			}
			if s.Settings().ToolCapable != passed || executions.Load() != 0 {
				t.Fatal("test result did not update capability or executed an operation")
			}
		})
	}
}

func TestConnectionProbeWithRequiredArgumentsOverResponses(t *testing.T) {
	s, executions, _ := aiFixture(t)
	requests := 0
	s.deps.Model = HTTPModel{Client: func(bool) *http.Client {
		return &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
			requests++
			if req.Method != http.MethodPost || req.URL.Path != "/v1/responses" || req.Header.Get("Authorization") != "Bearer model-secret" {
				t.Fatal("probe did not use the saved Responses connection")
			}
			var payload struct {
				Input             []map[string]any `json:"input"`
				Tools             []map[string]any `json:"tools"`
				ParallelToolCalls bool             `json:"parallel_tool_calls"`
			}
			if json.NewDecoder(req.Body).Decode(&payload) != nil {
				t.Fatal("invalid probe request")
			}
			output := `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"Connection confirmed."}]}]}`
			if requests == 2 {
				if len(payload.Tools) != 1 || payload.Tools[0]["type"] != "function" || payload.Tools[0]["name"] != "connection_probe" || payload.ParallelToolCalls {
					t.Fatal("invalid Responses test tool definition")
				}
				parameters, _ := payload.Tools[0]["parameters"].(map[string]any)
				properties, _ := parameters["properties"].(map[string]any)
				message, _ := properties["message"].(map[string]any)
				required, _ := parameters["required"].([]any)
				enum, _ := message["enum"].([]any)
				if len(properties) != 1 || message["type"] != "string" || len(required) != 1 || required[0] != "message" || len(enum) != 1 || enum[0] != "suma_connection_test" || parameters["additionalProperties"] != false {
					t.Fatal("probe regressed to an empty or unconstrained schema")
				}
				if len(payload.Input) != 1 || !strings.Contains(payload.Input[0]["content"].(string), `{"message":"suma_connection_test"}`) {
					t.Fatal("probe did not instruct the required arguments")
				}
				output = `{"status":"completed","output":[{"type":"reasoning","summary":[]},{"type":"function_call","call_id":"probe","name":"connection_probe","arguments":"{\"message\":\"suma_connection_test\"}"}]}`
			} else if requests != 1 || len(payload.Tools) != 0 {
				t.Fatal("connection test sent unexpected requests or tools")
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(output))}, nil
		})}
	}}
	result, err := s.TestModel(context.Background())
	if err != nil || result["tool_capable"] != true || result["summary_only"] != false || result["text_response"] != "Connection confirmed." || requests != 2 || !s.Settings().ToolCapable || executions.Load() != 0 {
		t.Fatal("valid Responses tool arguments did not verify safely", result, err, requests)
	}
}

func TestConnectionTextFailureDoesNotReportSuccessOrRunTools(t *testing.T) {
	for _, first := range []struct {
		reply ModelReply
		err   error
	}{
		{ModelReply{}, errors.New("model service returned HTTP 401")},
		{ModelReply{Text: " \n"}, nil},
	} {
		s, _, _ := aiFixture(t)
		before := s.Settings()
		calls := 0
		s.deps.Model = modelFunc(func(context.Context, Settings, string, []ModelMessage, []Tool) (ModelReply, error) {
			calls++
			return first.reply, first.err
		})
		if result, err := s.TestModel(context.Background()); err == nil || result != nil || calls != 1 {
			t.Fatal("failed text test reported success or ran a tool request", result, err, calls)
		}
		if after := s.Settings(); after.Version != before.Version || after.ToolCapable != before.ToolCapable {
			t.Fatal("failed text test replaced prior configuration")
		}
	}
}
