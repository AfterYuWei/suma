package ai

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/suma/suma/server/internal/database"
)

func TestADKSemanticCandidatesRequireConfirmationWithinAuthorizedDirectory(t *testing.T) {
	for _, check := range []struct {
		name, question, reply string
		want                  []string
	}{
		{"brand", "阿里云节点有哪些容器？", `{"general":false,"candidate_node_ids":["local"]}`, []string{"local"}},
		{"translation", "Alibaba Cloud 的容器有哪些？", `{"general":false,"candidate_node_ids":["local"]}`, []string{"local"}},
		{"typo", "aluyun 节点有哪些容器？", `{"general":false,"candidate_node_ids":["local"]}`, []string{"local"}},
		{"multiple", "云服务器有哪些容器？", `{"general":false,"candidate_node_ids":["remote","local"]}`, []string{"remote", "local"}},
		{"duplicate", "阿里云节点有哪些容器？", `{"general":false,"candidate_node_ids":["local","local"]}`, []string{"local"}},
		{"unknown_and_disabled", "阿里云节点有哪些容器？", `{"general":true,"candidate_node_ids":["foreign","disabled","invented"]}`, []string{}},
		{"malformed", "阿里云节点有哪些容器？", `{"general":false,"candidate_node_ids":"local"}`, []string{}},
		{"unrelated_fields", "阿里云节点有哪些容器？", `{"general":false,"candidate_node_ids":["local"],"execute":true}`, []string{}},
	} {
		t.Run(check.name, func(t *testing.T) {
			s, executions, _ := aiFixture(t)
			if err := s.db.Model(&database.Node{}).Where("id = ?", "local").Update("name", "aliyun").Error; err != nil {
				t.Fatal(err)
			}
			for _, row := range []database.Node{{ID: "remote", Name: "Hetzner", Enabled: true}, {ID: "foreign", Name: "PrivateForeign", Enabled: true}, {ID: "disabled", Name: "PrivateDisabled", Enabled: true}} {
				if err := s.db.Create(&row).Error; err != nil {
					t.Fatal(err)
				}
			}
			cfg := s.Settings()
			cfg.NodeIDs = []string{"local", "remote", "disabled"}
			if _, err := s.SaveSettings(context.Background(), SettingsInput{Settings: cfg}, Actor{UserID: 1}); err != nil {
				t.Fatal(err)
			}
			if err := s.db.Model(&database.Node{}).Where("id = ?", "disabled").Update("enabled", false).Error; err != nil {
				t.Fatal(err)
			}
			var reads, classifications atomic.Int32
			var readNode atomic.Value
			s.deps.Resources = func(_ context.Context, node string, _ ToolArgs) ([]ResourceOption, error) {
				reads.Add(1)
				readNode.Store(node)
				return []ResourceOption{{ID: "full-container-id", Name: "nginx", NodeID: node, Kind: "container"}}, nil
			}
			s.deps.Model = modelFunc(func(_ context.Context, _ Settings, _ string, messages []ModelMessage, tools []Tool) (ModelReply, error) {
				if len(tools) == 0 {
					classifications.Add(1)
					data := messages[1].Text
					if !strings.Contains(data, `"name":"aliyun"`) || strings.Contains(data, "PrivateForeign") || strings.Contains(data, "PrivateDisabled") {
						t.Error("semantic resolver did not receive only the enabled authorized directory")
					}
					return ModelReply{Text: check.reply}, nil
				}
				for i := len(messages) - 1; i >= 0; i-- {
					if messages[i].Role == "tool" {
						return ModelReply{Text: "Confirmed containers: " + messages[i].Text}, nil
					}
				}
				return ModelReply{Calls: []ToolCall{{ID: "confirmed-read", Name: "list_resources", Arguments: json.RawMessage(`{"node_id":"","kind":"container","id":""}`)}}}, nil
			})
			actor := Actor{UserID: 1, Source: "site"}
			run, err := s.Start(context.Background(), RunInput{Question: check.question}, actor)
			if err != nil {
				t.Fatal(err)
			}
			waiting := waitRun(t, s, run.ID, "waiting_input")
			if reads.Load() != 0 || executions.Load() != 0 || classifications.Load() != 1 || len(waiting.TargetNodeIDs) != 0 || waiting.Interaction == nil || waiting.Interaction.Kind != "node" || waiting.Interaction.Multiple {
				t.Fatal("a semantic guess established a target or reached Docker", waiting, reads.Load())
			}
			got := []string{}
			for _, option := range waiting.Interaction.Options {
				if option.Suggested {
					got = append(got, option.ID)
				}
				if option.ID == "foreign" || option.ID == "disabled" {
					t.Fatal("unauthorized or disabled node shown in confirmation")
				}
			}
			if !reflect.DeepEqual(got, check.want) || len(waiting.Interaction.Options) != 2 {
				t.Fatal("wrong candidates or no way to reject the suggestion", got, waiting.Interaction.Options)
			}
			if len(check.want) == 1 && !strings.Contains(waiting.Interaction.Prompt, "你指的是 aliyun 节点吗") {
				t.Fatal("single semantic candidate was not phrased as a confirmation", waiting.Interaction.Prompt)
			}
			// Reject any guessed target by explicitly choosing the other allowed node.
			if _, err := s.AnswerInput(context.Background(), run.ID, InputAnswer{InteractionID: waiting.Interaction.ID, ExpectedRevision: waiting.Revision, RequestID: "choose-other", Values: []string{"remote"}}, actor); err != nil {
				t.Fatal(err)
			}
			done := waitRun(t, s, run.ID, "completed")
			if reads.Load() != 1 || readNode.Load() != "remote" || !reflect.DeepEqual(done.TargetNodeIDs, []string{"remote"}) || done.TargetSource != "selection" || len(done.Result.OperationIDs) != 0 {
				t.Fatal("confirmation did not route to the human-selected node", done, readNode.Load())
			}
		})
	}
}

