import { useRef, type KeyboardEvent, type PointerEvent } from 'react'
import { motion, useReducedMotion } from 'motion/react'
import { Boxes, Container, Cpu, Grip, GripVertical, MemoryStick, Server } from 'lucide-react'
import { cn } from '@/lib/utils'
import { useI18n } from '../../lib/i18n'
import { nodeCardSizeOptions, type NodeCardSize } from '../../stores/ui'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '../ui/card'
import { StatusBadge } from '../ui/status-badge'
import { TooltipHint } from '../ui/tooltip-hint'

export interface FleetContainer {
  id: string
  name: string
  image: string
  state: string
  available: boolean
  cpu_percent: number
  memory_bytes: number
  network_rx_bytes: number
  network_tx_bytes: number
  block_read_bytes: number
  block_write_bytes: number
  uptime_seconds: number
}

export interface FleetNode {
  id: string
  name: string
  connection_type: string
  tls_mode: string
  enabled: boolean
  status: string
  engine_version?: string
  last_latency_ms?: number
  last_checked_at?: string
  last_error?: string
  hostname?: string
  os?: string
  os_version?: string
  architecture?: string
  kernel_version?: string
  containers_running: number
  containers_paused: number
  containers_stopped: number
  images: number
  networks?: number
  volumes?: number
  cpus: number
  memory_total_bytes: number
  docker_disk_usage_bytes?: number
  storage_driver?: string
  logging_driver?: string
  cgroup_driver?: string
  cgroup_version?: string
  default_runtime?: string
  live_restore: boolean
  security_options?: string[]
  metrics_available: boolean
  container_cpu_percent: number
  container_memory_bytes: number
  container_network_rx_bytes: number
  container_network_tx_bytes: number
  container_block_read_bytes: number
  container_block_write_bytes: number
  longest_container_uptime_seconds: number
  containers: FleetContainer[]
}

const bytes = (value?: number | null) => {
  const amount = value ?? 0
  if (amount >= 1024 ** 4) return `${(amount / 1024 ** 4).toFixed(1)} TB`
  if (amount >= 1024 ** 3) return `${(amount / 1024 ** 3).toFixed(1)} GB`
  return `${(amount / 1024 ** 2).toFixed(0)} MB`
}

const clampPercent = (value: number) => Math.max(0, Math.min(100, value))

const duration = (seconds: number, zh: boolean) => {
  if (seconds <= 0) return '—'
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  if (days > 0) return zh ? `${days} 天 ${hours} 小时` : `${days}d ${hours}h`
  if (hours > 0) return zh ? `${hours} 小时 ${minutes} 分钟` : `${hours}h ${minutes}m`
  return zh ? `${minutes} 分钟` : `${minutes}m`
}

function LoadBar({ value, tone }: { value: number; tone: 'neutral' | 'warning' | 'critical' }) {
  return (
    <div className="h-1.5 overflow-hidden rounded-full bg-muted" aria-hidden="true">
      <div
        className={cn('h-full rounded-full transition-[width]', tone === 'critical' ? 'bg-red-500' : tone === 'warning' ? 'bg-amber-500' : 'bg-foreground/55')}
        style={{ width: `${clampPercent(value)}%` }}
      />
    </div>
  )
}

function loadTone(value: number) {
  return value >= 85 ? 'critical' as const : value >= 65 ? 'warning' as const : 'neutral' as const
}

function Metric({ icon: Icon, label, value, detail, bar }: { icon: typeof Cpu; label: string; value: string; detail?: string; bar?: number }) {
  return (
    <div className="min-w-0 rounded-lg bg-muted/35 p-3">
      <div className="flex items-center gap-1.5 text-xs text-muted-foreground"><Icon className="size-3.5" />{label}</div>
      <div className="mt-1 truncate text-lg font-semibold tabular-nums">{value}</div>
      {detail && <div className="mt-0.5 truncate text-xs text-muted-foreground">{detail}</div>}
      {bar != null && <div className="mt-2"><LoadBar value={bar} tone={loadTone(bar)} /></div>}
    </div>
  )
}

