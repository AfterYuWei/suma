import { useQuery } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { LayoutGroup, motion, useReducedMotion } from 'motion/react'
import { useRef, useState } from 'react'
import {
  ArrowUpRight,
  GitPullRequest,
} from 'lucide-react'
import { NodeOverviewCard, type FleetNode } from '../components/docker/node-overview-card'
import { LoadingState } from '../components/ui/loading-state'
import { Button } from '../components/ui/button'
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '../components/ui/card'
import { StatusBadge } from '../components/ui/status-badge'
import { TooltipHint } from '../components/ui/tooltip-hint'
import { api } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { fleetGroupQuery, type DockerNode } from '../lib/nodes'
import { useUIStore } from '../stores/ui'

interface FleetOverview { nodes: FleetNode[] }
interface ReleaseSummary { id: number; status: string; commit_sha: string; trigger_type: string; created_at: string }
interface CDProject { name: string; configured: boolean; repository_url?: string; git_ref?: string; reconcile_mode: string; node_ids: string[]; drifted: boolean; runtime_healthy: boolean; drift_reason?: string; active_release?: ReleaseSummary; latest_release?: ReleaseSummary; awaiting_approval: boolean; releasing: boolean }
interface CDOverview { projects: CDProject[]; totals: { projects: number; configured: number; releasing: number; awaiting_approval: number; drifted: number; healthy: number } }

const fleetRefreshInterval = 10_000

function releaseTone(status: string) {
  if (status === 'succeeded' || status === 'approved') return 'success'
  if (status === 'failed' || status === 'partial_failed' || status === 'rollback_failed') return 'critical'
  if (status === 'awaiting_approval' || ['validating', 'pulling', 'deploying', 'verifying', 'rolling_back'].includes(status)) return 'warning'
  return 'neutral'
}

