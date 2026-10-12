import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { RefreshCw } from 'lucide-react'
import { lazy, Suspense, type ReactNode } from 'react'
import type { ContainerOverview, ContainerRuntime, MetricPoint } from '../../features/containers/types'
import { sizeLabel } from '../../features/containers/observation'
import { api } from '../../lib/api'
import { nodePath } from '../../lib/nodes'
import { Button } from '../ui/button'
import { CopyValue, QueryMessage } from './container-observation'

const MetricCharts = lazy(() => import('../../features/containers/metric-charts').then(m => ({ default: m.MetricCharts })))

function QuickTrends({ points, available, pending, state, zh }: { points: MetricPoint[]; available: boolean; pending: boolean; state: string; zh: boolean }) {
  const last = points.at(-1)
  const status = state !== 'running' ? state === 'syncing' ? zh ? '同步中' : 'Syncing' : points.length ? zh ? '历史采样' : 'Historical' : zh ? '未运行' : 'Not running' : pending ? zh ? '采样中' : 'Sampling' : !available ? zh ? '采样不可用' : 'Samples unavailable' : points.length < 2 ? zh ? '采样中' : 'Sampling' : zh ? '近 5 分钟' : 'Last 5 min'
  return <section aria-label={zh ? '资源趋势' : 'Resource trends'} className="min-w-0 space-y-2">
    <div className="flex items-center justify-between gap-2 text-xs"><h4 className="font-medium">{zh ? '资源趋势' : 'Resource trends'}</h4><span className="text-muted-foreground">{status}</span></div>
    <div className="grid grid-cols-2 gap-4 text-xs tabular-nums"><div><span aria-hidden="true" className="mr-1.5 inline-block size-1.5 rounded-full bg-foreground" /><span className="text-muted-foreground">CPU</span><span className="ml-2 font-mono">{available && last?.cpu != null ? `${last.cpu.toFixed(1)}%` : '—'}</span></div><div><span aria-hidden="true" className="mr-1.5 inline-block size-1.5 rounded-full bg-muted-foreground" /><span className="text-muted-foreground">{zh ? '内存' : 'Memory'}</span><span className="ml-2 font-mono">{available && last?.memory != null ? sizeLabel(last.memory) : '—'}</span></div></div>
    <Suspense fallback={<div className="h-16" />}><MetricCharts points={points} minutes={5} sparkline /></Suspense>
  </section>
}

function SummarySection({ title, extra, children }: { title: string; extra?: ReactNode; children: ReactNode }) {
  return <section className="min-w-0 space-y-2"><h4 className="flex items-center gap-2 text-xs font-medium">{title}{extra}</h4>{children}</section>
}

