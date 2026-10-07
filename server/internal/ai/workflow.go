package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/redact"
	"github.com/suma/suma/server/internal/task"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var requestPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func actorSource(a Actor) string {
	if a.Source == "" {
		return "site"
	}
	return a.Source
}
func (s *Service) cleanText(v string, n int) string {
	s.mu.Lock()
	key := s.key
	s.mu.Unlock()
	if key != "" {
		v = strings.ReplaceAll(v, key, "[redacted]")
	}
	return redact.Bounded(v, n)
}
func decodeRun(row database.AIRun) Run {
	out := Run{AIRun: row, Result: Result{Evidence: []Evidence{}, OperationIDs: []string{}, Missing: []string{}}, TargetNodeIDs: []string{}, Steps: []PlanStep{}}
	_ = json.Unmarshal([]byte(row.ResultJSON), &out.Result)
	_ = json.Unmarshal([]byte(row.NodeIDsJSON), &out.TargetNodeIDs)
	return out
}
func (s *Service) CreateConversation(ctx context.Context, in ConversationInput, a Actor) (Conversation, error) {
	if err := s.validateActor(ctx, a); err != nil {
		return Conversation{}, err
	}
	row := database.AIConversation{ID: id(), UserID: a.UserID, Source: actorSource(a), Title: s.cleanText(strings.TrimSpace(in.Title), 120), BindingID: a.BindingID, ChatID: a.ChatID, ContextJSON: "{}", Revision: 1}
	if row.Source == "chat" {
		row.ChatKey = digest([]string{fmt.Sprint(a.UserID), a.BindingID, a.ChatID})
		var found database.AIConversation
		if err := s.db.WithContext(ctx).Where("chat_key = ?", row.ChatKey).First(&found).Error; err == nil {
			return s.Conversation(ctx, found.ID, a)
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return Conversation{}, err
		}
	}
	err := s.db.WithContext(ctx).Create(&row).Error
	if errors.Is(err, gorm.ErrDuplicatedKey) && row.ChatKey != "" {
		var found database.AIConversation
		if err = s.db.WithContext(ctx).Where("chat_key = ?", row.ChatKey).First(&found).Error; err == nil {
			return s.Conversation(ctx, found.ID, a)
		}
	}
	if err != nil {
		return Conversation{}, err
	}
	return Conversation{AIConversation: row, Context: TargetContext{}, Messages: []database.AIMessage{}, Runs: []Run{}}, nil
}
func (s *Service) conversationRow(ctx context.Context, key string, a Actor) (database.AIConversation, error) {
	if err := s.validateActor(ctx, a); err != nil {
		return database.AIConversation{}, err
	}
	var row database.AIConversation
	if s.db.WithContext(ctx).Where("id = ? AND user_id = ?", key, a.UserID).First(&row).Error != nil {
		return row, ErrScope
	}
	if actorSource(a) == "chat" && (row.Source != "chat" || row.BindingID != a.BindingID || row.ChatID != a.ChatID) {
		return row, ErrScope
	}
	return row, nil
}
func (s *Service) Conversations(ctx context.Context, a Actor) ([]database.AIConversation, error) {
	if err := s.validateActor(ctx, a); err != nil {
		return nil, err
	}
	rows := []database.AIConversation{}
	q := s.db.WithContext(ctx).Where("user_id = ?", a.UserID)
	if actorSource(a) == "chat" {
		q = q.Where("binding_id = ? AND chat_id = ?", a.BindingID, a.ChatID)
	}
	return rows, q.Order("updated_at DESC, id DESC").Limit(200).Find(&rows).Error
}
func (s *Service) Conversation(ctx context.Context, key string, a Actor) (Conversation, error) {
	row, err := s.conversationRow(ctx, key, a)
	if err != nil {
		return Conversation{}, err
	}
	out := Conversation{AIConversation: row, Messages: []database.AIMessage{}, Runs: []Run{}}
	_ = json.Unmarshal([]byte(row.ContextJSON), &out.Context)
	if err = s.db.WithContext(ctx).Where("conversation_id = ?", key).Order("created_at ASC, id ASC").Find(&out.Messages).Error; err != nil {
		return out, err
	}
	var runs []database.AIRun
	if err = s.db.WithContext(ctx).Where("conversation_id = ?", key).Order("created_at ASC, id ASC").Find(&runs).Error; err != nil {
		return out, err
	}
	for _, r := range runs {
		view, err := s.Run(ctx, r.ID)
		if err != nil {
			return out, err
		}
		out.Runs = append(out.Runs, view)
		if r.ID == row.CurrentRunID {
			copy := view
			out.CurrentRun = &copy
		}
	}
	return out, nil
}
func (s *Service) RunAs(ctx context.Context, key string, a Actor) (Run, error) {
	r, err := s.Run(ctx, key)
	if err != nil {
		return r, err
	}
	if _, err = s.conversationRow(ctx, r.ConversationID, a); err != nil {
		return Run{}, err
	}
	return r, nil
}
func (s *Service) PostMessage(ctx context.Context, key string, in MessageInput, a Actor) (Run, error) {
	return s.postMessage(ctx, key, in, "", a)
}
func (s *Service) postMessage(ctx context.Context, key string, in MessageInput, eventID string, a Actor) (Run, error) {
	if _, err := s.conversationRow(ctx, key, a); err != nil {
		return Run{}, err
	}
	in.Question = s.cleanText(strings.TrimSpace(in.Question), 4000)
	if in.Question == "" || !requestPattern.MatchString(in.RequestID) {
		return Run{}, ErrInvalid
	}
	s.mu.Lock()
	cfg, modelKey := cloneSettings(s.cfg), s.key
	stopped := s.stopped
	s.mu.Unlock()
	if stopped || !cfg.Enabled {
		return Run{}, ErrDisabled
	}
	if len(cfg.NodeIDs) == 0 {
		return Run{}, ErrScope
	}
	selectedModel := cfg.Model
	if actorSource(a) == "site" && in.Model != "" {
		selectedModel = in.Model
	}
	if !has(cfg.Models, selectedModel) {
		return Run{}, ErrInvalid
	}
	if err := s.validateTargetContext(ctx, in.Context, cfg.NodeIDs); err != nil {
		return Run{}, err
	}
	requestHash := digest(in)
	var result database.AIRun
	var cancelRuns, cancelTasks []string
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var conv database.AIConversation
		if tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", key, a.UserID).First(&conv).Error != nil {
			return ErrScope
		}
		var previousMessage database.AIMessage
		e := tx.Where("conversation_id = ? AND request_key = ?", key, in.RequestID).First(&previousMessage).Error
		if e == nil {
			if previousMessage.RequestHash != requestHash {
				return ErrConflict
			}
			return tx.First(&result, "id = ?", previousMessage.RunID).Error
		}
		if !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		if in.ExpectedRevision != 0 && conv.Revision != in.ExpectedRevision {
			return ErrConflict
		}
		if actorSource(a) == "auto" {
			if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "suma:auto-budget").Error; err != nil {
				return err
			}
			now := s.deps.Now()
			day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
			var count int64
			if err := tx.Model(&database.AIRun{}).Where("source = ? AND created_at >= ?", "auto", day).Count(&count).Error; err != nil {
				return err
			}
			if count >= int64(cfg.DailyAutoLimit) {
				return ErrBudget
			}
			if err := tx.Model(&database.AIRun{}).Where("source = ? AND event_id = ? AND created_at > ?", "auto", eventID, now.Add(-10*time.Minute)).Count(&count).Error; err != nil {
				return err
			}
			if eventID != "" && count > 0 {
				return ErrBusy
			}
		}
		target := in.Context
		var predecessor string
		if conv.CurrentRunID != "" {
			var old database.AIRun
			if err := tx.First(&old, "id = ?", conv.CurrentRunID).Error; err != nil {
				return err
			}
			if len(target.NodeIDs) == 0 && target.ResourceNodeID == "" {
				_ = json.Unmarshal([]byte(conv.ContextJSON), &target)
			}
			if err := s.validateTargetContext(ctx, target, cfg.NodeIDs); err != nil {
				target = TargetContext{}
			}
			var active int64
			if err := tx.Model(&database.AIOperation{}).Where("run_id = ? AND status = ?", old.ID, "running").Count(&active).Error; err != nil {
				return err
			}
			if active > 0 {
				predecessor = old.ID
			}
			var queued []database.AIOperation
			if err := tx.Where("run_id = ? AND status = ?", old.ID, "queued").Find(&queued).Error; err != nil {
				return err
			}
			for _, op := range queued {
				if op.TaskID != "" {
					cancelTasks = append(cancelTasks, op.TaskID)
				}
			}
			if err := tx.Model(&database.AIOperation{}).Where("run_id = ? AND status IN ?", old.ID, []string{"prepared", "awaiting_approval", "queued"}).Updates(map[string]any{"status": "invalidated", "result": "Task requirements changed; review the new proposal"}).Error; err != nil {
				return err
			}
			if err := tx.Model(&database.AIInteraction{}).Where("run_id = ? AND status = ?", old.ID, "pending").Update("status", "superseded").Error; err != nil {
				return err
			}
			if !runTerminal(old.Status) {
				status := "canceled"
				if active > 0 {
					status = "paused"
				}
				if err := tx.Model(&old).Updates(map[string]any{"status": status, "error": "Task adjusted by a new message", "lease_owner": "", "lease_until": nil}).Error; err != nil {
					return err
				}
				cancelRuns = append(cancelRuns, old.ID)
			}
		}
		// A fresh resource link or explicit node context supersedes prior task context.
		if len(in.Context.NodeIDs) > 0 || in.Context.ResourceNodeID != "" {
			target = in.Context
		}
		if len(target.NodeIDs) == 0 && target.ResourceNodeID != "" {
			target.NodeIDs = []string{target.ResourceNodeID}
		}
		nodeID := ""
		if len(target.NodeIDs) == 1 {
			nodeID = target.NodeIDs[0]
		}
		source := ""
		if len(target.NodeIDs) > 0 {
			source = "conversation"
			if in.Context.ResourceID != "" {
				source = "resource"
			}
			if len(in.Context.NodeIDs) > 0 {
				source = "explicit"
			}
		}
		title := conv.Title
		if title == "" {
			title = redact.Bounded(in.Question, 120)
		}
		result = database.AIRun{ID: id(), ConversationID: key, UserID: a.UserID, NodeID: nodeID, NodeIDsJSON: marshal(target.NodeIDs), Model: selectedModel, Source: actorSource(a), EventID: eventID, Question: in.Question, Status: "queued", Phase: "target", Revision: 1, TargetSource: source, ActorJSON: marshal(a), InputJSON: marshal(target), ConfigHash: workflowConfigHash(cfg, modelKey), ResultJSON: marshal(Result{Evidence: []Evidence{}, OperationIDs: []string{}, Missing: []string{}}), PredecessorID: predecessor}
		if target.NodeIDs == nil {
			result.NodeIDsJSON = "[]"
		}
		if err := tx.Create(&result).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.AIMessage{ID: id(), ConversationID: key, RunID: result.ID, Role: "user", Content: in.Question, RequestKey: in.RequestID, RequestHash: requestHash}).Error; err != nil {
			return err
		}
		if err := tx.Model(&conv).Updates(map[string]any{"current_run_id": result.ID, "title": title, "context_json": marshal(target), "revision": conv.Revision + 1}).Error; err != nil {
			return err
		}
		if err := s.audit(ctx, tx, a, result.ID, "", "diagnosis", nodeID, "queued"); err != nil {
			return err
		}
		return s.eventTx(ctx, tx, key, result.ID, "run.queued", map[string]any{"run_id": result.ID})
	})
	if err != nil {
		return Run{}, err
	}
	s.mu.Lock()
	for _, runID := range cancelRuns {
		if cancel := s.cancels[runID]; cancel != nil {
			cancel()
		}
	}
	s.mu.Unlock()
	for _, taskID := range cancelTasks {
		_ = s.tasks.Cancel(taskID)
	}
	s.signalRuntime()
	return s.Run(ctx, result.ID)
}
func runTerminal(status string) bool {
	return status == "completed" || status == "failed" || status == "canceled"
}
func workflowConfigHash(cfg Settings, key string) string {
	return digest([]any{cfg.Protocol, cfg.Endpoint, cfg.Model, cfg.Models, cfg.AllowPrivate, cfg.AllowInsecure, cfg.NodeIDs, cfg.ToolCapable, key})
}
func (s *Service) validateTargetContext(ctx context.Context, c TargetContext, authorized []string) error {
	if len(c.NodeIDs) > 100 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, node := range c.NodeIDs {
		if !has(authorized, node) || seen[node] {
			return ErrScope
		}
		seen[node] = true
		var count int64
		if s.db.WithContext(ctx).Model(&database.Node{}).Where("id = ? AND enabled = ?", node, true).Count(&count).Error != nil || count != 1 {
			return ErrScope
		}
	}
	if c.ResourceID != "" {
		if c.ResourceKind == "" || !has([]string{"container", "image", "network", "volume", "project", "task", "cd", "cleanup", "node"}, c.ResourceKind) {
			return ErrInvalid
		}
		if c.ResourceNodeID == "" && len(c.NodeIDs) == 1 {
			c.ResourceNodeID = c.NodeIDs[0]
		}
		if !has(authorized, c.ResourceNodeID) {
			return ErrScope
		}
	}
	return nil
}
func (s *Service) AnswerInput(ctx context.Context, key string, in InputAnswer, a Actor) (Run, error) {
	view, err := s.RunAs(ctx, key, a)
	if err != nil {
		return Run{}, err
	}
	in.Text = s.cleanText(strings.TrimSpace(in.Text), 4000)
	s.mu.Lock()
	cfg, modelKey := cloneSettings(s.cfg), s.key
	s.mu.Unlock()
	if !cfg.Enabled {
		return Run{}, ErrDisabled
	}
	if workflowConfigHash(cfg, modelKey) != view.ConfigHash {
		return Run{}, ErrScope
	}
	if err := s.runActorValid(ctx, view.AIRun); err != nil {
		return Run{}, err
	}
	if err := s.checkpointReady(ctx, view.AIRun); err != nil {
		return Run{}, err
	}
	if !requestPattern.MatchString(in.RequestID) {
		return Run{}, ErrInvalid
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.lockConversation(ctx, tx, view.ConversationID); err != nil {
			return err
		}
		var run database.AIRun
		if tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&run, "id = ?", key).Error != nil {
			return ErrScope
		}
		var input database.AIInteraction
		if tx.Where("id = ? AND run_id = ?", in.InteractionID, key).First(&input).Error != nil {
			return ErrScope
		}
		if input.Status == "answered" && input.RequestKey == in.RequestID {
			var saved InputAnswer
			_ = json.Unmarshal([]byte(input.AnswerJSON), &saved)
			if digest(saved) != digest(in) {
				return ErrConflict
			}
			return nil
		}
		if run.Status != "waiting_input" || run.Revision != in.ExpectedRevision || input.Revision != run.Revision || input.Status != "pending" || !s.deps.Now().Before(input.ExpiresAt) {
			return ErrConflict
		}
		var options []ResourceOption
		if json.Unmarshal([]byte(input.OptionsJSON), &options) != nil {
			return ErrInvalid
		}
		if len(options) > 0 {
			if len(in.Values) == 0 || !input.Multiple && len(in.Values) != 1 {
				return ErrInvalid
			}
			seen := map[string]bool{}
			for _, value := range in.Values {
				allowed := false
				for _, option := range options {
					if option.ID == value {
						allowed = true
					}
				}
				if !allowed || seen[value] {
					return ErrInvalid
				}
				seen[value] = true
			}
		} else {
			if in.Text == "" {
				return ErrInvalid
			}
		}
		if input.Kind == "node" {
			if err := s.validateTargetContext(ctx, TargetContext{NodeIDs: in.Values}, cfg.NodeIDs); err != nil {
				return err
			}
		}
		if err := tx.Model(&input).Updates(map[string]any{"status": "answered", "request_key": in.RequestID, "answer_json": marshal(in)}).Error; err != nil {
			return err
		}
		if err := tx.Model(&run).Updates(map[string]any{"status": "queued", "resume_target": input.InterruptID, "resume_json": marshal(in), "lease_owner": "", "lease_until": nil}).Error; err != nil {
			return err
		}
		answerText := in.Text
		if len(in.Values) > 0 {
			labels := []string{}
			for _, value := range in.Values {
				for _, option := range options {
					if option.ID == value {
						labels = append(labels, option.Name+" ("+option.ID+")")
					}
				}
			}
			answerText = strings.Join(labels, ", ")
		}
		if err := tx.Create(&database.AIMessage{ID: id(), ConversationID: run.ConversationID, RunID: run.ID, Role: "interaction", Content: redact.Bounded(answerText, 4000), MetadataJSON: marshal(map[string]string{"interaction_id": input.ID, "kind": input.Kind})}).Error; err != nil {
			return err
		}
		if err := s.audit(ctx, tx, a, key, "", "input", input.Kind, "answered"); err != nil {
			return err
		}
		return s.eventTx(ctx, tx, run.ConversationID, key, "interaction.answered", map[string]any{"interaction_id": input.ID})
	})
	if err != nil {
		return Run{}, err
	}
	s.signalRuntime()
	return s.Run(ctx, key)
}
func (s *Service) CancelRun(ctx context.Context, key string, a Actor) (Run, error) {
	view, err := s.RunAs(ctx, key, a)
	if err != nil {
		return Run{}, err
	}
	var tasks []database.AIOperation
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.lockConversation(ctx, tx, view.ConversationID); err != nil {
			return err
		}
		if err := tx.Model(&database.AIRun{}).Where("id = ? AND status NOT IN ?", key, []string{"completed", "failed", "canceled"}).Updates(map[string]any{"status": "canceled", "error": "Canceled by user", "lease_owner": "", "lease_until": nil}).Error; err != nil {
			return err
		}
		if err := tx.Where("run_id = ? AND status IN ?", key, []string{"queued", "running"}).Find(&tasks).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.AIOperation{}).Where("run_id = ? AND status IN ?", key, []string{"prepared", "awaiting_approval", "queued"}).Updates(map[string]any{"status": "invalidated", "result": "Task canceled"}).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.AIInteraction{}).Where("run_id = ? AND status = ?", key, "pending").Update("status", "canceled").Error; err != nil {
			return err
		}
		if err := tx.Where("run_id = ?", key).Delete(&database.AICheckpoint{}).Error; err != nil {
			return err
		}
		if err := s.audit(ctx, tx, a, key, "", "cancel", key, "canceled"); err != nil {
			return err
		}
		return s.eventTx(ctx, tx, view.ConversationID, key, "run.canceled", map[string]any{"run_id": key})
	})
	if err != nil {
		return Run{}, err
	}
	s.mu.Lock()
	if cancel := s.cancels[key]; cancel != nil {
		cancel()
	}
	s.mu.Unlock()
	for _, op := range tasks {
		_ = s.tasks.Cancel(op.TaskID)
	}
	return s.Run(ctx, key)
}
func (s *Service) eventTx(ctx context.Context, tx *gorm.DB, convID, runID, kind string, payload any) error {
	var conv database.AIConversation
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).First(&conv, "id = ?", convID).Error; err != nil {
		return err
	}
	seq := conv.EventSeq + 1
	if err := tx.Model(&conv).Update("event_seq", seq).Error; err != nil {
		return err
	}
	return tx.Create(&database.AIWorkflowEvent{ConversationID: convID, RunID: runID, Seq: seq, Type: kind, PayloadJSON: marshal(payload)}).Error
}
func (s *Service) Events(ctx context.Context, convID string, after uint64, a Actor) ([]WorkflowEvent, error) {
	if _, err := s.conversationRow(ctx, convID, a); err != nil {
		return nil, err
	}
	var rows []database.AIWorkflowEvent
	if err := s.db.WithContext(ctx).Where("conversation_id = ? AND seq > ?", convID, after).Order("seq ASC").Limit(200).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := []WorkflowEvent{}
	for _, row := range rows {
		out = append(out, WorkflowEvent{AIWorkflowEvent: row, Payload: json.RawMessage(row.PayloadJSON)})
	}
	return out, nil
}
func (s *Service) taskCanContinue(ctx context.Context, row database.AIRun) bool {
	if row.PredecessorID == "" {
		return true
	}
	var active int64
	if s.db.WithContext(ctx).Model(&database.AIOperation{}).Where("run_id = ? AND status IN ?", row.PredecessorID, []string{"queued", "running"}).Count(&active).Error != nil || active > 0 {
		return false
	}
	if s.db.WithContext(ctx).Table("ai_operations").Joins("JOIN tasks ON tasks.id = ai_operations.task_id").Where("ai_operations.run_id = ? AND tasks.status IN ?", row.PredecessorID, []string{task.StatusPending, task.StatusRunning}).Count(&active).Error != nil {
		return false
	}
	return active == 0
}
func (s *Service) waitingTasks(ctx context.Context) {
	var rows []database.AIRun
	if s.db.WithContext(ctx).Where("status = ?", "waiting_task").Find(&rows).Error != nil {
		return
	}
	for _, row := range rows {
		var interaction database.AIInteraction
		if s.db.WithContext(ctx).Where("run_id = ? AND kind = ? AND status = ?", row.ID, "task", "pending").First(&interaction).Error == nil {
			var state waitState
			_ = json.Unmarshal([]byte(interaction.StateJSON), &state)
			var evidenceTask database.Task
			if s.db.WithContext(ctx).First(&evidenceTask, "id = ?", state.TaskID).Error != nil {
				continue
			}
			s.recordTaskProgress(ctx, row, evidenceTask)
			if evidenceTask.Status == task.StatusPending || evidenceTask.Status == task.StatusRunning {
				continue
			}
			_ = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				if err := s.lockConversation(ctx, tx, row.ConversationID); err != nil {
					return err
				}
				status := "queued"
				if evidenceTask.Status != task.StatusSuccess {
					status = "paused"
				}
				changed := tx.Model(&database.AIRun{}).Where("id = ? AND status = ?", row.ID, "waiting_task").Updates(map[string]any{"status": status, "resume_json": marshal(map[string]string{"task_id": state.TaskID})})
				if changed.Error != nil {
					return changed.Error
				}
				if changed.RowsAffected == 0 {
					return nil
				}
				if err := tx.Model(&interaction).Update("status", "completed").Error; err != nil {
					return err
				}
				return s.eventTx(ctx, tx, row.ConversationID, row.ID, "task."+evidenceTask.Status, map[string]any{"task_id": state.TaskID})
			})
			continue
		}
		var op database.AIOperation
		if s.db.WithContext(ctx).Where("run_id = ? AND task_id <> ''", row.ID).Order("created_at DESC").First(&op).Error != nil {
			continue
		}
		var taskRow database.Task
		if s.db.WithContext(ctx).First(&taskRow, "id = ?", op.TaskID).Error != nil {
			continue
		}
		s.recordTaskProgress(ctx, row, taskRow)
		if taskRow.Status == task.StatusPending || taskRow.Status == task.StatusRunning {
			continue
		}
		_ = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := s.lockConversation(ctx, tx, row.ConversationID); err != nil {
				return err
			}
			var locked database.AIRun
			if tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND status = ?", row.ID, "waiting_task").First(&locked).Error != nil {
				return nil
			}
			if op.Status != "completed" || taskRow.Status != task.StatusSuccess {
				if err := tx.Model(&locked).Updates(map[string]any{"status": "paused", "phase": "verify", "error": "Operation failed or its result is uncertain; inspect the result before continuing"}).Error; err != nil {
					return err
				}
				return s.eventTx(ctx, tx, row.ConversationID, row.ID, "run.paused", map[string]any{"operation_id": op.ID, "task_id": op.TaskID})
			}
			if err := tx.Model(&locked).Updates(map[string]any{"status": "queued", "resume_json": marshal(map[string]string{"operation_id": op.ID})}).Error; err != nil {
				return err
			}
			return s.eventTx(ctx, tx, row.ConversationID, row.ID, "task.completed", map[string]any{"operation_id": op.ID, "task_id": op.TaskID})
		})
	}
}

