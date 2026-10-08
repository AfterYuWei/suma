package notification

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
)

type streamHTTPCall struct {
	method, path string
	body         map[string]any
}
type streamHTTPFixture struct {
	mu                                       sync.Mutex
	calls                                    []streamHTTPCall
	denyCard, denyPatch, failSend, failClose bool
	sequence                                 uint64
}

func (f *streamHTTPFixture) transport(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var body map[string]any
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return nil, err
		}
	}
	f.calls = append(f.calls, streamHTTPCall{r.Method, r.URL.Path, body})
	switch {
	case strings.Contains(r.URL.Path, "tenant_access_token"):
		return response(200, `{"code":0,"tenant_access_token":"tenant-token-fixture","expire":3600}`), nil
	case r.URL.Path == "/open-apis/cardkit/v1/cards":
		if f.denyCard {
			return response(200, `{"code":99991672,"msg":"missing card permission"}`), nil
		}
		return response(200, `{"code":0,"data":{"card_id":"card_fixture"}}`), nil
	case r.Method == "POST" && r.URL.Path == "/open-apis/im/v1/messages":
		if f.failSend {
			f.failSend = false
			return response(503, `{}`), nil
		}
		return response(200, `{"code":0,"data":{"message_id":"om_fixture"}}`), nil
	case strings.Contains(r.URL.Path, "cardkit"):
		sequence := uint64(body["sequence"].(float64))
		if sequence <= f.sequence {
			return response(400, `{"code":123,"msg":"out of order"}`), nil
		}
		f.sequence = sequence
		if strings.HasSuffix(r.URL.Path, "/settings") && f.failClose {
			f.failClose = false
			return response(503, `{}`), nil
		}
		return response(200, `{"code":0}`), nil
	case r.Method == "PATCH" && strings.Contains(r.URL.Path, "/im/v1/messages/"):
		if f.denyPatch {
			return response(200, `{"code":99991672,"msg":"missing update permission"}`), nil
		}
		return response(200, `{"code":0}`), nil
	}
	return nil, errors.New("unexpected stream API")
}
func (f *streamHTTPFixture) adapter() *Adapter {
	a := NewAdapter()
	a.Client = func(bool) *http.Client { return &http.Client{Transport: roundTrip(f.transport)} }
	return a
}
func (f *streamHTTPFixture) counts(path string) int {
	n := 0
	for _, call := range f.calls {
		if call.path == path {
			n++
		}
	}
	return n
}

func TestFeishuStreamingUpdatesOneCardAndClosesAfterRetry(t *testing.T) {
	f := &streamHTTPFixture{}
	a := f.adapter()
	c := Channel{Config: Config{AppID: "cli_stream", ChatID: "oc_private"}}
	c.Provider = "feishu_app"
	m := Secrets{Token: "app-secret-fixture"}
	receipt, err := a.UpdateStream(context.Background(), c, m, "stable-nonce", StreamReceipt{}, StreamUpdate{Status: "正在生成 / Generating"})
	if err != nil || receipt.CardID != "card_fixture" || receipt.MessageID != "om_fixture" {
		t.Fatal("stream card did not start", receipt, err)
	}
	for _, text := range []string{"## ganzhou\n**online**", "## ganzhou\n**online**\nDocker 29.5", "### Final answer\n`online` <at user_id=\"all\">"} {
		receipt, err = a.UpdateStream(context.Background(), c, m, "stable-nonce", receipt, StreamUpdate{Text: text, Status: "生成中 / Generating"})
		if err != nil {
			t.Fatal(err)
		}
	}
	f.failClose = true
	final := StreamUpdate{Text: "### Final answer\n`online` <at user_id=\"all\">", Status: "完成 / Completed", Final: true}
	receipt, err = a.UpdateStream(context.Background(), c, m, "stable-nonce", receipt, final)
	if err == nil || receipt.Closed {
		t.Fatal("failed close was reported as completion", err)
	}
	failedSequence := receipt.Sequence
	receipt, err = a.UpdateStream(context.Background(), c, m, "stable-nonce", receipt, final)
	if err != nil || !receipt.Closed || receipt.Sequence <= failedSequence {
		t.Fatal("stream did not recover with a newer sequence", receipt, err)
	}
	if f.counts("/open-apis/cardkit/v1/cards") != 1 || f.counts("/open-apis/im/v1/messages") != 1 {
		t.Fatal("stream or retry created extra cards/messages")
	}
	var created map[string]any
	for _, call := range f.calls {
		if call.path == "/open-apis/cardkit/v1/cards" {
			_ = json.Unmarshal([]byte(call.body["data"].(string)), &created)
		}
		if raw, ok := call.body["content"].(string); ok && strings.Contains(call.path, "elements/content/content") && strings.Contains(raw, "ganzhou") && !strings.Contains(raw, "**online**") {
			t.Fatal("stream flattened Markdown")
		}
		if strings.HasSuffix(call.path, "/elements/content") {
			var element map[string]string
			_ = json.Unmarshal([]byte(call.body["element"].(string)), &element)
			if strings.Contains(element["content"], " <at ") {
				t.Fatal("model text created a live platform mention")
			}
		}
	}
	config := created["config"].(map[string]any)
	if config["streaming_mode"] != true || config["update_multi"] != true {
		t.Fatal("CardKit was not configured as a shared streaming card")
	}
}

