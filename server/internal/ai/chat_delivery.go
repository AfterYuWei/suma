package ai

import (
	"context"
	"encoding/json"
	"time"

	"github.com/suma/suma/server/internal/database"
	"gorm.io/gorm"
)

func (s *Service) SetChatDelivery(callback func(context.Context, Actor, WorkflowEvent, Run) error) {
	s.mu.Lock()
	s.deps.Deliver = callback
	s.mu.Unlock()
	s.signalRuntime()
}
func (s *Service) deliverChats(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		s.mu.Lock()
		callback := s.deps.Deliver
		s.mu.Unlock()
		if callback == nil {
			continue
		}
		var rows []database.AIConversation
		if s.db.WithContext(ctx).Where("source = ? AND chat_sent_seq < event_seq AND (chat_lease_until IS NULL OR chat_lease_until < ?)", "chat", s.deps.Now()).Order("updated_at ASC").Limit(10).Find(&rows).Error != nil {
			continue
		}
		for _, conv := range rows {
			if ctx.Err() != nil {
				return
			}
			until := s.deps.Now().Add(time.Minute)
			claim := s.db.WithContext(ctx).Model(&database.AIConversation{}).Where("id = ? AND chat_sent_seq = ? AND (chat_lease_until IS NULL OR chat_lease_until < ?)", conv.ID, conv.ChatSentSeq, s.deps.Now()).Update("chat_lease_until", until)
			if claim.Error != nil || claim.RowsAffected != 1 {
				continue
			}
			var entry database.AIWorkflowEvent
			if s.db.WithContext(ctx).Where("conversation_id = ? AND seq > ? AND type IN ?", conv.ID, conv.ChatSentSeq, chatEventKinds).Order("seq ASC").First(&entry).Error != nil {
				_ = s.db.WithContext(ctx).Model(&database.AIConversation{}).Where("id = ? AND chat_sent_seq = ?", conv.ID, conv.ChatSentSeq).Updates(map[string]any{"chat_sent_seq": conv.EventSeq, "chat_lease_until": nil}).Error
				continue
			}
			if isChatProgress(entry.Type) {
				entry = s.coalesceChatProgress(ctx, entry)
			}
			run, err := s.Run(ctx, entry.RunID)
			var actor Actor
			_ = json.Unmarshal([]byte(run.ActorJSON), &actor)
			if err == nil {
				sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				if s.validateActor(sendCtx, actor) == nil {
					s.mu.Lock()
					cfg, modelKey := cloneSettings(s.cfg), s.key
					s.mu.Unlock()
					if workflowConfigHash(cfg, modelKey) != run.ConfigHash {
						run.Result = Result{Summary: "任务权限或模型配置已变化，请在工作台调整任务。 / Task permissions or model settings changed; adjust the task in SUMA."}
						run.Error = ""
						if entry.Type != "run.paused" {
							err = nil
							cancel()
							_ = s.db.WithContext(ctx).Model(&database.AIConversation{}).Where("id = ? AND chat_sent_seq = ?", conv.ID, conv.ChatSentSeq).Updates(map[string]any{"chat_sent_seq": entry.Seq, "chat_lease_until": nil}).Error
							continue
						}
					}
					err = callback(sendCtx, actor, WorkflowEvent{AIWorkflowEvent: entry, Payload: json.RawMessage(entry.PayloadJSON)}, run)
				} else {
					err = ErrScope
				}
				cancel()
			}
			if err == nil {
				_ = s.db.WithContext(ctx).Model(&database.AIConversation{}).Where("id = ? AND chat_sent_seq = ?", conv.ID, conv.ChatSentSeq).Updates(map[string]any{"chat_sent_seq": entry.Seq, "chat_lease_until": nil}).Error
			} else if err != ErrScope {
				_ = s.db.WithContext(ctx).Model(&database.AIConversation{}).Where("id = ? AND chat_sent_seq = ?", conv.ID, conv.ChatSentSeq).Update("chat_lease_until", s.deps.Now().Add(3*time.Second)).Error
			}
			// Failed sends retain their durable cursor and lease, then retry. Removing a
			// binding never falls back to a guest delivery of resource information.
		}
	}
}

var chatProgressKinds = []string{"run.queued", "run.output", "tool.started", "tool.completed", "task.progress"}
var chatEventKinds = append(append([]string{}, chatProgressKinds...), "run.waiting_input", "run.waiting_approval", "run.completed", "run.failed", "run.paused", "run.canceled", "operation.completed", "operation.failed", "operation.interrupted")

func isChatProgress(kind string) bool { return has(chatProgressKinds, kind) }

// Replaceable snapshots can be merged, but never cross a task boundary or
// a durable question, approval or terminal event.
func (s *Service) coalesceChatProgress(ctx context.Context, first database.AIWorkflowEvent) database.AIWorkflowEvent {
	var barrier database.AIWorkflowEvent
	query := s.db.WithContext(ctx).Where("conversation_id = ? AND seq > ? AND type IN ? AND (run_id <> ? OR type NOT IN ?)", first.ConversationID, first.Seq, chatEventKinds, first.RunID, chatProgressKinds)
	err := query.Order("seq ASC").First(&barrier).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return first
	}
	query = s.db.WithContext(ctx).Where("conversation_id = ? AND run_id = ? AND seq >= ? AND type IN ?", first.ConversationID, first.RunID, first.Seq, chatProgressKinds)
	if barrier.Seq > 0 {
		query = query.Where("seq < ?", barrier.Seq)
	}
	var latest database.AIWorkflowEvent
	if query.Order("seq DESC").First(&latest).Error == nil {
		return latest
	}
	return first
}
