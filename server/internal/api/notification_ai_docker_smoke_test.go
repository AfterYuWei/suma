//go:build dockersmoke

package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/ai"
	"github.com/suma/suma/server/internal/auth"
	"github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/docker"

	"github.com/suma/suma/server/internal/node"
	"github.com/suma/suma/server/internal/notification"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/task"
	"gorm.io/gorm"
)

type smokeAIModel struct {
	containerID string
	calls       int
}

func (m *smokeAIModel) Complete(ctx context.Context, cfg ai.Settings, key string, messages []ai.ModelMessage, registered []ai.Tool) (ai.ModelReply, error) {
	m.calls++
	if m.calls == 1 {
		return ai.ModelReply{Text: "connected"}, nil
	}
	if m.calls == 2 {
		return ai.ModelReply{Calls: []ai.ToolCall{{ID: "probe", Name: "connection_probe", Arguments: json.RawMessage(`{}`)}}}, nil
	}
	if m.calls == 3 {
		raw, _ := json.Marshal(ai.OperationRequest{Action: "container.restart", ResourceID: m.containerID, Parameters: json.RawMessage(`{}`)})
		return ai.ModelReply{Calls: []ai.ToolCall{{ID: "proposal", Name: "create_proposal", Arguments: raw}}}, nil
	}
	return ai.ModelReply{Text: "Restart proposed; explicit review required."}, nil
}
func notificationAITransportSmoke(t *testing.T, ctx context.Context, root, nodeID, transport string, db *gorm.DB, nodes *node.Service, adapter *docker.Adapter, tasks *task.Service, authentication *auth.Service, current *compose.Service) {
	t.Helper()
	name := fmt.Sprintf("reviewed-%s-%d", transport, time.Now().UnixNano())
	_, err := current.Create(ctx, name, "services:\n  app:\n    image: alpine:3.24\n    command: [sleep, '300']\n    environment:\n      SECRET_PASSWORD: testing-value\n", "")
	if err != nil {
		t.Fatal(err)
	}
	defer current.ForceRemove(context.Background(), name, true)
	resolve := func(ctx context.Context, ref string) (string, error) {
		row, err := adapter.InspectImage(ctx, ref)
		return row.ID, err
	}
	review, err := current.ReviewUpdate(ctx, name, resolve)
	if err != nil {
		t.Fatal("review Compose", err)
	}
	if review.ConfigHash == "" || len(review.Images) != 1 {
		t.Fatal("missing frozen images")
	}
	if err = current.ApplyUpdateReviewed(ctx, name, review, func(int, string) {}); err != nil {
		t.Fatal("apply without implicit pull/build", err)
	}
	rows, err := current.Services(ctx, name)
	if err != nil || len(rows) != 1 {
		t.Fatal("reviewed project missing container", err)
	}
	containerID := rows[0].ID
	before, err := adapter.AIContainerState(ctx, containerID)
	if err != nil {
		t.Fatal(err)
	}
	store, err := secret.Open(filepath.Join(root, "ai-key"))
	if err != nil {
		t.Fatal(err)
	}
	notify := notification.NewService(db, store, notification.Dependencies{Sender: notificationSender{}})
	assistant, err := ai.NewService(db, store, tasks, ai.Dependencies{Model: &smokeAIModel{containerID: containerID}, Emit: notify.Emit, Query: func(ctx context.Context, id string) (ai.QuerySummary, error) {
		runtime, err := nodes.Runtime(ctx, id)
		if err != nil {
			return ai.QuerySummary{}, err
		}
		rows, err := runtime.List(ctx)
		out := ai.QuerySummary{Available: err == nil}
		out.Containers.Total = len(rows)
		return out, err
	}, Freeze: func(ctx context.Context, id string, req ai.OperationRequest) (ai.Snapshot, error) {
		runtime, err := nodes.Runtime(ctx, id)
		if err != nil {
			return ai.Snapshot{}, err
		}
		state, err := runtime.AIContainerState(ctx, req.ResourceID)
		raw, _ := json.Marshal(state)
		sum := sha256.Sum256(raw)
		view, err := nodes.Get(ctx, id)
		return ai.Snapshot{RuntimeKey: fmt.Sprintf("%s|%s|%v", view.EngineID, view.Endpoint, view.AgentConnectedAt), Fingerprint: hex.EncodeToString(sum[:]), Description: "Restart test container", Impact: "Test service interrupted", Details: json.RawMessage(raw)}, err
	}, Execute: func(ctx context.Context, row database.AIOperation, snap ai.Snapshot, report task.Reporter) error {
		runtime, err := nodes.Runtime(ctx, row.NodeID)
		if err != nil {
			return err
		}
		return runtime.Restart(ctx, row.ResourceID)
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer assistant.Stop()
	cfg := assistant.Settings()
	cfg.Enabled = true
	cfg.NodeIDs = []string{nodeID}
	cfg.Model = "smoke"
	actor := ai.Actor{UserID: 1, Source: "site"}
	if _, err = assistant.SaveSettings(ctx, ai.SettingsInput{Settings: cfg}, actor); err != nil {
		t.Fatal(err)
	}
	summary, err := assistant.Query(ctx, nodeID)
	if err != nil || !summary.Available || summary.Containers.Total < 1 {
		t.Fatal("real Docker safe query failed", err)
	}
	rawSummary, _ := json.Marshal(summary)
	if strings.Contains(string(rawSummary), "testing-value") || strings.Contains(string(rawSummary), containerID) || strings.Contains(string(rawSummary), name) {
		t.Fatal("safe query exposed resource identifiers or environment values")
	}
	if _, err := assistant.Query(ctx, "unauthorized"); err == nil {
		t.Fatal("unauthorized query node accepted")
	}
	assistant.TestModel(ctx)
	run, err := assistant.Start(ctx, ai.RunInput{NodeID: nodeID, Question: "Propose restarting the test container"}, actor)
	if err != nil {
		t.Fatal(err)
	}
	var latest ai.Run
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		latest, _ = assistant.Run(ctx, run.ID)
		if latest.Status != "running" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(latest.Result.OperationIDs) != 1 {
		t.Fatal("no proposal", latest)
	}
	op, _ := assistant.Operation(ctx, latest.Result.OperationIDs[0])
	unchanged, _ := adapter.AIContainerState(ctx, containerID)
	if before["started_at"] != unchanged["started_at"] {
		t.Fatal("model changed container before approval")
	}
	if _, err = assistant.Decide(ctx, op.ID, ai.Decision{Approve: true, ReviewToken: op.ReviewToken}, actor); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		op, _ = assistant.Operation(ctx, op.ID)
		if op.Status == "completed" || op.Status == "failed" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if op.Status != "completed" {
		t.Fatal("approved restart failed", op.Status, op.Result)
	}
	var globalAudit database.AuditLog
	if db.Where("operation_id = ? AND action = ?", op.ID, "ai.execution").First(&globalAudit).Error != nil || globalAudit.TaskID != op.TaskID || globalAudit.NodeID != nodeID {
		t.Fatal("real execution missing from global audit")
	}
	part, err := assistant.Audits(ctx)
	if err != nil {
		t.Fatal(err)
	}
	matched := false
	for _, row := range part {
		if row.ID == globalAudit.ID {
			matched = row.OperationID == op.ID && row.TaskID == op.TaskID
		}
	}
	if !matched {
		t.Fatal("AI audit did not project the global execution")
	}
	after, _ := adapter.AIContainerState(ctx, containerID)
	if before["started_at"] == after["started_at"] {
		t.Fatal("restart did not happen")
	}
	if _, err = assistant.Decide(ctx, op.ID, ai.Decision{Approve: true, ReviewToken: op.ReviewToken}, actor); err == nil {
		t.Fatal("approval replay accepted")
	}
	var linked database.Task
	if db.First(&linked, "id = ?", op.TaskID).Error != nil {
		t.Fatal("missing Task")
	}
	inbox, err := notify.Inbox(ctx, 1)
	if err != nil || len(inbox.Items) < 2 {
		t.Fatal("notification/approval feedback missing", err)
	}
	t.Log("reviewed Compose + notification → AI proposal → single approval → real Docker restart verified over", transport)
}
