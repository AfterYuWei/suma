import { candidateStatus, generateCron } from './project-helpers'
import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Button } from '../../components/ui/button'
import { Input } from '../../components/ui/input'
import { Label } from '../../components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../../components/ui/select'
import { ImageUpdateCheck, ImageUpdateDetails } from './image-updates'
import { useImageUpdates } from './hooks'
import { api } from '../../lib/api'
import { nodePath } from '../../lib/nodes'
import { useDateTime } from '../../lib/time-zone'
import { useI18n } from '../../lib/i18n'
import { confirmDialog } from '../../stores/dialog'
import type { ProjectImagePolicy } from './types'

export function ProjectImageUpdates({ nodeID, projectName, details = true }: { nodeID: string; projectName: string; details?: boolean }) {
 const { language } = useI18n(), zh = language === 'zh-CN', client = useQueryClient()
 const { timeZone, formatDateTime } = useDateTime()
 const view = useImageUpdates(nodeID, projectName)
 const path = nodePath(nodeID, `/image-updates/projects/${encodeURIComponent(projectName)}`)
 const policy = useQuery({ queryKey: ['project-image-policy', nodeID, projectName], queryFn: () => api<ProjectImagePolicy>(`${path}/policy`) })
 const [draft, setDraft] = useState<ProjectImagePolicy | null>(null)
 useEffect(() => { if (policy.data) setDraft(policy.data.version ? policy.data : { ...policy.data, timezone: timeZone }) }, [policy.data, timeZone])
 const save = useMutation({ mutationFn: () => api<ProjectImagePolicy>(`${path}/policy`, { method: 'PUT', body: JSON.stringify({ expected_version: draft?.version, mode: draft?.mode, cron: draft?.cron, timezone: draft?.timezone }) }), onSuccess: row => { setDraft(row); client.setQueryData(['project-image-policy', nodeID, projectName], row) } })
 const candidate = view.data?.candidate
 const publish = useMutation({ mutationFn: async () => {
  if (!candidate) throw new Error('No candidate')
  const description = candidate.services.map(row => `${row.name} · ${row.reference}\n${row.manifest_digest}`).join('\n\n') + (candidate.requires_docker_socket ? (zh ? '\n\n项目挂载 Docker Socket，将授予完整 Engine 控制权限。' : '\n\nThis project mounts the Docker socket and grants full Engine control.') : '')
  if (!await confirmDialog({ title: zh ? `发布 ${projectName}？` : `Publish ${projectName}?`, description, confirmLabel: zh ? '发布' : 'Publish' })) return
  return api<{ id: string }>(`${path}/candidates/${candidate.id}/publish`, { method: 'POST', body: JSON.stringify({ allow_docker_socket: candidate.requires_docker_socket }) })
 }, onSuccess: () => { void client.invalidateQueries({ queryKey: ['image-updates', nodeID] }); void client.invalidateQueries({ queryKey: ['project-image-summaries', nodeID] }) } })
 return <section className="flex min-w-0 flex-col gap-4">
  <div className="flex flex-wrap items-center justify-between gap-2"><h3 className="text-sm font-medium">{zh ? '镜像更新' : 'Image updates'}</h3><ImageUpdateCheck nodeID={nodeID} target={{ project_name: projectName }} /></div>
  <p className="text-xs text-muted-foreground">{zh ? '项目检测独立生成更新通知。发布前重新核对版本，仅更新运行中的变化服务，停止及暂停的服务保持原状。' : 'Project checks generate update notifications independently. Publishing verifies the version and updates only changed running services; stopped and paused services stay unchanged.'}</p>
  <details className="rounded-md bg-surface p-3"><summary className="cursor-pointer text-sm">{zh ? '定时检测' : 'Scheduled checks'}</summary>{draft && <form className="mt-3 flex flex-col gap-3" onSubmit={event => { event.preventDefault(); save.mutate() }}>
   <Label>{zh ? '检测策略' : 'Check policy'}</Label><Select<ProjectImagePolicy['mode']> items={[{ value: 'inherit', label: zh ? '继承节点周期' : 'Inherit node interval' }, { value: 'custom', label: zh ? '自定义 Cron' : 'Custom Cron' }, { value: 'disabled', label: zh ? '关闭' : 'Disabled' }]} value={draft.mode} onValueChange={value => { if (value) setDraft({ ...draft, mode: value }) }}><SelectTrigger aria-label={zh ? '项目检测策略' : 'Project check policy'}><SelectValue /></SelectTrigger><SelectContent align="start" alignItemWithTrigger={false}>{[['inherit', zh ? '继承节点周期' : 'Inherit node interval'], ['custom', zh ? '自定义 Cron' : 'Custom Cron'], ['disabled', zh ? '关闭' : 'Disabled']].map(([value, label]) => <SelectItem key={value} value={value}>{label}</SelectItem>)}</SelectContent></Select>
   {draft.mode === 'custom' && <CronEditor nodeID={nodeID} cron={draft.cron} timezone={draft.timezone} onChange={(cron, timezone) => setDraft({ ...draft, cron, timezone })} />}
   {draft.mode === 'inherit' && <p className="text-xs text-muted-foreground">{zh ? '仅继承节点的启停与周期。项目结果和通知不受镜像页检测结果影响。' : 'Inherits only the node switch and interval. Image-page results do not change project results or notifications.'}</p>}
   {draft.next_run_at && <p className="text-xs text-muted-foreground">{zh ? '下次检测：' : 'Next check: '}{formatDateTime(draft.next_run_at)}</p>}
   <Button className="self-start" type="submit" size="sm" disabled={save.isPending}>{zh ? '保存策略' : 'Save policy'}</Button>{save.isError && <p role="alert" className="text-sm text-destructive">{save.error.message}</p>}
  </form>}{policy.isError && <p role="alert" className="mt-2 text-sm text-destructive">{policy.error.message}</p>}</details>
  {candidate && <div className="flex min-w-0 flex-col gap-2 rounded-md bg-surface p-3"><div className="flex flex-wrap items-center justify-between gap-2"><span className="text-sm">{candidateStatus(candidate.status, zh)}</span>{candidate.status === 'pending' && <Button size="sm" disabled={publish.isPending} onClick={() => publish.mutate()}>{zh ? '发布' : 'Publish'}</Button>}</div>{candidate.services.map(row => <div className="min-w-0 text-xs" key={row.name}><p>{row.name} · {row.replicas} {zh ? '运行副本' : 'running replicas'}</p><p className="break-all font-mono text-muted-foreground">{row.reference} → {row.manifest_digest}</p></div>)}{candidate.message && <p className="break-words text-xs text-muted-foreground">{candidate.message}</p>}{candidate.task_id && <Link to="/tasks" className="text-xs underline">{zh ? '查看发布任务' : 'View publication task'}</Link>}</div>}
  {publish.isError && <p role="alert" className="text-sm text-destructive">{publish.error.message}</p>}{view.isError && <p role="alert" className="text-sm text-destructive">{view.error.message}</p>}
  {details && <ImageUpdateDetails rows={view.data?.results || []} zh={zh} />}
 </section>
}

