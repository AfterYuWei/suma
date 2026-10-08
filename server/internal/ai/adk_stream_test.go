package ai

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/database"
)

type blockedStreamModel struct {
	text    string
	release chan struct{}
	done    chan struct{}
}

func (m *blockedStreamModel) Complete(context.Context, Settings, string, []ModelMessage, []Tool) (ModelReply, error) {
	return ModelReply{Text: `{"general":false}`}, nil
}
func (m *blockedStreamModel) Stream(ctx context.Context, _ Settings, _ string, _ []ModelMessage, _ []Tool, emit func(string) error) (ModelReply, error) {
	defer close(m.done)
	// Split known keys, quoted credentials and private keys across actual chunks.
	for _, delta := range []string{strings.Repeat("节点状态 / node status available. ", 12), "model-", "secret PASSWORD=\"private left ", "right\"\n-----BEGIN RSA PRIVATE KEY-----\nprivate key fragment\n", "-----END RSA PRIVATE KEY-----\n"} {
		if err := emit(delta); err != nil {
			return ModelReply{}, err
		}
		m.text += delta
	}
	select {
	case <-ctx.Done():
		return ModelReply{}, ctx.Err()
	case <-m.release:
	}
	m.text += "\n完整结果 / complete result"
	if err := emit("\n完整结果 / complete result"); err != nil {
		return ModelReply{}, err
	}
	return ModelReply{Text: m.text, Tokens: 23}, nil
}

func waitStreamOutput(t *testing.T, service *Service, runID string) []database.AIWorkflowEvent {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var rows []database.AIWorkflowEvent
		if err := service.db.Where("run_id = ? AND type = ?", runID, "run.output").Order("seq ASC").Find(&rows).Error; err != nil {
			t.Fatal(err)
		}
		if len(rows) > 0 {
			return rows
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Eino did not publish an answer before the model completed")
	return nil
}

func TestEinoStreamsBeforeCompletionAndRedactsAcrossChunks(t *testing.T) {
	s, _, _ := aiFixture(t)
	model := &blockedStreamModel{release: make(chan struct{}), done: make(chan struct{})}
	s.deps.Model = model
	defer func() {
		select {
		case <-model.release:
		default:
			close(model.release)
		}
	}()
	run, err := s.Start(context.Background(), RunInput{NodeID: "local", Question: "查询 Local 节点"}, Actor{UserID: 1})
	if err != nil {
		t.Fatal(err)
	}
	rows := waitStreamOutput(t, s, run.ID)
	current, err := s.Run(context.Background(), run.ID)
	if err != nil || current.Status != "running" {
		t.Fatal("no output while model is still running", current.Status, err)
	}
	for _, row := range rows {
		for _, private := range []string{"model-secret", "model-secr", "private left", "private key fragment"} {
			if strings.Contains(row.PayloadJSON, private) {
				t.Fatal("stream leaked a split secret", private)
			}
		}
	}
	close(model.release)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		current, err = s.Run(context.Background(), run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status != "running" && current.Status != "queued" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if current.Status != "completed" || !strings.Contains(current.Result.Summary, "complete result") || !strings.Contains(current.Result.Summary, "REDACTED") {
		t.Fatal("stream result did not commit safely", current.Status, current.Error, current.Result.Summary)
	}
	if current.Tokens != 23 {
		t.Fatal("stream token budget was lost", current.Tokens)
	}
	var all []database.AIWorkflowEvent
	s.db.Where("run_id = ?", run.ID).Find(&all)
	encoded, _ := json.Marshal(all)
	for _, private := range []string{"model-secret", "private left", "private key fragment"} {
		if strings.Contains(string(encoded), private) {
			t.Fatal("persisted stream exposed credentials")
		}
	}
}

func TestIdleModelStreamStopsAfterChatIdentityRevocation(t *testing.T) {
	s, _, _ := aiFixture(t)
	var valid atomic.Bool
	valid.Store(true)
	s.deps.ActorValid = func(context.Context, Actor) error {
		if !valid.Load() {
			return ErrScope
		}
		return nil
	}
	model := &blockedStreamModel{release: make(chan struct{}), done: make(chan struct{})}
	s.deps.Model = model
	run, err := s.Start(context.Background(), RunInput{NodeID: "local", Question: "inspect Local"}, Actor{UserID: 1, Source: "chat", BindingID: "binding", ExternalUserID: "tenant:user", ChatID: "oc_private"})
	if err != nil {
		t.Fatal(err)
	}
	waitStreamOutput(t, s, run.ID)
	valid.Store(false)
	select {
	case <-model.done:
	case <-time.After(3 * time.Second):
		t.Fatal("idle model kept its upstream open after revocation")
	}
}
