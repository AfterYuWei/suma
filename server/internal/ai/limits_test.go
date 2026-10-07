package ai

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/database"
)

type modelFunc func(context.Context, Settings, string, []ModelMessage, []Tool) (ModelReply, error)

func (f modelFunc) Complete(ctx context.Context, cfg Settings, key string, messages []ModelMessage, tools []Tool) (ModelReply, error) {
	return f(ctx, cfg, key, messages, tools)
}

func TestConversationReadsAreBoundedAndCarryPriorContext(t *testing.T) {
	s, _, _ := aiFixture(t)
	parent := completedRun(t, s)
	var calls atomic.Int32
	var reads atomic.Int32
	s.deps.Read = func(context.Context, string, string, ToolArgs, int, int) (Evidence, error) {
		reads.Add(1)
		return Evidence{Content: strings.Repeat("token=hidden-log-secret\n", 1000)}, nil
	}
	s.deps.Model = modelFunc(func(_ context.Context, _ Settings, _ string, messages []ModelMessage, _ []Tool) (ModelReply, error) {
		calls.Add(1)
		joined := marshal(messages)
		if !strings.Contains(joined, parent.Question) || !strings.Contains(joined, parent.Result.Summary) {
			t.Error("conversation context missing")
		}
		return ModelReply{Calls: []ToolCall{{ID: "read", Name: "read_logs", Arguments: json.RawMessage(`{"kind":"container","id":"frozen-container"}`)}}}, nil
	})
	run, err := s.Start(context.Background(), RunInput{NodeID: "local", Question: "What should I check next?", ConversationID: parent.ConversationID}, Actor{UserID: 1})
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		latest, _ := s.Run(context.Background(), run.ID)
		if latest.Status != "running" && latest.Status != "queued" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if reads.Load() != int32(s.Settings().MaxToolCalls) || calls.Load() > int32(s.Settings().MaxIterations) {
		t.Fatal("unbounded reads/iterations", reads.Load(), calls.Load())
	}
	latest, _ := s.Run(context.Background(), run.ID)
	for _, evidence := range latest.Result.Evidence {
		if strings.Count(evidence.Content, "\n")+1 > s.Settings().LogLines || len(evidence.Content) > s.Settings().LogBytes || strings.Contains(evidence.Content, "hidden-log-secret") {
			t.Fatal("oversized or unredacted multiline tool response reached the model")
		}
	}
}

func TestScopeRevocationCancelsDiagnosisAndInvalidatesPendingApproval(t *testing.T) {
	s, count, _ := aiFixture(t)
	parent := completedRun(t, s)
	op, _ := s.Operation(context.Background(), parent.Result.OperationIDs[0])
	s.db.Create(&database.Node{ID: "other", Name: "Other", Enabled: true})
	started := make(chan struct{})
	s.deps.Model = modelFunc(func(ctx context.Context, _ Settings, _ string, _ []ModelMessage, _ []Tool) (ModelReply, error) {
		close(started)
		<-ctx.Done()
		return ModelReply{}, ctx.Err()
	})
	run, err := s.Start(context.Background(), RunInput{NodeID: "local", Question: "Slow diagnosis"}, Actor{UserID: 1})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	cfg := s.Settings()
	cfg.NodeIDs = []string{"other"}
	if _, err := s.SaveSettings(context.Background(), SettingsInput{Settings: cfg}, Actor{UserID: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decide(context.Background(), op.ID, Decision{RequestID: id(), Approve: true, ReviewToken: op.ReviewToken}, Actor{UserID: 1}); err == nil {
		t.Fatal("revoked node approval allowed")
	}
	until := time.Now().Add(2 * time.Second)
	for time.Now().Before(until) {
		latest, _ := s.Run(context.Background(), run.ID)
		if latest.Status == "paused" {
			if count.Load() != 0 {
				t.Fatal("mutation executed")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("revoked diagnosis not canceled")
}

func TestAutoBudgetUsesUTCAndRevokedBindingNeverApproves(t *testing.T) {
	s, count, _ := aiFixture(t)
	run := completedRun(t, s)
	op, _ := s.Operation(context.Background(), run.Result.OperationIDs[0])
	s.deps.ActorValid = func(_ context.Context, a Actor) error {
		if a.BindingID != "" {
			return ErrScope
		}
		return nil
	}
	if _, err := s.Decide(context.Background(), op.ID, Decision{RequestID: id(), Approve: true, ReviewToken: op.ReviewToken}, Actor{UserID: 1, BindingID: "revoked", Source: "chat"}); !errors.Is(err, ErrScope) {
		t.Fatal(err)
	}
	if count.Load() != 0 {
		t.Fatal("revoked chat executed")
	}
	s.deps.Now = func() time.Time { return time.Date(2026, 10, 4, 1, 0, 0, 0, time.FixedZone("UTC+8", 8*3600)).UTC() }
	s.db.Create(&database.AIRun{ID: "auto-before-midnight", NodeID: "local", UserID: 1, Source: "auto", Status: "completed", CreatedAt: time.Date(2026, 10, 3, 16, 0, 0, 0, time.UTC)})
	cfg := s.Settings()
	cfg.DailyAutoLimit = 1
	if _, err := s.SaveSettings(context.Background(), SettingsInput{Settings: cfg}, Actor{UserID: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(context.Background(), RunInput{NodeID: "local", Question: "New anomaly"}, Actor{UserID: 1, Source: "auto"}); !errors.Is(err, ErrBudget) {
		t.Fatal("UTC budget reset at local midnight", err)
	}
}
