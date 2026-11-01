import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { Check } from 'lucide-react'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '../../components/ui/dialog'
import { Button } from '../../components/ui/button'
import { Checkbox } from '../../components/ui/checkbox'
import { LoadingState } from '../../components/ui/loading-state'
import { api } from '../../lib/api'
import type { DockerNode } from '../../lib/nodes'
import { useDateTime } from '../../lib/time-zone'
import type { DeliveryRecord, DeliveryRecordPage, DeliveryRelease } from './types'
import { ReleaseStatus } from './releases'
import { RecordSnapshot } from './records'
import { recordOperationLabel } from './types'
import { TargetSelector } from './settings'

export function FailedNodesDialog({ release, operation, zh, onClose, onConfirm }: { release: DeliveryRelease; operation: 'retry-failed' | 'rollback-failed'; zh: boolean; onClose: () => void; onConfirm: () => void }) {
  const retry = operation === 'retry-failed'
  const ids = retry ? release.remediation.retry_failed_node_ids : release.remediation.rollback_failed_node_ids
  const label = retry ? (zh ? '重试' : 'Retry') : (zh ? '回滚失败节点' : 'Roll back failed nodes')
  return <Dialog open onOpenChange={open => { if (!open) onClose() }}><DialogContent className="sm:max-w-md"><DialogHeader><DialogTitle>{label}（{ids.length}）</DialogTitle><DialogDescription>{retry ? (zh ? '沿用失败记录的配置快照，仅重试下列节点。自动回滚成功的节点也会重试原配置。' : 'Retry only the nodes below using the failed record’s configuration, including nodes recovered by automatic rollback.') : (zh ? '下列节点各自恢复到部署前的配置快照，保留存储卷数据。' : 'Restore each node below to its own pre-deployment configuration; volume data is preserved.')}</DialogDescription></DialogHeader>
    <section className="flex min-w-0 flex-col gap-2 rounded-lg bg-destructive/5 p-3" aria-label={zh ? '失败节点' : 'Failed nodes'}><p className="text-xs font-medium text-muted-foreground">{zh ? '失败节点' : 'Failed nodes'}</p><ul className="max-h-60 space-y-1 overflow-auto text-sm text-red-600 dark:text-red-400">{ids.map(id => <li key={id} className="break-all">{release.deployments?.find(node => node.node_id === id)?.node_name || id}</li>)}</ul></section>
    <DialogFooter><Button variant="outline" onClick={onClose}>{zh ? '取消' : 'Cancel'}</Button><Button variant={retry ? 'default' : 'destructive'} disabled={!ids.length || !release.remediation.source_record_id} onClick={onConfirm}>{label}</Button></DialogFooter>
  </DialogContent></Dialog>
}