export function OverviewPage() {
  const navigate = useNavigate()
  const { language } = useI18n()
  const zh = language === 'zh-CN'
  const reduceMotion = useReducedMotion()
  const nodeCardSizes = useUIStore((state) => state.overviewNodeCardSizes)
  const nodeCardOrder = useUIStore((state) => state.overviewNodeCardOrder)
  const setNodeCardSize = useUIStore((state) => state.setOverviewNodeCardSize)
  const setNodeCardOrder = useUIStore((state) => state.setOverviewNodeCardOrder)
  const currentGroupFilter = useUIStore((state) => state.currentGroupFilter)
  const [draggingNodeID, setDraggingNodeID] = useState<string | null>(null)
  const [dragOrder, setDragOrder] = useState<string[] | null>(null)
  const dragOrderRef = useRef<string[] | null>(null)

  const fleet = useQuery({
    queryKey: ['fleet-overview', currentGroupFilter],
    queryFn: () => api<FleetOverview>(`/fleet/overview${fleetGroupQuery(currentGroupFilter)}`),
    refetchInterval: fleetRefreshInterval,
    refetchOnWindowFocus: 'always',
    refetchOnReconnect: 'always',
  })
  const allNodes = useQuery({ queryKey: ['nodes'], queryFn: () => api<DockerNode[]>('/nodes') })
  const cd = useQuery({ queryKey: ['cd-overview'], queryFn: () => api<CDOverview>('/cd/overview'), refetchInterval: 10_000 })

  const nodes = fleet.data?.nodes ?? []
  const nodeIDs = new Set(nodes.map((node) => node.id))
  const savedNodeIDs = nodeCardOrder.filter((nodeID) => nodeIDs.has(nodeID))
  const orderedNodeIDs = [...savedNodeIDs, ...nodes.map((node) => node.id).filter((nodeID) => !savedNodeIDs.includes(nodeID))]
  const renderedNodeIDs = dragOrder ?? orderedNodeIDs
  const nodeByID = new Map(nodes.map((node) => [node.id, node]))
  const orderedNodes = renderedNodeIDs.map((nodeID) => nodeByID.get(nodeID)).filter((node): node is FleetNode => node != null)
  const cdTotals = cd.data?.totals
  const nodeNames = new Map((allNodes.data ?? []).map((node) => [node.id, node.name]))
  const layoutTransition = reduceMotion ? { duration: 0 } : { layout: { duration: 0.3, ease: 'linear' as const } }
  const fleetUpdatedAt = fleet.dataUpdatedAt > 0
    ? new Date(fleet.dataUpdatedAt).toLocaleTimeString(language, { hour: '2-digit', minute: '2-digit', second: '2-digit' })
    : '—'

  const startNodeOrderDrag = (nodeID: string) => {
    const order = [...orderedNodeIDs]
    dragOrderRef.current = order
    setDragOrder(order)
    setDraggingNodeID(nodeID)
  }

  const moveNodeOrderDrag = (clientX: number, clientY: number) => {
    if (!draggingNodeID || !dragOrderRef.current) return
    const target = document.elementFromPoint(clientX, clientY)?.closest<HTMLElement>('[data-node-card-id]')?.dataset.nodeCardId
    if (!target || target === draggingNodeID) return
    const next = [...dragOrderRef.current]
    const sourceIndex = next.indexOf(draggingNodeID)
    const targetIndex = next.indexOf(target)
    if (sourceIndex < 0 || targetIndex < 0) return
    next.splice(sourceIndex, 1)
    next.splice(targetIndex, 0, draggingNodeID)
    dragOrderRef.current = next
    setDragOrder(next)
  }

  const finishNodeOrderDrag = () => {
    if (dragOrderRef.current) setNodeCardOrder(dragOrderRef.current)
    dragOrderRef.current = null
    setDragOrder(null)
    setDraggingNodeID(null)
  }

  const moveNodeOrderWithKeyboard = (nodeID: string, offset: -1 | 1) => {
    const next = [...orderedNodeIDs]
    const currentIndex = next.indexOf(nodeID)
    const targetIndex = currentIndex + offset
    if (currentIndex < 0 || targetIndex < 0 || targetIndex >= next.length) return
    next.splice(currentIndex, 1)
    next.splice(targetIndex, 0, nodeID)
    setNodeCardOrder(next)
  }

  const cdStatus = (project: CDProject) => {
    if (!project.configured) return <StatusBadge tone="neutral">{zh ? '未配置' : 'Not configured'}</StatusBadge>
    const release = project.active_release ?? project.latest_release
    if (!release) return <StatusBadge tone="neutral">{zh ? '未同步' : 'Not synced'}</StatusBadge>
    return <StatusBadge tone={releaseTone(release.status)}>{release.status.replaceAll('_', ' ')}</StatusBadge>
  }

  if (fleet.isPending && cd.isPending) return <LoadingState label={zh ? '正在加载全局概览' : 'Loading fleet overview'} rows={8} />

  return (
      <LayoutGroup>
        <div className="flex w-full flex-col gap-5">
          <motion.section layout transition={layoutTransition} aria-labelledby="overview-nodes-heading" className="flex flex-col gap-3">
          <div className="flex flex-wrap items-end gap-3">
            <div>
              <h3 id="overview-nodes-heading" className="cn-font-heading text-base font-medium">{zh ? '节点资源' : 'Node resources'}</h3>
              <p className="mt-0.5 text-sm text-muted-foreground">{zh ? `每 10 秒自动刷新 · 最近更新 ${fleetUpdatedAt}；拖动标题手柄调整顺序` : `Refreshes every 10 seconds · Updated ${fleetUpdatedAt}; drag title handles to reorder`}</p>
            </div>
            <Button variant="ghost" size="sm" className="ml-auto text-muted-foreground" onClick={() => void navigate({ to: '/nodes' })}><ArrowUpRight className="size-4" />{zh ? '管理节点' : 'Manage nodes'}</Button>
          </div>
          {fleet.isPending
            ? <LoadingState rows={5} label={zh ? '正在加载节点' : 'Loading nodes'} />
            : nodes.length === 0
              ? <div className="rounded-xl border border-dashed py-12 text-center text-sm text-muted-foreground">{zh ? '暂无节点' : 'No nodes'}</div>
              : <div className="grid grid-cols-12 gap-4">
                  {orderedNodes.map((node) => (
                    <NodeOverviewCard
                      key={node.id}
                      node={node}
                      size={nodeCardSizes[node.id] ?? 'medium'}
                      isDragging={draggingNodeID === node.id}
                      onSizeChange={(size) => setNodeCardSize(node.id, size)}
                      onOrderDragStart={() => startNodeOrderDrag(node.id)}
                      onOrderDragMove={moveNodeOrderDrag}
                      onOrderDragEnd={finishNodeOrderDrag}
                      onOrderMove={(offset) => moveNodeOrderWithKeyboard(node.id, offset)}
                    />
                  ))}
                </div>}
          </motion.section>

          <motion.div layout transition={layoutTransition}>
            <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2"><GitPullRequest className="size-4 text-muted-foreground" />{zh ? '持续交付' : 'Continuous delivery'}</CardTitle>
            <CardDescription>{zh ? '跨节点项目的发布与偏移状态' : 'Release and drift status for cross-node projects'}</CardDescription>
            <CardAction className="flex items-center gap-2">
              {cdTotals && (cdTotals.releasing > 0 || cdTotals.awaiting_approval > 0 || cdTotals.drifted > 0) && <StatusBadge tone="warning">{zh ? `${cdTotals.releasing + cdTotals.awaiting_approval + cdTotals.drifted} 项待处理` : `${cdTotals.releasing + cdTotals.awaiting_approval + cdTotals.drifted} need attention`}</StatusBadge>}
              <Button variant="ghost" size="icon-sm" aria-label={zh ? '查看持续交付' : 'View continuous delivery'} onClick={() => void navigate({ to: '/continuous-delivery' })}><ArrowUpRight /></Button>
            </CardAction>
          </CardHeader>
          <CardContent>
            {cd.isPending
              ? <LoadingState embedded rows={3} label={zh ? '正在加载交付项目' : 'Loading delivery projects'} />
              : (cd.data?.projects ?? []).length === 0
                ? <p className="py-8 text-center text-sm text-muted-foreground">{zh ? '暂无交付项目' : 'No delivery projects'}</p>
                : <div className="flex max-h-80 flex-col divide-y divide-border overflow-y-auto overscroll-contain">
                    {(cd.data?.projects ?? []).map((project) => {
                      const release = project.active_release ?? project.latest_release
                      return (
                        <div key={project.name} className="grid gap-2 py-3 first:pt-0 last:pb-0 sm:grid-cols-[minmax(0,1fr)_auto_auto] sm:items-center">
                          <div className="min-w-0"><Link to="/continuous-delivery/$projectName" params={{ projectName: project.name }} className="font-medium underline-offset-4 hover:underline">{project.name}</Link><div className="truncate text-xs text-muted-foreground">{project.repository_url || (zh ? '尚未配置 Git 仓库' : 'Git repository not configured')}</div></div>
                          <div className="flex flex-wrap items-center gap-1">{project.node_ids.map((id) => <span key={id} className="rounded bg-muted px-1.5 py-0.5 text-xs text-muted-foreground">{nodeNames.get(id) ?? id}</span>)}</div>
                          <div className="flex min-w-36 items-center justify-end gap-2">{project.drifted && <TooltipHint content={project.drift_reason}><span className="inline-flex items-center gap-1 text-xs text-amber-600 dark:text-amber-400"><span className="size-1.5 rounded-full bg-amber-500" />{zh ? '偏移' : 'Drift'}</span></TooltipHint>}{cdStatus(project)}{release && <span className="font-mono text-xs text-muted-foreground">{release.commit_sha.slice(0, 7)}</span>}</div>
                        </div>
                      )
                    })}
                  </div>}
          </CardContent>
            </Card>
          </motion.div>
        </div>
      </LayoutGroup>
  )
}
