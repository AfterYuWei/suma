import { useQuery } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { ArrowUpRight, Boxes, Cpu, HardDrive, Layers3, MemoryStick, Network } from 'lucide-react'
import { cn } from '@/lib/utils'
import { LoadingState } from '../components/ui/loading-state'
import { Button } from '../components/ui/button'
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '../components/ui/card'
import { Progress } from '../components/ui/progress'
import { StatusBadge } from '../components/ui/status-badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'
import type { ContainerSummary } from '../features/containers/types'
import { api } from '../lib/api'
import { displayDockerId } from '../lib/docker-id'
import { useI18n } from '../lib/i18n'
import { nodePath } from '../lib/nodes'
import { useUIStore } from '../stores/ui'
import { ResourceFrame } from './images'

interface Overview {
  host: { hostname: string; os: string; kernel: string; architecture: string; uptime_seconds: number; cpu_percent: number; cpus: number; memory_used: number; memory_total: number; disk_used: number; disk_total: number; network_rx: number; network_tx: number }
  docker: { server_version: string; containers_running: number; containers_stopped: number; images: number }
}
interface Audit { id: number; action: string; resource_type: string; resource_name: string; result: string; created_at: string }

const bytes = (value = 0) => value >= 1024 ** 3 ? `${(value / 1024 ** 3).toFixed(1)} GB` : `${(value / 1024 ** 2).toFixed(0)} MB`
const percent = (value = 0, total = 100) => total > 0 ? Math.max(0, Math.min(100, value / total * 100)) : 0

export function OverviewPage() {
  const navigate = useNavigate()
  const nodeID = useUIStore((state) => state.currentNodeID)
  const { language } = useI18n()
  const zh = language === 'zh-CN'
  const overview = useQuery({ queryKey: ['overview', nodeID], queryFn: () => api<Overview>(nodePath(nodeID, '/overview')), refetchInterval: 10_000 })
  const containers = useQuery({ queryKey: ['containers', nodeID], queryFn: () => api<ContainerSummary[]>(nodePath(nodeID, '/containers')), refetchInterval: 10_000 })
  const audits = useQuery({ queryKey: ['audit-logs', nodeID], queryFn: () => api<Audit[]>(`/audit-logs?node_id=${encodeURIComponent(nodeID)}`) })
  const networks = useQuery({ queryKey: ['networks', nodeID], queryFn: () => api<unknown[]>(nodePath(nodeID, '/networks')) })
  const volumes = useQuery({ queryKey: ['volumes', nodeID], queryFn: () => api<unknown[]>(nodePath(nodeID, '/volumes')) })
  const projects = useQuery({ queryKey: ['compose', nodeID], queryFn: () => api<unknown[]>(nodePath(nodeID, '/compose')) })
  const data = overview.data
  const cpu = data?.host.cpu_percent ?? 0

  const resourceCards = [
    { label: zh ? '容器 CPU' : 'Container CPU', value: `${data ? cpu.toFixed(1) : '—'}%`, detail: zh ? '运行容器指标聚合' : 'Running-container aggregate', progress: cpu, icon: Cpu },
    { label: zh ? '容器内存' : 'Container memory', value: bytes(data?.host.memory_used), detail: zh ? `Engine 总内存 ${bytes(data?.host.memory_total)}` : `Engine total ${bytes(data?.host.memory_total)}`, progress: percent(data?.host.memory_used, data?.host.memory_total), icon: MemoryStick },
    { label: zh ? 'Docker 磁盘占用' : 'Docker disk usage', value: bytes(data?.host.disk_used), detail: zh ? 'Docker system df 数据' : 'Docker system df data', icon: HardDrive },
    { label: zh ? '镜像' : 'Images', value: String(data?.docker.images ?? '—'), detail: zh ? 'Docker Engine 镜像数' : 'Docker Engine image count', icon: Boxes },
  ]

  const resourceTotals = [
    { label: zh ? '网络' : 'Networks', value: networks.data?.length ?? 0, icon: Network },
    { label: zh ? '存储卷' : 'Volumes', value: volumes.data?.length ?? 0, icon: HardDrive },
    { label: 'Compose', value: projects.data?.length ?? 0, icon: Layers3 },
  ]

  const engineInfo = [
    { key: zh ? '架构' : 'Architecture', value: data?.host.architecture ?? '—' },
    { key: 'Docker', value: data?.docker.server_version ?? '—' },
    { key: 'vCPU', value: String(data?.host.cpus ?? 0) },
    { key: zh ? '运行 / 停止' : 'Running / stopped', value: `${data?.docker.containers_running ?? 0} / ${data?.docker.containers_stopped ?? 0}` },
  ]

  const stateBadge = (state: string) => <StatusBadge tone={state === 'running' ? 'success' : 'neutral'}>{state}</StatusBadge>

  if (overview.isPending) return <LoadingState label={zh ? '正在加载概览' : 'Loading overview'} rows={8} />

  return <ResourceFrame
    title={data?.host.hostname ?? (zh ? '概览' : 'Overview')}
    detail={`${data?.host.os ?? '—'} · ${data?.host.kernel ?? '—'}`}
    action={<StatusBadge tone={overview.isSuccess ? 'success' : 'warning'}>{overview.isSuccess ? (zh ? 'Docker Engine 在线' : 'Docker Engine online') : (zh ? '正在连接' : 'Connecting')}</StatusBadge>}
  >
    <div className="flex w-full flex-col gap-6">
      <div className="grid w-full gap-4 sm:grid-cols-2 xl:grid-cols-4">
        {resourceCards.map(({ label, value, detail, progress: valueProgress, icon: Icon }) => (
          <Card key={label}>
            <CardHeader>
              <CardTitle className="flex items-center gap-2 text-sm"><Icon className="size-4 text-muted-foreground" />{label}</CardTitle>
            </CardHeader>
            <CardContent className="flex flex-col gap-2">
              <div className="text-2xl leading-none font-semibold tracking-tight tabular-nums">{value}</div>
              <CardDescription>{detail}</CardDescription>
              {valueProgress !== undefined && <Progress value={valueProgress} />}
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
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {(containers.data ?? []).slice(0, 6).map((row) => (
                          <TableRow key={row.id}>
                            <TableCell><a href={`/containers/${row.id}`} className="font-medium underline-offset-4 hover:underline">{row.name}</a></TableCell>
                            <TableCell className="max-w-56 truncate text-muted-foreground" title={row.image}>{row.image}</TableCell>
                            <TableCell>{stateBadge(row.state)}</TableCell>
                            <TableCell className="tabular-nums">{row.ports?.[0]?.public_port ? `${row.ports[0].public_port}:${row.ports[0].private_port}` : '—'}</TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  )}
          </CardContent>
        </Card>

        <div className="flex w-full flex-col gap-4">
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

          <Card>
            <CardHeader><CardTitle>{zh ? '资源统计' : 'Resource totals'}</CardTitle></CardHeader>
            <CardContent>
              <dl className="flex items-start justify-between gap-4">
                {resourceTotals.map(({ label, value, icon: Icon }) => (
                  <div key={label} className="flex flex-col gap-1">
                    <dt className="flex items-center gap-1.5 text-xs text-muted-foreground"><Icon className="size-3.5" />{label}</dt>
                    <dd className="text-lg leading-none font-semibold tabular-nums">{value}</dd>
                  </div>
                ))}
              </dl>
            </CardContent>
          </Card>
        </div>
      </div>
    </div>
  </ResourceFrame>
}
