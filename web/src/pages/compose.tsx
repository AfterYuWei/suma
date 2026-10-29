import { useContainerWorkspace } from '../features/containers/use-container-workspace'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { ChevronDown, ChevronRight, Download, Eye, MoreHorizontal, Pencil, Play, Plus, PowerOff, RefreshCw, Search, Square, Trash2, X } from 'lucide-react'
import { Fragment, type ReactElement, type ReactNode, useRef, useState } from 'react'
import { InlineStatus } from '../components/ui/inline-status'
import { Button } from '../components/ui/button'
import { Checkbox } from '../components/ui/checkbox'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '../components/ui/dropdown-menu'
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupInput } from '../components/ui/input-group'
import { ErrorState } from '../components/ui/error-state'
import { ListPagination } from '../components/ui/list-pagination'
import { useListPagination } from '../components/ui/use-list-pagination'
import { LoadingState } from '../components/ui/loading-state'
import { Spinner } from '../components/ui/spinner'
import { StatusBadge } from '../components/ui/status-badge'
import { TooltipHint } from '../components/ui/tooltip-hint'
import { ProjectServices, ProjectResourceSummary } from '../components/docker/project-services'
import { ContainerWorkspaceDialog } from '../components/docker/container-workspace-dialog'
import { confirmExternalProjectCleanup } from '../features/compose/external-project-cleanup'
import { confirmManagedProjectRemoval } from '../features/compose/managed-project-removal'
import { TakeoverWarningDialog } from '../features/compose/takeover-warning-dialog'
import type { ProjectCapability, ProjectSummary } from '../features/compose/types'
import { projectDisplayName } from '../features/compose/types'
import { aggregate, defaultProjectView, groupServices, projectStatus, selectProjects, type ProjectView } from '../features/compose/observation'
import { useProjectObservation } from '../features/compose/use-project-observation'
import type { ContainerSummary } from '../features/containers/types'
import { useFreshness } from '../features/containers/use-freshness'
import { api } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { nodePath } from '../lib/nodes'
import { confirmDialog } from '../stores/dialog'
import { useUIStore } from '../stores/ui'
import { useProjectViewStore } from '../stores/project-view'
import { ResourceFrame } from './images'

const statusLabel = (status: string, zh: boolean) => ({ running: zh ? '运行中' : 'Running', degraded: zh ? '部分运行' : 'Partially running', stopped: zh ? '已停止' : 'Stopped', paused: zh ? '已暂停' : 'Paused', not_created: zh ? '未创建' : 'Not created' }[status] ?? status)
interface ProjectTask { id: string; status: string; message: string }
type ProjectFeedback =
  | { kind: 'pending'; projectName: string; action: string }
  | { kind: 'task'; projectName: string; action: string; taskID: string }
  | { kind: 'success'; projectName: string; action: string }
  | { kind: 'error'; projectName: string; action: string; message: string }

