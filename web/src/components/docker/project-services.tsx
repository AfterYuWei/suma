import { Link } from '@tanstack/react-router'
import { ChevronRight } from 'lucide-react'
import { useState } from 'react'
import type { ContainerPort, ContainerSummary } from '../../features/containers/types'
import {
  ImageUpdateCheck,
  ImageUpdateDetails,
  UpdateStatus
} from '../../features/image-updates/image-updates'
import type { ImageUpdateResult } from '../../features/image-updates/types'
import { useDateTime } from '../../lib/time-zone'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '../ui/collapsible'
import { ErrorState } from '../ui/error-state'
import { ListPagination } from '../ui/list-pagination'
import { LoadingState } from '../ui/loading-state'
import { StatusBadge } from '../ui/status-badge'
import { TooltipHint } from '../ui/tooltip-hint'
import { useListPagination } from '../ui/use-list-pagination'

const serviceName = (row: ContainerSummary) =>
  row.labels['com.docker.compose.service'] || row.name

const portMapping = (port: ContainerPort, full = false) => {
  if (!port.public_port) return `${port.private_port}/${port.type}`
  const ip = port.ip || '0.0.0.0'
  const host = ip.includes(':') && !ip.startsWith('[') ? `[${ip}]` : ip
  return `${full ? `${host}:` : ''}${port.public_port} → ${port.private_port}/${port.type}`
}

const compactPorts = (ports: ContainerPort[]) => {
  const values = [...new Set(ports.map(port => portMapping(port)))]
  return values.length
    ? `${values[0]}${values.length > 1 ? ` +${values.length - 1}` : ''}`
    : '—'
}

const memoryLabel = (bytes: number) =>
  !bytes ? '—' : bytes >= 1024 ** 3
    ? `${(bytes / 1024 ** 3).toFixed(2)} GB`
    : `${(bytes / 1024 ** 2).toFixed(0)} MB`

const uptimeLabel = (seconds: number, zh: boolean) =>
  !seconds ? '—' : seconds >= 86400
    ? `${Math.floor(seconds / 86400)} ${zh ? '天' : 'd'}`
    : seconds >= 3600
      ? `${Math.floor(seconds / 3600)} ${zh ? '小时' : 'h'}`
      : `${Math.max(1, Math.floor(seconds / 60))} ${zh ? '分钟' : 'm'}`

const stateLabel = (state: string, zh: boolean) => zh
  ? ({ running: '运行中', paused: '已暂停', restarting: '重启中', exited: '已停止', dead: '异常', created: '已创建' }[state] ?? state)
  : state

const rowGrid = 'grid grid-cols-[minmax(0,1.35fr)_minmax(0,1fr)] gap-x-5 gap-y-3 lg:grid-cols-[minmax(0,2fr)_minmax(7rem,1fr)_minmax(7rem,1fr)_minmax(9rem,1.2fr)]'

