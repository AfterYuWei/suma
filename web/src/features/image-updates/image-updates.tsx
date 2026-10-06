import { useDateTime } from '../../lib/time-zone'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useEffect, useState } from 'react'
import { RefreshCw } from 'lucide-react'
import { Button } from '../../components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '../../components/ui/dialog'
import { Label } from '../../components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../../components/ui/select'
import { StatusBadge } from '../../components/ui/status-badge'
import { TooltipHint } from '../../components/ui/tooltip-hint'
import { Spinner } from '../../components/ui/spinner'
import { api, ApiError } from '../../lib/api'
import { nodePath } from '../../lib/nodes'
import { useI18n } from '../../lib/i18n'
import { type CheckTarget, type ImageUpdatePolicy, type ImageUpdateResult, type RegistryCredential, registryHost, reasonLabel, updateLabel } from './types'
import { useImageUpdates } from './hooks'
interface Task { id: string; status: string; progress: number; message: string }
export function UpdateStatus({ rows, zh }: { rows: ImageUpdateResult[]; zh: boolean }) {
 if (!rows.length) return <span className="text-xs text-muted-foreground">{zh ? '未检测' : 'Not checked'}</span>
 const row = rows.find(r => r.stale) || rows.find(r => r.recreate_required || r.status === 'update_available') || rows.find(r => r.status === 'checking' || r.status === 'unavailable') || rows[0]
 return <TooltipHint content={reasonLabel(row.reason_code, zh) || rows.map(r => r.reference).join(', ')}><span><StatusBadge tone={row.stale || row.recreate_required || row.status === 'update_available' ? 'warning' : row.status === 'current' ? 'success' : 'neutral'}>{updateLabel(row, zh)}</StatusBadge></span></TooltipHint>
}
export function RegistrySelections({ nodeID, hosts, values, onChange }: { nodeID: string; hosts: string[]; values: Record<string, number>; onChange: (next: Record<string, number>) => void }) {
 const { language } = useI18n(); const zh = language === 'zh-CN'
 const query = useQuery({ queryKey: ['registry-credentials'], queryFn: () => api<RegistryCredential[]>('/credentials/registries') })
 return <div className="flex flex-col gap-3">{hosts.map(host => {
  const options = [{ value: '', label: zh ? '匿名访问' : 'Anonymous' }, ...(query.data || []).filter(r => r.authorized_node_ids?.includes(nodeID) && registryHost(r.server_address) === host).map(r => ({ value: String(r.id), label: r.name }))]
  return <div key={host} className="grid gap-1.5"><Label>{host}</Label><Select items={options} value={values[host] ? String(values[host]) : ''} onValueChange={value => { const next = { ...values }; if (value) next[host] = Number(value); else delete next[host]; onChange(next) }}><SelectTrigger className="w-full" aria-label={`${host} ${zh ? '仓库凭据' : 'credential'}`}><SelectValue /></SelectTrigger><SelectContent align="start" alignItemWithTrigger={false}>{options.map(option => <SelectItem key={option.value} value={option.value}>{option.label}</SelectItem>)}</SelectContent></Select></div>
 })}{query.isError && <p className="text-xs text-destructive">{query.error.message}</p>}</div>
}
export function ImageUpdateCheck({ nodeID, target = {}, compact = false }: { nodeID: string; target?: CheckTarget; compact?: boolean }) {
 const { language } = useI18n(); const zh = language === 'zh-CN'; const client = useQueryClient()
 const results = useImageUpdates(nodeID, target.project_name, !compact)
 const policy = useQuery({ queryKey: ['image-update-policy', nodeID], queryFn: () => api<ImageUpdatePolicy>(nodePath(nodeID, '/image-updates/policy')), enabled: !!nodeID })
 const [open, setOpen] = useState(false); const [mappings, setMappings] = useState<Record<string, number>>({}); const [taskID, setTaskID] = useState('')
 const check = useMutation({ mutationFn: () => api<Task>(nodePath(nodeID, '/image-updates/check'), { method: 'POST', body: JSON.stringify({ ...target, registry_credentials: mappings }) }), onSuccess: task => { setTaskID(task.id); void client.invalidateQueries({ queryKey: ['image-updates', nodeID] }); void client.invalidateQueries({ queryKey: ['tasks'] }) }, onError: error => { if (error instanceof ApiError && error.status === 409) { const data = error.data as { task_id?: string } | undefined; if (data?.task_id) setTaskID(data.task_id); void results.refetch() } } })
 const runningID = taskID || results.data?.running_task_id || ''
 const task = useQuery({ queryKey: ['image-check-task', nodeID, runningID], queryFn: () => api<Task>(nodePath(nodeID, `/tasks/${encodeURIComponent(runningID)}`)), enabled: !!runningID, refetchInterval: query => query.state.data && ['success', 'failed', 'canceled'].includes(query.state.data.status) ? false : 1000 })
 const cancel = useMutation({ mutationFn: () => api(nodePath(nodeID, `/tasks/${encodeURIComponent(runningID)}/cancel`), { method: 'POST' }), onSuccess: () => { void task.refetch() } })
 const taskStatus = task.data?.status
 useEffect(() => { if (taskStatus && ['success', 'failed', 'canceled'].includes(taskStatus)) void client.invalidateQueries({ queryKey: ['image-updates', nodeID] }) }, [taskStatus, client, nodeID])
 const resetCheck = check.reset
 useEffect(() => { setOpen(false); setTaskID(''); resetCheck() }, [nodeID, resetCheck])
 const active = task.data && ['pending', 'running'].includes(task.data.status)
 const hosts = [...new Set((results.data?.results || []).filter(r => !target.image_ids || target.image_ids.includes(r.local_image_id)).map(r => r.registry).filter((r): r is string => !!r))].sort()
 return <><Button size={compact ? 'icon-sm' : 'sm'} variant="outline" aria-label={zh ? '检查镜像更新' : 'Check image updates'} onClick={() => { setMappings(policy.data?.registry_credentials || {}); if (!active) { setTaskID(''); check.reset() }; setOpen(true) }}><RefreshCw />{!compact && (zh ? '检查镜像更新' : 'Check image updates')}</Button>
 <Dialog open={open} onOpenChange={setOpen}><DialogContent className="sm:max-w-lg"><DialogHeader><DialogTitle>{zh ? '检查镜像更新' : 'Check image updates'}</DialogTitle><DialogDescription>{zh ? '仅查询当前平台的仓库元数据，不拉取镜像或重建容器。凭据必须授权给当前节点。' : 'Checks registry metadata for the current platform. Credentials must be authorized for this node.'}</DialogDescription></DialogHeader>
 {runningID ? <div className="flex flex-col gap-3"><p className="text-sm">{task.data?.message || (zh ? '正在检测…' : 'Checking…')} {task.data?.progress ?? 0}%</p>{task.isError && <p className="text-sm text-destructive">{task.error.message}</p>}<Link to="/tasks" className="text-sm underline">{zh ? '查看任务' : 'View tasks'}</Link>{active && <Button variant="outline" disabled={cancel.isPending} onClick={() => cancel.mutate()}>{zh ? '取消检测' : 'Cancel check'}</Button>}{cancel.isError && <p className="text-sm text-destructive">{cancel.error.message}</p>}</div> : <><RegistrySelections nodeID={nodeID} hosts={hosts} values={mappings} onChange={setMappings} />{results.isError && <p className="text-sm text-destructive">{results.error.message}</p>}{check.isError && <p className="text-sm text-destructive">{check.error.message}</p>}</>}
 <DialogFooter><Button variant="outline" onClick={() => setOpen(false)}>{active ? (zh ? '后台继续' : 'Continue in background') : (zh ? '关闭' : 'Close')}</Button>{!runningID && <Button disabled={check.isPending || results.isPending || results.isError} onClick={() => check.mutate()}>{check.isPending ? <Spinner /> : <RefreshCw />}{zh ? '开始检测' : 'Start check'}</Button>}</DialogFooter></DialogContent></Dialog></>
}
export function ImageUpdateDetails({ rows, zh, onPull, showUsage = true }: { rows: ImageUpdateResult[]; zh: boolean; onPull?: (row: ImageUpdateResult) => void; showUsage?: boolean }) {
  const { formatDateTime } = useDateTime()

 return <div className="flex flex-col divide-y divide-border">{rows.map(row => <div key={`${row.reference}/${row.local_image_id}`} className="flex flex-col gap-2 py-3"><div className="flex flex-wrap items-center justify-between gap-2"><span className="break-all font-mono text-xs">{row.reference || (zh ? '无标签镜像' : 'Untagged image')}</span><UpdateStatus rows={[row]} zh={zh} /></div><p className="text-xs text-muted-foreground">{row.platform.os}/{row.platform.architecture}{row.platform.variant ? `/${row.platform.variant}` : ''} · {row.checked_at ? formatDateTime(row.checked_at) : (zh ? '尚未检测' : 'Not checked')}</p>{row.reason_code && <p className="text-xs text-muted-foreground">{reasonLabel(row.reason_code, zh)}</p>}{row.remote_manifest_digest && <p className="break-all font-mono text-xs text-muted-foreground">{row.remote_manifest_digest}</p>}{showUsage && row.containers.map(container => <div key={container.container_id} className="flex flex-wrap items-center gap-2 text-xs"><Link to="/containers/$containerId" params={{ containerId: container.container_id }} className="underline">{container.container_name}</Link><span className="text-muted-foreground">{container.service}</span>{container.delivery_project ? <Link to="/continuous-delivery/$projectName" params={{ projectName: container.delivery_project }} className="underline">{zh ? '交付项目' : 'Delivery'}: {container.delivery_project}</Link> : container.project ? <Link to="/projects/$backend/$projectName" params={{ backend: 'compose', projectName: container.project }} className="underline">{container.project}</Link> : null}</div>)}{row.pull_required && onPull && <Button size="sm" variant="outline" className="self-start" onClick={() => onPull(row)}>{zh ? '拉取此镜像' : 'Pull this image'}</Button>}</div>)}{!rows.length && <p className="py-3 text-xs text-muted-foreground">{zh ? '没有可检测的已部署镜像；未部署服务尚无运行版本。' : 'No deployed images to check; undeployed services have no runtime version.'}</p>}</div>
}
