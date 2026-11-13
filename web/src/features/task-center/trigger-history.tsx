import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Button } from '../../components/ui/button'
import { ErrorState } from '../../components/ui/error-state'
import { ListPagination } from '../../components/ui/list-pagination'
import { LoadingState } from '../../components/ui/loading-state'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../../components/ui/select'
import { Sheet, SheetContent, SheetTitle } from '../../components/ui/sheet'
import { StatusBadge } from '../../components/ui/status-badge'
import { api } from '../../lib/api'
import { useI18n } from '../../lib/i18n'
import { useDateTime } from '../../lib/time-zone'
import { useUIStore } from '../../stores/ui'
import type { CenterScope } from '../../pages/tasks'
import { RuleFilter, SourceFilter } from './history-filters'
import { triggerSourceLabel } from './history-labels'
import { useTriggerRecords } from './queries'
import type { Schedule, ScheduleRun } from './types'

const statuses = ['dispatching', 'started', 'skipped', 'start_failed', 'interrupted']

export function TriggerHistory({ rule, scope = 'all', nodeID, onClose, onTask }: { rule?: Schedule; scope?: CenterScope; nodeID?: string; onClose: () => void; onTask: (id: string) => void }) {
  const { language } = useI18n(), zh = language === 'zh-CN'
  const { formatDateTime } = useDateTime()
  const [page, setPage] = useState(0), [source, setSource] = useState(''), [status, setStatus] = useState('')
  const [scheduleID, setScheduleID] = useState<number>()
  const pageSize = useUIStore(state => state.listPageSize), setPageSize = useUIStore(state => state.setListPageSize)
  const query = useTriggerRecords({ scope: rule ? 'all' : scope === 'current' ? 'node' : scope, nodeID: !rule && scope === 'current' ? nodeID : undefined, scheduleID: rule?.id ?? scheduleID, source, status, limit: pageSize, offset: page * pageSize })
  const total = query.data?.total ?? 0, pageCount = Math.max(1, Math.ceil(total / pageSize))
  useEffect(() => { if (query.data && page >= pageCount) setPage(pageCount - 1) }, [query.data, page, pageCount])
  return <Sheet open onOpenChange={open => { if (!open) onClose() }}><SheetContent className="overflow-y-auto"><div className="space-y-4 p-4">
    <SheetTitle>{zh ? '触发历史' : 'Trigger history'}{rule ? ` · ${rule.name}` : ''}</SheetTitle>
    <div className="flex flex-wrap gap-2">
      <SourceFilter value={source} onChange={value => { setSource(value); setPage(0) }} zh={zh} />
      <Select value={status} onValueChange={value => { if (value !== null) { setStatus(value); setPage(0) } }}>
        <SelectTrigger className="w-40 max-w-full" aria-label={zh ? '触发状态' : 'Trigger status'}><SelectValue>{status || (zh ? '全部触发状态' : 'All trigger states')}</SelectValue></SelectTrigger>
        <SelectContent><SelectItem value="">{zh ? '全部触发状态' : 'All trigger states'}</SelectItem>{statuses.map(value => <SelectItem key={value} value={value}>{value}</SelectItem>)}</SelectContent>
      </Select>
      {!rule && <RuleFilter value={scheduleID} onChange={value => { setScheduleID(value); setPage(0) }} zh={zh} />}
    </div>
    {query.isPending ? <LoadingState label={zh ? '正在加载触发历史' : 'Loading trigger history'} /> : query.isError ? <ErrorState description={query.error.message} /> : <>
      {!query.data.items.length && <p className="text-sm text-muted-foreground">{zh ? '暂无触发记录' : 'No trigger records'}</p>}
      {query.data.items.map(row => <section key={row.id} className="section-surface space-y-2">
        {!rule && <p className="break-words text-sm">{row.rule_name || row.subject || row.kind || (zh ? '临时触发' : 'Ad-hoc trigger')}{row.schedule_id ? ` · #${row.schedule_id}` : ''}</p>}
        <div className="flex flex-wrap items-center justify-between gap-2"><span className="flex flex-wrap gap-1.5"><StatusBadge tone={row.status === 'start_failed' ? 'critical' : 'neutral'}>{row.status}</StatusBadge>{row.task_status && <StatusBadge tone={row.task_status === 'failed' ? 'critical' : row.task_status === 'success' ? 'success' : 'neutral'}>{row.task_status}</StatusBadge>}</span><span className="text-xs text-muted-foreground">{triggerSourceLabel(row.triggered_by, zh)} · {formatDateTime(row.created_at)}</span></div>
        {!rule && (row.node_name || row.node_id) && <p className="break-words text-xs text-muted-foreground">{row.node_name || row.node_id}</p>}
        {row.scheduled_for && <p className="text-xs text-muted-foreground">{zh ? '计划时间' : 'Scheduled for'} · {formatDateTime(row.scheduled_for)}</p>}
        {(row.reason_code || row.error) && <p className="break-words text-xs text-muted-foreground">{row.reason_code}{row.error ? ` · ${row.error}` : ''}</p>}
        {row.task_id && <Button size="sm" variant="outline" onClick={() => onTask(row.task_id)}>{zh ? '查看执行' : 'View task'}</Button>}
        <TriggerSnapshot id={row.id} zh={zh} />
      </section>)}
      <ListPagination total={total} page={Math.min(page, pageCount - 1)} pageCount={pageCount} pageSize={pageSize} setPage={setPage} setPageSize={size => { setPageSize(size); setPage(0) }} zh={zh} />
    </>}
  </div></SheetContent></Sheet>
}

function TriggerSnapshot({ id, zh }: { id: number; zh: boolean }) {
  const [expanded, setExpanded] = useState(false)
  const query = useQuery({ queryKey: ['task-trigger', id], queryFn: () => api<ScheduleRun>(`/task-center/runs/${id}`), enabled: expanded })
  return <details onToggle={event => setExpanded(event.currentTarget.open)}><summary className="cursor-pointer text-xs text-muted-foreground">{zh ? '查看触发快照' : 'View trigger snapshot'}</summary>
    {expanded && (query.isPending ? <LoadingState label={zh ? '正在加载快照' : 'Loading snapshot'} /> : query.isError ? <ErrorState description={query.error.message} /> : <pre className="mt-2 max-h-64 overflow-auto break-all whitespace-pre-wrap text-xs text-muted-foreground">{JSON.stringify(query.data, null, 2)}</pre>)}
  </details>
}
