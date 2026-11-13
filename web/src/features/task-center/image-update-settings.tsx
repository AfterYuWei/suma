import { useRef, useState } from 'react'
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
import { LoadingState } from '../../components/ui/loading-state'
import { ErrorState } from '../../components/ui/error-state'
import { confirmDockerSocketMount } from '../compose/docker-socket-confirmation'
import { cronPresets, validateCron } from './cron'
import { ImageUpdateServiceTable } from './image-update-result'
import { useCronPreview, useCreateSchedule, useImageUpdates, useStartTask, useUpdateSchedule } from './queries'
import type { DockerNode } from '../../lib/nodes'
import type { Schedule } from './types'

// ImageUpdateSettingsDialog is the per-Project entry point (Project row →
// More → Image update settings). Scheduling is owned by the Task Center: the
// dialog reads and writes one kind=image.update plan bound to the project.
interface ImageUpdateSettingsProps { node: DockerNode; project: { name: string }; open: boolean; onOpenChange: (open: boolean) => void }

export function ImageUpdateSettingsDialog(props: ImageUpdateSettingsProps) {
  const { language } = useI18n()
  const zh = language === 'zh-CN'
  const schedules = useQuery({ queryKey: ['task-schedules', 'image.update', props.node.id], queryFn: () => api<Schedule[]>(`/task-center/schedules?kind=image.update&node_id=${encodeURIComponent(props.node.id)}`), enabled: props.open, refetchInterval: 10_000 })
  const findSchedule = (rows: Schedule[] | undefined) => rows?.find((row) => row.node_id === props.node.id && row.subject === props.project.name)
  if (!schedules.data) return <Sheet open={props.open} onOpenChange={props.onOpenChange}><SheetContent><SheetTitle>{zh ? '镜像更新设置' : 'Image update settings'}</SheetTitle>{schedules.isError ? <ErrorState description={schedules.error.message} /> : <LoadingState label={zh ? '正在加载计划' : 'Loading plan'} />}</SheetContent></Sheet>
  return <ImageUpdateSettingsEditor key={`${props.node.id}:${props.project.name}`} {...props} initialSchedule={findSchedule(schedules.data)} reload={async () => {
    const response = await schedules.refetch({ throwOnError: true })
    return findSchedule(response.data)
  }} />
}

