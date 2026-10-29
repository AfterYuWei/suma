import { Check, ChevronRight, CircleAlert, CircleStop, Layers, ListTodo, Terminal } from 'lucide-react'
import { useRef } from 'react'
import { Button } from '../../components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '../../components/ui/dialog'
import { Progress } from '../../components/ui/progress'
import { Spinner } from '../../components/ui/spinner'
import { StatusBadge } from '../../components/ui/status-badge'
import { useDateTime } from '../../lib/time-zone'
import { cn } from '../../lib/utils'
import { useLogAutoScroll } from '../containers/use-log-auto-scroll'
import { composeActionLabel, composeOutputLogs, composeProgress, composeStageLabel, type ComposeOperation, type ComposeOutputLog, type ComposeResourceProgress, type ComposeTask, type ComposeTaskLog } from './action-progress'

export function ComposeActionDialog(props: {
  open: boolean; operation: ComposeOperation | null; task?: ComposeTask; logs: ComposeTaskLog[]
  submitting: boolean; loading: boolean; error?: string; canceling: boolean; zh: boolean; projectName: string
  onOpenChange: (open: boolean) => void; onCancel: () => void; onViewTasks: () => void
}) {
  return <Dialog open={props.open} onOpenChange={props.onOpenChange}>
    {props.open && <ComposeActionContent key={props.operation?.taskID || 'submitting'} {...props} />}
  </Dialog>
}

