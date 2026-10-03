import { AIAnalyzeButton } from '../features/operations/workbench'
import { useDateTime } from '../lib/time-zone'
import { Fragment, useState } from 'react'
import { Link, useLocation } from '@tanstack/react-router'
import { CleanupNodeSheet } from '../features/cleanup/storage-cleanup'
import type { DockerNode } from '../lib/nodes'
import { useQuery } from '@tanstack/react-query'
import { cn } from '@/lib/utils'
import { Button } from '../components/ui/button'
import { ListShell } from '../components/ui/list-shell'
import { ListPagination } from '../components/ui/list-pagination'
import { useListPagination } from '../components/ui/use-list-pagination'
import { LoadingState } from '../components/ui/loading-state'
import { Progress } from '../components/ui/progress'
import { StatusBadge } from '../components/ui/status-badge'
import { Tabs, TabsList, TabsTrigger } from '../components/ui/tabs'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'
import { api } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { nodePath } from '../lib/nodes'
import { compactTaskLogs } from '../features/tasks/compact-task-logs'
import { useUIStore } from '../stores/ui'
import { ResourceFrame } from './images'

interface Task { id: string; scope: 'control_plane' | 'node'; node_id?: string; node_name?: string; type: string; name: string; status: string; progress: number; message: string; created_at: string }
interface Log { id: number; level: string; message: string; created_at: string }

const taskTone = (status: string) => status === 'success' ? 'success' : status === 'failed' ? 'critical' : status === 'running' ? 'warning' : 'neutral'

export function TasksPage() {
  const { formatDateTime } = useDateTime()

  const nodeID = useUIStore((state) => state.currentNodeID)
  const { t, language } = useI18n()
  const zh = language === 'zh-CN'
  const [expandedID, setExpandedID] = useState<string | null>(null)
  const [scope, setScope] = useState<'current' | 'control_plane' | 'all'>('current')
  const query = useQuery({ queryKey: ['tasks', scope, nodeID], queryFn: () => api<Task[]>(scope === 'current' ? nodePath(nodeID, '/tasks') : `/tasks?scope=${scope}`), refetchInterval: 2_000 })
  const pagination = useListPagination(query.data ?? [], scope)
  const [cleanupOpen, setCleanupOpen] = useState(false)
  const nodes = useQuery({ queryKey: ['nodes'], queryFn: () => api<DockerNode[]>('/nodes') })
  const currentNode = nodes.data?.find(node => node.id === nodeID)
  const linkedTaskID = useLocation({ select: (location) => location.hash })

  return (
    <ResourceFrame
      title={t('tasks')}
      detail={zh ? '长时间运行的 Docker 与 Compose 操作' : 'Long-running Docker and Compose operations'}
      action={(
        <div className="flex flex-wrap items-center gap-2"><Tabs value={scope} onValueChange={(value) => { setScope(value as typeof scope); setExpandedID(null) }}><TabsList><TabsTrigger value="current">{zh ? '当前节点' : 'Current node'}</TabsTrigger><TabsTrigger value="control_plane">{zh ? '控制平面' : 'Control plane'}</TabsTrigger><TabsTrigger value="all">{zh ? '全部' : 'All'}</TabsTrigger></TabsList></Tabs><Button variant="outline" disabled={!currentNode} onClick={() => setCleanupOpen(true)}>
            {zh ? '存储清理' : 'Storage cleanup'}
          </Button></div>
      )}
    >
      {cleanupOpen && currentNode && <CleanupNodeSheet key={nodeID} node={currentNode} open initialTab="preview" onOpenChange={setCleanupOpen} />}
      {linkedTaskID && <LinkedTask key={linkedTaskID} id={linkedTaskID} zh={zh} />}
      {query.isPending
        ? <LoadingState label={zh ? '正在加载任务' : 'Loading tasks'} />
        : (
            <><ListShell><Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{zh ? '任务' : 'Task'}</TableHead>
                  {scope !== 'current' && <TableHead>{zh ? '作用域 / 节点' : 'Scope / node'}</TableHead>}
                  <TableHead className="w-56">{zh ? '进度' : 'Progress'}</TableHead>
                  <TableHead className="w-28">{zh ? '状态' : 'Status'}</TableHead>
                  <TableHead className="w-44">{zh ? '创建时间' : 'Created'}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(query.data ?? []).length === 0 && (
                  <TableRow>
                    <TableCell colSpan={scope === 'current' ? 4 : 5} className="h-24 text-center text-muted-foreground">{zh ? '暂无任务' : 'No tasks'}</TableCell>
                  </TableRow>
                )}
                {pagination.items.map((row) => (
                  <Fragment key={row.id}>
                    <TableRow
                      aria-expanded={expandedID === row.id}
                      className="cursor-pointer"
                      onClick={() => setExpandedID((current) => current === row.id ? null : row.id)}
                    >
                      <TableCell className="max-w-72 whitespace-normal">
                        <div className="font-medium">{row.name}</div>
                        <div className="text-xs text-muted-foreground">{row.message || row.type}</div>
                      </TableCell>
                      {scope !== 'current' && <TableCell><div>{row.scope === 'control_plane' ? (zh ? '控制平面' : 'Control plane') : (zh ? '节点' : 'Node')}</div>{row.node_id && <div className="text-xs text-muted-foreground">{row.node_name || row.node_id}</div>}</TableCell>}
                      <TableCell><Progress value={Number(row.progress)} /></TableCell>
                      <TableCell><StatusBadge tone={taskTone(row.status)}>{row.status}</StatusBadge></TableCell>
                      <TableCell className="text-muted-foreground tabular-nums">{formatDateTime(row.created_at)}</TableCell>
                    </TableRow>
                    {expandedID === row.id && (
                      <TableRow className="hover:bg-transparent">
                        <TableCell colSpan={scope === 'current' ? 4 : 5} className="whitespace-normal bg-muted/40">
                          <TaskLogs task={row} />
                        </TableCell>
                      </TableRow>
                    )}
                  </Fragment>
                ))}
              </TableBody>
            </Table></ListShell><ListPagination {...pagination} zh={zh} /></>
          )}
    </ResourceFrame>
  )
}

