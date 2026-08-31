import { useQuery } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { useState } from 'react'
import { Activity, ArrowUpRight, Boxes, Container as ContainerIcon, Cpu, GitPullRequest, HardDrive, MemoryStick, Radio } from 'lucide-react'
import { cn } from '@/lib/utils'
import { LoadingState } from '../components/ui/loading-state'
import { Button } from '../components/ui/button'
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '../components/ui/card'
import { StatusBadge } from '../components/ui/status-badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'
import { TooltipHint } from '../components/ui/tooltip-hint'
import type { ContainerSummary } from '../features/containers/types'
import { api } from '../lib/api'
import { displayDockerId } from '../lib/docker-id'
import { useI18n } from '../lib/i18n'
import { nodePath } from '../lib/nodes'
import { useUIStore } from '../stores/ui'
import { ResourceFrame } from './images'

interface NodeOverview {
  host: { hostname: string; os: string; kernel: string; architecture: string; cpus?: number; uptime_seconds?: number; cpu_percent?: number | null; memory_used?: number | null; memory_total?: number | null; disk_used?: number | null; disk_total?: number | null }
  containers?: { cpu_percent: number; memory_bytes: number }
  docker: { server_version: string; containers_running: number; containers_stopped: number; images: number }
  docker_disk_usage_bytes?: number | null
}
interface Audit { id: number; action: string; resource_type: string; resource_name: string; result: string; created_at: string }
interface FleetNode { id: string; name: string; connection_type: string; enabled: boolean; status: string; engine_version?: string; last_latency_ms?: number; last_checked_at?: string; last_error?: string; hostname?: string; os?: string; containers_running: number; containers_stopped: number; images: number; container_cpu_percent: number; container_memory_bytes: number }
interface FleetOverview { nodes: FleetNode[]; totals: { nodes_total: number; nodes_online: number; nodes_offline: number; nodes_disabled: number; containers_running: number; containers_stopped: number; images: number } }
interface ReleaseSummary { id: number; status: string; commit_sha: string; trigger_type: string; created_at: string }
interface CDProject { name: string; configured: boolean; repository_url?: string; git_ref?: string; reconcile_mode: string; node_ids: string[]; drifted: boolean; runtime_healthy: boolean; drift_reason?: string; active_release?: ReleaseSummary; latest_release?: ReleaseSummary; awaiting_approval: boolean; releasing: boolean }
interface CDOverview { projects: CDProject[]; totals: { projects: number; configured: number; releasing: number; awaiting_approval: number; drifted: number; healthy: number } }
interface Task { id: string; name: string; status: string; progress: number; created_at: string }

const bytes = (value?: number | null) => {
  const v = value ?? 0
  return v >= 1024 ** 3 ? `${(v / 1024 ** 3).toFixed(1)} GB` : `${(v / 1024 ** 2).toFixed(0)} MB`
}
const percent = (value = 0, total = 100) => total > 0 ? Math.max(0, Math.min(100, value / total * 100)) : 0
const latencyClass = (ms?: number) => !ms ? 'text-muted-foreground' : ms < 150 ? 'text-emerald-600 dark:text-emerald-400' : ms < 500 ? 'text-amber-600 dark:text-amber-400' : 'text-red-600 dark:text-red-400'

function releaseTone(status: string) {
  if (status === 'succeeded' || status === 'approved') return 'success'
  if (status === 'failed' || status === 'partial_failed' || status === 'rollback_failed') return 'critical'
  if (status === 'awaiting_approval' || ['validating', 'pulling', 'deploying', 'verifying', 'rolling_back'].includes(status)) return 'warning'
  return 'neutral'
}

