package notification

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync/atomic"
	"time"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/outbound"
	"github.com/suma/suma/server/internal/redact"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Service) Chats(ctx context.Context, id string) ([]database.NotificationChat, error) {
	rows := []database.NotificationChat{}
	err := s.db.WithContext(ctx).Where("channel_id = ?", id).Order("updated_at DESC").Limit(50).Find(&rows).Error
	return rows, err
}
func (s *Service) HandleIncoming(ctx context.Context, in Incoming) error {
	return s.handleIncoming(ctx, in, "")
}
func (s *Service) handleIncoming(ctx context.Context, in Incoming, generation string) error {
	return s.handleIncomingObserved(ctx, in, generation, nil)
}
func (s *Service) handleIncomingObserved(ctx context.Context, in Incoming, generation string, observed *feishuMessageObservation) error {
	if in.ID == "" || in.UserID == "" || in.ChatID == "" {
		return ErrInvalid
	}
	err := s.discoverIncoming(ctx, in, generation)
	if err != nil {
		if observed != nil {
			observed.record("storage_error", "The message event arrived, but its conversation could not be saved")
		}
		return err
	}
	if observed != nil {
		observed.record("discovered", "")
	}
	return nil
}
func (s *Service) discoverIncoming(ctx context.Context, in Incoming, generation string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if generation != "" && s.connections[in.ChannelID].generation != generation {
		return context.Canceled
	}
	c, err := s.Channel(ctx, in.ChannelID)
	if err != nil || !needsConnection(c) {
		return ErrInvalid
	}
	key := c.Provider + "|" + in.ChannelID + "|" + in.ID
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		claim := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&database.NotificationIncoming{Key: key, CreatedAt: s.deps.Now()})
		if claim.Error != nil {
			return claim.Error
		}
		if claim.RowsAffected == 0 {
			return nil
		}
		return tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&database.NotificationChat{ChannelID: c.ID, ChatID: in.ChatID, Name: redact.Bounded(in.Name, 128), Private: in.Private, UpdatedAt: s.deps.Now()}).Error
	})
	return err
}
func (s *Service) startDiscoveryLocked(c Channel) {
	if _, exists := s.discoveryCancel[c.ID]; exists {
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	generation := ID()
	observed := &feishuMessageObservation{now: s.deps.Now}
	s.connections[c.ID] = connectionEntry{generation: generation, messages: observed, ConnectionStatus: ConnectionStatus{State: "connecting"}}
	previousDone := s.discoveryDone[c.ID]
	done := make(chan struct{})
	s.discoveryDone[c.ID] = done
	s.discoveryCancel[c.ID] = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer close(done)
		defer s.connectionState(c.ID, generation, ConnectionStatus{State: "stopped"})
		if previousDone != nil {
			select {
			case <-previousDone:
			case <-ctx.Done():
				return
			}
		}
		if ctx.Err() != nil {
			return
		}
		m, err := s.material(c)
		if err != nil {
			s.error(err)
			return
		}
		status := func(state ConnectionStatus) {
			state.Error = hide(state.Error, m)
			s.connectionState(c.ID, generation, state)
		}
		incoming := func(in Incoming) error { return s.handleIncomingObserved(ctx, in, generation, observed) }
		if c.Provider == "feishu_app" {
			if s.deps.FeishuConnect != nil {
				s.deps.FeishuConnect(ctx, c, m, status, incoming)
			} else {
				s.feishu(ctx, c, m, status, incoming, observed)
			}
			return
		}
		a, ok := s.deps.Sender.(*Adapter)
		if !ok {
			return
		}
		if c.Provider == "telegram" {
			s.telegram(ctx, a, c, m, incoming)
		}
	}()
}
func wait(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

type tgUser struct {
	ID   int64  `json:"id"`
	Name string `json:"first_name"`
	Bot  bool   `json:"is_bot"`
}
type tgChat struct {
	ID    int64  `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
}
type tgMessage struct {
	From tgUser `json:"from"`
	Chat tgChat `json:"chat"`
}
type tgUpdate struct {
	ID      int64      `json:"update_id"`
	Message *tgMessage `json:"message"`
}

func (s *Service) telegram(ctx context.Context, a *Adapter, c Channel, m Secrets, incoming func(Incoming) error) {
	cursorKey := "notification.cursor." + hash(m.Token)
	var saved database.Setting
	_ = s.db.WithContext(ctx).First(&saved, "key = ?", cursorKey).Error
	offset, _ := strconv.ParseInt(saved.Value, 10, 64)
	check, err := a.request(ctx, "GET", "https://api.telegram.org/bot"+m.Token+"/getWebhookInfo", nil, "", false, m)
	if err != nil {
		s.discoveryError(c.ID, "Telegram connection failed; check bot token and outbound HTTPS")
		return
	}
	var webhook struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(check["result"], &webhook)
	if webhook.URL != "" {
		s.discoveryError(c.ID, "Telegram bot has an existing webhook; use a dedicated bot or remove that webhook before enabling polling")
		return
	}
	for ctx.Err() == nil {
		result, err := a.request(ctx, "POST", "https://api.telegram.org/bot"+m.Token+"/getUpdates", map[string]any{"offset": offset, "timeout": 25, "allowed_updates": []string{"message"}}, "", false, m)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			s.discoveryError(c.ID, "Telegram polling disconnected; reconnecting")
			if !wait(ctx, 5*time.Second) {
				return
			}
			continue
		}
		s.discoveryError(c.ID, "")
		var updates []tgUpdate
		if err = json.Unmarshal(result["result"], &updates); err != nil {
			if !wait(ctx, time.Second) {
				return
			}
			continue
		}
		for _, u := range updates {
			in := Incoming{ID: strconv.FormatInt(u.ID, 10), ChannelID: c.ID}
			if u.Message != nil {
				if !u.Message.From.Bot && u.Message.From.ID != 0 && u.Message.Chat.ID != 0 {
					in.UserID = strconv.FormatInt(u.Message.From.ID, 10)
					in.Name = u.Message.From.Name
					in.ChatID = strconv.FormatInt(u.Message.Chat.ID, 10)
					in.Private = u.Message.Chat.Type == "private"
					if !in.Private && u.Message.Chat.Title != "" {
						in.Name = u.Message.Chat.Title
					}
				}
			}
			if in.UserID != "" {
				if err := incoming(in); err != nil {
					s.error(err)
					break
				}
			}
			offset = max(offset, u.ID+1)
			s.error(s.db.WithContext(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(&database.Setting{Key: cursorKey, Value: strconv.FormatInt(offset, 10)}).Error)
		}
	}
}
func ptr(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

type quietSDKLogger struct{}

func (quietSDKLogger) Debug(context.Context, ...interface{}) {}
func (quietSDKLogger) Info(context.Context, ...interface{})  {}
func (quietSDKLogger) Warn(context.Context, ...interface{})  {}
func (quietSDKLogger) Error(context.Context, ...interface{}) {}
func (s *Service) discoveryError(id, message string) {
	s.error(s.db.Model(&database.NotificationChannel{}).Where("id = ?", id).UpdateColumn("last_error", message).Error)
}
func (s *Service) feishu(ctx context.Context, c Channel, m Secrets, status func(ConnectionStatus), incoming func(Incoming) error, observed *feishuMessageObservation) {
	// A bounded queue keeps Feishu callbacks below the three-second response deadline.
	var botID atomic.Value
	botID.Store("")
	identityDone := make(chan struct{})
	go func() {
		defer close(identityDone)
		adapter, ok := s.deps.Sender.(*Adapter)
		if !ok {
			return
		}
		for ctx.Err() == nil {
			info, err := adapter.Check(ctx, c, m)
			if err == nil {
				if bot, ok := info["bot"].(map[string]any); ok {
					id, _ := bot["open_id"].(string)
					if id != "" {
						botID.Store(id)
						return
					}
				}
			}
			if !wait(ctx, 5*time.Second) {
				return
			}
		}
	}()
	defer func() { <-identityDone }()
	queue := make(chan Incoming, 64)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case in := <-queue:
				s.error(incoming(in))
			}
		}
	}()
	defer func() { <-done }()
	handler := feishuEventHandler(c, func() string { return botID.Load().(string) }, func(in Incoming) error {
		select {
		case queue <- in:
			return nil
		default:
			return errors.New("SUMA incoming queue is busy")
		}
	}, func(result string) { observed.record(result, "") })
	for ctx.Err() == nil {
		client := larkws.NewClient(c.Config.AppID, m.Token, larkws.WithEventHandler(handler), larkws.WithLogger(quietSDKLogger{}), larkws.WithLogLevel(larkcore.LogLevelError), larkws.WithHttpClient(outbound.Client(false)))
		client.SetOnReady(func() { status(ConnectionStatus{State: "connected"}) })
		client.SetOnReconnected(func() { status(ConnectionStatus{State: "connected"}) })
		client.SetOnReconnecting(func() { status(ConnectionStatus{State: "reconnecting"}) })
		client.SetOnError(func(error) {
			status(ConnectionStatus{State: "reconnecting", Error: "Feishu connection interrupted; reconnecting"})
		})
		err := client.Start(ctx)
		client.Close()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			status(ConnectionStatus{State: "error", Error: "Feishu connection failed; check App ID, App Secret, bot capability and outbound HTTPS/WSS"})
		}
		if !wait(ctx, 5*time.Second) {
			return
		}
	}
}
