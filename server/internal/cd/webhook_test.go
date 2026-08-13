package cd

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dockport/dockport/server/internal/database"
	"github.com/dockport/dockport/server/internal/task"
)

func TestGitHubWebhookSignatureDuplicateAndRepositoryMatching(t *testing.T) {
	harness := newCDHarness(t, ModeManual)
	secretValue := "github-webhook-secret"
	configureWebhookProject(t, harness, "https://github.com/example/deploy.git", secretValue)
	body := []byte(`{"ref":"refs/heads/main","after":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","repository":{"clone_url":"https://github.com/example/deploy.git","ssh_url":"git@github.com:example/deploy.git"}}`)
	headers := make(http.Header)
	headers.Set("X-Hub-Signature-256", webhookHMAC(secretValue, body))
	headers.Set("X-GitHub-Delivery", "github-delivery-1")
	headers.Set("X-GitHub-Event", "push")

	row, err := harness.service.HandleGitWebhook(context.Background(), "hook-test", headers, body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := harness.service.HandleGitWebhook(context.Background(), "hook-test", headers, body); !errors.Is(err, ErrWebhookDuplicate) {
		t.Fatalf("duplicate webhook error = %v", err)
	}
	waitTask(t, harness.db, row.ID, task.StatusSuccess)
	assertWebhookDelivery(t, harness, "github-delivery-1", "queued")

	badSignature := headers.Clone()
	badSignature.Set("X-Hub-Signature-256", "sha256="+strings.Repeat("0", 64))
	badSignature.Set("X-GitHub-Delivery", "github-delivery-invalid-signature")
	if _, err := harness.service.HandleGitWebhook(context.Background(), "hook-test", badSignature, body); !errors.Is(err, ErrWebhookSignature) {
		t.Fatalf("invalid GitHub signature error = %v", err)
	}

	wrongRepository := []byte(`{"ref":"refs/heads/main","after":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","repository":{"clone_url":"https://github.com/attacker/deploy.git"}}`)
	wrongHeaders := headers.Clone()
	wrongHeaders.Set("X-Hub-Signature-256", webhookHMAC(secretValue, wrongRepository))
	wrongHeaders.Set("X-GitHub-Delivery", "github-delivery-wrong-repository")
	if _, err := harness.service.HandleGitWebhook(context.Background(), "hook-test", wrongHeaders, wrongRepository); !errors.Is(err, ErrWebhookIgnored) {
		t.Fatalf("wrong GitHub repository error = %v", err)
	}
	assertWebhookDeliveryCount(t, harness, 1)
}

func TestGenericWebhookEndpointDetectsGitHubPayload(t *testing.T) {
	harness := newCDHarness(t, ModeManual)
	secretValue := "generic-endpoint-secret"
	configureWebhookProject(t, harness, "https://github.com/example/deploy.git", secretValue)
	body := []byte(`{"ref":"refs/heads/main","after":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","repository":{"clone_url":"https://github.com/example/deploy.git"}}`)
	headers := make(http.Header)
	headers.Set("X-Hub-Signature-256", webhookHMAC(secretValue, body))
	headers.Set("X-GitHub-Delivery", "auto-detected-delivery")
	headers.Set("X-GitHub-Event", "push")

	row, err := harness.service.HandleGitWebhook(context.Background(), "hook-test", headers, body)
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, harness.db, row.ID, task.StatusSuccess)
	assertWebhookDelivery(t, harness, "auto-detected-delivery", "queued")
}