function ComposeActionContent({ operation, task, logs, submitting, loading, error, canceling, zh, projectName, onOpenChange, onCancel, onViewTasks }: Parameters<typeof ComposeActionDialog>[0]) {
  const titleRef = useRef<HTMLHeadingElement>(null)
  const status = task?.status || (submitting ? 'submitting' : error ? 'failed' : 'pending')
  const running = submitting || status === 'pending' || status === 'running'
  const failed = status === 'failed'
  const canceled = status === 'canceled'
  const success = status === 'success'
  const action = operation?.action || ''
  const progress = composeProgress(action, task, logs)
  const statusLabel = zh
    ? ({ submitting: '正在提交', pending: '等待中', running: '执行中', success: '已完成', failed: '失败', canceled: '已取消' }[status] ?? status)
    : ({ submitting: 'Submitting', pending: 'Pending', running: 'Running', success: 'Completed', failed: 'Failed', canceled: 'Canceled' }[status] ?? status)
  const phases = action === 'update' ? [zh ? '拉取镜像' : 'Pull images', zh ? '重建并启动' : 'Recreate & start'] : [composeActionLabel(action, zh)]
  const activePhase = progress.phase === 'recreate' ? 1 : 0
  const phaseLabel = phases[activePhase]
  const summary = success
    ? zh ? '操作已完成' : 'Operation completed'
    : canceled ? zh ? `已取消 · ${phaseLabel}` : `Canceled · ${phaseLabel}`
      : failed ? zh ? `执行失败 · ${phaseLabel}` : `Failed · ${phaseLabel}`
        : submitting ? zh ? '正在提交任务' : 'Submitting task'
          : loading || status === 'pending' ? zh ? '等待执行' : 'Waiting to start'
            : phaseLabel
  const description = success
    ? action === 'update' ? zh ? '镜像拉取与服务重建已完成。容器运行状态可在服务列表查看。' : 'Image pull and service recreation completed. Check the service list for container runtime status.' : zh ? 'Docker Compose 已完成本次操作。' : 'Docker Compose completed this operation.'
    : canceled ? zh ? '操作已停止，已完成的变更会保留。' : 'The operation stopped. Changes already applied are retained.'
      : submitting ? zh ? '正在创建后台任务…' : 'Creating the background task…'
        : loading ? zh ? '正在读取任务状态…' : 'Loading task status…'
          : status === 'pending' ? zh ? '任务已创建，等待 Docker Compose 开始执行。' : 'Task created. Waiting for Docker Compose to start.'
      : progress.phase === 'pull' ? action === 'update' ? zh ? '正在获取项目镜像；镜像就绪后开始重建。' : 'Fetching project images. Recreation starts when the images are ready.' : zh ? '正在获取项目镜像。' : 'Fetching project images.'
        : progress.phase === 'recreate' ? zh ? '镜像已就绪，正在应用配置并重建、启动容器。' : 'Images are ready. Applying configuration, recreating and starting containers.'
          : zh ? '正在等待 Docker Compose 执行结果。' : 'Waiting for Docker Compose to finish.'
  const diagnostics = failed ? [...new Set([
    ...logs.filter(log => log.level === 'error' || /\berror\b|failed to|denied:|unauthorized:|manifest.*not found|port is already allocated|no space left/i.test(log.message)).map(log => log.message),
    task?.message,
  ].filter(Boolean))].slice(-3) : []
  const visibleImages = progress.images
  const layersDone = progress.layers.filter(row => row.state === 'complete').length
  return <DialogContent initialFocus={titleRef} className="flex h-[min(40rem,calc(100dvh-3rem))] w-[calc(100vw-3rem)] max-w-[70rem] flex-col overflow-hidden sm:max-w-[70rem]">
    <DialogHeader className="shrink-0">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2 pr-8">
        <DialogTitle ref={titleRef} tabIndex={-1} className="leading-snug outline-none">{zh ? `${composeActionLabel(action, zh)}进度` : `${composeActionLabel(action, zh)} progress`}</DialogTitle>
        <StatusBadge tone={success ? 'success' : failed ? 'critical' : canceled ? 'neutral' : 'warning'}>{statusLabel}</StatusBadge>
      </div>
      <DialogDescription className="break-all">{projectName}</DialogDescription>
    </DialogHeader>

    <div className="grid min-h-0 flex-1 grid-cols-[minmax(0,0.85fr)_minmax(0,1.15fr)] gap-4">
      <div data-testid="compose-task-overview" className="min-h-0 min-w-0 space-y-4 overflow-y-auto overscroll-contain">
        <section aria-label={zh ? '执行阶段' : 'Execution stages'} className="space-y-4 rounded-lg bg-muted/45 p-3">
          {phases.length > 1 && <ol className="flex gap-2" aria-label={zh ? '任务阶段' : 'Task stages'}>
            {phases.map((name, index) => {
              const done = success || index < activePhase
              const current = !success && index === activePhase
              return <li key={name} aria-current={current ? 'step' : undefined} className={cn('flex min-w-0 flex-1 items-center gap-2 rounded-md px-2.5 py-2 text-xs', current ? 'bg-background text-foreground' : 'text-muted-foreground')}>
                <span className={cn('flex size-5 shrink-0 items-center justify-center rounded-full text-[11px] tabular-nums', done ? 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400' : current ? 'bg-foreground/10' : 'bg-muted')}>
                  {done ? <Check className="size-3" /> : index + 1}
                </span><span className="min-w-0">{name}</span>
              </li>
            })}
          </ol>}
          <div className="flex items-start gap-3">
            {running ? <Spinner className="mt-0.5 size-5 shrink-0 motion-reduce:animate-none" aria-label={statusLabel} /> : success ? <Check className="mt-0.5 size-5 shrink-0 text-emerald-600 dark:text-emerald-400" /> : canceled ? <CircleStop className="mt-0.5 size-5 shrink-0 text-muted-foreground" /> : <CircleAlert className="mt-0.5 size-5 shrink-0 text-destructive" />}
            <div className="min-w-0 space-y-1.5"><h3 className="text-sm font-medium" aria-live="polite">{summary}</h3>
              {!failed && <p className="text-xs leading-relaxed text-muted-foreground">{description}</p>}
              {running && progress.phase === 'execute' && task?.message && <p className="text-xs leading-relaxed break-words text-muted-foreground [overflow-wrap:anywhere]">{task.message}</p>}
            </div>
          </div>
        </section>

        {(diagnostics.length > 0 || error) && <div role="alert" className="space-y-1.5 rounded-lg bg-destructive/10 p-3 text-xs leading-relaxed text-destructive">
          <p className="font-medium">{failed ? zh ? '失败原因' : 'Failure reason' : zh ? '状态读取或操作异常' : 'Status or operation error'}</p>
          {[...new Set([...diagnostics, error].filter(Boolean))].map(message => <p key={message} className="whitespace-pre-wrap break-words [overflow-wrap:anywhere]">{message}</p>)}
        </div>}

        {visibleImages.length > 0 && <ResourceSection title={zh ? '项目镜像' : 'Project images'} rows={visibleImages} finished={progress.pullComplete} running={running} zh={zh} />}
        {progress.resources.length > 0 && <ResourceSection title={zh ? '容器与资源' : 'Containers & resources'} rows={progress.resources} finished={success} running={running} zh={zh} />}
        {progress.layers.length > 0 && <details className="group rounded-lg bg-muted/30" data-testid="compose-layer-details">
          <summary className="flex cursor-pointer list-none items-center gap-2 p-3 text-xs [&::-webkit-details-marker]:hidden">
            <ChevronRight className="size-3.5 shrink-0 transition-transform group-open:rotate-90" /><Layers className="size-3.5 shrink-0 text-muted-foreground" />
            <span className="font-medium">{zh ? '镜像层详情' : 'Image layer details'}</span>
            <span className="ml-auto text-right text-muted-foreground tabular-nums">{zh ? `已就绪 ${layersDone} / ${progress.layers.length} 个已报告层` : `${layersDone} / ${progress.layers.length} reported layers ready`}</span>
          </summary>
          <div className="max-h-52 space-y-1 overflow-y-auto overscroll-contain px-2 pb-2">
            <p className="px-2 py-1 text-[11px] leading-relaxed text-muted-foreground">{zh ? '各层显示最近报告的状态；字节比例仅代表当前下载或解压阶段。' : 'Latest reported state per layer. Byte ratios refer to its current download or extraction stage.'}</p>
            {progress.layers.map(row => <ResourceRow key={row.name} row={row} finished={progress.pullComplete} running={running} zh={zh} />)}
          </div>
        </details>}
        {visibleImages.length === 0 && progress.resources.length === 0 && progress.layers.length === 0 && running && <p className="py-1 text-xs text-muted-foreground">{zh ? '等待 Docker Compose 报告资源状态…' : 'Waiting for Docker Compose resource updates…'}</p>}
      </div>
      <LiveOutput logs={logs} task={task} taskID={operation?.taskID} zh={zh} />
    </div>
    <DialogFooter className="mt-0 shrink-0 flex-row flex-wrap justify-end">
      <p className="mr-auto max-w-md self-center text-xs leading-relaxed text-muted-foreground">{running ? zh ? '关闭窗口后任务继续在后台执行。' : 'The task continues in the background when this window is closed.' : zh ? '可在任务中心查看完整记录。' : 'Review the complete record in the Task Center.'}</p>
      {task && running && <Button type="button" variant="ghost" className="text-destructive" disabled={canceling} onClick={onCancel}>{canceling ? <Spinner /> : <CircleStop />}{zh ? '取消任务' : 'Cancel task'}</Button>}
      {task && !running && <Button type="button" variant="outline" onClick={onViewTasks}><ListTodo />{zh ? '查看任务' : 'View task'}</Button>}
      <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>{running ? zh ? '后台运行' : 'Run in background' : zh ? '关闭窗口' : 'Close window'}</Button>
    </DialogFooter>
  </DialogContent>
}

function ResourceSection({ title, rows, finished, running, zh }: { title: string; rows: ComposeResourceProgress[]; finished: boolean; running: boolean; zh: boolean }) {
  const done = rows.filter(row => row.state === 'complete').length
  return <section aria-label={title} className="min-w-0 space-y-2">
    <div className="flex items-center justify-between gap-3 text-xs"><h3 className="font-medium">{title}</h3><span className="text-muted-foreground tabular-nums">{zh ? `${done} / ${rows.length} 项已报告完成` : `${done} / ${rows.length} reported complete`}</span></div>
    <div className="space-y-1 rounded-lg bg-muted/30 p-1.5">{rows.map(row => <ResourceRow key={`${row.kind}:${row.name}`} row={row} finished={finished} running={running} zh={zh} />)}</div>
  </section>
}

function ResourceRow({ row, finished, running, zh }: { row: ComposeResourceProgress; finished: boolean; running: boolean; zh: boolean }) {
  const active = row.state === 'active' && running && !finished
  const interrupted = row.state === 'active' && !running && !finished
  const label = interrupted ? zh ? '已中断' : 'Interrupted' : row.state === 'active' && finished ? zh ? '操作完成' : 'Operation complete' : composeStageLabel(row.stage, zh)
  return <div data-testid="compose-resource" className="rounded-md px-2 py-2 hover:bg-muted/60">
    <div className="flex items-start gap-2.5">
      {active ? <Spinner className="mt-0.5 size-3.5 shrink-0 text-muted-foreground motion-reduce:animate-none" aria-label={label} /> : row.state === 'complete' ? <Check className="mt-0.5 size-3.5 shrink-0 text-emerald-600 dark:text-emerald-400" /> : <span className={cn('mt-1.5 size-1.5 shrink-0 rounded-full', row.state === 'failed' ? 'bg-destructive' : 'bg-muted-foreground/50')} />}
      <span className={cn('min-w-0 flex-1 text-[13px] leading-5 break-words [overflow-wrap:anywhere]', row.kind === 'layer' && 'font-mono')}>{row.name}</span>
      <span className={cn('max-w-[45%] shrink-0 text-right text-xs leading-5', row.state === 'complete' ? 'text-emerald-600 dark:text-emerald-400' : row.state === 'failed' ? 'text-destructive' : 'text-muted-foreground')}>{label}</span>
    </div>
    {row.detail && <div className="mt-1.5 space-y-1.5 pl-6">
      <div className="flex items-center justify-between gap-2 text-[11px] text-muted-foreground tabular-nums"><span>{row.detail}</span>{row.percent !== undefined && <span>{row.percent}%</span>}</div>
      {row.percent !== undefined && <Progress value={row.percent} aria-label={`${row.name} · ${composeStageLabel(row.stage, zh)}`} />}
    </div>}
  </div>
}

function LiveOutput({ logs, task, taskID, zh }: { logs: ComposeTaskLog[]; task?: ComposeTask; taskID?: string; zh: boolean }) {
  const { formatTime } = useDateTime()
  const visibleLogs = composeOutputLogs(task, logs)
  return <section aria-label={zh ? '实时输出' : 'Live output'} className="flex min-h-0 min-w-0 flex-col gap-2">
    <div className="flex shrink-0 items-center gap-2 text-xs"><Terminal className="size-3.5 text-muted-foreground" /><h3 className="font-medium">{zh ? '实时输出' : 'Live output'}</h3><span className="ml-auto text-muted-foreground tabular-nums">{zh ? `${visibleLogs.length} 条` : `${visibleLogs.length} entries`}</span></div>
    <OutputLines logs={visibleLogs} taskID={taskID} formatTime={formatTime} zh={zh} />
  </section>
}

function OutputLines({ logs, taskID, formatTime, zh }: { logs: ComposeOutputLog[]; taskID?: string; formatTime: (date: string) => string; zh: boolean }) {
  const latest = logs.at(-1)
  const latestHistory = logs.at(latest?.id === 'live' ? -2 : -1)
  const { viewportRef, onScroll } = useLogAutoScroll<HTMLDivElement>(`${latestHistory?.id}:${latest?.message}`, taskID)
  return <div ref={viewportRef} onScroll={onScroll} data-testid="compose-output-viewport" className="min-h-0 flex-1 overflow-y-auto overscroll-contain rounded-lg bg-muted/40 p-3">
    {logs.length === 0 ? <p className="text-xs text-muted-foreground">{zh ? '暂无输出' : 'No output yet'}</p> : <div className="grid grid-cols-[max-content_minmax(0,1fr)] items-baseline gap-x-3 gap-y-2 font-mono text-xs leading-relaxed">{logs.map(log => <div key={log.id} className="contents"><span className="text-[11px] text-muted-foreground tabular-nums">{log.created_at ? formatTime(log.created_at) : zh ? '实时' : 'Live'}</span><span className={cn('min-w-0 whitespace-pre-wrap break-words [overflow-wrap:anywhere]', log.level === 'error' && 'text-destructive')}>{log.message}</span></div>)}</div>}
  </div>
}
