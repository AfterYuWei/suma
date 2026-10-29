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
	Sender        Sender
	Now           func() time.Time
	OnError       func(error)
	FeishuConnect func(context.Context, Channel, Secrets, func(ConnectionStatus), func(Incoming) error)
}
type Service struct {
	db              *gorm.DB
	secrets         *secret.Store
	deps            Dependencies
	mu              sync.Mutex
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	expected        map[string]time.Time
	discoveryCancel map[string]context.CancelFunc
	discoveryDone   map[string]chan struct{}
	connections     map[string]connectionEntry
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
	return &Service{db: db, secrets: secrets, deps: deps, discoveryCancel: map[string]context.CancelFunc{}, discoveryDone: map[string]chan struct{}{}, connections: map[string]connectionEntry{}}
}
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
	err := s.db.WithContext(ctx).Select("*, destination_version").Order("created_at ASC").Find(&rows).Error
	out := make([]Channel, 0, len(rows))
	for _, r := range rows {
		out = append(out, decodeChannel(r))
	}
	return out, err
}
func (s *Service) Channel(ctx context.Context, id string) (Channel, error) {
	var r database.NotificationChannel
	err := s.db.WithContext(ctx).Select("*, destination_version").First(&r, "id = ?", id).Error
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
	oldConfig := decodeChannel(row).Config
	originalAppID := oldConfig.AppID
	originalEndpoint := previous.Endpoint
	in.Config.AppID = strings.TrimSpace(in.Config.AppID)
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
	case "feishu_app":
		if !strings.HasPrefix(in.Config.AppID, "cli_") || !validChatID(in.Config.AppID) || previous.Token == "" {
			return Channel{}, fmt.Errorf("%w: App ID and App Secret are required", ErrInvalid)
		}
		in.Config.ChatID = ""
		in.Config.AutoDiscover = false
		if id != "" && originalAppID != in.Config.AppID {
			in.Config.Targets = nil
		}
		if err := validateTargets(in.Config.Targets); err != nil {
			return Channel{}, err
		}
	case "webhook":
		in.Config.ChatID = ""
		if err := outbound.Validate(previous.Endpoint, in.Config.AllowPrivate); err != nil {
			return Channel{}, err
		}
		in.Config.AutoDiscover = false
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
			if same && (in.Provider == "feishu_app" || in.Config.AutoDiscover && v.Config.AutoDiscover) {
				return Channel{}, fmt.Errorf("%w: one channel per Feishu application; select multiple targets in that channel", ErrInvalid)
			}
		}
	}
	if in.Provider != "feishu_app" {
		in.Config.Targets = nil
	}
	if err := s.validateSubscription(ctx, row.ID, &in.Config.Subscription); err != nil {
		return Channel{}, err
	}
	for i := range in.Config.Targets {
		if sub := in.Config.Targets[i].SubscriptionOverride; sub != nil {
			if err := s.validateSubscription(ctx, row.ID, sub); err != nil {
				return Channel{}, err
			}
		}
	}
	identityChanged := id != "" && (originalAppID != in.Config.AppID || in.Provider == "telegram" && (originalToken != previous.Token || oldConfig.ChatID != in.Config.ChatID) || in.Provider == "webhook" && originalEndpoint != previous.Endpoint)
	if row.DestinationVersion == 0 || identityChanged {
		row.DestinationVersion++
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
		if id != "" && (!in.Enabled || identityChanged) {
			if err := tx.Model(&database.NotificationDelivery{}).Where("channel_id = ? AND status IN ?", id, []string{"pending", "retry"}).Updates(map[string]any{"status": "suppressed", "reason": "channel paused or destination changed"}).Error; err != nil {
				return err
			}
		}
		if in.Provider == "feishu_app" && id != "" {
			ids := targetIDs(in.Config.Targets)
			pending := tx.Model(&database.NotificationDelivery{}).Where("channel_id = ? AND status IN ?", id, []string{"pending", "retry"})
			if len(ids) > 0 {
				pending = pending.Where("chat_id NOT IN ?", ids)
			}
			if err := pending.Updates(map[string]any{"status": "suppressed", "reason": "recipient removed or application changed"}).Error; err != nil {
				return err
			}
			if err := detachFallbacks(tx, id, in.Config.Targets); err != nil {
				return err
			}
			if originalAppID != in.Config.AppID {
				if err := tx.Where("channel_id = ?", id).Delete(&database.NotificationChat{}).Error; err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		return Channel{}, err
	}
	if originalToken != previous.Token || originalAppID != in.Config.AppID || !needsConnection(decodeChannel(row)) {
		s.stopDiscoveryLocked(row.ID)
	}
	if s.cancel != nil && needsConnection(decodeChannel(row)) {
		s.startDiscoveryLocked(decodeChannel(row))
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
		if err := detachFallbacks(tx, id, nil); err != nil {
			return err
		}
		return tx.Where("channel_id = ?", id).Delete(&database.NotificationChat{}).Error
	})
	if err == nil {
		s.stopDiscoveryLocked(id)
	}
	return err
}
func (s *Service) Test(ctx context.Context, id string, recipients ...string) error {
	row, err := s.Channel(ctx, id)
	if err != nil {
		return err
	}
	if row.Provider == "feishu_app" {
		if len(recipients) != 1 || !hasTarget(row.Config.Targets, recipients[0]) {
			return fmt.Errorf("%w: select a configured recipient before testing", ErrInvalid)
		}
		row.Config.ChatID = recipients[0]
	}
	m, err := s.material(row)
	if err != nil {
		return err
	}
	text := "SUMA 通知测试"
	if row.Config.Language == "en-US" {
		text = "SUMA notification test"
	}
	channelLabel, descriptionLabel, description := "通知渠道", "状态说明", "测试消息已送达，通知渠道连接正常。"
	if row.Config.Language == "en-US" {
		channelLabel, descriptionLabel, description = "Channel", "Status details", "Test message delivered. The notification channel is connected."
	}
	message := Message{
		Text: text, Title: text, Language: row.Config.Language, Default: true,
		Cards: []MessageCard{{Title: text, Status: "success", Fields: []MessageField{
			{channelLabel, shortLine(row.Name, 80)}, {descriptionLabel, description},
		}}},
	}
	_, err = s.deps.Sender.Send(ctx, row, m, message)
	if err == nil {
		s.db.WithContext(ctx).Model(&database.NotificationChannel{}).Where("id = ?", row.ID).Updates(map[string]any{"last_error": "", "last_sent_at": s.deps.Now()})
		s.Emit(event.Event{Type: "notification.recovered", Severity: "info", ResourceType: "notification_channel", ResourceID: row.ID, Title: row.Name, Message: "Notification channel test delivery succeeded", DedupeKey: deliveryIncidentKey(row, row.Config.ChatID)})
	}
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
		return map[string]any{"credentials_valid": true}, nil
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
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 128 || len(c.Events) == 0 {
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
		var channels []database.NotificationChannel
		if err := tx.Find(&channels).Error; err != nil {
			return err
		}
		for _, raw := range channels {
			c := decodeChannel(raw)
			changed := removeRule(&c.Config.Subscription, id)
			for i := range c.Config.Targets {
				if sub := c.Config.Targets[i].SubscriptionOverride; sub != nil {
					changed = removeRule(sub, id) || changed
				}
			}
			if changed {
				if err := tx.Model(&raw).Updates(map[string]any{"config_json": encode(c.Config), "version": gorm.Expr("version + 1")}).Error; err != nil {
					return err
				}
			}
		}
		return nil
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
	// Derive identity before display fields are bounded; long image references
	// must not collapse into the same incident after title truncation.
	family := incidentFamily(e)
	e.Title = redact.Bounded(e.Title, 256)
	e.Message = redact.Bounded(e.Message, 4000)
	if e.DedupeKey == "" {
		e.DedupeKey = e.Type + "|" + e.NodeID + "|" + e.ResourceID
		if e.TaskID != "" {
			e.DedupeKey += "|task:" + e.TaskID
		} else if e.ReleaseID != nil {
			e.DedupeKey += fmt.Sprintf("|release:%d", *e.ReleaseID)
		} else if e.Type == "auth.login" {
			e.DedupeKey += "|" + e.ID
		}
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		lock := "event|" + e.DedupeKey
		if family != "" {
			lock = "incident|" + family
		}
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", lock).Error; err != nil {
			return err
		}
		row := database.NotificationEvent{ID: e.ID, Severity: e.Severity, NodeID: e.NodeID, DedupeKey: e.DedupeKey, CreatedAt: e.Time}
		skip, recovered, err := observeIncident(tx, &e, &row, family)
		if err != nil || skip {
			return err
		}
		if family == "" {
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
		}
		row.Type, row.DataJSON = e.Type, encode(e)
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		var rules []database.NotificationRule
		if err := tx.Find(&rules).Error; err != nil {
			return err
		}
		matched := map[string]MatchedRule{}
		ruleEvidence := map[string]MatchedRule{}
		recoveryMatches := map[string][]string{}
		for _, r := range rules {
			ruleEvidence[r.ID] = MatchedRule{ID: r.ID, Name: r.Name, Version: r.Version}
			config := decodeRule(r).Config
			if matches(tx, config, e) {
				matched[r.ID] = ruleEvidence[r.ID]
			}
			for _, original := range recovered {
				if matches(tx, config, original) {
					recoveryMatches[r.ID] = append(recoveryMatches[r.ID], original.ID)
				}
			}
		}
		var channels []database.NotificationChannel
		if err := tx.Where("enabled = ?", true).Order("id ASC").Find(&channels).Error; err != nil {
			return err
		}
		for _, raw := range channels {
			channel := decodeChannel(raw)
			if e.Type == "notification.failed" && e.ResourceID == channel.ID {
				continue
			}
			for _, target := range channelTargets(channel) {
				sub := channel.Config.Subscription
				if target.SubscriptionOverride != nil {
					sub = *target.SubscriptionOverride
				}
				evidence := []MatchedRule{}
				for _, id := range sub.RuleIDs {
					if r, ok := matched[id]; ok {
						evidence = append(evidence, r)
						continue
					}
					for _, eventID := range recoveryMatches[id] {
						received, err := receivedFault(tx, channel, target.ChatID, eventID)
						if err != nil {
							return err
						}
						if received {
							evidence = append(evidence, ruleEvidence[id])
							break
						}
					}
				}
				if len(evidence) == 0 {
					continue
				}
				snapshot := DeliverySnapshot{Policy: sub.Policy, Language: channel.Config.Language, PublicURL: channel.Config.PublicURL}
				if err := s.queue(tx, channel, target.ChatID, e, snapshot, evidence); err != nil {
					return err
				}
			}
		}
		return nil
	})
	s.mu.Unlock()
	return err
}
func matches(tx *gorm.DB, c RuleConfig, e event.Event) bool {
	if !contains(c.Events, e.Type) || len(c.Severities) > 0 && !contains(c.Severities, e.Severity) {
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
	return e.Severity == "critical" || e.Type == "cd.awaiting_approval"
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
		out.Items = append(out.Items, InboxEvent{Event: e, Read: read[r.ID], Occurrences: r.Occurrences, LastOccurredAt: r.LastOccurredAt, ResolvedAt: r.ResolvedAt})
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

// ReadAll marks the statement's current events read, including those outside
// the inbox's display limit. Existing reads retain their original timestamp.
func (s *Service) ReadAll(ctx context.Context, userID uint) error {
	return s.db.WithContext(ctx).Exec(`
		INSERT INTO notification_reads (user_id, event_id, read_at)
		SELECT ?, events.id, ? FROM notification_events AS events
		WHERE NOT EXISTS (
			SELECT 1 FROM notification_reads AS reads
			WHERE reads.user_id = ? AND reads.event_id = events.id
		)
		ON CONFLICT (user_id, event_id) DO NOTHING
	`, userID, s.deps.Now(), userID).Error
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
	if s.cancel != nil {
		for _, row := range channels {
			current, err := s.Channel(ctx, row.ID)
			if err == nil && needsConnection(current) {
				s.startDiscoveryLocked(current)
			}
		}
	}
	s.mu.Unlock()
}
func (s *Service) Stop() {
	s.mu.Lock()
	cancel := s.cancel
	s.cancel = nil
	for id := range s.discoveryCancel {
		s.stopDiscoveryLocked(id)
	}
	s.discoveryCancel = map[string]context.CancelFunc{}
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.wg.Wait()
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
	if !destinationValid(channel, row) {
		s.finishDelivery(row.ID, "suppressed", "recipient removed or destination changed", now, "")
		return
	}
	if channel.Provider != "webhook" {
		channel.Config.ChatID = row.ChatID
	}
	var snapshot DeliverySnapshot
	if err = json.Unmarshal([]byte(row.SnapshotJSON), &snapshot); err != nil {
		s.error(err)
		return
	}
	events, links, err := s.deliveryEvents(ctx, row.ID)
	if err != nil {
		s.error(err)
		return
	}
	m, err := s.material(channel)
	var remoteID string
	if err == nil {
		remoteID, err = s.deps.Sender.Send(ctx, channel, m, s.render(snapshot, events))
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
	if status == "failed" {
		s.error(s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			result := tx.Model(&database.NotificationDelivery{}).Where("id = ? AND status = ?", row.ID, "sending").Updates(map[string]any{"status": status, "reason": reason, "due_at": due, "lease_until": nil, "provider_message_id": remoteID})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 1 && snapshot.Policy.FallbackID != "" && row.Source != "fallback" {
				return s.queueFallback(tx, row, snapshot, links)
			}
			return nil
		}))
	} else {
		s.finishDelivery(row.ID, status, reason, due, remoteID)
	}
	if err == nil {
		s.db.Model(&database.NotificationChannel{}).Where("id = ?", channel.ID).Updates(map[string]any{"last_error": "", "last_sent_at": now})
		s.Emit(event.Event{Type: "notification.recovered", Severity: "info", ResourceType: "notification_channel", ResourceID: channel.ID, Title: channel.Name, Message: "Notification channel delivery recovered", DedupeKey: deliveryIncidentKey(channel, row.ChatID), Time: now})
	} else {
		s.db.Model(&database.NotificationChannel{}).Where("id = ?", channel.ID).Update("last_error", reason)
	}
	if status == "failed" {
		if len(events) > 0 && !strings.HasPrefix(events[0].Type, "notification.") {
			s.Emit(event.Event{Type: "notification.failed", Severity: "error", ResourceType: "notification_channel", ResourceID: channel.ID, Title: channel.Name, Message: channel.Name + ": " + reason, DedupeKey: deliveryIncidentKey(channel, row.ChatID)})
		}
	}
}
func (s *Service) finishDelivery(id, status, reason string, due time.Time, remote string) {
	s.error(s.db.Model(&database.NotificationDelivery{}).Where("id = ? AND status = ?", id, "sending").Updates(map[string]any{"status": status, "reason": reason, "due_at": due, "lease_until": nil, "provider_message_id": remote}).Error)
}
func (s *Service) render(snapshot DeliverySnapshot, rows []database.NotificationEvent) Message {
	return renderMessage(snapshot, rows)
}

func hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