export function ComposePage() {
  const nodeID = useUIStore(state => state.currentNodeID)
  return <ComposeNodePage key={nodeID} nodeID={nodeID} />
}
function ComposeNodePage({ nodeID }: { nodeID: string }) {
  const client = useQueryClient(), navigate = useNavigate()
  const [selected, setSelected] = useState<Set<string>>(() => new Set())
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set())
  const [takeoverName, setTakeoverName] = useState('')
  const [feedback, setFeedback] = useState<ProjectFeedback | null>(null)
  const [menuOpen, setMenuOpen] = useState(false)
  const [serviceOverlay, setServiceOverlay] = useState(false)
  const { t, language } = useI18n(), zh = language === 'zh-CN'
  const workspace = useContainerWorkspace(nodeID, zh)
  const view = useProjectViewStore(state => state.views[nodeID]) ?? defaultProjectView
  const updateView = useProjectViewStore(state => state.update)
  const order = useRef<string[]>([])
  const update = (patch: Partial<ProjectView>) => { updateView(nodeID, patch); setSelected(new Set()); order.current = [] }
  const query = useQuery({ queryKey: ['projects', nodeID], queryFn: ({ signal }) => api<ProjectSummary[]>(nodePath(nodeID, '/projects'), { signal }), refetchInterval: 5_000 })
  const containers = useQuery({ queryKey: ['containers', nodeID], queryFn: ({ signal }) => api<ContainerSummary[]>(nodePath(nodeID, '/containers'), { signal }), refetchInterval: 10_000, retry: false })
  const fresh = useFreshness(containers.dataUpdatedAt)
  const available = !!containers.data && !containers.isError && fresh
  const observedRows = available ? containers.data! : []
  const observation = useProjectObservation(nodeID, observedRows)
  const rows = query.data ?? []
  const byProject = new Map<string, ContainerSummary[]>()
  for (const container of observedRows) {
    const name = container.labels['com.docker.compose.project']
    if (name) byProject.set(name, [...(byProject.get(name) ?? []), container])
  }
  const entries = rows.map(project => { const instances = byProject.get(project.name) ?? []; return { project, containers: instances, summary: aggregate(instances, observation), available } })
  const sorted = selectProjects(entries, view)
  if (!(selected.size || menuOpen || serviceOverlay || workspace.active || takeoverName) || !order.current.length) order.current = sorted.map(entry => entry.project.name)
  const ranks = new Map(order.current.map((name, index) => [name, index]))
  const visible = [...sorted].sort((a, b) => (ranks.get(a.project.name) ?? Infinity) - (ranks.get(b.project.name) ?? Infinity))
  const pagination = useListPagination(visible, `${nodeID}:${JSON.stringify(view)}`)
  const selectedRows = rows.filter(row => row.managed && selected.has(row.name))
  const pageManageable = pagination.items.filter(entry => entry.project.managed)
  const allSelected = pageManageable.length > 0 && pageManageable.every(entry => selected.has(entry.project.name))
  const someSelected = !allSelected && pageManageable.some(entry => selected.has(entry.project.name))
  const trackedTasks = useQuery({ queryKey: ['tasks', 'current', nodeID], queryFn: () => api<ProjectTask[]>(nodePath(nodeID, '/tasks')), enabled: feedback?.kind === 'task', refetchInterval: current => {
    if (feedback?.kind !== 'task') return false
    const task = current.state.data?.find(row => row.id === feedback.taskID)
    return !task || task.status === 'pending' || task.status === 'running' ? 1000 : false
  } })
  const batch = useMutation({ mutationFn: ({ names, action }: { names: string[]; action: string }) => api(nodePath(nodeID, '/projects/batch'), { method: 'POST', body: JSON.stringify({ backend: 'compose', names, action }) }), onSuccess: async () => { setSelected(new Set()); await Promise.all(['projects', 'containers', 'container-runtime', 'container-metrics', 'tasks'].map(key => client.invalidateQueries({ queryKey: [key, nodeID] }))) } })
  const runBatch = async (action: string) => {
    if (!selectedRows.length) return
    if (action === 'down' && !await confirmDialog({ title: zh ? `Down ${selectedRows.length} 个项目？` : `Down ${selectedRows.length} projects?`, description: zh ? '移除项目容器和网络，保留托管文件和命名卷。' : 'Remove project containers and networks; retain managed files and named volumes.', confirmLabel: 'Down', danger: true })) return
    batch.mutate({ names: selectedRows.map(row => row.name), action })
  }
  const toggleRow = (name: string, checked: boolean) => setSelected(old => { const next = new Set(old); if (checked) next.add(name); else next.delete(name); return next })
  const toggleExpanded = (name: string) => setExpanded(old => { const next = new Set(old); if (next.has(name)) next.delete(name); else next.add(name); return next })
  const toggleAll = (checked: boolean) => setSelected(old => { const next = new Set(old); for (const entry of pageManageable) { if (checked) next.add(entry.project.name); else next.delete(entry.project.name) } return next })
  const trackedTask = feedback?.kind === 'task' ? trackedTasks.data?.find(row => row.id === feedback.taskID) : undefined
  const feedbackView = projectFeedbackView(feedback, trackedTask, zh)
  const menu = (key: 'status' | 'source', label: string, options: [string, string][]) => <DropdownMenu><DropdownMenuTrigger render={<Button size="sm" variant={view[key] ? 'secondary' : 'ghost'}>{options.find(([value]) => value === view[key])?.[1] || label}<ChevronDown /></Button>} /><DropdownMenuContent>{options.map(([value, text]) => <DropdownMenuItem key={value} onClick={() => update({ [key]: value })}>{text}</DropdownMenuItem>)}</DropdownMenuContent></DropdownMenu>
  const toolbar = <div className="flex flex-wrap items-center gap-2">
    {menu('status', zh ? '状态' : 'Status', [['', zh ? '全部状态' : 'All states'], ...['running', 'degraded', 'paused', 'stopped', 'not_created'].map(value => [value, statusLabel(value, zh)] as [string, string])])}
    {menu('source', zh ? '来源' : 'Source', [['', zh ? '全部来源' : 'All sources'], ['managed', zh ? '托管' : 'Managed'], ['external', zh ? '外部' : 'External']])}
    <Button size="sm" variant={view.abnormal ? 'secondary' : 'ghost'} aria-pressed={view.abnormal} onClick={() => update({ abnormal: !view.abnormal })}>{zh ? '异常' : 'Abnormal'} · {entries.filter(entry => entry.summary.abnormal > 0).length}</Button>
    <DropdownMenu><DropdownMenuTrigger render={<Button variant="ghost" size="sm">{zh ? '排序' : 'Sort'}<ChevronDown /></Button>} /><DropdownMenuContent>{([['abnormal', zh ? '异常优先' : 'Abnormal first'], ['name', zh ? '名称' : 'Name'], ['cpu', 'CPU'], ['memory', zh ? '内存' : 'Memory']] as const).map(([value, label]) => <DropdownMenuItem key={value} onClick={() => update({ sort: value, descending: value === view.sort ? !view.descending : value === 'cpu' || value === 'memory' })}>{label}{view.sort === value ? view.descending ? ' ↓' : ' ↑' : ''}</DropdownMenuItem>)}</DropdownMenuContent></DropdownMenu>
    {(view.search || view.status || view.source || view.abnormal) && <Button size="sm" variant="ghost" className="text-destructive hover:text-destructive" onClick={() => update({ ...defaultProjectView, sort: view.sort, descending: view.descending })}>{zh ? '清除筛选' : 'Clear filters'}<X data-icon="inline-end" aria-hidden="true" /></Button>}
    <InputGroup className="ml-auto w-[260px]">
      <InputGroupAddon align="inline-start"><Search /></InputGroupAddon>
      <InputGroupInput value={view.search} onChange={event => update({ search: event.target.value })} aria-label={zh ? '搜索项目、服务或镜像' : 'Search projects, services or images'} placeholder={zh ? '项目、服务、镜像…' : 'Name, service, image…'} />
      {!!view.search && <InputGroupAddon align="inline-end"><InputGroupButton size="icon-xs" aria-label={zh ? '清空筛选' : 'Clear filter'} onClick={() => update({ search: '' })}><X /></InputGroupButton></InputGroupAddon>}
    </InputGroup>
  </div>
  const grid = 'grid grid-cols-[3.5rem_minmax(0,1fr)_minmax(5.5rem,auto)] items-center gap-x-3 gap-y-2 lg:grid-cols-[3.5rem_minmax(0,2fr)_minmax(8rem,1fr)_minmax(7rem,0.9fr)_7rem]'
  return <ResourceFrame compact title={zh ? '项目' : 'Projects'} detail={zh ? `${visible.length} / ${rows.length} 个项目` : `${visible.length} / ${rows.length} projects`} action={toolbar}>
    <div className="flex w-full min-w-0 flex-col gap-3">
      <div className="flex min-w-0 flex-wrap items-center justify-end gap-3">
        {feedbackView && <InlineStatus className="mr-auto flex-1" tone={feedbackView.error ? 'error' : feedbackView.pending ? 'pending' : 'success'}>{feedbackView.message}{feedback?.kind === 'task' && <> <Link className="underline underline-offset-2" to="/tasks">{zh ? '查看任务' : 'View task'}</Link></>}</InlineStatus>}
        <Button onClick={() => void navigate({ to: '/projects/new' })}><Plus />{t('newProject')}</Button>
      </div>
      {!!selectedRows.length && <div className="flex flex-wrap items-center gap-2"><span className="text-sm">{selectedRows.length} {zh ? '已选' : 'selected'}</span>{[['up', Play], ['stop', Square], ['restart', RefreshCw], ['down', PowerOff]].map(([name, Icon]) => <Button key={String(name)} variant={name === 'down' ? 'destructive' : 'outline'} size="sm" disabled={batch.isPending} onClick={() => void runBatch(String(name))}><Icon />{actionLabel(String(name), zh)}</Button>)}</div>}
      {batch.isError && <ErrorState description={batch.error.message} />}
      {(containers.isError || !fresh) && <p role="alert" className="text-xs text-muted-foreground">{zh ? '容器运行信息暂不可用。' : 'Container runtime information unavailable.'}<Button variant="ghost" size="sm" onClick={() => void containers.refetch()}>{zh ? '重试' : 'Retry'}</Button></p>}
      {query.isPending ? <LoadingState compact rows={7} label={zh ? '正在加载项目' : 'Loading projects'} /> : query.isError ? <ErrorState description={query.error.message} /> : <>
        <div className={`${grid} rounded-md bg-surface px-3 py-2 text-xs text-muted-foreground`}><Checkbox checked={allSelected} indeterminate={someSelected} onCheckedChange={value => toggleAll(value === true)} aria-label={zh ? '选择本页托管项目' : 'Select managed projects on this page'} /><span>{zh ? '项目' : 'Project'}</span><span className="hidden lg:block">{zh ? '运行' : 'Runtime'}</span><span className="hidden lg:block">{zh ? '资源' : 'Resources'}</span><span className="text-right">{zh ? '操作' : 'Actions'}</span></div>
        {!visible.length && <p className="rounded-lg bg-surface p-8 text-center text-sm text-muted-foreground">{rows.length ? zh ? '没有符合条件的项目。' : 'No matching projects.' : zh ? '未发现项目。' : 'No projects discovered.'}</p>}
        <div className="space-y-0.5">{pagination.items.map(entry => {
          const row = entry.project, status = projectStatus(entry), open = expanded.has(row.name)
          const serviceCount = available ? groupServices(entry.containers, row.declared_service_names ?? []).length : row.service_count
          return <Fragment key={row.name}><div className={`${grid} cursor-pointer rounded-lg px-3 py-[7px] hover:bg-muted/25`} data-testid="project-row" data-project={row.name} onClick={event => { if (event.currentTarget.contains(event.target as Node) && !(event.target as Element).closest('a, button, input, [data-slot="checkbox"]')) toggleExpanded(row.name) }}>
            <div className="flex items-center gap-3">
              <Checkbox disabled={!row.managed} checked={selected.has(row.name)} onCheckedChange={value => toggleRow(row.name, value === true)} aria-label={projectDisplayName(row)} />
              <Button variant="ghost" size="icon-xs" aria-expanded={open} aria-label={`${zh ? '展开服务' : 'Expand services'} · ${projectDisplayName(row)}`} onClick={() => toggleExpanded(row.name)}><ChevronRight className={`transition-transform ${open ? 'rotate-90' : ''}`} /></Button>
            </div>
            <div className="min-w-0"><div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1"><TooltipHint content={row.delivery_project_name && row.delivery_project_name !== row.name ? `${zh ? 'Compose 项目名' : 'Compose project name'}: ${row.name}` : undefined}><Link to="/projects/$backend/$projectName" params={{ backend: row.backend, projectName: row.name }} className="max-w-full truncate text-sm font-medium hover:underline">{projectDisplayName(row)}</Link></TooltipHint><StatusBadge tone={row.managed ? 'outline' : 'neutral'}>{row.delivery_project_name ? 'CD' : row.managed ? zh ? '托管' : 'Managed' : zh ? '外部' : 'External'}</StatusBadge></div><p className="mt-1 text-xs text-muted-foreground">{serviceCount} {zh ? '服务' : 'services'}{row.managed && !row.declared_service_names && ` · ${zh ? '声明不可用' : 'Declarations unavailable'}`}</p></div>
            <div className="col-start-2 flex flex-col items-start gap-1 lg:col-start-auto lg:row-start-auto"><StatusBadge tone={status === 'running' ? 'success' : status === 'degraded' ? 'warning' : 'neutral'}>{statusLabel(status, zh)}</StatusBadge><span className="text-xs text-muted-foreground">{available ? `${entry.summary.running}/${entry.summary.total}` : '—'} {zh ? '容器运行中' : 'containers running'}{entry.summary.abnormal > 0 && <span className="ml-2 text-destructive">{entry.summary.abnormal} {zh ? '异常' : 'abnormal'}</span>}{available && entry.summary.unknown > 0 && <span className="ml-2">{entry.summary.unknown} {zh ? '健康未知' : 'health unknown'}</span>}</span></div>
            <div className="col-start-3 row-start-2 lg:col-start-auto lg:row-start-auto"><ProjectResourceSummary summary={entry.summary} zh={zh} /></div>
            <div className="col-start-3 row-start-1 lg:col-start-auto lg:row-start-auto"><ProjectActions row={{ ...row, status }} zh={zh} onTakeover={() => setTakeoverName(row.name)} onFeedback={setFeedback} onMenuChange={setMenuOpen} /></div>
          </div>{open && <div className="p-3 lg:ml-[4.25rem]"><ProjectServices key={`${nodeID}/${row.name}`} nodeID={nodeID} projectName={row.name} rows={entry.containers} declared={row.declared_service_names ?? []} declarationUnavailable={row.managed && !row.declared_service_names} loading={containers.isPending} error={!available && !containers.isPending ? zh ? '运行信息暂不可用' : 'Runtime information unavailable' : undefined} observation={observation} showUpdates={false} onOverlayChange={setServiceOverlay} zh={zh} onOpenContainer={(row, tab, opener) => void workspace.open(row, tab, opener)} /></div>}</Fragment>
        })}</div>
        {visible.length > pagination.pageSize && <ListPagination {...pagination} zh={zh} />}
      </>}
    </div>
    <ContainerWorkspaceDialog workspace={workspace} rows={observedRows} available={available} zh={zh} />
    <TakeoverWarningDialog open={!!takeoverName} projectName={takeoverName} zh={zh} onOpenChange={open => { if (!open) setTakeoverName('') }} onContinue={() => { const name = takeoverName; setTakeoverName(''); void navigate({ to: '/projects/$backend/$projectName/takeover', params: { backend: 'compose', projectName: name } }) }} />
  </ResourceFrame>
}
function actionLabel(action: string, zh: boolean) {
  const values: Record<string, [string, string]> = { up: ['启动', 'Start'], start: ['启动现有容器', 'Start existing containers'], stop: ['停止', 'Stop'], restart: ['重启', 'Restart'], update: ['更新', 'Update'], down: ['Down', 'Down'], remove: ['删除', 'Remove'], cleanup: ['删除清理', 'Delete and clean'] }
  return values[action]?.[zh ? 0 : 1] ?? action
}

