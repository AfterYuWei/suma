import { useDateTime } from '../lib/time-zone'
import { useState } from 'react'
import { Link } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { LoadingState } from '../components/ui/loading-state'
import { ListShell } from '../components/ui/list-shell'
import { ListPagination } from '../components/ui/list-pagination'
import { useListPagination } from '../components/ui/use-list-pagination'
import { StatusBadge } from '../components/ui/status-badge'
import { Tabs, TabsList, TabsTrigger } from '../components/ui/tabs'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'
import { api } from '../lib/api'
import { displayDockerId } from '../lib/docker-id'
import { useI18n } from '../lib/i18n'
import { nodePath } from '../lib/nodes'
import { useUIStore } from '../stores/ui'
import { ResourceFrame } from './images'

interface Audit { id: number; scope: 'control_plane' | 'node'; node_id?: string; node_name?: string; user_id?: number; action: string; resource_type: string; resource_name: string; ip: string; result: string; created_at: string; source?: string; run_id?: string; operation_id?: string; task_id?: string; external_user_id?: string; chat_id?: string; details?: string }

export function AuditLogsPage() {
  const { formatDateTime } = useDateTime()

  const nodeID = useUIStore((state) => state.currentNodeID)
  const { t, language } = useI18n()
  const zh = language === 'zh-CN'
  const [scope, setScope] = useState<'current' | 'control_plane' | 'all'>('all')
  const query = useQuery({ queryKey: ['audit-logs', scope, nodeID], queryFn: () => api<Audit[]>(scope === 'current' ? nodePath(nodeID, '/audit-logs') : `/audit-logs?scope=${scope}`), refetchInterval: 5000 })
  const rows = query.data ?? []
  const pagination = useListPagination(rows, scope)

  return (
    <ResourceFrame title={t('auditLogs')} detail={zh ? '全部节点、控制平面和 AI 操作的统一审计记录' : 'Unified audit of all nodes, control-plane and AI actions'} action={<Tabs value={scope} onValueChange={(value) => setScope(value as typeof scope)}><TabsList><TabsTrigger value="all">{zh ? '全部' : 'All'}</TabsTrigger><TabsTrigger value="current">{zh ? '当前节点' : 'Current node'}</TabsTrigger><TabsTrigger value="control_plane">{zh ? '控制平面' : 'Control plane'}</TabsTrigger></TabsList></Tabs>}>
      {query.isPending
        ? <LoadingState compact label={zh ? '正在加载审计日志' : 'Loading audit logs'} />
        : rows.length === 0
          ? <p className="py-12 text-center text-sm text-muted-foreground">{zh ? '暂无审计记录' : 'No audit records'}</p>
          : (
              <>
                <ListShell><Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead className="w-44">{zh ? '时间' : 'Time'}</TableHead>
                      {scope !== 'current' && <TableHead>{zh ? '作用域 / 节点' : 'Scope / node'}</TableHead>}
                      <TableHead>{zh ? '操作' : 'Action'}</TableHead>
                      <TableHead>{zh ? '资源' : 'Resource'}</TableHead>
                      <TableHead className="w-28">{zh ? '结果' : 'Result'}</TableHead>
                      <TableHead className="w-40">IP</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {pagination.items.map((row) => (
                      <TableRow key={row.id}>
                        <TableCell className="text-muted-foreground tabular-nums">{formatDateTime(row.created_at)}</TableCell>
                        {scope !== 'current' && <TableCell><div>{row.scope === 'control_plane' ? (zh ? '控制平面' : 'Control plane') : (zh ? '节点' : 'Node')}</div>{row.node_id && <div className="text-xs text-muted-foreground">{row.node_name || row.node_id}</div>}</TableCell>}
                        <TableCell><div className="font-medium">{row.action}</div><div className="text-xs text-muted-foreground">{row.source || 'site'} · {row.user_id ? `${zh ? '用户' : 'user'} ${row.user_id}` : row.source === 'chat' ? (zh ? '访客' : 'guest') : '—'}</div></TableCell>
                        <TableCell className="max-w-md break-words"><div>{`${row.resource_type} · ${row.resource_type === 'container' ? displayDockerId(row.resource_name) : row.resource_name}`}</div>{(row.run_id || row.operation_id) && <Link to="/ai-operations" hash={new URLSearchParams(row.operation_id ? { operation: row.operation_id } : { run: row.run_id! }).toString()} className="mr-3 text-xs underline">{zh ? '查看 AI 记录' : 'View AI record'}</Link>}{row.task_id && <Link to="/tasks" hash={row.task_id} className="text-xs underline">{zh ? '查看任务' : 'View task'}</Link>}{(row.details || row.external_user_id) && <details className="mt-1 text-xs"><summary className="cursor-pointer text-muted-foreground">{zh ? '审计详情' : 'Audit details'}</summary><p className="mt-1 whitespace-pre-wrap break-all">{row.details}</p>{row.external_user_id && <p className="break-all text-muted-foreground">{row.external_user_id} · {row.chat_id}</p>}</details>}</TableCell>
                        <TableCell>
                          <StatusBadge tone={row.result === 'success' ? 'success' : row.result === 'failed' || row.result === 'denied' ? 'critical' : 'neutral'}>{row.result}</StatusBadge>
                        </TableCell>
                        <TableCell className="font-mono text-xs text-muted-foreground">{row.ip || '—'}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table></ListShell>
                <ListPagination {...pagination} zh={zh} />
              </>
            )}
    </ResourceFrame>
  )
}
