package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
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
				op, err = assistant.Decide(ctx, opID, ai.Decision{RequestID: notification.ID(), ReviewToken: op.ReviewToken, Approve: in.Action == "approve"}, actor)
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
			message := notification.Message{Text: assistant.PreviewText(op)}
			if channel, e := notify.Channel(ctx, b.ChannelID); e == nil && channel.Config.PublicURL != "" {
				message.URL = strings.TrimRight(channel.Config.PublicURL, "/") + "/ai-operations#operation=" + op.ID
			}
			if op.Status == "awaiting_approval" && time.Now().Before(op.ExpiresAt) {
				if len(op.Confirmations) > 0 {
					message.Text += "\n需要名称或风险确认，请在工作台完整审核。 / Complete the required confirmations in SUMA."
				} else {
					message.ApprovalToken, err = notify.ApprovalToken(ctx, b, op.ID, in.ChatID, op.ExpiresAt)
					message.OperationID = op.ID
				}
			}
			if err != nil {
				reply(err.Error())
				return
			}
			_ = notify.Reply(ctx, in, message)
			return
		}
		text := strings.TrimSpace(in.Text)
		if text == "/cancel" {
			conversation, err := assistant.CreateConversation(ctx, ai.ConversationInput{}, actor)
			if err != nil {
				reply(err.Error())
				return
			}
			if conversation.CurrentRunID == "" {
				reply("当前没有任务。 / No active task.")
				return
			}
			if _, err = assistant.CancelRun(ctx, conversation.CurrentRunID, actor); err != nil {
				reply(err.Error())
			} else {
				reply("已取消后续步骤，已执行结果保留。 / Remaining steps canceled; executed results are retained.")
			}
			return
		}
		if in.Action == "input" || strings.HasPrefix(text, "/select ") {
			response := ""
			if strings.HasPrefix(text, "/select ") {
				parts := strings.SplitN(strings.TrimPrefix(text, "/select "), " ", 2)
				in.OperationID = parts[0]
				if len(parts) == 2 {
					response = parts[1]
				}
			}
			payload, err := notify.PeekAction(ctx, in, b)
			if err != nil {
				reply(err.Error())
				return
			}
			parts := strings.Split(payload, ":")
			if len(parts) != 5 || parts[0] != "input" {
				reply("Invalid interaction")
				return
			}
			revision, e := strconv.ParseUint(parts[3], 10, 64)
			index, e2 := strconv.Atoi(parts[4])
			run, err := assistant.RunAs(ctx, parts[1], actor)
			if e != nil || e2 != nil || err != nil || run.Interaction == nil || run.Interaction.ID != parts[2] || run.Revision != revision {
				reply("选择已过期，请打开当前任务。 / This selection expired; open the current task.")
				return
			}
			requestHash := sha256.Sum256([]byte(in.OperationID))
			answer := ai.InputAnswer{InteractionID: parts[2], ExpectedRevision: revision, RequestID: hex.EncodeToString(requestHash[:]), Text: response}
			if index >= 0 {
				if index >= len(run.Interaction.Options) {
					reply("Invalid selection")
					return
				}
				answer.Values = []string{run.Interaction.Options[index].ID}
			} else if len(run.Interaction.Options) > 0 {
				for _, value := range strings.Split(response, ",") {
					n, err := strconv.Atoi(strings.TrimSpace(value))
					if err != nil || n < 1 || n > len(run.Interaction.Options) {
						reply("请使用选项编号。 / Use the listed option numbers.")
						return
					}
					answer.Values = append(answer.Values, run.Interaction.Options[n-1].ID)
				}
			}
			if _, err = assistant.AnswerInput(ctx, run.ID, answer, actor); err != nil {
				reply(err.Error())
			} else {
				_, _ = notify.ConsumeApproval(ctx, in, b)
				reply("已记录选择，继续任务。 / Selection recorded; resuming the task.")
			}
			return
		}
		if text == "" {
			return
		}
		nodeID := ""
		if strings.HasPrefix(text, "/node ") {
			parts := strings.SplitN(strings.TrimPrefix(text, "/node "), " ", 2)
			if len(parts) != 2 {
				reply("Use /node NODE_ID QUESTION")
				return
			}
			nodeID, text = parts[0], strings.TrimSpace(parts[1])
		}
		run, err := assistant.Start(ctx, ai.RunInput{NodeID: nodeID, Question: text}, actor)
		if err != nil {
			reply(err.Error())
			return
		}
		reply("任务已开始 / Task started: " + run.ID)
	}
}
func chatWorkflowDelivery(notify *notification.Service, assistant *ai.Service) func(context.Context, ai.Actor, ai.WorkflowEvent, ai.Run) error {
	return func(ctx context.Context, actor ai.Actor, entry ai.WorkflowEvent, run ai.Run) error {
		b, err := notify.ValidateBinding(ctx, actor.UserID, actor.BindingID)
		if err != nil {
			return err
		}
		in := notification.Incoming{ChannelID: b.ChannelID, UserID: actor.ExternalUserID, ChatID: actor.ChatID}
		message := notification.Message{}
		switch entry.Type {
		case "run.waiting_input":
			if run.Status != "waiting_input" || run.Interaction == nil {
				return nil
			}
			input := run.Interaction
			message.Text = input.Prompt
			channel, err := notify.Channel(ctx, b.ChannelID)
			if err != nil {
				return err
			}
			if !input.Multiple && (channel.Provider == "telegram" || channel.Provider == "feishu_app") {
				for index, option := range input.Options {
					if index >= 20 {
						break
					}
					token, err := notify.ApprovalToken(ctx, b, fmt.Sprintf("input:%s:%s:%d:%d", run.ID, input.ID, input.Revision, index), in.ChatID, input.ExpiresAt)
					if err != nil {
						return err
					}
					message.Choices = append(message.Choices, notification.InteractionChoice{Label: option.Name, Token: token})
				}
			}
			token, err := notify.ApprovalToken(ctx, b, fmt.Sprintf("input:%s:%s:%d:-1", run.ID, input.ID, input.Revision), in.ChatID, input.ExpiresAt)
			if err != nil {
				return err
			}
			for index, option := range input.Options {
				message.Text += fmt.Sprintf("\n%d. %s (%s)", index+1, option.Name, option.NodeID)
			}
			message.Text += "\n/select " + token + " " + "选项编号或参数 / option number(s) or parameter"
		case "run.waiting_approval":
			if run.Status != "waiting_approval" {
				return nil
			}
			message.Text = run.Result.Summary
			for _, id := range run.Result.OperationIDs {
				op, err := assistant.Operation(ctx, id)
				if err == nil && op.Status == "awaiting_approval" {
					message.Text += "\n/approve " + op.ID + " → 查看完整预览 / Open full preview"
					message.ApproveID = op.ID
				}
			}
		case "run.completed", "run.failed", "run.paused":
			message.Text = run.Result.Summary
			if run.Error != "" {
				message.Text += "\n" + run.Error
			}
		case "operation.completed", "operation.failed", "operation.interrupted":
			message.Text = "步骤执行结果 / Step result: " + string(entry.Payload)
		default:
			return nil
		}
		if message.Text == "" {
			return nil
		}
		return notify.Reply(ctx, in, message)
	}
}
