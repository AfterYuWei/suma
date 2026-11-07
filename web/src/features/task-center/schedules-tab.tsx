import { useState } from 'react'
import { CalendarClock, MoreHorizontal, Pencil, Play, Plus } from 'lucide-react'
import { useI18n } from '../../lib/i18n'
import { confirmDialog } from '../../stores/dialog'
import { useDateTime } from '../../lib/time-zone'
import { Button } from '../../components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '../../components/ui/dropdown-menu'
import { ListShell } from '../../components/ui/list-shell'
import { LoadingState } from '../../components/ui/loading-state'
import { StatusBadge } from '../../components/ui/status-badge'
import { Switch } from '../../components/ui/switch'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../components/ui/table'
import { taskKindFor } from './kinds'
import { describeCron } from './cron'
import { useDeleteSchedule, useRunSchedule, useSchedules, useUpdateSchedule } from './queries'
import { ScheduleForm } from './schedule-form'
import type { Schedule } from './types'

const statusTone = (status?: string) => status === 'success' ? 'success' : status === 'failed' ? 'critical' : status === 'running' || status === 'pending' ? 'warning' : 'neutral'

// SchedulesTab is the unified plan management surface: every Task Center plan
// appears here regardless of which feature created it.
export function SchedulesTab() {
  const { language } = useI18n()
  const zh = language === 'zh-CN'
  const { formatDateTime } = useDateTime()
  const query = useSchedules()
  const update = useUpdateSchedule()
  const remove = useDeleteSchedule()
  const run = useRunSchedule()
  const [formOpen, setFormOpen] = useState(false)
  const [editing, setEditing] = useState<Schedule | undefined>()
  const [actionError, setActionError] = useState('')
  const toggle = async (row: Schedule) => {
    setActionError('')
    try {
      await update.mutateAsync({ id: row.id, version: row.version, name: row.name, kind: row.kind, node_id: row.node_id || undefined, subject: row.subject || undefined, params: (row.params ?? {}) as Record<string, unknown>, cron: row.cron, timezone: row.timezone, enabled: !row.enabled })
    } catch (cause) {
      setActionError(cause instanceof Error ? cause.message : zh ? '操作失败' : 'Unable to update the plan')
    }
  }
  const runNow = async (row: Schedule) => {
    setActionError('')
    try {
      await run.mutateAsync(row.id)
    } catch (cause) {
      setActionError(cause instanceof Error && cause.message.includes('still active') ? (zh ? '上一轮运行尚未结束' : 'The previous run is still active') : cause instanceof Error ? cause.message : zh ? '启动失败' : 'Unable to start')
    }
  }
  const drop = async (row: Schedule) => {
    if (!await confirmDialog({ title: zh ? '删除定时计划' : 'Delete schedule', description: zh ? `确定删除「${row.name}」？该计划的运行历史将一并删除。` : `Delete "${row.name}"? Its run history is removed with it.`, confirmLabel: zh ? '删除' : 'Delete' })) return
    setActionError('')
    try {
      await remove.mutateAsync(row.id)
    } catch (cause) {
      setActionError(cause instanceof Error ? cause.message : zh ? '删除失败' : 'Unable to delete')
    }
  }
  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between gap-3">
        <p className="text-sm text-muted-foreground">{zh ? '所有定时计划由任务中心统一调度；项目镜像更新计划可在项目「更多」菜单中配置。' : 'All plans are scheduled by the Task Center; project image update plans are configured from the project More menu.'}</p>
        <Button size="sm" onClick={() => { setEditing(undefined); setFormOpen(true) }}><Plus />{zh ? '新建定时计划' : 'New schedule'}</Button>
      </div>
      {actionError && <p role="alert" className="text-sm text-destructive">{actionError}</p>}
      {query.isPending ? <LoadingState label={zh ? '正在加载定时计划' : 'Loading schedules'} /> : (
        <ListShell>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{zh ? '计划' : 'Plan'}</TableHead>
                <TableHead className="w-28">{zh ? '类型' : 'Kind'}</TableHead>
                <TableHead>{zh ? '目标' : 'Target'}</TableHead>
                <TableHead className="w-44">{zh ? '周期 / 下次运行' : 'Schedule / next run'}</TableHead>
                <TableHead className="w-44">{zh ? '最近一次' : 'Last run'}</TableHead>
                <TableHead className="w-20">{zh ? '启用' : 'Enabled'}</TableHead>
                <TableHead className="w-12" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {(query.data ?? []).length === 0 && (
                <TableRow><TableCell colSpan={7} className="h-24 text-center text-muted-foreground">{zh ? '暂无定时计划' : 'No schedules'}</TableCell></TableRow>
              )}
              {(query.data ?? []).map((row) => {
                const kind = taskKindFor(row.kind)
                const Icon = kind?.icon ?? CalendarClock
                return (
                  <TableRow key={row.id}>
                    <TableCell className="max-w-64">
                      <div className="flex items-center gap-2 font-medium"><Icon className="size-4 text-muted-foreground" /><span className="truncate">{row.name}</span></div>
                      {(row.summary || row.last_error) && <div className={`truncate text-xs ${row.last_error ? 'text-destructive' : 'text-muted-foreground'}`}>{row.last_error || row.summary}</div>}
                    </TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground">{kind ? kind.label(zh) : row.kind}</TableCell>
                    <TableCell className="text-sm">
                      <div>{row.node_name || row.node_id || (zh ? '控制平面' : 'Control plane')}</div>
                      {row.subject && <div className="truncate text-xs text-muted-foreground">{row.subject}</div>}
                    </TableCell>
                    <TableCell className="text-sm">
                      <div className="font-mono text-xs">{describeCron(row.cron, zh)} · {row.timezone}</div>
                      <div className="text-xs text-muted-foreground">{row.enabled && row.next_run_at ? `${zh ? '下次' : 'next'} ${formatDateTime(row.next_run_at)}` : zh ? '已停用' : 'disabled'}</div>
                    </TableCell>
                    <TableCell>
                      {row.last_task_id ? (
                        <div className="space-y-0.5">
                          <StatusBadge tone={statusTone(row.last_task_status)}>{row.last_task_status}</StatusBadge>
                          {row.last_task_finished_at && <div className="text-xs text-muted-foreground">{formatDateTime(row.last_task_finished_at)}</div>}
                        </div>
                      ) : <span className="text-xs text-muted-foreground">—</span>}
                    </TableCell>
                    <TableCell><Switch checked={row.enabled} onCheckedChange={() => void toggle(row)} /></TableCell>
                    <TableCell>
                      <DropdownMenu>
                        <DropdownMenuTrigger render={<Button variant="ghost" size="icon-sm" aria-label={zh ? '计划操作' : 'Plan actions'}><MoreHorizontal /></Button>} />
                        <DropdownMenuContent align="end">
                          <DropdownMenuItem onClick={() => void runNow(row)}><Play />{zh ? '立即运行' : 'Run now'}</DropdownMenuItem>
                          <DropdownMenuItem onClick={() => { setEditing(row); setFormOpen(true) }}><Pencil />{zh ? '编辑' : 'Edit'}</DropdownMenuItem>
                          <DropdownMenuItem variant="destructive" onClick={() => void drop(row)}>{zh ? '删除' : 'Delete'}</DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </ListShell>
      )}
      <ScheduleForm open={formOpen} onOpenChange={setFormOpen} kind={editing?.kind} nodeID={editing?.node_id} subject={editing?.subject || undefined} initial={editing} />
    </div>
  )
}
