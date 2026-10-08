package notification

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/redact"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type StreamReceipt struct {
	CardID, MessageID, Mode string
	Text, Status            string
	Sequence                uint64
	Closed                  bool
}

type StreamUpdate struct {
	Key, Text, Status string
	EventSeq          uint64
	Final, Incomplete bool
}

// StreamingSender updates one receipt. Returned receipt changes are retained
// even on failure, so an uncertain send is retried with the same UUID and card.
type StreamingSender interface {
	UpdateStream(context.Context, Channel, Secrets, string, StreamReceipt, StreamUpdate) (StreamReceipt, error)
}

type streamRequest struct {
	channel, binding string
	cancel           context.CancelFunc
}

func (s *Service) cancelStreamsLocked(channel, binding string) {
	for _, request := range s.streamCancel {
		if (channel == "" || request.channel == channel) && (binding == "" || request.binding == binding) {
			request.cancel()
		}
	}
}

func (s *Service) streamContext(ctx context.Context, channel, binding string) (context.Context, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return nil, nil, context.Canceled
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	key := ID()
	s.streamCancel[key] = streamRequest{channel: channel, binding: binding, cancel: cancel}
	s.wg.Add(1)
	return ctx, func() {
		cancel()
		s.mu.Lock()
		delete(s.streamCancel, key)
		s.mu.Unlock()
		s.wg.Done()
	}, nil
}

func (s *Service) SupportsStreaming(ctx context.Context, channel string) bool {
	c, err := s.Channel(ctx, channel)
	_, supported := s.deps.Sender.(StreamingSender)
	return err == nil && c.Provider == "feishu_app" && supported
}

// ReplyStream has an independent per-run cursor and row lock. Credentials,
// binding, channel and chat are revalidated before every provider write.
func (s *Service) ReplyStream(ctx context.Context, in Incoming, binding database.NotificationBinding, update StreamUpdate) error {
	if update.Key == "" || len(update.Key) > 128 || update.EventSeq == 0 {
		return ErrInvalid
	}
	c, err := s.Channel(ctx, in.ChannelID)
	if err != nil || !c.Enabled || !c.Config.Interactive {
		return ErrBinding
	}
	active, err := s.ValidateBinding(ctx, binding.UserID, binding.ID)
	if err != nil || active.ChannelID != c.ID || active.ExternalUserID != in.UserID || active.ChatID != in.ChatID {
		return ErrBinding
	}
	adapter, supported := s.deps.Sender.(StreamingSender)
	if !supported || c.Provider != "feishu_app" {
		if update.Final {
			return s.Reply(ctx, in, Message{Text: update.Text})
		}
		return nil
	}
	sendCtx, done, err := s.streamContext(ctx, c.ID, binding.ID)
	if err != nil {
		return err
	}
	defer done()
	// Persist known receipts after transport cancellation, so recovery never
	// creates a second message for a card whose send may already have succeeded.
	metadataCtx, cancelMetadata := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancelMetadata()
	var providerErr error
	err = s.db.WithContext(metadataCtx).Transaction(func(tx *gorm.DB) error {
		active, err := s.ValidateBinding(sendCtx, binding.UserID, binding.ID)
		if err != nil || active.ChannelID != c.ID || active.ExternalUserID != in.UserID || active.ChatID != in.ChatID {
			return ErrBinding
		}
		current, err := s.Channel(sendCtx, c.ID)
		if err != nil || !current.Enabled || !current.Config.Interactive {
			return ErrBinding
		}
		material, err := s.material(current)
		if err != nil {
			return err
		}
		identity := hash(current.Provider + "|" + current.Config.AppID + "|" + material.Token)
		row := database.NotificationChatStream{ID: hash(c.ID + "|" + update.Key), ChannelID: c.ID, BindingID: binding.ID, ChatID: in.ChatID, IdentityHash: identity}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "id = ?", row.ID).Error; err != nil {
			return err
		}
		if row.BindingID != binding.ID || row.ChatID != in.ChatID || row.IdentityHash != identity {
			return ErrBinding
		}
		if update.EventSeq <= row.EventSeq {
			return nil
		}
		if update.Text == "" && !update.Final {
			update.Text = row.Text
		}
		if update.Incomplete && row.Text != "" {
			const prefix = "输出未完成 / Incomplete output\n\n"
			if strings.HasPrefix(row.Text, prefix) && strings.HasSuffix(row.Text, "\n\n"+update.Text) {
				update.Text = row.Text
			} else {
				update.Text = prefix + redact.Bounded(row.Text, 12000) + "\n\n" + update.Text
			}
		}
		update.Text, update.Status = redact.Bounded(update.Text, 16000), redact.Bounded(update.Status, 256)
		if material.Token != "" {
			update.Text = strings.ReplaceAll(update.Text, material.Token, "[redacted]")
		}
		current.Config.ChatID = in.ChatID
		receipt := StreamReceipt{CardID: row.CardID, MessageID: row.MessageID, Mode: row.Mode, Sequence: row.Sequence, Closed: row.Closed, Text: row.Text, Status: row.Status}
		receipt, providerErr = adapter.UpdateStream(sendCtx, current, material, row.ID, receipt, update)
		if sendCtx.Err() != nil {
			providerErr = sendCtx.Err()
		}
		row.CardID, row.MessageID, row.Mode, row.Sequence, row.Closed = receipt.CardID, receipt.MessageID, receipt.Mode, receipt.Sequence, receipt.Closed
		row.Text, row.Status = receipt.Text, receipt.Status
		if providerErr == nil {
			row.EventSeq = update.EventSeq
		}
		return tx.Save(&row).Error
	})
	if err != nil {
		return err
	}
	if providerErr != nil {
		return errors.New(redact.Bounded(providerErr.Error(), 512))
	}
	return nil
}
