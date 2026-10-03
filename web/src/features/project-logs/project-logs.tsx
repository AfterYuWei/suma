import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Download, Pause, Play, RefreshCw, Trash2, ArrowDown } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { Button } from '../../components/ui/button'
import { Checkbox } from '../../components/ui/checkbox'
import { Input } from '../../components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../../components/ui/select'
import { Popover, PopoverContent, PopoverTrigger } from '../../components/ui/popover'
import { TooltipHint } from '../../components/ui/tooltip-hint'
import { api, demoMode, subscribeDemoProjectLogs } from '../../lib/api'
import { nodePath } from '../../lib/nodes'
import { useI18n } from '../../lib/i18n'
import { useUIStore } from '../../stores/ui'
import { LogTailSelect } from '../containers/log-tail-select'
import { useLogAutoScroll } from '../containers/use-log-auto-scroll'
import { type LogEvent, type LogSnapshot, type LogSource, mergeLogs } from './types'
const empty = (): LogSnapshot => ({ entries: [], sources: [], errors: [], truncated: false })
export function ProjectLogs({ nodeID, projectName }: { nodeID: string; projectName: string }) {
 const { language } = useI18n(); const zh = language === 'zh-CN'; const client = useQueryClient()
 const tail = useUIStore(s => s.logTail)
 const [services, setServices] = useState<string[]>([]); const [instances, setInstances] = useState<string[]>([])
 const [oneOff, setOneOff] = useState(false); const [stream, setStream] = useState('all'); const [search, setSearch] = useState('')
 const [mode, setMode] = useState('live'); const [sinceInput, setSinceInput] = useState(''); const [untilInput, setUntilInput] = useState('')
 const [range, setRange] = useState<{ since?: string; until?: string }>({})
 const [notice, setNotice] = useState(''); const [connection, setConnection] = useState('connecting'); const [retry, setRetry] = useState(0)
 const [frozen, setFrozen] = useState<LogSnapshot | null>(null)
 const parameters = useMemo(() => { const params = new URLSearchParams({ tail: String(tail), include_one_off: String(oneOff) }); if (services.length) params.set('services', services.join(',')); if (instances.length) params.set('containers', instances.join(',')); if (range.since) params.set('since', range.since); if (range.until) params.set('until', range.until); return params.toString() }, [tail, oneOff, services, instances, range])
 const cacheKey = useMemo(() => ['project-log-stream', nodeID, projectName, parameters, mode], [nodeID, projectName, parameters, mode])
 const sourceQuery = useQuery({ queryKey: ['project-log-sources', nodeID, projectName], queryFn: () => api<LogSource[]>(nodePath(nodeID, `/projects/compose/${encodeURIComponent(projectName)}/log-sources`)), refetchInterval: mode === 'live' ? 5000 : false })
 const sourceLimitExceeded = (sourceQuery.data || []).filter(source => (!services.length || services.includes(source.service)) && (!instances.length || instances.includes(source.container_id)) && (!source.one_off || oneOff || !!instances.length)).length > 64
 const history = useQuery({ queryKey: ['project-log-history', nodeID, projectName, parameters], queryFn: () => api<LogSnapshot>(nodePath(nodeID, `/projects/compose/${encodeURIComponent(projectName)}/logs/history?${parameters}`)), enabled: mode !== 'live' && (mode !== 'custom' || !!range.until) })
 const live = useQuery<LogSnapshot>({ queryKey: cacheKey, queryFn: async () => empty(), enabled: false, initialData: empty, gcTime: 0 })
 const keyRef = useRef(cacheKey); keyRef.current = cacheKey
 useEffect(() => { setServices([]); setInstances([]); setFrozen(null); setNotice('') }, [nodeID, projectName])
 useEffect(() => {
  if (mode !== 'live') return
  if (sourceLimitExceeded) { client.setQueryData(cacheKey, empty()); setConnection('selection_required'); return }
  let disposed = false; let socket: WebSocket | undefined; let timer: ReturnType<typeof setTimeout> | undefined; let demoCleanup: (() => void) | undefined; let attempts = 0
  client.setQueryData(cacheKey, empty()); setFrozen(null); setConnection('connecting')
  const consume = (event: LogEvent) => client.setQueryData<LogSnapshot>(cacheKey, previous => {
   const prior = previous || empty(); const merged = mergeLogs(prior.entries, event.entries || [], tail)
   return { entries: merged.entries, sources: event.type === 'sources' ? event.sources || [] : event.sources ?? prior.sources, errors: event.type === 'source_error' ? [...prior.errors, ...(event.errors || [])].slice(-64) : event.errors ?? prior.errors, truncated: prior.truncated || !!event.truncated || merged.truncated }
  })
  if (demoMode) { void subscribeDemoProjectLogs(nodeID, projectName, consume, tail).then(stop => { if (disposed) stop(); else demoCleanup = stop }); setConnection('live') }
  else {
   const connect = () => {
    if (disposed) return
    socket = new WebSocket(`${location.protocol === 'https:' ? 'wss:' : 'ws:'}//${location.host}/ws/nodes/${encodeURIComponent(nodeID)}/projects/compose/${encodeURIComponent(projectName)}/logs?${parameters}`)
    socket.onopen = () => { attempts = 0; setConnection('live') }
    socket.onmessage = event => { try { consume(JSON.parse(String(event.data)) as LogEvent) } catch { setNotice(zh ? '日志响应无法解析' : 'Unable to parse log response') } }
    socket.onclose = () => { if (!disposed) { setConnection('reconnecting'); timer = setTimeout(connect, Math.min(30000, 1000 * 2 ** attempts++)) } }
    socket.onerror = () => { if (!disposed) setConnection('reconnecting') }
   }; connect()
  }
  return () => { disposed = true; clearTimeout(timer); socket?.close(); demoCleanup?.() }
 }, [nodeID, projectName, parameters, mode, retry, client, cacheKey, tail, zh, sourceLimitExceeded])
 useEffect(() => { setFrozen(null) }, [parameters, mode])
 const snapshot = frozen || (mode === 'live' ? live.data : history.data) || empty()
 const visible = useMemo(() => snapshot.entries.filter(row => (stream === 'all' || row.stream === stream) && (!search || `${row.text} ${row.service} ${row.container_name}`.toLowerCase().includes(search.toLowerCase())) && (!services.length || services.includes(row.service)) && (!instances.length || instances.includes(row.container_id))), [snapshot.entries, stream, search, services, instances])
 const { viewportRef, onScroll } = useLogAutoScroll<HTMLDivElement>(visible, `${nodeID}/${projectName}/${parameters}`)
 const options = [{ value: 'live', label: zh ? '实时跟随' : 'Live' }, { value: '15', label: zh ? '最近 15 分钟' : 'Last 15 minutes' }, { value: '60', label: zh ? '最近 1 小时' : 'Last hour' }, { value: '1440', label: zh ? '最近 24 小时' : 'Last 24 hours' }, { value: 'custom', label: zh ? '自定义时间' : 'Custom range' }]
 const download = () => { const text = `${snapshot.truncated ? '# Bounded / truncated log view\n' : ''}${visible.map(row => `${row.time} ${row.service}/${row.container_name} [${row.stream}] ${row.text}${row.truncated ? ' [truncated]' : ''}`).join('\n')}`; const url = URL.createObjectURL(new Blob([text], { type: 'text/plain;charset=utf-8' })); const link = document.createElement('a'); link.href = url; link.download = `${projectName}.log`; link.click(); URL.revokeObjectURL(url) }
 const sourceRows = sourceQuery.data || snapshot.sources
 const selectedEnded = mode === 'live' && instances.length > 0 && !snapshot.sources.some(source => source.state === 'running' || source.state === 'restarting')
 const toggleList = (current: string[], value: string) => current.includes(value) ? current.filter(item => item !== value) : [...current, value]
 const applyRange = () => { const start = new Date(sinceInput); const end = new Date(untilInput); if (!Number.isFinite(start.getTime()) || !Number.isFinite(end.getTime()) || start >= end) { setNotice(zh ? '请输入有效时间，结束时间必须晚于开始时间。' : 'Enter valid times; end must follow start.'); return }; setNotice(''); setRange({ since: start.toISOString(), until: end.toISOString() }); setFrozen(null) }
 return <div className="flex w-full flex-col gap-3" data-testid="project-aggregate-logs"><div className="flex flex-wrap items-center gap-2"><span className="text-xs text-muted-foreground">{selectedEnded ? (zh ? '所选实例已结束' : 'Selected instances ended') : mode === 'live' ? ({ live: zh ? '实时' : 'Live', connecting: zh ? '连接中' : 'Connecting', reconnecting: zh ? '连接中断，正在重连' : 'Disconnected · reconnecting', selection_required: zh ? '请选择最多 64 个实例' : 'Select no more than 64 instances' } as Record<string, string>)[connection] : (zh ? '历史日志' : 'History')}</span><Popover><PopoverTrigger render={<Button size="sm" variant="outline" />}>{zh ? '服务' : 'Services'}{services.length ? ` (${services.length})` : ''}</PopoverTrigger><PopoverContent className="max-h-72 w-64 overflow-auto"><Button size="sm" variant="ghost" onClick={() => setServices([])}>{zh ? '全部服务' : 'All services'}</Button>{[...new Set(sourceRows.map(row => row.service))].sort().map(name => <label key={name} className="flex items-center gap-2 py-1.5 text-sm"><Checkbox checked={services.includes(name)} onCheckedChange={() => setServices(toggleList(services, name))} />{name}</label>)}</PopoverContent></Popover><Popover><PopoverTrigger render={<Button size="sm" variant="outline" />}>{zh ? '实例' : 'Instances'}{instances.length ? ` (${instances.length})` : ''}</PopoverTrigger><PopoverContent className="max-h-72 w-80 overflow-auto"><Button size="sm" variant="ghost" onClick={() => setInstances([])}>{zh ? '全部普通实例' : 'All regular instances'}</Button>{sourceRows.map(row => <label key={row.container_id} className="flex items-start gap-2 py-1.5 text-xs"><Checkbox checked={instances.includes(row.container_id)} onCheckedChange={() => setInstances(toggleList(instances, row.container_id))} /><span className="break-all">{row.container_name} · {row.state}{row.one_off ? ' · one-off' : ''}{row.orphan ? (zh ? ' · 孤立服务' : ' · orphan') : ''}</span></label>)}</PopoverContent></Popover><Select items={options} value={mode} onValueChange={value => { if (!value) return; setMode(value); setFrozen(null); setNotice(''); if (value === 'live' || value === 'custom') setRange({}); else setRange({ since: new Date(Date.now() - Number(value) * 60000).toISOString(), until: new Date().toISOString() }) }}><SelectTrigger className="w-40" aria-label={zh ? '日志时间范围' : 'Log time range'}><SelectValue /></SelectTrigger><SelectContent>{options.map(option => <SelectItem key={option.value} value={option.value}>{option.label}</SelectItem>)}</SelectContent></Select><LogTailSelect zh={zh} /></div>
 {mode === 'custom' && <div className="flex flex-wrap items-center gap-2"><Input type="datetime-local" className="w-full sm:w-64" aria-label={zh ? '开始时间' : 'Start time'} value={sinceInput} onChange={event => setSinceInput(event.target.value)} /><Input type="datetime-local" className="w-full sm:w-64" aria-label={zh ? '结束时间' : 'End time'} value={untilInput} onChange={event => setUntilInput(event.target.value)} /><Button size="sm" variant="outline" onClick={applyRange}>{zh ? '查询时间范围' : 'Query range'}</Button></div>}
 <div className="flex flex-wrap items-center gap-2"><Input className="min-w-40 flex-1" aria-label={zh ? '搜索项目日志' : 'Search project logs'} placeholder={zh ? '搜索日志、服务或实例…' : 'Search logs, services or instances…'} value={search} onChange={event => setSearch(event.target.value)} /><Select value={stream} onValueChange={value => { if (value) setStream(value) }}><SelectTrigger className="w-32" aria-label={zh ? '日志输出流' : 'Log stream'}><SelectValue>{stream === 'all' ? (zh ? '全部输出' : 'All output') : stream}</SelectValue></SelectTrigger><SelectContent>{['all', 'stdout', 'stderr', 'tty'].map(value => <SelectItem key={value} value={value}>{value === 'all' ? (zh ? '全部输出' : 'All output') : value}</SelectItem>)}</SelectContent></Select><label className="flex items-center gap-1.5 text-xs"><Checkbox checked={oneOff} onCheckedChange={value => setOneOff(Boolean(value))} />one-off</label>{[[frozen ? (zh ? '继续' : 'Resume') : (zh ? '暂停' : 'Pause'), frozen ? Play : Pause, () => setFrozen(frozen ? null : snapshot)], [zh ? '清空视图' : 'Clear view', Trash2, () => { client.setQueryData(mode === 'live' ? keyRef.current : ['project-log-history', nodeID, projectName, parameters], empty()); setFrozen(null) }], [zh ? '返回最新' : 'Jump to latest', ArrowDown, () => { const viewport = viewportRef.current; if (viewport) viewport.scrollTop = viewport.scrollHeight }], [zh ? '重新连接' : 'Reconnect', RefreshCw, () => { setFrozen(null); if (mode === 'live') setRetry(value => value + 1); else void history.refetch() }], [zh ? '下载筛选结果' : 'Download filtered logs', Download, download]].map(([label, Icon, action]) => { const IconComponent = Icon as typeof Play; return <TooltipHint key={label as string} content={label as string}><Button size="icon-sm" variant="outline" aria-label={label as string} onClick={action as () => void}><IconComponent /></Button></TooltipHint> })}</div>
 {sourceLimitExceeded && <p className="text-sm text-destructive">{zh ? '单连接最多 64 个实例，请缩小服务或实例选择。' : 'A connection supports at most 64 instances. Narrow the service or instance selection.'}</p>}
 {(notice || history.error || sourceQuery.error) && <p className="text-sm text-destructive">{notice || history.error?.message || sourceQuery.error?.message}</p>}
 {snapshot.truncated && <p className="text-xs text-muted-foreground">{zh ? `当前仅保留最新 ${tail} 行／8 MiB；更早或超长日志已截断。` : `View is bounded to ${tail} lines / 8 MiB; earlier or oversized logs were truncated.`}</p>}
 {snapshot.errors.length > 0 && <div className="text-xs text-destructive">{snapshot.errors.map((error, index) => <p key={`${error.container_id}/${index}`}>{sourceRows.find(row => row.container_id === error.container_id)?.container_name || error.container_id}: {zh ? '该日志源不可用或已中断；其余日志继续显示。' : error.message}</p>)}</div>}
 <div ref={viewportRef} onScroll={onScroll} className="h-[56vh] w-full overflow-auto overscroll-contain rounded-md border bg-background p-3 font-mono text-xs leading-relaxed" data-testid="project-log-lines">{visible.map(row => <div key={row.id} className="whitespace-pre-wrap break-words"><span className="text-muted-foreground">{new Date(row.time).toLocaleTimeString(language)} {row.service}/{row.container_name} [{row.stream}] </span><span>{row.text}</span>{row.truncated && <span className="text-muted-foreground"> [truncated]</span>}</div>)}{!visible.length && <p className="text-muted-foreground">{mode === 'custom' && !range.until ? (zh ? '选择起止时间后查询日志。' : 'Choose start and end times to query logs.') : history.isPending && mode !== 'live' ? (zh ? '正在查询…' : 'Loading…') : zh ? '当前筛选没有日志。已删除容器或已轮转日志无法恢复。' : 'No logs match the current view. Deleted containers and rotated logs cannot be recovered.'}</p>}</div></div>
}
