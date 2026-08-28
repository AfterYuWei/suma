import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { ChevronRight, Download, Eye, FileText, Pencil, Play, Plus, Power, RefreshCw, Square, SquareTerminal } from 'lucide-react'
import { type ReactElement, type ReactNode, useState } from 'react'
import { Badge } from '../components/ui/badge'
import { Button } from '../components/ui/button'
import { Checkbox } from '../components/ui/checkbox'
import { ErrorState } from '../components/ui/error-state'
import { ListShell } from '../components/ui/list-shell'
import { LoadingState } from '../components/ui/loading-state'
import { Spinner } from '../components/ui/spinner'
import { StatusBadge } from '../components/ui/status-badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'
import { TooltipHint } from '../components/ui/tooltip-hint'
import { TakeoverWarningDialog } from '../features/compose/takeover-warning-dialog'
import type { Project, ProjectSummary } from '../features/compose/types'
import type { ContainerSummary } from '../features/containers/types'
import { api } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { nodePath } from '../lib/nodes'
import { confirmDialog, promptDialog } from '../stores/dialog'
import { useUIStore } from '../stores/ui'
import { ResourceFrame } from './images'

const starter = `services:\n  app:\n    image: nginx:alpine\n    ports:\n      - "8080:80"\n`
const projectTone = (status: string) => status === 'running' ? 'success' : status === 'degraded' ? 'warning' : 'neutral'
const containerTone = (state: string) => state === 'running' ? 'success' : state === 'paused' || state === 'restarting' ? 'warning' : 'neutral'
interface ProjectTask { id: string }