function LinkedTask({ id, zh }: { id: string; zh: boolean }) {
  const query = useQuery({
    queryKey: ['linked-task', id],
    queryFn: () => api<Task>(`/tasks/${encodeURIComponent(id)}`),
    refetchInterval: (query) => ['pending', 'running'].includes(query.state.data?.status ?? '') ? 2000 : false,
  })
  const task = query.data
  return <section aria-label={zh ? '关联任务' : 'Linked task'} className="mb-4 space-y-3 border-y py-4">
    <div className="flex items-center justify-between gap-3"><p className="text-xs text-muted-foreground">{zh ? '关联任务' : 'Linked task'}</p><Button size="sm" variant="ghost" nativeButton={false} render={<Link to="/tasks" hash="" />}>{zh ? '关闭任务详情' : 'Close task details'}</Button></div>
    {query.isPending ? <LoadingState compact label={zh ? '正在加载任务' : 'Loading task'} /> : query.isError ? <p role="alert" className="text-sm text-destructive">{zh ? '无法加载关联任务' : 'Unable to load linked task'}</p> : task && <>
      <div className="flex flex-wrap items-center gap-3"><h2 className="min-w-0 break-words text-sm font-medium">{task.name}</h2><StatusBadge tone={taskTone(task.status)}>{task.status}</StatusBadge></div>
      <p className="break-all text-xs text-muted-foreground">{task.scope === 'node' ? task.node_name || task.node_id : zh ? '控制平面' : 'Control plane'} · {task.id}</p>
      <Progress value={Number(task.progress)} />
      <TaskLogs task={task} />
    </>}
  </section>
}

function TaskLogs({ task }: { task: Task }) {
  const { formatTime } = useDateTime()

  const { language } = useI18n()
  const zh = language === 'zh-CN'
  const logsPath = task.scope === 'node' && task.node_id ? nodePath(task.node_id, `/tasks/${encodeURIComponent(task.id)}/logs`) : `/tasks/${encodeURIComponent(task.id)}/logs`
  const logs = useQuery({ queryKey: ['task-logs', task.scope, task.node_id, task.id], queryFn: () => api<Log[]>(logsPath), refetchInterval: task.status === 'running' ? 1_000 : false })
  const rawLogs = logs.data ?? []
  const visibleLogs = compactTaskLogs(rawLogs)
  const pagination = useListPagination(visibleLogs)
  const compactedCount = rawLogs.length - visibleLogs.length
  if (logs.isPending) return <LoadingState embedded compact rows={3} label={zh ? '正在加载任务输出' : 'Loading task output'} />
  return (
    <><AIAnalyzeButton kind="task" id={task.id} nodeID={task.node_id} /><div className="flex max-h-64 flex-col gap-1.5 overflow-y-auto overscroll-contain">
      {rawLogs.length === 0 && <p className="py-2 text-center text-sm text-muted-foreground">{zh ? '等待任务输出…' : 'Waiting for task output…'}</p>}
      {pagination.items.map((log) => (
        <div key={log.id} className="flex items-baseline gap-3">
          <span className="shrink-0 font-mono text-xs text-muted-foreground tabular-nums">{formatTime(log.created_at)}</span>
          <span className={cn('font-mono text-xs break-all', log.level === 'error' ? 'text-destructive' : 'text-foreground')}>{log.message}</span>
        </div>
      ))}
    </div>{compactedCount > 0 && <p className="pt-2 text-xs text-muted-foreground">{zh ? `已将 ${compactedCount} 条重复的 Layer 进度刷新合并到最新状态。` : `${compactedCount} repeated layer progress updates were merged into their latest states.`}</p>}<ListPagination {...pagination} zh={zh} /></>
  )
}