export function RollbackDialog({ projectName, release, initialRecord, zh, onClose, onConfirm }: { projectName: string; release: DeliveryRelease; initialRecord?: DeliveryRecord; zh: boolean; onClose: () => void; onConfirm: (recordID: number, nodeIDs?: string[]) => void }) {
  const [selected, setSelected] = useState<DeliveryRecord | undefined>(initialRecord)
  const [page, setPage] = useState(0), [override, setOverride] = useState(false), [nodeIDs, setNodeIDs] = useState<string[]>(initialRecord?.node_ids || [])
  const { formatDateTime } = useDateTime()
  const records = useQuery({ queryKey: ['delivery-records', projectName, 'rollback', release.id, page], queryFn: () => api<DeliveryRecordPage>(`/delivery-projects/${encodeURIComponent(projectName)}/records?release_id=${release.id}&page=${page + 1}&page_size=10`), refetchInterval: 5_000 })
  const nodes = useQuery({ queryKey: ['nodes'], queryFn: () => api<DockerNode[]>('/nodes') })
  const effective = override ? nodeIDs : selected?.node_ids || []
  const missing = effective.filter(id => nodes.data && !nodes.data.some(node => node.id === id && node.enabled))
  return <Dialog open onOpenChange={open => { if (!open) onClose() }}><DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-2xl"><DialogHeader><DialogTitle>{zh ? '回滚' : 'Roll back'} · Release #{release.id}</DialogTitle><DialogDescription>{zh ? '选择本次使用的配置快照。回滚会重新拉取镜像并重建容器，保留存储卷数据，自动交付切换为手动。' : 'Select a configuration snapshot. Rollback pulls images and recreates containers, preserves volume data, and switches automatic delivery to manual.'}</DialogDescription></DialogHeader>
    {records.isPending ? <LoadingState label={zh ? '正在读取配置记录' : 'Loading snapshots'} /> : records.isError ? <p className="text-sm text-destructive">{records.error.message}</p> : <>
      {!records.data.items.length && <p className="text-sm text-muted-foreground">{zh ? '此 Release 暂无配置记录。' : 'This release has no configuration records.'}</p>}
      <div role="group" aria-label={zh ? '配置快照' : 'Configuration snapshots'} className="flex flex-col gap-2">{records.data.items.map(record => <button key={record.id} type="button" disabled={!record.snapshot_complete} aria-pressed={selected?.id === record.id} onClick={() => { setSelected(record); setOverride(false); setNodeIDs(record.node_ids) }} className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 rounded-lg bg-muted/40 px-3 py-2 text-left text-sm outline-none hover:bg-muted focus-visible:ring-1 focus-visible:ring-ring aria-pressed:bg-accent disabled:cursor-not-allowed disabled:opacity-50">
        <span className="flex min-w-0 items-center gap-1.5"><span className="inline-flex size-4 shrink-0">{selected?.id === record.id && <Check className="size-4" aria-hidden="true" />}</span>#{record.id} · {recordOperationLabel(record.operation, zh)}</span><ReleaseStatus status={record.status} zh={zh} /><span className="ml-auto text-xs text-muted-foreground">{record.actor_name || (zh ? '未知执行人' : 'Unknown actor')} · {formatDateTime(record.created_at)}</span><span className="w-full break-all font-mono text-xs text-muted-foreground">{record.config_hash || '—'}</span>{!record.snapshot_complete && <span className="w-full text-xs">{zh ? '配置快照不完整，仅供查看' : 'Incomplete snapshot; view only'}</span>}
      </button>)}</div>
      <nav className="flex items-center justify-between gap-2 text-xs text-muted-foreground" aria-label={zh ? '快照分页' : 'Snapshot pagination'}><span>{zh ? `共 ${records.data.total} 条` : `${records.data.total} records`}</span><div className="flex items-center gap-2"><Button variant="outline" size="sm" disabled={page === 0} onClick={() => setPage(page - 1)}>{zh ? '上一页' : 'Previous'}</Button><span>{page + 1} / {Math.max(1, Math.ceil(records.data.total / 10))}</span><Button variant="outline" size="sm" disabled={(page + 1) * 10 >= records.data.total} onClick={() => setPage(page + 1)}>{zh ? '下一页' : 'Next'}</Button></div></nav>
    </>}
    {selected && <div className="flex min-w-0 flex-col gap-3"><p className="text-xs font-medium">{zh ? '所选记录' : 'Selected record'} #{selected.id}</p><RecordSnapshot key={selected.id} record={selected} projectName={projectName} zh={zh} />
      <details className="rounded-lg bg-muted/30 p-3"><summary className="cursor-pointer text-sm">{zh ? '高级选项' : 'Advanced options'}</summary><div className="mt-3 flex flex-col gap-3"><label className="flex items-center gap-2 text-sm"><Checkbox checked={override} onCheckedChange={checked => { setOverride(checked === true); setNodeIDs(selected.node_ids) }} />{zh ? '临时覆盖目标节点' : 'Override target nodes for this operation'}</label><p className="text-xs text-muted-foreground">{zh ? '仅影响本次回滚，不修改设置或原快照。' : 'Applies only to this rollback; settings and the original snapshot are preserved.'}</p>{override && <>{nodeIDs.filter(id => nodes.data && !nodes.data.some(node => node.id === id)).map(id => <label key={id} className="flex items-start gap-2 text-sm text-destructive"><Checkbox checked onCheckedChange={() => setNodeIDs(current => current.filter(node => node !== id))} /><span className="break-all">{id} · {zh ? '已删除，取消勾选以移除' : 'Deleted; uncheck to remove'}</span></label>)}<TargetSelector nodes={nodes.data || []} value={nodeIDs} zh={zh} onChange={setNodeIDs} /></>}</div></details>
      <div className="text-sm"><p className="mb-1 text-xs text-muted-foreground">{zh ? '本次目标节点' : 'Target nodes for this rollback'}</p>{effective.map(id => <p key={id} className="break-all">{nodes.data?.find(node => node.id === id)?.name || id}</p>)}</div>
      {!!missing.length && <p className="break-all text-sm text-destructive">{zh ? '节点已删除或禁用，请通过高级选项调整：' : 'Nodes removed or disabled; adjust advanced options: '}{missing.join(', ')}</p>}
    </div>}
    {nodes.isError && <p className="text-sm text-destructive">{nodes.error.message}</p>}
    <DialogFooter><Button variant="outline" onClick={onClose}>{zh ? '取消' : 'Cancel'}</Button><Button variant="destructive" disabled={!selected?.snapshot_complete || !effective.length || !!missing.length || nodes.isPending || nodes.isError} onClick={() => { if (selected) onConfirm(selected.id, override ? nodeIDs : undefined) }}>{zh ? '回滚' : 'Roll back'}</Button></DialogFooter>
  </DialogContent></Dialog>
}
