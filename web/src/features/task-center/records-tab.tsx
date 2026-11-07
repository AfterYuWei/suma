import { useState } from 'react'
import { useI18n } from '../../lib/i18n'
import { useDateTime } from '../../lib/time-zone'
import { Button } from '../../components/ui/button'
import { ListPagination } from '../../components/ui/list-pagination'
import { useListPagination } from '../../components/ui/use-list-pagination'
import { ListShell } from '../../components/ui/list-shell'
import { LoadingState } from '../../components/ui/loading-state'
import { Progress } from '../../components/ui/progress'
import { StatusBadge } from '../../components/ui/status-badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../components/ui/table'
import { taskKindFor } from './kinds'
import { useTaskRecords } from './queries'
import { TaskRecordDetail } from './task-record-detail'

const statusTone = (status: string) => status === 'success' ? 'success' : status === 'failed' ? 'critical' : status === 'running' ? 'warning' : 'neutral'

const statusOptions = ['', 'pending', 'running', 'success', 'failed', 'canceled'] as const

// RecordsTab is the durable execution history: every task run with its
// recorded input, output, steps and logs one click away.
export function RecordsTab() {
  const { language } = useI18n()
  const zh = language === 'zh-CN'
  const { formatDateTime } = useDateTime()
  const [scope, setScope] = useState<'all' | 'control_plane' | 'node'>('all')
  const [status, setStatus] = useState('')
  const [type, setType] = useState('')
  const [detail, setDetail] = useState<string | null>(null)
  const query = useTaskRecords({ scope, status, type })
  const pagination = useListPagination(query.data?.items ?? [], `${scope}:${status}:${type}`)
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <div className="flex flex-wrap gap-1.5">
          {([['all', zh ? '全部' : 'All'], ['control_plane', zh ? '控制平面' : 'Control plane'], ['node', zh ? '节点' : 'Nodes']] as const).map(([value, label]) => (
            <Button key={value} size="sm" variant={scope === value ? 'secondary' : 'outline'} onClick={() => setScope(value)}>{label}</Button>
          ))}
        </div>
        <div className="flex flex-wrap gap-1.5">
          {statusOptions.map((value) => (
            <Button key={value || 'all'} size="sm" variant={status === value ? 'secondary' : 'ghost'} onClick={() => setStatus(value)}>{value ? value : zh ? '全部状态' : 'Any status'}</Button>
          ))}
        </div>
        <input
          value={type}
          onChange={(event) => setType(event.target.value)}
          placeholder={zh ? '类型前缀，如 compose. / image.' : 'Type prefix, e.g. compose. / image.'}
          className="h-8 w-56 rounded-md border bg-transparent px-2.5 text-xs"
        />
      </div>
      {query.isPending ? <LoadingState label={zh ? '正在加载任务记录' : 'Loading task records'} /> : (
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
                {pagination.items.length === 0 && (
                  <TableRow><TableCell colSpan={5} className="h-24 text-center text-muted-foreground">{zh ? '暂无任务记录' : 'No task records'}</TableCell></TableRow>
                )}
                {pagination.items.map((row) => {
                  const kind = taskKindFor(row.type)
                  const Icon = kind?.icon
                  return (
                    <TableRow key={row.id} className="cursor-pointer" onClick={() => setDetail(row.id)}>
                      <TableCell className="max-w-72 whitespace-normal">
                        <div className="flex items-center gap-2 font-medium">{Icon && <Icon className="size-4 shrink-0 text-muted-foreground" />}<span className="truncate">{row.name}</span></div>
                        <div className="truncate text-xs text-muted-foreground">{row.message || row.type}</div>
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
          <ListPagination {...pagination} zh={zh} />
          <p className="text-xs text-muted-foreground">{zh ? `最近 ${query.data?.total ?? 0} 条记录匹配当前筛选。` : `${query.data?.total ?? 0} records match the current filters.`}</p>
        </>
      )}
      <TaskRecordDetail id={detail ?? undefined} open={!!detail} onOpenChange={(open) => { if (!open) setDetail(null) }} />
    </div>
  )
}
