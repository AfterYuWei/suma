package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/suma/suma/server/internal/ai"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/notification"
)

const chatAIDisabledText = "SUMA 的全局 AI 运维尚未启用。账号绑定和渠道聊天开关不会自动启用 AI。 / Global AI operations are off. Account binding and channel chat do not enable AI automatically.\n1. 登录 SUMA → 设置 → AI 运维，配置默认模型并授权要查询的节点。 / Open SUMA → Settings → AI operations, configure the default model and authorize the target node.\n2. 开启“启用 AI 运维”，点击“保存 AI 设置”，然后“测试连接”。 / Turn on Enable AI operations, click Save AI settings, then Test connection.\n3. 保存后重新发送原问题；已绑定账号无需重复绑定。 / After saving, send your question again. Confirmed accounts do not need to bind again."

func chatAIError(err error) string {
	if errors.Is(err, ai.ErrDisabled) {
		return chatAIDisabledText
	}
	return err.Error()
}

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
				reply("Approval denied: " + chatAIError(err))
				return
			}
			op, err := assistant.Operation(ctx, opID)
			if err == nil {
				op, err = assistant.Decide(ctx, opID, ai.Decision{RequestID: notification.ID(), ReviewToken: op.ReviewToken, Approve: in.Action == "approve"}, actor)
			}
			if err != nil {
				reply("Approval denied: " + chatAIError(err))
				return
			}
			reply(op.ID + ": " + op.Status)
			return
		}
		if in.Action == "preview" {
			op, err := assistant.Operation(ctx, in.OperationID)
			if err != nil {
				reply(chatAIError(err))
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
				reply(chatAIError(err))
				return
			}
			_ = notify.Reply(ctx, in, message)
			return
		}
		text := strings.TrimSpace(in.Text)
		if text == "/cancel" {
			conversation, err := assistant.CreateConversation(ctx, ai.ConversationInput{}, actor)
			if err != nil {
				reply(chatAIError(err))
				return
			}
			if conversation.CurrentRunID == "" {
				reply("当前没有任务。 / No active task.")
				return
			}
			if _, err = assistant.CancelRun(ctx, conversation.CurrentRunID, actor); err != nil {
				reply(chatAIError(err))
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
				reply(chatAIError(err))
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
				reply(chatAIError(err))
			} else {
				_, _ = notify.ConsumeApproval(ctx, in, b)
				reply("已记录选择，继续任务。 / Selection recorded; resuming the task.")
			}
			return
		}
		if text == "" {
			return
		}
		conversation, err := assistant.CreateConversation(ctx, ai.ConversationInput{}, actor)
		if err != nil {
			reply(chatAIError(err))
			return
		}
		if chatNodeConfirmation(ctx, assistant, conversation.CurrentRun, actor, text, in.ID, reply) {
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
		run, err := assistant.Start(ctx, ai.RunInput{ConversationID: conversation.ID, NodeID: nodeID, Question: text}, actor)
		if err != nil {
			reply(chatAIError(err))
			return
		}
		if !notify.SupportsStreaming(ctx, in.ChannelID) {
			reply("任务已开始 / Task started: " + run.ID)
		}
	}
}

