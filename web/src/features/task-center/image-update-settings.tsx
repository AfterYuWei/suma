import { useMemo, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowUpCircle, SearchCheck } from 'lucide-react'
import { api, ApiError } from '../../lib/api'
import { useI18n } from '../../lib/i18n'
import { confirmDialog } from '../../stores/dialog'
import { useDateTime } from '../../lib/time-zone'
import { Button } from '../../components/ui/button'
import { Input } from '../../components/ui/input'
import { Label } from '../../components/ui/label'
import { Sheet, SheetContent, SheetTitle } from '../../components/ui/sheet'
import { Switch } from '../../components/ui/switch'
import { confirmDockerSocketMount } from '../compose/docker-socket-confirmation'
import { cronPresets, validateCron } from './cron'
import { ImageUpdateServiceTable } from './image-update-result'
import { useCronPreview, useCreateSchedule, useImageUpdates, useStartTask, useUpdateSchedule } from './queries'
import type { DockerNode } from '../../lib/nodes'
import type { Schedule } from './types'

// ImageUpdateSettingsDialog is the per-Project entry point (Project row →
// More → Image update settings). Scheduling is owned by the Task Center: the
// dialog reads and writes one kind=image.update plan bound to the project.
export function ImageUpdateSettingsDialog({ node, project, open, onOpenChange }: { node: DockerNode; project: { name: string }; open: boolean; onOpenChange: (open: boolean) => void }) {
  const { language } = useI18n()
  const zh = language === 'zh-CN'
  const { formatDateTime } = useDateTime()
  const client = useQueryClient()
  const updates = useImageUpdates(open ? node.id : undefined, project.name)
  const schedules = useQuery({
    queryKey: ['task-schedules', 'image.update'],
    queryFn: () => api<Schedule[]>('/task-center/schedules?kind=image.update'),
    enabled: open,
    refetchInterval: 10_000,
  })
  const schedule = useMemo(
    () => (schedules.data ?? []).find((row) => row.node_id === node.id && row.subject === project.name),
    [schedules.data, node.id, project.name],
  )
  const status = updates.data?.[0]
  const create = useCreateSchedule()
  const updateSchedule = useUpdateSchedule()
  const startTask = useStartTask()
  const [cron, setCron] = useState(schedule?.cron ?? '0 4 * * *')
  const [timezone, setTimezone] = useState(schedule?.timezone ?? 'UTC')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const socketAllowed = useRef(false)
  const cronValid = validateCron(cron)
  const preview = useCronPreview(cron, timezone, open && cronValid)
  const available = (status?.last_result?.services ?? []).filter((item) => item.status === 'update_available')
  const running = !!status?.running_task_id
  const save = async (enabled: boolean, autoApply: boolean) => {
    setError('')
    if (!cronValid) return setError(zh ? 'Cron 表达式无效' : 'The cron expression is invalid')
    setBusy(true)
    try {
      const params = { project: project.name, auto_apply: autoApply }
      if (schedule) {
        await updateSchedule.mutateAsync({ id: schedule.id, version: schedule.version, name: schedule.name, kind: 'image.update', node_id: node.id, subject: project.name, params, cron: cron.trim(), timezone: timezone.trim() || 'UTC', enabled })
      } else {
        await create.mutateAsync({ name: zh ? `${project.name} 镜像更新` : `Image update ${project.name}`, kind: 'image.update', node_id: node.id, subject: project.name, params, cron: cron.trim(), timezone: timezone.trim() || 'UTC', enabled })
      }
      await client.invalidateQueries({ queryKey: ['task-schedules'] })
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : zh ? '保存失败' : 'Unable to save the plan')
    } finally {
      setBusy(false)
    }
  }
  const checkNow = async () => {
    setError('')
    setBusy(true)
    try {
      await startTask.mutateAsync({ kind: 'image.update', nodeID: node.id, params: { project: project.name, auto_apply: false } })
      await client.invalidateQueries({ queryKey: ['image-updates'] })
    } catch (cause) {
      setError(describeStartError(cause, zh))
    } finally {
      setBusy(false)
    }
  }
  const applyNow = async (services?: string[]) => {
    setError('')
    const targets = services ?? available.map((item) => item.service)
    const confirmed = await confirmDialog({
      title: zh ? '立即更新服务镜像' : 'Update service images now',
      description: zh
        ? `将拉取已验证的新镜像并重建 ${targets.length} 个服务，期间服务会短暂重启。`
        : `Verified new images will be pulled and ${targets.length} services recreated; they restart briefly.`,
      confirmLabel: zh ? '开始更新' : 'Update now',
    })
    if (!confirmed) return
    setBusy(true)
    try {
      await startTask.mutateAsync({
        kind: 'image.update', nodeID: node.id,
        params: { project: project.name, services: targets, auto_apply: true, ...(socketAllowed.current ? { allow_docker_socket: true } : {}) },
      })
      socketAllowed.current = false
      await client.invalidateQueries({ queryKey: ['image-updates'] })
    } catch (cause) {
      if (cause instanceof ApiError && cause.message.includes('Docker socket mount requires explicit confirmation')) {
        const allowed = await confirmDockerSocketMount('/var/run/docker.sock', zh)
        if (allowed === null) return
        socketAllowed.current = allowed
        setBusy(false)
        return void applyNow(services)
      }
      setError(describeStartError(cause, zh))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="gap-0 overflow-y-auto data-[side=right]:w-full data-[side=right]:sm:w-[720px] data-[side=right]:sm:max-w-[calc(100vw-2rem)]">
        <div className="space-y-5 p-4">
          <div className="space-y-1">
            <SheetTitle className="text-base font-medium">{zh ? '镜像更新设置' : 'Image update settings'}</SheetTitle>
            <p className="text-xs text-muted-foreground">{project.name} · {node.name} · {zh ? '由任务中心统一调度' : 'Scheduled by the Task Center'}</p>
          </div>
          <label className="flex items-center justify-between gap-3 rounded-md bg-muted/40 px-3 py-2.5">
            <span className="space-y-0.5">
              <span className="block text-sm font-medium">{zh ? '定时镜像更新' : 'Scheduled image updates'}</span>
              <span className="block text-xs text-muted-foreground">{schedule?.enabled ? (zh ? '计划已启用，按下方周期执行' : 'Plan enabled; runs on the schedule below') : zh ? '计划关闭时仍可手动检测' : 'Manual checks still work while the plan is off'}</span>
            </span>
            <Switch checked={!!schedule?.enabled} disabled={busy} onCheckedChange={(next) => void save(next, !!schedule?.params?.auto_apply)} />
          </label>
          <div className="space-y-2">
            <Label>{zh ? '执行周期' : 'Schedule'}</Label>
            <div className="flex flex-wrap gap-1.5">
              {cronPresets.map((preset) => (
                <Button key={preset.value} size="sm" variant={cron === preset.value ? 'secondary' : 'outline'} disabled={busy} onClick={() => setCron(preset.value)}>{zh ? preset.zh : preset.en}</Button>
              ))}
            </div>
            <div className="flex gap-2">
              <Input value={cron} onChange={(event) => setCron(event.target.value)} className={cronValid ? '' : 'border-destructive'} />
              <Input value={timezone} onChange={(event) => setTimezone(event.target.value)} className="w-44" placeholder="UTC / Asia/Shanghai" />
              <Button variant="outline" disabled={busy || !cronValid} onClick={() => void save(true, !!schedule?.params?.auto_apply)}>{zh ? '保存' : 'Save'}</Button>
            </div>
            {cronValid && preview.data && <p className="text-xs tabular-nums text-muted-foreground">{zh ? '下次运行：' : 'Next runs: '}{preview.data.slice(0, 2).map((value) => formatDateTime(value)).join(zh ? '、' : ', ')}</p>}
            {schedule?.last_error && <p className="text-xs text-destructive">{schedule.last_error}</p>}
          </div>
          <div className="space-y-2">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <p className="text-sm font-medium">{zh ? '最近检测结果' : 'Latest check result'}</p>
              <div className="flex items-center gap-2">
                <Button size="sm" variant="outline" disabled={busy || running} onClick={() => void checkNow()}><SearchCheck />{zh ? '立即检测' : 'Check now'}</Button>
                <Button size="sm" disabled={busy || running || available.length === 0} onClick={() => void applyNow()}><ArrowUpCircle />{zh ? `立即更新${available.length ? `（${available.length}）` : ''}` : `Update now${available.length ? ` (${available.length})` : ''}`}</Button>
              </div>
            </div>
            {running && <p className="text-xs text-amber-600 dark:text-amber-400">{zh ? '检测或更新正在进行中，结果完成后自动刷新。' : 'A check or update is running; results refresh automatically.'}</p>}
            {status?.last_result ? (
              <div className="space-y-1">
                <p className="text-xs text-muted-foreground">{zh ? '检测于' : 'Checked'} {formatDateTime(status.last_result.finished_at)}{status.last_task_id ? ` · ${status.last_task_status}` : ''}</p>
                <ImageUpdateServiceTable services={status.last_result.services} zh={zh} />
              </div>
            ) : (
              !running && <p className="text-sm text-muted-foreground">{zh ? '尚未检测，点击「立即检测」获取各服务镜像状态' : 'No results yet — run a check to see per-service image status'}</p>
            )}
          </div>
          {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
        </div>
      </SheetContent>
    </Sheet>
  )
}

const describeStartError = (cause: unknown, zh: boolean) => {
  if (cause instanceof ApiError && cause.status === 409) return zh ? '该项目已有正在进行的检测或更新' : 'A check or update is already running for this project'
  return cause instanceof Error ? cause.message : zh ? '启动失败' : 'Unable to start'
}
