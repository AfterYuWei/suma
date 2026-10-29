package notification

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/suma/suma/server/internal/outbound"
	"github.com/suma/suma/server/internal/redact"
)

type RateLimitError struct{ After time.Duration }

func (e *RateLimitError) Error() string { return "provider rate limit; retry scheduled" }

type tokenEntry struct {
	Token string
	Until time.Time
}
type Adapter struct {
	Client func(bool) *http.Client
	mu     sync.Mutex
	tokens map[string]tokenEntry
}

func NewAdapter() *Adapter { return &Adapter{Client: outbound.Client, tokens: map[string]tokenEntry{}} }
func hide(text string, m Secrets) string {
	for _, v := range []string{m.Token, m.Endpoint, m.SigningKey, m.Authorization} {
		if v != "" {
			text = strings.ReplaceAll(text, v, "[REDACTED]")
		}
	}
	return redact.Bounded(text, 512)
}
func (a *Adapter) request(ctx context.Context, method, address string, body any, authorization string, private bool, m Secrets) (map[string]json.RawMessage, error) {
	if err := outbound.Validate(address, private); err != nil {
		return nil, err
	}
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, address, bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("invalid provider request")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	if m.SigningKey != "" && method == "POST" {
		stamp := strconv.FormatInt(time.Now().Unix(), 10)
		mac := hmac.New(sha256.New, []byte(m.SigningKey))
		_, _ = mac.Write([]byte(stamp + "."))
		_, _ = mac.Write(data)
		req.Header.Set("X-SUMA-Timestamp", stamp)
		req.Header.Set("X-SUMA-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	response, err := a.Client(private).Do(req)
	if err != nil {
		return nil, errors.New("provider connection failed")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return nil, errors.New("provider response exceeded limits")
	}
	var result map[string]json.RawMessage
	decodeErr := json.Unmarshal(raw, &result)
	if response.StatusCode == 429 || string(result["error_code"]) == "429" {
		delay := time.Second * 30
		if seconds, _ := strconv.Atoi(response.Header.Get("Retry-After")); seconds > 0 {
			delay = time.Duration(seconds) * time.Second
		}
		var p struct {
			RetryAfter int `json:"retry_after"`
		}
		_ = json.Unmarshal(result["parameters"], &p)
		if p.RetryAfter > 0 {
			delay = time.Duration(p.RetryAfter) * time.Second
		}
		return nil, &RateLimitError{After: delay}
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, fmt.Errorf("provider returned HTTP %d", response.StatusCode)
	}
	parsed, _ := url.Parse(address)
	if parsed.Hostname() == "api.telegram.org" {
		if decodeErr != nil || string(result["ok"]) != "true" && string(result["ok"]) != "false" {
			return nil, errors.New("invalid Telegram response")
		}
	} else if parsed.Hostname() == "open.feishu.cn" {
		if decodeErr != nil || len(result["code"]) == 0 && len(result["StatusCode"]) == 0 {
			return nil, errors.New("invalid Feishu response")
		}
	}
	if code := result["code"]; len(code) > 0 && string(code) != "0" {
		var msg string
		_ = json.Unmarshal(result["msg"], &msg)
		return nil, fmt.Errorf("Feishu error %s: %s", code, hide(msg, m))
	}
	if code := result["StatusCode"]; len(code) > 0 && string(code) != "0" {
		return nil, fmt.Errorf("Feishu bot error %s", code)
	}
	if ok := result["ok"]; string(ok) == "false" {
		var msg string
		_ = json.Unmarshal(result["description"], &msg)
		return nil, errors.New("Telegram: " + hide(msg, m))
	}
	return result, nil
}
func (a *Adapter) feishuToken(ctx context.Context, c Channel, m Secrets) (string, error) {
	key := c.Config.AppID + "|" + hash(m.Token)
	a.mu.Lock()
	defer a.mu.Unlock()
	if cached, ok := a.tokens[key]; ok && time.Now().Before(cached.Until) {
		return cached.Token, nil
	}
	result, err := a.request(ctx, "POST", "https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal", map[string]string{"app_id": c.Config.AppID, "app_secret": m.Token}, "", false, m)
	if err != nil {
		return "", err
	}
	var token string
	var expire int
	_ = json.Unmarshal(result["tenant_access_token"], &token)
	_ = json.Unmarshal(result["expire"], &expire)
	if token == "" {
		return "", errors.New("Feishu did not issue an application token")
	}
	a.tokens[key] = tokenEntry{Token: token, Until: time.Now().Add(time.Duration(max(expire-60, 1)) * time.Second)}
	return token, nil
}
func (a *Adapter) Check(ctx context.Context, c Channel, m Secrets) (map[string]any, error) {
	switch c.Provider {
	case "telegram":
		result, err := a.request(ctx, "GET", "https://api.telegram.org/bot"+m.Token+"/getMe", nil, "", false, m)
		if err != nil {
			return nil, err
		}
		var bot struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		}
		_ = json.Unmarshal(result["result"], &bot)
		if bot.ID == 0 {
			return nil, errors.New("invalid Telegram bot identity")
		}
		return map[string]any{"credentials_valid": true, "bot_name": bot.Username}, nil
	case "feishu_app":
		token, err := a.feishuToken(ctx, c, m)
		if err != nil {
			return nil, err
		}
		result, err := a.request(ctx, "GET", "https://open.feishu.cn/open-apis/bot/v3/info/", nil, "Bearer "+token, false, m)
		if err != nil {
			return nil, err
		}
		var bot map[string]any
		_ = json.Unmarshal(result["bot"], &bot)
		if identity, _ := bot["open_id"].(string); identity == "" {
			return nil, errors.New("invalid Feishu bot identity")
		}
		return map[string]any{"credentials_valid": true, "bot": bot, "permissions_require_platform_configuration": true}, nil
	default:
		return map[string]any{"connected": false, "requires_test_message": true}, nil
	}
}
func (a *Adapter) Send(ctx context.Context, c Channel, m Secrets, message Message) (string, error) {
	switch c.Provider {
	case "telegram":
		if c.Config.ChatID == "" {
			return "", errors.New("select a recipient chat before sending a test")
		}
		parts, rich := telegramParts(message)
		first := ""
		for i, part := range parts {
			body := map[string]any{"chat_id": c.Config.ChatID, "text": part, "link_preview_options": map[string]bool{"is_disabled": true}}
			if rich {
				body["parse_mode"] = "HTML"
			}
			if i == len(parts)-1 && message.URL != "" {
				body["reply_markup"] = telegramButtons(message)
			}
			result, err := a.request(ctx, "POST", "https://api.telegram.org/bot"+m.Token+"/sendMessage", body, "", false, m)
			if err != nil {
				return "", err
			}
			var sent struct {
				ID int64 `json:"message_id"`
			}
			_ = json.Unmarshal(result["result"], &sent)
			if sent.ID == 0 {
				return "", errors.New("Telegram returned no message receipt")
			}
			if first == "" {
				first = strconv.FormatInt(sent.ID, 10)
			}
		}
		return first, nil
	case "feishu_app":
		if c.Config.ChatID == "" {
			return "", errors.New("select a recipient chat before sending a test")
		}
		token, err := a.feishuToken(ctx, c, m)
		if err != nil {
			return "", err
		}
		body := map[string]any{"receive_id": c.Config.ChatID, "msg_type": "interactive", "content": encode(feishuCard(message))}
		result, err := a.request(ctx, "POST", "https://open.feishu.cn/open-apis/im/v1/messages?receive_id_type=chat_id", body, "Bearer "+token, false, m)
		if err != nil {
			return "", err
		}
		var data struct {
			ID string `json:"message_id"`
		}
		_ = json.Unmarshal(result["data"], &data)
		if data.ID == "" {
			return "", errors.New("Feishu returned no message receipt")
		}
		return data.ID, nil
	case "webhook":
		text := message.Text
		if message.Default && message.URL != "" {
			text += "\n" + message.URL
		}
		_, err := a.request(ctx, "POST", m.Endpoint, map[string]any{"schema_version": 1, "message": text, "events": message.Events, "url": message.URL}, m.Authorization, c.Config.AllowPrivate, m)
		return "", err
	default:
		return "", ErrInvalid
	}
}
