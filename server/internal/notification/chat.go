package notification

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
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

var ErrBinding = errors.New("chat identity is not bound or has been revoked")

func (s *Service) Bindings(ctx context.Context, user uint) ([]database.NotificationBinding, error) {
	rows := []database.NotificationBinding{}
	err := s.db.WithContext(ctx).Where("user_id = ?", user).Order("created_at DESC").Find(&rows).Error
	return rows, err
}
func (s *Service) BeginBinding(ctx context.Context, user uint, channelID string) (database.NotificationBinding, string, error) {
	c, err := s.Channel(ctx, channelID)
	if err != nil {
		return database.NotificationBinding{}, "", err
	}
	if !c.Enabled || !c.Config.Interactive {
		return database.NotificationBinding{}, "", errors.New("enable the interactive bot before binding")
	}
	var principal database.User
	if err = s.db.WithContext(ctx).First(&principal, user).Error; err != nil {
		return database.NotificationBinding{}, "", err
	}
	code := ID()
	row := database.NotificationBinding{ID: ID(), UserID: user, ChannelID: channelID, CodeHash: hash(code), Status: "pending", ExpiresAt: s.deps.Now().Add(10 * time.Minute)}
	err = s.db.WithContext(ctx).Create(&row).Error
	return row, code, err
}
func (s *Service) claimBinding(ctx context.Context, in Incoming, code string) error {
	if !in.Private || len(code) != 32 {
		return errors.New("binding codes must be submitted in a private bot conversation")
	}
	now := s.deps.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row database.NotificationBinding
		if err := tx.Where("code_hash = ? AND channel_id = ? AND status = ? AND expires_at > ?", hash(code), in.ChannelID, "pending", now).First(&row).Error; err != nil {
			return ErrBinding
		}
		var existing int64
		if err := tx.Model(&database.NotificationBinding{}).Where("channel_id = ? AND external_user_id = ? AND status IN ?", in.ChannelID, in.UserID, []string{"claimed", "active"}).Count(&existing).Error; err != nil {
			return err
		}
		if existing > 0 {
			return errors.New("this platform identity is already bound; revoke the previous binding first")
		}
		result := tx.Model(&row).Where("status = ?", "pending").Updates(map[string]any{"status": "claimed", "external_user_id": in.UserID, "external_name": redact.Bounded(in.Name, 128), "chat_id": in.ChatID})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrBinding
		}
		return nil
	})
}
func (s *Service) ConfirmBinding(ctx context.Context, user uint, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := s.db.WithContext(ctx).Model(&database.NotificationBinding{}).Where("id = ? AND user_id = ? AND status = ? AND expires_at > ?", id, user, "claimed", s.deps.Now()).Update("status", "active")
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrBinding
	}
	return nil
}
func (s *Service) RevokeBinding(ctx context.Context, user uint, id string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r := tx.Model(&database.NotificationBinding{}).Where("id = ? AND user_id = ?", id, user).Update("status", "revoked")
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return ErrBinding
		}
		return tx.Where("binding_id = ?", id).Delete(&database.NotificationAction{}).Error
	})
}
func (s *Service) ValidateBinding(ctx context.Context, user uint, id string) (database.NotificationBinding, error) {
	var row database.NotificationBinding
	if err := s.db.WithContext(ctx).Where("id = ? AND user_id = ? AND status = ?", id, user, "active").First(&row).Error; err != nil {
		return row, ErrBinding
	}
	c, err := s.Channel(ctx, row.ChannelID)
	if err != nil || !c.Enabled || !c.Config.Interactive {
		return row, ErrBinding
	}
	var n int64
	if err = s.db.WithContext(ctx).Model(&database.User{}).Where("id = ?", row.UserID).Count(&n).Error; err != nil || n == 0 {
		return row, ErrBinding
	}
	return row, nil
}
func (s *Service) Chats(ctx context.Context, id string) ([]database.NotificationChat, error) {
	rows := []database.NotificationChat{}
	err := s.db.WithContext(ctx).Where("channel_id = ?", id).Order("updated_at DESC").Limit(50).Find(&rows).Error
	return rows, err
}
func (s *Service) ApprovalToken(ctx context.Context, b database.NotificationBinding, op, chat string, expires time.Time) (string, error) {
	if _, err := s.ValidateBinding(ctx, b.UserID, b.ID); err != nil {
		return "", err
	}
	token := ID()
	row := database.NotificationAction{TokenHash: hash(token), OperationID: op, BindingID: b.ID, ChannelID: b.ChannelID, ChatID: chat, ExpiresAt: expires}
	return token, s.db.WithContext(ctx).Create(&row).Error
}
func (s *Service) ConsumeApproval(ctx context.Context, in Incoming, b database.NotificationBinding) (string, error) {
	if _, err := s.ValidateBinding(ctx, b.UserID, b.ID); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var row database.NotificationAction
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("token_hash = ? AND binding_id = ? AND channel_id = ? AND chat_id = ? AND consumed_at IS NULL AND expires_at > ?", hash(in.OperationID), b.ID, in.ChannelID, in.ChatID, s.deps.Now()).First(&row).Error; err != nil {
			return errors.New("approval preview expired, already used or belongs to another identity")
		}
		r := tx.Model(&row).Where("consumed_at IS NULL").Update("consumed_at", s.deps.Now())
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return ErrConflict
		}
		return nil
	})
	return row.OperationID, err
}
func (s *Service) Reply(ctx context.Context, in Incoming, message Message) error {
	c, err := s.Channel(ctx, in.ChannelID)
	if err != nil || !c.Enabled {
		return ErrBinding
	}
	m, err := s.material(c)
	if err != nil {
		return err
	}
	c.Config.ChatID = in.ChatID
	_, err = s.deps.Sender.Send(ctx, c, m, message)
	return err
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
	c, duplicate, err := s.discoverIncoming(ctx, in, generation)
	if err != nil {
		if observed != nil {
			observed.record("storage_error", "The message event arrived, but its conversation could not be saved")
		}
		return err
	}
	if observed != nil && in.Action == "" {
		observed.record("discovered", "")
	}
	if duplicate || in.DiscoveryOnly || !c.Config.Interactive {
		return nil
	}
	return s.handleChat(ctx, c, in)
}
func (s *Service) discoverIncoming(ctx context.Context, in Incoming, generation string) (Channel, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if generation != "" && s.connections[in.ChannelID].generation != generation {
		return Channel{}, false, context.Canceled
	}
	c, err := s.Channel(ctx, in.ChannelID)
	if err != nil || !needsConnection(c) {
		return Channel{}, false, ErrBinding
	}
	key := c.Provider + "|" + in.ChannelID + "|" + in.ID
	duplicate := false
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		claim := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&database.NotificationIncoming{Key: key, CreatedAt: s.deps.Now()})
		if claim.Error != nil {
			return claim.Error
		}
		duplicate = claim.RowsAffected == 0
		if duplicate || in.Action != "" {
			return nil
		}
		return tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&database.NotificationChat{ChannelID: c.ID, ChatID: in.ChatID, Name: redact.Bounded(in.Name, 128), Private: in.Private, UpdatedAt: s.deps.Now()}).Error
	})
	return c, duplicate, err
}
func (s *Service) handleChat(ctx context.Context, c Channel, in Incoming) error {
	var err error
	text := strings.TrimSpace(in.Text)
	if strings.HasPrefix(text, "/bind ") {
		err = s.claimBinding(ctx, in, strings.TrimSpace(strings.TrimPrefix(text, "/bind ")))
		answer := "已识别平台账号，请回到 SUMA 确认绑定。 / Return to SUMA to confirm your identity."
		if err != nil {
			answer = err.Error()
		}
		return s.Reply(ctx, in, Message{Text: answer})
	}
	if strings.HasPrefix(text, "/approve ") {
		in.Action = "preview"
		in.OperationID = strings.TrimSpace(strings.TrimPrefix(text, "/approve "))
	}
	if strings.HasPrefix(text, "/reject ") {
		in.Action = "preview"
		in.OperationID = strings.TrimSpace(strings.TrimPrefix(text, "/reject "))
	}
	var b database.NotificationBinding
	err = s.db.WithContext(ctx).Where("channel_id = ? AND external_user_id = ? AND status = ?", c.ID, in.UserID, "active").First(&b).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if err != nil {
		b = database.NotificationBinding{}
	}
	if b.ID != "" {
		if _, err = s.ValidateBinding(ctx, b.UserID, b.ID); err != nil {
			b = database.NotificationBinding{}
		}
	}
	if b.ID == "" {
		if in.Action != "" {
			// Do not persist the incoming operation value: it may be a secret
			// one-time approval token rather than a resource identifier.
			if err := s.deps.Audit.RecordAI(ctx, s.db, database.AIAudit{Action: "chat_access", Source: "chat", ExternalUserID: in.UserID, ChatID: in.ChatID, Resource: "operation", Result: "denied"}); err != nil {
				return err
			}
			return s.Reply(ctx, in, Message{Text: "不在操作白名单中，只能查询安全状态摘要。 / Read-only access: previews, approvals and changes require a site-confirmed identity."})
		}
		if !s.allowGuestQuery(in) {
			return s.Reply(ctx, in, Message{Text: "查询过于频繁，请稍后重试。 / Too many queries; try again shortly."})
		}
	}
	s.mu.Lock()
	h := s.chat
	s.mu.Unlock()
	if h != nil {
		h(in, b)
	}
	return nil
}

