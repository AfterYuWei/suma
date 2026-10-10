import { ApiError } from '../../lib/api'
import type { Channel, ChannelConfig, ConnectionStatus, Delivery, Inbox, Rule } from './types'
const feishuReception = new Map<string, { connection: ConnectionStatus; chats: { chat_id: string; name: string; private: boolean }[] }>()
export function setDemoFeishuReception(channelID: string, reception: { connection: ConnectionStatus; chats: { chat_id: string; name: string; private: boolean }[] }) { feishuReception.set(channelID, reception) }
let notificationSchemaAvailable = true
export function setDemoNotificationSchemaAvailable(available: boolean) { notificationSchemaAvailable = available }
let serial = 0
const identifier = () => `demo-ops-${++serial}`
const channels: Channel[] = [], rules: Rule[] = [], deliveries: Delivery[] = []
const catalog = {"events": [{"type": "auth.login", "category": "security", "title": "登录成功", "title_en": "Login succeeded"}, {"type": "auth.new_ip", "category": "security", "title": "新的登录 IP", "title_en": "New login IP"}, {"type": "auth.login_failed", "category": "security", "title": "连续登录失败", "title_en": "Repeated login failures"}, {"type": "account.changed", "category": "security", "title": "账户安全变更", "title_en": "Account security changed"}, {"type": "credential.changed", "category": "security", "title": "凭证变更", "title_en": "Credential changed"}, {"type": "node.offline", "category": "nodes", "title": "节点离线", "title_en": "Node offline"}, {"type": "node.recovered", "category": "nodes", "title": "节点恢复", "title_en": "Node recovered"}, {"type": "node.changed", "category": "nodes", "title": "节点配置变更", "title_en": "Node changed"}, {"type": "agent.error", "category": "nodes", "title": "Agent 连接异常", "title_en": "Agent connection error"}, {"type": "tls.expiring", "category": "nodes", "title": "TLS 证书临近到期", "title_en": "TLS certificate expiring"}, {"type": "container.exited", "category": "containers", "title": "容器异常退出", "title_en": "Container exited unexpectedly"}, {"type": "container.oom", "category": "containers", "title": "容器 OOM", "title_en": "Container OOM"}, {"type": "container.unhealthy", "category": "containers", "title": "健康检查失败", "title_en": "Container unhealthy"}, {"type": "container.recovered", "category": "containers", "title": "容器恢复", "title_en": "Container recovered"}, {"type": "container.restart_loop", "category": "containers", "title": "容器频繁重启", "title_en": "Container restart loop"}, {"type": "project.completed", "category": "containers", "title": "项目操作结果", "title_en": "Project operation completed"}, {"type": "image.available", "category": "images", "title": "发现镜像更新", "title_en": "Image update available"}, {"type": "image.check_failed", "category": "images", "title": "镜像检查失败", "title_en": "Image check failed"}, {"type": "image.recreate_required", "category": "images", "title": "镜像更新后需重建", "title_en": "Container recreation required"}, {"type": "image.pull_failed", "category": "images", "title": "镜像拉取失败", "title_en": "Image pull failed"}, {"type": "cd.awaiting_approval", "category": "delivery", "title": "Release 待审核", "title_en": "Release awaiting approval"}, {"type": "cd.completed", "category": "delivery", "title": "发布或回滚结果", "title_en": "Deployment or rollback completed"}, {"type": "cd.drift", "category": "delivery", "title": "发布状态漂移", "title_en": "Deployment drift"}, {"type": "cleanup.completed", "category": "cleanup", "title": "存储清理结果", "title_en": "Storage cleanup result"}, {"type": "cleanup.skipped", "category": "cleanup", "title": "清理被跳过", "title_en": "Cleanup skipped"}, {"type": "task.failed", "category": "tasks", "title": "任务失败", "title_en": "Task failed"}, {"type": "task.canceled", "category": "tasks", "title": "任务取消", "title_en": "Task canceled"}, {"type": "notification.failed", "category": "notifications", "title": "渠道发送失败", "title_en": "Channel delivery failed"}], "presets": {"security": ["auth.login", "auth.new_ip", "auth.login_failed", "account.changed", "credential.changed", "tls.expiring"], "important": ["node.offline", "node.recovered", "agent.error", "container.exited", "container.oom", "container.unhealthy", "container.recovered", "container.restart_loop", "task.failed", "notification.failed"], "images": ["image.available", "image.check_failed", "image.pull_failed", "image.recreate_required"], "delivery": ["cd.awaiting_approval", "cd.completed", "cd.drift"], "cleanup": ["cleanup.completed", "cleanup.skipped"]}}
const inbox: Inbox = { unread: 1, items: [{ id: 'demo-event', type: 'container.oom', severity: 'error', time: new Date().toISOString(), node_id: 'local', node_name: 'Local', resource_id: 'redis-main', title: 'Container OOM', message: 'The container exceeded its memory limit.', task_id: '', read: false }] }
const conflict = () => { throw new ApiError('Configuration changed; reload before saving', 20801, 409) }
export function mockNotifications(path: string, method: string, body: Record<string, unknown>): unknown {
 if (path === '/notifications/catalog') return catalog
 if (path === '/notifications/channels' && method === 'GET') return channels
 if (/^\/notifications\/channels(?:\/[^/]+)?$/.test(path) && (method === 'POST' || method === 'PUT')) {
  if (!notificationSchemaAvailable) throw new ApiError('Database schema is incompatible with this server version', 20802, 503)
  const id = path.split('/')[3], previous = channels.find(c => c.id === id)
  const input = body as unknown as Channel
  if (previous && previous.version !== input.version) conflict()
  if (!['telegram', 'feishu_app', 'webhook'].includes(input.provider)) throw new ApiError('Unsupported notification provider', 20801, 422)
  if (input.provider === 'feishu_app' && channels.some(channel => channel.id !== id && channel.config.app_id === input.config.app_id)) throw new ApiError('One channel per Feishu application', 20801, 422)
  const cfg = input.config as ChannelConfig, secrets = body.secrets as Record<string, unknown> | undefined
  if (previous && previous.config.app_id !== cfg.app_id) cfg.targets = []
  const row: Channel = { id: previous?.id || identifier(), name: input.name, provider: input.provider, enabled: input.enabled, version: (previous?.version || 0) + 1, config: { ...cfg, endpoint: '', auto_discover: input.provider === 'telegram' && !!cfg.auto_discover }, has_secrets: previous?.has_secrets || !!(secrets?.token || cfg.endpoint), last_error: '' }
  if (previous) Object.assign(previous, row); else channels.push(row)
  return row
 }
 if (/^\/notifications\/channels\/[^/]+$/.test(path) && method === 'DELETE') { const index = channels.findIndex(c => c.id === path.split('/')[3]); if (index >= 0) channels.splice(index, 1); return { deleted: true } }
 if (/\/notifications\/channels\/[^/]+\/(test|check)$/.test(path)) {
  const channel = channels.find(item => item.id === path.split('/')[3])
  if (!channel) throw new ApiError('Channel not found', 20801, 404)
  if (path.endsWith('/check')) return { credentials_valid: true }
  const chatID = String(body.chat_id || '')
  if (channel.provider === 'feishu_app' && !channel.config.targets?.some(target => target.chat_id === chatID)) throw new ApiError('Select a configured test recipient', 20801, 422)
  deliveries.unshift({ id: identifier(), channel_id: channel.id, chat_id: chatID, status: 'sent', reason: '', attempts: 1, due_at: new Date().toISOString(), created_at: new Date().toISOString() })
  return { sent: true, chat_id: chatID }
 }
 if (/\/notifications\/channels\/[^/]+\/connection$/.test(path)) { const id = path.split('/')[3], channel = channels.find(item => item.id === id); return feishuReception.get(id)?.connection || (channel?.enabled ? { state: 'connected', message_count: 2, last_message_at: new Date().toISOString(), last_message_result: 'discovered' } : { state: 'stopped' }) }
 if (/\/notifications\/channels\/[^/]+\/chats$/.test(path)) {
  const reception = feishuReception.get(path.split('/')[3]); if (reception) return reception.chats
  const channel = channels.find(item => item.id === path.split('/')[3])
  return channel?.provider === 'feishu_app' && channel.enabled ? [{ chat_id: 'oc_demo_private', name: 'Admin', private: true }, { chat_id: 'oc_demo_group', name: 'Operations', private: false }] : channel?.provider === 'telegram' && channel.enabled && channel.config.auto_discover ? [{ chat_id: '123456', name: 'Admin', private: true }] : []
 }
 if (path === '/notifications/rules' && method === 'GET') return rules
 if (/^\/notifications\/rules(?:\/[^/]+)?$/.test(path) && (method === 'PUT' || method === 'POST')) { const id = path.split('/')[3], input = body as unknown as Rule, previous = rules.find(r => r.id === id); if (previous && previous.version !== input.version) conflict(); const row: Rule = { id: previous?.id || identifier(), name: input.name, config: input.config, enabled: input.enabled, version: (previous?.version || 0) + 1 }; if (previous) Object.assign(previous, row); else rules.push(row); return row }
 if (path.startsWith('/notifications/rules/') && method === 'DELETE') { const index = rules.findIndex(r => r.id === path.split('/')[3]); if (index >= 0) rules.splice(index, 1); return {} }
 if (path === '/notifications/deliveries') return deliveries
 if (path.endsWith('/resend')) return { queued: true }
 if (path === '/notifications/inbox') return inbox
 if (/\/notifications\/inbox\/[^/]+\/read$/.test(path)) { const item = inbox.items.find(e => e.id === path.split('/')[3]); if (item && !item.read) { item.read = true; inbox.unread-- } return { read: true } }
 return undefined
}
const storageKey = 'suma-demo-notifications-v1'
export function persistMockNotifications() { if (typeof sessionStorage !== 'undefined') sessionStorage.setItem(storageKey, JSON.stringify({ serial, channels, rules, deliveries, inbox })) }
if (typeof sessionStorage !== 'undefined') {
 try {
  const saved = JSON.parse(sessionStorage.getItem(storageKey) || 'null') as { serial: number; channels: Channel[]; rules: Rule[]; deliveries: Delivery[]; inbox: Inbox } | null
  if (saved) { serial = saved.serial; channels.push(...saved.channels); rules.push(...saved.rules); deliveries.push(...saved.deliveries); Object.assign(inbox, saved.inbox) }
 } catch { sessionStorage.removeItem(storageKey) }
}
