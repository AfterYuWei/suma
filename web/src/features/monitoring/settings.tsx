import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useEffect, useState } from 'react'
import { Plus, Save, X } from 'lucide-react'
import { Button } from '../../components/ui/button'
import { Input } from '../../components/ui/input'
import { Checkbox } from '../../components/ui/checkbox'
import { Spinner } from '../../components/ui/spinner'
import { api } from '../../lib/api'
import { useI18n } from '../../lib/i18n'
import { useDateTime } from '../../lib/time-zone'
import { confirmDialog } from '../../stores/dialog'
import { MonitoringSelect } from './view'
import type { Exclusion, Policy, Status } from './types'

export function MonitoringSettings() {
  const { language } = useI18n(), zh = language === 'zh-CN', { formatDateTime } = useDateTime()
  const client = useQueryClient()
  const settings = useQuery({ queryKey: ['monitoring-settings'], queryFn: ({ signal }) => api<Policy>('/monitoring/settings', { signal }) })
  const status = useQuery({ queryKey: ['monitoring-status'], queryFn: ({ signal }) => api<Status>('/monitoring/status', { signal }), refetchInterval: 5000 })
  const nodes = useQuery({ queryKey: ['nodes'], queryFn: ({ signal }) => api<{ id: string; name: string }[]>('/nodes', { signal }) })
  const [draft, setDraft] = useState<Policy>(), [rule, setRule] = useState<Exclusion>({ kind: 'node', node_id: '' }), [error, setError] = useState('')
  useEffect(() => { if (settings.data && !draft) setDraft(settings.data) }, [settings.data, draft])
  const save = useMutation({
    mutationFn: (input: Policy & { confirm_retention_reduction: boolean }) => api<{ settings: Policy; task_id: string }>('/monitoring/settings', { method: 'PUT', body: JSON.stringify(input) }),
    onSuccess: result => { setDraft(result.settings); client.setQueryData(['monitoring-settings'], result.settings); void client.invalidateQueries({ queryKey: ['monitoring-status'] }); void client.invalidateQueries({ queryKey: ['monitoring-query'] }) },
  })
  if (settings.isError) return <p className="text-destructive">{settings.error.message}</p>
  if (!draft) return <Spinner />
  const update = (patch: Partial<Policy>) => setDraft({ ...draft, ...patch })
  const submit = async () => {
    let confirmed = false
    if (draft.retention_days < (settings.data?.retention_days ?? draft.retention_days)) {
      const now = Date.now()
      confirmed = await confirmDialog({ title: zh ? '缩短监控历史保存期限？' : 'Reduce monitoring history retention?', description: (zh ? '将永久清理监控指标和事件：' : 'Permanently remove monitoring metrics and events: ') + formatDateTime(new Date(now - (settings.data?.retention_days ?? 3) * 86400_000).toISOString()) + ' – ' + formatDateTime(new Date(now - draft.retention_days * 86400_000).toISOString()) + (zh ? '。清理任务启动后无法恢复。' : '. Cleanup cannot be undone.'), danger: true, confirmLabel: zh ? '确认保存并清理' : 'Save and clean history' })
      if (!confirmed) return
    }
    save.mutate({ ...draft, confirm_retention_reduction: confirmed })
  }
  const addRule = () => {
    if (!rule.node_id || rule.kind === 'container' && !/^[a-f0-9]{64}$/.test(rule.container_id ?? '') || rule.kind === 'compose' && !/^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$/.test(rule.project ?? '') || rule.service && !/^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$/.test(rule.service)) { setError(zh ? '请选择节点，并填写完整容器 ID 或有效的 Compose 项目／服务名。' : 'Choose a node and enter a full container ID or valid Compose project/service.'); return }
    if (draft.exclusions.length >= 200) { setError(zh ? '最多允许 200 条排除规则。' : 'Up to 200 exclusion rules are allowed.'); return }
    if (!draft.exclusions.some(r => JSON.stringify(r) === JSON.stringify(rule))) update({ exclusions: [...draft.exclusions, rule] })
    setRule({ kind: 'node', node_id: '' }); setError('')
  }
  const effectiveCount = status.data?.active_containers ?? 0
  return <div className="space-y-8">
    <section className="space-y-4"><h3 className="text-sm font-medium">{zh ? '容器监控' : 'Container monitoring'}</h3><label className="flex items-center gap-2 text-sm"><Checkbox checked={draft.enabled} onCheckedChange={v => update({ enabled: v === true })} />{zh ? '启用后台监控记录' : 'Enable background monitoring'}</label><p className="text-xs text-muted-foreground">{zh ? '无人访问时也持续记录。关闭采集后，历史仍按保存期限到期清理。' : 'Records continuously without visitors. Existing history still expires when collection is disabled.'}</p>
      <div className="flex flex-wrap gap-6"><div className="space-y-2"><p className="text-sm">{zh ? '记录频率' : 'Recording interval'}</p><MonitoringSelect label={zh ? '记录频率' : 'Recording interval'} value={String(draft.interval_seconds)} options={[5, 10, 15, 30, 60].map(n => [String(n), n + (zh ? ' 秒' : ' seconds')])} onChange={v => update({ interval_seconds: Number(v) })} /></div><div className="space-y-2"><p className="text-sm">{zh ? '指标与事件保存期限' : 'Metric and event retention'}</p><MonitoringSelect label={zh ? '保存期限' : 'Retention'} value={String(draft.retention_days)} options={[1, 3, 7, 14, 30].map(n => [String(n), n + (zh ? ' 天' : ' days')])} onChange={v => update({ retention_days: Number(v) })} /></div></div>
      <p className="text-xs text-muted-foreground">{zh ? '按当前采集数量预计' : 'Estimated at the current container count'}: {Math.floor(effectiveCount * 86400 / draft.interval_seconds).toLocaleString()} {zh ? '条／天' : 'records/day'} · {Math.floor(effectiveCount * 86400 / draft.interval_seconds * draft.retention_days).toLocaleString()} {zh ? '条保留记录' : 'retained records'}</p>
    </section>
    <section className="space-y-4"><h3 className="text-sm font-medium">{zh ? '排除规则' : 'Exclusions'}</h3><p className="text-xs text-muted-foreground">{zh ? '规则命中即停止新增采集。实例规则只匹配当前 ID；Compose 项目／服务规则对重建实例继续生效。' : 'Matching resources stop recording. Instance rules match one ID; Compose rules also match rebuilt instances.'}</p>
      <div className="flex flex-wrap gap-2"><MonitoringSelect label={zh ? '排除类型' : 'Exclusion kind'} value={rule.kind} options={[[ 'node', zh ? '整个节点' : 'Node'], ['container', zh ? '容器实例' : 'Container instance'], ['compose', zh ? 'Compose 项目／服务' : 'Compose project/service']]} onChange={v => setRule({ kind: v as Exclusion['kind'], node_id: rule.node_id })} /><MonitoringSelect label={zh ? '排除节点' : 'Excluded node'} value={rule.node_id} options={[[ '', zh ? '选择节点' : 'Choose node'], ...(nodes.data ?? []).map(n => [n.id, n.name] as [string, string])]} onChange={v => setRule({ ...rule, node_id: v })} />
      {rule.kind === 'container' && <Input className="w-72" aria-label={zh ? '排除容器 ID' : 'Excluded container ID'} placeholder={zh ? '完整容器 ID（64 位）' : 'Full container ID (64 characters)'} value={rule.container_id ?? ''} onChange={e => setRule({ ...rule, container_id: e.target.value.trim() })} />}
      {rule.kind === 'compose' && <><Input className="w-44" aria-label={zh ? '排除 Compose 项目' : 'Excluded Compose project'} placeholder={zh ? '项目名' : 'Project'} value={rule.project ?? ''} onChange={e => setRule({ ...rule, project: e.target.value.trim() })} /><Input className="w-44" aria-label={zh ? '排除 Compose 服务' : 'Excluded Compose service'} placeholder={zh ? '服务名（留空为整个项目）' : 'Service (blank for project)'} value={rule.service ?? ''} onChange={e => setRule({ ...rule, service: e.target.value.trim() })} /></>}<Button size="sm" variant="outline" onClick={addRule}><Plus />{zh ? '添加规则' : 'Add rule'}</Button></div>
      {error && <p className="text-xs text-destructive">{error}</p>}<div className="divide-y">{draft.exclusions.map((r, i) => <div className="flex items-center justify-between gap-3 py-2 text-sm" key={i}><span className="min-w-0 break-all">{nodes.data?.find(n => n.id === r.node_id)?.name ?? r.node_id} · {r.kind === 'node' ? zh ? '整个节点' : 'Whole node' : r.kind === 'container' ? r.container_id : r.project + (r.service ? ' / ' + r.service : '')}</span><Button variant="ghost" size="icon-sm" aria-label={zh ? '删除排除规则' : 'Remove exclusion'} onClick={() => update({ exclusions: draft.exclusions.filter((_, index) => index !== i) })}><X /></Button></div>)}</div>
    </section>
    <div className="flex flex-wrap items-center gap-3"><Button disabled={save.isPending} onClick={() => void submit()}>{save.isPending ? <Spinner /> : <Save />}{zh ? '保存监控设置' : 'Save monitoring settings'}</Button>{save.isSuccess && <span className="text-xs text-muted-foreground">{zh ? '设置已保存' : 'Settings saved'}</span>}{save.isError && <span className="text-xs text-destructive">{save.error.message}</span>}{(save.data?.task_id || status.data?.cleanup_task_id) && <Link to="/tasks" hash={save.data?.task_id || status.data?.cleanup_task_id} className="text-xs hover:underline">{zh ? '查看历史清理任务' : 'View history cleanup task'}</Link>}</div>
    <section className="space-y-3"><h3 className="text-sm font-medium">{zh ? '采集状态' : 'Collection status'} · {status.data?.active_containers ?? 0}</h3>{status.isError && <p className="text-xs text-destructive">{status.error.message}</p>}{status.data?.nodes.map(n => <div key={n.node_id} className="flex flex-wrap justify-between gap-2 border-b py-2 text-xs"><span>{nodes.data?.find(node => node.id === n.node_id)?.name ?? n.node_id} · {n.streams} {zh ? '个采集流' : 'streams'}</span><span className="text-muted-foreground">{zh ? '最近成功写入' : 'Last successful write'}: {n.last_write ? formatDateTime(n.last_write) : '—'}</span>{n.error && <span className="text-destructive">{n.error}</span>}</div>)}</section>
  </div>
}
