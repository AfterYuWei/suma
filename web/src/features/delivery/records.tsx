import { Link } from '@tanstack/react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { RotateCcw } from 'lucide-react'
import { Button } from '../../components/ui/button'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../../components/ui/select'
import { ListPagination } from '../../components/ui/list-pagination'
import { LoadingState } from '../../components/ui/loading-state'
import { ErrorState } from '../../components/ui/error-state'
import { api } from '../../lib/api'
import { useDateTime } from '../../lib/time-zone'
import type { ListPageSize } from '../../stores/ui'
import type { DeliveryRecord, DeliveryRecordPage, DeliveryRelease } from './types'
import { parseStringList, shortCommit, recordOperationLabel } from './types'
import { ReleaseStatus } from './releases'

export function RecordSnapshot({ record, projectName, zh }: { record: DeliveryRecord; projectName: string; zh: boolean }) {
  const [visible, setVisible] = useState(false)
  const client = useQueryClient()
  const values = useQuery({ queryKey: ['delivery-record-environment', projectName, record.id], queryFn: () => api<Record<string, string>>(`/delivery-projects/${encodeURIComponent(projectName)}/records/${record.id}/environment`), enabled: visible, gcTime: 0, staleTime: 0 })
  const snapshotNote = record.operation === 'rollback_failed' || record.snapshot_note === 'Each node restores its own previous configuration record'
    ? (zh ? '各节点使用独立配置，请展开节点查看对应快照。' : 'Each node uses its own configuration; expand a node to inspect its snapshot.')
    : (zh ? '历史配置快照不完整，无法用于回滚。' : record.snapshot_note || 'The historical snapshot is incomplete and cannot be restored.')
  return <div className="flex min-w-0 flex-col gap-3 text-xs">
    {!record.snapshot_complete && <p className="text-amber-600 dark:text-amber-400">{snapshotNote}</p>}
    {!record.images_pinned && <p className="text-xs text-muted-foreground">{zh ? '此历史记录未固定镜像内容，只能重新应用配置，不能保证恢复原版本。' : 'Historical image contents were not pinned. Reapplying configuration cannot guarantee the original version.'}</p>}
    <dl className="grid min-w-0 gap-2 rounded-lg bg-muted/40 p-3 sm:grid-cols-2 [&_dd]:break-all [&_dt]:text-muted-foreground">
      <div><dt>Commit</dt><dd>{record.commit_sha}</dd></div>
      <div><dt>{zh ? '配置 Hash' : 'Configuration hash'}</dt><dd>{record.config_hash || '—'}</dd></div>
      <div><dt>Compose</dt><dd>{parseStringList(record.compose_files).join(', ') || '—'}</dd></div>
      <div><dt>{zh ? '环境文件' : 'Environment file'}</dt><dd>{record.environment_file || '—'}</dd></div>
      <div><dt>{zh ? '目标节点' : 'Target nodes'}</dt><dd>{record.node_ids.join(', ') || '—'}</dd></div>
      <div><dt>{zh ? '镜像' : 'Images'}</dt><dd>{parseStringList(record.image_references).join(', ') || '—'}</dd></div>
      <div><dt>{zh ? 'Git 凭据' : 'Git credential'}</dt><dd>{record.git_credential_id || '—'}</dd></div>
      <div><dt>{zh ? '镜像仓库凭据' : 'Registry credentials'}</dt><dd>{record.registry_credential_ids.join(', ') || '—'}</dd></div>
      <div><dt>{zh ? '超时 / 自动回滚' : 'Timeout / automatic rollback'}</dt><dd>{record.snapshot_complete ? `${record.deployment_timeout}s / ${record.auto_rollback ? (zh ? '开启' : 'On') : (zh ? '关闭' : 'Off')}` : '—'}</dd></div>
    </dl>
    {!!record.environment.length && <div className="rounded-lg bg-muted/40 p-3">
      <div className="mb-2 flex items-center justify-between gap-2"><span>{zh ? '环境变量' : 'Environment variables'}</span><Button variant="ghost" size="xs" onClick={() => { setVisible(!visible); if (visible) client.removeQueries({ queryKey: ['delivery-record-environment', projectName, record.id] }) }}>{visible ? (zh ? '隐藏变量值' : 'Hide values') : (zh ? '显示变量值' : 'Show values')}</Button></div>
      {values.isError && visible && <p className="text-destructive">{values.error.message}</p>}
      {record.environment.map(variable => <p key={variable.name} className="whitespace-pre-wrap break-all font-mono">{variable.name}={visible && values.data ? values.data[variable.name] : '••••••'}</p>)}
    </div>}
  </div>
}

