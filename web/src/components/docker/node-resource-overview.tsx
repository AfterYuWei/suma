import { lazy, Suspense, useRef, useState, type KeyboardEvent, type PointerEvent } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { LayoutGroup, motion, useReducedMotion } from 'motion/react'
import { ArrowUpRight, ChevronDown, GripVertical, Info } from 'lucide-react'
import { cn } from '@/lib/utils'
import { useI18n } from '../../lib/i18n'
import type { DockerNode } from '../../lib/nodes'
import { useUIStore } from '../../stores/ui'
import { useNodeOverview } from '../../features/overview/use-node-overview'
import { memoryTopFive, nodeLoads, nodeMetricSummary, overviewBytes, type NodeMetricPoint } from '../../features/overview/observation'
import type { FleetNode } from '../../features/overview/types'
import { Button } from '../ui/button'
import { LoadingState } from '../ui/loading-state'
import { TooltipHint } from '../ui/tooltip-hint'
import { NodeInformationSheet } from './node-information-sheet'

const NodeTrend = lazy(() => import('../../features/overview/node-trends').then(module => ({ default: module.NodeTrend })))
type NodeOverview = ReturnType<typeof useNodeOverview>

function nodeStatus({ node, loading }: NodeOverview, zh: boolean) {
  if (!node.enabled) return { label: zh ? '已禁用' : 'Disabled', dot: 'bg-foreground/30' }
  if (loading.info) return { label: zh ? '连接中' : 'Connecting', dot: 'bg-amber-500' }
  if (node.status === 'online') return { label: zh ? '在线' : 'Online', dot: 'bg-emerald-500' }
  return { label: zh ? '离线' : 'Offline', dot: 'bg-destructive' }
}

const percent = (value: number | null) => value == null ? '—' : `${value.toFixed(1)}%`
const loadColor = (value: number | null) => value != null && value >= 85 ? 'text-destructive' : value != null && value >= 65 ? 'text-amber-600 dark:text-amber-400' : 'text-foreground'

function ResourceSummary({ node, metric, value, zh }: { node: FleetNode; metric: 'cpu' | 'memory'; value: number | null; zh: boolean }) {
  const label = metric === 'cpu' ? (zh ? '容器 CPU' : 'Container CPU') : (zh ? '容器内存' : 'Container memory')
  return <span data-node-summary-metric={metric} className="block min-w-0">
    <span className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5 text-xs"><span className="text-muted-foreground">{label}</span><span data-node-load={metric} data-value={value ?? ''} className={cn('font-medium tabular-nums', loadColor(value))}>{percent(value)}</span></span>
    {metric === 'memory' && <span className="mt-1 flex flex-wrap gap-x-1 text-xs text-muted-foreground tabular-nums">{value == null ? '—' : <><span className="whitespace-nowrap">{overviewBytes(node.container_memory_bytes)}</span><span>/</span><span className="whitespace-nowrap">{overviewBytes(node.memory_total_bytes)}</span></>}</span>}
  </span>
}

function ContainerSummary({ node, available, zh }: { node: FleetNode; available: boolean; zh: boolean }) {
  const groups = [
    { label: zh ? '运行' : 'Running', value: node.containers_running },
    { label: zh ? '暂停' : 'Paused', value: node.containers_paused },
    { label: zh ? '停止' : 'Stopped', value: node.containers_stopped },
  ]
  return <span data-node-container-states className="col-span-2 flex min-w-0 flex-wrap gap-x-3 gap-y-1 text-xs @3xl:col-span-1">
    {groups.map(group => <span key={group.label} className="inline-flex gap-1 text-muted-foreground">{group.label}<span className="font-medium text-foreground tabular-nums">{available ? group.value : '—'}</span></span>)}
  </span>
}

