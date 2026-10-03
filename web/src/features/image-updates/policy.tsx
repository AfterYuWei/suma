import { useDateTime } from '../../lib/time-zone'
import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Save } from 'lucide-react'
import { Button } from '../../components/ui/button'
import { Label } from '../../components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../../components/ui/select'
import { Switch } from '../../components/ui/switch'
import { LoadingState } from '../../components/ui/loading-state'
import { ErrorState } from '../../components/ui/error-state'
import { api, ApiError } from '../../lib/api'
import { nodePath } from '../../lib/nodes'
import { useI18n } from '../../lib/i18n'
import { ImageUpdateCheck, RegistrySelections } from './image-updates'
import { type ImageUpdatePolicy, type RegistryCredential, registryHost } from './types'
export function ImageUpdatePolicyPanel({ nodeID }: { nodeID: string }) {
 const { language } = useI18n(); const zh = language === 'zh-CN'
 return <section className="flex flex-col gap-5"><div><h3 className="text-sm font-medium">{zh ? '镜像更新检测' : 'Image update detection'}</h3><p className="mt-1 text-sm text-muted-foreground">{zh ? '管理当前节点的检测策略。定时检测默认关闭，只查询镜像元数据。发现更新后，由你确认拉取和部署。仓库需能从 SUMA 控制端通过 HTTPS 访问。' : 'Manage the current node’s check policy. Scheduled checks are off by default. Checks query metadata; pulling and deployment remain manual. Registries must be reachable over HTTPS from SUMA.'}</p></div>{nodeID && <PolicyEditor key={nodeID} nodeID={nodeID} zh={zh} />}</section>
}
function PolicyEditor({ nodeID, zh }: { nodeID: string; zh: boolean }) {
  const { formatDateTime } = useDateTime()

 const client = useQueryClient()
 const query = useQuery({ queryKey: ['image-update-policy', nodeID], queryFn: () => api<ImageUpdatePolicy>(nodePath(nodeID, '/image-updates/policy')) })
 const credentials = useQuery({ queryKey: ['registry-credentials'], queryFn: () => api<RegistryCredential[]>('/credentials/registries') })
 const [draft, setDraft] = useState<ImageUpdatePolicy | null>(null)
 useEffect(() => { if (query.data && !draft) setDraft(query.data) }, [query.data, draft])
 const save = useMutation({ mutationFn: () => api<ImageUpdatePolicy>(nodePath(nodeID, '/image-updates/policy'), { method: 'PUT', body: JSON.stringify({ expected_version: draft?.version, enabled: draft?.enabled, interval_hours: draft?.interval_hours, registry_credentials: draft?.registry_credentials }) }), onSuccess: row => { setDraft(row); client.setQueryData(['image-update-policy', nodeID], row); void client.invalidateQueries({ queryKey: ['image-updates', nodeID] }) } })
 if (query.isPending) return <LoadingState rows={3} label={zh ? '加载策略' : 'Loading policy'} />
 if (query.isError) return <ErrorState description={query.error.message} />
 if (!draft) return null
 const hosts = [...new Set([...Object.keys(draft.registry_credentials), ...(credentials.data || []).filter(c => c.authorized_node_ids.includes(nodeID)).map(c => registryHost(c.server_address))])].sort()
 const options = [1, 6, 24].map(hours => ({ value: String(hours), label: zh ? `每 ${hours} 小时` : `Every ${hours} hours` }))
 return <form className="flex max-w-xl flex-col gap-4" onSubmit={event => { event.preventDefault(); save.mutate() }}><div className="flex items-center justify-between gap-4"><Label htmlFor="image-update-enabled">{zh ? '启用定时检测' : 'Enable scheduled checks'}</Label><Switch id="image-update-enabled" checked={draft.enabled} onCheckedChange={enabled => setDraft({ ...draft, enabled })} /></div><div className="grid gap-1.5"><Label>{zh ? '检测间隔' : 'Check interval'}</Label><Select items={options} value={String(draft.interval_hours)} onValueChange={value => setDraft({ ...draft, interval_hours: Number(value) })}><SelectTrigger aria-label={zh ? '检测间隔' : 'Check interval'}><SelectValue /></SelectTrigger><SelectContent>{options.map(option => <SelectItem key={option.value} value={option.value}>{option.label}</SelectItem>)}</SelectContent></Select></div><RegistrySelections nodeID={nodeID} hosts={hosts} values={draft.registry_credentials} onChange={registry_credentials => setDraft({ ...draft, registry_credentials })} />{query.data?.next_run_at && <p className="text-xs text-muted-foreground">{zh ? '下次检测：' : 'Next check: '}{formatDateTime(query.data.next_run_at)}</p>}<div className="flex flex-wrap items-center gap-2"><Button type="submit" disabled={save.isPending}><Save />{zh ? '保存策略' : 'Save policy'}</Button><ImageUpdateCheck nodeID={nodeID} />{save.isSuccess && <span className="text-xs text-muted-foreground">{zh ? '已保存' : 'Saved'}</span>}</div>{save.isError && <div className="text-sm text-destructive">{save.error.message}{save.error instanceof ApiError && save.error.status === 409 && <Button variant="outline" size="sm" className="ml-2" onClick={() => { void query.refetch().then(result => { if (result.data) setDraft(result.data); save.reset() }) }}>{zh ? '重新加载策略' : 'Reload policy'}</Button>}</div>}</form>
}
