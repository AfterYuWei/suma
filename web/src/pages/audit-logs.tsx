import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { ChevronLeftIcon, ChevronRightIcon } from 'lucide-react'
import { LoadingState } from '../components/ui/loading-state'
import { Button } from '../components/ui/button'
import { ListShell } from '../components/ui/list-shell'
import { StatusBadge } from '../components/ui/status-badge'
import { Tabs, TabsList, TabsTrigger } from '../components/ui/tabs'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'
import { api } from '../lib/api'
import { displayDockerId } from '../lib/docker-id'
import { useI18n } from '../lib/i18n'
import { nodePath } from '../lib/nodes'
import { useUIStore } from '../stores/ui'
import { ResourceFrame } from './images'

interface Audit { id: number; scope: 'control_plane' | 'node'; node_id?: string; node_name?: string; user_id?: number; action: string; resource_type: string; resource_name: string; ip: string; result: string; created_at: string }

const PAGE_SIZE = 20

export function AuditLogsPage() {
  const nodeID = useUIStore((state) => state.currentNodeID)
  const { t, language } = useI18n()
  const zh = language === 'zh-CN'
  const [page, setPage] = useState(0)
  const [scope, setScope] = useState<'current' | 'control_plane' | 'all'>('current')
  const query = useQuery({ queryKey: ['audit-logs', scope, nodeID], queryFn: () => api<Audit[]>(scope === 'current' ? nodePath(nodeID, '/audit-logs') : `/audit-logs?scope=${scope}`) })
  const rows = query.data ?? []
  const pageCount = Math.max(1, Math.ceil(rows.length / PAGE_SIZE))
  const current = Math.min(page, pageCount - 1)
  const visible = rows.slice(current * PAGE_SIZE, current * PAGE_SIZE + PAGE_SIZE)

  return (
    <ResourceFrame title={t('auditLogs')} detail={zh ? '节点与控制平面的重要操作' : 'Important node and control-plane actions'} action={<Tabs value={scope} onValueChange={(value) => { setScope(value as typeof scope); setPage(0) }}><TabsList><TabsTrigger value="current">{zh ? '当前节点' : 'Current node'}</TabsTrigger><TabsTrigger value="control_plane">{zh ? '控制平面' : 'Control plane'}</TabsTrigger><TabsTrigger value="all">{zh ? '全部' : 'All'}</TabsTrigger></TabsList></Tabs>}>
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
                    {visible.map((row) => (
                      <TableRow key={row.id}>
                        <TableCell className="text-muted-foreground tabular-nums">{new Date(row.created_at).toLocaleString(language)}</TableCell>
                        {scope !== 'current' && <TableCell><div>{row.scope === 'control_plane' ? (zh ? '控制平面' : 'Control plane') : (zh ? '节点' : 'Node')}</div>{row.node_id && <div className="text-xs text-muted-foreground">{row.node_name || row.node_id}</div>}</TableCell>}
                        <TableCell className="font-medium">{row.action}</TableCell>
                        <TableCell>{`${row.resource_type} · ${row.resource_type === 'container' ? displayDockerId(row.resource_name) : row.resource_name}`}</TableCell>
                        <TableCell>
                          <StatusBadge tone={row.result === 'success' ? 'success' : 'critical'}>{row.result}</StatusBadge>
                        </TableCell>
                        <TableCell className="font-mono text-xs text-muted-foreground">{row.ip || '—'}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table></ListShell>
                <nav aria-label="pagination" className="flex items-center justify-between pt-3 text-sm text-muted-foreground">
                  <span className="tabular-nums">{zh ? `共 ${rows.length} 条 · 第 ${current + 1}/${pageCount} 页` : `${rows.length} records · page ${current + 1}/${pageCount}`}</span>
                  <span className="flex items-center gap-1">
                    <Button variant="outline" size="icon-sm" disabled={current === 0} aria-label={zh ? '上一页' : 'Previous page'} onClick={() => setPage(Math.max(0, current - 1))}><ChevronLeftIcon /></Button>
                    <Button variant="outline" size="icon-sm" disabled={current >= pageCount - 1} aria-label={zh ? '下一页' : 'Next page'} onClick={() => setPage(current + 1)}><ChevronRightIcon /></Button>
                  </span>
                </nav>
              </>
            )}
    </ResourceFrame>
  )
}
