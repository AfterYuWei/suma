import { useQuery } from '@tanstack/react-query'
import { cn } from '@/lib/utils'
import { Button } from '../../components/ui/button'
import { LoadingState } from '../../components/ui/loading-state'
import { Progress } from '../../components/ui/progress'
import { Sheet, SheetContent, SheetTitle } from '../../components/ui/sheet'
import { StatusBadge } from '../../components/ui/status-badge'
import { api } from '../../lib/api'
import { useI18n } from '../../lib/i18n'
import { nodePath } from '../../lib/nodes'
import { useDateTime } from '../../lib/time-zone'
import { compactTaskLogs } from '../tasks/compact-task-logs'
import { taskKindFor } from './kinds'
import { useTaskRecord } from './queries'
import type { TaskLog, TaskStep } from './types'

const statusTone = (status: string) => status === 'success' ? 'success' : status === 'failed' ? 'critical' : status === 'running' ? 'warning' : 'neutral'

// TaskRecordDetail is the record inspector: summary, recorded input
// parameters, structured output, per-service steps and the full log.
export function TaskRecordDetail({ id, open, onOpenChange }: { id: string | undefined; open: boolean; onOpenChange: (open: boolean) => void }) {
  const { t, language } = useI18n()
  const zh = language === 'zh-CN'
  const { formatDateTime, formatTime } = useDateTime()
  const query = useTaskRecord(open ? id : undefined)
  const task = query.data
  const logs = useQuery({
    queryKey: ['task-record-logs', id],
    queryFn: () => api<TaskLog[]>(task?.scope === 'node' && task?.node_id ? nodePath(task.node_id, `/tasks/${encodeURIComponent(id ?? '')}/logs`) : `/tasks/${encodeURIComponent(id ?? '')}/logs`),
    enabled: open && !!id && !!task,
    refetchInterval: task && ['pending', 'running'].includes(task.status) ? 1_000 : false,
  })
  const steps = useQuery({
    queryKey: ['task-record-steps', id],
    queryFn: () => api<TaskStep[]>(task?.scope === 'node' && task?.node_id ? nodePath(task.node_id, `/tasks/${encodeURIComponent(id ?? '')}/steps`) : `/tasks/${encodeURIComponent(id ?? '')}/steps`),
    enabled: open && !!id && !!task,
  })
  const kind = task ? taskKindFor(task.type) : undefined
  const compacted = compactTaskLogs(logs.data ?? [])
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="gap-0 overflow-y-auto data-[side=right]:w-full data-[side=right]:sm:w-[720px] data-[side=right]:sm:max-w-[calc(100vw-2rem)]">
        {query.isPending || !task ? <LoadingState label={zh ? '正在加载任务记录' : 'Loading task record'} /> : (
          <div className="space-y-5 p-4">
            <div className="space-y-2">
              <SheetTitle className="flex flex-wrap items-center gap-2 break-words text-base font-medium">{task.name}<StatusBadge tone={statusTone(task.status)}>{task.status}</StatusBadge></SheetTitle>
              <p className="break-all text-xs text-muted-foreground">
                {task.scope === 'node' ? `${zh ? '节点' : 'Node'} · ${task.node_name || task.node_id}` : zh ? '控制平面' : 'Control plane'}
                {' · '}{kind ? kind.label(zh) : task.type}{zh ? ' · 类型 ' : ' · type '}{task.type}
                {' · '}{formatDateTime(task.created_at)}
                {task.finished_at ? ` · ${zh ? '完成于' : 'finished'} ${formatDateTime(task.finished_at)}` : ''}
              </p>
              <Progress value={Number(task.progress)} />
              {task.message && <p className={cn('break-all text-xs', task.status === 'failed' ? 'text-destructive' : 'text-muted-foreground')}>{task.message}</p>}
            </div>
            {task.params != null && (
              <RecordSection title={zh ? '入参' : 'Input parameters'}>
                <JSONView value={task.params} />
              </RecordSection>
            )}
            {(task.result != null || kind?.resultView) && (
              <RecordSection title={zh ? '出参 / 结果' : 'Output / result'}>
                {kind?.resultView ? kind.resultView(task) : <JSONView value={task.result} />}
              </RecordSection>
            )}
            {(steps.data?.length ?? 0) > 0 && (
              <RecordSection title={zh ? '步骤' : 'Steps'}>
                <div className="space-y-1.5">
                  {(steps.data ?? []).map((step) => (
                    <div key={step.id} className="flex items-center justify-between gap-3 text-xs">
                      <span className="max-w-56 truncate font-mono" title={step.id}>{step.id}</span>
                      <span className="flex min-w-0 flex-1 items-center gap-2"><Progress value={step.progress} /><span className="shrink-0 tabular-nums text-muted-foreground">{step.status} {step.current}/{step.total}</span></span>
                    </div>
                  ))}
                </div>
              </RecordSection>
            )}
            <RecordSection title={zh ? '日志' : 'Logs'}>
              <div className="max-h-72 space-y-1 overflow-y-auto overscroll-contain">
                {compacted.length === 0 && <p className="py-2 text-center text-sm text-muted-foreground">{zh ? '等待任务输出…' : 'Waiting for task output…'}</p>}
                {compacted.map((log) => (
                  <div key={log.id} className="flex items-baseline gap-3">
                    <span className="shrink-0 font-mono text-xs tabular-nums text-muted-foreground">{formatTime(log.created_at)}</span>
                    <span className={cn('break-all font-mono text-xs', log.level === 'error' ? 'text-destructive' : 'text-foreground')}>{log.message}</span>
                  </div>
                ))}
              </div>
            </RecordSection>
            <div className="flex justify-end"><Button size="sm" variant="ghost" onClick={() => onOpenChange(false)}>{t('cancel')}</Button></div>
          </div>
        )}
      </SheetContent>
    </Sheet>
  )
}

function RecordSection({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="section-surface space-y-2">
      <p className="text-xs font-medium text-muted-foreground">{title}</p>
      {children}
    </section>
  )
}

function JSONView({ value }: { value: unknown }) {
  if (value == null) return null
  const text = typeof value === 'string' ? value : JSON.stringify(value, null, 2)
  return <pre className="max-h-48 overflow-auto break-all whitespace-pre-wrap font-mono text-xs text-muted-foreground">{text}</pre>
}
