import {Tabs, TabsList, TabsTrigger} from '../components/ui/tabs'
import {DeliveryHistory} from '../features/delivery/history'
import { useDateTime } from '../lib/time-zone'
import { useQuery } from '@tanstack/react-query'
import { Link, useNavigate, useSearch } from '@tanstack/react-router'
import { GitPullRequest, Plus } from 'lucide-react'
import { Button } from '../components/ui/button'
import { ErrorState } from '../components/ui/error-state'
import { ListShell } from '../components/ui/list-shell'
import { ListPagination } from '../components/ui/list-pagination'
import { useListPagination } from '../components/ui/use-list-pagination'
import { LoadingState } from '../components/ui/loading-state'
import { StatusBadge } from '../components/ui/status-badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'
import type { CDDrift, DeliveryProject, DeliveryRecordPage } from '../features/delivery/types'
import { DeliveryAutomationSwitch } from '../features/delivery/automation-switch'
import { shortCommit } from '../features/delivery/types'
import { api } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { ResourceFrame } from './images'


export function ContinuousDeliveryPage() {
  const { formatDateTime } = useDateTime()

  const navigate = useNavigate()
  const {view, task} = useSearch({from: '/continuous-delivery'})
  const records = view === 'records'
  const { language } = useI18n()
  const zh = language === 'zh-CN'
  const query = useQuery({ queryKey: ['delivery-projects'], queryFn: () => api<DeliveryProject[]>('/delivery-projects'), enabled: !records })
  const rows = query.data ?? []
  const pagination = useListPagination(rows)
  return <ResourceFrame title={zh ? '持续交付' : 'Continuous Delivery'} detail={records ? (zh ? '批量交付与节点项目 CD 的统一记录' : 'Batch delivery and node project CD history') : zh ? `${rows.length} 个交付项目` : `${rows.length} delivery projects`} action={<div className="flex flex-wrap items-center gap-2"><Button onClick={() => void navigate({ to: '/continuous-delivery/new/project' })}><Plus data-icon="inline-start" />{zh ? '新建项目' : 'New project'}</Button></div>}>
    <Tabs value={records ? 'records' : 'projects'} onValueChange={value => void navigate({to:'/continuous-delivery',search:{view:value === 'records' ? 'records' : undefined},replace:true})}><TabsList><TabsTrigger value="projects">{zh ? '项目' : 'Projects'}</TabsTrigger><TabsTrigger value="records">{zh ? '记录' : 'Records'}</TabsTrigger></TabsList></Tabs>
    {records ? <DeliveryHistory key={task ?? 'all'} zh={zh} taskID={task}/> : query.isPending ? <LoadingState compact rows={7} label={zh ? '正在加载持续交付项目' : 'Loading delivery projects'} /> : query.isError ? <ErrorState description={query.error.message} /> : rows.length === 0 ? <div className="flex w-full flex-col items-center gap-1.5 rounded-lg bg-surface py-12 text-center"><GitPullRequest className="size-6 text-muted-foreground" /><p className="text-sm font-medium">{zh ? '还没有持续交付项目' : 'No delivery projects yet'}</p><p className="max-w-md text-xs text-muted-foreground">{zh ? '直接在这里创建项目并连接 Git 仓库。' : 'Create a project here and connect its Git repository.'}</p></div> : (
      <><ListShell><Table>
        <TableHeader>
          <TableRow>
            <TableHead className="min-w-[240px]">{zh ? '项目' : 'Project'}</TableHead>
            <TableHead className="w-[220px]">{zh ? 'Docker 运行' : 'Docker runtime'}</TableHead>
            <TableHead className="w-[200px]">{zh ? '最近发布' : 'Latest publication'}</TableHead>
            <TableHead className="w-[130px]">{zh ? '自动检查' : 'Automatic checks'}</TableHead>
            <TableHead className="w-[180px]">{zh ? '更新时间' : 'Updated'}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {pagination.items.map(project => <DeliveryProjectRow key={project.id} project={project} zh={zh} updated={formatDateTime(String(project.updated_at))} />)}
        </TableBody>
      </Table></ListShell><ListPagination {...pagination} zh={zh} /></>
    )}
  </ResourceFrame>
}

function DeliveryProjectRow({project, zh, updated}: {project: DeliveryProject; zh: boolean; updated: string}) {
  const base = `/delivery-projects/${encodeURIComponent(project.name)}`
  const drift = useQuery({queryKey: ['delivery-drift', project.name], queryFn: () => api<CDDrift>(`${base}/drift`), enabled: project.configured, refetchInterval: 10000})
  const records = useQuery({queryKey: ['delivery-records-list', project.name], queryFn: () => api<DeliveryRecordPage>(`${base}/records?page=1&page_size=1`), enabled: project.configured, refetchInterval: 5000})
  const latest = records.data?.items[0]
  const known = drift.data?.nodes.filter(node => node.runtime_available === true).length ?? 0
  const healthy = drift.data?.nodes.filter(node => node.runtime_available === true && node.runtime_healthy).length ?? 0
  return <TableRow>
    <TableCell className="whitespace-normal"><Link to="/continuous-delivery/$projectName" params={{projectName: project.name}} className="font-medium underline-offset-4 hover:underline">{project.name}</Link><p className="mt-1 max-w-xs truncate text-xs text-muted-foreground">{project.git_ref || (zh ? '尚未连接 Git' : 'Git not configured')}</p></TableCell>
    <TableCell className="whitespace-normal text-xs">{!project.configured ? (zh ? '待配置' : 'Setup required') : drift.isError ? (zh ? '无法读取运行状态' : 'Runtime unavailable') : !drift.data ? (zh ? '读取中' : 'Loading') : <><span>{zh ? `${healthy}/${drift.data.nodes.length} 节点容器运行正常` : `${healthy}/${drift.data.nodes.length} nodes running`}</span>{known < drift.data.nodes.length && <p className="mt-1 text-muted-foreground">{zh ? `${drift.data.nodes.length - known} 节点状态未知` : `${drift.data.nodes.length - known} node states unknown`}</p>}</>}</TableCell>
    <TableCell className="whitespace-normal text-xs">{records.isError ? (zh ? '无法读取发布记录' : 'Publication unavailable') : latest ? <><StatusBadge tone={latest.status === 'succeeded' ? 'success' : ['failed', 'partial_failed', 'interrupted'].includes(latest.status) ? 'critical' : 'neutral'}>#{latest.id} · {latest.status === 'succeeded' ? (zh ? '成功' : 'Succeeded') : ['failed', 'partial_failed', 'interrupted'].includes(latest.status) ? (zh ? '需要处理' : 'Needs attention') : (zh ? '执行中' : 'In progress')}</StatusBadge><p className="mt-1 font-mono text-muted-foreground">{shortCommit(latest.commit_sha)}</p></> : (zh ? '尚未发布' : 'No publications')}</TableCell>
    <TableCell><DeliveryAutomationSwitch projectName={project.name} enabled={project.automation_enabled !== false} version={project.automation_version} zh={zh} /></TableCell>
    <TableCell className="text-xs text-muted-foreground">{updated}</TableCell>
  </TableRow>
}