func TestADKSemanticNewNodeDoesNotReusePreviousConversationTarget(t *testing.T) {
	s, _, _ := aiFixture(t)
	if err := s.db.Create(&database.Node{ID: "remote", Name: "aliyun", Enabled: true}).Error; err != nil {
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
		return []ResourceOption{{ID: "container-" + node, Name: "nginx", NodeID: node, Kind: "container"}}, nil
	}
	s.deps.Model = modelFunc(func(_ context.Context, _ Settings, _ string, messages []ModelMessage, tools []Tool) (ModelReply, error) {
		if len(tools) == 0 {
			// Even a contradictory reuse=true cannot bypass the new-node confirmation.
			return ModelReply{Text: `{"general":false,"candidate_node_ids":["remote"],"reuse_context":true}`}, nil
		}
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == "tool" {
				return ModelReply{Text: messages[i].Text}, nil
			}
		}
		return ModelReply{Calls: []ToolCall{{ID: "read", Name: "list_resources", Arguments: json.RawMessage(`{"node_id":"","kind":"container","id":""}`)}}}, nil
	})
	actor := Actor{UserID: 1}
	first, err := s.Start(context.Background(), RunInput{Question: "Local 节点有哪些容器？"}, actor)
	if err != nil {
		t.Fatal(err)
	}
	initial := waitRun(t, s, first.ID, "completed")
	if initial.NodeID != "local" || initial.TargetSource != "explicit" || reads.Load() != 1 {
		t.Fatal("exact node name required an unnecessary semantic confirmation", initial)
	}
	next, err := s.PostMessage(context.Background(), first.ConversationID, MessageInput{Question: "继续查阿里云节点有哪些容器？", RequestID: "new-alias"}, actor)
	if err != nil {
		t.Fatal(err)
	}
	waiting := waitRun(t, s, next.ID, "waiting_input")
	if reads.Load() != 1 || len(waiting.TargetNodeIDs) != 0 || waiting.Interaction == nil || waiting.Interaction.Options[0].ID != "remote" || !waiting.Interaction.Options[0].Suggested {
		t.Fatal("new semantic node silently inherited the prior target", waiting, reads.Load())
	}
	if _, err := s.AnswerInput(context.Background(), next.ID, InputAnswer{InteractionID: waiting.Interaction.ID, ExpectedRevision: waiting.Revision, RequestID: "yes-remote", Values: []string{"remote"}}, actor); err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, s, next.ID, "completed")
	if reads.Load() != 2 || done.NodeID != "remote" || !strings.Contains(done.Result.Summary, "container-remote") {
		t.Fatal("confirmed alias did not switch to the selected node", done)
	}
}