// All workflow transactions lock the conversation before its run/operation.
func (s *Service) lockConversation(ctx context.Context, tx *gorm.DB, key string) error {
	var row database.AIConversation
	return tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "id = ?", key).Error
}

func (f *executionFrame) progress(ctx context.Context, phase, kind string, payload any) error {
	return f.s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := f.s.lockConversation(ctx, tx, f.row.ConversationID); err != nil {
			return err
		}
		changed := tx.Model(&database.AIRun{}).Where("id = ? AND status = ? AND revision = ? AND lease_owner = ?", f.row.ID, "running", f.row.Revision, f.s.owner).Update("phase", phase)
		if changed.Error != nil {
			return changed.Error
		}
		if changed.RowsAffected != 1 {
			return ErrConflict
		}
		return f.s.eventTx(ctx, tx, f.row.ConversationID, f.row.ID, kind, payload)
	})
}
func (s *Service) recordTaskProgress(ctx context.Context, row database.AIRun, work database.Task) {
	if row.TaskProgress == work.Progress && row.TaskStatus == work.Status {
		return
	}
	_ = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.lockConversation(ctx, tx, row.ConversationID); err != nil {
			return err
		}
		changed := tx.Model(&database.AIRun{}).Where("id = ? AND status = ?", row.ID, "waiting_task").Updates(map[string]any{"task_progress": work.Progress, "task_status": work.Status})
		if changed.Error != nil {
			return changed.Error
		}
		if changed.RowsAffected != 1 {
			return nil
		}
		return s.eventTx(ctx, tx, row.ConversationID, row.ID, "task.progress", map[string]any{"task_id": work.ID, "status": work.Status, "progress": work.Progress})
	})
}

