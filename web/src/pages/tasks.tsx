import { Fragment, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { cn } from '@/lib/utils'
import { Button } from '../components/ui/button'
import { ListShell } from '../components/ui/list-shell'
import { ListPagination } from '../components/ui/list-pagination'
import { useListPagination } from '../components/ui/use-list-pagination'
import { LoadingState } from '../components/ui/loading-state'
import { Progress } from '../components/ui/progress'
import { Spinner } from '../components/ui/spinner'
import { StatusBadge } from '../components/ui/status-badge'
import { Tabs, TabsList, TabsTrigger } from '../components/ui/tabs'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'
import { api } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { nodePath } from '../lib/nodes'
import { promptDialog } from '../stores/dialog'
import { useUIStore } from '../stores/ui'
import { ResourceFrame } from './images'

interface Task { id: string; scope: 'control_plane' | 'node'; node_id?: string; node_name?: string; type: string; name: string; status: string; progress: number; message: string; created_at: string }
interface Log { id: number; level: string; message: string; created_at: string }

const taskTone = (status: string) => status === 'success' ? 'success' : status === 'failed' ? 'critical' : status === 'running' ? 'warning' : 'neutral'

export function TasksPage() {
  const nodeID = useUIStore((state) => state.currentNodeID)
  const client = useQueryClient()
  const { t, language } = useI18n()
  const zh = language === 'zh-CN'
  const [expandedID, setExpandedID] = useState<string | null>(null)
  const [scope, setScope] = useState<'current' | 'control_plane' | 'all'>('current')
  const query = useQuery({ queryKey: ['tasks', scope, nodeID], queryFn: () => api<Task[]>(scope === 'current' ? nodePath(nodeID, '/tasks') : `/tasks?scope=${scope}`), refetchInterval: 2_000 })
  const pagination = useListPagination(query.data ?? [], scope)
  const prune = useMutation({ mutationFn: () => api(nodePath(nodeID, '/system/prune'), { method: 'POST', body: JSON.stringify({ confirm: 'PRUNE' }) }), onSuccess: () => client.invalidateQueries({ queryKey: ['tasks', 'current', nodeID] }) })
  const startPrune = async () => { const value = await promptDialog({ title: t('systemPrune'), description: t('systemPruneDescription'), confirmLabel: t('systemPrune'), danger: true, input: { label: t('typeToConfirm', { value: 'PRUNE' }), requiredValue: 'PRUNE' } }); if (value === 'PRUNE') prune.mutate() }

  return (
    <ResourceFrame
      title={t('tasks')}
      detail={zh ? '长时间运行的 Docker 与 Compose 操作' : 'Long-running Docker and Compose operations'}
      action={(
        <div className="flex flex-wrap items-center gap-2"><Tabs value={scope} onValueChange={(value) => { setScope(value as typeof scope); setExpandedID(null) }}><TabsList><TabsTrigger value="current">{zh ? '当前节点' : 'Current node'}</TabsTrigger><TabsTrigger value="control_plane">{zh ? '控制平面' : 'Control plane'}</TabsTrigger><TabsTrigger value="all">{zh ? '全部' : 'All'}</TabsTrigger></TabsList></Tabs><Button variant="outline" className="text-destructive hover:text-destructive" disabled={prune.isPending} onClick={() => void startPrune()}>
            {prune.isPending && <Spinner className="size-4" />}
            {t('systemPrune')}
          </Button></div>
      )}
    >
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
                      <TableCell className="text-muted-foreground tabular-nums">{new Date(row.created_at).toLocaleString(language)}</TableCell>
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

function TaskLogs({ task }: { task: Task }) {
  const { language } = useI18n()
  const zh = language === 'zh-CN'
  const logsPath = task.scope === 'node' && task.node_id ? nodePath(task.node_id, `/tasks/${encodeURIComponent(task.id)}/logs`) : `/tasks/${encodeURIComponent(task.id)}/logs`
  const logs = useQuery({ queryKey: ['task-logs', task.scope, task.node_id, task.id], queryFn: () => api<Log[]>(logsPath), refetchInterval: task.status === 'running' ? 1_000 : false })
  const pagination = useListPagination(logs.data ?? [])
  if (logs.isPending) return <LoadingState embedded compact rows={3} label={zh ? '正在加载任务输出' : 'Loading task output'} />
  return (
    <><div className="flex max-h-64 flex-col gap-1.5 overflow-y-auto overscroll-contain">
      {(logs.data ?? []).length === 0 && <p className="py-2 text-center text-sm text-muted-foreground">{zh ? '等待任务输出…' : 'Waiting for task output…'}</p>}
      {pagination.items.map((log) => (
        <div key={log.id} className="flex items-baseline gap-3">
          <span className="shrink-0 font-mono text-xs text-muted-foreground tabular-nums">{new Date(log.created_at).toLocaleTimeString(language)}</span>
          <span className={cn('font-mono text-xs break-all', log.level === 'error' ? 'text-destructive' : 'text-foreground')}>{log.message}</span>
        </div>
      ))}
    </div><ListPagination {...pagination} zh={zh} /></>
  )
}
