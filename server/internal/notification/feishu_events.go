package notification

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func feishuEventHandler(c Channel, botID func() string, enqueue func(Incoming) error, observe func(string)) *dispatcher.EventDispatcher {
	handler := dispatcher.NewEventDispatcher("", "").OnP2MessageReceiveV1(func(_ context.Context, e *larkim.P2MessageReceiveV1) error {
		observe("received")
		if e == nil || e.Event == nil || e.Event.Message == nil || e.Event.Sender == nil || e.Event.Sender.SenderId == nil {
			observe("invalid_event")
			return nil
		}
		msg, sender := e.Event.Message, e.Event.Sender
		if e.EventV2Base != nil && e.EventV2Base.Header != nil && e.EventV2Base.Header.AppID != "" && e.EventV2Base.Header.AppID != c.Config.AppID {
			observe("invalid_event")
			return nil
		}
		tenant := ptr(sender.TenantKey)
		if tenant == "" {
			tenant = e.EventV2Base.TenantKey()
		}
		if tenant == "" || ptr(sender.SenderId.OpenId) == "" || !validChatID(ptr(msg.ChatId)) || ptr(msg.MessageId) == "" {
			observe("missing_identity")
			return nil
		}
		if ptr(sender.SenderType) != "user" {
			observe("ignored_bot")
			return nil
		}
		private := ptr(msg.ChatType) == "p2p"
		if !private {
			if ptr(msg.ChatType) != "group" {
				observe("invalid_event")
				return nil
			}
			id := botID()
			if id == "" {
				observe("bot_identity_pending")
				return nil
			}
			mentioned := false
			for _, mention := range msg.Mentions {
				if mention != nil && mention.Id != nil && ptr(mention.Id.OpenId) == id && id != "" {
					mentioned = true
				}
			}
			if !mentioned {
				observe("ignored_group")
				return nil
			}
		}
		var content struct {
			Text string `json:"text"`
		}
		discoveryOnly := ptr(msg.MessageType) != "text" || json.Unmarshal([]byte(ptr(msg.Content)), &content) != nil
		for _, mention := range msg.Mentions {
			if mention != nil {
				if key := ptr(mention.Key); key != "" {
					content.Text = strings.ReplaceAll(content.Text, key, "")
				}
			}
		}
		discoveryOnly = discoveryOnly || strings.TrimSpace(content.Text) == ""
		in := Incoming{ID: ptr(msg.MessageId), ChannelID: c.ID, UserID: tenant + ":" + ptr(sender.SenderId.OpenId), ChatID: ptr(msg.ChatId), Private: private, Text: content.Text, Name: ptr(sender.SenderId.OpenId), DiscoveryOnly: discoveryOnly}
		if !private {
			in.Name = in.ChatID
		}
		if err := enqueue(in); err != nil {
			observe("queue_full")
			return err
		}
		return nil
	}).OnP2CardActionTrigger(func(_ context.Context, e *callback.CardActionTriggerEvent) (*callback.CardActionTriggerResponse, error) {
		if e == nil || e.Event == nil || e.Event.Operator == nil || e.Event.Action == nil || e.Event.Context == nil {
			return nil, nil
		}
		if ptr(e.Event.Operator.TenantKey) == "" || e.Event.Operator.OpenID == "" || e.Event.Context.OpenChatID == "" {
			return nil, nil
		}
		action, _ := e.Event.Action.Value["action"].(string)
		op, _ := e.Event.Action.Value["operation_id"].(string)
		if action == "approve" || action == "reject" {
			op, _ = e.Event.Action.Value["token"].(string)
		}
		key := ""
		if e.EventV2Base != nil && e.EventV2Base.Header != nil {
			key = e.EventV2Base.Header.EventID
		}
		in := Incoming{ID: key, ChannelID: c.ID, UserID: ptr(e.Event.Operator.TenantKey) + ":" + e.Event.Operator.OpenID, ChatID: e.Event.Context.OpenChatID, Action: action, OperationID: op, Name: e.Event.Operator.OpenID}
		if err := enqueue(in); err != nil {
			return nil, err
		}
		return &callback.CardActionTriggerResponse{Toast: &callback.Toast{Type: "info", Content: "请求已接收，请查看机器人回复 / Request received"}}, nil
	})
	handler.Config.Logger = quietSDKLogger{}
	return handler
}
