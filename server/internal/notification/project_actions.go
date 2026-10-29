package notification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/imageupdate"
	"gorm.io/gorm"
)

type ProjectUpdateService interface {
	Candidate(context.Context, string, string, string) (*imageupdate.Candidate, error)
	Publish(context.Context, string, string, string, imageupdate.Actor, ...bool) (database.Task, error)
}

func decorateProjectCandidate(message *Message, view *imageupdate.Candidate) {
	label := "服务与目标版本"
	if message.Language == "en-US" {
		label = "Services and target versions"
	}
	for _, service := range view.Services {
		value := service.Name + " · " + service.Reference + " → " + service.ManifestDigest
		if message.Default && len(message.Cards) > 0 {
			message.Cards[0].Fields = append(message.Cards[0].Fields, MessageField{label, value})
		} else {
			message.Text += "\n" + label + ": " + value
		}
	}
	if view.RequiresDockerSocket {
		warning := "挂载 Docker Socket：发布将授予完整 Engine 控制权限。"
		if message.Language == "en-US" {
			warning = "Docker socket mounted: publishing authorizes full Engine control."
		}
		message.Text += "\n" + warning
		if len(message.Cards) > 0 {
			message.Cards[0].Fields = append(message.Cards[0].Fields, MessageField{Label: "Docker Socket", Value: warning})
		}
	}
}

var telegramApprover = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)
var feishuApprover = regexp.MustCompile(`^[a-zA-Z0-9_-]+:ou_[a-zA-Z0-9_-]+$`)

func validateApprovers(provider string, ids []string) error {
	if len(ids) > 100 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] || len(id) > 200 || provider == "telegram" && !telegramApprover.MatchString(id) || provider == "feishu_app" && !feishuApprover.MatchString(id) || provider == "webhook" {
			return fmt.Errorf("%w: invalid or duplicate publisher ID", ErrInvalid)
		}
		seen[id] = true
	}
	return nil
}
func (s *Service) SetProjectUpdates(service ProjectUpdateService) { s.deps.ProjectUpdates = service }
func (s *Service) prepareProjectAction(ctx context.Context, c Channel, delivery database.NotificationDelivery, events []database.NotificationEvent, message *Message) error {
	if s.deps.ProjectUpdates == nil || c.Provider == "webhook" {
		return nil
	}
	for _, row := range events {
		var e struct {
			CandidateID string `json:"candidate_id"`
		}
		if err := json.Unmarshal([]byte(row.DataJSON), &e); err != nil {
			return err
		}
		if e.CandidateID == "" {
			continue
		}
		var candidate database.ProjectImageCandidate
		if err := s.db.WithContext(ctx).First(&candidate, "id = ?", e.CandidateID).Error; err != nil {
			return err
		}
		view, err := s.deps.ProjectUpdates.Candidate(ctx, candidate.NodeID, candidate.Project, candidate.ID)
		if err != nil {
			return err
		}
		decorateProjectCandidate(message, view)
		if candidate.Status != "pending" || len(c.Config.ApproverIDs) == 0 {
			continue
		}
		token := ID()
		message.PublishToken = token
		action := database.ProjectImageAction{TokenHash: hash(token), CandidateID: candidate.ID, DeliveryID: delivery.ID, ChannelID: c.ID, ChatID: delivery.ChatID, DestinationVersion: delivery.DestinationVersion, State: "pending", CreatedAt: s.deps.Now()}
		return s.db.WithContext(ctx).Create(&action).Error
	}
	return nil
}

type ProjectActionInput struct{ ChannelID, ChatID, UserID, Name, Token, Generation string }

func (s *Service) HandleProjectAction(ctx context.Context, in ProjectActionInput) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if s.deps.ProjectUpdates == nil || len(in.Token) != 32 || in.UserID == "" {
		return "", ErrInvalid
	}
	s.mu.Lock()
	stale := in.Generation != "" && s.connections[in.ChannelID].generation != in.Generation
	s.mu.Unlock()
	if stale {
		return "", context.Canceled
	}
	c, err := s.Channel(ctx, in.ChannelID)
	if err != nil {
		return "", err
	}
	allowed := false
	for _, id := range c.Config.ApproverIDs {
		allowed = allowed || id == in.UserID
	}
	if !allowed {
		return "", errors.New("This user is not an authorized publisher")
	}
	var action database.ProjectImageAction
	if err := s.db.WithContext(ctx).First(&action, "token_hash = ? AND channel_id = ? AND chat_id = ?", hash(in.Token), c.ID, in.ChatID).Error; err != nil {
		return "", ErrInvalid
	}
	if !destinationValid(c, database.NotificationDelivery{ChatID: in.ChatID, DestinationVersion: action.DestinationVersion}) {
		return "", ErrInvalid
	}
	var candidate database.ProjectImageCandidate
	if err := s.db.WithContext(ctx).First(&candidate, "id = ?", action.CandidateID).Error; err != nil {
		return "", err
	}
	row, err := s.deps.ProjectUpdates.Publish(ctx, candidate.NodeID, candidate.Project, candidate.ID, imageupdate.Actor{Source: c.Provider, ChannelID: c.ID, ChatID: in.ChatID, ExternalID: in.UserID, Name: in.Name}, true)
	if err != nil {
		return "", err
	}
	return row.ID, nil
}