export function ComposePage() {
  const nodeID = useUIStore((state) => state.currentNodeID)
  const client = useQueryClient()
  const navigate = useNavigate()
  const [selected, setSelected] = useState<Set<string>>(() => new Set())
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set())
  const [takeoverName, setTakeoverName] = useState('')
  const { t, language } = useI18n()
  const zh = language === 'zh-CN'
  const query = useQuery({ queryKey: ['projects', nodeID], queryFn: () => api<ProjectSummary[]>(nodePath(nodeID, '/projects')), refetchInterval: 5_000 })
  const create = useMutation({
    mutationFn: (name: string) => api<Project>(nodePath(nodeID, '/projects'), { method: 'POST', body: JSON.stringify({ backend: 'compose', name, compose: starter, environment: '' }) }),
    onSuccess: async () => { await client.invalidateQueries({ queryKey: ['projects', nodeID] }) },
  })
  const batch = useMutation({
    mutationFn: ({ names, action }: { names: string[]; action: string }) => api(nodePath(nodeID, '/projects/batch'), { method: 'POST', body: JSON.stringify({ backend: 'compose', names, action }) }),
    onSuccess: async () => { setSelected(new Set()); await Promise.all([client.invalidateQueries({ queryKey: ['projects', nodeID] }), client.invalidateQueries({ queryKey: ['tasks', nodeID] })]) },
  })
  const rows = query.data ?? []
  const manageable = rows.filter((row) => row.managed)
  const selectedRows = manageable.filter((row) => selected.has(row.name))
  const allSelected = manageable.length > 0 && selectedRows.length === manageable.length
  const add = async () => {
    const name = await promptDialog({ title: t('newProject'), description: zh ? '创建一个使用 Docker Compose 后端的 SUMA Project。' : 'Create a SUMA Project using the Docker Compose backend.', confirmLabel: t('create'), input: { label: t('projectName') } })
    if (name) create.mutate(name)
  }
  const runBatch = async (action: string) => {
    if (!selectedRows.length) return
    if (action === 'down' && !await confirmDialog({ title: zh ? `Down ${selectedRows.length} 个项目？` : `Down ${selectedRows.length} projects?`, description: zh ? '这会移除项目容器和网络，但保留托管文件和命名卷。' : 'This removes project containers and networks while preserving managed files and named volumes.', confirmLabel: 'Down', danger: true })) return
    batch.mutate({ names: selectedRows.map((row) => row.name), action })
  }
  const toggleAll = (checked: boolean) => setSelected(checked ? new Set(manageable.map((row) => row.name)) : new Set())
  const toggleRow = (name: string, checked: boolean) => setSelected((current) => { const next = new Set(current); if (checked) next.add(name); else next.delete(name); return next })
  const toggleExpanded = (name: string) => setExpanded((current) => { const next = new Set(current); if (next.has(name)) next.delete(name); else next.add(name); return next })
  const running = rows.filter((row) => row.status === 'running').length
  const toolbar = <div className="flex flex-wrap items-center gap-2"><StatusBadge tone="success">{running} {zh ? '运行中' : 'running'}</StatusBadge><StatusBadge tone="neutral">{rows.length - running} {zh ? '其他' : 'other'}</StatusBadge><Button onClick={() => void add()} disabled={create.isPending}>{create.isPending ? <Spinner /> : <Plus />}{t('newProject')}</Button></div>

  return <ResourceFrame title={zh ? '项目' : 'Projects'} detail={zh ? `${rows.length} 个 Project` : `${rows.length} Projects`} action={toolbar}>
    <div className="flex w-full flex-col gap-4">
      {!!selected.size && <div className="flex flex-wrap items-center gap-2"><span className="text-sm font-medium">{selected.size} {zh ? '已选' : 'selected'}</span>{[['start', Play], ['stop', Square], ['restart', RefreshCw], ['update', Download], ['down', Power]].map(([name, Icon]) => <Button key={String(name)} variant={name === 'down' ? 'destructive' : 'outline'} size="sm" disabled={batch.isPending} onClick={() => void runBatch(String(name))}><Icon />{actionLabel(String(name), zh)}</Button>)}</div>}
      {batch.isError && <ErrorState description={batch.error.message} />}
      {create.isError && <ErrorState description={create.error.message} />}
      {query.isPending ? <LoadingState compact rows={7} label={zh ? '正在加载项目' : 'Loading Projects'} /> : query.isError ? <ErrorState description={query.error.message} /> : rows.length === 0 ? <div className="rounded-xl bg-card px-4 py-10 text-center ring-1 ring-foreground/10"><p className="text-sm font-medium">{zh ? '未发现 Project' : 'No Projects discovered'}</p><p className="text-sm text-muted-foreground">{zh ? 'Compose Project 会根据 Docker 标签自动聚合显示。' : 'Compose Projects are aggregated automatically from Docker labels.'}</p></div> : <ListShell><Table>
        <TableHeader><TableRow><TableHead className="w-9 pr-0"><Checkbox checked={allSelected} indeterminate={!allSelected && selected.size > 0} onCheckedChange={(value) => toggleAll(value === true)} aria-label={zh ? '全选托管项目' : 'Select managed Projects'} /></TableHead><TableHead className="w-9" /><TableHead>{zh ? '项目' : 'Project'}</TableHead><TableHead className="w-24">Backend</TableHead><TableHead className="w-28">{zh ? '状态' : 'Status'}</TableHead><TableHead className="w-40">{zh ? '运行资源' : 'Runtime'}</TableHead><TableHead className="w-56">{zh ? '操作' : 'Actions'}</TableHead></TableRow></TableHeader>
        <TableBody>{rows.flatMap((row) => {
          const open = expanded.has(row.name)
          return [<TableRow key={row.name}><TableCell className="pr-0"><Checkbox disabled={!row.managed} checked={selected.has(row.name)} onCheckedChange={(value) => toggleRow(row.name, value === true)} aria-label={row.managed ? row.name : (zh ? '外部项目不可批量操作' : 'External Project cannot be batch operated')} /></TableCell><TableCell><Button variant="ghost" size="icon-xs" onClick={() => toggleExpanded(row.name)} aria-label={zh ? '展开服务' : 'Expand services'}><ChevronRight className={open ? 'rotate-90 transition-transform' : 'transition-transform'} /></Button></TableCell><TableCell><div className="flex flex-col gap-1"><div className="flex items-center gap-2"><Link to="/projects/$backend/$projectName" params={{ backend: row.backend, projectName: row.name }} className="font-medium hover:underline">{row.name}</Link><StatusBadge tone={row.managed ? 'outline' : 'neutral'}>{row.managed ? (zh ? '托管' : 'Managed') : (zh ? '外部' : 'External')}</StatusBadge></div><span className="max-w-72 truncate text-xs text-muted-foreground">{row.source === 'managed' ? (zh ? 'SUMA 托管配置' : 'SUMA managed configuration') : (zh ? 'Docker 运行态发现' : 'Discovered from Docker runtime')} · {row.scope.id}</span></div></TableCell><TableCell><Badge variant="outline">{row.backend === 'compose' ? 'Compose' : 'Swarm'}</Badge></TableCell><TableCell><StatusBadge tone={projectTone(row.status)}>{row.status}</StatusBadge></TableCell><TableCell>{row.service_count} {zh ? '服务' : 'services'} · <span className="text-muted-foreground">{row.instance_count} {zh ? '实例' : 'instances'}</span></TableCell><TableCell><ProjectActions row={row} zh={zh} onTakeover={() => setTakeoverName(row.name)} /></TableCell></TableRow>, ...(open ? [<TableRow key={`${row.name}-services`}><TableCell colSpan={7} className="bg-muted/30 p-4"><ProjectServices project={row} zh={zh} /></TableCell></TableRow>] : [])]
        })}</TableBody>
      </Table></ListShell>}
    </div>
    <TakeoverWarningDialog open={!!takeoverName} projectName={takeoverName} zh={zh} onOpenChange={(open) => { if (!open) setTakeoverName('') }} onContinue={() => { const name = takeoverName; setTakeoverName(''); void navigate({ to: '/projects/$backend/$projectName/takeover', params: { backend: 'compose', projectName: name } }) }} />
  </ResourceFrame>
}