function CronEditor({ nodeID, cron, timezone, onChange }: { nodeID: string; cron: string; timezone: string; onChange: (cron: string, timezone: string) => void }) {
 const { language } = useI18n(), zh = language === 'zh-CN', { formatDateTime } = useDateTime()
 const [kind, setKind] = useState('hours'), [step, setStep] = useState('6'), [clock, setClock] = useState('03:00'), [weekday, setWeekday] = useState('1')
 const [previewInput, setPreviewInput] = useState({ cron, timezone })
 useEffect(() => { const timer = setTimeout(() => setPreviewInput({ cron, timezone }), 350); return () => clearTimeout(timer) }, [cron, timezone])
 const preview = useQuery({ queryKey: ['image-cron-preview', nodeID, previewInput], queryFn: () => api<string[]>(nodePath(nodeID, '/image-updates/schedule/preview'), { method: 'POST', body: JSON.stringify(previewInput) }), retry: false })
 const options = [{ value: 'minutes', label: zh ? '按分钟' : 'Minutes' }, { value: 'hours', label: zh ? '按小时' : 'Hours' }, { value: 'daily', label: zh ? '每天' : 'Daily' }, { value: 'weekly', label: zh ? '每周' : 'Weekly' }]
 return <><div className="flex flex-wrap items-end gap-2"><div className="min-w-32 flex-1"><Label>{zh ? '表达式生成器' : 'Expression generator'}</Label><Select items={options} value={kind} onValueChange={value => { if (value) { setKind(value); setStep(value === 'minutes' ? '15' : '6') } }}><SelectTrigger aria-label={zh ? '生成方式' : 'Generator mode'}><SelectValue /></SelectTrigger><SelectContent align="start" alignItemWithTrigger={false}>{options.map(option => <SelectItem key={option.value} value={option.value}>{option.label}</SelectItem>)}</SelectContent></Select></div>{['minutes', 'hours'].includes(kind) ? <div className="w-24"><Label>{zh ? '步进' : 'Step'}</Label><Input aria-label={zh ? '步进' : 'Step'} type="number" min={1} max={kind === 'minutes' ? 59 : 23} value={step} onChange={event => setStep(event.target.value)} /></div> : <><Input className="w-32" aria-label={zh ? '检测时刻' : 'Check time'} type="time" value={clock} onChange={event => setClock(event.target.value)} />{kind === 'weekly' && <Input className="w-24" aria-label={zh ? '星期 0–6' : 'Weekday 0–6'} type="number" min={0} max={6} value={weekday} onChange={event => setWeekday(event.target.value)} />}</>}<Button type="button" variant="outline" size="sm" onClick={() => onChange(generateCron(kind, step, clock, weekday), timezone)}>{zh ? '生成表达式' : 'Generate'}</Button></div>
 <Label htmlFor="project-cron">Cron · {zh ? '分 时 日 月 周' : 'minute hour day month weekday'}</Label><Input id="project-cron" value={cron} onChange={event => onChange(event.target.value, timezone)} placeholder="0 */6 * * *" className="font-mono" />
 <Label htmlFor="project-cron-timezone">{zh ? 'IANA 时区' : 'IANA timezone'}</Label><Input id="project-cron-timezone" value={timezone} onChange={event => onChange(cron, event.target.value)} />
 {preview.data && <div className="text-xs text-muted-foreground"><p>{zh ? '未来五次（所选调度时区）' : 'Next five (schedule timezone)'}</p>{preview.data.map(time => <p key={time}>{formatDateTime(time, { timeZone: previewInput.timezone })}</p>)}</div>}{preview.isError && <p role="alert" className="text-xs text-destructive">{preview.error.message}</p>}</>
}