func (s *Service) runActorValid(ctx context.Context, run database.AIRun) error {
	var actor Actor
	if json.Unmarshal([]byte(run.ActorJSON), &actor) != nil {
		return ErrScope
	}
	return s.validateActor(ctx, actor)
}
func (s *Service) checkpointReady(ctx context.Context, run database.AIRun) error {
	var checkpoints []database.AICheckpoint
	if err := s.db.WithContext(ctx).Where("run_id = ?", run.ID).Find(&checkpoints).Error; err != nil {
		return err
	}
	if len(checkpoints) == 0 {
		return fmt.Errorf("%w: workflow checkpoint is unavailable; start a new diagnosis", ErrConflict)
	}
	for _, checkpoint := range checkpoints {
		if checkpoint.Revision != run.Revision || checkpoint.FrameworkVersion != FrameworkVersion || checkpoint.WorkflowVersion != WorkflowVersion || checkpoint.FormatVersion != CheckpointFormat {
			return fmt.Errorf("%w: workflow checkpoint version changed; start a new diagnosis", ErrConflict)
		}
		if _, err := s.secrets.Decrypt(checkpoint.Ciphertext); err != nil {
			return fmt.Errorf("%w: workflow checkpoint cannot be decrypted; start a new diagnosis", ErrConflict)
		}
	}
	return nil
}