func (s *Service) allowGuestQuery(in Incoming) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.deps.Now()
	for key, at := range s.queryAt {
		if !at.After(now.Add(-10 * time.Second)) {
			delete(s.queryAt, key)
		}
	}
	// Hash stable IDs so the map has fixed-length keys and bounded capacity.
	key := hash(in.ChannelID + "|" + in.UserID)
	if _, exists := s.queryAt[key]; exists || len(s.queryAt) >= 2048 {
		return false
	}
	s.queryAt[key] = now
	return true
}
func (s *Service) startChatLocked(c Channel) {
	if _, exists := s.chatCancel[c.ID]; exists {
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	generation := ID()
	observed := &feishuMessageObservation{now: s.deps.Now}
	s.connections[c.ID] = connectionEntry{generation: generation, messages: observed, ConnectionStatus: ConnectionStatus{State: "connecting"}}
	previousDone := s.chatDone[c.ID]
	done := make(chan struct{})
	s.chatDone[c.ID] = done
	s.chatCancel[c.ID] = cancel
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
			s.telegram(ctx, a, c, m)
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
	Text string `json:"text"`
}
type tgUpdate struct {
	ID       int64      `json:"update_id"`
	Message  *tgMessage `json:"message"`
	Callback *struct {
		ID      string    `json:"id"`
		From    tgUser    `json:"from"`
		Message tgMessage `json:"message"`
		Data    string    `json:"data"`
	} `json:"callback_query"`
}

func (s *Service) telegram(ctx context.Context, a *Adapter, c Channel, m Secrets) {
	cursorKey := "notification.cursor." + hash(m.Token)
	var saved database.Setting
	_ = s.db.WithContext(ctx).First(&saved, "key = ?", cursorKey).Error
	offset, _ := strconv.ParseInt(saved.Value, 10, 64)
	check, err := a.request(ctx, "GET", "https://api.telegram.org/bot"+m.Token+"/getWebhookInfo", nil, "", false, m)
	if err != nil {
		s.chatError(c.ID, "Telegram connection failed; check bot token and outbound HTTPS")
		return
	}
	var webhook struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(check["result"], &webhook)
	if webhook.URL != "" {
		s.chatError(c.ID, "Telegram bot has an existing webhook; use a dedicated bot or remove that webhook before enabling polling")
		return
	}
	for ctx.Err() == nil {
		result, err := a.request(ctx, "POST", "https://api.telegram.org/bot"+m.Token+"/getUpdates", map[string]any{"offset": offset, "timeout": 25, "allowed_updates": []string{"message", "callback_query"}}, "", false, m)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			s.chatError(c.ID, "Telegram polling disconnected; reconnecting")
			if !wait(ctx, 5*time.Second) {
				return
			}
			continue
		}
		s.chatError(c.ID, "")
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
				if !u.Message.From.Bot {
					in.UserID = strconv.FormatInt(u.Message.From.ID, 10)
					in.Name = u.Message.From.Name
					in.ChatID = strconv.FormatInt(u.Message.Chat.ID, 10)
					in.Private = u.Message.Chat.Type == "private"
					in.Text = u.Message.Text
				}
			} else if u.Callback != nil {
				in.UserID = strconv.FormatInt(u.Callback.From.ID, 10)
				in.Name = u.Callback.From.Name
				in.ChatID = strconv.FormatInt(u.Callback.Message.Chat.ID, 10)
				in.Private = u.Callback.Message.Chat.Type == "private"
				parts := strings.SplitN(u.Callback.Data, ":", 2)
				if len(parts) == 2 {
					in.Action = parts[0]
					in.OperationID = parts[1]
				}
				_, _ = a.request(ctx, "POST", "https://api.telegram.org/bot"+m.Token+"/answerCallbackQuery", map[string]string{"callback_query_id": u.Callback.ID}, "", false, m)
			}
			if in.UserID != "" {
				s.error(s.HandleIncoming(ctx, in))
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
func (s *Service) chatError(id, message string) {
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
func (s *Service) ValidateChatActor(ctx context.Context, user uint, bindingID string) error {
	_, err := s.ValidateBinding(ctx, user, bindingID)
	return err
}

// Input commands can be corrected without burning their one-use token. The
// caller consumes it only after the workflow accepts the validated answer.
func (s *Service) PeekAction(ctx context.Context, in Incoming, b database.NotificationBinding) (string, error) {
	if _, err := s.ValidateBinding(ctx, b.UserID, b.ID); err != nil {
		return "", err
	}
	var row database.NotificationAction
	if err := s.db.WithContext(ctx).Where("token_hash = ? AND binding_id = ? AND channel_id = ? AND chat_id = ? AND consumed_at IS NULL AND expires_at > ?", hash(in.OperationID), b.ID, in.ChannelID, in.ChatID, s.deps.Now()).First(&row).Error; err != nil {
		return "", ErrConflict
	}
	return row.OperationID, nil
}
