import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Button } from '../../components/ui/button'
import { Input } from '../../components/ui/input'
import { api } from '../../lib/api'
import type { DockerNode, NodeGroup } from '../../lib/nodes'
import { Check, Choice, ErrorText, Field } from './common'
import { toggle, useOpsText } from './helpers'
import type { Catalog, Rule, RuleConfig } from './types'

const newRule = (): RuleConfig => ({ events: [], severities: [], node_ids: [], group_ids: [], projects: [] })
export function RuleEditor({ row, onDone, onCancel }: { row?: Rule; onDone: (rule: Rule) => void; onCancel?: () => void }) {
 const t = useOpsText(), client = useQueryClient()
 const [preset, setPreset] = useState('custom'), [name, setName] = useState(row?.name || '')
 const [cfg, setCfg] = useState<RuleConfig>(() => ({
  ...newRule(), ...row?.config,
  severities: row?.config.severities || [], node_ids: row?.config.node_ids || [], group_ids: row?.config.group_ids || [], projects: row?.config.projects || [],
 }))
 const update = <K extends keyof RuleConfig>(key: K, value: RuleConfig[K]) => setCfg(prev => ({ ...prev, [key]: value }))
 const catalog = useQuery({ queryKey: ['notification-catalog'], queryFn: () => api<Catalog>('/notifications/catalog') })
 const nodes = useQuery({ queryKey: ['nodes'], queryFn: () => api<DockerNode[]>('/nodes') }), groups = useQuery({ queryKey: ['node-groups'], queryFn: () => api<NodeGroup[]>('/node-groups') })
 const save = useMutation({ mutationFn: () => api<Rule>(`/notifications/rules${row ? `/${row.id}` : ''}`, { method: row ? 'PUT' : 'POST', body: JSON.stringify({ name, version: row?.version || 0, config: cfg }) }), onSuccess: rule => { void client.invalidateQueries({ queryKey: ['notification-rules'] }); onDone(rule) } })
 return <form className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden" onSubmit={e => { e.preventDefault(); save.mutate() }}>
 <fieldset disabled={save.isPending} className="min-h-0 min-w-0 flex-1 space-y-4 overflow-y-auto p-5">
 <Field label={t('规则名称', 'Rule name')}><Input value={name} onChange={e => setName(e.target.value)} required /></Field>
 <Choice label={t('使用预设', 'Use preset')} value={preset} options={[[ 'custom', t('自定义', 'Custom') ], ['security', t('账户安全', 'Account security')], ['important', t('重要异常', 'Important incidents')], ['images', t('镜像更新', 'Image updates')], ['delivery', t('发布动态', 'Delivery updates')], ['cleanup', t('清理报告', 'Cleanup reports')]]} onChange={key => { setPreset(key); if (catalog.data?.presets[key]) { update('events', catalog.data.presets[key]); if (!name) setName(key) } }} />
 <details open><summary className="mb-2 cursor-pointer text-sm font-medium select-none">{t('事件', 'Events')} · {cfg.events.length}</summary><p className="mb-3 text-xs text-muted-foreground">{t('事件是 SUMA 检测到的变化，例如容器 OOM 或节点离线。勾选决定哪些变化触发通知，消息正文由模板控制。', 'Events are changes detected by SUMA, such as container OOM or a node going offline. Select which changes trigger notifications; templates control their text.')}</p><div className="grid gap-2 sm:grid-cols-2">{catalog.data?.events.map(e => <Check key={e.type} label={t(e.title, e.title_en)} checked={cfg.events.includes(e.type)} onChange={() => update('events', toggle(cfg.events, e.type))} />)}</div></details>
 <details><summary className="cursor-pointer py-1 text-sm select-none">{t('筛选范围（可选）', 'Filter scope (optional)')}</summary><div className="pt-3">
 <section aria-label={t('规则范围', 'Rule scope')} className="space-y-3"><h3 className="text-sm font-medium">{t('规则范围', 'Rule scope')}</h3><p className="text-xs text-muted-foreground">{t('在已选事件中，进一步筛选严重程度、节点和项目。不选表示不限范围；节点与节点组取并集，再与其他筛选条件同时匹配。', 'Filter selected events by severity, nodes and projects. Empty selections impose no limit. Nodes and groups combine as a union, then must also match the other filters.')}</p>
 <Field label={t('严重程度（不选为全部）', 'Severity (none = all)')}><div className="flex flex-wrap gap-3">{['info', 'warning', 'error', 'critical'].map(v => <Check key={v} label={v} checked={cfg.severities.includes(v)} onChange={() => update('severities', toggle(cfg.severities, v))} />)}</div></Field>
 <Field label={t('节点（不选为全部）', 'Nodes (none = all)')}><div className="space-y-2">{nodes.data?.map(n => <Check key={n.id} label={n.name} checked={cfg.node_ids.includes(n.id)} onChange={() => update('node_ids', toggle(cfg.node_ids, n.id))} />)}</div></Field>
 <Field label={t('节点组（与节点范围取并集）', 'Node groups (combined with selected nodes)')}><div className="space-y-2">{groups.data?.map(g => <Check key={g.id} label={g.name} checked={cfg.group_ids.includes(g.id)} onChange={() => update('group_ids', cfg.group_ids.includes(g.id) ? cfg.group_ids.filter(id => id !== g.id) : [...cfg.group_ids, g.id])} />)}</div></Field>
 <Field label={t('项目名称（逗号分隔）', 'Project names (comma separated)')}><Input value={cfg.projects.join(',')} onChange={e => update('projects', e.target.value.split(',').map(v => v.trim()).filter(Boolean))} /></Field>
 </section>
 </div></details>
 <p className="text-xs text-muted-foreground">{t('渠道或对话勾选并保存规则后即生效。频率、静默和模板在通知订阅中设置。修改规则或取消订阅只影响新事件，已排队消息继续按原配置发送。', 'Rules take effect when selected and saved in a channel or conversation subscription. Set frequency, quiet hours and templates there. Rule changes and unsubscribing affect new events; queued messages retain their original configuration.')}</p>
 <ErrorText error={save.error || catalog.error || nodes.error || groups.error} />
 </fieldset><div className="flex shrink-0 items-center justify-end gap-2 bg-surface px-5 py-3">{onCancel && <Button type="button" variant="outline" onClick={onCancel} disabled={save.isPending}>{t('返回订阅', 'Back to subscriptions')}</Button>}<Button type="submit" disabled={save.isPending || !name.trim() || !cfg.events.length}>{t('保存规则', 'Save rule')}</Button></div>
 </form>
}
