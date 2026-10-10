package ai

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/suma/suma/server/internal/database"
	"reflect"
	"strings"
	"testing"
)

func TestMultiNodeTaskRequiresExplicitRequestAndFreezesTargetSet(t *testing.T) {
	s, _, _ := aiFixture(t)
	s.db.Create(&database.Node{ID: "remote", Name: "Remote", Enabled: true})
	cfg := s.Settings()
	cfg.NodeIDs = []string{"local", "remote"}
	if _, err := s.SaveSettings(context.Background(), SettingsInput{Settings: cfg}, Actor{UserID: 1}); err != nil {
		t.Fatal(err)
	}
	s.deps.Model = modelFunc(func(_ context.Context, _ Settings, _ string, messages []ModelMessage, _ []Tool) (ModelReply, error) {
		if !strings.Contains(marshal(messages), `remote`) {
			t.Error("fixed target scope missing")
		}
		return ModelReply{Text: "Compared requested nodes"}, nil
	})
	run, err := s.Start(context.Background(), RunInput{Question: "Compare all nodes"}, Actor{UserID: 1})
	if err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, s, run.ID, "completed")
	if !reflect.DeepEqual(done.TargetNodeIDs, []string{"local", "remote"}) {
		t.Fatal(done.TargetNodeIDs)
	}
	s.db.Create(&database.Node{ID: "new-node", Name: "New", Enabled: true})
	same, _ := s.Run(context.Background(), run.ID)
	if len(same.TargetNodeIDs) != 2 {
		t.Fatal("new node automatically added")
	}
}
func TestToolCannotExpandScopeAndNewConversationStartsWithoutTarget(t *testing.T) {
	s, _, _ := aiFixture(t)
	s.db.Create(&database.Node{ID: "remote", Name: "Remote", Enabled: true})
	cfg := s.Settings()
	cfg.NodeIDs = []string{"local", "remote"}
	s.SaveSettings(context.Background(), SettingsInput{Settings: cfg}, Actor{UserID: 1})
	reads := 0
	s.deps.Read = func(context.Context, string, string, ToolArgs, int, int) (Evidence, error) {
		reads++
		return Evidence{}, nil
	}
	s.deps.Model = modelFunc(func(context.Context, Settings, string, []ModelMessage, []Tool) (ModelReply, error) {
		return ModelReply{Calls: []ToolCall{{ID: "escape", Name: "read_status", Arguments: json.RawMessage(`{"node_id":"remote","kind":"node","id":""}`)}}}, nil
	})
	run, err := s.Start(context.Background(), RunInput{NodeID: "local", Question: "Check status"}, Actor{UserID: 1})
	if err != nil {
		t.Fatal(err)
	}
	failed := waitRun(t, s, run.ID, "failed")
	if reads != 0 || failed.Error == "" {
		t.Fatal("scope expansion reached Docker")
	}
	s.deps.Model = targetModel{}
	fresh, err := s.Start(context.Background(), RunInput{Question: "Check resource status"}, Actor{UserID: 1})
	if err != nil {
		t.Fatal(err)
	}
	waiting := waitRun(t, s, fresh.ID, "waiting_input")
	if len(waiting.TargetNodeIDs) != 0 {
		t.Fatal("new conversation inherited a node")
	}
}
func TestPerTurnModelDoesNotChangeDefaultAndResumeKeepsModel(t *testing.T) {
	s, _, _ := aiFixture(t)
	cfg := s.Settings()
	cfg.Models = []string{"test", "alternate"}
	s.SaveSettings(context.Background(), SettingsInput{Settings: cfg}, Actor{UserID: 1})
	s.deps.Model = modelFunc(func(_ context.Context, cfg Settings, _ string, _ []ModelMessage, defs []Tool) (ModelReply, error) {
		if len(defs) == 1 && defs[0].Name == "connection_probe" {
			return ModelReply{Calls: []ToolCall{{ID: "probe", Name: "connection_probe", Arguments: json.RawMessage(`{"message":"suma_connection_test"}`)}}}, nil
		}
		if len(defs) == 0 {
			return ModelReply{Text: `{"general":false,"candidate_node_ids":[],"reuse_context":true}`}, nil
		}
		return ModelReply{Text: "Model " + cfg.Model}, nil
	})
	actor := Actor{UserID: 1, Source: "site"}
	first, err := s.Start(context.Background(), RunInput{NodeID: "local", Question: "inspect", Model: "alternate"}, actor)
	if err != nil {
		t.Fatal(err)
	}
	waitRun(t, s, first.ID, "completed")
	second, err := s.PostMessage(context.Background(), first.ConversationID, MessageInput{Question: "continue", Model: "test", RequestID: "model-next"}, actor)
	if err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, s, second.ID, "completed")
	if done.Model != "test" || s.Settings().Model != "test" || first.Model != "alternate" {
		t.Fatal("per-turn model contaminated default")
	}
}
func TestConversationOwnershipAndRevokedScopeFailClosed(t *testing.T) {
	s, _, _ := aiFixture(t)
	run := completedRun(t, s)
	s.db.Create(&database.User{ID: 2, Username: "other", PasswordHash: "test"})
	if _, err := s.RunAs(context.Background(), run.ID, Actor{UserID: 2}); !errors.Is(err, ErrScope) {
		t.Fatal("foreign run exposed", err)
	}
	cfg := s.Settings()
	cfg.NodeIDs = []string{}
	s.SaveSettings(context.Background(), SettingsInput{Settings: cfg}, Actor{UserID: 1})
	if _, err := s.AnswerInput(context.Background(), run.ID, InputAnswer{InteractionID: run.Interaction.ID, RequestID: "revoked", ExpectedRevision: run.Revision, Values: []string{"local"}}, Actor{UserID: 1}); err == nil {
		t.Fatal("revoked task resumed")
	}
}
