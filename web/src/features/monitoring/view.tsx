import { keepPreviousData, useInfiniteQuery, useQueries, useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { lazy, Suspense, useCallback, useEffect, useMemo, useState } from 'react'
import { Download, RefreshCw, X } from 'lucide-react'
import { Button } from '../../components/ui/button'
import { Input } from '../../components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../../components/ui/select'
import { Spinner } from '../../components/ui/spinner'
import { api } from '../../lib/api'
import { useI18n } from '../../lib/i18n'
import { useDateTime } from '../../lib/time-zone'
const MonitoringCharts=lazy(()=>import('./charts').then(module=>({default:module.MonitoringCharts})))
import { localInput, parseInput, metricLabel, metricValue, monitoringCSV } from './range'
import { fields, type EventPage, type Instance, type Instances, type MonitorEvent, type QueryResult, type Target } from './types'

export function MonitoringSelect({ label, value, options, onChange }: { label: string; value: string; options: [string, string][]; onChange: (value: string) => void }) {
  return <Select value={value} onValueChange={v => v != null && onChange(String(v))}><SelectTrigger aria-label={label} className="w-auto min-w-36 max-w-full"><SelectValue>{options.find(o => o[0] === value)?.[1] ?? label}</SelectValue></SelectTrigger><SelectContent alignItemWithTrigger={false} className="max-w-[calc(100vw-32px)]">{options.map(([id, name]) => <SelectItem value={id} key={id}>{name}</SelectItem>)}</SelectContent></Select>
}
export function MonitoringView({ nodeID, containerID, name, instance, compact, compareIDs = [] }: { nodeID?: string; containerID?: string; name?: string; instance?: Instance; compact?: boolean; compareIDs?: string[] }) {
  const node = useQuery({ queryKey: ['monitoring-node', nodeID], queryFn: ({ signal }) => api<{ id: string; engine_id: string; name: string }>('/nodes/' + encodeURIComponent(nodeID!), { signal }), enabled: !instance && !!nodeID })
  if (!instance && node.isPending) return <Spinner />
  if (!instance && !node.data) return <p className="text-sm text-destructive">{node.error?.message}</p>
  const primary = instance ?? { id: '', node_id: nodeID!, container_id: containerID!, engine_id: node.data!.engine_id, node_name: node.data!.name, name: name ?? containerID!.slice(0, 12), image: '', project: '', service: '', first_seen: '', last_seen: '' }
  return <MonitoringContent key={[primary.node_id, primary.engine_id, primary.container_id].join(':')} primary={primary} compact={compact} compareIDs={compareIDs} />
}
function MonitoringContent({ primary, compact = false, compareIDs }: { primary: Instance; compact?: boolean; compareIDs: string[] }) {
  const { language } = useI18n(), zh = language === 'zh-CN'
  const { timeZone, formatDateTime } = useDateTime()
  const [hours, setHours] = useState(1), [fixed, setFixed] = useState<{ since: number; until: number }>()
  const [sinceDraft, setSinceDraft] = useState(() => localInput(Date.now() - 3600_000, timeZone)), [untilDraft, setUntilDraft] = useState(() => localInput(Date.now(), timeZone))
  const [mode, setMode] = useState<'single' | 'containers' | 'period'>(compareIDs.length ? 'containers' : 'single')
  const [extraIDs, setExtraIDs] = useState(compareIDs.slice(0, 3)), [period, setPeriod] = useState('previous')
  const [comparisonDraft, setComparisonDraft] = useState(() => localInput(Date.now() - 86400_000, timeZone))
  const [comparisonStart, setComparisonStart] = useState<number>()
  const [search, setSearch] = useState(''), [error, setError] = useState(''), [eventGroups, setEventGroups] = useState<Record<string,MonitorEvent[]>>({}), [focusedEvent, setFocusedEvent] = useState('')
  const [eventKind, setEventKind] = useState('')
  const instances = useQuery({ queryKey: ['monitoring-instances', 'picker', search], queryFn: ({ signal }) => api<Instances>('/monitoring/instances?' + new URLSearchParams({ search, limit: '100' }), { signal }), enabled: !compact && mode === 'containers' })
  const extraQueries = useQueries({ queries: extraIDs.map(id => ({ queryKey: ['monitoring-instance', id], queryFn: ({ signal }: { signal: AbortSignal }) => api<Instance>('/monitoring/instances/' + id, { signal }), enabled: mode === 'containers' })) })
  const extras = extraQueries.flatMap(q => q.data ? [q.data] : [])
  const key = [primary.node_id, primary.engine_id, primary.container_id, mode, extraIDs.join(','), fixed?.since, fixed?.until, hours, period, comparisonStart]
  const query = useQuery({
    queryKey: ['monitoring-query', ...key], retry: false,
    placeholderData: keepPreviousData,
    queryFn: ({ signal }) => {
      const until = fixed?.until ?? Date.now(), since = fixed?.since ?? until - hours * 3600_000
      const target = (instance: Instance, a = since, b = until): Target => ({ node_id: instance.node_id, engine_id: instance.engine_id, container_id: instance.container_id, since: new Date(a).toISOString(), until: new Date(b).toISOString() })
      const targets = [target(primary)]
      if (mode === 'containers') targets.push(...extras.map(i => target(i)))
      if (mode === 'period') {
        const duration = until - since
        const other = period === 'previous' ? since - duration : period === 'yesterday' ? since - 86400_000 : comparisonStart ?? since - duration
        targets.push(target(primary, other, other + duration))
      }
      return api<QueryResult>('/monitoring/query', { method: 'POST', body: JSON.stringify({ targets, max_points: 1440 }), signal })
    },
    enabled: mode !== 'containers' || extras.length === extraIDs.length,
    refetchInterval: fixed ? false : 5000,
    refetchOnWindowFocus: !fixed,
  })
  const result = query.data
  const first = result?.series[0]
  const changeRange = (h: number) => {const until=Date.now();setSinceDraft(localInput(until-h*3600_000,timeZone));setUntilDraft(localInput(until,timeZone));setHours(h); setFixed(undefined); setError('') }
  const apply = () => {
    try {
      const since = parseInput(sinceDraft, timeZone), until = parseInput(untilDraft, timeZone)
      if (since >= until || until - since > 30 * 86400_000 || until > Date.now() + 60_000) throw new Error('Invalid range')
      if (period === 'custom') setComparisonStart(parseInput(comparisonDraft, timeZone))
      setFixed({ since, until }); setError('')
    } catch { setError(zh ? '请选择有效的起止时间，范围不能超过 30 天。' : 'Choose a valid time range of up to 30 days.') }
  }
  const zoom = (a: number, b: number) => { if (Number.isFinite(a) && Number.isFinite(b) && b > a) { setFixed({ since: a, until: b }); setSinceDraft(localInput(a, timeZone)); setUntilDraft(localInput(b, timeZone)) } }
  const exportCSV = () => {
    if (!result) return
    const url = URL.createObjectURL(new Blob([monitoringCSV(result)], { type: 'text/csv;charset=utf-8' }))
    const link = document.createElement('a'); link.href = url; link.download = 'container-monitoring.csv'; link.click(); setTimeout(() => URL.revokeObjectURL(url), 1000)
  }
  const receiveEvents = useCallback((key:string,items:MonitorEvent[]) => setEventGroups(old=>old[key]===items?old:{...old,[key]:items}), [])
  const events=useMemo(()=>Object.values(eventGroups).flat(),[eventGroups])
  const eventClick = (event: MonitorEvent) => { setFocusedEvent(event.id); document.getElementById('monitor-event-' + event.id)?.scrollIntoView({ block: 'nearest' }) }
  const coverage = (value: string) => ({ expired: zh ? '部分或全部历史已到期' : 'History partly or fully expired', not_collected: zh ? '尚未采集到历史' : 'No recorded history', excluded: zh ? '监控已关闭或对象已排除' : 'Monitoring disabled or excluded', partial: zh ? '包含采集覆盖缺口' : 'Contains collection coverage gaps', truncated: zh ? '分段过多，显示点已达上限' : 'Segment display limit reached' }[value])
  return <section className="min-w-0 space-y-4 text-left" aria-label={zh ? '容器监控' : 'Container monitoring'} aria-busy={query.isFetching}>
    <div className="flex flex-wrap items-center justify-between gap-2">
      <span className="text-xs text-muted-foreground">{compact ? zh ? '最近 1 小时' : 'Last hour' : fixed ? zh ? '固定时间范围' : 'Fixed range' : zh ? '跟随最新 · 每 5 秒刷新' : 'Following latest · refresh every 5s'}</span>
      <div className="flex gap-1">
        {!compact && <><Button size="sm" variant="ghost" disabled={!result || query.isPlaceholderData} onClick={exportCSV}><Download />CSV</Button>{fixed && <Button size="sm" variant="secondary" onClick={() => changeRange(hours)}>{zh ? '回到最新' : 'Back to latest'}</Button>}</>}
        <Button size="icon-sm" variant="ghost" aria-label={zh ? '刷新监控' : 'Refresh monitoring'} onClick={() => void query.refetch()}>{query.isFetching ? <Spinner /> : <RefreshCw />}</Button>
      </div>
    </div>
    {!compact && <div className="section-surface grid gap-5 xl:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
      <fieldset className="min-w-0 space-y-3">
        <legend className="mb-2 text-xs font-medium">{zh ? '时间范围' : 'Time range'}<span className="ml-3 font-normal text-muted-foreground">{timeZone}</span></legend>
        <div className="flex gap-1" role="group" aria-label={zh ? '快捷时间范围' : 'Quick time range'}>{[1, 6, 24, 72].map(h => <Button key={h} size="sm" aria-pressed={!fixed && h === hours} variant={!fixed && h === hours ? 'default' : 'ghost'} onClick={() => changeRange(h)}>{h === 72 ? '3d' : h + 'h'}</Button>)}</div>
        <div className="grid max-w-2xl items-end gap-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto]">
          <label className="grid min-w-0 gap-1 text-xs">{zh ? '开始' : 'From'}<Input className="min-w-0 w-full" aria-label={zh ? '监控开始时间' : 'Monitoring start time'} type="datetime-local" value={sinceDraft} onChange={e => setSinceDraft(e.target.value)} /></label>
          <label className="grid min-w-0 gap-1 text-xs">{zh ? '结束' : 'Until'}<Input className="min-w-0 w-full" aria-label={zh ? '监控结束时间' : 'Monitoring end time'} type="datetime-local" value={untilDraft} onChange={e => setUntilDraft(e.target.value)} /></label>
          <Button size="sm" variant="outline" onClick={apply}>{zh ? '应用时间' : 'Apply range'}</Button>
        </div>
      </fieldset>
      <fieldset className="min-w-0 space-y-3">
        <legend className="mb-2 text-xs font-medium">{zh ? '对比方式' : 'Comparison'}</legend>
        <div className="flex flex-wrap items-end gap-3">
          <div className="grid min-w-0 gap-1 text-xs"><span className="text-muted-foreground">{zh ? '模式' : 'Mode'}</span><MonitoringSelect label={zh ? '对比模式' : 'Comparison mode'} value={mode} onChange={v => { setMode(v as typeof mode); setEventGroups({}) }} options={[[ 'single', zh ? '单容器' : 'Single container'], ['containers', zh ? '跨容器对比' : 'Compare containers'], ['period', zh ? '同期对比' : 'Compare periods']]} /></div>
          {mode === 'period' && <div className="grid min-w-0 gap-1 text-xs"><span className="text-muted-foreground">{zh ? '时段' : 'Period'}</span><MonitoringSelect label={zh ? '对比时段' : 'Comparison period'} value={period} onChange={setPeriod} options={[[ 'previous', zh ? '上一时段' : 'Previous period'], ['yesterday', zh ? '昨日同期' : 'Yesterday'], ['custom', zh ? '自定义等长时段' : 'Custom equal-length period']]} /></div>}
        </div>
        {mode === 'containers' && <>
          <div className="flex flex-wrap items-end gap-2">
            <label className="grid min-w-0 flex-1 gap-1 text-xs">{zh ? '搜索' : 'Search'}<Input className="min-w-0 w-full" aria-label={zh ? '搜索对比容器' : 'Search comparison containers'} placeholder={zh ? '搜索容器或节点' : 'Search containers or nodes'} value={search} onChange={e => setSearch(e.target.value)} /></label>
            <MonitoringSelect label={zh ? '添加对比容器' : 'Add comparison container'} value="" options={[['', zh ? '添加容器（最多 4 个）' : 'Add container (up to 4)'], ...(instances.data?.items ?? []).filter(i => !(i.node_id===primary.node_id&&i.engine_id===primary.engine_id&&i.container_id===primary.container_id) && !extraIDs.includes(i.id)).map(i => [i.id, i.node_name + ' / ' + i.name] as [string, string])]} onChange={id => { if (id && extraIDs.length < 3) setExtraIDs([...extraIDs, id]) }} />
          </div>
          <div className="flex flex-wrap gap-2">{extraIDs.map((id, i) => <Button key={id} variant="outline" size="sm" className="max-w-full" aria-label={(zh ? '移除对比容器 ' : 'Remove comparison container ') + (extras[i]?.name ?? id)} onClick={() => setExtraIDs(extraIDs.filter(v => v !== id))}><span className="min-w-0 truncate">{extras[i]?.name ?? id.slice(0, 12)}</span><X /></Button>)}</div>
          {extraQueries.some(q => q.isError) && <p className="text-xs text-destructive">{zh ? '对比实例已过期或读取失败，请移除后重试。' : 'Comparison instance expired or unavailable; remove it and retry.'}</p>}
        </>}
        {mode === 'period' && period === 'custom' && <label className="grid max-w-xs gap-1 text-xs">{zh ? '对比开始时间' : 'Comparison start'}<Input aria-label={zh ? '对比开始时间' : 'Comparison start'} type="datetime-local" value={comparisonDraft} onChange={e => setComparisonDraft(e.target.value)} onBlur={() => { try { setComparisonStart(parseInput(comparisonDraft, timeZone)); setError('') } catch { setError(zh ? '对比时间无效' : 'Invalid comparison time') } }} /></label>}
      </fieldset>
    </div>}
    {(error || query.isError) && <p className="text-sm text-destructive">{error || query.error?.message}</p>}
    {query.isPending && <div className="flex items-center gap-2 text-sm"><Spinner />{zh ? '正在读取监控历史' : 'Loading monitoring history'}</div>}
    {result?.series.map((s, i) => (i === 0 || mode === 'period' || coverage(s.coverage)) && <div className="flex flex-wrap gap-x-2 text-xs text-muted-foreground" key={i}>
      {coverage(s.coverage) && <span>{s.instance.name || primary.name} · {coverage(s.coverage)}</span>}
      {!compact && <><span>{mode === 'period' ? i === 0 ? zh ? '当前时段' : 'Current period' : zh ? '对比时段' : 'Comparison period' : zh ? '查询范围' : 'Query range'}</span><span>{formatDateTime(s.target.since)} – {formatDateTime(s.target.until)}</span><span>· {zh ? '聚合粒度' : 'Resolution'} {s.step_seconds}s</span></>}
    </div>)}
    {first && <>
      {compact ? <dl className="flex flex-wrap gap-x-6 gap-y-2">{fields.filter(f => ['network_rx', 'network_tx', 'block_read', 'block_write', 'pids'].includes(f)).map(field => <div key={field}><dt className="text-xs text-muted-foreground">{metricLabel(field, zh)}</dt><dd className="font-mono text-xs">{metricValue(field, first.points.at(-1)?.values[field]?.avg)}</dd></div>)}</dl> : <section className="section-surface space-y-3" aria-label={zh ? '范围统计' : 'Range statistics'}>
        <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1"><h3 className="text-sm font-medium">{zh ? '范围统计' : 'Range statistics'}</h3>{result!.series.length > 1 && <span className="text-xs text-muted-foreground">{first.instance.name} · {first.instance.node_name}{mode === 'period' && (zh ? ' · 当前时段' : ' · Current period')}</span>}</div>
        <div className="grid items-start gap-x-8 gap-y-4 md:grid-cols-2">{([['cpu', 'memory', 'pids'], ['network_rx', 'network_tx', 'block_read', 'block_write']] as const).map((group, i) => <table key={i} className="w-full text-xs tabular-nums">
          <caption className="pb-2 text-left font-medium">{i === 0 ? zh ? 'CPU / 内存 / PIDs' : 'CPU / Memory / PIDs' : zh ? '网络 / 磁盘' : 'Network / Disk'}</caption>
          <thead className="text-muted-foreground"><tr><th scope="col" className="pb-2 text-left font-normal">{zh ? '指标' : 'Metric'}</th><th scope="col" className="w-[28%] pb-2 text-right font-normal">{zh ? '平均值' : 'Average'}</th><th scope="col" className="w-[28%] pb-2 text-right font-normal">{zh ? '峰值' : 'Peak'}</th></tr></thead>
          <tbody>{group.map(field => <tr key={field} className="hover:bg-muted/50"><th scope="row" className="py-1.5 text-left font-normal">{metricLabel(field, zh)}</th><td className="whitespace-nowrap py-1.5 text-right font-mono">{metricValue(field, first.summary[field]?.avg)}</td><td className="whitespace-nowrap py-1.5 text-right font-mono text-muted-foreground">{metricValue(field, first.summary[field]?.max)}</td></tr>)}</tbody>
        </table>)}</div>
      </section>}
      <Suspense fallback={<Spinner />}><MonitoringCharts series={result!.series} compact={compact} relative={mode === 'period'} events={events} onZoom={compact ? undefined : zoom} onEvent={eventClick} focusedEvent={focusedEvent} /></Suspense>
      <div className="text-xs text-muted-foreground">{zh ? '最近记录' : 'Latest record'}: {first.points.length ? formatDateTime(first.points.at(-1)!.time) : '—'}{compact && <> · <Link to="/containers/$containerId" params={{ containerId: primary.container_id }} hash="monitoring" className="text-foreground hover:underline">{zh ? '查看完整监控' : 'View full monitoring'}</Link></>}</div>
      {!compact && <><div className="flex flex-wrap items-center gap-2"><h3 className="text-sm font-medium">{zh ? '事件' : 'Events'}</h3><MonitoringSelect label={zh ? '监控事件类型' : 'Monitoring event type'} value={eventKind} onChange={setEventKind} options={[[ '', zh ? '全部事件' : 'All events'], ['lifecycle', zh ? '生命周期' : 'Lifecycle'], ['health', zh ? '健康变化' : 'Health changes'], ['abnormal', zh ? '异常' : 'Abnormal']]} /></div>{result!.series.map((s, i) => <MonitoringEvents key={s.instance.id + '-' + i} series={s} kind={eventKind} follow={!fixed} label={s.instance.name} focused={focusedEvent} eventKey={s.instance.id+'-'+i} onData={receiveEvents} onSelect={eventClick} />)}</>}
    </>}
  </section>
}
function MonitoringEvents({ series, kind, follow, label, focused, eventKey, onData, onSelect }: { series: QueryResult['series'][number]; kind: string; follow: boolean; label: string; focused: string; eventKey: string; onData?: (key:string,items: MonitorEvent[]) => void; onSelect: (e: MonitorEvent) => void }) {
  const { language } = useI18n(), zh = language === 'zh-CN', { formatDateTime } = useDateTime()
  const query = useInfiniteQuery({ queryKey: ['monitoring-events', series.instance.id, kind, follow, follow ? '' : series.target.since, follow ? '' : series.target.until], initialPageParam: '', placeholderData: keepPreviousData, queryFn: ({ signal, pageParam }) => api<EventPage>('/monitoring/instances/' + series.instance.id + '/events?' + new URLSearchParams({ since: series.target.since, until: series.target.until, kind, cursor: pageParam, limit: '200' }), { signal }), getNextPageParam: page => page.next_cursor || undefined, refetchInterval: follow ? 5000 : false, retry: false })
  const events = useMemo(() => query.data?.pages.flatMap(p => p.items) ?? [], [query.data])
  useEffect(() => { onData?.(eventKey,events) }, [events, onData, eventKey])
  return <div className="space-y-2"><h4 className="text-xs text-muted-foreground">{label}</h4>{query.isError && <p className="text-xs text-destructive">{query.error.message}</p>}{!events.length && !query.isPending && <p className="text-xs text-muted-foreground">{zh ? '所选范围内无已记录事件' : 'No recorded events in this range'}</p>}<div className="max-h-72 overflow-auto">{events.map(e => <button id={'monitor-event-' + e.id} key={e.id} type="button" onClick={() => onSelect(e)} className={'flex w-full flex-wrap justify-between gap-2 rounded-md px-2 py-2 text-left text-xs hover:bg-muted/50 ' + (focused === e.id ? 'bg-muted' : '')}><span>{e.action}{e.health_status && ' · ' + e.health_status}{e.exit_code != null && ' · exit ' + e.exit_code}</span><time>{formatDateTime(e.time)}</time></button>)}</div>{query.hasNextPage && events.length < 1000 && <Button size="sm" variant="ghost" disabled={query.isFetchingNextPage} onClick={() => void query.fetchNextPage()}>{zh ? '加载更多事件' : 'Load more events'}</Button>}{query.hasNextPage && events.length >= 1000 && <p className="text-xs text-muted-foreground">{zh ? '已达 1000 条显示上限，请缩小范围或筛选类型。' : 'Showing 1000 events; narrow the range or filter by type.'}</p>}</div>
}
