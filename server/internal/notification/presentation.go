package notification

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/event"
	"github.com/suma/suma/server/internal/redact"
)

func renderMessage(snapshot DeliverySnapshot, rows []database.NotificationEvent) Message {
	zone, err := time.LoadLocation(snapshot.Policy.Timezone)
	if err != nil {
		zone = time.UTC
	}
	message := Message{Language: snapshot.Language, Default: snapshot.Policy.Template == ""}
	blocks := []string{}
	for _, row := range rows {
		var e event.Event
		if json.Unmarshal([]byte(row.DataJSON), &e) != nil {
			continue
		}
		message.Events = append(message.Events, e)
		if message.Default && len(blocks) >= 8 {
			continue
		}
		link := strings.TrimRight(snapshot.PublicURL, "/") + "/notifications#" + e.ID
		if message.Default {
			blocks = append(blocks, defaultEventText(e, snapshot.Language, e.Time.In(zone).Format("2006-01-02 15:04")))
			message.Cards = append(message.Cards, buildMessageCard(e, snapshot.Language, e.Time.In(zone).Format("2006-01-02 15:04 -07:00")))
		} else {
			// Substitute once: placeholder-shaped resource/error text is data.
			body := strings.NewReplacer("{{title}}", e.Title, "{{message}}", e.Message, "{{node}}", e.NodeName, "{{resource}}", e.ResourceID, "{{time}}", e.Time.In(zone).Format("2006-01-02 15:04:05 -07:00"), "{{url}}", link).Replace(snapshot.Policy.Template)
			blocks = append(blocks, body)
		}
		if snapshot.PublicURL != "" {
			message.URL = link
		}
	}
	if message.Default && len(message.Events) > 1 {
		message.Title = fmt.Sprintf("通知摘要 · %d 条", len(message.Events))
		if snapshot.Language == "en-US" {
			message.Title = fmt.Sprintf("Notifications · %d events", len(message.Events))
		}
		if len(message.Events) > len(blocks) {
			more := fmt.Sprintf("另有 %d 条，请在站内消息查看", len(message.Events)-len(blocks))
			if snapshot.Language == "en-US" {
				more = fmt.Sprintf("%d more events in your inbox", len(message.Events)-len(blocks))
			}
			blocks = append(blocks, more)
			message.More = more
		}
		if snapshot.PublicURL != "" {
			message.URL = strings.TrimRight(snapshot.PublicURL, "/") + "/notifications"
		}
		blocks = append([]string{message.Title}, blocks...)
	} else if message.Default && len(blocks) > 0 {
		message.Title = strings.SplitN(blocks[0], "\n", 2)[0]
	}
	message.Text = redact.Bounded(strings.Join(blocks, "\n\n"), 12000)
	return message
}

type Presentation struct {
	Title   string `json:"title"`
	Subject string `json:"subject,omitempty"`
	Summary string `json:"summary,omitempty"`
	Status  string `json:"status"`
}

var exitCode = regexp.MustCompile(`(?:exit=|exit code |exited with code )(-?\d+)`)
var releaseCommit = regexp.MustCompile(`\b[0-9a-f]{40,64}\b`)

func shortLine(value string, limit int) string {
	value = strings.Join(strings.Fields(redact.Bounded(value, 4000)), " ")
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit-1]) + "…"
	}
	return value
}

