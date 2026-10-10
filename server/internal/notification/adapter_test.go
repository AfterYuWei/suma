package notification

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/event"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
}
func TestWebhookUnifiedPayloadAndSignature(t *testing.T) {
	a := NewAdapter()
	a.Client = func(bool) *http.Client {
		return &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(req.Body)
			timestamp := req.Header.Get("X-SUMA-Timestamp")
			mac := hmac.New(sha256.New, []byte("signing"))
			mac.Write([]byte(timestamp + "."))
			mac.Write(body)
			if req.Header.Get("X-SUMA-Signature") != "sha256="+hex.EncodeToString(mac.Sum(nil)) {
				t.Fatal("invalid signature")
			}
			if req.Header.Get("Authorization") != "Bearer AUTH" || !strings.Contains(string(body), "container.oom") {
				t.Fatal("missing event/auth")
			}
			return response(204, ""), nil
		})}
	}
	_, err := a.Send(context.Background(), Channel{NotificationChannel: database.NotificationChannel{Provider: "webhook"}, Config: Config{}}, Secrets{Endpoint: "https://example.com/hook", SigningKey: "signing", Authorization: "Bearer AUTH"}, Message{Text: "failure", Events: []event.Event{{Type: "container.oom"}}})
	if err != nil {
		t.Fatal(err)
	}
}
func TestTelegramRateLimitAndNotificationLink(t *testing.T) {
	a := NewAdapter()
	a.Client = func(bool) *http.Client {
		return &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(req.Body)
			if !strings.Contains(string(body), "https://suma.example/notifications") || strings.Contains(string(body), "callback_data") {
				t.Fatal("notification link missing or callback included")
			}
			return response(429, `{"ok":false,"parameters":{"retry_after":12}}`), nil
		})}
	}
	_, err := a.Send(context.Background(), Channel{NotificationChannel: database.NotificationChannel{Provider: "telegram"}, Config: Config{ChatID: "chat"}}, Secrets{Token: "123:PRIVATE"}, Message{Text: "notice", URL: "https://suma.example/notifications"})
	limit, ok := err.(*RateLimitError)
	if !ok || limit.After != 12*time.Second {
		t.Fatal(err)
	}
}
func TestFeishuCardsAndProviderErrorsHideSecrets(t *testing.T) {
	card := feishuCard(Message{Text: "Notice \n <at id=all> prompt", URL: "https://suma.example/notifications"})
	raw, _ := json.Marshal(card)
	if !strings.Contains(string(raw), "card") && len(raw) == 0 {
		t.Fatal("empty card")
	}
	if strings.Contains(string(raw), "<at id=all>") {
		t.Fatal("untrusted mentions not escaped")
	}
	a := NewAdapter()
	a.Client = func(bool) *http.Client {
		return &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
			return response(200, `{"code":99991672,"msg":"SECRET configuration invalid"}`), nil
		})}
	}
	_, err := a.request(context.Background(), "POST", "https://open.feishu.cn/example", map[string]any{}, "", false, Secrets{Token: "SECRET"})
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatal("provider secret leaked", err)
	}
}

func TestNativeProviderMalformedSuccessAndInBodyRateLimit(t *testing.T) {
	for _, body := range []string{"", "not-json", "{}", `{"ok":true,"result":{}}`} {
		a := NewAdapter()
		a.Client = func(bool) *http.Client {
			return &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { return response(200, body), nil })}
		}
		if _, err := a.Send(context.Background(), Channel{NotificationChannel: database.NotificationChannel{Provider: "telegram"}, Config: Config{ChatID: "test"}}, Secrets{Token: "123:test"}, Message{Text: "test"}); err == nil {
			t.Fatal("malformed success accepted", body)
		}
	}
	a := NewAdapter()
	a.Client = func(bool) *http.Client {
		return &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
			return response(200, `{"ok":false,"error_code":429,"parameters":{"retry_after":22}}`), nil
		})}
	}
	_, err := a.Send(context.Background(), Channel{NotificationChannel: database.NotificationChannel{Provider: "telegram"}, Config: Config{ChatID: "test"}}, Secrets{Token: "123:test"}, Message{Text: "test"})
	if limit, ok := err.(*RateLimitError); !ok || limit.After != 22*time.Second {
		t.Fatal("native rate limit ignored", err)
	}
}
