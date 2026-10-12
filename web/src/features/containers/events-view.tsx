import { eventLabels, mergeEvents } from './events'
import { useQuery } from '@tanstack/react-query'
import { useEffect, useMemo, useRef, useState } from 'react'
import { Button } from '../../components/ui/button'
import { api, demoMode } from '../../lib/api'
import { nodePath } from '../../lib/nodes'
import { useI18n } from '../../lib/i18n'
import { useDateTime } from '../../lib/time-zone'
import type { ContainerEvent, EventHistory } from './types'
import { useObservationStream } from './use-observation-stream'

function EventTimeline({ nodeID, containerId }: { nodeID: string; containerId: string }) {
  const { language } = useI18n(), zh = language === 'zh-CN', { formatDateTime } = useDateTime()
  const [hours, setHours] = useState(1), [kind, setKind] = useState(''), [rows, setRows] = useState<ContainerEvent[]>([]), [pending, setPending] = useState<ContainerEvent[]>([])
  const [clock, setClock] = useState(Date.now), seen = useRef(new Set<string>())
  useEffect(() => { const timer = setInterval(() => setClock(Date.now()), 60_000); return () => clearInterval(timer) }, [])
  const remember = (id: string) => { seen.current.add(id); if (seen.current.size > 1000) seen.current.delete(seen.current.values().next().value!) }
  const [follow, setFollow] = useState(true), scroll = useRef<HTMLDivElement>(null), cursor = useRef(''), [resume, setResume] = useState('')
  const range = useMemo(() => { const until = new Date(); return { since: new Date(until.getTime() - hours * 3600_000).toISOString(), until: until.toISOString() } }, [hours])
  const query = useQuery({ queryKey: ['container-events', nodeID, containerId, range], queryFn: ({ signal }) => api<EventHistory>(nodePath(nodeID, `/containers/${containerId}/events?${new URLSearchParams({ ...range, limit: '200' })}`), { signal }), retry: false })
  useEffect(() => { setRows([]); setPending([]); seen.current.clear(); cursor.current = ''; setResume(''); setFollow(true) }, [range])
  useEffect(() => { if (!query.data) return; for (const event of query.data.events) remember(event.id); setRows(old => mergeEvents(old, query.data.events)); const newest = query.data.events.at(-1); setResume(newest ? new Date(Date.parse(newest.time) - 1000).toISOString() : range.since) }, [query.data, range.since])
  const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:'
  const url = query.isSuccess && resume && !demoMode ? `${protocol}//${location.host}/ws/nodes/${encodeURIComponent(nodeID)}/containers/${encodeURIComponent(containerId)}/events?since=${encodeURIComponent(resume)}` : null
  const status = useObservationStream(url, payload => {
    const message = JSON.parse(payload) as { type: string; entries?: ContainerEvent[] }
    if (message.type === 'error') throw new Error('Event stream ended')
    if (message.type !== 'events' || !message.entries) return
    const entries = message.entries.filter(e => e.container_id === containerId && e.node_id === nodeID && !seen.current.has(e.id))
    for (const event of entries) remember(event.id)
    const newest = entries.at(-1); if (newest) cursor.current = newest.time
    if (follow) setRows(old => mergeEvents(old, entries)); else setPending(old => mergeEvents(old, entries))
  }, () => { if (cursor.current) setResume(new Date(Date.parse(cursor.current) - 1000).toISOString()) })
  useEffect(() => { if (!demoMode || !query.isSuccess) return; const timer = setInterval(() => { const now = new Date(), e: ContainerEvent = { id: `demo-${now.getTime()}`, node_id: nodeID, container_id: containerId, time: now.toISOString(), time_nano: String(BigInt(now.getTime()) * 1000000n), action: 'health_status', health_status: 'healthy', exit_code: null }; if (follow) setRows(old => mergeEvents(old, [e])); else setPending(old => mergeEvents(old, [e])) }, 5000); return () => clearInterval(timer) }, [nodeID, containerId, query.isSuccess, follow])
  const labels = eventLabels(zh)
  const visible = rows.filter(e => Date.parse(e.time) >= clock - hours * 3600_000 && (!kind || (kind === 'health' ? e.action === 'health_status' : kind === 'abnormal' ? e.action === 'oom' || e.health_status === 'unhealthy' || e.exit_code != null && e.exit_code !== 0 : e.action !== 'oom' && e.action !== 'health_status')))
  return <section className="space-y-3"><div className="flex flex-wrap items-center gap-2">{[1, 6, 24].map(h => <Button key={h} size="sm" variant={h === hours ? 'secondary' : 'ghost'} onClick={() => setHours(h)}>{h}h</Button>)}<select aria-label={zh ? '事件类型' : 'Event type'} value={kind} onChange={e => setKind(e.target.value)} className="h-8 rounded-sm border bg-background px-2 text-xs">{[['', zh ? '全部事件' : 'All events'], ['lifecycle', zh ? '生命周期' : 'Lifecycle'], ['health', zh ? '健康' : 'Health'], ['abnormal', zh ? '异常' : 'Abnormal']].map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select><Button variant="ghost" size="sm" onClick={() => void query.refetch()}>{zh ? '刷新' : 'Refresh'}</Button><span className="text-xs text-muted-foreground">{demoMode ? zh ? '演示实时事件' : 'Demo live events' : ({ connecting: zh ? '连接中' : 'Connecting', live: zh ? '实时监听中' : 'Live', offline: zh ? '连接中断，正在重试' : 'Disconnected; retrying', paused: zh ? '后台暂停' : 'Paused in background' }[status])}</span></div>
    <p className="text-xs text-muted-foreground">{zh ? '展示 Docker 当前保留的近期事件，所选时间范围可能不完整。' : 'Recent events retained by Docker; the selected time range may be incomplete.'}{(query.data?.truncated || rows.length === 500) && (zh ? ' 已达到显示条数上限。' : ' Display limit reached.')}</p>
    {query.isPending && <p>{zh ? '正在读取事件…' : 'Loading events…'}</p>}{query.isError && <p className="text-destructive">{zh ? '事件读取失败，请重试。' : 'Unable to read events. Retry.'}</p>}
    {pending.length > 0 && <Button variant="outline" size="sm" onClick={() => { setRows(old => mergeEvents(old, pending)); setPending([]); setFollow(true); scroll.current?.scrollTo({ top: 0 }) }}>{zh ? `有 ${pending.length} 条新事件，回到最新` : `${pending.length} new events · latest`}</Button>}
    <div ref={scroll} className="max-h-[65vh] overflow-y-auto" onScroll={e => { const atTop = e.currentTarget.scrollTop < 20; setFollow(atTop); if (atTop && pending.length) { setRows(old => mergeEvents(old, pending)); setPending([]) } }}>
      {!visible.length && !query.isPending && !query.isError && <p className="py-8 text-center text-sm text-muted-foreground">{zh ? '当前范围内无可用事件' : 'No retained events in this range'}</p>}
      {visible.map(e => <div key={e.id} className="grid grid-cols-[12px_minmax(0,1fr)] gap-x-3 border-t py-3 text-sm"><span className={`mt-1.5 size-1.5 rounded-full ${e.action === 'oom' || e.health_status === 'unhealthy' || e.exit_code != null && e.exit_code !== 0 ? 'bg-destructive' : 'bg-muted-foreground/50'}`} /><div><div className="flex flex-wrap justify-between gap-2"><span>{labels[e.action] ?? e.action}{e.health_status && ` · ${e.health_status}`}{e.exit_code != null && ` · ${zh ? '退出码' : 'exit'} ${e.exit_code}`}{e.signal && ` · ${e.signal}`}</span><time dateTime={e.time} className="font-mono text-xs text-muted-foreground">{formatDateTime(e.time)}</time></div></div></div>)}
    </div>
  </section>
}

export function EventsView(props: { nodeID: string; containerId: string }) { return <EventTimeline key={`${props.nodeID}:${props.containerId}`} {...props} /> }
