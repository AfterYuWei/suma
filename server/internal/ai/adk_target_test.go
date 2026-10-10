package ai

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/suma/suma/server/internal/database"
)

func TestADKMisclassifiedQueryClarifiesBeforeAnyDockerAccess(t *testing.T) {
	s, executions, _ := aiFixture(t)
	if err := s.db.Model(&database.Node{}).Where("id = ?", "local").Update("name", "Alibaba Cloud").Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []database.Node{{ID: "foreign", Name: "Private", Enabled: true}, {ID: "disabled", Name: "Disabled", Enabled: false}} {
		if err := s.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	var reads atomic.Int32
	var freezes atomic.Int32
	s.deps.Freeze = func(context.Context, string, OperationRequest) (Snapshot, error) {
		freezes.Add(1)
		return Snapshot{}, errors.New("unverified mutation reached preview")
	}
	s.deps.Resources = func(_ context.Context, node string, args ToolArgs) ([]ResourceOption, error) {
		reads.Add(1)
		if node != "local" || args.ID != "" || args.Query != "" {
			t.Error("unverified arguments were replayed", node, args)
		}
		return []ResourceOption{{ID: "full-container-id", Name: "nginx", Kind: "container", NodeID: node}}, nil
	}
	s.deps.Model = modelFunc(func(_ context.Context, _ Settings, _ string, messages []ModelMessage, tools []Tool) (ModelReply, error) {
		if len(tools) == 0 {
			// A provider can incorrectly classify a real resource query as general.
			return ModelReply{Text: `{"general":true}`}, nil
		}
		for i := len(messages) - 1; i >= 0; i-- {
			message := messages[i]
			if message.Role != "tool" {
				continue
			}
			if strings.Contains(message.Text, "full-container-id") {
				return ModelReply{Text: "Alibaba Cloud has nginx."}, nil
			}
			if strings.Contains(message.Text, "target_node_ids") {
				return ModelReply{Calls: []ToolCall{{ID: "verified-read", Name: "list_resources", Arguments: json.RawMessage(`{"node_id":"local","kind":"container","id":"","query":"","cursor":""}`)}}}, nil
			}
		}
		return ModelReply{Calls: []ToolCall{{ID: "unverified-read", Name: "list_resources", Arguments: json.RawMessage(`{"node_id":"invented","kind":"container","id":"stale-id","query":"stale-name","cursor":""}`)}, {ID: "unverified-write", Name: "create_proposal", Arguments: json.RawMessage(`{"node_id":"local","action":"container.restart","resource_id":"stale-id","parameters":{}}`)}}}, nil
	})
	actor := Actor{UserID: 1, Source: "site"}
	run, err := s.Start(context.Background(), RunInput{Question: "阿里云节点有哪些容器？"}, actor)
	if err != nil {
		t.Fatal(err)
	}
	waiting := waitRun(t, s, run.ID, "waiting_input")
	if reads.Load() != 0 || freezes.Load() != 0 || executions.Load() != 0 || len(waiting.TargetNodeIDs) != 0 || waiting.Interaction == nil || waiting.Interaction.Kind != "node" || len(waiting.Interaction.Options) != 1 || waiting.Interaction.Options[0].ID != "local" {
		t.Fatal("missing target did not become a safe node selection", waiting, reads.Load())
	}
	answer := InputAnswer{InteractionID: waiting.Interaction.ID, ExpectedRevision: waiting.Revision, RequestID: "select-target", Values: []string{"foreign"}}
	if _, err = s.AnswerInput(context.Background(), run.ID, answer, actor); err == nil {
		t.Fatal("unlisted node was selected")
	}
	answer.Values = []string{"local"}
	if err := s.db.Model(&database.Node{}).Where("id = ?", "local").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.AnswerInput(context.Background(), run.ID, answer, actor); !errors.Is(err, ErrScope) {
		t.Fatal("a node disabled after clarification was selected", err)
	}
	if err := s.db.Model(&database.Node{}).Where("id = ?", "local").Update("enabled", true).Error; err != nil {
		t.Fatal(err)
	}
	// Exercise the same checkpoint after a service restart.
	s.Stop()
	resumed, err := NewService(s.db, s.secrets, s.tasks, s.deps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(resumed.Stop)
	answer.Values = []string{"local"}
	if _, err = resumed.AnswerInput(context.Background(), run.ID, answer, actor); err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, resumed, run.ID, "completed")
	if reads.Load() != 1 || freezes.Load() != 0 || executions.Load() != 0 || !reflect.DeepEqual(done.TargetNodeIDs, []string{"local"}) || done.TargetSource != "selection" || done.Error != "" || len(done.Result.OperationIDs) != 0 {
		t.Fatal("selection did not resume a fresh bounded read", done, reads.Load())
	}
	var audited int64
	if err := s.db.Model(&database.AuditLog{}).Where("run_id = ? AND action = ?", run.ID, "ai.list_resources").Count(&audited).Error; err != nil || audited != 1 {
		t.Fatal("selection was falsely audited as a Docker read", audited, err)
	}
}

func TestADKIncorrectToolNodeCanBeCorrectedWithoutExpandingTargets(t *testing.T) {
	for _, wrong := range []string{"阿里云", "remote", "invented"} {
		t.Run(wrong, func(t *testing.T) {
			s, _, _ := aiFixture(t)
			if err := s.db.Model(&database.Node{}).Where("id = ?", "local").Update("name", "阿里云").Error; err != nil {
				t.Fatal(err)
			}
			if err := s.db.Create(&database.Node{ID: "remote", Name: "Other", Enabled: true}).Error; err != nil {
				t.Fatal(err)
			}
			cfg := s.Settings()
			cfg.NodeIDs = []string{"local", "remote"}
			if _, err := s.SaveSettings(context.Background(), SettingsInput{Settings: cfg}, Actor{UserID: 1}); err != nil {
				t.Fatal(err)
			}
			var reads atomic.Int32
			s.deps.Resources = func(_ context.Context, node string, _ ToolArgs) ([]ResourceOption, error) {
				reads.Add(1)
				if node != "local" {
					t.Error("tool expanded the task target", node)
				}
				return []ResourceOption{{ID: "full-container-id", Name: "nginx", NodeID: node, Kind: "container"}}, nil
			}
			s.deps.Model = modelFunc(func(_ context.Context, _ Settings, _ string, messages []ModelMessage, _ []Tool) (ModelReply, error) {
				for i := len(messages) - 1; i >= 0; i-- {
					message := messages[i]
					if message.Role != "tool" {
						continue
					}
					if strings.Contains(message.Text, "full-container-id") {
						return ModelReply{Text: "阿里云节点有 nginx。"}, nil
					}
					if strings.Contains(message.Text, "invalid_node_id") {
						return ModelReply{Calls: []ToolCall{{ID: "corrected", Name: "list_resources", Arguments: json.RawMessage(`{"node_id":"local","kind":"container","id":""}`)}}}, nil
					}
				}
				args, _ := json.Marshal(ToolArgs{NodeID: wrong, Kind: "container"})
				return ModelReply{Calls: []ToolCall{{ID: "wrong", Name: "list_resources", Arguments: args}}}, nil
			})
			run, err := s.Start(context.Background(), RunInput{Question: "阿里云节点有哪些容器？"}, Actor{UserID: 1})
			if err != nil {
				t.Fatal(err)
			}
			done := waitRun(t, s, run.ID, "completed")
			if reads.Load() != 1 || !reflect.DeepEqual(done.TargetNodeIDs, []string{"local"}) || done.TargetSource != "explicit" || done.Reads != 1 || done.Error != "" {
				t.Fatal("incorrect tool ID did not recover within the verified target", done, reads.Load())
			}
			var audited int64
			if err := s.db.Model(&database.AuditLog{}).Where("run_id = ? AND action = ?", run.ID, "ai.list_resources").Count(&audited).Error; err != nil || audited != 1 {
				t.Fatal("rejected call counted as a successful read", audited, err)
			}
		})
	}
}

func TestADKActualRuntimeScopeDenialStillStops(t *testing.T) {
	s, _, _ := aiFixture(t)
	s.deps.Model = targetModel{}
	s.deps.Read = func(context.Context, string, string, ToolArgs, int, int) (Evidence, error) {
		return Evidence{}, ErrScope
	}
	run, err := s.Start(context.Background(), RunInput{NodeID: "local", Question: "Check status"}, Actor{UserID: 1})
	if err != nil {
		t.Fatal(err)
	}
	failed := waitRun(t, s, run.ID, "failed")
	if !strings.Contains(failed.Error, "Settings") || strings.Contains(failed.Error, "NodeRunError") || len(failed.Result.Evidence) != 0 {
		t.Fatal("runtime permission failure was treated as a parameter correction", failed)
	}
	if _, err := s.Start(context.Background(), RunInput{NodeID: "foreign", Question: "Check status"}, Actor{UserID: 1}); !errors.Is(err, ErrScope) {
		t.Fatal("scope authorization was broadened", err)
	}
}

func TestADKUnavailableNodeGivesAuthorizationStepsWithoutDocker(t *testing.T) {
	for _, general := range []bool{false, true} {
		name := "classified_as_resource"
		if general {
			name = "classified_as_general"
		}
		t.Run(name, func(t *testing.T) {
			s, _, _ := aiFixture(t)
			if err := s.db.Model(&database.Node{}).Where("id = ?", "local").Update("enabled", false).Error; err != nil {
				t.Fatal(err)
			}
			s.deps.Resources = func(context.Context, string, ToolArgs) ([]ResourceOption, error) {
				t.Error("unavailable node reached a Docker adapter")
				return nil, ErrScope
			}
			s.deps.Model = modelFunc(func(_ context.Context, _ Settings, _ string, _ []ModelMessage, defs []Tool) (ModelReply, error) {
				if len(defs) == 0 {
					text, _ := json.Marshal(map[string]bool{"general": general})
					return ModelReply{Text: string(text)}, nil
				}
				return ModelReply{Calls: []ToolCall{{ID: "unavailable-list", Name: "list_resources", Arguments: json.RawMessage(`{"node_id":"local","kind":"container","id":""}`)}}}, nil
			})
			run, err := s.Start(context.Background(), RunInput{Question: "阿里云节点有哪些容器？"}, Actor{UserID: 1})
			if err != nil {
				t.Fatal(err)
			}
			failed := waitRun(t, s, run.ID, "failed")
			if failed.Error != errNoAvailableTarget.Error() || failed.Interaction != nil || failed.Reads != 0 {
				t.Fatal("unavailable node did not provide actionable guidance", failed)
			}
		})
	}
}