function NodeResourceRow({ metadata, isDragging, zh, onDragStart, onDragMove, onDragEnd, onMove }: {
  metadata: DockerNode; isDragging: boolean; zh: boolean
  onDragStart: () => void; onDragMove: (x: number, y: number) => void; onDragEnd: () => void; onMove: (offset: -1 | 1) => void
}) {
  // Filtering unmounts the row and clears its local expansion and sheet state.
  // One query observer serves both the summary and the expanded content.
  const [expanded, setExpanded] = useState(false)
  const [infoOpen, setInfoOpen] = useState(false)
  const overview = useNodeOverview(metadata, expanded ? 'medium' : 'small')
  const { node, stale, errors, loading, retry } = overview
  const status = nodeStatus(overview, zh)
  const available = node.enabled && node.status === 'online'
  const loads = nodeLoads(available ? node : undefined, node.cpus, node.memory_total_bytes)
  const drag = useRef<{ id: number; x: number; y: number; active: boolean } | null>(null)
  const reduceMotion = useReducedMotion()
  const start = (event: PointerEvent<HTMLButtonElement>) => {
    if (event.button !== 0) return
    event.preventDefault(); event.currentTarget.focus(); event.currentTarget.setPointerCapture(event.pointerId)
    drag.current = { id: event.pointerId, x: event.clientX, y: event.clientY, active: false }
  }
  const move = (event: PointerEvent<HTMLButtonElement>) => {
    const current = drag.current
    if (!current || current.id !== event.pointerId) return
    if (!current.active && Math.hypot(event.clientX - current.x, event.clientY - current.y) >= 6) { current.active = true; onDragStart() }
    if (current.active) onDragMove(event.clientX, event.clientY)
  }
  const finish = (event: PointerEvent<HTMLButtonElement>) => {
    const current = drag.current
    if (!current || current.id !== event.pointerId) return
    drag.current = null
    if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId)
    if (current.active) onDragEnd()
  }
  const keyboard = (event: KeyboardEvent<HTMLButtonElement>) => {
    if (!['ArrowLeft', 'ArrowUp', 'ArrowRight', 'ArrowDown'].includes(event.key)) return
    event.preventDefault()
    onMove(event.key === 'ArrowLeft' || event.key === 'ArrowUp' ? -1 : 1)
  }
  const errorLabels = { info: zh ? '节点信息加载失败' : 'Node information failed to load', metrics: zh ? '指标加载失败' : 'Metrics unavailable' }
  const summaryID = `node-resource-summary-${node.id}`
  const detailID = `node-resource-details-${node.id}`
  return <motion.div layout="position" data-node-resource-id={node.id} className={cn('@container group relative min-w-0 rounded-lg', expanded ? 'bg-surface' : 'bg-surface/50 hover:bg-surface/80', isDragging && 'z-10 opacity-60')}
    transition={{ layout: { duration: reduceMotion ? 0 : 0.2, ease: 'linear' } }}>
    <div className="relative min-w-0">
      <button type="button" aria-label={zh ? `调整 ${node.name} 节点顺序` : `Reorder ${node.name} node`} className="absolute top-2 left-1 flex size-8 shrink-0 touch-none cursor-grab items-center justify-center rounded text-muted-foreground/60 focus-visible:ring-2 focus-visible:ring-ring active:cursor-grabbing [@media(pointer:coarse)]:size-11 @3xl:top-1/2 @3xl:-translate-y-1/2"
        onPointerDown={start} onPointerMove={move} onPointerUp={finish} onPointerCancel={finish} onLostPointerCapture={finish} onKeyDown={keyboard}><GripVertical className="size-3.5" /></button>
      <button id={summaryID} type="button" aria-label={zh ? `查看 ${node.name} 资源` : `View ${node.name} resources`} aria-expanded={expanded} aria-controls={detailID} onClick={() => setExpanded(value => !value)}
        className="grid w-full min-w-0 grid-cols-2 items-start gap-x-4 gap-y-3 rounded-md px-4 py-3 text-left @3xl:px-12 focus-visible:ring-2 focus-visible:ring-ring @3xl:grid-cols-[minmax(0,1.3fr)_minmax(0,0.8fr)_minmax(0,1fr)_minmax(0,0.9fr)] @3xl:items-center @3xl:gap-x-5">
        <span className="col-span-2 flex min-w-0 items-start justify-between gap-2 pr-6 pl-6 [@media(pointer:coarse)]:px-10 @3xl:col-span-1 @3xl:pr-0 @3xl:pl-0">
          <span className="block min-w-0"><span className="block break-words text-sm font-medium [overflow-wrap:anywhere]">{node.name}</span><span className="mt-1 inline-flex items-center gap-1.5 text-xs text-muted-foreground"><span className={cn('size-1.5 rounded-full', status.dot)} />{status.label}</span></span>
          <ChevronDown aria-hidden="true" className={cn('mt-0.5 size-4 shrink-0 text-muted-foreground transition-transform motion-reduce:transition-none', expanded && 'rotate-180')} />
        </span>
        <ResourceSummary node={node} metric="cpu" value={loads.cpu} zh={zh} />
        <ResourceSummary node={node} metric="memory" value={loads.memory} zh={zh} />
        <ContainerSummary node={node} available={available} zh={zh} />
      </button>
      <Button variant="ghost" size="icon" className="absolute top-2 right-1 text-muted-foreground [@media(pointer:coarse)]:size-11 @3xl:top-1/2 @3xl:-translate-y-1/2" aria-label={zh ? `查看 ${node.name} 节点信息` : `Node information for ${node.name}`} onClick={() => setInfoOpen(true)}><Info className="size-3.5" /></Button>
    </div>
    <div className="px-4 @3xl:px-12">
      {(Object.keys(errors) as (keyof typeof errors)[]).filter(key => errors[key]).map(key => <div key={key} role="alert" className="flex flex-wrap items-center justify-between gap-2 pb-3 text-xs text-destructive"><span>{errorLabels[key]}</span><button type="button" className="underline underline-offset-4" aria-label={zh ? `重试：${errorLabels[key]}` : `Retry: ${errorLabels[key]}`} onClick={retry[key]}>{zh ? '重试' : 'Retry'}</button></div>)}
      {stale && <p className="pb-3 text-xs text-muted-foreground">{zh ? '数据已过期，等待更新' : 'Data is stale; waiting for updates'}</p>}
      {loading.metrics && <p role="status" className="pb-3 text-xs text-muted-foreground">{zh ? '正在加载指标' : 'Loading metrics'}</p>}
      {available && !loading.metrics && !errors.metrics && !stale && !node.metrics_available && <p className="pb-3 text-xs text-muted-foreground">{zh ? '汇总数据不完整' : 'Aggregate data is incomplete'}</p>}
    </div>
    {expanded && <NodeResourceDetails id={detailID} labelledBy={summaryID} overview={overview} zh={zh} />}
    {infoOpen && <NodeInformationSheet metadata={metadata} node={node} zh={zh} onClose={() => setInfoOpen(false)} />}
  </motion.div>
}

