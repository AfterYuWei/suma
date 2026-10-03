package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestBothModelToolProtocols(t *testing.T) {
	for _, protocol := range []string{"responses", "chat_completions"} {
		t.Run(protocol, func(t *testing.T) {
			m := HTTPModel{Client: func(bool) *http.Client {
				return &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
					if req.Header.Get("Authorization") != "Bearer KEY" {
						t.Fatal("key not forwarded")
					}
					var payload map[string]json.RawMessage
					if json.NewDecoder(req.Body).Decode(&payload) != nil || len(payload["tools"]) == 0 {
						t.Fatal("missing tools")
					}
					raw := `{"output":[{"type":"function_call","call_id":"call","name":"read_status","arguments":"{\"kind\":\"node\",\"id\":\"local\"}"}],"usage":{"total_tokens":10}}`
					if protocol == "chat_completions" {
						if req.URL.Path != "/v1/chat/completions" || payload["messages"] == nil {
							t.Fatal("wrong protocol")
						}
						raw = `{"choices":[{"message":{"tool_calls":[{"id":"call","function":{"name":"read_status","arguments":"{\"kind\":\"node\",\"id\":\"local\"}"}}]}}],"usage":{"total_tokens":10}}`
					}
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(raw))}, nil
				})}
			}}
			reply, err := m.Complete(context.Background(), Settings{Protocol: protocol, Endpoint: "https://example.com/v1", Model: "test"}, "KEY", []ModelMessage{{Role: "user", Text: "diagnose"}}, tools())
			if err != nil || len(reply.Calls) != 1 || reply.Calls[0].Name != "read_status" || reply.Tokens != 10 {
				t.Fatal(reply, err)
			}
		})
	}
}
