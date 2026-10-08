package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/suma/suma/server/internal/ai"
	"github.com/suma/suma/server/internal/notification"
)

func (r aiRuntime) Query(ctx context.Context, nodeID string) (ai.QuerySummary, error) {
	var out ai.QuerySummary
	node, err := r.nodes.Get(ctx, nodeID)
	if err != nil || !node.Enabled {
		return out, ai.ErrScope
	}
	out.Available = node.Status == "online"
	out.MissingContainers, out.MissingImages, out.MissingCleanup = true, true, true
	runtime, err := r.nodes.Runtime(ctx, nodeID)
	if err == nil {
		out.Available = true
		if containers, err := runtime.List(ctx); err == nil {
			out.MissingContainers = false
			out.Containers.Total = len(containers)
			for _, container := range containers {
				switch container.State {
				case "running":
					out.Containers.Running++
				case "exited", "created", "dead":
					out.Containers.Stopped++
				case "restarting":
					out.Containers.Restarting++
				}
				if strings.Contains(container.Status, "unhealthy") {
					out.Containers.Unhealthy++
				}
			}
		}
		if images, err := runtime.ListImages(ctx); err == nil {
			out.MissingImages = false
			out.Images.Total = len(images)
		}
		if r.imageUpdates != nil {
			if updates, err := r.imageUpdates.View(ctx, nodeID, ""); err == nil {
				for _, update := range updates.Results {
					if update.Status == "update_available" {
						out.Images.Updates++
					}
					if update.RecreateRequired {
						out.Images.Recreate++
					}
					if update.Status == "unchecked" || update.Status == "checking" || update.Status == "unavailable" {
						out.Images.Unchecked++
					}
					if update.Stale {
						out.Images.Stale++
					}
				}
			}
		}
	} else {
		out.Available = false
	}
	if r.cleanup != nil {
		if view, err := r.cleanup.Get(ctx, nodeID, false); err == nil {
			out.MissingCleanup = false
			if view.LatestRun != nil {
				out.Cleanup.HasResult = true
				for _, stats := range view.LatestRun.Result.Stats {
					if stats == nil {
						continue
					}
					out.Cleanup.Deleted += max(0, stats.Deleted)
					out.Cleanup.Skipped += max(0, stats.Skipped)
					out.Cleanup.Failed += max(0, stats.Failed)
					if stats.ReclaimedBytes != nil {
						out.Cleanup.ReclaimedBytes += *stats.ReclaimedBytes
					}
				}
			}
		}
	}
	return out, nil
}

func chatQuery(ctx context.Context, notify *notification.Service, assistant *ai.Service, in notification.Incoming) {
	reply := func(text string) { _ = notify.Reply(ctx, in, notification.UnboundMessage(text)) }
	if in.Action != "" {
		reply("不在操作白名单中，只能查询安全状态摘要。 / Read-only access: operation previews and approvals are unavailable.")
		return
	}
	out, err := assistant.QueryTextAs(ctx, in.Text, ai.Actor{Source: "chat", ExternalUserID: in.UserID, ChatID: in.ChatID})
	if errors.Is(err, ai.ErrQueryTarget) {
		reply("请明确一个已启用且已授权的节点名称或 ID，例如“查询 节点名称 的状态”或 /node NODE_ID status。名称不唯一时请使用节点 ID。 / Specify one enabled, authorized node by name or ID, for example: show NODE_NAME status or /node NODE_ID status. Use its ID if the name is ambiguous.")
		return
	}
	if err != nil {
		reply("当前无法查询该授权节点的安全摘要。 / Safe status is unavailable for this authorized node.")
		return
	}
	availability := "不可用 / unavailable"
	if out.Available {
		availability = "可用 / available"
	}
	answer := fmt.Sprintf("只读安全摘要 / Read-only safe status\n节点 / Node: %s\n容器 / Containers: %d · 运行 / running %d · 停止 / stopped %d · 重启 / restarting %d · 不健康 / unhealthy %d\n镜像 / Images: %d · 已检测更新 / detected updates %d · 待重建 / pending rebuild %d · 未检查 / unchecked %d · 过期 / stale %d", availability, out.Containers.Total, out.Containers.Running, out.Containers.Stopped, out.Containers.Restarting, out.Containers.Unhealthy, out.Images.Total, out.Images.Updates, out.Images.Recreate, out.Images.Unchecked, out.Images.Stale)
	if out.Cleanup.HasResult {
		answer += fmt.Sprintf("\n最近清理 / Latest cleanup: 删除 / deleted %d · 跳过 / skipped %d · 失败 / failed %d · 回收 / reclaimed %d bytes", out.Cleanup.Deleted, out.Cleanup.Skipped, out.Cleanup.Failed, out.Cleanup.ReclaimedBytes)
	}
	if out.MissingContainers || out.MissingImages || out.MissingCleanup {
		answer += "\n部分数据不可用，不能推断缺失数据。 / Partial data unavailable; missing data is not evidence."
	}
	reply(answer)
}
