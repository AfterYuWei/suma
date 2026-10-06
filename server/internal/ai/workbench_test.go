package ai

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/database"
)

func awaitWorkbenchRun(t *testing.T, s *Service, run Run) Run {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		current, err := s.Run(context.Background(), run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status != "running" {
			if current.Status != "completed" {
				t.Fatal(current.Status, current.Error)
			}
			return current
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("diagnosis timed out")
	return Run{}
}

func TestGlobalWorkbenchResolvesEveryReadAndProposalToAuthorizedRuntime(t *testing.T) {
	s, executions, _ := aiFixture(t)
	s.db.Create(&database.Node{ID: "remote", Name: "Remote", Enabled: true, Endpoint: "tcp://private-endpoint:2376"})
	s.db.Create(&database.Node{ID: "blocked", Name: "Forbidden name", Enabled: true})
	cfg := s.Settings()
	cfg.NodeIDs = []string{"local", "remote"}
	if _, err := s.SaveSettings(context.Background(), SettingsInput{Settings: cfg}, Actor{UserID: 1}); err != nil {
		t.Fatal(err)
	}
	var readNodes, frozenNodes []string
	s.deps.Read = func(_ context.Context, node, name string, args ToolArgs, _, _ int) (Evidence, error) {
		readNodes = append(readNodes, node)
		return Evidence{Source: name, Resource: args.ID, Content: node + " status"}, nil
	}
	originalFreeze := s.deps.Freeze
	s.deps.Freeze = func(ctx context.Context, node string, req OperationRequest) (Snapshot, error) {
		frozenNodes = append(frozenNodes, node)
		return originalFreeze(ctx, node, req)
	}
	var calls int
	s.deps.Model = modelFunc(func(_ context.Context, _ Settings, _ string, messages []ModelMessage, definitions []Tool) (ModelReply, error) {
		calls++
		if calls > 1 {
			return ModelReply{Text: "Compared both nodes; changes await review."}, nil
		}
		system := messages[1].Text
		if !strings.Contains(system, `"id":"remote"`) || strings.Contains(system, "private-endpoint") || strings.Contains(system, "Forbidden name") {
			t.Error("unsafe or incomplete node directory", system)
		}
		for _, definition := range definitions {
			required := definition.Parameters["required"].([]string)
			count := 0
			for _, name := range required {
				if name == "node_id" {
					count++
				}
			}
			if count != 1 {
				t.Error("global schema requires exactly one node ID", required)
			}
		}
		return ModelReply{Calls: []ToolCall{
			{ID: "local", Name: "read_status", Arguments: json.RawMessage(`{"node_id":"local","kind":"container","id":"same-id"}`)},
			{ID: "remote", Name: "read_status", Arguments: json.RawMessage(`{"node_id":"remote","kind":"container","id":"same-id"}`)},
			{ID: "missing", Name: "read_status", Arguments: json.RawMessage(`{"kind":"container","id":"same-id"}`)},
			{ID: "blocked", Name: "read_status", Arguments: json.RawMessage(`{"node_id":"blocked","kind":"container","id":"same-id"}`)},
			{ID: "proposal-local", Name: "create_proposal", Arguments: json.RawMessage(`{"node_id":"local","action":"container.restart","resource_id":"same-id","parameters":{}}`)},
			{ID: "proposal-remote", Name: "create_proposal", Arguments: json.RawMessage(`{"node_id":"remote","action":"container.restart","resource_id":"same-id","parameters":{}}`)},
			{ID: "proposal-blocked", Name: "create_proposal", Arguments: json.RawMessage(`{"node_id":"blocked","action":"container.restart","resource_id":"same-id","parameters":{}}`)},
		}}, nil
	})
	run, err := s.Start(context.Background(), RunInput{Question: "Compare my Docker nodes"}, Actor{UserID: 1})
	if err != nil {
		t.Fatal(err)
	}
	run = awaitWorkbenchRun(t, s, run)
	if run.NodeID != "" || run.Model != "test" || !reflect.DeepEqual(readNodes, []string{"local", "remote"}) || !reflect.DeepEqual(frozenNodes, []string{"local", "remote"}) || executions.Load() != 0 {
		t.Fatal("global execution scope", run, readNodes, frozenNodes)
	}
	if len(run.Result.OperationIDs) != 2 || len(run.Result.Evidence) != 4 || run.Result.Evidence[0].NodeID != "local" || run.Result.Evidence[1].NodeID != "remote" {
		t.Fatal("missing scoped evidence/proposals", run.Result)
	}
	for i, node := range []string{"local", "remote"} {
		op, err := s.Operation(context.Background(), run.Result.OperationIDs[i])
		if err != nil || op.NodeID != node || op.Status != "awaiting_approval" {
			t.Fatal("proposal lost runtime", op, err)
		}
		var entries []database.AuditLog
		if err = s.db.Where("run_id = ? AND action = ? AND node_id = ?", run.ID, "ai.read_status", node).Find(&entries).Error; err != nil || len(entries) != 1 {
			t.Fatal("read audit lost runtime", entries, err)
		}
	}
}

func TestWorkbenchModelSwitchIsPerTurnAndChatUsesSavedDefault(t *testing.T) {
	s, _, _ := aiFixture(t)
	cfg := s.Settings()
	cfg.Models = []string{"test", "alternative"}
	if _, err := s.SaveSettings(context.Background(), SettingsInput{Settings: cfg}, Actor{UserID: 1}); err != nil {
		t.Fatal(err)
	}
	s.deps.ActorValid = func(context.Context, Actor) error { return nil }
	var models []string
	var probes atomic.Int32
	s.deps.Model = modelFunc(func(_ context.Context, cfg Settings, _ string, _ []ModelMessage, definitions []Tool) (ModelReply, error) {
		if len(definitions) == 1 && definitions[0].Name == "connection_probe" {
			probes.Add(1)
			if cfg.Model != "alternative" {
				t.Error("probe used wrong model", cfg.Model)
			}
			return ModelReply{Calls: []ToolCall{{ID: "probe", Name: "connection_probe", Arguments: json.RawMessage(`{"message":"suma_connection_test"}`)}}}, nil
		}
		models = append(models, cfg.Model)
		return ModelReply{Text: "Model " + cfg.Model}, nil
	})
	actor := Actor{UserID: 1, Source: "site"}
	first, err := s.Start(context.Background(), RunInput{NodeID: "local", Question: "First turn"}, actor)
	if err != nil {
		t.Fatal(err)
	}
	first = awaitWorkbenchRun(t, s, first)
	second, err := s.Start(context.Background(), RunInput{Question: "Compare all nodes", Model: "alternative", ParentID: first.ID}, actor)
	if err != nil {
		t.Fatal(err)
	}
	second = awaitWorkbenchRun(t, s, second)
	if first.Model != "test" || second.Model != "alternative" || second.ParentID != first.ID || probes.Load() != 1 {
		t.Fatal(first, second, probes.Load())
	}
	if _, err = s.Start(context.Background(), RunInput{Question: "Unknown model", Model: "unconfigured"}, actor); !errors.Is(err, ErrInvalid) {
		t.Fatal("unknown model accepted", err)
	}
	chat := Actor{UserID: 1, Source: "chat", BindingID: "binding", ChatID: "chat", ExternalUserID: "user"}
	for _, source := range []Actor{chat, {UserID: 1, Source: "auto"}} {
		run, err := s.Start(context.Background(), RunInput{Question: "Default model " + source.Source, Model: "alternative"}, source)
		if err != nil {
			t.Fatal(err)
		}
		if run = awaitWorkbenchRun(t, s, run); run.Model != "test" {
			t.Fatal("external/auto used site override", run)
		}
	}
	if !reflect.DeepEqual(models, []string{"test", "alternative", "test", "test"}) || s.Settings().Model != "test" || !s.Settings().ToolCapable {
		t.Fatal(models, s.Settings())
	}
	cfg = s.Settings()
	cfg.Model = "alternative"
	if _, err = s.SaveSettings(context.Background(), SettingsInput{Settings: cfg}, Actor{UserID: 1}); err != nil {
		t.Fatal(err)
	}
	run, err := s.Start(context.Background(), RunInput{Question: "Use the newly saved default", Model: "test"}, chat)
	if err != nil {
		t.Fatal(err)
	}
	if run = awaitWorkbenchRun(t, s, run); run.Model != "alternative" {
		t.Fatal("chat retained the previous default", run)
	}
}

func TestGlobalResourceContextAndRevokedNodeFailClosed(t *testing.T) {
	s, _, _ := aiFixture(t)
	s.db.Create(&database.Node{ID: "remote", Name: "Remote", Enabled: true})
	cfg := s.Settings()
	cfg.NodeIDs = []string{"local", "remote"}
	if _, err := s.SaveSettings(context.Background(), SettingsInput{Settings: cfg}, Actor{UserID: 1}); err != nil {
		t.Fatal(err)
	}
	var reads atomic.Int32
	s.deps.Read = func(_ context.Context, node, _ string, _ ToolArgs, _, _ int) (Evidence, error) {
		if node != "remote" {
			t.Error("resource context used another runtime", node)
		}
		reads.Add(1)
		cfg := s.Settings()
		cfg.NodeIDs = []string{"local"}
		if _, err := s.SaveSettings(context.Background(), SettingsInput{Settings: cfg}, Actor{UserID: 1}); err != nil {
			t.Error(err)
		}
		return Evidence{Content: "Collected on remote"}, nil
	}
	var calls int
	s.deps.Model = modelFunc(func(_ context.Context, _ Settings, _ string, _ []ModelMessage, _ []Tool) (ModelReply, error) {
		calls++
		if calls > 1 {
			return ModelReply{Text: "Scope was revoked"}, nil
		}
		return ModelReply{Calls: []ToolCall{{ID: "revoked", Name: "read_status", Arguments: json.RawMessage(`{"node_id":"remote","kind":"node","id":"all"}`)}}}, nil
	})
	run, err := s.Start(context.Background(), RunInput{Question: "Inspect this resource, then compare nodes", ResourceNodeID: "remote", ResourceType: "container", ResourceID: "same-id"}, Actor{UserID: 1})
	if err != nil {
		t.Fatal(err)
	}
	run = awaitWorkbenchRun(t, s, run)
	if reads.Load() != 1 || len(run.Result.Evidence) != 2 || !run.Result.Evidence[0].Unavailable || !run.Result.Evidence[1].Unavailable || run.NodeID != "" {
		t.Fatal(run, reads.Load())
	}
	if _, err = s.Start(context.Background(), RunInput{Question: "No resource runtime", ResourceType: "container", ResourceID: "same-id"}, Actor{UserID: 1}); !errors.Is(err, ErrScope) {
		t.Fatal("missing resource runtime", err)
	}
	if _, err = s.Start(context.Background(), RunInput{Question: "Forbidden node", NodeID: "remote"}, Actor{UserID: 1}); !errors.Is(err, ErrScope) {
		t.Fatal(err)
	}
}

func TestScopedToolCannotEscapeAndAlternateSummaryModelCannotPropose(t *testing.T) {
	s, executions, _ := aiFixture(t)
	cfg := s.Settings()
	cfg.Models = []string{"test", "summary"}
	if _, err := s.SaveSettings(context.Background(), SettingsInput{Settings: cfg}, Actor{UserID: 1}); err != nil {
		t.Fatal(err)
	}
	var reads atomic.Int32
	s.deps.Read = func(context.Context, string, string, ToolArgs, int, int) (Evidence, error) {
		reads.Add(1)
		return Evidence{}, nil
	}
	var calls int
	s.deps.Model = modelFunc(func(_ context.Context, cfg Settings, _ string, _ []ModelMessage, definitions []Tool) (ModelReply, error) {
		if cfg.Model == "summary" {
			if len(definitions) == 1 && definitions[0].Name == "connection_probe" {
				return ModelReply{Text: "No tools supported"}, nil
			}
			if len(definitions) != 0 {
				t.Error("default capability was applied to summary model")
			}
			return ModelReply{Text: "Summary only"}, nil
		}
		calls++
		if calls > 1 {
			return ModelReply{Text: "Rejected cross-node tool"}, nil
		}
		return ModelReply{Calls: []ToolCall{{ID: "escape", Name: "read_status", Arguments: json.RawMessage(`{"node_id":"remote","kind":"node","id":"all"}`)}}}, nil
	})
	run, err := s.Start(context.Background(), RunInput{NodeID: "local", Question: "Scoped read"}, Actor{UserID: 1})
	if err != nil {
		t.Fatal(err)
	}
	run = awaitWorkbenchRun(t, s, run)
	if reads.Load() != 0 || !run.Result.Evidence[0].Unavailable {
		t.Fatal("scoped tool escaped", run)
	}
	run, err = s.Start(context.Background(), RunInput{Question: "Use a summary model", Model: "summary"}, Actor{UserID: 1})
	if err != nil {
		t.Fatal(err)
	}
	run = awaitWorkbenchRun(t, s, run)
	if len(run.Result.OperationIDs) != 0 || executions.Load() != 0 || !s.Settings().ToolCapable {
		t.Fatal("summary run affected default/proposals", run)
	}
}

func TestGlobalHistoryOmitsSummariesAfterNodeAuthorizationIsRemoved(t *testing.T) {
	s, _, _ := aiFixture(t)
	s.db.Create(&database.Node{ID: "remote", Name: "Remote", Enabled: true})
	cfg := s.Settings()
	cfg.NodeIDs = []string{"local", "remote"}
	if _, err := s.SaveSettings(context.Background(), SettingsInput{Settings: cfg}, Actor{UserID: 1}); err != nil {
		t.Fatal(err)
	}
	s.deps.Model = modelFunc(func(context.Context, Settings, string, []ModelMessage, []Tool) (ModelReply, error) {
		return ModelReply{Text: "private remote historical facts"}, nil
	})
	first, err := s.Start(context.Background(), RunInput{Question: "Inspect the whole fleet"}, Actor{UserID: 1})
	if err != nil {
		t.Fatal(err)
	}
	first = awaitWorkbenchRun(t, s, first)
	cfg = s.Settings()
	cfg.NodeIDs = []string{"local"}
	if _, err = s.SaveSettings(context.Background(), SettingsInput{Settings: cfg}, Actor{UserID: 1}); err != nil {
		t.Fatal(err)
	}
	s.deps.Model = modelFunc(func(_ context.Context, _ Settings, _ string, messages []ModelMessage, _ []Tool) (ModelReply, error) {
		if strings.Contains(marshal(messages), first.Result.Summary) {
			t.Error("revoked global history was sent to the model")
		}
		return ModelReply{Text: "Fresh local diagnosis"}, nil
	})
	next, err := s.Start(context.Background(), RunInput{Question: "Continue with the remaining nodes", ParentID: first.ID}, Actor{UserID: 1})
	if err != nil {
		t.Fatal(err)
	}
	awaitWorkbenchRun(t, s, next)
}