function actionLabel(action: string, zh: boolean) {
  const values: Record<string, [string, string]> = { start: ['启动', 'Start'], stop: ['停止', 'Stop'], restart: ['重启', 'Restart'], update: ['更新', 'Update'], down: ['Down', 'Down'] }
  return values[action]?.[zh ? 0 : 1] ?? action
}

function ProjectActions({ row, zh, onTakeover }: { row: ProjectSummary; zh: boolean; onTakeover: () => void }) {
  const nodeID = useUIStore((state) => state.currentNodeID)
  const client = useQueryClient()
  const action = useMutation({ mutationFn: (name: string) => api<ProjectTask>(nodePath(nodeID, `/projects/compose/${encodeURIComponent(row.name)}/actions/${name}`), { method: 'POST' }), onSuccess: () => { void client.invalidateQueries({ queryKey: ['projects', nodeID] }); void client.invalidateQueries({ queryKey: ['tasks', nodeID] }) } })
  const run = async (name: string) => {
    if (name === 'down' && !await confirmDialog({ title: `Down ${row.name}?`, description: zh ? '将移除项目容器和网络。' : 'Project containers and networks will be removed.', confirmLabel: 'Down', danger: true })) return
    action.mutate(name)
  }
  if (!row.managed) return <div className="flex items-center"><ActionIcon label={zh ? '查看项目' : 'View Project'}><Link to="/projects/$backend/$projectName" params={{ backend: row.backend, projectName: row.name }}><Eye /></Link></ActionIcon><ActionIcon label={zh ? '接管项目' : 'Take over Project'} onClick={onTakeover}><Download /></ActionIcon></div>
  const supported = (capability: string) => row.capabilities.includes(capability as never)
  return <div className="flex items-center"><ActionIcon label={zh ? '编辑项目' : 'Edit Project'}><Link to="/projects/$backend/$projectName" params={{ backend: row.backend, projectName: row.name }}><Pencil /></Link></ActionIcon>{[['start', Play], ['stop', Square], ['restart', RefreshCw], ['update', Download], ['down', Power]].map(([name, Icon]) => supported(String(name) === 'down' ? 'delete' : String(name)) && <ActionIcon key={String(name)} label={actionLabel(String(name), zh)} destructive={name === 'down'} disabled={action.isPending} onClick={() => void run(String(name))}>{action.isPending && action.variables === name ? <Spinner /> : <Icon />}</ActionIcon>)}</div>
}

function ActionIcon({ label, destructive, disabled, onClick, children }: { label: string; destructive?: boolean; disabled?: boolean; onClick?: () => void; children: ReactNode }) {
  return <TooltipHint content={label}><Button variant="ghost" size="icon-sm" className={destructive ? 'text-destructive' : undefined} disabled={disabled} onClick={onClick} aria-label={label} render={!onClick && typeof children === 'object' ? children as ReactElement : undefined}>{onClick ? children : null}</Button></TooltipHint>
}

function ProjectServices({ project, zh }: { project: ProjectSummary; zh: boolean }) {
  const nodeID = useUIStore((state) => state.currentNodeID)
  const query = useQuery({ queryKey: ['project-services', nodeID, project.name], queryFn: () => api<ContainerSummary[]>(nodePath(nodeID, `/projects/compose/${encodeURIComponent(project.name)}/services`)), refetchInterval: 5_000 })
  if (query.isPending) return <LoadingState embedded compact rows={2} label={zh ? '正在加载实例' : 'Loading instances'} />
  if (query.isError) return <ErrorState description={query.error.message} />
  return <Table><TableHeader><TableRow><TableHead>{zh ? '服务' : 'Service'}</TableHead><TableHead>{zh ? '容器实例' : 'Container instance'}</TableHead><TableHead>{zh ? '镜像' : 'Image'}</TableHead><TableHead>{zh ? '状态' : 'State'}</TableHead><TableHead className="w-28">{zh ? '操作' : 'Actions'}</TableHead></TableRow></TableHeader><TableBody>{(query.data ?? []).map((row) => <TableRow key={row.id}><TableCell>{row.labels['com.docker.compose.service'] || row.name}</TableCell><TableCell className="text-muted-foreground">{row.name}</TableCell><TableCell className="max-w-72 truncate">{row.image}</TableCell><TableCell><StatusBadge tone={containerTone(row.state)}>{row.state}</StatusBadge></TableCell><TableCell><div className="flex"><ActionIcon label={zh ? '日志' : 'Logs'} onClick={() => location.assign(`/containers/${row.id}#logs`)}><FileText /></ActionIcon><ActionIcon label={zh ? '终端' : 'Terminal'} disabled={row.state !== 'running'} onClick={() => location.assign(`/containers/${row.id}#terminal`)}><SquareTerminal /></ActionIcon></div></TableCell></TableRow>)}</TableBody></Table>
}