function ImageUpdateSettingsEditor({ node, project, open, onOpenChange, initialSchedule, reload }: ImageUpdateSettingsProps & { initialSchedule?: Schedule; reload: () => Promise<Schedule | undefined> }) {
  const { language } = useI18n()
  const zh = language === 'zh-CN'
  const { formatDateTime } = useDateTime()
  const client = useQueryClient()
  const updates = useImageUpdates(open ? node.id : undefined, project.name)
  const [schedule, setSchedule] = useState(initialSchedule)
  const status = updates.data?.[0]
  const create = useCreateSchedule()
  const updateSchedule = useUpdateSchedule()
  const startTask = useStartTask()
  const [cron, setCron] = useState(schedule?.cron ?? '0 4 * * *')
  const [timezone, setTimezone] = useState(schedule?.timezone ?? 'UTC')
  const [autoApply, setAutoApply] = useState(!!schedule?.params?.auto_apply)
  const [serviceNames, setServiceNames] = useState(Array.isArray(schedule?.params?.services) ? schedule.params.services.filter((value): value is string => typeof value === 'string').join(', ') : '')
  const selectedServices = serviceNames.split(',').map(value => value.trim()).filter(Boolean)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [conflict, setConflict] = useState(false)
  const socketAllowed = useRef(false)
  const cronValid = validateCron(cron)
  const preview = useCronPreview(cron, timezone, open && cronValid)
  const available = (status?.last_result?.services ?? []).filter(item => item.status === 'update_available' && (!selectedServices.length || selectedServices.includes(item.service)))
  const running = !!status?.running_task_id
  const save = async (enabled: boolean, autoApply: boolean): Promise<void> => {
    setError('')
    if (!cronValid) return setError(zh ? 'Cron 表达式无效' : 'The cron expression is invalid')
    setBusy(true)
    try {
      const params = { ...schedule?.params, project: project.name, auto_apply: autoApply, services: selectedServices, ...(socketAllowed.current ? { allow_docker_socket: true } : {}) }
      let saved: Schedule
      if (schedule) {
        saved = await updateSchedule.mutateAsync({ id: schedule.id, version: schedule.version, params, cron: cron.trim(), timezone: timezone.trim() || 'UTC', enabled })
      } else {
        saved = await create.mutateAsync({ name: zh ? `${project.name} 镜像更新` : `Image update ${project.name}`, kind: 'image.update', node_id: node.id, subject: project.name, params, cron: cron.trim(), timezone: timezone.trim() || 'UTC', enabled })
      }
      setSchedule(saved)
      setConflict(false)
      await client.invalidateQueries({ queryKey: ['task-schedules'] })
    } catch (cause) {
      if (cause instanceof ApiError && cause.message.includes('Docker socket mount requires explicit confirmation')) {
        const allowed = await confirmDockerSocketMount('/var/run/docker.sock', zh)
        if (allowed === true) { socketAllowed.current = true; return await save(enabled, autoApply) }
        return
      }
      setConflict(cause instanceof ApiError && cause.status === 409)
      setError(cause instanceof Error ? cause.message : zh ? '保存失败' : 'Unable to save the plan')
    } finally {
      setBusy(false)
    }
  }
  const toggleEnabled = async (enabled: boolean) => {
    if (!schedule) return save(enabled, autoApply)
    setBusy(true)
    setError('')
    try {
      setSchedule(await updateSchedule.mutateAsync({ id: schedule.id, version: schedule.version, enabled }))
      setConflict(false)
    } catch (cause) {
      setConflict(cause instanceof ApiError && cause.status === 409)
      setError(cause instanceof Error ? cause.message : zh ? '操作失败' : 'Unable to update')
    } finally { setBusy(false) }
  }
  const reloadPlan = async () => {
    setBusy(true)
    try {
      const next = await reload()
      setSchedule(next)
      setCron(next?.cron ?? '0 4 * * *')
      setTimezone(next?.timezone ?? 'UTC')
      setAutoApply(!!next?.params?.auto_apply)
      setServiceNames(Array.isArray(next?.params?.services) ? next.params.services.filter((value): value is string => typeof value === 'string').join(', ') : '')
      setConflict(false)
      setError('')
    } catch (cause) { setError(cause instanceof Error ? cause.message : zh ? '重新加载失败' : 'Unable to reload') }
    finally { setBusy(false) }
  }
  const checkNow = async () => {
    setError('')
    setBusy(true)
    try {
      await startTask.mutateAsync({ kind: 'image.update', nodeID: node.id, params: { project: project.name, auto_apply: false, services: selectedServices } })
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
            <Switch checked={!!schedule?.enabled} disabled={busy} onCheckedChange={(next) => void toggleEnabled(next)} />
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
              <Button variant="outline" disabled={busy || !cronValid} onClick={() => void save(schedule?.enabled ?? false, autoApply)}>{zh ? '保存' : 'Save'}</Button>
            </div>
            {cronValid && preview.data && <p className="text-xs tabular-nums text-muted-foreground">{zh ? '下次运行：' : 'Next runs: '}{preview.data.slice(0, 2).map((value) => formatDateTime(value)).join(zh ? '、' : ', ')}</p>}
            {schedule?.last_error && <p className="text-xs text-destructive">{schedule.last_error}</p>}
          </div>
          <label className="flex items-center justify-between gap-3 rounded-md bg-muted/40 px-3 py-2.5"><span><span className="block text-sm font-medium">{zh ? '检测并自动更新' : 'Detect and auto-update'}</span><span className="block text-xs text-muted-foreground">{zh ? '开启后按计划拉取新镜像并重建服务，保存后生效。' : 'Pull new images and recreate services on schedule after saving.'}</span></span><Switch checked={autoApply} disabled={busy} onCheckedChange={setAutoApply} /></label>
          <div className="space-y-1.5"><Label htmlFor="image-update-services">{zh ? '服务范围' : 'Service selection'}</Label><Input id="image-update-services" value={serviceNames} onChange={event => setServiceNames(event.target.value)} placeholder={zh ? '留空表示全部；多个服务用逗号分隔' : 'All services when empty; separate names with commas'} /><p className="text-xs text-muted-foreground">{zh ? '保存后应用于自动检查；手动检查和更新使用当前选择。' : 'Save to apply this selection to automation. Manual checks and updates use the current selection.'}</p></div>
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
          {conflict && <Button variant="outline" disabled={busy} onClick={() => void reloadPlan()}>{zh ? '重新加载计划' : 'Reload plan'}</Button>}
        </div>
      </SheetContent>
    </Sheet>
  )
}

const describeStartError = (cause: unknown, zh: boolean) => {
  if (cause instanceof ApiError && cause.status === 409) return zh ? '该项目已有正在进行的检测或更新' : 'A check or update is already running for this project'
  return cause instanceof Error ? cause.message : zh ? '启动失败' : 'Unable to start'
}
