package notification

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/event"
	"github.com/suma/suma/server/internal/outbound"
	"github.com/suma/suma/server/internal/redact"
	"github.com/suma/suma/server/internal/secret"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Sender interface {
	Send(context.Context, Channel, Secrets, Message) (string, error)
}
type Dependencies struct {
	Audit   *audit.Service
	Sender  Sender
	Now     func() time.Time
	OnError func(error)
}
type Service struct {
	db         *gorm.DB
	secrets    *secret.Store
	deps       Dependencies
	mu         sync.Mutex
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	chat       ChatHandler
	onEvent    func(event.Event)
	expected   map[string]time.Time
	chatCancel map[string]context.CancelFunc
	queryAt    map[string]time.Time
}

func NewService(db *gorm.DB, secrets *secret.Store, deps Dependencies) *Service {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	clock := deps.Now
	deps.Now = func() time.Time { return clock().UTC() }
	db = db.Session(&gorm.Session{NowFunc: deps.Now})
	if deps.Sender == nil {
		deps.Sender = NewAdapter()
	}
	if deps.Audit == nil {
		deps.Audit = audit.NewService(db)
	}
	return &Service{db: db, secrets: secrets, deps: deps, chatCancel: map[string]context.CancelFunc{}, queryAt: map[string]time.Time{}}
}
func (s *Service) SetChatHandler(h ChatHandler)        { s.mu.Lock(); s.chat = h; s.mu.Unlock() }
func (s *Service) SetEventHandler(h func(event.Event)) { s.mu.Lock(); s.onEvent = h; s.mu.Unlock() }
func (s *Service) error(err error) {
	if err != nil && s.deps.OnError != nil {
		s.deps.OnError(errors.New(redact.Bounded(err.Error(), 512)))
	}
}
func decodeChannel(row database.NotificationChannel) Channel {
	v := Channel{NotificationChannel: row, HasSecrets: len(row.SecretCiphertext) > 0}
	_ = json.Unmarshal([]byte(row.ConfigJSON), &v.Config)
	return v
}
func decodeRule(row database.NotificationRule) Rule {
	v := Rule{NotificationRule: row}
	_ = json.Unmarshal([]byte(row.ConfigJSON), &v.Config)
	return v
}
func (s *Service) Channels(ctx context.Context) ([]Channel, error) {
	var rows []database.NotificationChannel
	err := s.db.WithContext(ctx).Order("created_at ASC").Find(&rows).Error
	out := make([]Channel, 0, len(rows))
	for _, r := range rows {
		out = append(out, decodeChannel(r))
	}
	return out, err
}
func (s *Service) Channel(ctx context.Context, id string) (Channel, error) {
	var r database.NotificationChannel
	err := s.db.WithContext(ctx).First(&r, "id = ?", id).Error
	return decodeChannel(r), err
}
func (s *Service) material(row Channel) (Secrets, error) {
	var v Secrets
	raw, err := s.secrets.Decrypt(row.SecretCiphertext)
	if err != nil {
		return v, err
	}
	err = json.Unmarshal([]byte(raw), &v)
	return v, err
}
func (s *Service) SaveChannel(ctx context.Context, id string, in ChannelInput) (Channel, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 128 {
		return Channel{}, ErrInvalid
	}
	if in.Config.Language == "" {
		in.Config.Language = "zh-CN"
	}
	if in.Config.Language != "zh-CN" && in.Config.Language != "en-US" {
		return Channel{}, ErrInvalid
	}
	if _, err := time.LoadLocation(in.Config.Timezone); err != nil || in.Config.Timezone == "Local" {
		return Channel{}, fmt.Errorf("%w: select an IANA timezone", ErrInvalid)
	}
	if in.Config.PublicURL != "" {
		u, err := url.Parse(in.Config.PublicURL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return Channel{}, fmt.Errorf("%w: public links require an HTTPS origin", ErrInvalid)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var row database.NotificationChannel
	if id == "" {
		row.ID = ID()
	} else {
		if err := s.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
			return Channel{}, err
		}
		if row.Version != in.Version {
			return Channel{}, ErrConflict
		}
		if row.Provider != in.Provider {
			return Channel{}, fmt.Errorf("%w: provider cannot change", ErrInvalid)
		}
	}
	var previous Secrets
	if len(row.SecretCiphertext) > 0 {
		raw, err := s.secrets.Decrypt(row.SecretCiphertext)
		if err != nil {
			return Channel{}, err
		}
		if err = json.Unmarshal([]byte(raw), &previous); err != nil {
			return Channel{}, err
		}
	}
	originalToken := previous.Token
	originalAppID := decodeChannel(row).Config.AppID
	if in.Secrets != nil {
		if in.Secrets.Token != "" {
			previous.Token = strings.TrimSpace(in.Secrets.Token)
		}
		if in.Secrets.SigningKey != "" {
			previous.SigningKey = in.Secrets.SigningKey
		}
		if in.Secrets.Authorization != "" {
			previous.Authorization = in.Secrets.Authorization
		}
	}
	if in.Config.Endpoint != "" {
		previous.Endpoint = strings.TrimSpace(in.Config.Endpoint)
	}
	switch in.Provider {
	case "telegram":
		if in.Config.Endpoint != "" || !strings.Contains(previous.Token, ":") {
			return Channel{}, fmt.Errorf("%w: Telegram Bot Token is required", ErrInvalid)
		}
	case "feishu_webhook":
		if err := outbound.Validate(previous.Endpoint, false); err != nil {
			return Channel{}, err
		}
		u, _ := url.Parse(previous.Endpoint)
		if u.Host != "open.feishu.cn" || !strings.HasPrefix(u.Path, "/open-apis/bot/v2/hook/") {
			return Channel{}, ErrInvalid
		}
		in.Config.Interactive = false
	case "feishu_app":
		if !strings.HasPrefix(in.Config.AppID, "cli_") || previous.Token == "" {
			return Channel{}, fmt.Errorf("%w: App ID and App Secret are required", ErrInvalid)
		}
	case "webhook":
		if err := outbound.Validate(previous.Endpoint, in.Config.AllowPrivate); err != nil {
			return Channel{}, err
		}
		in.Config.Interactive = false
	default:
		return Channel{}, ErrInvalid
	}
	if in.Provider == "telegram" || in.Provider == "feishu_app" {
		var peers []database.NotificationChannel
		if err := s.db.WithContext(ctx).Where("provider = ? AND id <> ?", in.Provider, row.ID).Find(&peers).Error; err != nil {
			return Channel{}, err
		}
		for _, peer := range peers {
			v := decodeChannel(peer)
			m, err := s.material(v)
			if err != nil {
				return Channel{}, err
			}
			same := in.Provider == "feishu_app" && v.Config.AppID == in.Config.AppID || in.Provider == "telegram" && m.Token == previous.Token
			if same && in.Config.Interactive && v.Config.Interactive {
				return Channel{}, fmt.Errorf("%w: one interactive channel per bot; use multiple rules for destinations", ErrInvalid)
			}
		}
	}
	// Endpoint paths can contain bearer credentials, so all destination URLs are encrypted.
	in.Config.Endpoint = ""
	cipher, err := s.secrets.Encrypt(encode(previous))
	if err != nil {
		return Channel{}, err
	}
	row.Name = in.Name
	row.Provider = in.Provider
	row.Enabled = in.Enabled
	row.Version++
	row.ConfigJSON = encode(in.Config)
	row.SecretCiphertext = cipher
	if err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		if id != "" && (originalToken != previous.Token || originalAppID != in.Config.AppID) {
			if err := tx.Model(&database.NotificationBinding{}).Where("channel_id = ?", id).Update("status", "revoked").Error; err != nil {
				return err
			}
			return tx.Where("channel_id = ?", id).Delete(&database.NotificationAction{}).Error
		}
		return nil
	}); err != nil {
		return Channel{}, err
	}
	if cancel := s.chatCancel[row.ID]; cancel != nil {
		cancel()
		delete(s.chatCancel, row.ID)
	}
	if s.ctx != nil && row.Enabled && in.Config.Interactive {
		s.startChatLocked(decodeChannel(row))
	}
	return decodeChannel(row), nil
}
func (s *Service) DeleteChannel(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&database.NotificationChannel{}, "id = ?", id).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.NotificationDelivery{}).Where("channel_id = ? AND status IN ?", id, []string{"pending", "retry"}).Updates(map[string]any{"status": "suppressed", "reason": "channel deleted"}).Error; err != nil {
			return err
		}
		return tx.Model(&database.NotificationBinding{}).Where("channel_id = ?", id).Update("status", "revoked").Error
	})
	if err == nil {
		if cancel := s.chatCancel[id]; cancel != nil {
			cancel()
			delete(s.chatCancel, id)
		}
	}
	return err
}
func (s *Service) Test(ctx context.Context, id string) error {
	row, err := s.Channel(ctx, id)
	if err != nil {
		return err
	}
	m, err := s.material(row)
	if err != nil {
		return err
	}
	_, err = s.deps.Sender.Send(ctx, row, m, Message{Text: "SUMA · 通知渠道测试 / Notification channel test"})
	return err
}
func (s *Service) Check(ctx context.Context, id string) (map[string]any, error) {
	row, err := s.Channel(ctx, id)
	if err != nil {
		return nil, err
	}
	m, err := s.material(row)
	if err != nil {
		return nil, err
	}
	a, ok := s.deps.Sender.(*Adapter)
	if !ok {
		return map[string]any{"connected": true}, nil
	}
	return a.Check(ctx, row, m)
}
func (s *Service) Rules(ctx context.Context) ([]Rule, error) {
	var rows []database.NotificationRule
	err := s.db.WithContext(ctx).Order("created_at ASC").Find(&rows).Error
	out := make([]Rule, 0, len(rows))
	for _, r := range rows {
		out = append(out, decodeRule(r))
	}
	return out, err
}
func (s *Service) SaveRule(ctx context.Context, id string, in RuleInput) (Rule, error) {
	c := in.Config
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 128 || len(c.Events) == 0 || len(c.ChannelIDs) == 0 || len(c.ChannelIDs) > 20 {
		return Rule{}, ErrInvalid
	}
	for _, typ := range c.Events {
		found := false
		for _, item := range Catalog {
			if item.Type == typ {
				found = true
			}
		}
		if !found {
			return Rule{}, ErrInvalid
		}
	}
	for _, severity := range c.Severities {
		if !contains([]string{"info", "warning", "error", "critical"}, severity) {
			return Rule{}, ErrInvalid
		}
	}
	if c.Mode != "immediate" && c.Mode != "merge" && c.Mode != "daily" {
		return Rule{}, ErrInvalid
	}
	if _, err := time.LoadLocation(c.Timezone); err != nil || c.Timezone == "Local" || c.DigestHour < 0 || c.DigestHour > 23 {
		return Rule{}, ErrInvalid
	}
	if (c.QuietStart == "") != (c.QuietEnd == "") || c.QuietStart != "" && c.QuietStart == c.QuietEnd {
		return Rule{}, ErrInvalid
	}
	for _, v := range []string{c.QuietStart, c.QuietEnd} {
		if v != "" {
			if _, err := time.Parse("15:04", v); err != nil {
				return Rule{}, ErrInvalid
			}
		}
	}
	if len(c.Template) > 4000 {
		return Rule{}, ErrInvalid
	}
	clean := c.Template
	for _, field := range []string{"title", "message", "node", "resource", "time", "url"} {
		clean = strings.ReplaceAll(clean, "{{"+field+"}}", "")
	}
	if strings.Contains(clean, "{{") || strings.Contains(clean, "}}") {
		return Rule{}, fmt.Errorf("%w: unsupported template field", ErrInvalid)
	}
	for _, channel := range append(append([]string{}, c.ChannelIDs...), c.FallbackID) {
		if channel != "" {
			if _, err := s.Channel(ctx, channel); err != nil {
				return Rule{}, ErrInvalid
			}
		}
	}
	for _, node := range c.NodeIDs {
		var n int64
		if err := s.db.WithContext(ctx).Model(&database.Node{}).Where("id = ?", node).Count(&n).Error; err != nil {
			return Rule{}, err
		}
		if n == 0 {
			return Rule{}, ErrInvalid
		}
	}
	for _, group := range c.GroupIDs {
		var n int64
		if err := s.db.WithContext(ctx).Model(&database.NodeGroup{}).Where("id = ?", group).Count(&n).Error; err != nil {
			return Rule{}, err
		}
		if n == 0 {
			return Rule{}, ErrInvalid
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var row database.NotificationRule
	if id == "" {
		row.ID = ID()
	} else {
		if err := s.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
			return Rule{}, err
		}
		if row.Version != in.Version {
			return Rule{}, ErrConflict
		}
	}
	row.Name = in.Name
	row.Enabled = in.Enabled
	row.Version++
	row.ConfigJSON = encode(c)
	err := s.db.WithContext(ctx).Save(&row).Error
	return decodeRule(row), err
}
func (s *Service) DeleteRule(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&database.NotificationRule{}, "id = ?", id).Error; err != nil {
			return err
		}
		return tx.Model(&database.NotificationDelivery{}).Where("rule_id = ? AND status IN ?", id, []string{"pending", "retry"}).Updates(map[string]any{"status": "suppressed", "reason": "rule deleted"}).Error
	})
}
func (s *Service) Emit(e event.Event) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s.error(s.Ingest(ctx, e))
}
func (s *Service) Ingest(ctx context.Context, e event.Event) error {
	s.mu.Lock()
	if e.Time.IsZero() {
		e.Time = s.deps.Now().UTC()
	}
	e.Time = e.Time.UTC()
	if e.ID == "" {
		e.ID = ID()
	}
	if e.Severity == "" {
		e.Severity = "info"
	}
	if e.Scope == "" {
		if e.NodeID != "" {
			e.Scope = "node"
		} else {
			e.Scope = "control_plane"
		}
	}
	e.Title = redact.Bounded(e.Title, 256)
	e.Message = redact.Bounded(e.Message, 4000)
	if e.DedupeKey == "" {
		e.DedupeKey = e.Type + "|" + e.NodeID + "|" + e.ResourceID
		if e.OperationID != "" {
			e.DedupeKey += "|" + e.OperationID
		} else if e.RunID != "" {
			e.DedupeKey += "|" + e.RunID
		}
		if e.Type == "auth.login" {
			e.DedupeKey += "|" + e.ID
		}
	}
	inserted := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		q := tx.Model(&database.NotificationEvent{}).Where("dedupe_key = ?", e.DedupeKey)
		if e.Type != "image.available" && e.Type != "image.recreate_required" && e.Type != "auth.new_ip" {
			window := 10 * time.Minute
			if e.Type == "tls.expiring" {
				window = 24 * time.Hour
			}
			q = q.Where("created_at > ?", e.Time.Add(-window))
		}
		if err := q.Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return nil
		}
		row := database.NotificationEvent{ID: e.ID, Type: e.Type, Severity: e.Severity, NodeID: e.NodeID, DedupeKey: e.DedupeKey, DataJSON: encode(e), CreatedAt: e.Time}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		var rules []database.NotificationRule
		if err := tx.Where("enabled = ?", true).Find(&rules).Error; err != nil {
			return err
		}
		for _, r := range rules {
			rule := decodeRule(r)
			if matches(tx, rule.Config, e) {
				for _, ch := range rule.Config.ChannelIDs {
					if err := s.queue(tx, rule, ch, e); err != nil {
						return err
					}
				}
			}
		}
		inserted = true
		return nil
	})
	h := s.onEvent
	s.mu.Unlock()
	if err == nil && inserted && h != nil {
		h(e)
	}
	return err
}
func matches(tx *gorm.DB, c RuleConfig, e event.Event) bool {
	if !contains(c.Events, e.Type) || len(c.Severities) > 0 && !contains(c.Severities, e.Severity) {
		return false
	}
	if !c.Recovery && strings.HasSuffix(e.Type, ".recovered") {
		return false
	}
	if len(c.NodeIDs) > 0 || len(c.GroupIDs) > 0 {
		match := contains(c.NodeIDs, e.NodeID)
		if !match && len(c.GroupIDs) > 0 {
			var n int64
			tx.Model(&database.NodeGroupNode{}).Where("node_id = ? AND group_id IN ?", e.NodeID, c.GroupIDs).Count(&n)
			match = n > 0
		}
		if !match {
			return false
		}
	}
	return len(c.Projects) == 0 || contains(c.Projects, e.Project)
}
func urgent(e event.Event) bool {
	return e.Severity == "critical" || e.Type == "ai.awaiting_approval" || e.Type == "cd.awaiting_approval"
}
func (s *Service) queue(tx *gorm.DB, rule Rule, channelID string, e event.Event) error {
	now := s.deps.Now().UTC()
	due := now
	status, reason := "pending", ""
	c := rule.Config
	if !urgent(e) && c.MutedUntil != nil && now.Before(*c.MutedUntil) {
		status, reason = "suppressed", "temporarily muted"
	}
	zone, _ := time.LoadLocation(c.Timezone)
	if zone == nil {
		zone = time.UTC
	}
	local := now.In(zone)
	if !urgent(e) && c.QuietStart != "" {
		clock := local.Format("15:04")
		quiet := c.QuietStart < c.QuietEnd && clock >= c.QuietStart && clock < c.QuietEnd || c.QuietStart > c.QuietEnd && (clock >= c.QuietStart || clock < c.QuietEnd)
		if quiet {
			end, _ := time.Parse("15:04", c.QuietEnd)
			due = time.Date(local.Year(), local.Month(), local.Day(), end.Hour(), end.Minute(), 0, 0, zone)
			if !due.After(now) {
				due = due.AddDate(0, 0, 1)
			}
			reason = "quiet hours"
		}
	}
	if !urgent(e) && c.Mode == "merge" {
		due = due.Truncate(5 * time.Minute).Add(5 * time.Minute)
	}
	if !urgent(e) && c.Mode == "daily" {
		candidate := time.Date(local.Year(), local.Month(), local.Day(), c.DigestHour, 0, 0, 0, zone)
		if !candidate.After(due) {
			candidate = candidate.AddDate(0, 0, 1)
		}
		due = candidate
	}
	due = due.UTC()
	key := rule.ID + "|" + channelID + "|" + e.ID
	if !urgent(e) && c.Mode != "immediate" && status == "pending" {
		key = rule.ID + "|" + channelID + "|" + due.UTC().Format(time.RFC3339)
	}
	var row database.NotificationDelivery
	if err := tx.Where("batch_key = ? AND status = ?", key, "pending").First(&row).Error; err == nil {
		var ids []string
		_ = json.Unmarshal([]byte(row.EventIDsJSON), &ids)
		if !contains(ids, e.ID) {
			ids = append(ids, e.ID)
		}
		if len(ids) <= 200 {
			return tx.Model(&row).Update("event_ids_json", encode(ids)).Error
		}
		// Keep digest envelopes bounded; overflow remains in the inbox and gets
		// its own delivery rather than growing one unbounded payload.
		key += "|" + e.ID
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return tx.Create(&database.NotificationDelivery{ID: ID(), ChannelID: channelID, RuleID: rule.ID, BatchKey: key, EventIDsJSON: encode([]string{e.ID}), Status: status, Reason: reason, DueAt: due}).Error
}
func (s *Service) Inbox(ctx context.Context, userID uint) (Inbox, error) {
	var rows []database.NotificationEvent
	err := s.db.WithContext(ctx).Order("created_at DESC").Limit(200).Find(&rows).Error
	out := Inbox{Items: []InboxEvent{}}
	if err != nil {
		return out, err
	}
	var reads []database.NotificationRead
	if err = s.db.WithContext(ctx).Where("user_id = ?", userID).Find(&reads).Error; err != nil {
		return out, err
	}
	read := map[string]bool{}
	for _, r := range reads {
		read[r.EventID] = true
	}
	for _, r := range rows {
		var e event.Event
		_ = json.Unmarshal([]byte(r.DataJSON), &e)
		out.Items = append(out.Items, InboxEvent{Event: e, Read: read[r.ID]})
	}
	err = s.db.WithContext(ctx).Model(&database.NotificationEvent{}).Where("id NOT IN (?)", s.db.Model(&database.NotificationRead{}).Select("event_id").Where("user_id = ?", userID)).Count(&out.Unread).Error
	return out, err
}
func (s *Service) Read(ctx context.Context, userID uint, id string) error {
	var row database.NotificationEvent
	if err := s.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		return err
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&database.NotificationRead{UserID: userID, EventID: id, ReadAt: s.deps.Now()}).Error
}
func (s *Service) Deliveries(ctx context.Context) ([]database.NotificationDelivery, error) {
	rows := []database.NotificationDelivery{}
	err := s.db.WithContext(ctx).Order("created_at DESC").Limit(200).Find(&rows).Error
	return rows, err
}
func (s *Service) Resend(ctx context.Context, id string) error {
	var r database.NotificationDelivery
	if err := s.db.WithContext(ctx).First(&r, "id = ?", id).Error; err != nil {
		return err
	}
	r.ID = ID()
	r.Status = "pending"
	r.Attempts = 0
	r.Reason = "manual resend"
	r.DueAt = s.deps.Now()
	r.LeaseUntil = nil
	r.CreatedAt = time.Time{}
	r.UpdatedAt = time.Time{}
	return s.db.WithContext(ctx).Create(&r).Error
}
func (s *Service) Start() {
	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		return
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	ctx := s.ctx
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			s.error(s.Tick(ctx))
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	channels, err := s.Channels(ctx)
	s.error(err)
	s.mu.Lock()
	for _, row := range channels {
		if row.Enabled && row.Config.Interactive {
			s.startChatLocked(row)
		}
	}
	s.mu.Unlock()
}
func (s *Service) Stop() {
	s.mu.Lock()
	cancel := s.cancel
	s.cancel = nil
	for _, stop := range s.chatCancel {
		stop()
	}
	s.chatCancel = map[string]context.CancelFunc{}
	s.mu.Unlock()
	if cancel != nil {
		cancel()
		s.wg.Wait()
	}
}
func (s *Service) Done() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx != nil {
		return s.ctx.Done()
	}
	done := make(chan struct{})
	close(done)
	return done
}
func (s *Service) Tick(ctx context.Context) error {
	var rows []database.NotificationDelivery
	now := s.deps.Now().UTC()
	if err := s.db.WithContext(ctx).Where("(status IN ? AND due_at <= ?) OR (status = ? AND lease_until < ?)", []string{"pending", "retry"}, now, "sending", now).Order("due_at ASC").Limit(20).Find(&rows).Error; err != nil {
		return err
	}
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, row := range rows {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(r database.NotificationDelivery) { defer wg.Done(); defer func() { <-sem }(); s.deliver(ctx, r) }(row)
	}
	wg.Wait()
	return ctx.Err()
}
func (s *Service) deliver(ctx context.Context, row database.NotificationDelivery) {
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	now := s.deps.Now().UTC()
	lease := now.Add(time.Minute)
	// Claim and read the event list together: a digest may have grown since Tick selected it.
	s.mu.Lock()
	result := s.db.WithContext(ctx).Model(&database.NotificationDelivery{}).Where("id = ? AND (status IN ? OR (status = ? AND lease_until < ?))", row.ID, []string{"pending", "retry"}, "sending", now).Updates(map[string]any{"status": "sending", "lease_until": lease, "attempts": gorm.Expr("attempts + 1")})
	if result.Error == nil && result.RowsAffected == 1 {
		result.Error = s.db.WithContext(ctx).First(&row, "id = ?", row.ID).Error
	}
	s.mu.Unlock()
	if result.Error != nil || result.RowsAffected != 1 {
		return
	}
	channel, err := s.Channel(ctx, row.ChannelID)
	if err != nil || !channel.Enabled {
		s.finishDelivery(row.ID, "suppressed", "channel disabled or deleted", now, "")
		return
	}
	var config RuleConfig
	if row.RuleID != "" {
		var raw database.NotificationRule
		if s.db.WithContext(ctx).First(&raw, "id = ?", row.RuleID).Error != nil || !raw.Enabled {
			s.finishDelivery(row.ID, "suppressed", "rule disabled or deleted", now, "")
			return
		}
		config = decodeRule(raw).Config
	}
	var ids []string
	_ = json.Unmarshal([]byte(row.EventIDsJSON), &ids)
	var events []database.NotificationEvent
	if err = s.db.WithContext(ctx).Where("id IN ?", ids).Order("created_at ASC").Find(&events).Error; err != nil {
		s.error(err)
		return
	}
	m, err := s.material(channel)
	var remoteID string
	if err == nil {
		remoteID, err = s.deps.Sender.Send(ctx, channel, m, s.render(channel, config, events))
	}
	status, reason := "sent", ""
	due := now
	if err != nil {
		status = "retry"
		reason = redact.Bounded(err.Error(), 512)
		due = now.Add(time.Duration(1<<min(row.Attempts, 8)) * time.Second)
		var limited *RateLimitError
		if errors.As(err, &limited) {
			due = now.Add(min(limited.After, time.Hour))
		}
		if row.Attempts >= 5 {
			status = "failed"
		}
	}
	s.finishDelivery(row.ID, status, reason, due, remoteID)
	if err == nil {
		s.db.Model(&database.NotificationChannel{}).Where("id = ?", channel.ID).Updates(map[string]any{"last_error": "", "last_sent_at": now})
	} else {
		s.db.Model(&database.NotificationChannel{}).Where("id = ?", channel.ID).Update("last_error", reason)
	}
	if status == "failed" {
		if config.FallbackID != "" && config.FallbackID != row.ChannelID {
			fallback := row
			fallback.ID = ID()
			fallback.ChannelID = config.FallbackID
			fallback.RuleID = ""
			fallback.Status = "pending"
			fallback.Attempts = 0
			fallback.DueAt = now
			fallback.LeaseUntil = nil
			fallback.CreatedAt = time.Time{}
			fallback.UpdatedAt = time.Time{}
			s.error(s.db.Create(&fallback).Error)
		}
		if len(events) > 0 && events[0].Type != "notification.failed" {
			s.Emit(event.Event{Type: "notification.failed", Severity: "error", ResourceType: "notification_channel", ResourceID: channel.ID, Title: "通知渠道发送失败 / Channel delivery failed", Message: channel.Name + ": " + reason, DedupeKey: "delivery-failed|" + channel.ID})
		}
	}
}
func (s *Service) finishDelivery(id, status, reason string, due time.Time, remote string) {
	s.error(s.db.Model(&database.NotificationDelivery{}).Where("id = ? AND status = ?", id, "sending").Updates(map[string]any{"status": status, "reason": reason, "due_at": due, "lease_until": nil, "provider_message_id": remote}).Error)
}
func (s *Service) render(channel Channel, c RuleConfig, rows []database.NotificationEvent) Message {
	var text strings.Builder
	zone, _ := time.LoadLocation(channel.Config.Timezone)
	if zone == nil {
		zone = time.UTC
	}
	message := Message{}
	for _, row := range rows {
		var e event.Event
		_ = json.Unmarshal([]byte(row.DataJSON), &e)
		message.Events = append(message.Events, e)
		path := "/notifications#" + e.ID
		if e.OperationID != "" {
			path = "/ai-operations#" + e.OperationID
		}
		link := strings.TrimRight(channel.Config.PublicURL, "/") + path
		title := e.Title
		if c.Template == "" {
			for _, entry := range Catalog {
				if entry.Type == e.Type {
					if channel.Config.Language == "en-US" {
						title = entry.TitleEN
					} else {
						title = entry.Title
					}
				}
			}
		}
		body := c.Template
		if body == "" {
			body = "[" + e.Severity + "] {{title}}\n{{time}} · {{node}} · {{resource}}\n{{message}}\n{{url}}"
		}
		for key, value := range map[string]string{"title": title, "message": e.Message, "node": e.NodeName, "resource": e.ResourceID, "time": e.Time.In(zone).Format("2006-01-02 15:04:05 MST"), "url": link} {
			body = strings.ReplaceAll(body, "{{"+key+"}}", value)
		}
		text.WriteString(body + "\n\n")
		if len(rows) == 1 && e.Type == "ai.awaiting_approval" {
			message.ApproveID = e.OperationID
			message.OperationID = e.OperationID
		}
		if channel.Config.PublicURL != "" {
			message.URL = link
		}
	}
	message.Text = redact.Bounded(text.String(), 12000)
	return message
}
func hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
