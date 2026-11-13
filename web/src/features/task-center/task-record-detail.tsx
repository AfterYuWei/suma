import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { cn } from '@/lib/utils'
import { Button } from '../../components/ui/button'
import { LoadingState } from '../../components/ui/loading-state'
import { ErrorState } from '../../components/ui/error-state'
import { Progress } from '../../components/ui/progress'
import { Sheet, SheetContent, SheetTitle } from '../../components/ui/sheet'
import { StatusBadge } from '../../components/ui/status-badge'
import { api } from '../../lib/api'
import { useUIStore } from '../../stores/ui'
import { useI18n } from '../../lib/i18n'
import { useDateTime } from '../../lib/time-zone'
import { compactTaskLogs } from '../tasks/compact-task-logs'
import { taskKindFor } from './kinds'
import { useTaskRecord } from './queries'
import { triggerSourceLabel } from './history-labels'
import type { TaskLog, TaskStep, TaskRecord } from './types'

const statusTone = (status: string) => status === 'success' ? 'success' : status === 'failed' ? 'critical' : status === 'running' ? 'warning' : 'neutral'

// TaskRecordDetail is the record inspector: summary, recorded input
// parameters, structured output, per-service steps and the full log.
export function TaskRecordDetail({ id: initialID, open, onOpenChange }: { id: string | undefined; open: boolean; onOpenChange: (open: boolean) => void }) {
  const { t, language } = useI18n()
  const zh = language === 'zh-CN'
  const { formatDateTime, formatTime } = useDateTime()
  const [id, setID] = useState(initialID)
  useEffect(() => { setID(initialID) }, [initialID, open])
  const client = useQueryClient()
  const query = useTaskRecord(open ? id : undefined)
  const task = query.data
  const logs = useQuery({
    queryKey: ['task-record-logs', id],
    queryFn: () => api<TaskLog[]>(`/tasks/${encodeURIComponent(id ?? '')}/logs`),
    enabled: open && !!id && !!task,
    refetchInterval: task && ['pending', 'running'].includes(task.status) ? 1_000 : false,
  })
  const steps = useQuery({
    queryKey: ['task-record-steps', id],
    queryFn: () => api<TaskStep[]>(`/tasks/${encodeURIComponent(id ?? '')}/steps`),
    enabled: open && !!id && !!task,
    refetchInterval: task && ['pending', 'running'].includes(task.status) ? 2_000 : false,
  })
  const children = useQuery({
    queryKey: ['task-children', id], queryFn: () => api<TaskRecord[]>(`/tasks/${encodeURIComponent(id ?? '')}/children`), enabled: open && !!task,
    refetchInterval: task && ['pending', 'running'].includes(task.status) ? 2000 : false,
  })
  const cancel = useMutation({ mutationFn: (target: string) => api(`/tasks/${encodeURIComponent(target)}/cancel`, { method: 'POST' }), onSuccess: (_data, target) => client.invalidateQueries({ queryKey: ['task-record', target] }) })
  const resetCancel = cancel.reset
  useEffect(() => { resetCancel() }, [id, resetCancel])
  const setCurrentNodeID = useUIStore(state => state.setCurrentNodeID)
  const kind = task ? taskKindFor(task.type) : undefined
  const params = task?.params
  const project = params && typeof params === 'object' && 'project' in params && typeof params.project === 'string' ? params.project : undefined
  const businessHref = project ? kind?.businessHref?.(project) : undefined
  const compacted = compactTaskLogs(logs.data ?? [])
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="gap-0 overflow-y-auto data-[side=right]:w-full data-[side=right]:sm:w-[720px] data-[side=right]:sm:max-w-[calc(100vw-2rem)]">
        {query.isError ? <ErrorState title={zh ? '无法加载任务记录' : 'Unable to load task record'} description={query.error.message} /> : query.isPending || !task ? <LoadingState label={zh ? '正在加载任务记录' : 'Loading task record'} /> : (
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
              <p className="break-words text-xs text-muted-foreground">{zh ? '触发来源' : 'Trigger source'} · {triggerSourceLabel(task.triggered_by, zh)}{task.run_id ? ` · #${task.run_id}` : ''}{task.schedule_id ? ` · ${zh ? '规则' : 'Rule'} #${task.schedule_id} ${task.schedule_name ?? ''}` : ''}</p>
              {task.message && <p className={cn('break-all text-xs', task.status === 'failed' ? 'text-destructive' : 'text-muted-foreground')}>{task.message}</p>}
              {task.result_error && <ErrorState title={zh ? '结果记录异常' : 'Result evidence unavailable'} description={task.result_error} />}
            </div>
            <div className="flex flex-wrap gap-2">
              {businessHref && <Button size="sm" variant="outline" nativeButton={false} render={<a href={businessHref} onClick={async event => {
                if (task.scope !== 'node' || !task.node_id) return
                event.preventDefault()
                if (await setCurrentNodeID(task.node_id)) window.location.assign(businessHref)
              }} />}>{zh ? '查看业务来源' : 'View business source'}</Button>}
              {task.parent_task_id && <Button size="sm" variant="outline" onClick={() => setID(task.parent_task_id)}>{zh ? '查看父任务' : 'View parent task'}</Button>}
              {id !== initialID && <Button size="sm" variant="ghost" onClick={() => setID(initialID)}>{zh ? '返回原任务' : 'Back to original task'}</Button>}
              {['pending', 'running'].includes(task.status) && <Button size="sm" variant="outline" disabled={cancel.isPending || cancel.isSuccess} onClick={() => { if (id) cancel.mutate(id) }}>{cancel.isSuccess ? (zh ? '正在取消…' : 'Canceling…') : zh ? '取消任务' : 'Cancel task'}</Button>}
            </div>
            {cancel.isError && <ErrorState description={cancel.error.message} />}
            {children.isError && <ErrorState title={zh ? '无法加载关联执行' : 'Unable to load child tasks'} description={children.error.message} />}
            {!!children.data?.length && <RecordSection title={zh ? '关联子任务' : 'Child tasks'}><div className="flex flex-col items-start gap-1">{children.data.map(child => <Button key={child.id} size="sm" variant="ghost" className="h-auto max-w-full whitespace-normal text-left" onClick={() => setID(child.id)}>{child.node_name || child.node_id} · {child.name} · {child.status}</Button>)}</div></RecordSection>}
            {task.params != null && (
              <RecordSection title={zh ? '入参' : 'Input parameters'}>
                <details><summary className="cursor-pointer text-xs text-muted-foreground">{zh ? '展开原始入参' : 'Show recorded input'}</summary><JSONView value={task.params} /></details>
              </RecordSection>
            )}
            {(task.result != null || kind?.resultView) && (
              <RecordSection title={zh ? '出参 / 结果' : 'Output / result'}>
                {open && kind?.resultView && kind.resultView(task)}
                {task.result != null && <details><summary className="cursor-pointer text-xs text-muted-foreground">{zh ? '展开完整结果 JSON' : 'Show full result JSON'}</summary><JSONView value={task.result} /></details>}
              </RecordSection>
            )}
            {steps.isError && <ErrorState title={zh ? '无法加载步骤' : 'Unable to load steps'} description={steps.error.message} />}
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
              {logs.isError && <ErrorState description={logs.error.message} />}
              <div className="max-h-72 space-y-1 overflow-y-auto overscroll-contain">
                {!logs.isError && compacted.length === 0 && <p className="py-2 text-center text-sm text-muted-foreground">{zh ? '等待任务输出…' : 'Waiting for task output…'}</p>}
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
