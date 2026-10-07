package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/suma/suma/server/internal/database"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func waitRun(t *testing.T, s *Service, key, status string) Run {
	t.Helper()
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		row, err := s.Run(context.Background(), key)
		if err != nil {
			t.Fatal(err)
		}
		if row.Status == status {
			return row
		}
		if row.Status == "failed" {
			t.Fatalf("workflow failed: %s", row.Error)
		}
		time.Sleep(10 * time.Millisecond)
	}
	row, _ := s.Run(context.Background(), key)
	t.Fatalf("waiting for %s, got %s: %s", status, row.Status, row.Error)
	return Run{}
}
func TestADKApprovalCheckpointResumesAfterRestart(t *testing.T) {
	s, executions, _ := aiFixture(t)
	run := completedRun(t, s)
	var cp database.AICheckpoint
	if err := s.db.First(&cp, "run_id = ?", run.ID).Error; err != nil {
		t.Fatal(err)
	}
	plain, decryptErr := s.secrets.Decrypt(cp.Ciphertext)
	if decryptErr != nil || len(cp.Ciphertext) == 0 || strings.Contains(plain, "model-secret") {
		t.Fatal("checkpoint is not protected")
	}
	s.Stop()
	resumed, err := NewService(s.db, s.secrets, s.tasks, s.deps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(resumed.Stop)
	op, err := resumed.Operation(context.Background(), run.Result.OperationIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	decision := Decision{RequestID: "same-review", ReviewToken: op.ReviewToken, Approve: true}
	if _, err = resumed.Decide(context.Background(), op.ID, decision, Actor{UserID: 1, Source: "site"}); err != nil {
		t.Fatal(err)
	}
	if _, err = resumed.Decide(context.Background(), op.ID, decision, Actor{UserID: 1, Source: "site"}); err != nil {
		t.Fatal("duplicate decision must return its original result", err)
	}
	done := waitRun(t, resumed, run.ID, "completed")
	if executions.Load() != 1 || done.Operations != 1 || done.Iterations < 2 {
		t.Fatal("resume lost its budget or replayed the change", executions.Load(), done)
	}
	var remaining int64
	s.db.Model(&database.AICheckpoint{}).Where("run_id = ?", run.ID).Count(&remaining)
	if remaining != 0 {
		t.Fatal("completed checkpoint retained")
	}
}

type targetModel struct{}

func (targetModel) Complete(_ context.Context, _ Settings, _ string, messages []ModelMessage, defs []Tool) (ModelReply, error) {
	if len(defs) == 0 {
		return ModelReply{Text: `{"general":false}`}, nil
	}
	for _, message := range messages {
		if message.Role == "tool" {
			return ModelReply{Text: "Actual state checked."}, nil
		}
	}
	return ModelReply{Calls: []ToolCall{{ID: "read-after-choice", Name: "read_status", Arguments: json.RawMessage(`{"kind":"node","id":"","node_id":"local"}`)}}}, nil
}
func TestADKMissingNodeWaitsWithoutDockerAndInputIsIdempotent(t *testing.T) {
	s, _, _ := aiFixture(t)
	reads := &atomic.Int32{}
	s.deps.Model = targetModel{}
	s.deps.Read = func(context.Context, string, string, ToolArgs, int, int) (Evidence, error) {
		reads.Add(1)
		return Evidence{Source: "node", Content: "healthy"}, nil
	}
	actor := Actor{UserID: 1, Source: "site"}
	conv, err := s.CreateConversation(context.Background(), ConversationInput{}, actor)
	if err != nil {
		t.Fatal(err)
	}
	request := MessageInput{Question: "check resource status", RequestID: "same-message"}
	run, err := s.PostMessage(context.Background(), conv.ID, request, actor)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := s.PostMessage(context.Background(), conv.ID, request, actor)
	if err != nil || duplicate.ID != run.ID {
		t.Fatal("message not idempotent", err)
	}
	waiting := waitRun(t, s, run.ID, "waiting_input")
	if reads.Load() != 0 || waiting.Interaction == nil || len(waiting.TargetNodeIDs) != 0 {
		t.Fatal("Docker read before target selection")
	}
	answer := InputAnswer{InteractionID: waiting.Interaction.ID, ExpectedRevision: waiting.Revision, RequestID: "same-answer", Values: []string{"local"}}
	var wg sync.WaitGroup
	accepted := atomic.Int32{}
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.AnswerInput(context.Background(), run.ID, answer, actor); err == nil {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	done := waitRun(t, s, run.ID, "completed")
	if reads.Load() != 1 || accepted.Load() != 5 || len(done.TargetNodeIDs) != 1 || done.TargetSource != "selection" {
		t.Fatal("input resume was duplicated or scope lost", reads.Load(), accepted.Load(), done)
	}
}
func TestADKChangedTaskInvalidatesPendingProposal(t *testing.T) {
	s, executions, _ := aiFixture(t)
	run := completedRun(t, s)
	actor := Actor{UserID: 1, Source: "site"}
	op, _ := s.Operation(context.Background(), run.Result.OperationIDs[0])
	replacement, err := s.PostMessage(context.Background(), run.ConversationID, MessageInput{Question: "instead inspect status only", RequestID: "adjust-task"}, actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Decide(context.Background(), op.ID, Decision{RequestID: "obsolete", ReviewToken: op.ReviewToken, Approve: true}, actor); !errors.Is(err, ErrConflict) {
		t.Fatal("obsolete proposal approved", err)
	}
	waitRun(t, s, replacement.ID, "waiting_input")
	if executions.Load() != 0 {
		t.Fatal("obsolete operation executed")
	}
	var input database.AIInteraction
	s.db.Where("run_id = ?", run.ID).First(&input)
	if input.Status != "superseded" {
		t.Fatal("old waiting point still usable")
	}
}
func TestADKVerificationFailurePausesFollowingSteps(t *testing.T) {
	s, _, _ := aiFixture(t)
	s.deps.Verify = func(context.Context, database.AIOperation, Snapshot) (Verification, error) {
		return Verification{Satisfied: false, Summary: "state did not change"}, nil
	}
	run := completedRun(t, s)
	op, _ := s.Operation(context.Background(), run.Result.OperationIDs[0])
	if _, err := s.Decide(context.Background(), op.ID, Decision{RequestID: "verify-failure", ReviewToken: op.ReviewToken, Approve: true}, Actor{UserID: 1, Source: "site"}); err != nil {
		t.Fatal(err)
	}
	paused := waitRun(t, s, run.ID, "paused")
	if paused.Phase != "verify" {
		t.Fatal("failed verification continued the workflow")
	}
	operation, _ := s.Operation(context.Background(), op.ID)
	if operation.Status != "failed" || !strings.Contains(string(operation.Verification), "state did not change") {
		t.Fatal("verification evidence lost")
	}
}

func TestADKCheckpointFailureNeverPublishesApproval(t *testing.T) {
	s, executions, _ := aiFixture(t)
	if err := s.db.Exec("CREATE FUNCTION reject_checkpoint() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'checkpoint unavailable'; END $$; CREATE TRIGGER reject_checkpoint BEFORE INSERT ON ai_checkpoints FOR EACH ROW EXECUTE FUNCTION reject_checkpoint()").Error; err != nil {
		t.Fatal(err)
	}
	run, err := s.Start(context.Background(), RunInput{NodeID: "local", Question: "restart"}, Actor{UserID: 1})
	if err != nil {
		t.Fatal(err)
	}
	waitRun(t, s, run.ID, "failed")
	var operations, interactions, events int64
	s.db.Model(&database.AIOperation{}).Where("run_id = ?", run.ID).Count(&operations)
	s.db.Model(&database.AIInteraction{}).Where("run_id = ?", run.ID).Count(&interactions)
	s.db.Model(&database.AIWorkflowEvent{}).Where("run_id = ? AND type = ?", run.ID, "run.waiting_approval").Count(&events)
	if operations != 0 || interactions != 0 || events != 0 || executions.Load() != 0 {
		t.Fatal("waiting point escaped a rolled-back checkpoint transaction")
	}
}

func TestADKIncompatibleCheckpointNeverStartsMutation(t *testing.T) {
	s, executions, _ := aiFixture(t)
	run := completedRun(t, s)
	op, _ := s.Operation(context.Background(), run.Result.OperationIDs[0])
	if err := s.db.Model(&database.AICheckpoint{}).Where("run_id = ?", run.ID).Update("workflow_version", "future-incompatible").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decide(context.Background(), op.ID, Decision{RequestID: "version-change", ReviewToken: op.ReviewToken, Approve: true}, Actor{UserID: 1}); !errors.Is(err, ErrConflict) {
		t.Fatal("incompatible checkpoint approved", err)
	}
	var tasks int64
	s.db.Model(&database.Task{}).Count(&tasks)
	if tasks != 0 || executions.Load() != 0 {
		t.Fatal("incompatible checkpoint created a Task")
	}
}

func TestADKTypedBusinessConfirmationsCannotBeFilledByModel(t *testing.T) {
	s, executions, _ := aiFixture(t)
	freeze := s.deps.Freeze
	s.deps.Freeze = func(ctx context.Context, node string, req OperationRequest) (Snapshot, error) {
		snapshot, err := freeze(ctx, node, req)
		snapshot.Confirmations = []Confirmation{{Key: "name", Label: "Type the resource name", Expected: req.ResourceID}, {Key: "docker_socket", Label: "Confirm full Engine control", Checkbox: true}}
		return snapshot, err
	}
	run := completedRun(t, s)
	op, _ := s.Operation(context.Background(), run.Result.OperationIDs[0])
	actor := Actor{UserID: 1, Source: "site"}
	for i, confirmations := range []map[string]string{nil, {"name": op.ResourceID}, {"name": "wrong", "docker_socket": "true"}} {
		if _, err := s.Decide(context.Background(), op.ID, Decision{RequestID: fmt.Sprintf("missing-confirmation-%d", i), ReviewToken: op.ReviewToken, Approve: true, Confirmations: confirmations}, actor); !errors.Is(err, ErrInvalid) {
			t.Fatal("missing or wrong confirmation accepted", err)
		}
	}
	if executions.Load() != 0 {
		t.Fatal("model or missing confirmation executed a mutation")
	}
	if _, err := s.Decide(context.Background(), op.ID, Decision{RequestID: "human-confirmed", ReviewToken: op.ReviewToken, Approve: true, Confirmations: map[string]string{"name": op.ResourceID, "docker_socket": "true"}}, actor); err != nil {
		t.Fatal(err)
	}
	waitRun(t, s, run.ID, "completed")
}

func TestOperationWithoutBusinessConfirmationsReturnsEmptyArray(t *testing.T) {
	operation := decodeOperation(database.AIOperation{ConfirmationsJSON: "null", ParametersJSON: "{}", SnapshotJSON: "{}"})
	if operation.Confirmations == nil {
		t.Fatal("missing confirmations prevent the UI from approving ordinary actions")
	}
}
