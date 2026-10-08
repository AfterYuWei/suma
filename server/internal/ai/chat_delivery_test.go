package ai

import (
	"context"
	"testing"

	"github.com/suma/suma/server/internal/database"
)

func TestChatProgressCoalescingPreservesApprovalAndTaskBoundaries(t *testing.T) {
	s, _, _ := aiFixture(t)
	ctx := context.Background()
	conversation, err := s.CreateConversation(ctx, ConversationInput{}, Actor{UserID: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ run, kind string }{
		{"a", "run.queued"}, {"a", "run.output"}, {"a", "tool.started"}, {"a", "run.output"}, {"a", "run.waiting_approval"},
		{"a", "run.output"}, {"b", "run.queued"}, {"b", "run.output"}, {"a", "run.output"},
	} {
		if err := s.eventTx(ctx, s.db, conversation.ID, item.run, item.kind, map[string]string{"text": "snapshot"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ first, want uint64 }{{1, 4}, {6, 6}, {7, 8}} {
		var entry database.AIWorkflowEvent
		if err := s.db.Where("conversation_id = ? AND seq = ?", conversation.ID, tc.first).First(&entry).Error; err != nil {
			t.Fatal(err)
		}
		if latest := s.coalesceChatProgress(ctx, entry); latest.Seq != tc.want {
			t.Fatal("coalescing skipped an approval or crossed into another task", tc, latest.Seq)
		}
	}
}