func TestSelfManagedGitLabWebhookHMACDuplicateAndRepositoryMatching(t *testing.T) {
	harness := newCDHarness(t, ModeManual)
	secretValue := "gitlab-webhook-secret"
	configureWebhookProject(t, harness, "https://git.company.test/team/deploy.git", secretValue)
	body := []byte(`{"ref":"refs/heads/main","checkout_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","project":{"git_http_url":"https://git.company.test/team/deploy.git","git_ssh_url":"git@git.company.test:team/deploy.git"}}`)
	headers := make(http.Header)
	headers.Set("Webhook-Timestamp", fmt.Sprintf("%d", time.Now().Unix()))
	headers.Set("Webhook-Signature", webhookHMAC(secretValue, body))
	headers.Set("X-Gitlab-Webhook-UUID", "gitlab-delivery-1")
	headers.Set("X-Gitlab-Event", "Push Hook")

	row, err := harness.service.HandleGitWebhook(context.Background(), "hook-test", headers, body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := harness.service.HandleGitWebhook(context.Background(), "hook-test", headers, body); !errors.Is(err, ErrWebhookDuplicate) {
		t.Fatalf("duplicate webhook error = %v", err)
	}
	waitTask(t, harness.db, row.ID, task.StatusSuccess)
	assertWebhookDelivery(t, harness, "gitlab-delivery-1", "queued")

	stale := headers.Clone()
	stale.Set("Webhook-Timestamp", fmt.Sprintf("%d", time.Now().Add(-10*time.Minute).Unix()))
	stale.Set("X-Gitlab-Webhook-UUID", "gitlab-delivery-stale")
	if _, err := harness.service.HandleGitWebhook(context.Background(), "hook-test", stale, body); !errors.Is(err, ErrWebhookSignature) {
		t.Fatalf("stale GitLab signature error = %v", err)
	}

	wrongRepository := []byte(`{"ref":"refs/heads/main","checkout_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","project":{"git_http_url":"https://git.company.test/other/deploy.git"}}`)
	wrongHeaders := headers.Clone()
	wrongHeaders.Set("Webhook-Timestamp", time.Now().Format(time.RFC3339))
	wrongHeaders.Set("Webhook-Signature", webhookHMAC(secretValue, wrongRepository))
	wrongHeaders.Set("X-Gitlab-Webhook-UUID", "gitlab-delivery-wrong-repository")
	if _, err := harness.service.HandleGitWebhook(context.Background(), "hook-test", wrongHeaders, wrongRepository); !errors.Is(err, ErrWebhookIgnored) {
		t.Fatalf("wrong GitLab repository error = %v", err)
	}
	assertWebhookDeliveryCount(t, harness, 1)
}

func TestGitLabWebhookAcceptsLegacyConstantTimeToken(t *testing.T) {
	harness := newCDHarness(t, ModeManual)
	secretValue := "legacy-gitlab-secret"
	configureWebhookProject(t, harness, "git@git.company.test:team/deploy.git", secretValue)
	body := []byte(`{"ref":"refs/heads/main","after":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","project":{"git_ssh_url":"git@git.company.test:team/deploy.git"}}`)
	headers := make(http.Header)
	headers.Set("X-Gitlab-Token", secretValue)
	headers.Set("X-Gitlab-Event-UUID", "gitlab-legacy-1")
	headers.Set("X-Gitlab-Event", "Push Hook")
	row, err := harness.service.HandleGitWebhook(context.Background(), "hook-test", headers, body)
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, harness.db, row.ID, task.StatusSuccess)
}

func TestOtherWebhookBearerDuplicateAndRepositoryMatching(t *testing.T) {
	harness := newCDHarness(t, ModeManual)
	secretValue := "generic-trigger-secret"
	configureWebhookProject(t, harness, "ssh://git@forge.example.test:2222/team/deploy.git", secretValue)
	body := []byte(`{"repository":"https://forge.example.test/team/deploy.git","ref":"refs/heads/main"}`)
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+secretValue)
	headers.Set("Idempotency-Key", "other-delivery-1")

	row, err := harness.service.HandleGitWebhook(context.Background(), "hook-test", headers, body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := harness.service.HandleGitWebhook(context.Background(), "hook-test", headers, body); !errors.Is(err, ErrWebhookDuplicate) {
		t.Fatalf("duplicate webhook error = %v", err)
	}
	waitTask(t, harness.db, row.ID, task.StatusSuccess)
	assertWebhookDelivery(t, harness, "other-delivery-1", "queued")

	badToken := headers.Clone()
	badToken.Set("Authorization", "Bearer wrong-token")
	badToken.Set("Idempotency-Key", "other-delivery-invalid-token")
	if _, err := harness.service.HandleGitWebhook(context.Background(), "hook-test", badToken, body); !errors.Is(err, ErrWebhookSignature) {
		t.Fatalf("invalid Other bearer error = %v", err)
	}

	wrongRepository := []byte(`{"repository":"https://forge.example.test/team/other.git","ref":"refs/heads/main"}`)
	wrongHeaders := headers.Clone()
	wrongHeaders.Set("Idempotency-Key", "other-delivery-wrong-repository")
	if _, err := harness.service.HandleGitWebhook(context.Background(), "hook-test", wrongHeaders, wrongRepository); !errors.Is(err, ErrWebhookIgnored) {
		t.Fatalf("wrong Other repository error = %v", err)
	}
	assertWebhookDeliveryCount(t, harness, 1)
}