export function ContainerOverviewContent({ nodeID, containerID, zh, points, available, pending, state, runtime: r }: { nodeID: string; containerID: string; zh: boolean; points: MetricPoint[]; available: boolean; pending: boolean; state: string; runtime?: ContainerRuntime }) {
  const query = useQuery({ queryKey: ['container-overview', nodeID, containerID, 'topology'], queryFn: ({ signal }) => api<ContainerOverview>(nodePath(nodeID, `/containers/${containerID}/overview`), { signal }), retry: false, staleTime: 30_000, refetchOnWindowFocus: false })
  const d = !query.isError ? query.data : undefined
  const issues = [
    r?.oom_killed === true ? 'OOM' : '',
    r?.exit_code != null && r.exit_code !== 0 ? `${zh ? '退出码' : 'Exit code'} ${r.exit_code}` : '',
    r?.health_status === 'unhealthy' ? zh ? '健康检查失败' : 'Health check failed' : '',
    state === 'restarting' ? zh ? '容器正在重启' : 'Container is restarting' : '',
  ].filter(Boolean)
  const specialMode = d?.network_mode === 'host' || d?.network_mode === 'none' || d?.network_mode?.startsWith('container:') ? d.network_mode : ''
  const sections = d ? Number(d.networks.length > 0 || !!specialMode) + Number(d.mounts.length > 0) + Number(d.ports.length > 0) : 0
  const more = (count: number, noun: string) => <Link to="/containers/$containerId" params={{ containerId: containerID }} className="block text-xs text-muted-foreground hover:underline">{zh ? `另 ${count} ${noun}，查看详情` : `${count} more ${noun} · View details`}</Link>
  return <div className="space-y-3 whitespace-normal text-left text-xs">
    {(issues.length > 0 || r?.state_error?.trim()) && <div role="status" className="space-y-1 text-destructive">{issues.length > 0 && <p>{issues.join(' · ')}</p>}{r?.state_error?.trim() && <p className="line-clamp-2 break-all" title={r.state_error}>{r.state_error}</p>}</div>}
    <div className="grid min-w-0 gap-5 lg:grid-cols-[minmax(0,1.2fr)_minmax(0,2.6fr)]">
    <QuickTrends points={points} available={available} pending={pending} state={state} zh={zh} />
    <div className="min-w-0">
    <QueryMessage pending={query.isPending} error={query.isError} retry={() => void query.refetch()} zh={zh} />
    {d && <>
      <div className={`grid gap-4 ${sections === 3 ? 'md:grid-cols-3' : sections === 2 ? 'md:grid-cols-2' : ''}`}>
        {(d.networks.length > 0 || specialMode) && <SummarySection title={zh ? '网络' : 'Networks'} extra={specialMode && <span className="min-w-0 truncate font-normal text-muted-foreground" title={specialMode}>{specialMode}</span>}>
          {d.networks.slice(0, 3).map(n => <div key={n.name} className="min-w-0 space-y-1"><div className="truncate [&>button]:max-w-full [&>button]:truncate"><CopyValue value={n.name} /></div><div className="flex flex-wrap gap-x-3 gap-y-1 text-muted-foreground">{n.ip_address && <CopyValue value={n.ip_address} />}{n.ipv6_address && <CopyValue value={n.ipv6_address} />}</div></div>)}
          {d.counts.networks > d.networks.length && more(d.counts.networks - d.networks.length, zh ? '个网络' : 'networks')}
        </SummarySection>}
        {d.mounts.length > 0 && <SummarySection title={zh ? '挂载' : 'Mounts'}>
          {d.mounts.slice(0, 3).map((m, index) => <div key={`${m.destination}-${index}`} className="min-w-0 space-y-1"><div className="flex items-start justify-between gap-3"><div className="min-w-0 truncate [&>button]:max-w-full [&>button]:truncate">{m.type === 'volume' && m.anonymous === true ? <span className="text-muted-foreground" title={m.name}>{zh ? '匿名卷' : 'Anonymous volume'}</span> : <CopyValue value={m.type === 'volume' ? m.name || (zh ? '存储卷' : 'Volume') : m.source || m.type} />}</div><span className="shrink-0 text-muted-foreground">{m.read_write ? zh ? '读写' : 'Read/write' : zh ? '只读' : 'Read-only'}</span></div><div className="flex min-w-0 items-start gap-1 text-muted-foreground"><span>→</span><div className="min-w-0 truncate [&>button]:max-w-full [&>button]:truncate"><CopyValue value={m.destination} /></div></div></div>)}
          {d.counts.mounts > d.mounts.length && more(d.counts.mounts - d.mounts.length, zh ? '个挂载' : 'mounts')}
        </SummarySection>}
        {d.ports.length > 0 && <SummarySection title={zh ? '端口' : 'Ports'}>
          {d.ports.slice(0, 4).map((p, index) => <div key={`${p.private_port}-${p.type}-${p.ip}-${p.public_port}-${index}`} className="flex min-w-0 items-center justify-between gap-2"><span className="min-w-0 break-all font-mono">{p.host_binding ? `${p.ip?.includes(':') ? `[${p.ip}]` : p.ip || '*'}:${p.public_port || (zh ? '待分配' : 'Pending')} → ${p.private_port}` : p.private_port} <span className="text-muted-foreground">{p.type}</span></span>{!p.host_binding && <span className="shrink-0 text-muted-foreground">{zh ? '未发布' : 'Unpublished'}</span>}</div>)}
          {d.counts.ports > d.ports.length && more(d.counts.ports - d.ports.length, zh ? '个映射' : 'mappings')}
        </SummarySection>}
      </div>
      {sections === 0 && <p className="text-muted-foreground">{zh ? '暂无网络、挂载或端口信息' : 'No network, mount or port information available'}</p>}
    </>}
    </div></div>
    <div className="flex items-center justify-end gap-2"><Button variant="ghost" size="icon-xs" disabled={query.isFetching} onClick={() => void query.refetch()} aria-label={zh ? '刷新容器概览' : 'Refresh container overview'} title={zh ? '刷新' : 'Refresh'}><RefreshCw className={query.isFetching ? 'animate-spin' : undefined} /></Button><Link to="/containers/$containerId" params={{ containerId: containerID }} className="text-muted-foreground hover:text-foreground hover:underline">{zh ? '查看详情' : 'View details'} →</Link></div>
  </div>
}
