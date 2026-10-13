import { useQuery } from '@tanstack/react-query'
import { Link, useNavigate, useParams, useSearch } from '@tanstack/react-router'
import { useState } from 'react'
import { Button } from '../../components/ui/button'
import { Checkbox } from '../../components/ui/checkbox'
import { Input } from '../../components/ui/input'
import { Spinner } from '../../components/ui/spinner'
import { api } from '../../lib/api'
import { useI18n } from '../../lib/i18n'
import { useDateTime } from '../../lib/time-zone'
import { ResourceFrame } from '../../pages/images'
import { MonitoringSelect, MonitoringView } from './view'
import { localInput, parseInput } from './range'
import type { Instance, Instances } from './types'

export function MonitoringHistory() {
  const { language } = useI18n(), zh = language === 'zh-CN', { formatDateTime, timeZone } = useDateTime()
  const navigate = useNavigate()
  const [search, setSearch] = useState(''), [node, setNode] = useState(''), [page, setPage] = useState(1), [selected, setSelected] = useState<string[]>([])
  const [from, setFrom] = useState(''), [to, setTo] = useState(''), [range, setRange] = useState({ since: '', until: '' }), [error, setError] = useState('')
  const nodes = useQuery({ queryKey: ['monitoring-history-nodes'], queryFn: ({ signal }) => api<Instances>('/monitoring/instances?limit=1', { signal }) })
  const query = useQuery({ queryKey: ['monitoring-instances', search, node, page, range], queryFn: ({ signal }) => api<Instances>('/monitoring/instances?' + new URLSearchParams({ search, node_id: node, page: String(page), limit: '50', ...range }), { signal }), refetchInterval: 10_000 })
  const apply = () => { try { const a = from ? parseInput(from, timeZone) : undefined, b = to ? parseInput(to, timeZone) : undefined; if (a != null && b != null && b <= a) throw new Error(); setRange({ since: a == null ? '' : new Date(a).toISOString(), until: b == null ? '' : new Date(b).toISOString() }); setPage(1); setError('') } catch { setError(zh ? '历史时间范围无效' : 'Invalid history range') } }
  const nodeOptions: [string,string][] = (nodes.data?.nodes??[]).map(n=>[n.id,n.name])
  return <div className="space-y-4"><div className="flex flex-wrap items-end gap-2"><Input className="w-64" placeholder={zh ? '搜索名称、ID、节点或项目' : 'Search name, ID, node or project'} aria-label={zh ? '搜索监控历史' : 'Search monitoring history'} value={search} onChange={e => { setSearch(e.target.value); setPage(1) }} /><MonitoringSelect label={zh ? '历史节点' : 'Historical node'} value={node} options={[[ '', zh ? '全部节点' : 'All nodes'], ...nodeOptions]} onChange={v => { setNode(v); setPage(1) }} /><label className="grid gap-1 text-xs">{zh ? '开始' : 'From'}<Input aria-label={zh ? '历史开始时间' : 'History start time'} type="datetime-local" value={from} placeholder={localInput(Date.now(), timeZone)} onChange={e => setFrom(e.target.value)} /></label><label className="grid gap-1 text-xs">{zh ? '结束' : 'Until'}<Input aria-label={zh ? '历史结束时间' : 'History end time'} type="datetime-local" value={to} onChange={e => setTo(e.target.value)} /></label><Button variant="outline" size="sm" onClick={apply}>{zh ? '筛选时间' : 'Filter time'}</Button>{selected.length > 1 && <Button size="sm" onClick={() => void navigate({ to: '/containers/history/$instanceId', params: { instanceId: selected[0] }, search: { compare: selected.slice(1).join(',') } })}>{zh ? '对比所选实例' : 'Compare selected'} ({selected.length})</Button>}</div>
    {(query.isError || error) && <p className="text-destructive">{error || query.error?.message}</p>}{query.isPending && <Spinner />}
    <div className="space-y-1">{query.data?.items.map(instance => <div key={instance.id} className="flex items-center gap-3 py-3"><Checkbox aria-label={(zh ? '选择历史实例 ' : 'Select historical instance ') + instance.name} checked={selected.includes(instance.id)} disabled={!selected.includes(instance.id) && selected.length >= 4} onCheckedChange={v => setSelected(v ? [...selected, instance.id] : selected.filter(id => id !== instance.id))} /><div className="min-w-0 flex-1"><Link to="/containers/history/$instanceId" params={{ instanceId: instance.id }} className="block truncate text-sm hover:underline">{instance.name}</Link><p className="truncate text-xs text-muted-foreground">{instance.node_name} · {instance.container_id.slice(0, 12)} · {instance.project}{instance.service && ' / ' + instance.service}</p></div><span className="hidden text-xs text-muted-foreground sm:inline">{formatDateTime(instance.first_seen)} – {formatDateTime(instance.last_seen)}</span><Link to="/containers/history/$instanceId" params={{ instanceId: instance.id }} className="text-xs hover:underline">{zh ? '查看监控' : 'View monitoring'}</Link></div>)}</div>
    {query.isSuccess && !query.data.items.length && <p className="py-8 text-center text-sm text-muted-foreground">{zh ? '没有已记录的监控历史' : 'No recorded monitoring history'}</p>}
    <div className="flex items-center justify-end gap-3 text-xs"><span>{query.data?.total ?? 0} · {page}</span><Button size="sm" variant="ghost" disabled={page <= 1} onClick={() => setPage(page - 1)}>{zh ? '上一页' : 'Previous'}</Button><Button size="sm" variant="ghost" disabled={page * 50 >= (query.data?.total ?? 0)} onClick={() => setPage(page + 1)}>{zh ? '下一页' : 'Next'}</Button></div>
  </div>
}
export function MonitoringHistoryPage() {
  const { instanceId } = useParams({ from: '/containers/history/$instanceId' })
  const { compare } = useSearch({ from: '/containers/history/$instanceId' })
  const { language } = useI18n(), zh = language === 'zh-CN'
  const query = useQuery({ queryKey: ['monitoring-instance', instanceId], queryFn: ({ signal }) => api<Instance>('/monitoring/instances/' + instanceId, { signal }) })
  return <ResourceFrame title={query.data?.name ?? (zh ? '监控历史' : 'Monitoring history')} detail={query.data ? query.data.node_name + ' · ' + query.data.container_id : ''} action={<Link to="/containers" hash="monitoring-history" className="text-sm hover:underline">{zh ? '返回监控历史' : 'Back to monitoring history'}</Link>}>{query.isPending ? <Spinner /> : query.isError ? <p className="text-destructive">{query.error.message}</p> : <MonitoringView instance={query.data!} compareIDs={compare?.split(',').filter(id => /^[a-f0-9]{64}$/.test(id)).slice(0, 3)} />}</ResourceFrame>
}