// Natural yes/no replies can confirm only a current node selection. They never
// grant operation approval or apply to an expired/different interaction.
func chatNodeConfirmation(ctx context.Context, assistant *ai.Service, run *ai.Run, actor ai.Actor, text, messageID string, reply func(string)) bool {
	value := strings.ToLower(strings.Trim(strings.TrimSpace(text), "。.!！?？"))
	yes := value == "是" || value == "是的" || value == "对" || value == "对的" || value == "确认" || value == "yes" || value == "y"
	no := value == "不是" || value == "否" || value == "不对" || value == "no" || value == "n"
	if run != nil && run.Status == "waiting_input" && run.Interaction != nil && run.Interaction.Kind != "node" {
		return false
	}
	if run != nil && run.Status == "waiting_input" && run.Interaction != nil && run.Interaction.Multiple && (yes || no) {
		reply("请从列表中明确选择目标节点。 / Select the target nodes from the list.")
		return true
	}
	if run == nil || run.Status != "waiting_input" || run.Interaction == nil || run.Interaction.Kind != "node" || run.Interaction.Multiple {
		if yes || no {
			reply("当前没有待确认的节点，请发送具体查询；变更操作仍须通过完整审批预览确认。 / No node is awaiting confirmation. Send a specific query; changes still require approval of the full preview.")
			return true
		}
		return false
	}
	input := run.Interaction
	if no {
		reply("好的，请在上方选择其他节点，或回复它的完整名称。 / Choose another node above, or reply with its exact name.")
		return true
	}
	selected := []string{}
	for _, option := range input.Options {
		if yes && option.Suggested || !yes && (strings.EqualFold(text, option.Name) || text == option.ID) {
			selected = append(selected, option.ID)
		}
	}
	if len(selected) != 1 {
		if yes {
			reply("请先从候选列表中明确选择一个节点。 / Select one node from the candidates first.")
			return true
		}
		return false
	}
	requestID := notification.ID()
	if messageID != "" {
		hash := sha256.Sum256([]byte(run.ID + ":" + input.ID + ":" + messageID))
		requestID = hex.EncodeToString(hash[:])
	}
	if _, err := assistant.AnswerInput(ctx, run.ID, ai.InputAnswer{InteractionID: input.ID, ExpectedRevision: input.Revision, RequestID: requestID, Values: selected}, actor); err != nil {
		reply(chatAIError(err))
	} else {
		reply("已确认节点，继续任务。 / Node confirmed; resuming the task.")
	}
	return true
}
func chatWorkflowDelivery(notify *notification.Service, assistant *ai.Service) func(context.Context, ai.Actor, ai.WorkflowEvent, ai.Run) error {
	return func(ctx context.Context, actor ai.Actor, entry ai.WorkflowEvent, run ai.Run) error {
		b, err := notify.ValidateBinding(ctx, actor.UserID, actor.BindingID)
		if err != nil {
			return err
		}
		in := notification.Incoming{ChannelID: b.ChannelID, UserID: actor.ExternalUserID, ChatID: actor.ChatID}
		streaming := notify.SupportsStreaming(ctx, in.ChannelID)
		stream := func(text, status string, final, incomplete bool) error {
			return notify.ReplyStream(ctx, in, b, notification.StreamUpdate{Key: run.ID, EventSeq: entry.Seq, Text: text, Status: status, Final: final, Incomplete: incomplete})
		}
		message := notification.Message{}
		switch entry.Type {
		case "run.queued", "run.output", "tool.started", "tool.completed", "task.progress":
			if !streaming {
				return nil
			}
			text, status := "", "正在分析请求… / Analyzing your request…"
			if entry.Type == "run.output" {
				var output ai.StreamOutput
				if json.Unmarshal(entry.Payload, &output) != nil {
					return ai.ErrInvalid
				}
				text, status = output.Text, "正在生成回复… / Generating the reply…"
			} else if entry.Type == "tool.started" {
				status = "正在收集授权节点的数据… / Collecting data from authorized nodes…"
			} else if entry.Type == "tool.completed" {
				status = "正在整理查询结果… / Preparing the result…"
			} else if entry.Type == "task.progress" {
				status = "正在执行已审批的步骤… / Executing the approved step…"
			}
			return stream(text, status, false, false)
		case "run.waiting_input":
			if run.Status != "waiting_input" || run.Interaction == nil {
				return nil
			}
			input := run.Interaction
			if streaming {
				if err := stream("需要补充信息，请在下方选择或回答。 / More information is needed; choose or answer below.", "等待回答 / Awaiting your answer", true, false); err != nil {
					return err
				}
			}
			message.Text = input.Prompt
			suggestions := 0
			for _, option := range input.Options {
				if option.Suggested {
					suggestions++
				}
			}
			if input.Kind == "node" && !input.Multiple && suggestions == 1 {
				message.Text += "\n可回复“是”确认这个节点，回复“不是”选择其他节点。 / Reply yes to confirm this node, or no to choose another."
			}
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
			if streaming {
				if err := stream("当前步骤需要审批，请在下方打开完整预览。 / This step needs approval; open the full preview below.", "等待审批 / Awaiting approval", true, false); err != nil {
					return err
				}
			}
			for _, id := range run.Result.OperationIDs {
				op, err := assistant.Operation(ctx, id)
				if err == nil && op.Status == "awaiting_approval" {
					message.Text += "\n/approve " + op.ID + " → 查看完整预览 / Open full preview"
					message.ApproveID = op.ID
				}
			}
		case "run.completed", "run.failed", "run.paused", "run.canceled":
			message.Text = run.Result.Summary
			if run.Error != "" {
				message.Text += "\n" + run.Error
			}
			if streaming {
				status := "已完成 / Completed"
				if entry.Type == "run.failed" {
					status = "生成失败 / Generation failed"
				} else if entry.Type == "run.paused" {
					status = "已暂停 / Paused"
				} else if entry.Type == "run.canceled" {
					status = "已取消 / Canceled"
				}
				if message.Text == "" {
					message.Text = status
				}
				return stream(message.Text, status, true, run.Error != "" || entry.Type == "run.canceled")
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