function projectFeedbackView(feedback: ProjectFeedback | null, task: ProjectTask | undefined, zh: boolean) {
  if (!feedback) return null
  const operation = actionLabel(feedback.action, zh)
  if (feedback.kind === 'error') return { error: true, pending: false, message: zh ? `${feedback.projectName} ${operation}失败：${feedback.message}` : `${operation} failed for ${feedback.projectName}: ${feedback.message}` }
  if (feedback.kind === 'pending') return { error: false, pending: true, message: zh ? `正在提交 ${feedback.projectName} 的${operation}操作…` : `Submitting ${operation} for ${feedback.projectName}…` }
  if (feedback.kind === 'success') return { error: false, pending: false, message: zh ? `${feedback.projectName} ${operation}完成。` : `${operation} completed for ${feedback.projectName}.` }
  if (!task || task.status === 'pending') return { error: false, pending: true, message: zh ? `${feedback.projectName} 的${operation}任务已提交，正在等待执行。` : `${operation} was queued for ${feedback.projectName}.` }
  if (task.status === 'running') return { error: false, pending: true, message: zh ? `${feedback.projectName} 正在${operation}：${task.message || '执行中…'}` : `${operation} is running for ${feedback.projectName}: ${task.message || 'In progress…'}` }
  if (task.status === 'success') return { error: false, pending: false, message: zh ? `${feedback.projectName} ${operation}完成。` : `${operation} completed for ${feedback.projectName}.` }
  return { error: true, pending: false, message: zh ? `${feedback.projectName} ${operation}失败：${task.message || task.status}` : `${operation} failed for ${feedback.projectName}: ${task.message || task.status}` }
}