func TestFeishuStreamingRetainsCardAndSendUUIDAfterAnUncertainSend(t *testing.T) {
	f := &streamHTTPFixture{failSend: true}
	a := f.adapter()
	c := Channel{Config: Config{AppID: "cli_stream", ChatID: "oc_private"}}
	c.Provider = "feishu_app"
	receipt, err := a.UpdateStream(context.Background(), c, Secrets{Token: "fixture"}, "nonce", StreamReceipt{}, StreamUpdate{Status: "working"})
	if err == nil || receipt.CardID == "" || receipt.MessageID != "" {
		t.Fatal("uncertain send lost its known card", receipt, err)
	}
	receipt, err = a.UpdateStream(context.Background(), c, Secrets{Token: "fixture"}, "nonce", receipt, StreamUpdate{Text: "new answer", Status: "done", Final: true})
	if err != nil || !receipt.Closed || receipt.Text != "new answer" {
		t.Fatal("retry did not update the retained card", receipt, err)
	}
	if f.counts("/open-apis/cardkit/v1/cards") != 1 {
		t.Fatal("send retry created a new entity")
	}
	var uuids []string
	for _, call := range f.calls {
		if call.method == "POST" && call.path == "/open-apis/im/v1/messages" {
			uuids = append(uuids, call.body["uuid"].(string))
		}
	}
	if len(uuids) != 2 || uuids[0] != uuids[1] {
		t.Fatal("uncertain send did not retain its idempotency key")
	}
}

func TestFeishuStreamingPermissionFallbackKeepsTheFinalAnswer(t *testing.T) {
	for _, denyPatch := range []bool{false, true} {
		t.Run(map[bool]string{false: "same message", true: "complete reply"}[denyPatch], func(t *testing.T) {
			f := &streamHTTPFixture{denyCard: true, denyPatch: denyPatch}
			a := f.adapter()
			c := Channel{Config: Config{AppID: "cli_stream", ChatID: "oc_private"}}
			c.Provider = "feishu_app"
			receipt, err := a.UpdateStream(context.Background(), c, Secrets{Token: "fixture"}, "nonce", StreamReceipt{}, StreamUpdate{Status: "working"})
			if err != nil {
				t.Fatal(err)
			}
			receipt, err = a.UpdateStream(context.Background(), c, Secrets{Token: "fixture"}, "nonce", receipt, StreamUpdate{Text: "**actual final answer**", Status: "done", Final: true})
			if err != nil || !receipt.Closed {
				t.Fatal("permission fallback lost the final result", receipt, err)
			}
			want := 1
			if denyPatch {
				want = 2
			}
			if f.counts("/open-apis/im/v1/messages") != want {
				t.Fatal("fallback duplicated the final response")
			}
			last := f.calls[len(f.calls)-1].body["content"].(string)
			if !strings.Contains(last, "actual final answer") || denyPatch && !strings.Contains(last, "cardkit:card:write") {
				t.Fatal("fallback omitted the answer or actionable permissions")
			}
		})
	}
}