function ResourceTrend({ metric, points, zh }: { metric: 'cpu' | 'memory'; points: NodeMetricPoint[]; zh: boolean }) {
  const label = metric === 'cpu' ? (zh ? '容器 CPU' : 'Container CPU') : (zh ? '容器内存' : 'Container memory')
  const { peak } = nodeMetricSummary(points, metric)
  return <section data-node-metric={metric} aria-label={label} className="min-w-0">
    <div className="mb-2 flex flex-wrap items-center justify-between gap-2 text-xs"><h5 className="flex items-center gap-1.5 font-medium"><span className={cn('size-1.5 rounded-full', metric === 'cpu' ? 'bg-chart-1' : 'bg-chart-2')} />{label}</h5><span className="text-muted-foreground">{zh ? '观测峰值' : 'Observed peak'} <span className="tabular-nums">{percent(peak)}</span></span></div>
    <Suspense fallback={<div className="h-28 animate-pulse rounded bg-foreground/3 @xl:h-40" />}><NodeTrend metric={metric} points={points} /></Suspense>
  </section>
}

function MemoryTopFive({ node, loading, unavailable, zh, onViewAll, opening }: { node: FleetNode; loading: boolean; unavailable: boolean; zh: boolean; onViewAll: () => void; opening: boolean }) {
  const rows = memoryTopFive(node.containers)
  const maximum = rows[0]?.memory_bytes ?? 0
  const incomplete = !unavailable && !loading && (!node.metrics_available || node.containers.some(row => !row.available))
  return <section aria-label={zh ? '内存 Top 5' : 'Memory Top 5'} className="min-w-0">
    <div className="mb-2 flex flex-wrap items-center justify-between gap-2"><h5 className="text-xs font-medium">{zh ? '内存 Top 5' : 'Memory Top 5'}</h5><Button variant="ghost" size="xs" disabled={opening} className="text-muted-foreground" onClick={onViewAll}>{zh ? '全部容器' : 'All containers'}<ArrowUpRight className="size-3" /></Button></div>
    {incomplete && <p className="mb-2 text-xs text-muted-foreground">{zh ? '数据不完整，仅展示可用指标' : 'Incomplete data; available metrics only'}</p>}
    {loading ? <LoadingState embedded compact rows={3} label={zh ? '正在加载容器数据' : 'Loading container data'} />
      : unavailable ? <p className="py-3 text-xs text-muted-foreground">{zh ? '容器数据暂不可用' : 'Container data unavailable'}</p>
      : rows.length === 0 ? <p className="py-3 text-xs text-muted-foreground">{node.containers_running === 0 ? zh ? '暂无运行中容器' : 'No running containers' : zh ? '暂无可用容器指标' : 'No available container metrics'}</p>
      : <div className="space-y-1">{rows.map(container => <div key={container.id} data-top-container={container.id} className="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 rounded-md py-2 hover:bg-foreground/3">
        <div className="min-w-0"><TooltipHint content={container.name || container.id}><span className="block min-w-0 truncate text-xs font-medium">{container.name || container.id.slice(0, 12)}</span></TooltipHint>
          <div role="img" aria-label={`${container.name} · ${overviewBytes(container.memory_bytes)}`} className="mt-2 h-1 max-w-64 overflow-hidden rounded-full bg-foreground/10"><div className="h-full rounded-full bg-chart-2/60" style={{ width: `${maximum > 0 ? container.memory_bytes / maximum * 100 : 0}%` }} /></div>
        </div>
        <div className="text-right"><div className="text-xs font-medium tabular-nums">{overviewBytes(container.memory_bytes)}</div><div className="mt-0.5 text-xs text-muted-foreground tabular-nums">CPU {Number.isFinite(container.cpu_percent) ? `${container.cpu_percent.toFixed(1)}%` : '—'}</div></div>
      </div>)}</div>}
  </section>
}