// Refresh existing platform messages as the candidate advances; failed edits retry on the next tick.
func (s *Service) refreshProjectActions(ctx context.Context) error {
	if s.deps.ProjectUpdates == nil {
		return nil
	}
	var actions []database.ProjectImageAction
	if err := s.db.WithContext(ctx).Table("project_image_actions AS action").Select("action.*").Joins("JOIN project_image_candidates AS candidate ON candidate.id = action.candidate_id").Joins("JOIN notification_deliveries AS delivery ON delivery.id = action.delivery_id").Where("action.state <> candidate.status AND action.state <> ? AND delivery.status = ?", "retired", "sent").Limit(50).Find(&actions).Error; err != nil {
		return err
	}
	adapter, ok := s.deps.Sender.(*Adapter)
	if !ok {
		return nil
	}
	done := map[string]bool{}
	for _, action := range actions {
		if done[action.DeliveryID] {
			continue
		}
		done[action.DeliveryID] = true
		var delivery database.NotificationDelivery
		if err := s.db.WithContext(ctx).First(&delivery, "id = ?", action.DeliveryID).Error; err != nil {
			return err
		}
		if delivery.ProviderMessageID == "" {
			continue
		}
		c, err := s.Channel(ctx, action.ChannelID)
		if err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err := s.db.WithContext(ctx).Model(&database.ProjectImageAction{}).Where("delivery_id = ?", delivery.ID).Update("state", "retired").Error; err != nil {
				return err
			}
			continue
		}
		if !destinationValid(c, delivery) {
			if err := s.db.WithContext(ctx).Model(&database.ProjectImageAction{}).Where("delivery_id = ?", delivery.ID).Update("state", "retired").Error; err != nil {
				return err
			}
			continue
		}
		var candidate database.ProjectImageCandidate
		if err := s.db.WithContext(ctx).First(&candidate, "id = ?", action.CandidateID).Error; err != nil {
			return err
		}
		events, _, err := s.deliveryEvents(ctx, delivery.ID)
		if err != nil {
			return err
		}
		var snapshot DeliverySnapshot
		if err := json.Unmarshal([]byte(delivery.SnapshotJSON), &snapshot); err != nil {
			return err
		}
		message := s.render(snapshot, events)
		view, err := s.deps.ProjectUpdates.Candidate(ctx, candidate.NodeID, candidate.Project, candidate.ID)
		if err != nil {
			return err
		}
		decorateProjectCandidate(&message, view)
		statusText := candidateStatus(candidate.Status, snapshot.Language)
		if candidate.Message != "" {
			statusText += "\n" + candidate.Message
		}
		message.Text += "\n" + statusText
		if len(message.Cards) > 0 {
			message.Cards[0].Fields = append(message.Cards[0].Fields, MessageField{Label: "SUMA", Value: statusText})
		}
		secrets, err := s.material(c)
		if err != nil {
			return err
		}
		c.Config.ChatID = delivery.ChatID
		if err := adapter.editProjectMessage(ctx, c, secrets, delivery.ProviderMessageID, message); err != nil {
			return err
		}
		if err := s.db.WithContext(ctx).Model(&database.ProjectImageAction{}).Where("delivery_id = ?", delivery.ID).Update("state", candidate.Status).Error; err != nil {
			return err
		}
	}
	return nil
}
func candidateStatus(status, language string) string {
	if language == "en-US" {
		return "Publication: " + status
	}
	names := map[string]string{"publishing": "发布中", "succeeded": "发布成功", "failed": "发布失败", "superseded": "版本已变化，请使用新的通知发布", "canceled": "发布已取消"}
	return names[status]
}
func (a *Adapter) editProjectMessage(ctx context.Context, c Channel, m Secrets, id string, message Message) error {
	switch c.Provider {
	case "telegram":
		parts, rich := telegramParts(message)
		text := message.Text
		if len(parts) > 0 {
			text = parts[len(parts)-1]
		}
		body := map[string]any{"chat_id": c.Config.ChatID, "message_id": id, "text": text, "reply_markup": telegramButtons(message)}
		if rich {
			body["parse_mode"] = "HTML"
		}
		_, err := a.request(ctx, http.MethodPost, "https://api.telegram.org/bot"+m.Token+"/editMessageText", body, "", false, m)
		return err
	case "feishu_app":
		token, err := a.feishuToken(ctx, c, m)
		if err != nil {
			return err
		}
		_, err = a.request(ctx, http.MethodPatch, "https://open.feishu.cn/open-apis/im/v1/messages/"+url.PathEscape(id), map[string]any{"content": encode(feishuCard(message))}, "Bearer "+token, false, m)
		return err
	}
	return errors.New("provider cannot update project messages")
}

func callbackMessage(language string, err error) string {
	if err != nil {
		if language == "en-US" {
			return "Unable to publish. Check authorization and the latest project notification."
		}
		return "无法发布，请检查权限并使用最新的项目通知。"
	}
	if language == "en-US" {
		return "Checking the version and publishing"
	}
	return "正在核对版本并发布"
}
