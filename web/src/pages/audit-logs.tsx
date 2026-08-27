import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { ChevronLeftIcon, ChevronRightIcon } from 'lucide-react'
import { cn } from '@/lib/utils'
import { LoadingState } from '../components/ui/loading-state'
import { Badge } from '../components/ui/badge'
import { Button } from '../components/ui/button'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'
import { api } from '../lib/api'
import { displayDockerId } from '../lib/docker-id'
import { useI18n } from '../lib/i18n'
import { useUIStore } from '../stores/ui'
import { ResourceFrame } from './images'

interface Audit { id: number; node_id: string; node_name?: string; user_id?: number; action: string; resource_type: string; resource_name: string; ip: string; result: string; created_at: string }

const PAGE_SIZE = 20

export function AuditLogsPage() {
  const nodeID = useUIStore((state) => state.currentNodeID)
  const { t, language } = useI18n()
  const zh = language === 'zh-CN'
  const [page, setPage] = useState(0)
  const query = useQuery({ queryKey: ['audit-logs', nodeID], queryFn: () => api<Audit[]>(`/audit-logs?node_id=${encodeURIComponent(nodeID)}`) })
  const rows = query.data ?? []
  const pageCount = Math.max(1, Math.ceil(rows.length / PAGE_SIZE))
  const current = Math.min(page, pageCount - 1)
  const visible = rows.slice(current * PAGE_SIZE, current * PAGE_SIZE + PAGE_SIZE)

  return (
    <ResourceFrame title={t('auditLogs')} detail={zh ? '当前 Docker 节点的重要操作' : 'Important actions on the current Docker node'}>
      {query.isPending
        ? <LoadingState compact label={zh ? '正在加载审计日志' : 'Loading audit logs'} />
        : rows.length === 0
          ? <p className="py-12 text-center text-sm text-muted-foreground">{zh ? '暂无审计记录' : 'No audit records'}</p>
          : (
              <>
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead className="w-44">{zh ? '时间' : 'Time'}</TableHead>
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
                        <TableCell className="font-medium">{row.action}</TableCell>
                        <TableCell>{`${row.resource_type} · ${row.resource_type === 'container' ? displayDockerId(row.resource_name) : row.resource_name}`}</TableCell>
                        <TableCell>
                          <Badge variant={row.result === 'success' ? 'secondary' : 'destructive'} className={cn(row.result === 'success' && 'bg-emerald-500/10 text-emerald-700 hover:bg-emerald-500/20 dark:bg-emerald-500/15 dark:text-emerald-400')}>{row.result}</Badge>
                        </TableCell>
                        <TableCell className="font-mono text-xs text-muted-foreground">{row.ip || '—'}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
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
