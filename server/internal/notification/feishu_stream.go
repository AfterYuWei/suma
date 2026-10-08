package notification

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
)

// Markdown remains Markdown. Escape platform tags; mentions and HTML cannot
// create actions in the mutable answer. Buttons live in separate review cards.
func streamMarkdown(text string) string {
	return strings.NewReplacer("<", "\\<", ">", "\\>").Replace(text)
}

func streamCard(text, status string, streaming bool) map[string]any {
	return map[string]any{
		"schema": "2.0",
		"config": map[string]any{"wide_screen_mode": true, "update_multi": true, "streaming_mode": streaming,
			"summary": map[string]string{"content": "SUMA"}, "streaming_config": map[string]any{"print_frequency_ms": map[string]int{"default": 50}, "print_step": map[string]int{"default": 1}}},
		"header": map[string]any{"title": map[string]string{"tag": "plain_text", "content": "SUMA"}},
		"body": map[string]any{"elements": []any{
			map[string]string{"tag": "markdown", "element_id": "content", "content": streamMarkdown(text)},
			map[string]string{"tag": "markdown", "element_id": "status", "content": streamMarkdown(status)},
		}},
	}
}

func feishuScopeDenied(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "HTTP 403") || strings.Contains(err.Error(), "Feishu error 999916"))
}

func (a *Adapter) streamSend(ctx context.Context, c Channel, m Secrets, token, nonce string, content any) (string, error) {
	body := map[string]any{"receive_id": c.Config.ChatID, "msg_type": "interactive", "content": encode(content), "uuid": hash(nonce)[:32]}
	response, err := a.request(ctx, "POST", "https://open.feishu.cn/open-apis/im/v1/messages?receive_id_type=chat_id", body, "Bearer "+token, false, m)
	if err != nil {
		return "", err
	}
	var receipt struct {
		ID string `json:"message_id"`
	}
	_ = json.Unmarshal(response["data"], &receipt)
	if !validChatID(receipt.ID) {
		return "", errors.New("Feishu returned no stream message receipt")
	}
	return receipt.ID, nil
}

func (a *Adapter) UpdateStream(ctx context.Context, c Channel, m Secrets, nonce string, receipt StreamReceipt, update StreamUpdate) (StreamReceipt, error) {
	if c.Provider != "feishu_app" || !validChatID(c.Config.ChatID) {
		return receipt, ErrInvalid
	}
	token, err := a.feishuToken(ctx, c, m)
	if err != nil {
		return receipt, err
	}
	if receipt.Mode == "" {
		response, err := a.request(ctx, "POST", "https://open.feishu.cn/open-apis/cardkit/v1/cards", map[string]string{"type": "card_json", "data": encode(streamCard(update.Text, update.Status, true))}, "Bearer "+token, false, m)
		if err != nil {
			if !feishuScopeDenied(err) {
				return receipt, err
			}
			receipt.Mode = "message"
		} else {
			var card struct {
				ID string `json:"card_id"`
			}
			_ = json.Unmarshal(response["data"], &card)
			if !validChatID(card.ID) {
				return receipt, errors.New("Feishu returned no streaming card receipt")
			}
			receipt.Mode, receipt.CardID = "cardkit", card.ID
			receipt.Text, receipt.Status = update.Text, update.Status
		}
	}
	if receipt.MessageID == "" {
		var content any = streamCard(update.Text, update.Status, false)
		if receipt.Mode == "cardkit" {
			content = map[string]any{"type": "card", "data": map[string]string{"card_id": receipt.CardID}}
		}
		id, err := a.streamSend(ctx, c, m, token, nonce, content)
		if err != nil {
			return receipt, err
		}
		receipt.MessageID = id
		if receipt.Mode != "cardkit" {
			receipt.Text, receipt.Status = update.Text, update.Status
		}
	}
	if receipt.Mode == "cardkit" {
		cardURL := "https://open.feishu.cn/open-apis/cardkit/v1/cards/" + url.PathEscape(receipt.CardID)
		settings := func(streaming bool) error {
			receipt.Sequence++
			body := map[string]any{"settings": encode(map[string]any{"config": map[string]any{"streaming_mode": streaming}}), "sequence": receipt.Sequence, "uuid": hash(nonce + "|settings|" + encode(receipt.Sequence))[:32]}
			_, err := a.request(ctx, "PATCH", cardURL+"/settings", body, "Bearer "+token, false, m)
			return err
		}
		if receipt.Closed && !update.Final {
			if err := settings(true); err != nil {
				return receipt, err
			}
			receipt.Closed = false
		}
		if receipt.Text != update.Text {
			receipt.Sequence++
			path := cardURL + "/elements/content/content"
			body := map[string]any{"content": streamMarkdown(update.Text), "sequence": receipt.Sequence, "uuid": hash(nonce + "|content|" + encode(receipt.Sequence))[:32]}
			if !strings.HasPrefix(update.Text, receipt.Text) {
				path = cardURL + "/elements/content"
				delete(body, "content")
				body["element"] = encode(map[string]string{"tag": "markdown", "element_id": "content", "content": streamMarkdown(update.Text)})
			}
			if _, err := a.request(ctx, "PUT", path, body, "Bearer "+token, false, m); err != nil {
				return receipt, err
			}
			receipt.Text = update.Text
		}
		if receipt.Status != update.Status {
			receipt.Sequence++
			body := map[string]any{"content": streamMarkdown(update.Status), "sequence": receipt.Sequence, "uuid": hash(nonce + "|status|" + encode(receipt.Sequence))[:32]}
			if _, err := a.request(ctx, "PUT", cardURL+"/elements/status/content", body, "Bearer "+token, false, m); err != nil {
				return receipt, err
			}
			receipt.Status = update.Status
		}
		if update.Final && !receipt.Closed {
			if err := settings(false); err != nil {
				return receipt, err
			}
			receipt.Closed = true
		}
		return receipt, nil
	}
	if receipt.Mode == "message" {
		body := map[string]string{"content": encode(streamCard(update.Text, update.Status, false))}
		_, err := a.request(ctx, "PATCH", "https://open.feishu.cn/open-apis/im/v1/messages/"+url.PathEscape(receipt.MessageID), body, "Bearer "+token, false, m)
		if err == nil {
			receipt.Text, receipt.Status, receipt.Closed = update.Text, update.Status, update.Final
			return receipt, nil
		}
		if !feishuScopeDenied(err) {
			return receipt, err
		}
		receipt.Mode = "plain"
	}
	// If neither CardKit nor message-update permission is available, retain the
	// answer and deliver an explicit complete reply instead of losing the result.
	receipt.Text = update.Text
	if update.Final {
		text := update.Text + "\n\n流式卡片权限尚未生效，本次使用完整回复。请开通 cardkit:card:write（或 im:message:update）并发布应用版本。 / Streaming card permissions are unavailable; this is a complete reply. Enable cardkit:card:write (or im:message:update) and publish the app version."
		id, err := a.streamSend(ctx, c, m, token, nonce+"|final", streamCard(text, update.Status, false))
		if err != nil {
			return receipt, err
		}
		receipt.MessageID, receipt.Status, receipt.Closed = id, update.Status, true
	}
	return receipt, nil
}
