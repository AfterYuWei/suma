import { useEffect, useState } from 'react'
import { useI18n } from '../../lib/i18n'
import { useDateTime } from '../../lib/time-zone'
import { Button } from '../../components/ui/button'
import { ListPagination } from '../../components/ui/list-pagination'
import { useUIStore } from '../../stores/ui'
import { ErrorState } from '../../components/ui/error-state'
import { ListShell } from '../../components/ui/list-shell'
import { LoadingState } from '../../components/ui/loading-state'
import { Progress } from '../../components/ui/progress'
import { StatusBadge } from '../../components/ui/status-badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../components/ui/table'
import { taskKindFor } from './kinds'
import type { CenterScope } from '../../pages/tasks'
import { useTaskRecords } from './queries'
import { TaskRecordDetail } from './task-record-detail'
import { RuleFilter, SourceFilter } from './history-filters'
import { triggerSourceLabel } from './history-labels'

const statusTone = (status: string) => status === 'success' ? 'success' : status === 'failed' ? 'critical' : status === 'running' ? 'warning' : 'neutral'

const statusOptions = ['finished', 'success', 'failed', 'canceled'] as const

// RecordsTab is the durable execution history: every task run with its
// recorded input, output, steps and logs one click away.
export function RecordsTab({ scope, nodeID, active = false }: { scope: CenterScope; nodeID?: string; active?: boolean }) {
  const { language } = useI18n()
  const zh = language === 'zh-CN'
  const { formatDateTime } = useDateTime()
  const [status, setStatus] = useState('finished')
  const [type, setType] = useState('')
  const [search, setSearch] = useState('')
  const [source, setSource] = useState('')
  const [scheduleID, setScheduleID] = useState<number>()
  const [detail, setDetail] = useState<string | null>(null)
  const [page, setPage] = useState(0)
  const pageSize = useUIStore((state) => state.listPageSize)
  const setPageSize = useUIStore((state) => state.setListPageSize)
  const query = useTaskRecords({ scope: scope === 'current' ? 'node' : scope, nodeID: scope === 'current' ? nodeID : undefined, status: active ? 'active' : status, type, search: search.trim(), source, scheduleID, limit: pageSize, offset: page * pageSize })
  const total = query.data?.total ?? 0
  const pageCount = Math.max(1, Math.ceil(total / pageSize))
  useEffect(() => { if (query.data && page >= pageCount) setPage(pageCount - 1) }, [query.data, page, pageCount])
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        {!active && <div className="flex flex-wrap gap-1.5">
          {statusOptions.map(value => <Button key={value} size="sm" variant={status === value ? 'secondary' : 'ghost'} onClick={() => { setStatus(value); setPage(0) }}>{value === 'finished' ? (zh ? '全部结果' : 'All results') : value}</Button>)}
        </div>}
        <input value={search} maxLength={256} aria-label={zh ? '搜索任务' : 'Search tasks'} onChange={event => { setSearch(event.target.value); setPage(0) }} placeholder={zh ? '搜索任务、摘要或节点' : 'Search task, summary or node'} className="h-8 w-56 max-w-full rounded-md border bg-transparent px-2.5 text-xs" />
        <SourceFilter value={source} onChange={value => { setSource(value); setPage(0) }} zh={zh} includeUnrecorded />
        <RuleFilter value={scheduleID} onChange={value => { setScheduleID(value); setPage(0) }} zh={zh} />
        <input
          value={type}
          maxLength={64}
          aria-label={zh ? '任务类型前缀' : 'Task type prefix'}
          onChange={(event) => { setType(event.target.value); setPage(0) }}
          placeholder={zh ? '类型前缀，如 compose. / image.' : 'Type prefix, e.g. compose. / image.'}
          className="h-8 w-56 rounded-md border bg-transparent px-2.5 text-xs"
        />
      </div>
      {query.isPending ? <LoadingState label={zh ? '正在加载任务记录' : 'Loading task records'} /> : query.isError ? <ErrorState description={query.error.message} /> : (
        <>
          <ListShell>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{zh ? '任务' : 'Task'}</TableHead>
                  <TableHead className="w-28">{zh ? '类型' : 'Kind'}</TableHead>
                  <TableHead className="w-32">{zh ? '作用域' : 'Scope'}</TableHead>
                  <TableHead className="w-36">{zh ? '状态' : 'Status'}</TableHead>
                  <TableHead className="w-44">{zh ? '创建时间' : 'Created'}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(query.data?.items ?? []).length === 0 && (
                  <TableRow><TableCell colSpan={5} className="h-24 text-center text-muted-foreground">{zh ? '暂无任务记录' : 'No task records'}</TableCell></TableRow>
                )}
                {(query.data?.items ?? []).map((row) => {
                  const kind = taskKindFor(row.type)
                  const Icon = kind?.icon
                  return (
                    <TableRow key={row.id} className="cursor-pointer" onClick={() => setDetail(row.id)}>
                      <TableCell className="max-w-72 whitespace-normal">
                        <div className="flex items-center gap-2 font-medium">{Icon && <Icon className="size-4 shrink-0 text-muted-foreground" />}<span className="truncate">{row.name}</span></div>
                        <div className="truncate text-xs text-muted-foreground">{row.message || row.type}</div>
                        <div className="truncate text-xs text-muted-foreground">{triggerSourceLabel(row.triggered_by, zh)}{row.schedule_id ? ` · #${row.schedule_id} ${row.schedule_name ?? ''}` : ''}</div>
                      </TableCell>
                      <TableCell className="font-mono text-xs text-muted-foreground">{kind ? kind.label(zh) : row.type}</TableCell>
                      <TableCell className="text-sm">
                        <div>{row.scope === 'control_plane' ? (zh ? '控制平面' : 'Control plane') : (zh ? '节点' : 'Node')}</div>
                        {row.node_id && <div className="truncate text-xs text-muted-foreground">{row.node_name || row.node_id}</div>}
                      </TableCell>
                      <TableCell>
                        <StatusBadge tone={statusTone(row.status)}>{row.status}</StatusBadge>
                        {['running', 'pending'].includes(row.status) && <Progress value={Number(row.progress)} className="mt-1" />}
                      </TableCell>
                      <TableCell className="text-muted-foreground tabular-nums">
                        <div>{formatDateTime(row.created_at)}</div>
                        {row.finished_at && <div className="text-xs">{zh ? '完成' : 'finished'} {formatDateTime(row.finished_at)}</div>}
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          </ListShell>
          <ListPagination total={total} page={page} pageCount={pageCount} pageSize={pageSize} setPage={setPage} setPageSize={(size) => { setPageSize(size); setPage(0) }} zh={zh} />
          <p className="text-xs text-muted-foreground">{zh ? `最近 ${query.data?.total ?? 0} 条记录匹配当前筛选。` : `${query.data?.total ?? 0} records match the current filters.`}</p>
        </>
      )}
      <TaskRecordDetail id={detail ?? undefined} open={!!detail} onOpenChange={(open) => { if (!open) setDetail(null) }} />
    </div>
  )
}