function ProjectActions({ row, zh, onTakeover, onFeedback, onMenuChange }: { row: ProjectSummary; zh: boolean; onTakeover: () => void; onFeedback: (feedback: ProjectFeedback) => void; onMenuChange: (open: boolean) => void }) {
  const nodeID = useUIStore((state) => state.currentNodeID)
  const client = useQueryClient()
  const action = useMutation({ mutationFn: (name: string) => api<ProjectTask>(nodePath(nodeID, `/projects/compose/${encodeURIComponent(row.name)}/actions/${name}`), { method: 'POST' }), onMutate: (name) => onFeedback({ kind: 'pending', projectName: row.name, action: name }), onSuccess: (task, name) => { onFeedback({ kind: 'task', projectName: row.name, action: name, taskID: task.id }); void client.invalidateQueries({ queryKey: ['projects', nodeID] }); void client.invalidateQueries({ queryKey: ['tasks', 'current', nodeID] }) }, onError: (error, name) => onFeedback({ kind: 'error', projectName: row.name, action: name, message: error.message }) })
  const remove = useMutation({
    mutationFn: (removeVolumes: boolean) => api(nodePath(nodeID, `/projects/compose/${encodeURIComponent(row.name)}?confirm=${encodeURIComponent(row.name)}&force=true&preserve_volumes=${!removeVolumes}`), { method: 'DELETE' }),
    onMutate: () => onFeedback({ kind: 'pending', projectName: row.name, action: 'remove' }),
    onSuccess: () => { onFeedback({ kind: 'success', projectName: row.name, action: 'remove' }); void client.invalidateQueries({ queryKey: ['projects', nodeID] }) },
    onError: (error) => onFeedback({ kind: 'error', projectName: row.name, action: 'remove', message: error.message }),
  })
  const cleanup = useMutation({
    mutationFn: (removeVolumes: boolean) => api<ProjectTask>(nodePath(nodeID, `/projects/compose/${encodeURIComponent(row.name)}/cleanup`), { method: 'POST', body: JSON.stringify({ confirmation_name: row.name, remove_volumes: removeVolumes }) }),
    onMutate: () => onFeedback({ kind: 'pending', projectName: row.name, action: 'cleanup' }),
    onSuccess: (task) => {
      onFeedback({ kind: 'task', projectName: row.name, action: 'cleanup', taskID: task.id })
      void client.invalidateQueries({ queryKey: ['projects', nodeID] })
      void client.invalidateQueries({ queryKey: ['tasks', 'current', nodeID] })
    },
    onError: (error) => onFeedback({ kind: 'error', projectName: row.name, action: 'cleanup', message: error.message }),
  })
  const run = async (name: string) => {
    if (action.isPending || remove.isPending) return
    if (name === 'down' && !await confirmDialog({ title: `Down ${row.name}?`, description: zh ? '将移除项目容器和网络。' : 'Project containers and networks will be removed.', confirmLabel: 'Down', danger: true })) return
    action.mutate(name)
  }
  const removeProject = async () => {
    if (action.isPending || remove.isPending) return
    const result = await confirmManagedProjectRemoval(row.name, zh)
    if (result) remove.mutate(result.checked)
  }
  const cleanupProject = async () => {
    if (action.isPending || remove.isPending || cleanup.isPending) return
    const result = await confirmExternalProjectCleanup(row.name, zh)
    if (result) cleanup.mutate(result.checked)
  }
  const busy = action.isPending || remove.isPending || cleanup.isPending
  const primaryAction = row.status === 'running' || row.status === 'degraded' ? 'stop' : 'up'
  const supported = (capability: ProjectCapability) => row.capabilities.includes(capability)
  return <div className="flex items-center justify-end" aria-busy={busy}>
    <ActionIcon label={row.managed ? (zh ? '编辑项目' : 'Edit Project') : (zh ? '查看项目' : 'View Project')}><Link to="/projects/$backend/$projectName" params={{ backend: row.backend, projectName: row.name }}>{row.managed ? <Pencil /> : <Eye />}</Link></ActionIcon>
    {row.managed && supported(primaryAction === 'stop' ? 'stop' : 'deploy') && <ActionIcon label={actionLabel(primaryAction, zh)} disabled={busy} onClick={() => void run(primaryAction)}>{action.isPending ? <Spinner /> : primaryAction === 'stop' ? <Square /> : <Play />}</ActionIcon>}
    <DropdownMenu onOpenChange={onMenuChange}><DropdownMenuTrigger render={<Button variant="ghost" size="icon-sm" disabled={busy} aria-label={zh ? '更多项目操作' : 'More project actions'}><MoreHorizontal /></Button>} /><DropdownMenuContent align="end">
      {row.managed ? <>{supported('restart') && <DropdownMenuItem onClick={() => void run('restart')}><RefreshCw />{actionLabel('restart', zh)}</DropdownMenuItem>}{supported('deploy') && <DropdownMenuItem variant="destructive" onClick={() => void run('down')}><PowerOff />Down</DropdownMenuItem>}{supported('delete') && <DropdownMenuItem variant="destructive" onClick={() => void removeProject()}><Trash2 />{zh ? '删除项目' : 'Remove Project'}</DropdownMenuItem>}</> : <>{supported('takeover') && <DropdownMenuItem onClick={onTakeover}><Download />{zh ? '接管项目' : 'Take over Project'}</DropdownMenuItem>}{supported('cleanup') && <DropdownMenuItem variant="destructive" onClick={() => void cleanupProject()}><Trash2 />{zh ? '删除并清理项目' : 'Delete and clean Project'}</DropdownMenuItem>}</>}
    </DropdownMenuContent></DropdownMenu>
  </div>
}

function ActionIcon({ label, destructive, disabled, onClick, children }: { label: string; destructive?: boolean; disabled?: boolean; onClick?: () => void; children: ReactNode }) {
  return <TooltipHint content={label}><Button variant="ghost" size="icon-sm" className={destructive ? 'text-destructive' : undefined} disabled={disabled} onClick={onClick} aria-label={label} nativeButton={!!onClick} render={!onClick && typeof children === 'object' ? children as ReactElement : undefined}>{onClick ? children : null}</Button></TooltipHint>
}
