package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func writeStreamEvent(w http.ResponseWriter, event any) {
	raw, _ := json.Marshal(event)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
	w.(http.Flusher).Flush()
}

func TestResponsesStreamDeliversTextBeforeCompletionAndWithholdsReasoningAndArguments(t *testing.T) {
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		_ = json.NewDecoder(r.Body).Decode(&request)
		if request["stream"] != true || r.Header.Get("Accept") != "text/event-stream" {
			t.Error("Responses request did not enable SSE")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		writeStreamEvent(w, map[string]any{"type": "response.reasoning_text.delta", "delta": "private reasoning"})
		writeStreamEvent(w, map[string]any{"type": "response.function_call_arguments.delta", "delta": "private arguments"})
		writeStreamEvent(w, map[string]any{"type": "response.output_text.delta", "delta": "ganzhou "})
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		writeStreamEvent(w, map[string]any{"type": "response.output_text.delta", "delta": "online"})
		writeStreamEvent(w, map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed", "output": []any{
			map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": "ganzhou online"}}},
			map[string]any{"type": "function_call", "call_id": "call-1", "name": "read_status", "arguments": `{"kind":"node","id":"local"}`},
		}, "usage": map[string]int{"total_tokens": 17}}})
	}))
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
		server.Close()
	}()
	cfg := DefaultSettings()
	cfg.Model, cfg.Endpoint, cfg.AllowPrivate, cfg.AllowInsecure = "fixture", server.URL, true, true
	model := HTTPModel{Client: func(bool) *http.Client { return server.Client() }}
	updates := make(chan string, 4)
	finished := make(chan ModelReply, 1)
	failure := make(chan error, 1)
	go func() {
		reply, err := model.Stream(context.Background(), cfg, "fixture-key", []ModelMessage{{Role: "user", Text: "read local"}}, nil, func(delta string) error { updates <- delta; return nil })
		finished <- reply
		failure <- err
	}()
	select {
	case delta := <-updates:
		if delta != "ganzhou " {
			t.Fatal("private or incorrect delta", delta)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no text before model completion")
	}
	select {
	case <-finished:
		t.Fatal("model completed before the fixture released its final event")
	default:
	}
	close(release)
	select {
	case reply := <-finished:
		if err := <-failure; err != nil || reply.Text != "ganzhou online" || reply.Tokens != 17 || len(reply.Calls) != 1 || string(reply.Calls[0].Arguments) != `{"kind":"node","id":"local"}` {
			t.Fatal("complete streamed result lost text, usage or verified call", reply, err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not finish")
	}
	if delta := <-updates; delta != "online" || len(updates) != 0 {
		t.Fatal("reasoning or arguments entered visible output")
	}
}

func TestResponsesStreamRequiresATerminalEventAndHidesUpstreamErrors(t *testing.T) {
	for _, ending := range []string{"", "data: [DONE]\n\n", `data: {"type":"response.failed","error":{"message":"private-key-fixture"}}` + "\n\n", `data: {"type":"response.incomplete"}` + "\n\n", "data: malformed\n\n"} {
		t.Run(fmt.Sprint(len(ending), strings.Contains(ending, "failed")), func(t *testing.T) {
			reply, err := readResponsesStream(context.Background(), strings.NewReader("data: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"unsafe call\"}\n\n"+ending), func(string) error { t.Fatal("tool arguments became text"); return nil })
			if err == nil || len(reply.Calls) != 0 || strings.Contains(err.Error(), "private-key-fixture") {
				t.Fatal("incomplete stream authorized a call or exposed a private error", err)
			}
		})
	}
}

func TestResponsesStreamCancellationClosesTheUpstreamAndJSONDoesNotFakeDeltas(t *testing.T) {
	disconnected := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "json") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"whole reply"}]}]}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		writeStreamEvent(w, map[string]string{"type": "response.output_text.delta", "delta": "first"})
		<-r.Context().Done()
		close(disconnected)
	}))
	defer server.Close()
	cfg := DefaultSettings()
	cfg.Model, cfg.Endpoint, cfg.AllowPrivate, cfg.AllowInsecure = "fixture", server.URL, true, true
	model := HTTPModel{Client: func(bool) *http.Client { return server.Client() }}
	ctx, cancel := context.WithCancel(context.Background())
	_, err := model.Stream(ctx, cfg, "", nil, nil, func(string) error { cancel(); return ctx.Err() })
	if !errors.Is(err, context.Canceled) {
		t.Fatal("stream ignored cancellation", err)
	}
	select {
	case <-disconnected:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream connection remained open")
	}
	cfg.Endpoint = server.URL + "/json"
	callbacks := 0
	reply, err := model.Stream(context.Background(), cfg, "", nil, nil, func(string) error { callbacks++; return nil })
	if err != nil || reply.Text != "whole reply" || callbacks != 0 {
		t.Fatal("JSON fallback fabricated token streaming", reply, err)
	}
}
