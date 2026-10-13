import { useFreshness } from '../../features/containers/use-freshness'
import { useSmallScreen } from '../../features/containers/use-small-screen'
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { lazy, Suspense, useState, type ReactElement, type ReactNode } from 'react'
import { Button } from '../ui/button'
import { Popover, PopoverContent, PopoverTrigger } from '../ui/popover'
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from '../ui/sheet'
import { Spinner } from '../ui/spinner'
import { api } from '../../lib/api'
import { nodePath } from '../../lib/nodes'
import { useDateTime } from '../../lib/time-zone'
import type { ContainerDetail, ContainerDiagnostics, MetricPoint } from '../../features/containers/types'
import { sizeLabel } from '../../features/containers/observation'

const MetricCharts = lazy(() => import('../../features/containers/metric-charts').then(m => ({ default: m.MetricCharts })))
export function ObservationOverlay({ open, onOpenChange, trigger, title, children }: { open: boolean; onOpenChange: (open: boolean) => void; trigger: ReactElement; title: string; children: ReactNode }) {
  const small = useSmallScreen()
  if (small) return <Sheet open={open} onOpenChange={onOpenChange}><SheetTrigger render={trigger} /><SheetContent side="bottom" className="max-h-[85dvh] overflow-y-auto"><SheetHeader><SheetTitle>{title}</SheetTitle></SheetHeader><div className="px-4 pb-5">{open && children}</div></SheetContent></Sheet>
  return <Popover open={open} onOpenChange={onOpenChange}><PopoverTrigger render={trigger} /><PopoverContent className="max-h-[75dvh] w-[520px] max-w-[calc(100vw-32px)] overflow-y-auto p-4 shadow-none"><h3 className="font-medium">{title}</h3>{open && children}</PopoverContent></Popover>
}
export function QueryMessage({ pending, error, retry, zh }: { pending: boolean; error: boolean; retry: () => void; zh: boolean }) {
  if (pending) return <div className="flex items-center gap-2 py-4 text-muted-foreground"><Spinner />{zh ? '正在读取…' : 'Loading…'}</div>
  if (error) return <div className="flex items-center justify-between gap-3 py-4 text-destructive">{zh ? '数据暂不可用' : 'Data unavailable'}<Button variant="outline" size="sm" onClick={retry}>{zh ? '重试' : 'Retry'}</Button></div>
  return null
}
export function DiagnosticsContent({ nodeID, containerID, zh, healthOnly = false }: { nodeID: string; containerID: string; zh: boolean; healthOnly?: boolean }) {
  const { formatDateTime } = useDateTime()
  const query = useQuery({ queryKey: ['container-diagnostics', nodeID, containerID], queryFn: ({ signal }) => api<ContainerDiagnostics>(nodePath(nodeID, `/containers/${containerID}/runtime`), { signal }), refetchInterval: 10_000, retry: false, staleTime: 0 })
  const fresh = useFreshness(query.dataUpdatedAt)
  const d = !query.isError && fresh ? query.data : undefined, r = d?.runtime, h = d?.health
  const date = (value?: string | null) => value ? formatDateTime(value) : '—'
  const fields: [string, string][] = [[zh ? '重启次数' : 'Restarts', r?.restart_count?.toString() ?? '—'], [zh ? '重启策略' : 'Restart policy', d?.restart_policy ?? '—'], [zh ? '启动时间' : 'Started', date(r?.started_at)], [zh ? '退出时间' : 'Finished', date(r?.finished_at)], [zh ? '退出码' : 'Exit code', r?.exit_code?.toString() ?? '—'], ['OOM', r?.oom_killed == null ? '—' : r.oom_killed ? zh ? '是' : 'Yes' : zh ? '否' : 'No']]
  const logs = [...(h?.logs ?? [])].reverse()
  const failed = logs.findIndex(log => log.exit_code !== 0)
  const healthNames: Record<string, string> = { healthy: zh ? '健康' : 'Healthy', unhealthy: zh ? '不健康' : 'Unhealthy', starting: zh ? '检查启动中' : 'Starting', none: zh ? '未配置' : 'No check', unknown: zh ? '未知' : 'Unknown' }
  const duration = (value: number | null) => value == null ? '—' : value === 0 ? zh ? '默认' : 'Default' : `${value / 1000}s`
  return <div className="space-y-4 text-left text-sm">
    <QueryMessage pending={query.isPending} error={query.isError || !fresh} retry={() => void query.refetch()} zh={zh} />
    {d && <>
      {!healthOnly && <dl className="space-y-1">{fields.map(([label, value]) => <div key={label} className="flex justify-between gap-4 py-1.5"><dt className="text-muted-foreground">{label}</dt><dd className="break-all text-right font-mono text-xs">{value}</dd></div>)}{r?.state_error && <div className="py-2 text-destructive">{r.state_error}</div>}</dl>}
      <section className="space-y-2"><h4 className="font-medium">{zh ? '健康检查' : 'Health check'} · <span className={r?.health_status === 'unhealthy' ? 'text-destructive' : 'text-muted-foreground'}>{healthNames[r?.health_status ?? 'unknown'] ?? r?.health_status}</span></h4>
        <p className="text-xs text-muted-foreground">{r?.state !== 'running' && r?.state ? zh ? '容器未运行，以下为历史检查结果。' : 'Container is not running; results below are historical.' : null}</p>
        {h?.configured === false ? <p className="text-muted-foreground">{zh ? '未配置健康检查' : 'No health check configured'}</p> : h?.configured == null ? <p>{zh ? '检查配置未知' : 'Health configuration unknown'}</p> : <>
          <dl className="grid grid-cols-2 gap-x-4 gap-y-2 text-xs">{[[zh ? '连续失败' : 'Consecutive failures', h.failing_streak ?? '—'], [zh ? '最近检查' : 'Last check', date(h.last_checked_at)], [zh ? '间隔' : 'Interval', duration(h.interval_ms)], [zh ? '超时' : 'Timeout', duration(h.timeout_ms)], [zh ? '启动宽限期' : 'Start period', duration(h.start_period_ms)], [zh ? '启动检查间隔' : 'Start interval', duration(h.start_interval_ms)], [zh ? '重试次数' : 'Retries', h.retries === 0 ? zh ? '默认' : 'Default' : h.retries ?? '—']].map(([key, value]) => <div key={key}><dt className="text-muted-foreground">{key}</dt><dd className="mt-0.5 break-all">{value}</dd></div>)}</dl>
          {h.command && <pre className="overflow-x-auto whitespace-pre-wrap break-all rounded-sm bg-muted/40 p-2 text-xs">{h.command}</pre>}
          {!h.logs.length && <p className="text-muted-foreground">{zh ? '尚无检查记录' : 'No check results yet'}</p>}
          {logs.map((log, index) => <details key={`${log.started_at}-${index}`} open={index === (failed < 0 ? 0 : failed) || undefined} className="py-2"><summary className="cursor-pointer text-xs"><span className={log.exit_code !== 0 ? 'text-destructive' : ''}>{date(log.finished_at)} · {zh ? '退出码' : 'Exit'} {log.exit_code}</span> · {Math.max(0, Date.parse(log.finished_at) - Date.parse(log.started_at))}ms</summary><pre className="mt-2 max-h-48 overflow-auto whitespace-pre-wrap break-all text-xs">{log.output || '—'}</pre>{(log.output_redacted || log.output_truncated) && <p className="mt-1 text-xs text-muted-foreground">{zh ? '输出已脱敏或截断' : 'Output redacted or truncated'}</p>}</details>)}
        </>}
      </section>
      <div className="flex gap-4 pt-2 text-xs"><Link to="/containers/$containerId" params={{ containerId: containerID }} hash="logs" className="hover:underline">{zh ? '查看日志' : 'View logs'}</Link><Link to="/containers/$containerId" params={{ containerId: containerID }} hash="events" className="hover:underline">{zh ? '查看事件' : 'View events'}</Link></div>
      <p className="text-xs text-muted-foreground">{zh ? '数据更新时间' : 'Observed'}：{date(r?.observed_at)}</p>
    </>}
  </div>
}
export function CopyValue({ value }: { value: string }) {
  const [copied, setCopied] = useState(false)
  return <button type="button" className="break-all text-left font-mono text-xs hover:underline" title={value} aria-label={`Copy ${value}`} onClick={() => { void navigator.clipboard.writeText(value).then(() => setCopied(true)).catch(() => setCopied(false)) }}>{value}{copied && ' ✓'}</button>
}
export function ContainerTopologyContent({ data, zh }: { data: Pick<ContainerDetail, 'networks' | 'mounts'> & { network_mode?: string | null }; zh: boolean }) {
  return <div className="grid gap-5 md:grid-cols-2">
    <section><h4 className="mb-2 text-sm font-medium">{zh ? '网络' : 'Networks'} <span className="text-xs font-normal text-muted-foreground">{data.network_mode ?? '—'}</span></h4>{!data.networks?.length && <p className="text-xs text-muted-foreground">{zh ? '无独立网络端点' : 'No independent network endpoints'}</p>}{data.networks?.map(n => <div key={n.name} className="space-y-1 py-2"><CopyValue value={n.name} /><dl className="space-y-1 text-xs">{[['IPv4', n.ip_address], ['IPv6', n.ipv6_address], [zh ? '网关' : 'Gateway', n.gateway], [zh ? 'IPv6 网关' : 'IPv6 gateway', n.ipv6_gateway], ['MAC', n.mac_address]].map(([label, value]) => <div key={label} className="flex justify-between gap-3"><dt className="text-muted-foreground">{label}</dt><dd>{value ? <CopyValue value={value} /> : zh ? '未分配' : 'Unassigned'}</dd></div>)}</dl></div>)}</section>
    <section><h4 className="mb-2 text-sm font-medium">{zh ? '挂载' : 'Mounts'}</h4>{!data.mounts?.length && <p className="text-xs text-muted-foreground">{zh ? '无挂载' : 'No mounts'}</p>}{data.mounts?.map((m, i) => <div key={`${m.destination}-${i}`} className="space-y-1 py-2 text-xs"><div className="flex justify-between gap-3"><span>{m.type === 'volume' ? m.anonymous === true ? zh ? '匿名卷' : 'Anonymous volume' : m.anonymous === false ? zh ? '具名卷' : 'Named volume' : 'volume' : m.type} {m.type === 'volume' ? m.name || (zh ? '未命名卷' : 'Unnamed volume') : ''}</span><span className="text-muted-foreground">{m.read_write ? zh ? '读写' : 'Read/write' : zh ? '只读' : 'Read-only'}</span></div>{m.source && <CopyValue value={m.source} />}<div>→ <CopyValue value={m.destination} /></div>{m.source.endsWith('docker.sock') && <p className="text-muted-foreground">{zh ? 'Docker Socket 可控制 Engine，只读挂载不限制 API 权限。' : 'Docker socket grants Engine control; a read-only mount does not restrict API permissions.'}</p>}</div>)}</section>
  </div>
}
export function MemoryBar({ used, limit, zh }: { used?: number; limit?: number | null; zh: boolean }) {
  if (used == null || limit == null || limit <= 0) return null
  const ratio = used / limit * 100
  return <div role="meter" aria-label={zh ? '内存限额使用率' : 'Memory limit usage'} aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.min(100, ratio)} aria-valuetext={`${ratio.toFixed(1)}%`} className="mx-auto h-0.5 w-24 overflow-hidden bg-muted"><div className={ratio >= 90 ? 'h-full bg-destructive' : ratio >= 80 ? 'h-full bg-amber-500' : 'h-full bg-muted-foreground/50'} style={{ width: `${Math.min(100, ratio)}%` }} /></div>
}
export function ResourceTrend({ points, limit, unavailable, nodeID, containerID, zh }: { points: MetricPoint[]; limit?: number | null; unavailable: boolean; nodeID: string; containerID: string; zh: boolean }) {
  const [minutes, setMinutes] = useState(1), last = points.at(-1)
  const { formatDateTime } = useDateTime()
  return <div className="space-y-3 text-left"><div className="flex items-center justify-between"><span className="text-xs text-muted-foreground">{unavailable ? zh ? '数据不可用' : 'Data unavailable' : !points.length ? zh ? '采样中' : 'Sampling' :  `CPU ${last?.cpu != null ? `${last.cpu.toFixed(1)}%` : '—'} · ${last?.memory != null ? sizeLabel(last.memory) : '—'} / ${limit == null ? '—' : limit === 0 ? '∞' : sizeLabel(limit)}${limit != null && limit > 0 && last?.memory != null ? ` · ${(last.memory / limit * 100).toFixed(1)}%` : ''}`}</span><div className="flex gap-1">{[1, 5].map(m => <Button key={m} size="xs" variant={m === minutes ? 'secondary' : 'ghost'} onClick={() => setMinutes(m)}>{m} min</Button>)}</div></div><Suspense fallback={<Spinner />}><MetricCharts points={points} minutes={minutes} compact /></Suspense><MemoryBar used={!unavailable && last?.memory != null ? last.memory : undefined} limit={limit} zh={zh} /><Link to="/containers/$containerId" params={{ containerId: containerID }} hash="stats" className="text-xs hover:underline">{zh ? '查看完整统计' : 'View full statistics'}</Link><p className="text-xs text-muted-foreground">{zh ? '数据更新时间' : 'Updated'}: {last ? formatDateTime(new Date(last.time).toISOString()) : '—'}</p><span className="sr-only">{nodeID}</span></div>
}
