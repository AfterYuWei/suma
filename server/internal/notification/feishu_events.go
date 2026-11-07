package notification

import (
	"context"

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
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
		in := Incoming{ID: ptr(msg.MessageId), ChannelID: c.ID, UserID: tenant + ":" + ptr(sender.SenderId.OpenId), ChatID: ptr(msg.ChatId), Private: private, Name: ptr(sender.SenderId.OpenId)}
		if !private {
			in.Name = in.ChatID
		}
		if err := enqueue(in); err != nil {
			observe("queue_full")
			return err
		}
		return nil
	})
	handler.Config.Logger = quietSDKLogger{}
	return handler
}
