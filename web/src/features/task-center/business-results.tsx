import { useQuery } from '@tanstack/react-query'
import { api } from '../../lib/api'
import { useI18n } from '../../lib/i18n'
import { Button } from '../../components/ui/button'
import { ErrorState } from '../../components/ui/error-state'
import { LoadingState } from '../../components/ui/loading-state'
import { StatusBadge } from '../../components/ui/status-badge'
import { CleanupRunEvidence } from '../cleanup/storage-cleanup'
import { statusLabel, type Run } from '../cleanup/types'
import { recordOperationLabel, type DeliveryHistoryPage } from '../delivery/types'
import { ReleaseStatus } from '../delivery/releases'
import type { TaskRecord } from './types'

export interface DeliveryTaskEvidence {
  records: DeliveryHistoryPage
  releases: { id: number; project_id: number; project_name: string; status: string; commit_sha: string; commit_message: string; created_at: string }[]
}

export function DeliveryTaskResult({ task }: { task: TaskRecord }) {
  const { language } = useI18n(), zh = language === 'zh-CN'
  const query = useQuery({
    queryKey: ['task-delivery-evidence', task.id],
    queryFn: () => api<DeliveryTaskEvidence>(`/delivery-records/by-task/${encodeURIComponent(task.id)}`),
    refetchInterval: ['pending', 'running'].includes(task.status) ? 2000 : false,
  })
  if (query.isPending) return <LoadingState compact label={zh ? '正在加载业务记录' : 'Loading business records'} />
  if (query.isError) return <ErrorState description={query.error.message} />
  const evidence = query.data
  return <div className="space-y-3" data-testid="task-delivery-evidence">
    <p className="text-xs text-muted-foreground">{zh ? '以下为原交付记录，执行状态以当前 Task 为准。' : 'Original delivery evidence. Execution status remains owned by this Task.'}</p>
    {!evidence.records.total && !evidence.releases.length && <p className="text-sm text-muted-foreground">{zh ? '暂无可用的关联业务记录，可展开 Task 中保存的完整结果。' : 'No linked business records are available. Expand the full result recorded on this Task.'}</p>}
    {evidence.releases.map(row => <div key={row.id} className="space-y-1 break-words text-xs"><p>{row.project_name || `#${row.project_id}`} · Release #{row.id} · {row.status}</p><p className="break-all font-mono text-muted-foreground">{row.commit_sha}</p>{row.commit_message && <p>{row.commit_message}</p>}{row.project_name && <a className="inline-block underline underline-offset-2" href={`/continuous-delivery/${encodeURIComponent(row.project_name)}`}>{zh ? '查看交付项目' : 'Open delivery project'}</a>}</div>)}
    {evidence.records.items.map(row => <div key={row.id} className="space-y-1 break-words text-xs"><div className="flex flex-wrap items-center gap-2"><span>{row.project_name || `#${row.project_id}`} · #{row.record_id} · {recordOperationLabel(row.operation, zh)}</span><ReleaseStatus status={row.status} zh={zh} /></div><p className="break-all font-mono text-muted-foreground">{row.commit_sha}{row.config_hash ? ` · ${row.config_hash}` : ''}</p>{row.failure_reason && <p className="text-destructive">{row.failure_reason}</p>}{row.source_record_id && <p>{zh ? '来源记录' : 'Source record'} #{row.source_record_id}</p>}</div>)}
    {!!evidence.records.total && <Button size="sm" variant="outline" nativeButton={false} render={<a href={`/continuous-delivery?view=records&task=${encodeURIComponent(task.id)}`} />}>{zh ? `查看关联业务历史（${evidence.records.total}）` : `View linked business history (${evidence.records.total})`}</Button>}
  </div>
}

export function CleanupTaskResult({ task }: { task: TaskRecord }) {
  const { language } = useI18n(), zh = language === 'zh-CN'
  const query = useQuery({
    queryKey: ['task-cleanup-evidence', task.id],
    queryFn: () => api<Run>(`/cleanup/runs/by-task/${encodeURIComponent(task.id)}`),
    refetchInterval: ['pending', 'running'].includes(task.status) ? 2000 : false,
  })
  if (query.isPending) return <LoadingState compact label={zh ? '正在加载清理记录' : 'Loading cleanup record'} />
  if (query.isError) return <ErrorState description={query.error.message} />
  const run = query.data
  return <div className="space-y-3" data-testid="task-cleanup-evidence"><div className="flex flex-wrap items-center gap-2 text-xs"><span>{run.node_name || run.node_id} · {run.id}</span><StatusBadge tone={run.status === 'success' ? 'success' : run.status === 'partial_failed' ? 'warning' : 'neutral'}>{statusLabel(run.status, zh)}</StatusBadge></div><p className="text-xs text-muted-foreground">{zh ? '原清理记录 · 策略版本' : 'Original cleanup record · policy version'} {run.policy_version}</p><CleanupRunEvidence run={run} zh={zh} /></div>
}