function ContainerMetricsList({ containers, detailed, zh }: { containers: FleetContainer[]; detailed?: boolean; zh: boolean }) {
  const rows = [...containers].sort((left, right) => right.memory_bytes - left.memory_bytes || left.name.localeCompare(right.name))
  return (
    <section className="mt-4 flex min-h-0 flex-1 flex-col border-t pt-3" aria-label={zh ? '运行中容器性能' : 'Running container performance'}>
      <div className="flex shrink-0 items-center justify-between gap-3">
        <div className="flex items-center gap-1.5 text-xs font-medium"><Container className="size-3.5 text-muted-foreground" />{zh ? '运行中容器' : 'Running containers'}</div>
        <span className="text-[11px] text-muted-foreground">{zh ? '按内存倒序' : 'Memory descending'}</span>
      </div>
      {rows.length === 0
        ? <div className="flex min-h-20 flex-1 items-center justify-center text-xs text-muted-foreground">{zh ? '暂无运行中容器' : 'No running containers'}</div>
        : <div className="mt-2 min-h-0 flex-1 divide-y divide-border overflow-y-auto overscroll-contain pr-1">
            {rows.map((container) => (
              <div key={container.id} className="grid grid-cols-[minmax(0,1fr)_auto_auto] items-center gap-x-3 gap-y-1 py-2 first:pt-0 last:pb-0">
                <div className="min-w-0">
                  <div className="flex min-w-0 items-center gap-1.5"><span className={cn('size-1.5 shrink-0 rounded-full', container.available ? 'bg-emerald-500' : 'bg-zinc-400')} /><span className="truncate text-xs font-medium">{container.name || container.id.slice(0, 12)}</span></div>
                  <div className="mt-0.5 truncate pl-3 text-[11px] text-muted-foreground">{container.image || container.state}</div>
                </div>
                <div className="text-right"><div className="text-[10px] text-muted-foreground">CPU</div><div className="text-xs font-medium tabular-nums">{container.available ? `${container.cpu_percent.toFixed(1)}%` : '—'}</div></div>
                <div className="min-w-16 text-right"><div className="text-[10px] text-muted-foreground">{zh ? '内存' : 'Memory'}</div><div className="text-xs font-medium tabular-nums">{container.available ? bytes(container.memory_bytes) : '—'}</div></div>
                {detailed && (
                  <div className="col-span-3 ml-3 grid grid-cols-2 gap-x-4 gap-y-1 rounded-md bg-muted/30 px-2 py-1.5 text-[10px] sm:grid-cols-3">
                    <div className="flex justify-between gap-2"><span className="text-muted-foreground">Network</span><span className="tabular-nums">{container.available ? `↓${bytes(container.network_rx_bytes)} ↑${bytes(container.network_tx_bytes)}` : '—'}</span></div>
                    <div className="flex justify-between gap-2"><span className="text-muted-foreground">Block I/O</span><span className="tabular-nums">{container.available ? `R ${bytes(container.block_read_bytes)} W ${bytes(container.block_write_bytes)}` : '—'}</span></div>
                    <div className="flex justify-between gap-2"><span className="text-muted-foreground">{zh ? '运行时间' : 'Uptime'}</span><span className="tabular-nums">{container.available ? duration(container.uptime_seconds, zh) : '—'}</span></div>
                  </div>
                )}
              </div>
            ))}
          </div>}
    </section>
  )
}