export function DeliveryRecords({ projectName, releases, busy, canDeliver, zh, onRollback, onRetry, onRestoreFailed }: { projectName: string; releases: DeliveryRelease[]; busy: boolean; canDeliver: boolean; zh: boolean; onRollback: (record: DeliveryRecord) => void; onRetry?: (record: DeliveryRecord) => void; onRestoreFailed?: (record: DeliveryRecord) => void }) {
  const [operation, setOperation] = useState('all'), [status, setStatus] = useState('all'), [releaseID, setReleaseID] = useState('all')
  const [page, setPage] = useState(0), [pageSize, setPageSize] = useState<ListPageSize>(20)
  const { formatDateTime } = useDateTime()
  const query = useQuery({ queryKey: ['delivery-records', projectName, releaseID, operation, status, page, pageSize], queryFn: () => api<DeliveryRecordPage>(`/delivery-projects/${encodeURIComponent(projectName)}/records?page=${page + 1}&page_size=${pageSize}${releaseID === 'all' ? '' : `&release_id=${releaseID}`}${operation === 'all' ? '' : `&operation=${operation}`}${status === 'all' ? '' : `&status=${status}`}`), refetchInterval: 5_000 })
  const counts = query.data?.counts ?? {}
  return <div className="flex min-w-0 flex-col gap-3">
    <div className="flex flex-wrap items-center gap-3 text-xs text-muted-foreground">{['deploy', 'retry', 'manual_rollback', 'auto_rollback', 'rollback_failed'].map(kind => <span key={kind}>{recordOperationLabel(kind, zh)} <span className="text-foreground tabular-nums">{counts[kind] ?? 0}</span></span>)}</div>
    <div className="flex flex-wrap gap-2">
      <Select value={releaseID} onValueChange={value => { if (value) { setReleaseID(value); setPage(0) } }}><SelectTrigger className="w-48" aria-label="Release"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="all">{zh ? '全部 Release' : 'All releases'}</SelectItem>{releases.map(release => <SelectItem key={release.id} value={String(release.id)}>#{release.id} · {shortCommit(release.commit_sha)}</SelectItem>)}</SelectContent></Select>
      <Select value={operation} onValueChange={value => { if (value) { setOperation(value); setPage(0) } }}><SelectTrigger className="w-44" aria-label={zh ? '操作类型' : 'Operation type'}><SelectValue /></SelectTrigger><SelectContent><SelectItem value="all">{zh ? '全部操作' : 'All operations'}</SelectItem>{['deploy', 'retry', 'manual_rollback', 'auto_rollback', 'rollback_failed'].map(kind => <SelectItem key={kind} value={kind}>{recordOperationLabel(kind, zh)}</SelectItem>)}</SelectContent></Select>
      <Select value={status} onValueChange={value => { if (value) { setStatus(value); setPage(0) } }}><SelectTrigger className="w-36" aria-label={zh ? '记录状态' : 'Record status'}><SelectValue /></SelectTrigger><SelectContent><SelectItem value="all">{zh ? '全部状态' : 'All statuses'}</SelectItem>{['succeeded', 'failed', 'partial_failed', 'interrupted', 'deploying', 'validating', 'pending'].map(value => <SelectItem key={value} value={value}><ReleaseStatus status={value} zh={zh} /></SelectItem>)}</SelectContent></Select>
    </div>
    {query.isPending ? <LoadingState label={zh ? '正在加载记录' : 'Loading records'} /> : query.isError ? <ErrorState title={zh ? '无法加载记录' : 'Unable to load records'} description={query.error.message} /> : <>
      {!query.data.items.length && <p className="rounded-lg bg-muted/40 p-8 text-center text-sm text-muted-foreground">{zh ? '暂无交付记录' : 'No delivery records'}</p>}
      {query.data.items.map(record => {
        const failed = record.attempts.filter(attempt => ['failed', 'interrupted'].includes(attempt.status)).length
        return <details key={record.id} className="min-w-0 rounded-lg bg-surface px-3 py-2">
          <summary className="cursor-pointer list-none"><div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm"><span className="font-medium">#{record.id} · {recordOperationLabel(record.operation, zh)}</span><ReleaseStatus status={record.status} zh={zh} /><span className="text-xs text-muted-foreground">Release #{record.release_id} · {shortCommit(record.commit_sha)}</span><span className="ml-auto text-xs text-muted-foreground">{record.actor_name || (zh ? '未知执行人' : 'Unknown actor')} · {formatDateTime(record.started_at || record.created_at)}</span></div></summary>
          <div className="mt-3 flex min-w-0 flex-col gap-3">
            {record.failure_reason && <p className="break-words text-xs text-destructive">{record.failure_reason}</p>}
            <div className="flex flex-wrap items-center gap-3 text-xs text-muted-foreground"><span>{zh ? `节点：成功 ${record.attempts.filter(a => a.status === 'succeeded').length} / 失败 ${failed}` : `Nodes: ${record.attempts.filter(a => a.status === 'succeeded').length} succeeded / ${failed} failed`}</span><span className="break-all">{zh ? '任务' : 'Task'}: {record.task_id ? <Link to="/tasks" hash={record.task_id} className="underline underline-offset-2">{record.task_id}</Link> : '—'}</span>{record.source_record_id && <span>{zh ? '来源记录' : 'Source record'} #{record.source_record_id}</span>}</div>
            <RecordSnapshot key={record.id} record={record} projectName={projectName} zh={zh} />
            {record.attempts.map(attempt => <div key={attempt.id} className="flex flex-wrap items-start gap-2 rounded-md bg-muted/30 p-2 text-xs"><span>{attempt.node_name || attempt.node_id}</span><ReleaseStatus status={attempt.status} zh={zh} /><span className="min-w-0 break-all text-destructive">{attempt.failure_reason}</span>{attempt.target_record_id && <span className="text-muted-foreground">{zh ? '配置记录' : 'Configuration record'} #{attempt.target_record_id}</span>}{attempt.target_record_id && attempt.target_record_id !== record.id && <NodeRecordSnapshot projectName={projectName} recordID={attempt.target_record_id} zh={zh} />}</div>)}
            {canDeliver && <div className="flex flex-wrap gap-2">{failed > 0 && record.images_pinned && onRetry && <Button size="sm" disabled={busy} onClick={() => onRetry(record)}>{zh ? `重试失败节点（${failed}）…` : `Retry failed nodes (${failed})…`}</Button>}{failed > 0 && onRestoreFailed && <Button variant="ghost" size="sm" disabled={busy} onClick={() => onRestoreFailed(record)}>{zh ? '恢复失败节点…' : 'Restore failed nodes…'}</Button>}{record.attempts.some(attempt => attempt.status === 'succeeded') && <Button variant="outline" size="sm" disabled={busy || !record.snapshot_complete} onClick={() => onRollback(record)}><RotateCcw />{record.images_pinned ? (zh ? '恢复到此版本…' : 'Restore this version…') : (zh ? '重新应用历史配置…' : 'Reapply historical configuration…')}</Button>}</div>}
          </div>
        </details>
      })}
      <ListPagination total={query.data.total} page={page} pageCount={Math.max(1, Math.ceil(query.data.total / pageSize))} pageSize={pageSize} setPage={setPage} setPageSize={size => { setPageSize(size); setPage(0) }} zh={zh} />
    </>}
  </div>
}

function NodeRecordSnapshot({ projectName, recordID, zh }: { projectName: string; recordID: number; zh: boolean }) {
  const [open, setOpen] = useState(false)
  const query = useQuery({ queryKey: ['delivery-record', projectName, recordID], queryFn: () => api<DeliveryRecord>(`/delivery-projects/${encodeURIComponent(projectName)}/records/${recordID}`), enabled: open })
  return <details className="w-full" onToggle={event => setOpen(event.currentTarget.open)}><summary className="cursor-pointer">{zh ? '查看节点配置快照' : 'Inspect node configuration snapshot'}</summary><div className="mt-2">{open && (query.isPending ? <span>{zh ? '加载中' : 'Loading'}</span> : query.isError ? <span className="text-destructive">{query.error.message}</span> : <RecordSnapshot record={query.data} projectName={projectName} zh={zh} />)}</div></details>
}
