import { useDateTime } from '../lib/time-zone'
import { useQuery } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { GitPullRequest, Plus } from 'lucide-react'
import { Button } from '../components/ui/button'
import { ErrorState } from '../components/ui/error-state'
import { ListShell } from '../components/ui/list-shell'
import { ListPagination } from '../components/ui/list-pagination'
import { useListPagination } from '../components/ui/use-list-pagination'
import { LoadingState } from '../components/ui/loading-state'
import { StatusBadge } from '../components/ui/status-badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'
import type { DeliveryProject } from '../features/delivery/types'
import { shortCommit } from '../features/delivery/types'
import { api } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { ResourceFrame } from './images'


export function ContinuousDeliveryPage() {
  const { formatDateTime } = useDateTime()

  const navigate = useNavigate()
  const { language } = useI18n()
  const zh = language === 'zh-CN'
  const query = useQuery({ queryKey: ['delivery-projects'], queryFn: () => api<DeliveryProject[]>('/delivery-projects') })
  const rows = query.data ?? []
  const pagination = useListPagination(rows)
  const synchronized = rows.filter((project) => deliveryState(project) === 'synchronized').length
  const pending = rows.filter((project) => deliveryState(project) === 'pending').length
  const setup = rows.length - synchronized - pending
  return <ResourceFrame title={zh ? '持续交付' : 'Continuous Delivery'} detail={zh ? `${rows.length} 个交付项目` : `${rows.length} delivery projects`} action={<div className="flex flex-wrap items-center gap-2"><StatusBadge tone="success">{synchronized} {zh ? '已同步' : 'synced'}</StatusBadge><StatusBadge tone="warning">{pending} {zh ? '待对账' : 'pending'}</StatusBadge><StatusBadge tone="outline">{setup} {zh ? '待配置' : 'setup'}</StatusBadge><Button onClick={() => void navigate({ to: '/continuous-delivery/new/project' })}><Plus data-icon="inline-start" />{zh ? '新建项目' : 'New project'}</Button></div>}>
    {query.isPending ? <LoadingState compact rows={7} label={zh ? '正在加载持续交付项目' : 'Loading delivery projects'} /> : query.isError ? <ErrorState description={query.error.message} /> : rows.length === 0 ? <div className="flex w-full flex-col items-center gap-1.5 rounded-xl border border-dashed py-12 text-center"><GitPullRequest className="size-6 text-muted-foreground" /><p className="text-sm font-medium">{zh ? '还没有持续交付项目' : 'No delivery projects yet'}</p><p className="max-w-md text-xs text-muted-foreground">{zh ? '直接在这里创建项目并连接 Git 仓库。' : 'Create a project here and connect its Git repository.'}</p></div> : (
      <><ListShell><Table>
        <TableHeader>
          <TableRow>
            <TableHead className="min-w-[240px]">{zh ? '项目' : 'Project'}</TableHead>
            <TableHead className="w-[110px]">{zh ? '状态' : 'Status'}</TableHead>
            <TableHead className="w-[140px]">{zh ? '引用' : 'Reference'}</TableHead>
            <TableHead className="w-[100px]">{zh ? '期望版本' : 'Desired'}</TableHead>
            <TableHead className="w-[100px]">{zh ? '已观测' : 'Observed'}</TableHead>
            <TableHead className="w-[180px]">{zh ? '更新时间' : 'Updated'}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {pagination.items.map((project) => {
            const state = deliveryState(project)
            return <TableRow key={project.id}>
              <TableCell className="whitespace-normal">
                <Link to="/continuous-delivery/$projectName" params={{ projectName: project.name }} className="font-medium text-primary underline-offset-4 hover:underline">{project.name}</Link>
                <p className="mt-0.5 max-w-xs truncate text-xs text-muted-foreground">{project.configured ? project.repository_url || (zh ? '已连接 Git 仓库' : 'Git repository connected') : (zh ? '尚未连接 Git 仓库' : 'Git repository not configured')}</p>
              </TableCell>
              <TableCell><StateBadge state={state} zh={zh} /></TableCell>
              <TableCell className="text-muted-foreground">{project.git_ref || '—'}</TableCell>
              <TableCell className="font-mono text-xs">{shortCommit(project.desired_commit)}</TableCell>
              <TableCell className="font-mono text-xs">{shortCommit(project.observed_commit)}</TableCell>
              <TableCell className="text-sm text-muted-foreground">{formatDateTime(String(project.updated_at))}</TableCell>
            </TableRow>
          })}
        </TableBody>
      </Table></ListShell><ListPagination {...pagination} zh={zh} /></>
    )}
  </ResourceFrame>
}

function StateBadge({ state, zh }: { state: string; zh: boolean }) {
  if (state === 'synchronized') return <StatusBadge tone="success">{zh ? '已同步' : 'Synchronized'}</StatusBadge>
  if (state === 'pending') return <StatusBadge tone="warning">{zh ? '待对账' : 'Pending'}</StatusBadge>
  return <StatusBadge tone="outline">{zh ? '待配置' : 'Setup'}</StatusBadge>
}

function deliveryState(project: DeliveryProject) { return !project.configured ? 'setup' : project.desired_commit && project.desired_commit === project.observed_commit ? 'synchronized' : 'pending' }
