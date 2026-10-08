package notification

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/event"
)

var ErrConflict = errors.New("configuration changed; reload before saving")
var ErrInvalid = errors.New("invalid notification configuration")

type Config struct {
	Endpoint     string   `json:"endpoint,omitempty"`
	ChatID       string   `json:"chat_id,omitempty"`
	Targets      []Target `json:"targets,omitempty"`
	AppID        string   `json:"app_id,omitempty"`
	Language     string   `json:"language"`
	Timezone     string   `json:"timezone"`
	AllowPrivate bool     `json:"allow_private"`
	Interactive  bool     `json:"interactive"`
	PublicURL    string   `json:"public_url,omitempty"`
}
type Target struct {
	ChatID string `json:"chat_id"`
	Name   string `json:"name"`
}
type ConnectionStatus struct {
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}
type Secrets struct {
	Endpoint      string `json:"endpoint,omitempty"`
	Token         string `json:"token,omitempty"`
	SigningKey    string `json:"signing_key,omitempty"`
	Authorization string `json:"authorization,omitempty"`
}
type ChannelInput struct {
	Name     string   `json:"name"`
	Provider string   `json:"provider"`
	Enabled  bool     `json:"enabled"`
	Version  uint64   `json:"version"`
	Config   Config   `json:"config"`
	Secrets  *Secrets `json:"secrets,omitempty"`
}
type Channel struct {
	database.NotificationChannel
	Config     Config `json:"config"`
	HasSecrets bool   `json:"has_secrets"`
}
type RuleConfig struct {
	Events         []string            `json:"events"`
	Severities     []string            `json:"severities"`
	NodeIDs        []string            `json:"node_ids"`
	GroupIDs       []uint              `json:"group_ids"`
	Projects       []string            `json:"projects"`
	ChannelIDs     []string            `json:"channel_ids"`
	ChannelTargets map[string][]string `json:"channel_targets,omitempty"`
	FallbackID     string              `json:"fallback_id,omitempty"`
	FallbackChatID string              `json:"fallback_chat_id,omitempty"`
	Mode           string              `json:"mode"`
	Timezone       string              `json:"timezone"`
	DigestHour     int                 `json:"digest_hour"`
	QuietStart     string              `json:"quiet_start,omitempty"`
	QuietEnd       string              `json:"quiet_end,omitempty"`
	MutedUntil     *time.Time          `json:"muted_until,omitempty"`
	Recovery       bool                `json:"recovery"`
	Template       string              `json:"template,omitempty"`
}
type RuleInput struct {
	Name    string     `json:"name"`
	Enabled bool       `json:"enabled"`
	Version uint64     `json:"version"`
	Config  RuleConfig `json:"config"`
}
type Rule struct {
	database.NotificationRule
	Config RuleConfig `json:"config"`
}
type InboxEvent struct {
	event.Event
	Read bool `json:"read"`
}
type Inbox struct {
	Items  []InboxEvent `json:"items"`
	Unread int64        `json:"unread"`
}
type CatalogEntry struct {
	Type     string `json:"type"`
	Category string `json:"category"`
	Title    string `json:"title"`
	TitleEN  string `json:"title_en"`
}
type Incoming struct {
	ID          string
	ChannelID   string
	UserID      string
	Name        string
	ChatID      string
	Private     bool
	Text        string
	Action      string
	OperationID string
}
type ChatHandler func(Incoming, database.NotificationBinding)
type InteractionChoice struct {
	Label string
	Token string
}
type Message struct {
	Choices       []InteractionChoice
	Text          string
	ApproveID     string
	OperationID   string
	ApprovalToken string
	URL           string
	Events        []event.Event
}

var Catalog = []CatalogEntry{
	{"auth.login", "security", "登录成功", "Login succeeded"}, {"auth.new_ip", "security", "新的登录 IP", "New login IP"}, {"auth.login_failed", "security", "连续登录失败", "Repeated login failures"}, {"account.changed", "security", "账户安全变更", "Account security changed"}, {"credential.changed", "security", "凭证变更", "Credential changed"},
	{"node.offline", "nodes", "节点离线", "Node offline"}, {"node.recovered", "nodes", "节点恢复", "Node recovered"}, {"node.changed", "nodes", "节点配置变更", "Node changed"}, {"agent.error", "nodes", "Agent 连接异常", "Agent connection error"}, {"tls.expiring", "nodes", "TLS 证书临近到期", "TLS certificate expiring"},
	{"container.exited", "containers", "容器异常退出", "Container exited unexpectedly"}, {"container.oom", "containers", "容器 OOM", "Container OOM"}, {"container.unhealthy", "containers", "健康检查失败", "Container unhealthy"}, {"container.recovered", "containers", "容器恢复", "Container recovered"}, {"container.restart_loop", "containers", "容器频繁重启", "Container restart loop"}, {"project.completed", "containers", "项目操作结果", "Project operation completed"},
	{"image.available", "images", "发现镜像更新", "Image update available"}, {"image.check_failed", "images", "镜像检查失败", "Image check failed"}, {"image.recreate_required", "images", "镜像更新后需重建", "Container recreation required"}, {"image.pull_failed", "images", "镜像拉取失败", "Image pull failed"},
	{"cd.awaiting_approval", "delivery", "Release 待审核", "Release awaiting approval"}, {"cd.completed", "delivery", "发布或回滚结果", "Deployment or rollback completed"}, {"cd.drift", "delivery", "发布状态漂移", "Deployment drift"},
	{"cleanup.completed", "cleanup", "存储清理结果", "Storage cleanup result"}, {"cleanup.skipped", "cleanup", "清理被跳过", "Cleanup skipped"}, {"task.failed", "tasks", "任务失败", "Task failed"}, {"task.canceled", "tasks", "任务取消", "Task canceled"},
	{"ai.diagnosed", "ai", "AI 诊断完成", "AI diagnosis completed"}, {"ai.awaiting_approval", "ai", "AI 操作待审核", "AI operation awaiting approval"}, {"ai.completed", "ai", "AI 操作结果", "AI operation completed"}, {"ai.expired", "ai", "AI 审批过期", "AI approval expired"}, {"ai.unavailable", "ai", "模型不可用", "Model unavailable"}, {"ai.budget", "ai", "AI 用量上限", "AI usage limit reached"}, {"notification.failed", "notifications", "渠道发送失败", "Channel delivery failed"},
}
var Presets = map[string][]string{
	"security":  {"auth.login", "auth.new_ip", "auth.login_failed", "account.changed", "credential.changed", "tls.expiring"},
	"important": {"node.offline", "node.recovered", "agent.error", "container.exited", "container.oom", "container.unhealthy", "container.recovered", "container.restart_loop", "task.failed", "ai.awaiting_approval", "ai.expired", "notification.failed"},
	"images":    {"image.available", "image.check_failed", "image.pull_failed", "image.recreate_required"},
	"delivery":  {"cd.awaiting_approval", "cd.completed", "cd.drift", "ai.completed"},
	"cleanup":   {"cleanup.completed", "cleanup.skipped"},
}

func ID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func encode(v any) string { b, _ := json.Marshal(v); return string(b) }
func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value || item == "*" {
			return true
		}
	}
	return false
}
