import { Fragment, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { cn } from '@/lib/utils'
import { Button } from '../components/ui/button'
import { ListShell } from '../components/ui/list-shell'
import { LoadingState } from '../components/ui/loading-state'
import { Progress } from '../components/ui/progress'
import { Spinner } from '../components/ui/spinner'
import { StatusBadge } from '../components/ui/status-badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'
import { api } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { nodePath } from '../lib/nodes'
import { promptDialog } from '../stores/dialog'
import { useUIStore } from '../stores/ui'
import { ResourceFrame } from './images'

interface Task { id: string; type: string; name: string; status: string; progress: number; message: string; created_at: string }
interface Log { id: number; level: string; message: string; created_at: string }

const taskTone = (status: string) => status === 'success' ? 'success' : status === 'failed' ? 'critical' : status === 'running' ? 'warning' : 'neutral'

export function TasksPage() {
  const nodeID = useUIStore((state) => state.currentNodeID)
  const client = useQueryClient()
  const { t, language } = useI18n()
  const zh = language === 'zh-CN'
  const [expandedID, setExpandedID] = useState<string | null>(null)
  const query = useQuery({ queryKey: ['tasks', nodeID], queryFn: () => api<Task[]>(`/tasks?node_id=${encodeURIComponent(nodeID)}`), refetchInterval: 2_000 })
  const prune = useMutation({ mutationFn: () => api(nodePath(nodeID, '/system/prune'), { method: 'POST', body: JSON.stringify({ confirm: 'PRUNE' }) }), onSuccess: () => client.invalidateQueries({ queryKey: ['tasks', nodeID] }) })
  const startPrune = async () => { const value = await promptDialog({ title: t('systemPrune'), description: t('systemPruneDescription'), confirmLabel: t('systemPrune'), danger: true, input: { label: t('typeToConfirm', { value: 'PRUNE' }), requiredValue: 'PRUNE' } }); if (value === 'PRUNE') prune.mutate() }

  return (
    <ResourceFrame
      title={t('tasks')}
      detail={zh ? '长时间运行的 Docker 与 Compose 操作' : 'Long-running Docker and Compose operations'}
      action={(
        <Button variant="outline" className="text-destructive hover:text-destructive" disabled={prune.isPending} onClick={() => void startPrune()}>
          {prune.isPending && <Spinner className="size-4" />}
          {t('systemPrune')}
        </Button>
      )}
    >
      {query.isPending
        ? <LoadingState label={zh ? '正在加载任务' : 'Loading tasks'} />
        : (
            <ListShell><Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{zh ? '任务' : 'Task'}</TableHead>
                  <TableHead className="w-56">{zh ? '进度' : 'Progress'}</TableHead>
                  <TableHead className="w-28">{zh ? '状态' : 'Status'}</TableHead>
                  <TableHead className="w-44">{zh ? '创建时间' : 'Created'}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(query.data ?? []).length === 0 && (
                  <TableRow>
                    <TableCell colSpan={4} className="h-24 text-center text-muted-foreground">{zh ? '暂无任务' : 'No tasks'}</TableCell>
                  </TableRow>
                )}
                {(query.data ?? []).map((row) => (
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
                      <TableCell><Progress value={Number(row.progress)} /></TableCell>
                      <TableCell><StatusBadge tone={taskTone(row.status)}>{row.status}</StatusBadge></TableCell>
                      <TableCell className="text-muted-foreground tabular-nums">{new Date(row.created_at).toLocaleString(language)}</TableCell>
                    </TableRow>
                    {expandedID === row.id && (
                      <TableRow className="hover:bg-transparent">
                        <TableCell colSpan={4} className="whitespace-normal bg-muted/40">
                          <TaskLogs task={row} />
                        </TableCell>
                      </TableRow>
                    )}
                  </Fragment>
                ))}
              </TableBody>
            </Table></ListShell>
          )}
    </ResourceFrame>
  )
}

function TaskLogs({ task }: { task: Task }) {
  const { language } = useI18n()
  const zh = language === 'zh-CN'
  const logs = useQuery({ queryKey: ['task-logs', task.id], queryFn: () => api<Log[]>(`/tasks/${task.id}/logs`), refetchInterval: task.status === 'running' ? 1_000 : false })
  if (logs.isPending) return <LoadingState embedded compact rows={3} label={zh ? '正在加载任务输出' : 'Loading task output'} />
  return (
    <div className="flex max-h-64 flex-col gap-1.5 overflow-y-auto">
      {(logs.data ?? []).length === 0 && <p className="py-2 text-center text-sm text-muted-foreground">{zh ? '等待任务输出…' : 'Waiting for task output…'}</p>}
      {(logs.data ?? []).map((log) => (
        <div key={log.id} className="flex items-baseline gap-3">
          <span className="shrink-0 font-mono text-xs text-muted-foreground tabular-nums">{new Date(log.created_at).toLocaleTimeString(language)}</span>
          <span className={cn('font-mono text-xs break-all', log.level === 'error' ? 'text-destructive' : 'text-foreground')}>{log.message}</span>
        </div>
      ))}
    </div>
  )
}