export function NodeOverviewCard({ node, size, isDragging, onSizeChange, onOrderDragStart, onOrderDragMove, onOrderDragEnd, onOrderMove }: {
  node: FleetNode
  size: NodeCardSize
  isDragging: boolean
  onSizeChange: (size: NodeCardSize) => void
  onOrderDragStart: () => void
  onOrderDragMove: (clientX: number, clientY: number) => void
  onOrderDragEnd: () => void
  onOrderMove: (offset: -1 | 1) => void
}) {
  const { language } = useI18n()
  const zh = language === 'zh-CN'
  const reduceMotion = useReducedMotion()
  const previewSize = size
  const orderDragRef = useRef<{ pointerID: number; x: number; y: number; active: boolean } | null>(null)

  const available = node.enabled && node.status === 'online'
  const metricsAvailable = available && node.metrics_available
  const containerTotal = node.containers_running + node.containers_paused + node.containers_stopped
  const cpuLoad = metricsAvailable && node.cpus > 0 ? node.container_cpu_percent / node.cpus : null
  const memoryLoad = metricsAvailable && node.memory_total_bytes > 0 ? node.container_memory_bytes / node.memory_total_bytes * 100 : null
  const statusLabel = !node.enabled ? (zh ? '已禁用' : 'Disabled') : node.status === 'online' ? (zh ? '在线' : 'Online') : (zh ? '离线' : 'Offline')
  const statusTone = !node.enabled ? 'neutral' as const : node.status === 'online' ? 'success' as const : 'critical' as const
  const sizeLabel = previewSize === 'small' ? (zh ? '小型' : 'Small') : previewSize === 'medium' ? (zh ? '中型' : 'Medium') : (zh ? '大型' : 'Large')
  const nextSize = nodeCardSizeOptions[(nodeCardSizeOptions.indexOf(previewSize) + 1) % nodeCardSizeOptions.length]
  const nextSizeLabel = nextSize === 'small' ? (zh ? '小型' : 'Small') : nextSize === 'medium' ? (zh ? '中型' : 'Medium') : (zh ? '大型' : 'Large')
  const gridClass = previewSize === 'small'
    ? 'md:col-span-6 xl:col-span-4'
    : previewSize === 'medium'
      ? 'md:col-span-12 xl:col-span-6'
      : 'md:col-span-12 xl:col-span-12'

  const startOrderDrag = (event: PointerEvent<HTMLButtonElement>) => {
    if (event.button !== 0) return
    event.preventDefault()
    event.currentTarget.focus()
    event.currentTarget.setPointerCapture(event.pointerId)
    orderDragRef.current = { pointerID: event.pointerId, x: event.clientX, y: event.clientY, active: false }
  }

  const moveOrderDrag = (event: PointerEvent<HTMLButtonElement>) => {
    const drag = orderDragRef.current
    if (!drag || drag.pointerID !== event.pointerId) return
    if (!drag.active && Math.hypot(event.clientX - drag.x, event.clientY - drag.y) >= 6) {
      drag.active = true
      onOrderDragStart()
    }
    if (drag.active) onOrderDragMove(event.clientX, event.clientY)
  }

  const finishOrderDrag = (event: PointerEvent<HTMLButtonElement>) => {
    const drag = orderDragRef.current
    if (!drag || drag.pointerID !== event.pointerId) return
    orderDragRef.current = null
    if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId)
    if (drag.active) onOrderDragEnd()
  }

  const moveOrderWithKeyboard = (event: KeyboardEvent<HTMLButtonElement>) => {
    if (event.key === 'ArrowLeft' || event.key === 'ArrowUp') {
      event.preventDefault()
      onOrderMove(-1)
    } else if (event.key === 'ArrowRight' || event.key === 'ArrowDown') {
      event.preventDefault()
      onOrderMove(1)
    }
  }

  const unavailable = '—'
  const latency = available && node.last_latency_ms != null ? `${node.last_latency_ms} ms` : unavailable
  const checkedAt = node.last_checked_at ? new Date(node.last_checked_at).toLocaleString(language) : unavailable

  return (
    <motion.div
      layout
      data-node-card-id={node.id}
      className={cn('col-span-12 min-w-0', gridClass, isDragging && 'z-10 opacity-70')}
      transition={reduceMotion ? { duration: 0 } : { layout: { duration: 0.3, ease: 'linear' } }}
    >
      <Card className={cn('relative', previewSize === 'small' ? 'h-72' : previewSize === 'medium' ? 'h-[42rem]' : 'h-[72rem] md:h-[54rem]')}>
        <CardHeader>
          <div className="flex min-w-0 items-center gap-2">
            <TooltipHint content={zh ? '拖动调整顺序，或使用方向键移动' : 'Drag to reorder, or use the arrow keys'}>
              <button
                type="button"
                aria-label={zh ? `调整 ${node.name} 卡片顺序` : `Reorder ${node.name} card`}
                className="flex size-6 shrink-0 touch-none cursor-grab items-center justify-center rounded text-muted-foreground/55 outline-none hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring active:cursor-grabbing"
                onPointerDown={startOrderDrag}
                onPointerMove={moveOrderDrag}
                onPointerUp={finishOrderDrag}
                onPointerCancel={finishOrderDrag}
                onLostPointerCapture={finishOrderDrag}
                onKeyDown={moveOrderWithKeyboard}
              >
                <GripVertical className="size-3.5" />
              </button>
            </TooltipHint>
            <span className={cn('size-2 shrink-0 rounded-full', !node.enabled ? 'bg-zinc-400' : available ? 'bg-emerald-500' : 'bg-red-500')} />
            <CardTitle className="min-w-0 flex-1 truncate">{node.name}</CardTitle>
            <StatusBadge tone={statusTone}>{statusLabel}</StatusBadge>
          </div>
          <CardDescription className="truncate">
            {previewSize === 'small'
              ? `${node.connection_type.toUpperCase()} · ${latency}`
              : [node.hostname || node.id, node.os].filter(Boolean).join(' · ')}
          </CardDescription>
        </CardHeader>

        <CardContent className="flex min-h-0 flex-1 flex-col pb-5">
          {previewSize === 'small' && (
            <>
              <div className="grid grid-cols-3 gap-2">
                <div><div className="text-xs text-muted-foreground">{zh ? '运行容器' : 'Running'}</div><div className="mt-1 text-xl font-semibold tabular-nums">{available ? node.containers_running : unavailable}</div></div>
                <div><div className="text-xs text-muted-foreground">CPU</div><div className="mt-1 text-xl font-semibold tabular-nums">{cpuLoad == null ? unavailable : `${cpuLoad.toFixed(1)}%`}</div></div>
                <div><div className="text-xs text-muted-foreground">{zh ? '内存' : 'Memory'}</div><div className="mt-1 text-xl font-semibold tabular-nums">{memoryLoad == null ? unavailable : `${memoryLoad.toFixed(1)}%`}</div></div>
              </div>
              <dl className="mt-4 grid grid-cols-2 gap-x-5 gap-y-2 border-t pt-3 text-xs">
                <div className="flex justify-between gap-2"><dt className="text-muted-foreground">{zh ? '容器总数' : 'Containers'}</dt><dd className="font-medium tabular-nums">{available ? containerTotal : unavailable}</dd></div>
                <div className="flex justify-between gap-2"><dt className="text-muted-foreground">{zh ? '镜像' : 'Images'}</dt><dd className="font-medium tabular-nums">{available ? node.images : unavailable}</dd></div>
                <div className="flex justify-between gap-2"><dt className="text-muted-foreground">{zh ? '网络' : 'Networks'}</dt><dd className="font-medium tabular-nums">{node.networks ?? unavailable}</dd></div>
                <div className="flex justify-between gap-2"><dt className="text-muted-foreground">{zh ? '存储卷' : 'Volumes'}</dt><dd className="font-medium tabular-nums">{node.volumes ?? unavailable}</dd></div>
                <div className="flex justify-between gap-2"><dt className="text-muted-foreground">Docker {zh ? '磁盘' : 'disk'}</dt><dd className="font-medium tabular-nums">{node.docker_disk_usage_bytes == null ? unavailable : bytes(node.docker_disk_usage_bytes)}</dd></div>
                <div className="flex justify-between gap-2"><dt className="text-muted-foreground">Engine</dt><dd className="max-w-24 truncate font-mono">{available ? node.engine_version || unavailable : unavailable}</dd></div>
              </dl>
            </>
          )}

          {previewSize === 'medium' && (
            <>
              <div className="grid grid-cols-2 gap-2">
                <Metric icon={Container} label={zh ? '容器' : 'Containers'} value={available ? `${node.containers_running} / ${containerTotal}` : unavailable} detail={available ? `${node.containers_stopped} ${zh ? '停止' : 'stopped'} · ${node.containers_paused} ${zh ? '暂停' : 'paused'}` : undefined} />
                <Metric icon={Cpu} label={zh ? '容器 CPU' : 'Container CPU'} value={cpuLoad == null ? unavailable : `${cpuLoad.toFixed(1)}%`} detail={available ? `${node.cpus} vCPU` : undefined} bar={cpuLoad ?? undefined} />
                <Metric icon={MemoryStick} label={zh ? '容器内存' : 'Container memory'} value={memoryLoad == null ? unavailable : `${memoryLoad.toFixed(1)}%`} detail={metricsAvailable ? `${bytes(node.container_memory_bytes)} / ${bytes(node.memory_total_bytes)}` : undefined} bar={memoryLoad ?? undefined} />
                <Metric icon={Boxes} label="Docker disk" value={node.docker_disk_usage_bytes == null ? unavailable : bytes(node.docker_disk_usage_bytes)} detail={zh ? '镜像、容器、卷与构建缓存' : 'Images, containers, volumes, and build cache'} />
              </div>
              <dl className="mt-3 grid grid-cols-3 gap-3 border-t pt-3 text-xs sm:grid-cols-5">
                <div><dt className="text-muted-foreground">{zh ? '镜像' : 'Images'}</dt><dd className="mt-1 font-medium tabular-nums">{available ? node.images : unavailable}</dd></div>
                <div><dt className="text-muted-foreground">{zh ? '网络' : 'Networks'}</dt><dd className="mt-1 font-medium tabular-nums">{node.networks ?? unavailable}</dd></div>
                <div><dt className="text-muted-foreground">{zh ? '存储卷' : 'Volumes'}</dt><dd className="mt-1 font-medium tabular-nums">{node.volumes ?? unavailable}</dd></div>
                <div><dt className="text-muted-foreground">Engine</dt><dd className="mt-1 truncate font-mono">{available ? node.engine_version || unavailable : unavailable}</dd></div>
                <div><dt className="text-muted-foreground">{zh ? '最长运行' : 'Longest uptime'}</dt><dd className="mt-1 truncate font-medium tabular-nums">{metricsAvailable ? duration(node.longest_container_uptime_seconds, zh) : unavailable}</dd></div>
              </dl>
              <div className="mt-3 grid grid-cols-2 gap-x-5 gap-y-2 rounded-lg bg-muted/35 p-3 text-xs">
                <div className="flex justify-between gap-2"><span className="text-muted-foreground">Network I/O</span><span className="tabular-nums">{metricsAvailable ? `↓ ${bytes(node.container_network_rx_bytes)} · ↑ ${bytes(node.container_network_tx_bytes)}` : unavailable}</span></div>
                <div className="flex justify-between gap-2"><span className="text-muted-foreground">Block I/O</span><span className="tabular-nums">{metricsAvailable ? `R ${bytes(node.container_block_read_bytes)} · W ${bytes(node.container_block_write_bytes)}` : unavailable}</span></div>
              </div>
              <ContainerMetricsList containers={node.containers ?? []} zh={zh} />
            </>
          )}

          {previewSize === 'large' && (
            <>
              <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
                <Metric icon={Container} label={zh ? '容器' : 'Containers'} value={available ? `${node.containers_running} ${zh ? '运行中' : 'running'}` : unavailable} detail={available ? `${node.containers_stopped} ${zh ? '停止' : 'stopped'} · ${node.containers_paused} ${zh ? '暂停' : 'paused'} · ${containerTotal} ${zh ? '总计' : 'total'}` : undefined} />
                <Metric icon={Cpu} label={zh ? '容器 CPU' : 'Container CPU'} value={cpuLoad == null ? unavailable : `${cpuLoad.toFixed(1)}%`} detail={metricsAvailable ? `${node.container_cpu_percent.toFixed(1)}% ${zh ? '容器合计' : 'container total'} · ${node.cpus} vCPU` : undefined} bar={cpuLoad ?? undefined} />
                <Metric icon={MemoryStick} label={zh ? '容器内存' : 'Container memory'} value={metricsAvailable ? bytes(node.container_memory_bytes) : unavailable} detail={metricsAvailable ? `${memoryLoad?.toFixed(1)}% · ${bytes(node.memory_total_bytes)} ${zh ? '总容量' : 'total'}` : undefined} bar={memoryLoad ?? undefined} />
                <Metric icon={Boxes} label="Docker disk" value={node.docker_disk_usage_bytes == null ? unavailable : bytes(node.docker_disk_usage_bytes)} detail={zh ? `${node.images} 镜像 · ${node.volumes ?? '—'} 存储卷` : `${node.images} images · ${node.volumes ?? '—'} volumes`} />
              </div>
              <div className="mt-3 grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
                <Metric icon={Server} label="Network RX / TX" value={metricsAvailable ? bytes(node.container_network_rx_bytes) : unavailable} detail={metricsAvailable ? `${bytes(node.container_network_tx_bytes)} TX` : undefined} />
                <Metric icon={Server} label="Block read / write" value={metricsAvailable ? bytes(node.container_block_read_bytes) : unavailable} detail={metricsAvailable ? `${bytes(node.container_block_write_bytes)} write` : undefined} />
                <Metric icon={Container} label={zh ? '最长容器运行时间' : 'Longest container uptime'} value={metricsAvailable ? duration(node.longest_container_uptime_seconds, zh) : unavailable} />
              </div>
              <div className="mt-4 grid gap-4 border-t pt-4 md:grid-cols-3">
                <div>
                  <div className="flex items-center gap-1.5 text-xs font-medium"><Server className="size-3.5 text-muted-foreground" />{zh ? '主机与 Engine' : 'Host and Engine'}</div>
                  <dl className="mt-2 grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-xs">
                    <dt className="text-muted-foreground">{zh ? '主机' : 'Host'}</dt><dd className="truncate text-right">{node.hostname || node.id}</dd>
                    <dt className="text-muted-foreground">{zh ? '系统' : 'OS'}</dt><dd className="truncate text-right">{node.os || unavailable}</dd>
                    <dt className="text-muted-foreground">OS version</dt><dd className="truncate text-right">{node.os_version || unavailable}</dd>
                    <dt className="text-muted-foreground">Kernel</dt><dd className="truncate text-right font-mono">{node.kernel_version || unavailable}</dd>
                    <dt className="text-muted-foreground">{zh ? '架构' : 'Architecture'}</dt><dd className="truncate text-right">{node.architecture || unavailable}</dd>
                    <dt className="text-muted-foreground">Engine</dt><dd className="truncate text-right font-mono">{node.engine_version || unavailable}</dd>
                  </dl>
                </div>
                <div>
                  <div className="text-xs font-medium">{zh ? 'Engine 配置' : 'Engine configuration'}</div>
                  <dl className="mt-2 grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-xs">
                    <dt className="text-muted-foreground">{zh ? '存储驱动' : 'Storage'}</dt><dd className="truncate text-right font-mono">{node.storage_driver || unavailable}</dd>
                    <dt className="text-muted-foreground">{zh ? '日志驱动' : 'Logging'}</dt><dd className="truncate text-right font-mono">{node.logging_driver || unavailable}</dd>
                    <dt className="text-muted-foreground">Cgroup</dt><dd className="truncate text-right font-mono">{[node.cgroup_driver, node.cgroup_version].filter(Boolean).join(' · ') || unavailable}</dd>
                    <dt className="text-muted-foreground">Runtime</dt><dd className="truncate text-right font-mono">{node.default_runtime || unavailable}</dd>
                    <dt className="text-muted-foreground">Live restore</dt><dd className="text-right">{available ? (node.live_restore ? (zh ? '已启用' : 'Enabled') : (zh ? '未启用' : 'Disabled')) : unavailable}</dd>
                  </dl>
                </div>
                <div>
                  <div className="text-xs font-medium">{zh ? '连接与资源' : 'Connection and resources'}</div>
                  <dl className="mt-2 grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-xs">
                    <dt className="text-muted-foreground">{zh ? '类型' : 'Type'}</dt><dd className="text-right font-mono">{node.connection_type.toUpperCase()}</dd>
                    <dt className="text-muted-foreground">TLS</dt><dd className="text-right font-mono">{node.tls_mode === 'required' ? 'mTLS' : 'Plain'}</dd>
                    <dt className="text-muted-foreground">{zh ? '延迟' : 'Latency'}</dt><dd className="text-right tabular-nums">{latency}</dd>
                    <dt className="text-muted-foreground">{zh ? '网络 / 存储卷' : 'Networks / volumes'}</dt><dd className="text-right tabular-nums">{node.networks ?? unavailable} / {node.volumes ?? unavailable}</dd>
                    <dt className="text-muted-foreground">{zh ? '最后检查' : 'Last checked'}</dt><dd className="truncate text-right tabular-nums">{checkedAt}</dd>
                  </dl>
                </div>
              </div>
              {(node.security_options?.length ?? 0) > 0 && <div className="mt-4 flex flex-wrap items-center gap-1.5 border-t pt-3"><span className="mr-1 text-xs text-muted-foreground">Security</span>{node.security_options?.map((option) => <span key={option} className="rounded bg-muted px-1.5 py-0.5 font-mono text-[11px]">{option}</span>)}</div>}
              {!available && node.last_error && <div className="mt-4 rounded-lg bg-red-500/10 px-3 py-2 text-xs break-all text-red-700 dark:text-red-300">{node.last_error}</div>}
              <ContainerMetricsList containers={node.containers ?? []} detailed zh={zh} />
            </>
          )}
        </CardContent>

        <TooltipHint content={zh ? `当前${sizeLabel}，点击切换到${nextSizeLabel}` : `${sizeLabel}; click for ${nextSizeLabel.toLowerCase()}`}>
          <button
            type="button"
            aria-label={zh ? `将 ${node.name} 卡片从${sizeLabel}切换到${nextSizeLabel}` : `Switch ${node.name} card from ${sizeLabel.toLowerCase()} to ${nextSizeLabel.toLowerCase()}`}
            className="absolute right-1.5 bottom-1.5 flex size-7 items-center justify-center rounded-md text-muted-foreground/55 outline-none transition-colors hover:bg-muted hover:text-foreground focus-visible:bg-muted focus-visible:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
            onClick={() => onSizeChange(nextSize)}
          >
            <Grip className="size-3.5 rotate-[-45deg]" />
          </button>
        </TooltipHint>
      </Card>
    </motion.div>
  )
}
