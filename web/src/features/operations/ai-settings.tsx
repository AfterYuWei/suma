import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Button } from '../../components/ui/button'
import { Input } from '../../components/ui/input'
import { api } from '../../lib/api'
import type { DockerNode } from '../../lib/nodes'
import { Check, Choice, ErrorText, Field } from './common'
import { toggle, useOpsText } from './helpers'
import type { AISettings as Settings, Binding, Channel } from './types'
export function AISettings() {
 const t = useOpsText()
 const query = useQuery({ queryKey: ['ai-settings'], queryFn: () => api<Settings>('/ai/settings') })
 return <div className="space-y-7"><p className="text-sm text-muted-foreground">{t('内置助手只读取授权节点的脱敏信息。每项变更均需独立审批；关闭后待执行操作失效，运行中的任务请求取消。', 'The assistant reads redacted evidence on authorized nodes. Every change requires individual approval. Disabling AI invalidates pending operations and requests cancellation of running tasks.')}</p><ErrorText error={query.error} />{query.data && <SettingsForm key={query.data.version} initial={query.data} />}<Bindings /></div>
}
function SettingsForm({ initial }: { initial: Settings }) {
 const t = useOpsText(), client = useQueryClient(), [cfg, setCfg] = useState(initial), [key, setKey] = useState('')
 const update = <K extends keyof Settings>(name: K, value: Settings[K]) => setCfg(prev => ({ ...prev, [name]: value }))
 const nodes = useQuery({ queryKey: ['nodes'], queryFn: () => api<DockerNode[]>('/nodes') })
 const save = useMutation({ mutationFn: () => api<Settings>('/ai/settings', { method: 'PUT', body: JSON.stringify({ ...cfg, api_key: key }) }), onSuccess: result => { setKey(''); client.setQueryData(['ai-settings'], result) } })
 const test = useMutation({ mutationFn: () => api<{ text: boolean; tool_capable: boolean }>('/ai/settings/test', { method: 'POST' }), onSuccess: () => { void client.invalidateQueries({ queryKey: ['ai-settings'] }) } })
 return <form className="max-w-2xl space-y-5" onSubmit={e => { e.preventDefault(); save.mutate() }}>
 <Check label={t('启用 AI 运维', 'Enable AI operations')} checked={cfg.enabled} onChange={v => update('enabled', v)} />
 <div className="grid gap-4 sm:grid-cols-2"><Choice label={t('模型协议', 'Model protocol')} value={cfg.protocol} options={[[ 'responses', 'Responses' ], [ 'chat_completions', 'Chat Completions' ]]} onChange={v => update('protocol', v)} /><Field label={t('模型名称', 'Model name')}><Input value={cfg.model} onChange={e => update('model', e.target.value)} /></Field></div>
 <Field label={t('服务地址', 'Service URL')} hint={t('填写 API 基础地址，如 https://api.openai.com/v1。', 'Enter the API base URL, e.g. https://api.openai.com/v1.')}><Input value={cfg.endpoint} onChange={e => update('endpoint', e.target.value)} /></Field>
 <Field label={t('API 密钥', 'API key')} hint={cfg.has_secret ? t('已配置，留空保留。', 'Configured; leave blank to retain.') : undefined}><Input type="password" autoComplete="new-password" value={key} onChange={e => setKey(e.target.value)} /></Field>
 <Check label={t('允许内部模型服务地址', 'Allow an internal model endpoint')} checked={cfg.allow_private} onChange={v => update('allow_private', v)} />
 <Field label={t('授权节点（必须明确选择）', 'Authorized nodes (select explicitly)')} hint={t('同时限制聊天查询范围；非白名单账号只能看到这些节点的安全状态和数量摘要。', 'Also limits chat queries; identities outside the allowlist see safe status and counts for these nodes only.')}><div className="space-y-2">{nodes.data?.map(n => <Check key={n.id} label={n.name} checked={cfg.node_ids.includes(n.id)} onChange={() => update('node_ids', toggle(cfg.node_ids, n.id))} />)}</div></Field>
 <details className="space-y-4"><summary className="cursor-pointer text-sm font-medium">{t('自动分析和用量限制', 'Automatic diagnosis and limits')}</summary>
 <Field label={t('自动分析事件（默认手动）', 'Automatic events (manual by default)')}><div className="space-y-2">{[['container.exited', t('容器异常退出', 'Unexpected exit')], ['container.oom', 'OOM'], ['container.unhealthy', t('健康检查失败', 'Unhealthy')], ['container.restart_loop', t('频繁重启', 'Restart loop')], ['node.offline', t('节点离线', 'Node offline')], ['task.failed', t('任务失败', 'Task failure')], ['image.check_failed', t('镜像检查失败', 'Image check failure')], ['cd.completed', t('发布结果', 'Delivery result')], ['cleanup.completed', t('清理结果', 'Cleanup result')]].map(([id, name]) => <Check key={id} label={name} checked={cfg.auto_events.includes(id)} onChange={() => update('auto_events', toggle(cfg.auto_events, id))} />)}</div></Field>
 <div className="grid gap-4 sm:grid-cols-2">{([['max_concurrent', t('并发诊断数', 'Concurrent diagnoses'), 1, 8], ['daily_auto_limit', t('每日自动诊断次数', 'Automatic diagnoses per day'), 1, 1000], ['max_tool_calls', t('每次只读工具上限', 'Read tool calls per diagnosis'), 1, 32], ['log_lines', t('日志行数（最近十五分钟）', 'Log lines (last 15 minutes)'), 1, 500], ['log_bytes', t('日志字节上限', 'Log byte limit'), 1024, 65536], ['approval_minutes', t('审批有效期（分钟）', 'Approval expiry (minutes)'), 1, 60]] as const).map(([name, label, min, max]) => <Field key={name} label={label}><Input type="number" min={min} max={max} value={cfg[name]} onChange={e => update(name, Number(e.target.value))} /></Field>)}</div>
 </details>
 <ErrorText error={save.error || test.error} />
 <div className="flex flex-wrap items-center gap-3"><Button type="submit" disabled={save.isPending || (cfg.enabled && !cfg.node_ids.length)}>{t('保存 AI 设置', 'Save AI settings')}</Button><Button type="button" variant="outline" disabled={test.isPending || !initial.model} onClick={() => test.mutate()}>{t('测试已保存的连接', 'Test saved connection')}</Button><span className="text-xs text-muted-foreground">{test.data ? test.data.tool_capable ? t('文本和工具调用通过', 'Text and tool calling verified') : t('仅支持摘要', 'Summary only') : initial.tool_capable ? t('工具调用可用', 'Tool calling available') : t('尚未验证工具调用', 'Tool calling not verified')}</span></div>
 </form>
}
function Bindings() {
 const t = useOpsText(), client = useQueryClient(), [channel, setChannel] = useState(''), [code, setCode] = useState('')
 const channels = useQuery({ queryKey: ['notification-channels'], queryFn: () => api<Channel[]>('/notifications/channels') })
 const bindings = useQuery({ queryKey: ['notification-bindings'], queryFn: () => api<Binding[]>('/notification-bindings'), refetchInterval: 3000 })
 const action = useMutation({ mutationFn: ({ path, method, body }: { path: string; method: string; body?: unknown }) => api<{ code?: string }>(path, { method, body: body ? JSON.stringify(body) : undefined }), onSuccess: data => { if (data?.code) setCode(data.code); void client.invalidateQueries({ queryKey: ['notification-bindings'] }) } })
 return <section className="max-w-2xl space-y-4 border-t pt-5"><h3 className="text-sm font-medium">{t('聊天账号绑定与操作白名单', 'Chat identity binding & operator allowlist')}</h3><p className="text-sm text-muted-foreground">{t('生成十分钟有效的一次性码 → 私聊机器人提交 /bind 绑定码 → 返回此处确认稳定平台 ID，加入操作白名单。白名单内的变更仍需逐项审核；撤销后立即恢复只读查询。', 'Generate a one-time code (10 minutes) → send /bind CODE privately → confirm the stable platform ID here to join the operator allowlist. Each change still requires review; revocation immediately restores read-only access.')}</p><p className="text-sm text-muted-foreground">{t('非白名单账号只能查询授权节点的状态和数量摘要，不能访问密钥、凭证、日志、配置、环境变量、任务输出、审批预览或变更操作。', 'Other identities can query status and counts on authorized nodes only. Keys, credentials, logs, configuration, environment values, task output, approval previews and changes are inaccessible.')}</p>
 <Choice label={t('聊天渠道', 'Chat channel')} value={channel || 'none'} options={[[ 'none', t('请选择', 'Select') ], ...(channels.data || []).filter(c => c.enabled && c.config.interactive).map(c => [c.id, c.name] as [string, string])]} onChange={v => setChannel(v === 'none' ? '' : v)} />
 <Button variant="outline" disabled={!channel || action.isPending} onClick={() => action.mutate({ path: '/notification-bindings', method: 'POST', body: { channel_id: channel } })}>{t('生成绑定码', 'Generate binding code')}</Button>{code && <div className="break-all rounded-md border p-3 font-mono text-sm" role="status">/bind {code}</div>}<ErrorText error={action.error || bindings.error} />
 <div className="divide-y border-y">{bindings.data?.filter(b => b.status !== 'revoked').map(b => <div key={b.id} className="flex flex-wrap items-center gap-3 py-3 text-sm"><div className="min-w-0 flex-1"><span>{channels.data?.find(c => c.id === b.channel_id)?.name}</span><p className="break-all text-xs text-muted-foreground">{b.external_user_id || t('等待私聊提交', 'Awaiting private message')} · {b.status === 'active' ? t('操作白名单', 'Operator allowlist') : b.status}</p></div>{b.status === 'claimed' && <Button size="sm" disabled={action.isPending} onClick={() => action.mutate({ path: `/notification-bindings/${b.id}/confirm`, method: 'POST' })}>{t('确认并加入操作白名单', 'Confirm and allow operations')}</Button>}<Button size="sm" variant="ghost" disabled={action.isPending} onClick={() => action.mutate({ path: `/notification-bindings/${b.id}`, method: 'DELETE' })}>{t('撤销', 'Revoke')}</Button></div>)}</div>
 </section>
}
