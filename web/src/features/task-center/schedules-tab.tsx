import { useState } from 'react'
import { CalendarClock, History, MoreHorizontal, Pencil, Play, Plus } from 'lucide-react'
import { useI18n } from '../../lib/i18n'
import { confirmDialog } from '../../stores/dialog'
import { useDateTime } from '../../lib/time-zone'
import { Button } from '../../components/ui/button'
import { Checkbox } from '../../components/ui/checkbox'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '../../components/ui/dropdown-menu'
import { ErrorState } from '../../components/ui/error-state'
import { ListShell } from '../../components/ui/list-shell'
import { LoadingState } from '../../components/ui/loading-state'
import { StatusBadge } from '../../components/ui/status-badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../components/ui/table'
import type { CenterScope } from '../../pages/tasks'
import { taskKindFor } from './kinds'
import { describeCron } from './cron'
import { useDeleteSchedule, useRunSchedule, useSchedules, useUpdateSchedule } from './queries'
import { ScheduleForm } from './schedule-form'
import { TaskRecordDetail } from './task-record-detail'
import type { Schedule } from './types'
import { TriggerHistory } from './trigger-history'

const statusTone = (status?: string) => status === 'success' ? 'success' : status === 'failed' || status === 'start_failed' || status === 'parked' ? 'critical' : status === 'running' || status === 'pending' || status === 'dispatching' ? 'warning' : 'neutral'
const stateLabel = (row: Schedule, zh: boolean) => ({ enabled: zh ? '已启用' : 'Enabled', paused: zh ? '全部暂停' : 'Paused', timer_paused: zh ? '周期暂停' : 'Timer paused', parked: zh ? '已停摆' : 'Parked', archived: zh ? '已归档' : 'Archived' })[row.effective_status ?? (row.enabled ? 'enabled' : 'paused')]