func TestWebhookRejectsWrongHookAndRef(t *testing.T) {
	harness := newCDHarness(t, ModeManual)
	secretValue := "github-webhook-secret"
	configureWebhookProject(t, harness, "https://github.com/example/deploy.git", secretValue)
	body := []byte(`{"ref":"refs/heads/release","after":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","repository":{"clone_url":"https://github.com/example/deploy.git"}}`)
	headers := make(http.Header)
	headers.Set("X-Hub-Signature-256", webhookHMAC(secretValue, body))
	headers.Set("X-GitHub-Delivery", "wrong-ref")
	headers.Set("X-GitHub-Event", "push")
	if _, err := harness.service.HandleGitWebhook(context.Background(), "missing-hook", headers, body); !errors.Is(err, ErrWebhookNotFound) {
		t.Fatalf("missing hook error = %v", err)
	}
	if _, err := harness.service.HandleGitWebhook(context.Background(), "hook-test", headers, body); !errors.Is(err, ErrWebhookIgnored) {
		t.Fatalf("ref mismatch error = %v", err)
	}
	assertWebhookDeliveryCount(t, harness, 0)
}

func TestWebhookRejectsEmptyOversizedAndUnsupportedEvents(t *testing.T) {
	harness := newCDHarness(t, ModeManual)
	secretValue := "github-webhook-secret"
	configureWebhookProject(t, harness, "https://github.com/example/deploy.git", secretValue)
	if _, err := harness.service.HandleGitWebhook(context.Background(), "hook-test", nil, nil); err == nil {
		t.Fatal("empty webhook body was accepted")
	}
	oversized := make([]byte, (2<<20)+1)
	if _, err := harness.service.HandleGitWebhook(context.Background(), "hook-test", nil, oversized); err == nil {
		t.Fatal("oversized webhook body was accepted")
	}
	body := []byte(`{"repository":{"clone_url":"https://github.com/example/deploy.git"}}`)
	headers := make(http.Header)
	headers.Set("X-Hub-Signature-256", webhookHMAC(secretValue, body))
	headers.Set("X-GitHub-Event", "issues")
	if _, err := harness.service.HandleGitWebhook(context.Background(), "hook-test", headers, body); !errors.Is(err, ErrWebhookIgnored) {
		t.Fatalf("unsupported event error = %v", err)
	}
}

func configureWebhookProject(t *testing.T, harness *cdHarness, cloneURL, secretValue string) {
	t.Helper()
	ciphertext, err := harness.secrets.Encrypt(secretValue)
	if err != nil {
		t.Fatal(err)
	}
	if err := harness.db.Model(&database.DeliveryProject{}).Where("id = ?", harness.project.ID).Updates(map[string]any{
		"git_clone_url":   cloneURL,
		"webhook_enabled": true, "webhook_id": "hook-test", "webhook_secret": ciphertext,
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func webhookHMAC(secretValue string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secretValue))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func assertWebhookDelivery(t *testing.T, harness *cdHarness, deliveryID, status string) {
	t.Helper()
	var row database.GitWebhookDelivery
	if err := harness.db.Where("delivery_id = ?", deliveryID).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Status != status || row.ProcessedAt == nil {
		t.Fatalf("webhook delivery = %#v", row)
	}
}

func assertWebhookDeliveryCount(t *testing.T, harness *cdHarness, want int64) {
	t.Helper()
	var count int64
	if err := harness.db.Model(&database.GitWebhookDelivery{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("webhook delivery count = %d, want %d", count, want)
	}
}
