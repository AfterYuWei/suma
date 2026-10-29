package notification

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"
)

func telegramButtons(message Message) any {
	rows := []any{}
	if message.URL != "" {
		rows = append(rows, []any{map[string]string{"text": messageAction(message), "url": message.URL}})
	}
	return map[string]any{"inline_keyboard": rows}
}

func feishuCard(message Message) map[string]any {
	title, elements := "SUMA", []any{}
	icon, color := messageStyle(message)
	if message.Default && message.Title != "" {
		title = icon + " " + message.Title
	}
	if message.Default && len(message.Cards) > 0 {
		for i, card := range message.Cards {
			sections := []string{}
			if len(message.Cards) > 1 {
				sections = append(sections, "**"+escapeFeishu(card.Title)+"**")
			}
			for _, field := range card.Fields {
				sections = append(sections, "**"+escapeFeishu(field.Label)+"**\n"+escapeFeishu(field.Value))
			}
			elements = append(elements, map[string]any{"tag": "markdown", "content": strings.Join(sections, "\n\n")})
			if i < len(message.Cards)-1 {
				elements = append(elements, map[string]any{"tag": "hr"})
			}
		}
		if message.More != "" {
			elements = append(elements, map[string]any{"tag": "markdown", "content": escapeFeishu(message.More)})
		}
	} else {
		body := message.Text
		if message.Default && message.Title != "" {
			body = strings.TrimSpace(strings.TrimPrefix(body, message.Title+"\n"))
		}
		elements = append(elements, map[string]any{"tag": "markdown", "content": escapeFeishu(body)})
	}
	if message.URL != "" {
		button := map[string]any{"tag": "button", "type": "primary", "text": map[string]string{"tag": "plain_text", "content": messageAction(message)}, "behaviors": []any{map[string]string{"type": "open_url", "default_url": message.URL}}}
		elements = append(elements, map[string]any{"tag": "column_set", "flex_mode": "flow", "columns": []any{map[string]any{"tag": "column", "width": "weighted", "weight": 1, "elements": []any{button}}}})
	}
	elements = append(elements, map[string]any{"tag": "markdown", "content": "SUMA · " + notificationFooter(message.Language), "text_size": "notation"})
	card := map[string]any{"schema": "2.0", "config": map[string]any{"wide_screen_mode": true}, "header": map[string]any{"template": color, "title": map[string]string{"tag": "plain_text", "content": title}}, "body": map[string]any{"elements": elements}}
	// Feishu limits serialized cards to 30 KB. A multilingual digest can hit
	// this before the eight-event limit; preserve every event in the inbox.
	if raw, _ := json.Marshal(card); len(raw) > 28000 && len(message.Cards) > 1 {
		message.Cards = message.Cards[:len(message.Cards)-1]
		remaining := len(message.Events) - len(message.Cards)
		message.More = fmt.Sprintf("另有 %d 条，请在站内消息查看", remaining)
		if message.Language == "en-US" {
			message.More = fmt.Sprintf("%d more events in your inbox", remaining)
		}
		return feishuCard(message)
	}
	return card
}

func notificationFooter(language string) string {
	if language == "en-US" {
		return "Docker operations notifications"
	}
	return "Docker 运维通知"
}

func messageAction(message Message) string {
	if message.Language == "en-US" {
		return "View details"
	}
	return "查看详情"
}

func escapeFeishu(s string) string {
	r := strings.NewReplacer("\\", "\\\\", "<", "\\<", ">", "\\>", "*", "\\*", "[", "\\[", "]", "\\]", "`", "\\`", "!", "\\!", "_", "\\_", "~", "\\~")
	return r.Replace(s)
}

func telegramParts(message Message) ([]string, bool) {
	if !message.Default || len(message.Cards) == 0 {
		// Telegram's limit counts UTF-16 units. Split plain custom templates
		// before formatting, so astral characters and literal markup stay intact.
		parts, part, size := []string{}, []rune{}, 0
		for _, r := range message.Text {
			units := 1
			if r > 0xffff {
				units = 2
			}
			if size+units > 3500 {
				parts = append(parts, string(part))
				part, size = nil, 0
			}
			part, size = append(part, r), size+units
		}
		if len(part) > 0 {
			parts = append(parts, string(part))
		}
		return parts, false
	}
	icon, _ := messageStyle(message)
	blocks := []string{"<b>" + icon + " " + html.EscapeString(message.Title) + "</b>"}
	for _, card := range message.Cards {
		if len(message.Cards) > 1 {
			blocks = append(blocks, "<b>"+html.EscapeString(card.Title)+"</b>")
		}
		for _, field := range card.Fields {
			blocks = append(blocks, "<b>"+html.EscapeString(field.Label)+"</b>\n"+html.EscapeString(field.Value))
		}
	}
	if message.More != "" {
		blocks = append(blocks, html.EscapeString(message.More))
	}
	blocks = append(blocks, "<i>SUMA · "+notificationFooter(message.Language)+"</i>")
	parts, part := []string{}, ""
	for _, block := range blocks {
		// The encoded byte count is conservative for UTF-16 and parsed HTML.
		// Each bounded field fits independently; never cut a tag or entity.
		if len(part)+len(block)+2 > 3500 && part != "" {
			parts, part = append(parts, part), ""
		}
		if part != "" {
			part += "\n\n"
		}
		part += block
	}
	if part != "" {
		parts = append(parts, part)
	}
	return parts, true
}
