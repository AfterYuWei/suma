package deployment

import (
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
)

var (
	ErrWebhookSignature = errors.New("invalid webhook signature")
	ErrWebhookIgnored   = errors.New("webhook ignored")
	ErrWebhookNotFound  = errors.New("webhook not found")
)

const (
	webhookGitHub  = "github"
	webhookGitLab  = "gitlab"
	webhookGeneric = "generic"
)

type WebhookEvent struct{ DeliveryID, Event, Repository, Ref, Commit string }

func WebhookFormat(headers http.Header) string {
	if headers.Get("X-GitHub-Event") != "" || headers.Get("X-Hub-Signature-256") != "" {
		return webhookGitHub
	}
	if headers.Get("X-Gitlab-Event") != "" || headers.Get("X-Gitlab-Token") != "" || headers.Get("X-Gitlab-Webhook-UUID") != "" {
		return webhookGitLab
	}
	return webhookGeneric
}

func VerifyWebhook(format, secretValue string, headers http.Header, body []byte) error {
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

func ParseWebhook(format string, headers http.Header, body []byte) (WebhookEvent, error) {
	switch format {
	case webhookGitHub:
		if headers.Get("X-GitHub-Event") != "push" {
			return WebhookEvent{}, ErrWebhookIgnored
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
			return WebhookEvent{}, errors.New("invalid GitHub webhook payload")
		}
		repository := payload.Repository.CloneURL
		if repository == "" {
			repository = payload.Repository.SSHURL
		}
		return WebhookEvent{DeliveryID: headers.Get("X-GitHub-Delivery"), Event: "push", Repository: repository, Ref: payload.Ref, Commit: payload.After}, nil
	case webhookGitLab:
		eventName := headers.Get("X-Gitlab-Event")
		if eventName != "Push Hook" && eventName != "Tag Push Hook" {
			return WebhookEvent{}, ErrWebhookIgnored
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
			return WebhookEvent{}, errors.New("invalid GitLab webhook payload")
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
		return WebhookEvent{DeliveryID: deliveryID, Event: eventName, Repository: repository, Ref: payload.Ref, Commit: commit}, nil
	case webhookGeneric:
		var payload struct {
			Repository string `json:"repository"`
			Ref        string `json:"ref"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return WebhookEvent{}, errors.New("invalid generic webhook payload")
		}
		if payload.Repository == "" {
			return WebhookEvent{}, errors.New("generic webhook repository is required")
		}
		return WebhookEvent{DeliveryID: headers.Get("Idempotency-Key"), Event: "trigger", Repository: payload.Repository, Ref: payload.Ref}, nil
	default:
		return WebhookEvent{}, ErrWebhookNotFound
	}
}
