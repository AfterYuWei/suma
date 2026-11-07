import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { cn } from '@/lib/utils'
import { useI18n } from '../../lib/i18n'
import { api } from '../../lib/api'
import type { DockerNode } from '../../lib/nodes'
import { Button } from '../../components/ui/button'
import { Input } from '../../components/ui/input'
import { Label } from '../../components/ui/label'
import { Sheet, SheetContent, SheetTitle } from '../../components/ui/sheet'
import { Switch } from '../../components/ui/switch'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../../components/ui/select'
import { useCreateSchedule, useCronPreview, useUpdateSchedule } from './queries'
import { cronPresets, validateCron } from './cron'
import type { Schedule } from './types'

export interface ScheduleFormProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Omit to let the operator pick from the registered kinds (generic create). */
  kind?: string
  /** Fixed node binding, e.g. when opened from a Project row. */
  nodeID?: string
  /** Fixed subject binding, e.g. the Compose project name. */
  subject?: string
  initial?: Schedule
  onSaved?: (schedule: Schedule) => void
}

// ScheduleForm edits one Task Center plan. The same form serves the generic
// schedules tab and feature dialogs (image update settings), which pin the
// kind, node and subject.
export function ScheduleForm({ open, onOpenChange, kind, nodeID, subject, initial, onSaved }: ScheduleFormProps) {
  const { language } = useI18n()
  const zh = language === 'zh-CN'
  const kinds = useQuery({ queryKey: ['task-kinds'], queryFn: () => api<{ type: string; requires_node: boolean }[]>('/task-center/kinds'), enabled: open && !kind })
  const nodes = useQuery({ queryKey: ['nodes'], queryFn: () => api<DockerNode[]>('/nodes'), enabled: open && !nodeID })
  const requiresNode = !!nodeID || !!kinds.data?.find((entry) => entry.type === effectiveKind)?.requires_node
  const [name, setName] = useState('')
  const [targetNode, setTargetNode] = useState('')
  const [projectName, setProjectName] = useState('')
  const [cron, setCron] = useState('0 4 * * *')
  const [timezone, setTimezone] = useState('UTC')
  const [autoApply, setAutoApply] = useState(false)
  const [enabled, setEnabled] = useState(true)
  const [pickedKind, setPickedKind] = useState('')
  const [error, setError] = useState('')
  const activeKind = kind || pickedKind
  const effectiveKind = activeKind || kinds.data?.[0]?.type || ''
  useEffect(() => {
    if (!open) return
    setError('')
    setName(initial?.name ?? (subject ? (zh ? `${subject} 镜像更新` : `Image update ${subject}`) : ''))
    setTargetNode(nodeID ?? initial?.node_id ?? nodes.data?.[0]?.id ?? '')
    setProjectName(subject ?? initial?.subject ?? '')
    setCron(initial?.cron ?? '0 4 * * *')
    setTimezone(initial?.timezone ?? 'UTC')
    setAutoApply(!!initial?.params?.auto_apply)
    setEnabled(initial?.enabled ?? true)
    setPickedKind(kind ?? initial?.kind ?? '')
  }, [open, initial, nodeID, subject, nodes.data, kind, zh])
  const cronValid = validateCron(cron)
  const preview = useCronPreview(cron, timezone, open && cronValid)
  const create = useCreateSchedule()
  const update = useUpdateSchedule()
  const busy = create.isPending || update.isPending
  const submit = async () => {
    setError('')
    if (!name.trim()) return setError(zh ? '请填写计划名称' : 'A plan name is required')
    if (!cronValid) return setError(zh ? 'Cron 表达式无效' : 'The cron expression is invalid')
    if (requiresNode && !targetNode) return setError(zh ? '请选择目标节点' : 'A target node is required')
    const payload = {
      name: name.trim(), kind: effectiveKind,
      node_id: requiresNode ? targetNode : undefined,
      subject: subject ?? (projectName.trim() || undefined),
      params: effectiveKind === 'image.update' ? { project: projectName.trim(), auto_apply: autoApply } : {},
      cron: cron.trim(), timezone: timezone.trim() || 'UTC', enabled,
    }
    try {
      const saved = initial
        ? await update.mutateAsync({ ...payload, id: initial.id, version: initial.version })
        : await create.mutateAsync(payload)
      onSaved?.(saved)
      onOpenChange(false)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : zh ? '保存失败' : 'Unable to save the plan')
    }
  }
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="gap-0 overflow-y-auto data-[side=right]:w-full data-[side=right]:sm:w-[560px] data-[side=right]:sm:max-w-[calc(100vw-2rem)]">
        <div className="space-y-4 p-4">
          <SheetTitle className="text-base font-medium">{initial ? (zh ? '编辑定时计划' : 'Edit schedule') : zh ? '新建定时计划' : 'New schedule'}</SheetTitle>
          <div className="space-y-1.5">
            <Label>{zh ? '名称' : 'Name'}</Label>
            <Input value={name} onChange={(event) => setName(event.target.value)} maxLength={120} />
          </div>
          {!kind && (
            <div className="space-y-1.5">
              <Label>{zh ? '任务类型' : 'Task kind'}</Label>
              <Select value={effectiveKind} onValueChange={(value) => setPickedKind(value ?? '')}>
                <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
                <SelectContent>
                  {(kinds.data ?? []).map((entry) => <SelectItem key={entry.type} value={entry.type}>{entry.type}</SelectItem>)}
                </SelectContent>
              </Select>
            </div>
          )}
          {requiresNode && (
            <div className="space-y-1.5">
              <Label>{zh ? '目标节点' : 'Target node'}</Label>
              {nodeID ? <Input value={nodes.data?.find((node) => node.id === nodeID)?.name ?? nodeID} disabled /> : (
                <Select value={targetNode} onValueChange={(value) => setTargetNode(value ?? '')}>
                  <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
                  <SelectContent>
                    {(nodes.data ?? []).filter((node) => node.enabled).map((node) => <SelectItem key={node.id} value={node.id}>{node.name}</SelectItem>)}
                  </SelectContent>
                </Select>
              )}
            </div>
          )}
          {effectiveKind === 'image.update' && !subject && (
            <div className="space-y-1.5">
              <Label>{zh ? '项目名称' : 'Project name'}</Label>
              <Input value={projectName} onChange={(event) => setProjectName(event.target.value)} placeholder="blog" maxLength={128} />
            </div>
          )}
          {effectiveKind === 'image.update' && (
            <label className="flex items-center justify-between gap-3 rounded-md bg-muted/40 px-3 py-2.5">
              <span className="space-y-0.5">
                <span className="block text-sm font-medium">{zh ? '检测并自动更新' : 'Detect and auto-update'}</span>
                <span className="block text-xs text-muted-foreground">{zh ? '关闭后仅检测并记录可用更新，更新需手动确认' : 'When off, only detect and record available updates; updates need manual confirmation'}</span>
              </span>
              <Switch checked={autoApply} onCheckedChange={setAutoApply} />
            </label>
          )}
          <div className="space-y-1.5">
            <Label>{zh ? '执行周期' : 'Schedule'}</Label>
            <div className="flex flex-wrap gap-1.5">
              {cronPresets.map((preset) => (
                <Button key={preset.value} size="sm" variant={cron === preset.value ? 'secondary' : 'outline'} onClick={() => setCron(preset.value)}>{zh ? preset.zh : preset.en}</Button>
              ))}
            </div>
            <Input value={cron} onChange={(event) => setCron(event.target.value)} className={cn(cronValid ? '' : 'border-destructive')} placeholder="0 4 * * *" />
            <p className="text-xs text-muted-foreground">{zh ? '5 段 Cron 表达式（分 时 日 月 周）' : 'Five-field cron expression (minute hour day month weekday)'}</p>
          </div>
          <div className="space-y-1.5">
            <Label>{zh ? '时区' : 'Timezone'}</Label>
            <Input value={timezone} onChange={(event) => setTimezone(event.target.value)} placeholder="UTC / Asia/Shanghai" />
          </div>
          <div className="space-y-1.5">
            <Label>{zh ? '未来 5 次运行' : 'Next 5 runs'}</Label>
            {cronValid && preview.data && (
              <ol className="space-y-1 text-xs tabular-nums text-muted-foreground">
                {preview.data.map((value) => <li key={value}>{new Date(value).toLocaleString(zh ? 'zh-CN' : 'en-US')}</li>)}
              </ol>
            )}
            {cronValid && preview.isError && <p className="text-xs text-destructive">{zh ? 'Cron 或时区无效' : 'Cron or timezone is invalid'}</p>}
          </div>
          <label className="flex items-center justify-between gap-3">
            <span className="text-sm font-medium">{zh ? '启用计划' : 'Enabled'}</span>
            <Switch checked={enabled} onCheckedChange={setEnabled} />
          </label>
          {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => onOpenChange(false)}>{zh ? '取消' : 'Cancel'}</Button>
            <Button onClick={() => void submit()} disabled={busy || !effectiveKind}>{busy ? (zh ? '保存中…' : 'Saving…') : zh ? '保存' : 'Save'}</Button>
          </div>
        </div>
      </SheetContent>
    </Sheet>
  )
}
