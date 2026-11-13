import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useI18n } from '../../lib/i18n'
import { api } from '../../lib/api'
import type { DockerNode } from '../../lib/nodes'
import { Button } from '../../components/ui/button'
import { Input } from '../../components/ui/input'
import { Label } from '../../components/ui/label'
import { LoadingState } from '../../components/ui/loading-state'
import { ErrorState } from '../../components/ui/error-state'
import { Sheet, SheetContent, SheetTitle } from '../../components/ui/sheet'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../../components/ui/select'
import { useSchedule, useTaskKinds } from './queries'
import { taskKindFor } from './kinds'
import type { Schedule } from './types'

export interface ScheduleFormProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  kind?: string
  nodeID?: string
  subject?: string
  initial?: Schedule
  manual?: boolean
}

// The generic entry only chooses a registered business editor. It never
// guesses execution params or renders an arbitrary JSON form.
export function ScheduleForm({ open, onOpenChange, kind, nodeID, subject, initial, manual }: ScheduleFormProps) {
  const { language } = useI18n()
  const zh = language === 'zh-CN'
  const kinds = useTaskKinds()
  const nodes = useQuery({ queryKey: ['nodes'], queryFn: () => api<DockerNode[]>('/nodes'), enabled: open })
  const rule = useSchedule(initial?.id)
  const [pickedKind, setPickedKind] = useState(kind ?? initial?.kind ?? '')
  const [target, setTarget] = useState(nodeID ?? initial?.node_id ?? '')
  const [project, setProject] = useState(subject ?? initial?.subject ?? '')
  const [editing, setEditing] = useState(!!initial)
  const effectiveKind = pickedKind || kinds.data?.[0]?.type || ''
  const info = kinds.data?.find(entry => entry.type === effectiveKind)
  const view = taskKindFor(effectiveKind)
  const node = nodes.data?.find(entry => entry.id === target)
  const Editor = view?.Editor
  if (editing && info && Editor && !nodes.isError && !rule.isError && (!info.requires_node || node) && (!initial || rule.data)) return <Editor manual={manual} node={node} subject={project} initial={rule.data} open={open} onOpenChange={onOpenChange} businessHref={view?.businessHref?.(project)} />
  return <Sheet open={open} onOpenChange={onOpenChange}><SheetContent className="overflow-y-auto"><div className="space-y-4 p-4">
    <SheetTitle>{initial ? (zh ? '编辑自动化' : 'Edit automation') : zh ? '新建自动化' : 'New automation'}</SheetTitle>
    {kinds.isError || nodes.isError || rule.isError ? <ErrorState description={(kinds.error ?? nodes.error ?? rule.error)?.message ?? (zh ? '无法加载自动化配置' : 'Unable to load automation configuration')} /> : kinds.isPending || nodes.isPending || (initial && rule.isPending) ? <LoadingState label={zh ? '正在加载配置' : 'Loading configuration'} /> : <>
      {!kind && !initial && <div className="space-y-1.5"><Label>{zh ? '业务类型' : 'Kind'}</Label><Select value={effectiveKind} onValueChange={value => { setPickedKind(value ?? ''); setProject('') }}><SelectTrigger className="w-full"><SelectValue>{view?.label(zh)}</SelectValue></SelectTrigger><SelectContent>{kinds.data.filter(entry => taskKindFor(entry.type)).map(entry => <SelectItem key={entry.type} value={entry.type}>{taskKindFor(entry.type)?.label(zh)}</SelectItem>)}</SelectContent></Select></div>}
      {!view ? <ErrorState description={zh ? '该业务类型尚无编辑入口' : 'No editor is available for this kind'} /> : view.createHref && !initial ? <>
        <p className="text-sm text-muted-foreground">{zh ? '请在业务配置中完成仓库、凭据与交付策略；保存后自动建立规则。' : 'Configure the repository, credentials and delivery policy in business settings. Saving creates its automation rule.'}</p>
        <Button nativeButton={false} render={<a href={view.createHref} />}>{zh ? '打开业务配置' : 'Open business settings'}</Button>
      </> : <>
        {info?.requires_node && <div className="space-y-1.5"><Label>{zh ? '目标节点' : 'Target node'}</Label><Select value={target} onValueChange={value => setTarget(value ?? '')} disabled={!!initial}><SelectTrigger className="w-full"><SelectValue /></SelectTrigger><SelectContent>{nodes.data.filter(entry => entry.enabled).map(entry => <SelectItem key={entry.id} value={entry.id}>{entry.name}</SelectItem>)}</SelectContent></Select></div>}
        {view.requiresSubject && <div className="space-y-1.5"><Label>{zh ? '托管项目名称' : 'Managed project name'}</Label><Input value={project} onChange={event => setProject(event.target.value)} maxLength={128} disabled={!!initial} /></div>}
        {initial && info?.requires_node && !node && <ErrorState description={zh ? '原节点已不存在，可继续查看历史' : 'The original node no longer exists; history remains available'} />}
        <Button disabled={(info?.requires_node && !node) || (view.requiresSubject && !project.trim())} onClick={() => setEditing(true)}>{zh ? '配置自动化' : 'Configure automation'}</Button>
      </>}
    </>}
  </div></SheetContent></Sheet>
}
