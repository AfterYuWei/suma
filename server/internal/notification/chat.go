package notification

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
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
	if in.ID == "" || in.UserID == "" || in.ChatID == "" {
		return ErrInvalid
	}
	c, err := s.Channel(ctx, in.ChannelID)
	if err != nil || !c.Enabled || !c.Config.Interactive {
		return ErrBinding
	}
	key := c.Provider + "|" + in.ChannelID + "|" + in.ID
	claim := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&database.NotificationIncoming{Key: key, CreatedAt: s.deps.Now()})
	if claim.Error != nil {
		return claim.Error
	}
	if claim.RowsAffected == 0 {
		return nil
	}
	s.error(s.db.WithContext(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(&database.NotificationChat{ChannelID: c.ID, ChatID: in.ChatID, Name: redact.Bounded(in.Name, 128), Private: in.Private, UpdatedAt: s.deps.Now()}).Error)
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
	s.chatCancel[c.ID] = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		m, err := s.material(c)
		if err != nil {
			s.error(err)
			return
		}
		a, ok := s.deps.Sender.(*Adapter)
		if !ok {
			return
		}
		if c.Provider == "telegram" {
			s.telegram(ctx, a, c, m)
		} else if c.Provider == "feishu_app" {
			s.feishu(ctx, c, m)
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
func (s *Service) feishu(ctx context.Context, c Channel, m Secrets) {
	// A bounded queue keeps Feishu callbacks below the three-second response deadline.
	botID := ""
	if adapter, ok := s.deps.Sender.(*Adapter); ok {
		info, err := adapter.Check(ctx, c, m)
		if err != nil {
			s.chatError(c.ID, "Feishu bot identity unavailable")
			return
		}
		if bot, ok := info["bot"].(map[string]any); ok {
			botID, _ = bot["open_id"].(string)
		}
	}
	queue := make(chan Incoming, 64)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case in := <-queue:
				s.error(s.HandleIncoming(ctx, in))
			}
		}
	}()
	defer func() { <-done }()
	handler := dispatcher.NewEventDispatcher("", "").OnP2MessageReceiveV1(func(_ context.Context, e *larkim.P2MessageReceiveV1) error {
		if e == nil || e.Event == nil || e.Event.Message == nil || e.Event.Sender == nil || e.Event.Sender.SenderId == nil {
			return nil
		}
		msg, sender := e.Event.Message, e.Event.Sender
		if ptr(sender.TenantKey) == "" || ptr(sender.SenderId.OpenId) == "" || ptr(msg.ChatId) == "" || ptr(msg.MessageId) == "" {
			return nil
		}
		if ptr(sender.SenderType) != "user" || ptr(msg.MessageType) != "text" {
			return nil
		}
		private := ptr(msg.ChatType) == "p2p"
		if !private {
			mentioned := false
			for _, mention := range msg.Mentions {
				if mention != nil && mention.Id != nil && ptr(mention.Id.OpenId) == botID && botID != "" {
					mentioned = true
				}
			}
			if !mentioned {
				return nil
			}
		}
		var content struct {
			Text string `json:"text"`
		}
		if json.Unmarshal([]byte(ptr(msg.Content)), &content) != nil {
			return nil
		}
		for _, mention := range msg.Mentions {
			if mention != nil {
				content.Text = strings.ReplaceAll(content.Text, ptr(mention.Key), "")
			}
		}
		in := Incoming{ID: ptr(msg.MessageId), ChannelID: c.ID, UserID: ptr(sender.TenantKey) + ":" + ptr(sender.SenderId.OpenId), ChatID: ptr(msg.ChatId), Private: private, Text: content.Text, Name: ptr(sender.SenderId.OpenId)}
		select {
		case queue <- in:
			return nil
		default:
			return errors.New("SUMA incoming queue is busy")
		}
	}).OnP2CardActionTrigger(func(_ context.Context, e *callback.CardActionTriggerEvent) (*callback.CardActionTriggerResponse, error) {
		if e == nil || e.Event == nil || e.Event.Operator == nil || e.Event.Action == nil || e.Event.Context == nil {
			return nil, nil
		}
		if ptr(e.Event.Operator.TenantKey) == "" || e.Event.Operator.OpenID == "" || e.Event.Context.OpenChatID == "" {
			return nil, nil
		}
		action, _ := e.Event.Action.Value["action"].(string)
		op, _ := e.Event.Action.Value["operation_id"].(string)
		if action == "approve" || action == "reject" {
			op, _ = e.Event.Action.Value["token"].(string)
		}
		key := ""
		if e.EventV2Base != nil && e.EventV2Base.Header != nil {
			key = e.EventV2Base.Header.EventID
		}
		in := Incoming{ID: key, ChannelID: c.ID, UserID: ptr(e.Event.Operator.TenantKey) + ":" + e.Event.Operator.OpenID, ChatID: e.Event.Context.OpenChatID, Action: action, OperationID: op, Name: e.Event.Operator.OpenID}
		select {
		case queue <- in:
			return &callback.CardActionTriggerResponse{Toast: &callback.Toast{Type: "info", Content: "请求已接收，请查看机器人回复 / Request received"}}, nil
		default:
			return nil, errors.New("SUMA incoming queue is busy")
		}
	})
	handler.Config.Logger = quietSDKLogger{}
	for ctx.Err() == nil {
		client := larkws.NewClient(c.Config.AppID, m.Token, larkws.WithEventHandler(handler), larkws.WithLogger(quietSDKLogger{}), larkws.WithLogLevel(larkcore.LogLevelError), larkws.WithHttpClient(outbound.Client(false)))
		client.SetOnReady(func() { s.chatError(c.ID, "") })
		client.SetOnError(func(error) {
			s.chatError(c.ID, "Feishu WebSocket disconnected; check application publication and event/callback subscriptions")
		})
		err := client.Start(ctx)
		client.Close()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			s.chatError(c.ID, "Feishu WebSocket connection failed; check App ID, App Secret, released version and long-connection configuration")
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
