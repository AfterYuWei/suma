package cd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/suma/suma/server/internal/database"
	gitrepo "github.com/suma/suma/server/internal/git"
	"github.com/suma/suma/server/internal/deployment"
	"gorm.io/gorm"
)

var (
	ErrAutomationPaused = errors.New("automatic delivery is paused")
	ErrWebhookNotFound  = deployment.ErrWebhookNotFound
	ErrWebhookSignature = deployment.ErrWebhookSignature
	ErrWebhookIgnored   = deployment.ErrWebhookIgnored
	ErrWebhookDuplicate = errors.New("webhook delivery was already processed")
)

func (s *Service) HandleGitWebhook(ctx context.Context, hookID string, headers http.Header, body []byte) (database.Task, error) {
	return s.handleWebhook(ctx, webhookFormat(headers), hookID, headers, body)
}

func (s *Service) handleWebhook(ctx context.Context, format, hookID string, headers http.Header, body []byte) (database.Task, error) {
	if len(body) == 0 || len(body) > 2<<20 {
		return database.Task{}, errors.New("webhook payload must be between 1 byte and 2 MiB")
	}
	var project database.DeliveryProject
	if err := s.db.WithContext(ctx).Where("webhook_id = ? AND webhook_enabled = ?", hookID, true).First(&project).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return database.Task{}, ErrWebhookNotFound
		}
		return database.Task{}, err
	}
	secretValue, err := s.secrets.Decrypt(project.WebhookSecret)
	if err != nil || secretValue == "" {
		return database.Task{}, ErrWebhookSignature
	}
	if err := verifyWebhook(format, secretValue, headers, body); err != nil {
		return database.Task{}, err
	}
	if !project.AutomationEnabled {
		return database.Task{}, ErrAutomationPaused
	}
	event, err := parseWebhook(format, headers, body)
	if err != nil {
		return database.Task{}, err
	}
	configured, err := gitrepo.CanonicalRepository(project.GitCloneURL)
	if err != nil {
		return database.Task{}, err
	}
	received, err := gitrepo.CanonicalRepository(event.Repository)
	if err != nil || configured != received || !matchesRef(project, event.Ref) {
		return database.Task{}, ErrWebhookIgnored
	}
	if event.DeliveryID == "" {
		hash := sha256.Sum256(body)
		event.DeliveryID = hex.EncodeToString(hash[:])
	}
	delivery := database.GitWebhookDelivery{Format: format, HookID: hookID, DeliveryID: event.DeliveryID, Event: event.Event, Repository: received, GitRef: event.Ref, CommitSHA: event.Commit, Status: "accepted"}
	if err := s.db.WithContext(ctx).Create(&delivery).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return database.Task{}, ErrWebhookDuplicate
		}
		return database.Task{}, err
	}
	row, err := s.Sync(ctx, project.Name, "webhook", Actor{Name: "Git webhook"})
	now := time.Now()
	updates := map[string]any{"processed_at": now, "status": "queued"}
	if err != nil {
		updates["status"] = "failed"
		updates["failure_reason"] = err.Error()
	}
	_ = s.db.WithContext(ctx).Model(&delivery).Updates(updates).Error
	return row, err
}

func automaticTrigger(trigger string) bool {
	return trigger == "poll" || trigger == "webhook"
}

const (
	webhookGitHub  = "github"
	webhookGitLab  = "gitlab"
	webhookGeneric = "generic"
)

var webhookFormat = deployment.WebhookFormat
var verifyWebhook = deployment.VerifyWebhook
var parseWebhook = deployment.ParseWebhook

func matchesRef(project database.DeliveryProject, value string) bool {
	if project.GitRefType == gitrepo.RefCommit {
		return true
	}
	prefix := "refs/heads/"
	if project.GitRefType == gitrepo.RefTag {
		prefix = "refs/tags/"
	}
	return value == "" || value == prefix+project.GitRef
}
