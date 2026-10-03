package app

import (
	"context"
	"strings"
	"time"

	"github.com/suma/suma/server/internal/ai"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/notification"
)

func chatOperations(notify *notification.Service, assistant *ai.Service) notification.ChatHandler {
	return func(in notification.Incoming, b database.NotificationBinding) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if b.ID == "" || notify.ValidateChatActor(ctx, b.UserID, b.ID) != nil {
			chatQuery(ctx, notify, assistant, in)
			return
		}
		actor := ai.Actor{UserID: b.UserID, Source: "chat", BindingID: b.ID, ExternalUserID: in.UserID, ChatID: in.ChatID}
		reply := func(text string) { _ = notify.Reply(ctx, in, notification.Message{Text: text}) }
		if in.Action == "approve" || in.Action == "reject" {
			opID, err := notify.ConsumeApproval(ctx, in, b)
			if err != nil {
				reply("Approval denied: " + err.Error())
				return
			}
			op, err := assistant.Operation(ctx, opID)
			if err == nil {
				op, err = assistant.Decide(ctx, opID, ai.Decision{ReviewToken: op.ReviewToken, Approve: in.Action == "approve"}, actor)
			}
			if err != nil {
				reply("Approval denied: " + err.Error())
				return
			}
			reply(op.ID + ": " + op.Status)
			return
		}
		if in.Action == "preview" {
			op, err := assistant.Operation(ctx, in.OperationID)
			if err != nil {
				reply(err.Error())
				return
			}
			if op.Status != "awaiting_approval" || !time.Now().Before(op.ExpiresAt) {
				reply(assistant.PreviewText(op) + "\nStatus: " + op.Status)
				return
			}
			token, err := notify.ApprovalToken(ctx, b, op.ID, in.ChatID, op.ExpiresAt)
			if err != nil {
				reply(err.Error())
				return
			}
			_ = notify.Reply(ctx, in, notification.Message{Text: assistant.PreviewText(op), OperationID: op.ID, ApprovalToken: token})
			return
		}
		cfg := assistant.Settings()
		question := strings.TrimSpace(in.Text)
		if question == "" {
			return
		}
		nodeID := ""
		if strings.HasPrefix(question, "/node ") {
			parts := strings.SplitN(strings.TrimPrefix(question, "/node "), " ", 2)
			if len(parts) == 2 {
				nodeID = parts[0]
				question = parts[1]
			}
		} else if len(cfg.NodeIDs) == 1 {
			nodeID = cfg.NodeIDs[0]
		}
		if nodeID == "" {
			reply("请使用 /node 节点ID 问题 选择已授权节点。 / Use /node NODE_ID QUESTION to select an authorized node.")
			return
		}
		run, err := assistant.Start(ctx, ai.RunInput{NodeID: nodeID, Question: question, ParentID: assistant.ChatParent(ctx, nodeID, actor)}, actor)
		if err != nil {
			reply(err.Error())
			return
		}
		reply("诊断已开始 / Diagnosis started: " + run.ID)
		// A diagnosis is bounded by five minutes. Revalidate binding before replying.
		go func() {
			timer := time.NewTicker(2 * time.Second)
			defer timer.Stop()
			deadline := time.NewTimer(6 * time.Minute)
			defer deadline.Stop()
			for {
				select {
				case <-notify.Done():
					return
				case <-deadline.C:
					return
				case <-timer.C:
					ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
					if notify.ValidateChatActor(ctx, b.UserID, b.ID) != nil {
						cancel()
						return
					}
					latest, err := assistant.Run(ctx, run.ID)
					if err != nil {
						cancel()
						return
					}
					if latest.Status == "running" {
						cancel()
						continue
					}
					text := latest.Result.Summary
					if latest.Error != "" {
						text = latest.Error
					}
					for _, id := range latest.Result.OperationIDs {
						text += "\n/approve " + id + " → 查看完整预览 / Open full preview"
					}
					_ = notify.Reply(ctx, in, notification.Message{Text: text})
					cancel()
					return
				}
			}
		}()
	}
}