func present(e event.Event, language string) Presentation {
	en := language == "en-US"
	t := func(zh, english string) string {
		if en {
			return english
		}
		return zh
	}
	p := Presentation{Status: "info"}
	for _, entry := range Catalog {
		if entry.Type == e.Type {
			p.Title = t(entry.Title, entry.TitleEN)
			break
		}
	}
	failed := e.Severity == "error" || e.Severity == "critical"
	if e.Severity == "warning" || failed {
		p.Status = "warning"
	}
	if failed {
		p.Status = "error"
	}
	if e.Severity == "critical" {
		p.Status = "critical"
	}
	switch e.Type {
	case "node.offline":
		p.Title = t("节点离线", "Node offline")
	case "node.recovered":
		p.Title, p.Status = t("节点已恢复", "Node recovered"), "recovered"
	case "node.changed":
		p.Title = t("节点配置已更新", "Node configuration updated")
	case "agent.error":
		p.Title = t("Agent 连接异常", "Agent connection failed")
		if strings.Contains(strings.ToLower(e.Title), "incompatible") {
			p.Title = t("Agent 版本不兼容", "Agent version incompatible")
		}
	case "tls.expiring":
		p.Title = t("TLS 证书即将到期", "TLS certificate expiring")
		if len(e.Message) >= 10 && e.Message[4] == '-' && e.Message[7] == '-' {
			p.Summary = t("到期：", "Expires: ") + e.Message[:10]
		}
	case "tls.recovered":
		p.Title, p.Status = t("TLS 证书已更新", "TLS certificate renewed"), "recovered"
	case "container.exited":
		p.Title = t("容器异常退出", "Container exited unexpectedly")
		if match := exitCode.FindStringSubmatch(e.Message); len(match) == 2 {
			p.Summary = t("退出码 ", "Exit code ") + match[1]
		}
	case "container.oom":
		p.Title = t("容器内存不足", "Container out of memory")
	case "container.unhealthy":
		p.Title = t("容器健康检查失败", "Container health check failed")
	case "container.restart_loop":
		p.Title = t("容器频繁重启", "Container restarting repeatedly")
	case "container.recovered":
		p.Title, p.Status = t("容器已恢复", "Container recovered"), "recovered"
		if strings.Contains(e.Message, "health") {
			p.Title = t("健康检查已恢复", "Health check recovered")
		}
		if strings.Contains(e.Message, "stably") {
			p.Title = t("容器运行已稳定", "Container running stably")
		}
	case "image.available":
		p.Title = t("镜像有更新", "Image update available")
	case "image.recreate_required":
		p.Title = t("容器待重建", "Container recreation needed")
	case "image.check_recovered":
		p.Title, p.Status = t("镜像检查已恢复", "Image check recovered"), "recovered"
	case "cd.awaiting_approval":
		p.Title, p.Status = t("发布待审核", "Release awaiting approval"), "pending"
		if commit := releaseCommit.FindString(e.Message); commit != "" {
			p.Summary = t("版本 ", "Commit ") + commit[:7]
		}
	case "cd.completed":
		if failed {
			p.Title = t("发布失败", "Deployment failed")
		} else {
			p.Title, p.Status = t("发布完成", "Deployment completed"), "success"
		}
		if strings.Contains(e.Title, "rollback") {
			if failed {
				p.Title = t("回滚失败", "Rollback failed")
			} else {
				p.Title = t("回滚完成", "Rollback completed")
			}
		}
		if strings.HasPrefix(e.Title, "cd.sync") {
			if failed {
				p.Title = t("代码同步失败", "Git sync failed")
			} else {
				p.Title = t("代码同步完成", "Git sync completed")
			}
		}
		if strings.HasPrefix(e.Title, "cd.approve") {
			if failed {
				p.Title = t("发布审核失败", "Release review failed")
			} else {
				p.Title = t("发布已批准", "Release approved")
			}
		}
		if strings.HasPrefix(e.Title, "cd.reject") {
			if failed {
				p.Title = t("发布审核失败", "Release review failed")
			} else {
				p.Title, p.Status = t("发布已拒绝", "Release rejected"), "canceled"
			}
		}
	case "cd.drift":
		p.Title = t("发布状态不一致", "Deployment drift detected")
	case "cd.recovered":
		p.Title, p.Status = t("发布状态已恢复", "Deployment recovered"), "recovered"
	case "project.completed":
		if failed {
			p.Title = t("项目操作失败", "Project operation failed")
		} else if e.Severity == "warning" {
			p.Title, p.Status = t("项目操作已取消", "Project operation canceled"), "canceled"
		} else {
			p.Title, p.Status = t("项目操作完成", "Project operation completed"), "success"
		}
	case "cleanup.completed":
		if failed {
			p.Title = t("清理失败", "Cleanup failed")
		} else {
			p.Title, p.Status = t("清理完成", "Cleanup completed"), "success"
		}
		if strings.HasPrefix(e.Message, "partial_failed") {
			p.Title = t("清理部分失败", "Cleanup partially failed")
		}
	case "cleanup.skipped":
		p.Title, p.Status = t("清理已跳过", "Cleanup skipped"), "skipped"
	case "task.canceled":
		p.Title, p.Status = t("任务已取消", "Task canceled"), "canceled"
	case "notification.failed":
		p.Title = t("通知发送失败", "Notification delivery failed")
	case "notification.recovered":
		p.Title, p.Status = t("通知发送已恢复", "Notification delivery recovered"), "recovered"
	case "auth.login", "auth.new_ip", "auth.login_failed":
		if e.Type == "auth.login" && !failed {
			p.Status = "success"
		}
		for _, word := range strings.Fields(e.Message) {
			if ip, err := netip.ParseAddr(word); err == nil {
				p.Summary = "IP " + ip.String()
				break
			}
		}
	}
	// Resource names are more useful than full Engine/task identifiers. Only
	// known resource events use their original title as a resource name.
	switch {
	case strings.HasPrefix(e.Type, "container."), strings.HasPrefix(e.Type, "image."):
		p.Subject = strings.TrimPrefix(e.Title, "/")
	case strings.HasPrefix(e.Type, "cd."):
		p.Subject = e.Project
		if p.Subject == "" && (e.ResourceType == "project" || e.ResourceType == "delivery_project") {
			p.Subject = e.ResourceID
		}
		if p.Subject == "" && !strings.HasPrefix(e.Title, "cd.") {
			p.Subject = e.Title
		}
	case strings.HasPrefix(e.Type, "task."), e.Type == "project.completed":
		p.Subject = e.Title
	case strings.HasPrefix(e.Type, "notification."):
		if !strings.Contains(e.Title, "Channel delivery failed") {
			p.Subject = e.Title
		}
	case e.Type == "credential.changed":
		p.Subject = compactResource(e.ResourceID)
	case strings.HasPrefix(e.Type, "auth."), e.Type == "account.changed":
		if e.ActorID != nil {
			p.Subject = fmt.Sprintf(t("用户 #%d", "User #%d"), *e.ActorID)
		}
	}
	if p.Subject == p.Title {
		p.Subject = ""
	}
	// Old/demo events sometimes used an event label where a resource name
	// belongs. Recognize both languages before falling back to the identifier.
	for _, label := range map[string][]string{
		"container.oom":           {"容器内存不足", "Container out of memory"},
		"container.unhealthy":     {"容器健康检查失败", "Container health check failed"},
		"container.restart_loop":  {"容器频繁重启", "Container restarting repeatedly"},
		"container.recovered":     {"容器已恢复", "Container recovered"},
		"image.available":         {"镜像有更新", "Image update available"},
		"image.recreate_required": {"容器待重建", "Container recreation needed"},
		"notification.failed":     {"通知发送失败", "Notification delivery failed"},
		"notification.recovered":  {"通知发送已恢复", "Notification delivery recovered"},
	}[e.Type] {
		if p.Subject == label {
			p.Subject = ""
			break
		}
	}
	for _, entry := range Catalog {
		if entry.Type == e.Type && (p.Subject == entry.Title || p.Subject == entry.TitleEN) {
			p.Subject = ""
			break
		}
	}
	if p.Subject == "" && (strings.HasPrefix(e.Type, "container.") || strings.HasPrefix(e.Type, "image.") || strings.HasPrefix(e.Type, "notification.")) {
		p.Subject = compactResource(e.ResourceID)
	}
	if p.Subject == p.Title || p.Subject == e.NodeName && !strings.HasPrefix(e.Type, "container.") && !strings.HasPrefix(e.Type, "image.") {
		p.Subject = ""
	}
	p.Subject = shortLine(p.Subject, 80)
	if p.Title == "" {
		p.Title = shortLine(e.Title, 100)
		p.Summary = shortLine(e.Message, 100)
	}
	if p.Title == "" {
		p.Title = t("系统通知", "System notification")
	}
	if p.Summary == "" && (failed || e.Type == "image.check_failed" || e.Type == "agent.error" || e.Type == "cd.drift") {
		p.Summary = conciseReason(e.Message, en)
	}
	return p
}

