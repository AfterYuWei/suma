package notification

import (
	"strings"

	"github.com/suma/suma/server/internal/event"
)

// One content model feeds each platform's native presentation. Transport and
// provider limits stay in the adapter; event wording stays here.
func buildMessageCard(e event.Event, language, timestamp string) MessageCard {
	t := func(zh, en string) string {
		if language == "en-US" {
			return en
		}
		return zh
	}
	p := present(e, language)
	card := MessageCard{Title: p.Title, Status: p.Status, Fields: []MessageField{}}
	if p.Status == "critical" {
		card.Title = t("紧急 · ", "Urgent · ") + card.Title
	}
	add := func(label, value string) {
		if value != "" {
			card.Fields = append(card.Fields, MessageField{label, shortLine(value, 240)})
		}
	}
	subjectLabel := t("相关资源", "Resource")
	switch {
	case strings.HasPrefix(e.Type, "container."):
		subjectLabel = t("容器", "Container")
		if p.Subject == "" || releaseCommit.MatchString(e.ResourceID) && (p.Subject == compactResource(e.ResourceID) || p.Subject == e.ResourceID) {
			p.Subject = t("名称不可用", "Name unavailable")
		}
	case strings.HasPrefix(e.Type, "image."):
		subjectLabel = t("镜像 / 容器", "Image / container")
	case strings.HasPrefix(e.Type, "cd."), e.Type == "project.completed":
		subjectLabel = t("项目", "Project")
	case strings.HasPrefix(e.Type, "task."):
		subjectLabel = t("任务", "Task")
	case strings.HasPrefix(e.Type, "notification."):
		subjectLabel = t("通知渠道", "Channel")
	case strings.HasPrefix(e.Type, "auth."), e.Type == "account.changed":
		subjectLabel = t("账户", "Account")
	}
	add(subjectLabel, p.Subject)
	if e.NodeName != "" {
		add(t("所属节点", "Node"), e.NodeName)
	} else if e.NodeID != "" {
		add(t("所属节点", "Node"), t("节点名称不可用", "Node name unavailable"))
	}
	if e.Project != p.Subject {
		add(t("Compose 项目", "Compose project"), e.Project)
	}
	add(t("发生时间", "Occurred at"), timestamp)
	reason, advice := p.Summary, ""
	switch e.Type {
	case "container.exited":
		if match := exitCode.FindStringSubmatch(e.Message); len(match) == 2 {
			code := match[1]
			add(t("退出码", "Exit code"), code)
			switch code {
			case "137":
				reason = t("进程被 SIGKILL 强制终止；仅凭退出码无法确定是否内存不足。", "Process terminated by SIGKILL; the exit code alone does not confirm an out-of-memory kill.")
			case "143":
				reason = t("进程收到 SIGTERM 终止信号。", "Process received SIGTERM.")
			case "139":
				reason = t("进程发生段错误（SIGSEGV）。", "Process encountered a segmentation fault (SIGSEGV).")
			case "126":
				reason = t("启动命令无法执行，请检查文件权限及可执行格式。", "Command could not be executed; check permissions and executable format.")
			case "127":
				reason = t("启动命令不存在，请检查镜像及启动配置。", "Command was not found; check the image and startup configuration.")
			default:
				reason = t("容器主进程以非零状态退出。", "The container's main process exited with a non-zero status.")
			}
		} else {
			reason = t("容器主进程异常退出，未取得退出码。", "The container exited unexpectedly; no exit code was available.")
		}
		advice = t("查看容器日志和退出前的资源用量，确认重启策略及依赖服务。", "Review container logs and resource usage before the exit, then check the restart policy and dependencies.")
	case "container.oom":
		reason = t("Docker 检测到容器发生内存不足（OOM）事件。", "Docker reported an out-of-memory (OOM) event for this container.")
		advice = t("检查容器内存上限、节点可用内存和应用内存占用。", "Check the container memory limit, available node memory and application memory usage.")
	case "container.unhealthy":
		reason = t("容器健康检查连续失败，当前状态为 unhealthy。", "Consecutive health checks failed; the container is unhealthy.")
		advice = t("查看健康检查输出，确认探测命令、端口及依赖服务。", "Review health check output, the probe command, ports and dependencies.")
	case "container.restart_loop":
		reason = t("检测到容器短时间内反复重启。", "The container restarted repeatedly within a short period.")
		advice = t("查看最近一次退出原因和容器日志，检查启动配置与资源限制。", "Review the most recent exit and container logs, then check startup settings and resource limits.")
	case "container.recovered":
		if strings.Contains(e.Message, "stably") {
			reason = t("已连续稳定运行至少两分钟。", "Running stably for at least two minutes.")
		} else if strings.Contains(e.Message, "health") {
			reason = t("健康检查已重新通过。", "Health checks are passing again.")
		}
	case "node.offline", "agent.error":
		if reason == "" {
			reason = t("控制端暂时无法连接该节点。", "The control plane cannot currently reach this node.")
		}
		advice = t("检查节点网络、Docker 服务及 Agent 连接状态。", "Check node connectivity, the Docker service and Agent connection.")
	}
	if reason == "" && (p.Status == "error" || p.Status == "critical") {
		reason = shortLine(e.Message, 240)
	}
	add(t("状态说明", "Status details"), reason)
	add(t("建议排查", "Suggested checks"), advice)
	return card
}

func messageStyle(message Message) (icon, color string) {
	status, priority := "info", 0
	ranks := map[string]int{"success": 1, "recovered": 1, "pending": 2, "warning": 3, "error": 4, "critical": 5}
	consider := func(candidate string) {
		if rank := ranks[candidate]; rank > priority {
			status, priority = candidate, rank
		}
	}
	for _, card := range message.Cards {
		consider(card.Status)
	}
	// The highest severity still controls the header when the corresponding
	// event is summarized beyond the visible digest entries.
	for _, e := range message.Events {
		consider(present(e, message.Language).Status)
	}
	switch status {
	case "success", "recovered":
		return "🟢", "green"
	case "pending", "warning":
		return "🟠", "orange"
	case "error", "critical":
		return "🔴", "red"
	default:
		return "ℹ️", "blue"
	}
}