export function ProjectServices({
  nodeID,
  projectName,
  rows,
  updates,
  declared,
  loading,
  error,
  updateError,
  zh
}: {
  nodeID: string
  projectName: string
  rows?: ContainerSummary[]
  updates: ImageUpdateResult[]
  declared: string[]
  loading: boolean
  error?: string
  updateError?: string
  zh: boolean
}) {
  const { formatDateTime } = useDateTime()
  const [expanded, setExpanded] = useState<string | null>(null)
  const containers = rows ?? []
  const deployed = new Set(containers.map(serviceName))
  const names = new Set([...declared, ...deployed])
  const entries: { key: string; name: string; container?: ContainerSummary }[] = [
    ...containers.map(container => ({ key: container.id, name: serviceName(container), container })),
    ...declared.filter(name => !deployed.has(name)).map(name => ({ key: `undeployed/${name}`, name }))
  ]
  entries.sort((a, b) => a.name.localeCompare(b.name) || (a.container?.name ?? '').localeCompare(b.container?.name ?? ''))
  const pagination = useListPagination(entries, `${nodeID}/${projectName}`)
  const running = containers.filter(row => row.state === 'running').length

  return (
    <section className="flex w-full min-w-0 flex-col gap-4" aria-label={zh ? '项目服务' : 'Project services'}>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex flex-wrap items-center gap-2">
          <h3 className="text-sm font-medium">{zh ? '服务' : 'Services'}</h3>
          {!loading && !error && <>
            <span className="text-xs text-muted-foreground tabular-nums">{names.size}</span>
            {!!containers.length && <span className="ml-2 text-xs text-muted-foreground">
              {zh ? `${running} / ${containers.length} 运行中` : `${running} / ${containers.length} running`}
            </span>}
          </>}
        </div>
        <ImageUpdateCheck nodeID={nodeID} target={{ project_name: projectName }} />
      </div>
      {updateError && <p role="alert" className="text-xs text-destructive">{updateError}</p>}
      {loading ? <LoadingState compact label={zh ? '正在加载项目服务' : 'Loading project services'} />
        : error ? <ErrorState description={error} />
          : !entries.length ? <p className="py-8 text-sm text-muted-foreground">
            {zh ? '项目尚未创建容器，请先启动项目。' : 'No containers have been created. Start the project first.'}
          </p> : <>
            <div className={`${rowGrid} hidden text-xs text-muted-foreground lg:grid`} aria-hidden="true">
              <span>{zh ? '服务 / 镜像' : 'Service / image'}</span>
              <span>{zh ? '状态' : 'State'}</span>
              <span>{zh ? '资源' : 'Resources'}</span>
              <span>{zh ? '端口' : 'Ports'}</span>
            </div>
            <div className="divide-y divide-border border-y border-border">
              {pagination.items.map(entry => {
                const row = entry.container
                if (!row) return <div key={entry.key} className="flex flex-wrap items-center gap-x-6 gap-y-2 py-4">
                  <span className="text-sm font-medium">{entry.name}</span>
                  <span className="text-xs text-muted-foreground">{zh ? '尚未部署 · 没有本地运行版本可检测' : 'Not deployed · no local runtime version to check'}</span>
                </div>
                const imageRows = updates.filter(update => update.containers.some(container => container.container_id === row.id))
                const deliveryProject = imageRows.flatMap(update => update.containers).find(container => container.container_id === row.id && container.delivery_project)?.delivery_project
                const number = row.labels['com.docker.compose.container-number']
                const image = row.image.replace(/^docker\.io\/(library\/)?/, '')
                return <Collapsible key={row.id} open={expanded === row.id} onOpenChange={open => setExpanded(open ? row.id : null)}>
                  <CollapsibleTrigger
                    className={`${rowGrid} group w-full items-center rounded-sm py-4 text-left outline-none hover:bg-muted/30 focus-visible:ring-2 focus-visible:ring-ring`}
                    aria-label={zh ? `服务详情 ${entry.name}${number ? ` · #${number}` : ''} · ${row.name}` : `Service details ${entry.name}${number ? ` · #${number}` : ''} · ${row.name}`}
                  >
                    <span className="flex min-w-0 flex-col gap-1.5">
                      <span className="flex min-w-0 flex-wrap items-center gap-2 text-sm font-medium">
                        <span className="break-all">{entry.name}</span>
                        {number && <span className="font-mono text-[11px] font-normal text-muted-foreground">#{number}</span>}
                        {row.labels['com.docker.compose.oneoff']?.toLowerCase() === 'true' && <span className="text-xs font-normal text-muted-foreground">{zh ? '一次性' : 'One-off'}</span>}
                      </span>
                      <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                        <TooltipHint content={row.image}><span className="truncate font-mono text-xs text-muted-foreground">{image}</span></TooltipHint>
                        <UpdateStatus rows={imageRows} zh={zh} />
                      </span>
                    </span>
                    <span className="flex flex-col items-end gap-1.5 lg:items-start">
                      <StatusBadge tone={row.state === 'running' ? 'success' : row.state === 'dead' ? 'critical' : 'neutral'}>{stateLabel(row.state, zh)}</StatusBadge>
                      <TooltipHint content={row.status}><span className="text-xs text-muted-foreground">{uptimeLabel(row.uptime_seconds, zh)}</span></TooltipHint>
                    </span>
                    <span className="flex flex-wrap gap-x-2 gap-y-1 text-xs tabular-nums lg:flex-col">
                      {row.state === 'running' ? <><span>CPU {Number.isFinite(row.cpu_percent) ? `${row.cpu_percent.toFixed(1)}%` : '—'}</span><span className="text-muted-foreground">{memoryLabel(row.memory_bytes)}</span></> : '—'}
                    </span>
                    <span className="flex min-w-0 items-center justify-end gap-2 lg:justify-between">
                      <span className="min-w-0 break-words text-right font-mono text-xs lg:text-left">{compactPorts(row.ports)}</span>
                      <ChevronRight aria-hidden="true" className="size-4 shrink-0 text-muted-foreground transition-transform group-aria-expanded:rotate-90 motion-reduce:transition-none" />
                    </span>
                  </CollapsibleTrigger>
                  <CollapsibleContent>
                    <div className="mb-4 grid min-w-0 gap-5 rounded-lg bg-muted/35 p-4 xl:grid-cols-[minmax(0,1fr)_minmax(0,1.3fr)]">
                      <dl className="grid min-w-0 gap-4 sm:grid-cols-2">
                        <div className="min-w-0">
                          <dt className="mb-1 text-xs text-muted-foreground">{zh ? '容器' : 'Container'}</dt>
                          <dd className="min-w-0 text-xs">
                            <Link to="/containers/$containerId" params={{ containerId: row.id }} className="break-all underline underline-offset-4">{row.name}</Link>
                            <span className="mt-1 block break-all font-mono text-[11px] text-muted-foreground">{row.id}</span>
                          </dd>
                        </div>
                        <div className="min-w-0">
                          <dt className="mb-1 text-xs text-muted-foreground">{zh ? '创建时间' : 'Created'}</dt>
                          <dd className="text-xs tabular-nums">{formatDateTime(row.created)}</dd>
                        </div>
                        {deliveryProject && <div className="min-w-0 sm:col-span-2">
                          <dt className="mb-1 text-xs text-muted-foreground">{zh ? '交付项目' : 'Delivery project'}</dt>
                          <dd className="text-xs"><Link to="/continuous-delivery/$projectName" params={{ projectName: deliveryProject }} className="break-all underline underline-offset-4">{deliveryProject}</Link></dd>
                        </div>}
                        <div className="min-w-0 sm:col-span-2">
                          <dt className="mb-1 text-xs text-muted-foreground">{zh ? '完整端口映射' : 'Full port mappings'}</dt>
                          <dd className="font-mono text-xs">{row.ports.length ? <ul className="space-y-1">{row.ports.map((port, index) => <li key={index} className="break-all">{portMapping(port, true)}</li>)}</ul> : '—'}</dd>
                        </div>
                      </dl>
                      <div className="min-w-0">
                        <h4 className="text-xs text-muted-foreground">{zh ? '镜像详情' : 'Image details'}</h4>
                        {imageRows.length ? <ImageUpdateDetails rows={imageRows} zh={zh} showUsage={false} /> : <p className="mt-2 break-all font-mono text-xs">{row.image}</p>}
                      </div>
                    </div>
                  </CollapsibleContent>
                </Collapsible>
              })}
            </div>
            {pagination.total > pagination.pageSize ? <ListPagination {...pagination} zh={zh} /> : <p className="text-xs text-muted-foreground">
              {zh ? `${names.size} 个服务 · ${containers.length} 个容器` : `${names.size} services · ${containers.length} containers`}
            </p>}
          </>}
    </section>
  )
}