export function OverviewPage() {
  const navigate = useNavigate()
  const nodeID = useUIStore((state) => state.currentNodeID)
  const [detailNodeID, setDetailNodeID] = useState<string | null>(null)
  const effectiveNodeID = detailNodeID ?? nodeID
  const { language } = useI18n()
  const zh = language === 'zh-CN'

  const fleet = useQuery({ queryKey: ['fleet-overview'], queryFn: () => api<FleetOverview>('/fleet/overview'), refetchInterval: 15_000 })
  const cd = useQuery({ queryKey: ['cd-overview'], queryFn: () => api<CDOverview>('/cd/overview'), refetchInterval: 10_000 })
  const tasks = useQuery({ queryKey: ['tasks', 'all'], queryFn: () => api<Task[]>('/tasks?scope=all'), refetchInterval: 10_000 })
  const overview = useQuery({ queryKey: ['overview', effectiveNodeID], queryFn: () => api<NodeOverview>(nodePath(effectiveNodeID, '/overview')), refetchInterval: 10_000 })
  const containers = useQuery({ queryKey: ['containers', effectiveNodeID], queryFn: () => api<ContainerSummary[]>(nodePath(effectiveNodeID, '/containers')), refetchInterval: 10_000 })
  const audits = useQuery({ queryKey: ['audit-logs', 'current', effectiveNodeID], queryFn: () => api<Audit[]>(nodePath(effectiveNodeID, '/audit-logs')) })
  const networks = useQuery({ queryKey: ['networks', effectiveNodeID], queryFn: () => api<unknown[]>(nodePath(effectiveNodeID, '/networks')) })
  const volumes = useQuery({ queryKey: ['volumes', effectiveNodeID], queryFn: () => api<unknown[]>(nodePath(effectiveNodeID, '/volumes')) })
  const projects = useQuery({ queryKey: ['projects', effectiveNodeID], queryFn: () => api<unknown[]>(nodePath(effectiveNodeID, '/projects')) })

  const data = overview.data
  const host = data?.host
  const containerCpu = data?.containers?.cpu_percent ?? 0
  const containerMemory = data?.containers?.memory_bytes ?? 0
  const dockerDisk = data?.docker_disk_usage_bytes ?? 0
  const totals = fleet.data?.totals
  const cdTotals = cd.data?.totals
  const nodeNames = new Map((fleet.data?.nodes ?? []).map((node) => [node.id, node.name]))
  const runningTasks = (tasks.data ?? []).filter((row) => row.status === 'running' || row.status === 'pending')
  const failedTasks = (tasks.data ?? []).filter((row) => row.status === 'failed')

  type MetricBar = { percent: number; segment: number; legend: string }
  const metricNote = zh ? ' · 未采集到宿主机指标' : ' · host metrics unavailable'
  const cpuHasHost = host?.cpu_percent != null
  const cpuValue = Math.min(100, cpuHasHost ? host!.cpu_percent! : containerCpu)
  const memTotal = host?.memory_total != null ? host.memory_total : null
  const memHasHost = host?.memory_used != null && memTotal != null
  const memValue = memHasHost ? host!.memory_used! : containerMemory
  const diskHasHost = host?.disk_used != null && host?.disk_total != null
  const diskValue = diskHasHost ? host!.disk_used! : dockerDisk

  const uptime = (seconds?: number | null) => {
    if (seconds == null) return '—'
    const days = Math.floor(seconds / 86400)
    const hours = Math.floor((seconds % 86400) / 3600)
    const minutes = Math.floor((seconds % 3600) / 60)
    if (zh) return `${days ? `${days} 天 ` : ''}${hours ? `${hours} 小时 ` : ''}${minutes} 分钟`
    return `${days ? `${days}d ` : ''}${hours ? `${hours}h ` : ''}${minutes}m`
  }

  const fleetCards: Array<{ label: string; value: string; detail: string; icon: typeof Cpu; tone?: 'success' | 'warning' | 'critical' | 'neutral' }> = [
    {
      label: zh ? '节点' : 'Nodes',
      value: totals ? `${totals.nodes_online}/${totals.nodes_total}` : '—',
      detail: totals
        ? [zh ? `${totals.nodes_online} 在线` : `${totals.nodes_online} online`, totals.nodes_offline ? (zh ? `${totals.nodes_offline} 离线` : `${totals.nodes_offline} offline`) : '', totals.nodes_disabled ? (zh ? `${totals.nodes_disabled} 禁用` : `${totals.nodes_disabled} disabled`) : ''].filter(Boolean).join(' · ')
        : zh ? '正在采集节点状态' : 'collecting node state',
      icon: Radio,
      tone: totals && totals.nodes_offline > 0 ? 'warning' : 'success',
    },
    {
      label: zh ? '容器（全部节点）' : 'Containers (fleet)',
      value: totals ? String(totals.containers_running) : '—',
      detail: totals ? (zh ? `已停止 ${totals.containers_stopped} · 全部节点合计` : `${totals.containers_stopped} stopped · all nodes`) : zh ? '正在采集' : 'collecting',
      icon: ContainerIcon,
    },
    {
      label: zh ? '镜像（全部节点）' : 'Images (fleet)',
      value: totals ? String(totals.images) : '—',
      detail: totals ? (zh ? `分布于 ${totals.nodes_total} 个节点` : `across ${totals.nodes_total} nodes`) : zh ? '正在采集' : 'collecting',
      icon: Boxes,
    },
    {
      label: zh ? '进行中任务' : 'Active tasks',
      value: String(runningTasks.length),
      detail: failedTasks.length
        ? (zh ? `${failedTasks.length} 个失败任务需关注` : `${failedTasks.length} failed tasks`)
        : (zh ? '最近无失败任务' : 'no recent failures'),
      icon: Activity,
      tone: runningTasks.length ? 'warning' : failedTasks.length ? 'critical' : 'neutral',
    },
  ]

  const resourceCards: Array<{ label: string; value: string; detail: string; bar?: MetricBar; icon: typeof Cpu }> = [
    {
      label: zh ? '宿主机 CPU' : 'Host CPU',
      value: `${cpuValue.toFixed(1)}%`,
      detail: zh
        ? `${host?.cpus ?? '—'} vCPU · 运行容器合计 ${containerCpu.toFixed(1)}%${cpuHasHost ? '' : metricNote}`
        : `${host?.cpus ?? '—'} vCPUs · containers total ${containerCpu.toFixed(1)}%${cpuHasHost ? '' : metricNote}`,
      bar: { percent: Math.max(cpuValue, Math.min(100, containerCpu)), segment: Math.min(100, containerCpu), legend: zh ? '运行容器' : 'Containers' },
      icon: Cpu,
    },
    {
      label: zh ? '宿主机内存' : 'Host memory',
      value: bytes(memValue),
      detail: zh
        ? `${memHasHost ? '' : (memTotal ? '引擎总内存 · ' : '')}总计 ${bytes(memTotal ?? 0)} · 运行容器合计 ${bytes(containerMemory)}${!memHasHost && !memTotal ? metricNote : ''}`
        : `Total ${bytes(memTotal ?? 0)} · containers total ${bytes(containerMemory)}${!memHasHost ? metricNote : ''}`,
      bar: memTotal ? { percent: percent(memValue, memTotal), segment: percent(containerMemory, memTotal), legend: zh ? '运行容器' : 'Containers' } : undefined,
      icon: MemoryStick,
    },
    {
      label: zh ? '磁盘占用' : 'Disk usage',
      value: bytes(diskValue),
      detail: diskHasHost
        ? zh
          ? `磁盘 ${bytes(host!.disk_used)} / 总量 ${bytes(host!.disk_total!)} · Docker 占用 ${bytes(dockerDisk)}`
          : `${bytes(host!.disk_used)} / total ${bytes(host!.disk_total!)} · Docker uses ${bytes(dockerDisk)}`
        : zh
          ? `宿主机磁盘总量不可用 · Docker 占用 ${bytes(dockerDisk)}`
          : `Host disk total unavailable · Docker uses ${bytes(dockerDisk)}`,
      bar: diskHasHost
        ? { percent: percent(diskValue, host!.disk_total!), segment: percent(dockerDisk, host!.disk_total!), legend: 'Docker' }
        : { percent: 100, segment: 100, legend: 'Docker' },
      icon: HardDrive,
    },
    { label: zh ? '镜像' : 'Images', value: String(data?.docker.images ?? '—'), detail: zh ? 'Docker Engine 镜像数' : 'Docker Engine image count', icon: Boxes },
  ]

  const engineInfo = [
    { key: zh ? '运行时长' : 'Uptime', value: uptime(host?.uptime_seconds) },
    { key: zh ? '架构' : 'Architecture', value: data?.host.architecture ?? '—' },
    { key: 'Docker', value: data?.docker.server_version ?? '—' },
    { key: 'vCPU', value: String(data?.host.cpus ?? 0) },
    { key: zh ? '运行 / 停止' : 'Running / stopped', value: `${data?.docker.containers_running ?? 0} / ${data?.docker.containers_stopped ?? 0}` },
    { key: zh ? '网络 / 存储卷' : 'Networks / volumes', value: `${networks.data?.length ?? 0} / ${volumes.data?.length ?? 0}` },
    { key: 'Compose', value: String(projects.data?.length ?? 0) },
  ]

  const stateBadge = (state: string) => <StatusBadge tone={state === 'running' ? 'success' : 'neutral'}>{state}</StatusBadge>
  const cdStatus = (project: CDProject) => {
    if (!project.configured) return <StatusBadge tone="neutral">{zh ? '未配置' : 'Not configured'}</StatusBadge>
    const release = project.active_release ?? project.latest_release
    if (!release) return <StatusBadge tone="neutral">{zh ? '未同步' : 'Not synced'}</StatusBadge>
    return <StatusBadge tone={releaseTone(release.status)}>{release.status.replaceAll('_', ' ')}</StatusBadge>
  }
  const cdDrift = (project: CDProject) => {
    if (!project.configured) return null
    if (project.drifted) return <TooltipHint content={project.drift_reason}><span className="inline-flex items-center gap-1.5 text-xs text-amber-600 dark:text-amber-400"><span className="size-2 rounded-full bg-amber-500" />{zh ? '偏移' : 'Drift'}</span></TooltipHint>
    if (project.active_release && project.runtime_healthy) return <span className="inline-flex items-center gap-1.5 text-xs text-emerald-600 dark:text-emerald-400"><span className="size-2 rounded-full bg-emerald-500" />{zh ? '已同步' : 'In sync'}</span>
    return null
  }
  const modeLabel = (mode: string) => mode === 'auto' ? (zh ? '自动' : 'Auto') : mode === 'observe' ? (zh ? '观察' : 'Observe') : zh ? '手动' : 'Manual'

  if (fleet.isPending && cd.isPending && overview.isPending) return <LoadingState label={zh ? '正在加载概览' : 'Loading overview'} rows={8} />

  return <ResourceFrame
    title={zh ? '概览' : 'Overview'}
    detail={zh ? 'SUMA 控制平面 · 多节点 Docker 总览' : 'SUMA control plane · fleet-wide Docker overview'}
  >
    <div className="flex w-full flex-col gap-6">
      <div className="grid w-full gap-4 sm:grid-cols-2 xl:grid-cols-4">
        {fleetCards.map(({ label, value, detail, icon: Icon, tone }) => (
          <Card key={label}>
            <CardHeader>
              <CardTitle className="flex items-center gap-2 text-sm"><Icon className="size-4 text-muted-foreground" />{label}</CardTitle>
            </CardHeader>
            <CardContent className="flex flex-col gap-2">
              <div className="text-2xl leading-none font-semibold tracking-tight tabular-nums">{value}</div>
              <CardDescription className="flex items-center gap-1.5">
                {tone === 'success' && <span className="size-2 rounded-full bg-emerald-500" />}
                {tone === 'warning' && <span className="size-2 rounded-full bg-amber-500" />}
                {tone === 'critical' && <span className="size-2 rounded-full bg-red-500" />}
                {detail}
              </CardDescription>
            </CardContent>
          </Card>
        ))}
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2"><GitPullRequest className="size-4 text-muted-foreground" />{zh ? '持续交付' : 'Continuous delivery'}</CardTitle>
          <CardDescription>{zh ? '控制平面 · 发布与漂移状态' : 'Control plane · releases and drift'}</CardDescription>
          <CardAction className="flex items-center gap-2">
            {cdTotals && cdTotals.projects > 0 && (
              <div className="flex items-center gap-2 text-xs text-muted-foreground">
                {cdTotals.releasing > 0 && <StatusBadge tone="warning">{zh ? `${cdTotals.releasing} 发布中` : `${cdTotals.releasing} releasing`}</StatusBadge>}
                {cdTotals.awaiting_approval > 0 && <StatusBadge tone="warning">{zh ? `${cdTotals.awaiting_approval} 待批准` : `${cdTotals.awaiting_approval} awaiting`}</StatusBadge>}
                {cdTotals.drifted > 0 && <StatusBadge tone="warning">{zh ? `${cdTotals.drifted} 偏移` : `${cdTotals.drifted} drift`}</StatusBadge>}
                {cdTotals.healthy > 0 && <StatusBadge tone="success">{zh ? `${cdTotals.healthy} 健康` : `${cdTotals.healthy} healthy`}</StatusBadge>}
              </div>
            )}
            <Button variant="ghost" size="sm" className="text-muted-foreground" onClick={() => void navigate({ to: '/continuous-delivery' })}><ArrowUpRight className="size-4" />{zh ? '查看全部' : 'View all'}</Button>
          </CardAction>
        </CardHeader>
        <CardContent>
          {cd.isPending
            ? <LoadingState embedded rows={3} label={zh ? '正在加载交付项目' : 'Loading delivery projects'} />
            : (cd.data?.projects ?? []).length === 0
              ? <p className="py-6 text-center text-sm text-muted-foreground">{zh ? '暂无交付项目' : 'No delivery projects'}</p>
              : (
                  <div className="flex flex-col divide-y divide-border">
                    {(cd.data?.projects ?? []).map((project) => {
                      const release = project.active_release ?? project.latest_release
                      return (
                        <div key={project.name} className="flex flex-wrap items-center gap-x-4 gap-y-1.5 py-2.5 first:pt-0 last:pb-0">
                          <div className="min-w-40 flex-1 basis-48">
                            <Link to="/continuous-delivery/$projectName" params={{ projectName: project.name }} className="text-sm font-medium underline-offset-4 hover:underline">{project.name}</Link>
                            <TooltipHint content={project.repository_url}><span className="block truncate text-xs text-muted-foreground">{project.repository_url || (zh ? '尚未配置 Git 仓库' : 'Git repository not configured')}</span></TooltipHint>
                          </div>
                          {project.configured && <span className="text-xs text-muted-foreground">{modeLabel(project.reconcile_mode)}</span>}
                          <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
                            {project.node_ids.map((id) => <span key={id} className="rounded bg-muted px-1.5 py-0.5">{nodeNames.get(id) ?? id}</span>)}
                          </div>
                          {cdStatus(project)}
                          {cdDrift(project)}
                          {release && (
                            <TooltipHint content={release.commit_sha} className="ml-auto"><span className="text-xs text-muted-foreground tabular-nums">
                              #{release.id} · {release.commit_sha.slice(0, 8)} · {new Date(release.created_at).toLocaleString(language)}
                            </span></TooltipHint>
                          )}
                        </div>
                      )
                    })}
                  </div>
                )}
        </CardContent>
      </Card>

      <div className="grid w-full gap-4 xl:grid-cols-[minmax(0,3fr)_minmax(300px,1fr)]">
        <Card>
          <CardHeader>
            <CardTitle>{zh ? '节点' : 'Nodes'}</CardTitle>
            <CardDescription>{zh ? '点击行查看该节点的详细数据' : 'Click a row to inspect a node'}</CardDescription>
            <CardAction>
              <Button variant="ghost" size="sm" className="text-muted-foreground" onClick={() => void navigate({ to: '/nodes' })}><ArrowUpRight className="size-4" />{zh ? '管理' : 'Manage'}</Button>
            </CardAction>
          </CardHeader>
          <CardContent>
            {fleet.isPending
              ? <LoadingState embedded rows={4} label={zh ? '正在加载节点' : 'Loading nodes'} />
              : (fleet.data?.nodes ?? []).length === 0
                ? <p className="py-6 text-center text-sm text-muted-foreground">{zh ? '暂无节点' : 'No nodes'}</p>
                : (
                    <Table className="border-separate border-spacing-x-0 border-spacing-y-0.5">
                      <TableHeader>
                        <TableRow className="hover:bg-transparent [&>th]:border-b [&>th]:pb-2 [&>th:first-child]:rounded-tl-lg [&>th:last-child]:rounded-tr-lg">
                          <TableHead>{zh ? '节点' : 'Node'}</TableHead>
                          <TableHead className="w-20">{zh ? '协议' : 'Proto'}</TableHead>
                          <TableHead className="w-24">{zh ? '延迟' : 'Latency'}</TableHead>
                          <TableHead className="w-20">{zh ? '容器' : 'Cont.'}</TableHead>
                          <TableHead className="w-16">{zh ? '镜像' : 'Img.'}</TableHead>
                          <TableHead className="w-20">{zh ? '容器 CPU' : 'CPU'}</TableHead>
                          <TableHead className="w-24">{zh ? '容器内存' : 'Memory'}</TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {(fleet.data?.nodes ?? []).map((node) => {
                          const selected = node.id === effectiveNodeID
                          return (
                            <TableRow
                              key={node.id}
                              aria-selected={selected}
                              tabIndex={0}
                              className={cn(
                                'cursor-pointer transition-colors outline-none',
                                'hover:bg-transparent [&>td]:transition-colors',
                                '[&>td:first-child]:rounded-l-lg [&>td:last-child]:rounded-r-lg',
                                selected
                                  ? '[&:hover>td]:bg-primary/[0.045] [&>td]:bg-primary/[0.06]'
                                  : 'hover:[&>td]:bg-muted/40 [&:focus-visible>td]:bg-muted/40',
                              )}
                              onClick={() => setDetailNodeID(node.id)}
                              onKeyDown={(event) => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); setDetailNodeID(node.id) } }}
                            >
                            <TableCell>
                              <div className="flex items-center gap-2">
                                <span className={cn('size-2 shrink-0 rounded-full', !node.enabled ? 'bg-zinc-400' : node.status === 'online' ? 'bg-emerald-500' : 'bg-red-500')} />
                                <TooltipHint content={node.last_error || `${node.hostname ?? ''} ${node.os ?? ''}`}><span className={cn('text-sm', selected && 'font-medium')}>{node.name}</span></TooltipHint>
                              </div>
                            </TableCell>
                            <TableCell className="text-muted-foreground">{node.connection_type}</TableCell>
                            <TableCell>
                              {!node.enabled
                                ? <span className="text-xs text-muted-foreground">{zh ? '禁用' : 'Disabled'}</span>
                                : node.status !== 'online'
                                  ? <span className="text-xs text-muted-foreground">{zh ? '离线' : 'Offline'}</span>
                                  : <span className={cn('font-mono text-xs tabular-nums', latencyClass(node.last_latency_ms))}>{node.last_latency_ms ?? 0} ms</span>}
                            </TableCell>
                            <TableCell className="tabular-nums">{node.containers_running}<span className="text-muted-foreground">/{node.containers_running + node.containers_stopped}</span></TableCell>
                            <TableCell className="tabular-nums">{node.images}</TableCell>
                            <TableCell className="tabular-nums">{node.container_cpu_percent.toFixed(1)}%</TableCell>
                            <TableCell className="tabular-nums">{bytes(node.container_memory_bytes)}</TableCell>
                            </TableRow>
                          )
                        })}
                      </TableBody>
                    </Table>
                  )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>{zh ? '最近活动' : 'Recent activity'}</CardTitle>
            <CardAction>
              <Button variant="ghost" size="icon-sm" aria-label={zh ? '查看审计日志' : 'View audit logs'} onClick={() => void navigate({ to: '/audit-logs' })}><ArrowUpRight /></Button>
            </CardAction>
          </CardHeader>
          <CardContent>
            {audits.isPending
              ? <LoadingState embedded compact rows={4} label={zh ? '正在加载最近活动' : 'Loading recent activity'} />
              : (audits.data ?? []).length === 0
                ? <p className="py-6 text-center text-sm text-muted-foreground">{zh ? '暂无活动记录' : 'No recent activity'}</p>
                : (
                    <ol className="ml-1.5 flex list-none flex-col border-l border-border">
                      {(audits.data ?? []).slice(0, 5).map((row) => (
                        <li key={row.id} className="relative pb-4 pl-5 last:pb-0">
                          <span className={cn('absolute top-1 -left-[7px] size-2.5 rounded-full border-2 border-card', row.result === 'success' ? 'bg-emerald-500' : 'bg-red-500')} />
                          <div className="text-sm font-medium">{row.resource_name ? (row.resource_type === 'container' ? displayDockerId(row.resource_name) : row.resource_name) : row.action}</div>
                          <div className="text-xs text-muted-foreground">{new Date(row.created_at).toLocaleTimeString(language)} · {row.action}</div>
                        </li>
                      ))}
                    </ol>
                  )}
          </CardContent>
        </Card>
      </div>

      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <h3 className="text-sm font-semibold">{zh ? '节点详情' : 'Node detail'}</h3>
        <span className="text-sm text-muted-foreground">{nodeNames.get(effectiveNodeID) ?? effectiveNodeID}{host ? ` · ${host.hostname} · ${host.os} · ${host.kernel}` : ''}</span>
      </div>

      <div className="grid w-full gap-4 sm:grid-cols-2 xl:grid-cols-4">
        {resourceCards.map(({ label, value, detail, bar, icon: Icon }) => (
          <Card key={label}>
            <CardHeader>
              <CardTitle className="flex items-center gap-2 text-sm"><Icon className="size-4 text-muted-foreground" />{label}</CardTitle>
            </CardHeader>
            <CardContent className="flex flex-col gap-2">
              <div className="text-2xl leading-none font-semibold tracking-tight tabular-nums">{value}</div>
              <CardDescription>{detail}</CardDescription>
              {bar && (
                <div className="flex items-center gap-2">
                  <div className="flex h-1 flex-1 overflow-hidden rounded-full bg-muted">
                    {bar.percent > bar.segment && <div className="h-full bg-primary transition-all" style={{ width: `${bar.percent - bar.segment}%` }} />}
                    {bar.segment > 0 && <div className="h-full bg-amber-500 transition-all" style={{ width: `${bar.segment}%` }} />}
                  </div>
                  <span className="flex shrink-0 items-center gap-1 text-xs text-muted-foreground tabular-nums">
                    <span className="size-2 rounded-full bg-amber-500" />{bar.legend}
                  </span>
                </div>
              )}
            </CardContent>
          </Card>
        ))}
      </div>

      <div className="grid w-full gap-4 xl:grid-cols-[minmax(0,3fr)_minmax(300px,1fr)]">
        <Card>
          <CardHeader>
            <CardTitle>{zh ? '活跃容器' : 'Active containers'}</CardTitle>
            <CardAction>
              <Button variant="ghost" size="sm" className="text-muted-foreground" onClick={() => void navigate({ to: '/containers' })}><ArrowUpRight className="size-4" />{zh ? '查看全部' : 'View all'}</Button>
            </CardAction>
          </CardHeader>
          <CardContent>
            {containers.isPending
              ? <LoadingState embedded rows={5} label={zh ? '正在加载容器' : 'Loading containers'} />
              : (containers.data?.slice(0, 6) ?? []).length === 0
                ? <p className="py-8 text-center text-sm text-muted-foreground">{zh ? '暂无容器' : 'No containers'}</p>
                : (
                    <Table>
                      <TableHeader>
                        <TableRow>
                          <TableHead>{zh ? '容器' : 'Container'}</TableHead>
                          <TableHead>{zh ? '镜像' : 'Image'}</TableHead>
                          <TableHead className="w-24">{zh ? '状态' : 'State'}</TableHead>
                          <TableHead className="w-20">{zh ? '端口' : 'Port'}</TableHead>
                          <TableHead className="w-20">{zh ? 'CPU' : 'CPU'}</TableHead>
                          <TableHead className="w-24">{zh ? '内存' : 'Memory'}</TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {(containers.data ?? []).slice(0, 6).map((row) => (
                          <TableRow key={row.id}>
                            <TableCell><a href={`/containers/${row.id}`} className="font-medium underline-offset-4 hover:underline">{row.name}</a></TableCell>
                            <TableCell className="max-w-56 text-muted-foreground"><TooltipHint content={row.image}><span className="block truncate">{row.image}</span></TooltipHint></TableCell>
                            <TableCell>{stateBadge(row.state)}</TableCell>
                            <TableCell className="tabular-nums">{row.ports?.[0]?.public_port ? `${row.ports[0].public_port}:${row.ports[0].private_port}` : '—'}</TableCell>
                            <TableCell className="tabular-nums">{row.cpu_percent?.toFixed(1) ?? '—'}%</TableCell>
                            <TableCell className="tabular-nums">{bytes(row.memory_bytes)}</TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader><CardTitle>{zh ? 'Engine 信息' : 'Engine information'}</CardTitle></CardHeader>
          <CardContent>
            <dl className="flex flex-col divide-y divide-border text-sm">
              {engineInfo.map((item) => (
                <div key={item.key} className="flex items-center justify-between gap-4 py-1.5 first:pt-0 last:pb-0">
                  <dt className="text-muted-foreground">{item.key}</dt>
                  <dd className="font-medium tabular-nums">{item.value}</dd>
                </div>
              ))}
            </dl>
          </CardContent>
        </Card>
      </div>
    </div>
  </ResourceFrame>
}