func compactResource(value string) string {
	if strings.HasPrefix(value, "sha256:") {
		return shortLine(value, 19)
	}
	if utf8.RuneCountInString(value) >= 32 && !strings.ContainsAny(value, "/.: ") {
		return shortLine(value, 13)
	}
	return shortLine(value, 80)
}

func conciseReason(message string, en bool) string {
	value := strings.ToLower(message)
	for _, reason := range []struct {
		matches []string
		zh, en  string
	}{
		{[]string{"authentication_required", "unauthorized", "authentication failed", "http 401"}, "认证失败", "Authentication failed"},
		{[]string{"permission denied", "forbidden", "http 403"}, "权限不足", "Permission denied"},
		{[]string{"reference_not_found"}, "镜像不存在", "Image reference not found"},
		{[]string{"rate_limited", "http 429"}, "请求受限", "Rate limited"},
		{[]string{"platform_unavailable"}, "没有匹配的系统架构", "No matching platform"},
		{[]string{"unsupported_manifest"}, "镜像格式不支持", "Unsupported image format"},
		{[]string{"invalid_reference"}, "镜像地址无效", "Invalid image reference"},
		{[]string{"timeout", "timed out", "deadline exceeded"}, "连接超时", "Connection timed out"},
		{[]string{"registry_unreachable", "registry_unavailable"}, "镜像仓库不可达", "Registry unreachable"},
		{[]string{"connection refused", "provider connection failed"}, "连接失败", "Connection failed"},
		{[]string{"desired_commit_mismatch"}, "运行版本与期望不一致", "Running revision differs from desired"},
		{[]string{"no_active_release"}, "尚无运行中的发布", "No active release"},
		{[]string{"runtime_unhealthy"}, "服务未正常运行", "Service is not healthy"},
	} {
		for _, match := range reason.matches {
			if strings.Contains(value, match) {
				if en {
					return reason.en
				}
				return reason.zh
			}
		}
	}
	return ""
}

func defaultEventText(e event.Event, language, timestamp string) string {
	p := present(e, language)
	lines := []string{p.Title}
	context := []string{}
	if e.NodeName != "" {
		context = append(context, shortLine(e.NodeName, 48))
	} else if e.NodeID != "" {
		context = append(context, compactResource(e.NodeID))
	}
	if p.Subject != "" && (len(context) == 0 || context[0] != p.Subject) {
		context = append(context, p.Subject)
	}
	if timestamp != "" {
		context = append(context, timestamp)
	}
	if len(context) > 0 {
		lines = append(lines, strings.Join(context, " · "))
	}
	if p.Summary != "" {
		lines = append(lines, p.Summary)
	}
	if e.Severity == "critical" {
		lines[0] = fmt.Sprintf("%s · %s", map[bool]string{true: "Urgent", false: "紧急"}[language == "en-US"], lines[0])
	}
	return strings.Join(lines, "\n")
}
