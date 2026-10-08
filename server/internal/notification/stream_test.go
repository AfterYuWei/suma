package notification

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/database"
)

type streamRecorder struct {
	mu        sync.Mutex
	updates   []StreamUpdate
	created   int
	failFinal int
	block     chan struct{}
	entered   chan struct{}
}

func (*streamRecorder) Send(context.Context, Channel, Secrets, Message) (string, error) {
	return "ordinary", nil
}
func (r *streamRecorder) UpdateStream(ctx context.Context, _ Channel, _ Secrets, _ string, receipt StreamReceipt, update StreamUpdate) (StreamReceipt, error) {
	if r.block != nil {
		select {
		case r.entered <- struct{}{}:
		default:
		}
		select {
		case <-r.block:
		case <-ctx.Done():
			return receipt, ctx.Err()
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if receipt.MessageID == "" {
		r.created++
		receipt.CardID, receipt.MessageID, receipt.Mode = "card_one", "om_one", "cardkit"
	}
	r.updates = append(r.updates, update)
	receipt.Sequence++
	receipt.Text, receipt.Status, receipt.Closed = update.Text, update.Status, update.Final
	if update.Final && r.failFinal > 0 {
		r.failFinal--
		receipt.Closed = false
		return receipt, errors.New("final close failed")
	}
	return receipt, nil
}

func TestIncompleteStreamRetryDoesNotDuplicateTheAcceptedText(t *testing.T) {
	s, _, b, in, r := boundStreamFixture(t)
	ctx := context.Background()
	if err := s.ReplyStream(ctx, in, b, StreamUpdate{Key: "run", EventSeq: 1, Text: "partial answer", Status: "working"}); err != nil {
		t.Fatal(err)
	}
	r.failFinal = 1
	final := StreamUpdate{Key: "run", EventSeq: 2, Text: "stream failed", Status: "failed", Final: true, Incomplete: true}
	if err := s.ReplyStream(ctx, in, b, final); err == nil {
		t.Fatal("failed close was acknowledged")
	}
	if err := s.ReplyStream(ctx, in, b, final); err != nil {
		t.Fatal(err)
	}
	if len(r.updates) != 3 || r.updates[1].Text != r.updates[2].Text || strings.Count(r.updates[2].Text, "Incomplete output") != 1 {
		t.Fatal("retry appended incomplete content twice", r.updates)
	}
}

func boundStreamFixture(t *testing.T) (*Service, Channel, database.NotificationBinding, Incoming, *streamRecorder) {
	t.Helper()
	s, db, _, _ := fixture(t)
	if err := db.Create(&database.User{Username: "stream-admin", PasswordHash: "fixture"}).Error; err != nil {
		t.Fatal(err)
	}
	c := feishuChannel(t, s, "cli_stream")
	c.Config.Interactive = true
	var err error
	c, err = s.SaveChannel(context.Background(), c.ID, ChannelInput{Name: c.Name, Provider: c.Provider, Enabled: true, Version: c.Version, Config: c.Config})
	if err != nil {
		t.Fatal(err)
	}
	b := database.NotificationBinding{ID: "binding", UserID: 1, ChannelID: c.ID, ExternalUserID: "tenant:user", ChatID: "oc_private", CodeHash: "private-code-hash", Status: "active", ExpiresAt: time.Now().Add(time.Hour)}
	if err := db.Create(&b).Error; err != nil {
		t.Fatal(err)
	}
	r := &streamRecorder{}
	s.deps.Sender = r
	t.Cleanup(s.Stop)
	return s, c, b, Incoming{ChannelID: c.ID, UserID: b.ExternalUserID, ChatID: b.ChatID, Private: true}, r
}

func TestChatStreamPersistsReceiptRejectsLateUpdatesAndKeepsIncompleteOutput(t *testing.T) {
	s, _, b, in, r := boundStreamFixture(t)
	ctx := context.Background()
	for _, u := range []StreamUpdate{{Key: "run", EventSeq: 1, Text: "**partial**", Status: "working"}, {Key: "run", EventSeq: 2, Text: "stream failed", Status: "failed", Final: true, Incomplete: true}, {Key: "run", EventSeq: 1, Text: "late old answer"}} {
		if err := s.ReplyStream(ctx, in, b, u); err != nil {
			t.Fatal(err)
		}
	}
	if r.created != 1 || len(r.updates) != 2 || !strings.Contains(r.updates[1].Text, "**partial**") || !strings.Contains(r.updates[1].Text, "Incomplete output") {
		t.Fatal("stream duplicated, regressed or discarded partial output", r.updates)
	}
	// A new service instance uses the same receipt and durable sequence.
	reopened := NewService(s.db, s.secrets, Dependencies{Sender: r})
	defer reopened.Stop()
	if err := reopened.ReplyStream(ctx, in, b, StreamUpdate{Key: "run", EventSeq: 3, Text: "new continued answer", Status: "done", Final: true}); err != nil {
		t.Fatal(err)
	}
	if r.created != 1 {
		t.Fatal("restart created another message")
	}
	var row database.NotificationChatStream
	if err := s.db.First(&row).Error; err != nil || row.EventSeq != 3 || !row.Closed || row.MessageID != "om_one" {
		t.Fatal("receipt or final cursor was not retained", err, row)
	}
	if err := s.ReplyStream(ctx, Incoming{ChannelID: in.ChannelID, UserID: "forged", ChatID: in.ChatID}, b, StreamUpdate{Key: "run", EventSeq: 4, Text: "private", Final: true}); !errors.Is(err, ErrBinding) {
		t.Fatal("stream accepted a forged identity", err)
	}
}

func TestChatStreamLifecycleCancelsInflightProviderWrites(t *testing.T) {
	for _, action := range []string{"revoke", "pause", "credentials", "chat off", "delete", "shutdown"} {
		t.Run(action, func(t *testing.T) {
			s, c, b, in, r := boundStreamFixture(t)
			r.block, r.entered = make(chan struct{}), make(chan struct{}, 1)
			finished := make(chan error, 1)
			go func() {
				finished <- s.ReplyStream(context.Background(), in, b, StreamUpdate{Key: "run", EventSeq: 1, Text: "private partial"})
			}()
			select {
			case <-r.entered:
			case <-time.After(3 * time.Second):
				t.Fatal("provider write did not start")
			}
			var err error
			switch action {
			case "revoke":
				err = s.RevokeBinding(context.Background(), 1, b.ID)
			case "delete":
				err = s.DeleteChannel(context.Background(), c.ID)
			case "shutdown":
				s.Stop()
			default:
				input := ChannelInput{Name: c.Name, Provider: c.Provider, Enabled: true, Version: c.Version, Config: c.Config}
				if action == "pause" {
					input.Enabled = false
				}
				if action == "credentials" {
					input.Secrets = &Secrets{Token: "rotated-app-secret"}
				}
				if action == "chat off" {
					input.Config.Interactive = false
				}
				_, err = s.SaveChannel(context.Background(), c.ID, input)
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-finished:
				if err == nil {
					t.Fatal("canceled provider write was reported as delivered")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("old provider callback was not canceled promptly")
			}
			if len(r.updates) != 0 {
				t.Fatal("canceled stream wrote private output")
			}
		})
	}
}
