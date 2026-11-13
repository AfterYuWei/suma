import { useState } from 'react'
import { ApiError } from '../../lib/api'
import { useUIStore } from '../../stores/ui'
import { useI18n } from '../../lib/i18n'
import { Button } from '../../components/ui/button'
import { Input } from '../../components/ui/input'
import { Label } from '../../components/ui/label'
import { Switch } from '../../components/ui/switch'
import { Sheet, SheetContent, SheetTitle } from '../../components/ui/sheet'
import { PollIntervalInput } from '../delivery/poll-interval-input'
import { useSchedule, useUpdateSchedule } from './queries'
import type { KindEditorProps } from './kinds'

// The center edits scheduling only. Repository, credentials and publication
// policy stay in their existing business editor.
export function CDScheduleEditor({ initial, open, onOpenChange, businessHref, node }: KindEditorProps) {
  const { language } = useI18n()
  const zh = language === 'zh-CN'
  const [saved, setSaved] = useState(initial)
  const [name, setName] = useState(initial?.name ?? '')
  const [interval, setInterval] = useState(initial?.interval_seconds ?? 300)
  const [enabled, setEnabled] = useState(initial?.enabled ?? false)
  const [timer, setTimer] = useState(initial?.timer_enabled ?? true)
  const [error, setError] = useState('')
  const [conflict, setConflict] = useState(false)
  const query = useSchedule(initial?.id)
  const update = useUpdateSchedule()
  const setCurrentNodeID = useUIStore(state => state.setCurrentNodeID)
  const submit = async () => {
    if (!saved || !name.trim() || !Number.isInteger(interval) || interval < 30 || interval > 86400) return setError(zh ? '请填写名称和有效的轮询间隔' : 'Enter a name and a valid polling interval')
    setError('')
    try {
      await update.mutateAsync({ id: saved.id, version: saved.version, name: name.trim(), interval_seconds: interval, enabled, timer_enabled: timer })
      onOpenChange(false)
    } catch (cause) {
      setConflict(cause instanceof ApiError && cause.status === 409)
      setError(cause instanceof Error ? cause.message : zh ? '保存失败' : 'Unable to save')
    }
  }
  const reload = async () => {
    try {
      const response = await query.refetch({ throwOnError: true })
      if (!response.data) return
      setSaved(response.data); setName(response.data.name); setInterval(response.data.interval_seconds); setEnabled(response.data.enabled); setTimer(response.data.timer_enabled); setConflict(false); setError('')
    } catch (cause) { setError(cause instanceof Error ? cause.message : zh ? '重新加载失败' : 'Unable to reload') }
  }
  return <Sheet open={open} onOpenChange={onOpenChange}><SheetContent className="overflow-y-auto"><div className="space-y-4 p-4">
    <SheetTitle>{zh ? 'CD 自动化' : 'CD automation'}</SheetTitle>
    <div className="space-y-1.5"><Label>{zh ? '名称' : 'Name'}</Label><Input value={name} onChange={event => setName(event.target.value)} maxLength={120} /></div>
    <PollIntervalInput value={interval} onChange={setInterval} zh={zh} />
    <label className="flex items-center justify-between gap-3 text-sm">{zh ? '允许自动触发' : 'Allow automatic triggers'}<Switch checked={enabled} onCheckedChange={setEnabled} /></label>
    <label className="flex items-center justify-between gap-3 text-sm">{zh ? '周期触发' : 'Timer triggers'}<Switch checked={timer} onCheckedChange={setTimer} /></label>
    <p className="text-xs text-muted-foreground">{zh ? `Webhook：${saved?.webhook_enabled ? '已配置' : '未启用'}。暂停周期不会关闭 Webhook；暂停全部自动触发会同时阻止两者。` : `Webhook: ${saved?.webhook_enabled ? 'configured' : 'disabled'}. Pausing the timer keeps Webhook triggers; pausing all automation blocks both.`}</p>
    {businessHref && <Button variant="outline" nativeButton={false} render={<a href={businessHref} onClick={async event => {
      if (!node) return
      event.preventDefault()
      if (await setCurrentNodeID(node.id)) window.location.assign(businessHref)
    }} />}>{zh ? '仓库与交付策略' : 'Repository and delivery policy'}</Button>}
    {error && <p role="alert" className="break-words text-sm text-destructive">{error}</p>}
    {conflict && <Button variant="outline" onClick={() => void reload()}>{zh ? '重新加载配置' : 'Reload configuration'}</Button>}
    <div className="flex justify-end gap-2"><Button variant="outline" onClick={() => onOpenChange(false)}>{zh ? '取消' : 'Cancel'}</Button><Button disabled={update.isPending || !saved} onClick={() => void submit()}>{zh ? '保存' : 'Save'}</Button></div>
  </div></SheetContent></Sheet>
}