function NodeResourceDetails({ id, labelledBy, overview, zh }: { id: string; labelledBy: string; overview: NodeOverview; zh: boolean }) {
  const { node, points, stale, loading, errors } = overview
  const available = node.enabled && node.status === 'online'
  const observations = points.filter(point => point.cpu != null || point.memory != null)
  const collecting = available && !stale && !errors.metrics && observations.length > 0 && observations.at(-1)!.time - observations[0].time < 10_000
  const navigate = useNavigate()
  const [opening, setOpening] = useState(false)
  const viewAll = async () => {
    if (opening) return
    setOpening(true)
    try { if (await useUIStore.getState().setCurrentNodeID(node.id)) await navigate({ to: '/containers' }) }
    finally { setOpening(false) }
  }
  return <section id={id} data-node-detail-id={node.id} aria-labelledby={labelledBy} className="@container min-w-0 px-4 pt-2 pb-4 @3xl:px-12">
    <div className="grid gap-x-6 gap-y-5 @xl:grid-cols-2">
      {(['cpu', 'memory'] as const).map(metric => <ResourceTrend key={metric} metric={metric} points={points} zh={zh} />)}
    </div>
    <div className="mt-1 mb-4 flex flex-wrap items-center justify-between gap-x-4 gap-y-1 text-xs text-muted-foreground"><span>{zh ? '最近 5 分钟 · 每 10 秒采样' : 'Last 5 minutes · Samples every 10 seconds'}</span>{collecting && <span>{zh ? '趋势正在积累' : 'Collecting trend'}</span>}</div>
    <MemoryTopFive node={node} loading={loading.metrics} unavailable={!available || errors.metrics || stale} zh={zh} onViewAll={() => void viewAll()} opening={opening} />
  </section>
}

