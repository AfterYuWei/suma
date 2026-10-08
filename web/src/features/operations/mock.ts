import { ApiError } from '../../lib/api'
import type { AIConversationDetail, AIOperation, AIRun, AISettings, Binding, Channel, ChannelConfig, Delivery, Inbox, Rule } from './types'
let serial = 0
const identifier = () => `demo-ops-${++serial}`
let settings: AISettings = { version: 0, enabled: false, protocol: 'responses', endpoint: 'https://api.openai.com/v1', model: '', models: [], allow_private: false, allow_insecure: false, node_ids: [], auto_events: [], max_concurrent: 2, daily_auto_limit: 20, max_tool_calls: 8, max_iterations: 40, max_operations: 20, log_lines: 500, log_bytes: 65536, approval_minutes: 15, has_secret: false, tool_capable: false }
const conversations: AIConversationDetail[] = []
const channels: Channel[] = [], rules: Rule[] = [], deliveries: Delivery[] = [], runs: AIRun[] = [], operations: AIOperation[] = [], bindings: Binding[] = [], audits: Record<string, unknown>[] = []
const catalog = {"events": [{"type": "auth.login", "category": "security", "title": "登录成功", "title_en": "Login succeeded"}, {"type": "auth.new_ip", "category": "security", "title": "新的登录 IP", "title_en": "New login IP"}, {"type": "auth.login_failed", "category": "security", "title": "连续登录失败", "title_en": "Repeated login failures"}, {"type": "account.changed", "category": "security", "title": "账户安全变更", "title_en": "Account security changed"}, {"type": "credential.changed", "category": "security", "title": "凭证变更", "title_en": "Credential changed"}, {"type": "node.offline", "category": "nodes", "title": "节点离线", "title_en": "Node offline"}, {"type": "node.recovered", "category": "nodes", "title": "节点恢复", "title_en": "Node recovered"}, {"type": "node.changed", "category": "nodes", "title": "节点配置变更", "title_en": "Node changed"}, {"type": "agent.error", "category": "nodes", "title": "Agent 连接异常", "title_en": "Agent connection error"}, {"type": "tls.expiring", "category": "nodes", "title": "TLS 证书临近到期", "title_en": "TLS certificate expiring"}, {"type": "container.exited", "category": "containers", "title": "容器异常退出", "title_en": "Container exited unexpectedly"}, {"type": "container.oom", "category": "containers", "title": "容器 OOM", "title_en": "Container OOM"}, {"type": "container.unhealthy", "category": "containers", "title": "健康检查失败", "title_en": "Container unhealthy"}, {"type": "container.recovered", "category": "containers", "title": "容器恢复", "title_en": "Container recovered"}, {"type": "container.restart_loop", "category": "containers", "title": "容器频繁重启", "title_en": "Container restart loop"}, {"type": "project.completed", "category": "containers", "title": "项目操作结果", "title_en": "Project operation completed"}, {"type": "image.available", "category": "images", "title": "发现镜像更新", "title_en": "Image update available"}, {"type": "image.check_failed", "category": "images", "title": "镜像检查失败", "title_en": "Image check failed"}, {"type": "image.recreate_required", "category": "images", "title": "镜像更新后需重建", "title_en": "Container recreation required"}, {"type": "image.pull_failed", "category": "images", "title": "镜像拉取失败", "title_en": "Image pull failed"}, {"type": "cd.awaiting_approval", "category": "delivery", "title": "Release 待审核", "title_en": "Release awaiting approval"}, {"type": "cd.completed", "category": "delivery", "title": "发布或回滚结果", "title_en": "Deployment or rollback completed"}, {"type": "cd.drift", "category": "delivery", "title": "发布状态漂移", "title_en": "Deployment drift"}, {"type": "cleanup.completed", "category": "cleanup", "title": "存储清理结果", "title_en": "Storage cleanup result"}, {"type": "cleanup.skipped", "category": "cleanup", "title": "清理被跳过", "title_en": "Cleanup skipped"}, {"type": "task.failed", "category": "tasks", "title": "任务失败", "title_en": "Task failed"}, {"type": "task.canceled", "category": "tasks", "title": "任务取消", "title_en": "Task canceled"}, {"type": "ai.diagnosed", "category": "ai", "title": "AI 诊断完成", "title_en": "AI diagnosis completed"}, {"type": "ai.awaiting_approval", "category": "ai", "title": "AI 操作待审核", "title_en": "AI operation awaiting approval"}, {"type": "ai.completed", "category": "ai", "title": "AI 操作结果", "title_en": "AI operation completed"}, {"type": "ai.expired", "category": "ai", "title": "AI 审批过期", "title_en": "AI approval expired"}, {"type": "ai.unavailable", "category": "ai", "title": "模型不可用", "title_en": "Model unavailable"}, {"type": "ai.budget", "category": "ai", "title": "AI 用量上限", "title_en": "AI usage limit reached"}, {"type": "notification.failed", "category": "notifications", "title": "渠道发送失败", "title_en": "Channel delivery failed"}], "presets": {"security": ["auth.login", "auth.new_ip", "auth.login_failed", "account.changed", "credential.changed", "tls.expiring"], "important": ["node.offline", "node.recovered", "agent.error", "container.exited", "container.oom", "container.unhealthy", "container.recovered", "container.restart_loop", "task.failed", "ai.awaiting_approval", "ai.expired", "notification.failed"], "images": ["image.available", "image.check_failed", "image.pull_failed", "image.recreate_required"], "delivery": ["cd.awaiting_approval", "cd.completed", "cd.drift", "ai.completed"], "cleanup": ["cleanup.completed", "cleanup.skipped"]}}
const inbox: Inbox = { unread: 1, items: [{ id: 'demo-event', type: 'container.oom', severity: 'error', time: new Date().toISOString(), node_id: 'local', node_name: 'Local', resource_id: 'redis-main', title: 'Container OOM', message: 'The container exceeded its memory limit.', task_id: '', run_id: '', operation_id: '', read: false }] }
const conflict = () => { throw new ApiError('Preview expired or changed; reload the operation', 20801, 409) }
export function mockOperations(path: string, method: string, body: Record<string, unknown>): unknown {
 if (path === '/notifications/catalog') return catalog
 if (path === '/notifications/channels' && method === 'GET') return channels
 if (/^\/notifications\/channels(?:\/[^/]+)?$/.test(path) && (method === 'POST' || method === 'PUT')) {
  const id = path.split('/')[3], previous = channels.find(c => c.id === id)
  const input = body as unknown as Channel
  if (previous && previous.version !== input.version) conflict()
  if (!['telegram', 'feishu_app', 'webhook'].includes(input.provider)) throw new ApiError('Unsupported notification provider', 20801, 422)
  if (input.provider === 'feishu_app' && channels.some(channel => channel.id !== id && channel.config.app_id === input.config.app_id)) throw new ApiError('One channel per Feishu application', 20801, 422)
  const cfg = input.config as ChannelConfig, secrets = body.secrets as Record<string, unknown> | undefined
  if (previous && previous.config.app_id !== cfg.app_id) cfg.targets = []
  const row: Channel = { id: previous?.id || identifier(), name: input.name, provider: input.provider, enabled: input.enabled, version: (previous?.version || 0) + 1, config: { ...cfg, endpoint: '' }, has_secrets: previous?.has_secrets || !!(secrets?.token || cfg.endpoint), last_error: '' }
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
 if (/\/notifications\/channels\/[^/]+\/connection$/.test(path)) { const channel = channels.find(item => item.id === path.split('/')[3]); return { state: channel?.enabled ? 'connected' : 'stopped' } }
 if (/\/notifications\/channels\/[^/]+\/chats$/.test(path)) {
  const channel = channels.find(item => item.id === path.split('/')[3])
  return channel?.provider === 'feishu_app' && channel.enabled ? [{ chat_id: 'oc_demo_private', name: 'Admin', private: true }, { chat_id: 'oc_demo_group', name: 'Operations', private: false }] : []
 }
 if (path === '/notifications/rules' && method === 'GET') return rules
 if (/^\/notifications\/rules(?:\/[^/]+)?$/.test(path) && (method === 'PUT' || method === 'POST')) { const id = path.split('/')[3], input = body as unknown as Rule, previous = rules.find(r => r.id === id); if (previous && previous.version !== input.version) conflict(); const row: Rule = { id: previous?.id || identifier(), name: input.name, config: input.config, enabled: input.enabled, version: (previous?.version || 0) + 1 }; if (previous) Object.assign(previous, row); else rules.push(row); return row }
 if (path.startsWith('/notifications/rules/') && method === 'DELETE') { const index = rules.findIndex(r => r.id === path.split('/')[3]); if (index >= 0) rules.splice(index, 1); return {} }
 if (path === '/notifications/deliveries') return deliveries
 if (path.endsWith('/resend')) return { queued: true }
 if (path === '/notifications/inbox') return inbox
 if (/\/notifications\/inbox\/[^/]+\/read$/.test(path)) { const item = inbox.items.find(e => e.id === path.split('/')[3]); if (item && !item.read) { item.read = true; inbox.unread-- } return { read: true } }
 if (path === '/ai/settings' && method === 'GET') return settings
 if (path === '/ai/settings' && method === 'PUT') {
  const input = body as unknown as AISettings
  if (input.protocol !== 'responses') throw new ApiError('Only Responses protocol is supported', 20801, 422)
  if (settings.version !== input.version) conflict()
  const changed = Boolean(body.api_key) || (['protocol', 'endpoint', 'model', 'allow_private', 'allow_insecure'] as const).some(key => settings[key] !== input[key])
  settings = { ...input, version: input.version + 1, has_secret: !!body.api_key || settings.has_secret, tool_capable: changed ? false : settings.tool_capable }
  delete (settings as unknown as Record<string, unknown>).api_key
  if (!settings.enabled) operations.forEach(o => { if (o.status === 'awaiting_approval') o.status = 'invalidated' })
  return settings
 }
 if (path === '/ai/settings/test') { settings.tool_capable = true; return { text: true, tool_capable: true, summary_only: false, model: settings.model, duration_ms: 250, text_response: 'Connection confirmed.' } }
 if (path === '/ai/settings/models' && method === 'POST') { if (body.version !== settings.version) conflict(); return { models: ['deepseek-v4-flash', 'deepseek-v4-pro', 'deepseek-v4-flash-vision-exp'] } }
 if (path === '/ai/conversations' && method === 'GET') return conversations.slice().reverse()
 if (path === '/ai/conversations' && method === 'POST') {
  const now = new Date().toISOString(), row: AIConversationDetail = { id: identifier(), title: String(body.title || ''), source: 'site', current_run_id: '', revision: 1, event_seq: 0, created_at: now, updated_at: now, context: {}, messages: [], runs: [] }; conversations.push(row); return row
 }
 if (/^\/ai\/conversations\/[^/]+$/.test(path)) { const row = conversations.find(c => c.id === path.split('/')[3]); if (row) { row.runs = runs.filter(r => r.conversation_id === row.id).slice().reverse(); row.current_run = runs.find(r => r.id === row.current_run_id) }; return row }
 if (/^\/ai\/conversations\/[^/]+\/messages$/.test(path) && method === 'POST') {
  const conv = conversations.find(c => c.id === path.split('/')[3]); if (!conv || !settings.enabled) throw new ApiError('Conversation unavailable or AI disabled', 20801, 403)
  const model = String(body.model || settings.model); if (!settings.models.includes(model)) throw new ApiError('Model not configured', 20801, 422)
  if (body.expected_revision && conv.revision !== body.expected_revision) conflict()
  const old = runs.find(r => r.id === conv.current_run_id); if (old) { operations.filter(o => o.run_id === old.id && o.status === 'awaiting_approval').forEach(o => { o.status = 'invalidated' }); if (!['completed', 'failed', 'canceled'].includes(old.status)) old.status = 'canceled' }
  const context = body.context as AIConversationDetail['context'] | undefined, target = context?.resource_node_id || conv.context.node_ids?.[0] || ''
  if (target && !settings.node_ids.includes(target)) throw new ApiError('Node unauthorized', 20801, 403)
  const now = new Date().toISOString(), run: AIRun = { id: identifier(), conversation_id: conv.id, revision: 2, phase: 'target', target_node_ids: target ? [target] : [], target_source: context?.resource_id ? 'resource' : target ? 'conversation' : '', node_id: target, model, question: String(body.question), status: target ? 'waiting_approval' : 'waiting_input', error: '', tokens: 240, created_at: now, steps: [], result: { summary: '', evidence: [], operation_ids: [], missing: [] } }
  if (target) demoPropose(run, context?.resource_id || 'redis-main'); else run.interaction = { id: identifier(), run_id: run.id, revision: run.revision, kind: 'node', prompt: '需要在哪个节点执行这项任务？ / Which node should this task use?', multiple: false, status: 'pending', expires_at: new Date(Date.now() + 86400000).toISOString(), options: settings.node_ids.map(id => ({ id, name: id, node_id: id, kind: 'node' })) }
  conv.revision++; conv.event_seq++; conv.current_run_id = run.id; conv.title ||= run.question; conv.updated_at = now; conv.context = context || conv.context; conv.messages.push({ id: identifier(), run_id: run.id, role: 'user', content: run.question, created_at: now }); runs.unshift(run); return run
 }
 if (/^\/ai\/runs\/[^/]+\/inputs$/.test(path) && method === 'POST') { const run = runs.find(r => r.id === path.split('/')[3]), values = body.values as string[]; if (!run || run.status !== 'waiting_input' || run.interaction?.id !== body.interaction_id || run.revision !== body.expected_revision || values.length !== 1 || !settings.node_ids.includes(values[0])) conflict(); const row = run!; row.node_id = values[0]; row.target_node_ids = values; row.target_source = 'selection'; row.status = 'waiting_approval'; row.interaction = undefined; row.revision++; demoPropose(row, 'redis-main'); const conv = conversations.find(c => c.id === row.conversation_id)!; conv.context.node_ids = values; return row }
 if (/^\/ai\/runs\/[^/]+\/cancel$/.test(path) && method === 'POST') { const run = runs.find(r => r.id === path.split('/')[3]); if (run) { run.status = 'canceled'; run.interaction = undefined; operations.filter(o => o.run_id === run.id && o.status === 'awaiting_approval').forEach(o => { o.status = 'invalidated' }) }; return run }
 if (/^\/ai\/runs\/[^/]+$/.test(path)) return runs.find(r => r.id === path.split('/')[3])
 if (path === '/ai/operations') return operations
 if (/^\/ai\/operations\/[^/]+$/.test(path)) return operations.find(o => o.id === path.split('/')[3])
 if (/^\/ai\/operations\/[^/]+\/decision$/.test(path)) { const op = operations.find(o => o.id === path.split('/')[3]); if (!op || op.status !== 'awaiting_approval' || op.review_token !== body.review_token || !settings.enabled) conflict(); const row = op!; row.status = body.approve ? 'completed' : 'rejected'; row.result = body.approve ? 'Completed: container restart verified' : 'Rejected'; row.approval_source = 'site'; const run = runs.find(r => r.id === row.run_id); if (run) { run.status = body.approve ? 'completed' : 'paused'; run.steps.forEach(step => { if (step.operation_id === row.id) step.status = row.status }); run.result.summary = row.result }; row.verification = { satisfied: !!body.approve, summary: row.result, evidence: [] }; audits.unshift({ id: ++serial, action: body.approve ? 'approved' : 'rejected', source: 'site', resource: row.resource_id, result: row.status, operation_id: row.id, user_id: 1, created_at: new Date().toISOString() }); return row }
 if (path === '/ai/audit') return audits.map(a => ({ ...a, id: 100000 + Number(a.id) }))
 if (path === '/notification-bindings' && method === 'GET') return bindings
 if (path === '/notification-bindings' && method === 'POST') { const row: Binding = { id: identifier(), channel_id: String(body.channel_id), status: 'pending', chat_id: '', external_name: '', external_user_id: '', expires_at: new Date(Date.now() + 600000).toISOString() }; bindings.push(row); return { binding: row, code: identifier() } }
 if (path.startsWith('/notification-bindings/')) return {}
 return undefined
}
const storageKey = 'suma-demo-operations-v2'
export function persistMockOperations() { if (typeof sessionStorage !== 'undefined') sessionStorage.setItem(storageKey, JSON.stringify({ serial, settings, channels, rules, deliveries, conversations, runs, operations, bindings, audits, inbox })) }
if (typeof sessionStorage !== 'undefined') {
 try { const saved = JSON.parse(sessionStorage.getItem(storageKey) || 'null') as { serial: number; settings: AISettings; channels: Channel[]; rules: Rule[]; deliveries: Delivery[]; conversations: AIConversationDetail[]; runs: AIRun[]; operations: AIOperation[]; bindings: Binding[]; audits: Record<string, unknown>[]; inbox: Inbox } | null; if (saved) { serial = saved.serial; settings = saved.settings; channels.push(...saved.channels); rules.push(...saved.rules); deliveries.push(...saved.deliveries); conversations.push(...saved.conversations); runs.push(...saved.runs); operations.push(...saved.operations); bindings.push(...saved.bindings); audits.push(...saved.audits); Object.assign(inbox, saved.inbox) } } catch { sessionStorage.removeItem(storageKey) }
}

export function mockAIAuditLogs() { return audits.map(a => { const op = operations.find(o => o.id === a.operation_id); return { id: 100000 + Number(a.id), scope: op ? 'node' : 'control_plane', node_id: op?.node_id || '', node_name: op ? 'homelab-01' : '', user_id: a.user_id, source: a.source, action: `ai.${String(a.action)}`, resource_type: 'ai', resource_name: a.resource, result: a.result === 'completed' ? 'success' : 'denied', details: a.result, run_id: op?.run_id, operation_id: a.operation_id, task_id: op?.task_id, ip: '', created_at: a.created_at } }) }

function demoPropose(run: AIRun, resource: string) {
 const now = new Date().toISOString(), op: AIOperation = { id: identifier(), run_id: run.id, node_id: run.node_id, action: 'container.restart', resource_id: resource, title: `Restart ${resource}`, impact: 'Interrupts service. Volumes are preserved. Recovery requires a new approval.', parameters: {}, snapshot: { details: { state: 'running', image_id: 'sha256:demo-image', restart_count: 5 }, fingerprint: 'frozen-demo-state' }, review_token: identifier(), status: 'awaiting_approval', task_id: '', result: '', expires_at: new Date(Date.now() + 900000).toISOString(), approval_source: '', confirmations: [] }
 operations.unshift(op); run.phase = 'review'; run.steps = [{ id: identifier(), position: 1, title: op.title, node_id: op.node_id, action: op.action, resource_id: resource, status: 'awaiting_approval', operation_id: op.id, expected: 'Container running with a new start time' }]; run.result = { summary: 'Evidence suggests the container exceeded its memory limit. Review this restart before execution.', evidence: [{ node_id: run.node_id, source: 'read_status', resource, time: now, content: 'State: restarting; OOMKilled: true; PASSWORD=[redacted]', unavailable: false }], operation_ids: [op.id], missing: [] }
 inbox.items.unshift({ id: identifier(), type: 'ai.awaiting_approval', severity: 'warning', time: now, node_id: op.node_id, node_name: op.node_id, resource_id: resource, title: op.title, message: op.impact, run_id: run.id, operation_id: op.id, task_id: '', read: false }); inbox.unread++
}