export function SchedulesTab({ scope, nodeID }: { scope: CenterScope; nodeID?: string }) {
  const { language } = useI18n()
  const zh = language === 'zh-CN'
  const { formatDateTime } = useDateTime()
  const [includeArchived, setIncludeArchived] = useState(false)
  const query = useSchedules(undefined, { scope: scope === 'current' ? 'node' : scope, nodeID: scope === 'current' ? nodeID : undefined, includeArchived })
  const update = useUpdateSchedule()
  const remove = useDeleteSchedule()
  const run = useRunSchedule()
  const [form, setForm] = useState<{ row?: Schedule; manual?: boolean }>()
  const [history, setHistory] = useState<Schedule | 'all'>()
  const [taskID, setTaskID] = useState<string>()
  const [actionError, setActionError] = useState('')
  const mutate = async (row: Schedule, input: { enabled?: boolean; timer_enabled?: boolean }) => {
    setActionError('')
    const kind = taskKindFor(row.kind)
    if (kind?.manualViaEditor && (input.enabled || input.timer_enabled)) { setForm({ row }); return }
    try { await update.mutateAsync({ id: row.id, version: row.version, ...input }) }
    catch (cause) { setActionError(cause instanceof Error ? cause.message : zh ? '操作失败' : 'Unable to update') }
  }
  const runNow = async (row: Schedule) => {
    setActionError('')
    const kind = taskKindFor(row.kind)
    if (!kind) return
    if (kind.manualViaEditor) { setForm({ row, manual: true }); return }
    try { const result = await run.mutateAsync({ id: row.id, action: kind.manualAction }); if (result.task_id) setTaskID(result.task_id) }
    catch (cause) { setActionError(cause instanceof Error ? cause.message : zh ? '启动失败' : 'Unable to start') }
  }
  const archive = async (row: Schedule) => {
    if (!await confirmDialog({ title: zh ? '归档自动化规则' : 'Archive automation rule', description: zh ? `停止「${row.name}」的后续触发，保留所有执行与触发历史。仍有业务配置的清理和 CD 请使用暂停。` : `Stop future triggers for "${row.name}" and retain all history. Pause cleanup and CD rules while their business configuration remains active.`, confirmLabel: zh ? '归档' : 'Archive' })) return
    setActionError('')
    try { await remove.mutateAsync({ id: row.id, version: row.version }) }
    catch (cause) { setActionError(cause instanceof Error ? cause.message : zh ? '归档失败' : 'Unable to archive') }
  }
  return <div className="space-y-3">
    <div className="flex flex-wrap items-center justify-between gap-3"><label className="flex items-center gap-2 text-sm text-muted-foreground"><Checkbox checked={includeArchived} onCheckedChange={value => setIncludeArchived(value === true)} />{zh ? '包含已归档' : 'Include archived'}</label><div className="flex flex-wrap gap-2"><Button size="sm" variant="ghost" onClick={() => setHistory('all')}><History />{zh ? '全部触发历史' : 'All trigger history'}</Button><Button size="sm" onClick={() => setForm({})}><Plus />{zh ? '新建自动化' : 'New automation'}</Button></div></div>
    {actionError && <p role="alert" className="break-words text-sm text-destructive">{actionError}</p>}
    {query.isPending ? <LoadingState label={zh ? '正在加载自动化' : 'Loading automation'} /> : query.isError ? <ErrorState description={query.error.message} /> : <ListShell><Table className="block md:table">
      <TableHeader className="hidden md:table-header-group"><TableRow><TableHead>{zh ? '规则 / 目标' : 'Rule / target'}</TableHead><TableHead>{zh ? '触发方式 / 下次时间' : 'Triggers / next run'}</TableHead><TableHead>{zh ? '最近触发' : 'Latest trigger'}</TableHead><TableHead>{zh ? '状态' : 'State'}</TableHead><TableHead className="w-12" /></TableRow></TableHeader>
      <TableBody className="block space-y-3 md:table-row-group md:space-y-0">
        {!query.data?.length && <TableRow><TableCell colSpan={5} className="h-24 text-center text-muted-foreground">{zh ? '暂无自动化规则' : 'No automation rules'}</TableCell></TableRow>}
        {query.data?.map(row => {
          const kind = taskKindFor(row.kind), Icon = kind?.icon ?? CalendarClock
          return <TableRow key={row.id} className="grid grid-cols-[minmax(0,1fr)_auto] gap-x-3 gap-y-2 rounded-lg bg-muted/20 p-3 md:table-row md:rounded-none md:bg-transparent md:p-0">
            <TableCell className="col-start-1 row-start-1 block max-w-72 p-0 whitespace-normal md:table-cell md:p-2"><div className="flex items-center gap-2 font-medium"><Icon className="size-4 shrink-0 text-muted-foreground" /><span className="break-words">{row.name}</span></div><p className="mt-1 break-words text-xs text-muted-foreground">{kind?.label(zh) ?? row.kind} · {row.node_name || row.node_id || (zh ? '控制平面' : 'Control plane')}{row.subject ? ` · ${row.subject}` : ''}</p>{row.last_error && <p className="mt-1 break-words text-xs text-destructive">{row.last_error}</p>}</TableCell>
            <TableCell className="col-start-1 row-start-2 block max-w-64 p-0 whitespace-normal text-xs md:table-cell md:p-2"><p>{row.schedule_type === 'interval' ? (zh ? `每 ${row.interval_seconds} 秒` : `Every ${row.interval_seconds}s`) : row.schedule_type === 'none' ? (zh ? '仅手动' : 'Manual only') : `${describeCron(row.cron, zh)} · ${row.timezone}`}</p>{row.webhook_enabled && <p className="mt-1 text-muted-foreground">Webhook · {row.enabled && !row.archived_at ? (zh ? '有效' : 'active') : (zh ? '已暂停' : 'paused')}</p>}{row.next_run_at && <p className="mt-1 text-muted-foreground">{formatDateTime(row.next_run_at)}</p>}</TableCell>
            <TableCell className="col-start-1 row-start-3 block max-w-56 p-0 whitespace-normal md:table-cell md:p-2">{row.last_run_status ? <Button size="sm" variant="ghost" className="h-auto max-w-full flex-col items-start gap-1 whitespace-normal text-left" onClick={() => setHistory(row)}><StatusBadge tone={statusTone(row.last_task_status || row.last_run_status)}>{row.last_task_status || row.last_run_status}</StatusBadge><span className="break-words text-xs text-muted-foreground">{row.last_run_reason || row.last_run_error}</span></Button> : '—'}</TableCell>
            <TableCell className="col-start-2 row-start-2 block self-start p-0 md:table-cell md:p-2"><StatusBadge tone={statusTone(row.effective_status)}>{stateLabel(row, zh)}</StatusBadge></TableCell>
            <TableCell className="col-start-2 row-start-1 block justify-self-end p-0 md:table-cell md:p-2"><DropdownMenu><DropdownMenuTrigger render={<Button variant="ghost" size="icon-sm" aria-label={zh ? '规则操作' : 'Rule actions'}><MoreHorizontal /></Button>} /><DropdownMenuContent align="end">
              <DropdownMenuItem onClick={() => setHistory(row)}><History />{zh ? '触发历史' : 'Trigger history'}</DropdownMenuItem>
              {!row.archived_at && <>
                <DropdownMenuItem disabled={!kind || run.isPending} onClick={() => void runNow(row)}><Play />{kind?.manualViaEditor ? (zh ? '预览并运行' : 'Preview and run') : zh ? '立即检查' : 'Check now'}</DropdownMenuItem>
                <DropdownMenuItem disabled={!kind} onClick={() => setForm({ row })}><Pencil />{zh ? '编辑' : 'Edit'}</DropdownMenuItem>
                {row.schedule_type !== 'none' && <DropdownMenuItem disabled={update.isPending} onClick={() => void mutate(row, { timer_enabled: !row.timer_enabled })}>{row.timer_enabled ? (zh ? '暂停周期触发' : 'Pause timer') : (zh ? '恢复周期触发' : 'Resume timer')}</DropdownMenuItem>}
                <DropdownMenuItem disabled={update.isPending} onClick={() => void mutate(row, { enabled: !row.enabled })}>{row.enabled ? (zh ? '暂停全部自动触发' : 'Pause all automation') : (zh ? '恢复自动触发' : 'Resume automation')}</DropdownMenuItem>
                <DropdownMenuItem onClick={() => void archive(row)}>{zh ? '归档' : 'Archive'}</DropdownMenuItem>
              </>}
            </DropdownMenuContent></DropdownMenu></TableCell>
          </TableRow>
        })}
      </TableBody>
    </Table></ListShell>}
    {form && <ScheduleForm key={form.row?.id ?? 'new'} open onOpenChange={open => { if (!open) setForm(undefined) }} initial={form.row} kind={form.row?.kind} nodeID={form.row?.node_id ?? (scope === 'current' ? nodeID : undefined)} subject={form.row?.subject} manual={form.manual} />}
    {history && <TriggerHistory key={history === 'all' ? 'all' : history.id} rule={history === 'all' ? undefined : history} scope={scope} nodeID={nodeID} onClose={() => setHistory(undefined)} onTask={setTaskID} />}
    <TaskRecordDetail id={taskID} open={!!taskID} onOpenChange={open => { if (!open) setTaskID(undefined) }} />
  </div>
}
