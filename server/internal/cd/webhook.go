package cd

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/suma/suma/server/internal/database"
	gitrepo "github.com/suma/suma/server/internal/git"
	"gorm.io/gorm"
)

var (
	ErrWebhookNotFound  = errors.New("webhook not found")
	ErrWebhookSignature = errors.New("invalid webhook signature")
	ErrWebhookIgnored   = errors.New("webhook event does not match the configured repository and ref")
	ErrWebhookDuplicate = errors.New("webhook delivery was already processed")
)

type webhookEvent struct {
	DeliveryID string
	Event      string
	Repository string
	Ref        string
	Commit     string
}

// HandleGitWebhook accepts one repository-neutral endpoint. The payload
// adapter is selected from standard webhook headers, not project configuration.
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
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
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

const (
	webhookGitHub  = "github"
	webhookGitLab  = "gitlab"
	webhookGeneric = "generic"
)

func webhookFormat(headers http.Header) string {
	if headers.Get("X-GitHub-Event") != "" || headers.Get("X-Hub-Signature-256") != "" {
		return webhookGitHub
	}
	if headers.Get("X-Gitlab-Event") != "" || headers.Get("X-Gitlab-Token") != "" || headers.Get("X-Gitlab-Webhook-UUID") != "" {
		return webhookGitLab
	}
	return webhookGeneric
}

func verifyWebhook(format, secretValue string, headers http.Header, body []byte) error {
	switch format {
	case webhookGitHub:
		value := headers.Get("X-Hub-Signature-256")
		if !verifyHMAC(value, "sha256=", secretValue, body) {
			return ErrWebhookSignature
		}
	case webhookGitLab:
		if signature := headers.Get("Webhook-Signature"); signature != "" {
			if !freshTimestamp(headers.Get("Webhook-Timestamp"), 5*time.Minute) || !verifyHMAC(signature, "sha256=", secretValue, body) {
				return ErrWebhookSignature
			}
		} else if subtle.ConstantTimeCompare([]byte(headers.Get("X-Gitlab-Token")), []byte(secretValue)) != 1 {
			return ErrWebhookSignature
		}
	case webhookGeneric:
		authorization := headers.Get("Authorization")
		if !strings.HasPrefix(authorization, "Bearer ") {
			return ErrWebhookSignature
		}
		value := strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer "))
		if subtle.ConstantTimeCompare([]byte(value), []byte(secretValue)) != 1 {
			return ErrWebhookSignature
		}
	default:
		return ErrWebhookNotFound
	}
	return nil
}

func verifyHMAC(value, prefix, secretValue string, body []byte) bool {
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	received, err := hex.DecodeString(strings.TrimPrefix(value, prefix))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secretValue))
	_, _ = mac.Write(body)
	return hmac.Equal(received, mac.Sum(nil))
}

func freshTimestamp(value string, window time.Duration) bool {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		var seconds int64
		if _, scanErr := fmt.Sscan(value, &seconds); scanErr != nil {
			return false
		}
		parsed = time.Unix(seconds, 0)
	}
	difference := time.Since(parsed)
	return difference <= window && difference >= -window
}

func parseWebhook(format string, headers http.Header, body []byte) (webhookEvent, error) {
	switch format {
	case webhookGitHub:
		if headers.Get("X-GitHub-Event") != "push" {
			return webhookEvent{}, ErrWebhookIgnored
		}
		var payload struct {
			Ref        string `json:"ref"`
			After      string `json:"after"`
			Repository struct {
				CloneURL string `json:"clone_url"`
				SSHURL   string `json:"ssh_url"`
			} `json:"repository"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return webhookEvent{}, errors.New("invalid GitHub webhook payload")
		}
		repository := payload.Repository.CloneURL
		if repository == "" {
			repository = payload.Repository.SSHURL
		}
		return webhookEvent{DeliveryID: headers.Get("X-GitHub-Delivery"), Event: "push", Repository: repository, Ref: payload.Ref, Commit: payload.After}, nil
	case webhookGitLab:
		eventName := headers.Get("X-Gitlab-Event")
		if eventName != "Push Hook" && eventName != "Tag Push Hook" {
			return webhookEvent{}, ErrWebhookIgnored
		}
		var payload struct {
			Ref         string `json:"ref"`
			CheckoutSHA string `json:"checkout_sha"`
			After       string `json:"after"`
			Project     struct {
				GitHTTPURL string `json:"git_http_url"`
				GitSSHURL  string `json:"git_ssh_url"`
			} `json:"project"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return webhookEvent{}, errors.New("invalid GitLab webhook payload")
		}
		repository := payload.Project.GitHTTPURL
		if repository == "" {
			repository = payload.Project.GitSSHURL
		}
		commit := payload.CheckoutSHA
		if commit == "" {
			commit = payload.After
		}
		deliveryID := headers.Get("X-Gitlab-Webhook-UUID")
		if deliveryID == "" {
			deliveryID = headers.Get("X-Gitlab-Event-UUID")
		}
		return webhookEvent{DeliveryID: deliveryID, Event: eventName, Repository: repository, Ref: payload.Ref, Commit: commit}, nil
	case webhookGeneric:
		var payload struct {
			Repository string `json:"repository"`
			Ref        string `json:"ref"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return webhookEvent{}, errors.New("invalid generic webhook payload")
		}
		if payload.Repository == "" {
			return webhookEvent{}, errors.New("generic webhook repository is required")
		}
		return webhookEvent{DeliveryID: headers.Get("Idempotency-Key"), Event: "trigger", Repository: payload.Repository, Ref: payload.Ref}, nil
	default:
		return webhookEvent{}, ErrWebhookNotFound
	}
}

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