export function NodeResourceOverview({ nodes }: { nodes: DockerNode[] }) {
  const zh = useI18n().language === 'zh-CN'
  const savedOrder = useUIStore(state => state.overviewNodeOrder)
  const saveOrder = useUIStore(state => state.setOverviewNodeOrder)
  const [draggingID, setDraggingID] = useState<string | null>(null)
  const [dragOrder, setDragOrder] = useState<string[] | null>(null)
  const dragRef = useRef<string[] | null>(null)
  const byID = new Map(nodes.map(node => [node.id, node]))
  const orderedIDs = [...savedOrder.filter(id => byID.has(id)), ...nodes.map(node => node.id).filter(id => !savedOrder.includes(id))]
  const orderedNodes = (dragOrder ?? orderedIDs).map(id => byID.get(id)).filter((node): node is DockerNode => node != null)
  const startDrag = (id: string) => { const order = [...orderedIDs]; dragRef.current = order; setDragOrder(order); setDraggingID(id) }
  const moveDrag = (x: number, y: number) => {
    if (!draggingID || !dragRef.current) return
    const target = document.elementFromPoint(x, y)?.closest<HTMLElement>('[data-node-resource-id]')?.dataset.nodeResourceId
    const next = [...dragRef.current], sourceIndex = next.indexOf(draggingID), targetIndex = target ? next.indexOf(target) : -1
    if (sourceIndex < 0 || targetIndex < 0 || sourceIndex === targetIndex) return
    next.splice(sourceIndex, 1); next.splice(targetIndex, 0, draggingID)
    dragRef.current = next; setDragOrder(next)
  }
  // Retain preferences for nodes hidden by the group filter.
  const persistOrder = (order: string[]) => saveOrder([...order, ...savedOrder.filter(id => !order.includes(id))])
  const finishDrag = () => { if (dragRef.current) persistOrder(dragRef.current); dragRef.current = null; setDragOrder(null); setDraggingID(null) }
  const moveWithKeyboard = (id: string, offset: -1 | 1) => {
    const next = [...orderedIDs], sourceIndex = next.indexOf(id), targetIndex = sourceIndex + offset
    if (sourceIndex < 0 || targetIndex < 0 || targetIndex >= next.length) return
    next.splice(sourceIndex, 1); next.splice(targetIndex, 0, id); persistOrder(next)
  }
  return <LayoutGroup><div data-node-resources role="list" aria-label={zh ? '节点资源列表' : 'Node resource list'} className="min-w-0 space-y-2">
    {orderedNodes.map(node => <div key={node.id} role="listitem"><NodeResourceRow metadata={node} isDragging={node.id === draggingID} zh={zh}
      onDragStart={() => startDrag(node.id)} onDragMove={moveDrag} onDragEnd={finishDrag} onMove={offset => moveWithKeyboard(node.id, offset)} /></div>)}
  </div></LayoutGroup>
}
