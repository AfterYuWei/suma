import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { ChevronRight, CircleAlert, Download, Eye, FileText, Pencil, Play, Plus, Power, RefreshCw, Square, SquareTerminal, X } from 'lucide-react'
import { type ReactNode, useEffect, useState } from 'react'
import { Alert, AlertAction, AlertDescription } from '../components/ui/alert'
import { Button } from '../components/ui/button'
import { Checkbox } from '../components/ui/checkbox'
import { ErrorState } from '../components/ui/error-state'
import { ListShell } from '../components/ui/list-shell'
import { LoadingState } from '../components/ui/loading-state'
import { Spinner } from '../components/ui/spinner'
import { StatusBadge } from '../components/ui/status-badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'
import { Tooltip, TooltipContent, TooltipTrigger } from '../components/ui/tooltip'
import { TooltipHint } from '../components/ui/tooltip-hint'
import type { ComposeProject } from '../features/compose/types'
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
interface ComposeTask { id: string; status: string; progress: number; message: string }
interface ActiveTask { id: string; project: string; action: string }

export function ComposePage() {
  const nodeID = useUIStore((state) => state.currentNodeID)
  const client = useQueryClient()
  const [selected, setSelected] = useState<Set<string>>(() => new Set())
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set())
  const [operationError, setOperationError] = useState('')
  const [activeTask, setActiveTask] = useState<ActiveTask | null>(null)
  const { t, language } = useI18n()
  const zh = language === 'zh-CN'
  const query = useQuery({ queryKey: ['compose', nodeID], queryFn: () => api<ComposeProject[]>(nodePath(nodeID, '/compose')), refetchInterval: 5_000 })
  const tasks = useQuery({ queryKey: ['tasks', nodeID], queryFn: () => api<ComposeTask[]>(`/tasks?node_id=${encodeURIComponent(nodeID)}`), enabled: activeTask !== null, refetchInterval: (taskQuery) => { const rows = taskQuery.state.data; const tracked = rows?.find((task) => task.id === activeTask?.id); return tracked && ['success', 'failed', 'canceled'].includes(tracked.status) ? false : 1_000 } })
  const create = useMutation({ mutationFn: (name: string) => api<ComposeProject>(nodePath(nodeID, '/compose'), { method: 'POST', body: JSON.stringify({ name, compose: starter, environment: '' }) }), onSuccess: () => { void client.invalidateQueries({ queryKey: ['compose', nodeID] }) } })
  const batch = useMutation({ mutationFn: ({ names, action }: { names: string[]; action: string }) => api<{ results: { name: string; task_id?: string; success: boolean }[] }>(nodePath(nodeID, '/compose/batch'), { method: 'POST', body: JSON.stringify({ names, action }) }), onMutate: () => setOperationError(''), onSuccess: async (result, variables) => { const failed = result.results.filter((item) => !item.success).length; const started = result.results.find((item) => item.success && item.task_id); setSelected(new Set()); if (started?.task_id) setActiveTask({ id: started.task_id, project: started.name, action: variables.action }); if (failed) setOperationError(zh ? `${failed} 个 Compose 项目未能启动操作。` : `${failed} Compose project operations could not be started.`); await Promise.all([client.invalidateQueries({ queryKey: ['compose', nodeID] }), client.invalidateQueries({ queryKey: ['tasks', nodeID] })]) }, onError: (error) => setOperationError(error.message) })
  const add = async () => { const name = await promptDialog({ title: t('newProject'), description: zh ? '创建一个本地管理的 Compose 项目。Git 持续交付请在独立的“持续交付”菜单配置。' : 'Create a locally managed Compose project. Configure Git delivery from the separate Continuous Delivery menu.', confirmLabel: t('create'), input: { label: t('projectName') } }); if (name) create.mutate(name) }
  const rows = query.data ?? []
  const trackedTask = activeTask ? tasks.data?.find((task) => task.id === activeTask.id) : undefined
  const manageableRows = rows.filter((row) => row.can_manage)
  const selectedRows = rows.filter((row) => selected.has(row.name))
  const allSelected = manageableRows.length > 0 && selectedRows.length === manageableRows.length
  const running = rows.filter((row) => row.status === 'running').length
  const degraded = rows.filter((row) => row.status === 'degraded').length
  const stopped = rows.length - running - degraded
  const runBatch = async (action: string) => {
    if (!selectedRows.length) return
    if (action === 'down') {
      const names = selectedRows.slice(0, 3).map((row) => row.name).join('、')
      if (!await confirmDialog({ title: zh ? `Down 选中的 ${selectedRows.length} 个项目？` : `Down ${selectedRows.length} selected projects?`, description: zh ? `${names}${selectedRows.length > 3 ? ' 等' : ''}。这会停止并移除项目容器和网络，但保留项目文件与命名卷。` : `${names}${selectedRows.length > 3 ? ' and others' : ''}. This stops and removes project containers and networks while keeping project files and named volumes.`, confirmLabel: 'Down', danger: true })) return
    }
    batch.mutate({ names: selectedRows.map((row) => row.name), action })
  }
  const toggleAll = (checked: boolean) => setSelected(checked ? new Set(manageableRows.map((row) => row.name)) : new Set())
  const toggleRow = (name: string, checked: boolean) => { const next = new Set(selected); if (checked) next.add(name); else next.delete(name); setSelected(next) }
  const toggleExpanded = (name: string) => { const next = new Set(expanded); if (next.has(name)) next.delete(name); else next.add(name); setExpanded(next) }

  useEffect(() => {
    if (trackedTask?.status !== 'success') return
    void client.invalidateQueries({ queryKey: ['compose', nodeID] })
  }, [trackedTask?.status, client, nodeID])

  const toolbar = <div className="flex flex-wrap items-center gap-2">
    <StatusBadge tone="success">{running} {zh ? '运行中' : 'running'}</StatusBadge>
    <StatusBadge tone="warning">{degraded} {zh ? '异常' : 'degraded'}</StatusBadge>
    <StatusBadge tone="neutral">{stopped} {zh ? '已停止' : 'stopped'}</StatusBadge>
    <Button onClick={() => void add()} disabled={create.isPending}>{create.isPending ? <Spinner className="size-4" /> : <Plus size={16} />}{t('newProject')}</Button>
  </div>

  return <ResourceFrame title="Compose" detail={zh ? `${rows.length} 个项目` : `${rows.length} projects`} action={toolbar}>
    <div className="flex w-full flex-col items-start gap-4">
      {!!selected.size && <div className="flex flex-wrap items-center gap-2"><span className="text-sm font-medium">{selected.size} {zh ? '已选' : 'selected'}</span>
        <Button variant="outline" size="sm" disabled={batch.isPending} onClick={() => void runBatch('start')}>{batch.isPending ? <Spinner className="size-3.5" /> : <Play size={16} />}{zh ? '启动' : 'Start'}</Button>
        <Button variant="outline" size="sm" disabled={batch.isPending} onClick={() => void runBatch('stop')}>{batch.isPending ? <Spinner className="size-3.5" /> : <Square size={16} />}{zh ? '停止' : 'Stop'}</Button>
        <Button variant="outline" size="sm" disabled={batch.isPending} onClick={() => void runBatch('restart')}>{batch.isPending ? <Spinner className="size-3.5" /> : <RefreshCw size={16} />}{zh ? '重启' : 'Restart'}</Button>
        <TooltipHint content={zh ? '为选中的项目拉取最新镜像并重新应用 Compose 配置' : 'Pull latest images and reapply Compose for selected projects'}><Button variant="outline" size="sm" disabled={batch.isPending} onClick={() => void runBatch('update')}>{batch.isPending ? <Spinner className="size-3.5" /> : <Download size={16} />}{zh ? '拉取并重建' : 'Pull & recreate'}</Button></TooltipHint>
        <Button variant="destructive" size="sm" disabled={batch.isPending} onClick={() => void runBatch('down')}>{batch.isPending ? <Spinner className="size-3.5" /> : <Power size={16} />}Down</Button>
      </div>}
      {operationError && <Alert variant="destructive" className="w-full">
        <CircleAlert />
        <AlertDescription>{operationError}</AlertDescription>
        <AlertAction><Button variant="ghost" size="icon-xs" aria-label={zh ? '关闭' : 'Dismiss'} onClick={() => setOperationError('')}><X /></Button></AlertAction>
      </Alert>}
      {activeTask && <Alert variant={trackedTask?.status === 'failed' || trackedTask?.status === 'canceled' ? 'destructive' : 'default'} className="w-full pr-44">
        {trackedTask?.status === 'failed' ? <CircleAlert /> : trackedTask?.status === 'running' || trackedTask?.status === 'pending' ? <Spinner /> : <Download />}
        <AlertDescription><span className="font-medium">{activeTask.project}</span> · {actionLabel(activeTask.action, zh)} · {trackedTask ? `${trackedTask.status}${trackedTask.message ? ` — ${trackedTask.message}` : ''}` : (zh ? '正在创建任务…' : 'Creating task…')}</AlertDescription>
        <AlertAction className="flex items-center gap-1"><Button variant="outline" size="sm" render={<Link to="/tasks" />}>{zh ? '查看任务' : 'View task'}</Button><Button variant="ghost" size="icon-sm" aria-label={zh ? '关闭任务提示' : 'Dismiss task status'} onClick={() => setActiveTask(null)}><X /></Button></AlertAction>
      </Alert>}
      {query.isPending ? <LoadingState compact rows={7} label={zh ? '正在加载 Compose 项目' : 'Loading Compose projects'} /> : query.isError ? <ErrorState description={query.error.message} /> : rows.length === 0 ? <div className="flex w-full flex-col items-center gap-1 rounded-xl bg-card px-4 py-10 text-center ring-1 ring-foreground/10">
        <p className="text-sm font-medium">{zh ? '未发现 Compose 项目' : 'No Compose projects discovered'}</p>
        <p className="text-sm text-muted-foreground">{zh ? '运行中的 Compose 项目会根据 Docker 标签自动显示，也可以新建一个由 SUMA 管理的项目。' : 'Running Compose projects appear automatically from Docker labels, or create a project managed by SUMA.'}</p>
      </div> : <ListShell><Table>
        <TableHeader>
          <TableRow>
            <TableHead className="w-9 pr-0"><Checkbox checked={allSelected} indeterminate={!allSelected && selected.size > 0} onCheckedChange={(checked) => toggleAll(checked === true)} aria-label={zh ? '全选' : 'Select all'} /></TableHead>
            <TableHead className="w-9"><span className="sr-only">{zh ? '展开' : 'Expand'}</span></TableHead>
            <TableHead>{zh ? '项目' : 'Project'}</TableHead>
            <TableHead className="w-28">{zh ? '状态' : 'Status'}</TableHead>
            <TableHead className="w-44">{zh ? '运行资源' : 'Runtime'}</TableHead>
            <TableHead className="w-48">{zh ? '更新时间' : 'Updated'}</TableHead>
            <TableHead className="w-64">{zh ? '操作' : 'Actions'}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((row) => {
            const isExpanded = expanded.has(row.name)
            return [
              <TableRow key={row.name} aria-expanded={isExpanded}>
                <TableCell className="pr-0"><Checkbox disabled={!row.can_manage} checked={selected.has(row.name)} onCheckedChange={(checked) => toggleRow(row.name, checked === true)} aria-label={row.can_manage ? row.name : (zh ? `${row.name} 是只读外部项目` : `${row.name} is a read-only external project`)} /></TableCell>
                <TableCell><Button variant="ghost" size="icon-xs" aria-expanded={isExpanded} aria-label={zh ? '展开项目容器' : 'Expand project containers'} onClick={() => toggleExpanded(row.name)}><ChevronRight className={isExpanded ? 'rotate-90 transition-transform' : 'transition-transform'} /></Button></TableCell>
                <TableCell><div><div className="flex items-center gap-2"><Link to="/compose/$projectName" params={{ projectName: row.name }} className="font-medium hover:underline">{row.name}</Link><StatusBadge tone={row.can_manage ? 'outline' : 'neutral'}>{row.can_manage ? (zh ? '托管' : 'Managed') : (zh ? '外部' : 'External')}</StatusBadge></div><TooltipHint content={row.path}><span className="block max-w-72 truncate text-xs text-muted-foreground">{row.path || (zh ? '工作目录未知' : 'Working directory unavailable')}</span></TooltipHint></div></TableCell>
                <TableCell><StatusBadge tone={projectTone(row.status)}>{row.status}</StatusBadge></TableCell>
                <TableCell><div className="flex items-baseline gap-2"><span>{row.services} {zh ? '服务' : 'services'}</span><span className="text-muted-foreground">{row.containers} {zh ? '容器' : 'containers'}</span></div></TableCell>
                <TableCell className="text-muted-foreground">{new Date(row.updated_at).toLocaleString(language)}</TableCell>
                <TableCell><ProjectActions row={row} zh={zh} onTaskStarted={(task, action) => setActiveTask({ id: task.id, project: row.name, action })} onError={setOperationError} /></TableCell>
              </TableRow>,
              ...(isExpanded ? [<TableRow key={`${row.name}-services`} className="hover:bg-transparent"><TableCell colSpan={7} className="bg-muted/30 p-4"><ProjectServices project={row} zh={zh} /></TableCell></TableRow>] : []),
            ]
          })}
        </TableBody>
      </Table></ListShell>}
      {create.isError && <ErrorState description={create.error.message} />}
    </div>
  </ResourceFrame>
}

function actionLabel(action: string, zh: boolean) {
  const labels: Record<string, [string, string]> = { start: ['启动', 'Start'], stop: ['停止', 'Stop'], restart: ['重启', 'Restart'], update: ['拉取并重建', 'Pull & recreate'], down: ['Down', 'Down'] }
  return labels[action]?.[zh ? 0 : 1] ?? action
}

function ProjectActions({ row, zh, onTaskStarted, onError }: { row: ComposeProject; zh: boolean; onTaskStarted: (task: ComposeTask, action: string) => void; onError: (message: string) => void }) {
  const nodeID = useUIStore((state) => state.currentNodeID)
  const client = useQueryClient()
  const action = useMutation({ mutationFn: (name: string) => api<ComposeTask>(nodePath(nodeID, `/compose/${encodeURIComponent(row.name)}/${name}`), { method: 'POST' }), onSuccess: (task, name) => { onTaskStarted(task, name); void client.invalidateQueries({ queryKey: ['compose', nodeID] }); void client.invalidateQueries({ queryKey: ['tasks', nodeID] }) }, onError: (error) => onError(error.message) })
  const importProject = useMutation({ mutationFn: () => api<ComposeProject>(nodePath(nodeID, `/compose/${encodeURIComponent(row.name)}/import`), { method: 'POST' }), onSuccess: () => { void client.invalidateQueries({ queryKey: ['compose', nodeID] }); location.assign(`/compose/${encodeURIComponent(row.name)}`) }, onError: (error) => onError(error.message) })
  const run = async (name: string) => { if (name === 'down' && !await confirmDialog({ title: zh ? `Down ${row.name}？` : `Down ${row.name}?`, description: zh ? '这会停止并移除该 Compose 项目的容器和网络，但不会删除项目文件或命名卷。' : 'This stops and removes the Compose project containers and networks, but keeps project files and named volumes.', confirmLabel: 'Down', danger: true })) return; action.mutate(name) }
  const pending = (name: string) => action.isPending && action.variables === name
  if (!row.can_manage) return <div className="flex items-center gap-1">{row.config_files.length === 1 ? <ActionIcon label={zh ? '导入并编辑' : 'Import & edit'} disabled={importProject.isPending} onClick={() => importProject.mutate()}>{importProject.isPending ? <Spinner /> : <Download />}</ActionIcon> : <Tooltip><TooltipTrigger render={<Button variant="ghost" size="icon-sm" aria-label={zh ? '查看项目' : 'View project'} render={<Link to="/compose/$projectName" params={{ projectName: row.name }} />} />}><Eye /></TooltipTrigger><TooltipContent>{zh ? '查看项目' : 'View project'}</TooltipContent></Tooltip>}</div>
  return <div className="flex items-center gap-1">
    <Tooltip><TooltipTrigger render={<Button variant="ghost" size="icon-sm" aria-label={zh ? '编辑项目' : 'Edit project'} render={<Link to="/compose/$projectName" params={{ projectName: row.name }} />} />}><Pencil /></TooltipTrigger><TooltipContent>{zh ? '编辑项目' : 'Edit project'}</TooltipContent></Tooltip>
    <ActionIcon label={zh ? '启动项目' : 'Start project'} disabled={row.status === 'running' || row.containers === 0 || pending('start')} onClick={() => void run('start')}>{pending('start') ? <Spinner /> : <Play />}</ActionIcon>
    <ActionIcon label={zh ? '停止项目' : 'Stop project'} disabled={row.status === 'stopped' || pending('stop')} onClick={() => void run('stop')}>{pending('stop') ? <Spinner /> : <Square />}</ActionIcon>
    <ActionIcon label={zh ? '重启项目' : 'Restart project'} disabled={row.containers === 0 || pending('restart')} onClick={() => void run('restart')}>{pending('restart') ? <Spinner /> : <RefreshCw />}</ActionIcon>
    <ActionIcon label={zh ? '拉取最新镜像并重新应用 Compose 配置' : 'Pull latest images and reapply Compose'} disabled={pending('update')} onClick={() => void run('update')}>{pending('update') ? <Spinner /> : <Download />}</ActionIcon>
    <ActionIcon label="Down" destructive disabled={pending('down')} onClick={() => void run('down')}>{pending('down') ? <Spinner /> : <Power />}</ActionIcon>
  </div>
}

function ActionIcon({ label, destructive = false, disabled, onClick, children }: { label: string; destructive?: boolean; disabled?: boolean; onClick: () => void; children: ReactNode }) {
  return <Tooltip><TooltipTrigger render={<span className="inline-flex" />}><Button variant="ghost" size="icon-sm" className={destructive ? 'text-red-600 hover:text-red-600 dark:text-red-400 dark:hover:text-red-400' : undefined} disabled={disabled} onClick={onClick} aria-label={label}>{children}</Button></TooltipTrigger><TooltipContent>{label}</TooltipContent></Tooltip>
}

function ProjectServices({ project, zh }: { project: ComposeProject; zh: boolean }) {
  const nodeID = useUIStore((state) => state.currentNodeID)
  const services = useQuery({ queryKey: ['compose-services', nodeID, project.name], queryFn: () => api<ContainerSummary[]>(nodePath(nodeID, `/compose/${encodeURIComponent(project.name)}/services`)), refetchInterval: 5_000 })
  if (services.isPending) return <LoadingState embedded compact rows={2} label={zh ? '正在加载项目容器' : 'Loading project containers'} />
  if (services.isError) return <ErrorState description={services.error.message} />
  const data = services.data ?? []
  if (!data.length) return <p className="py-2 text-center text-sm text-muted-foreground">{zh ? '该项目当前没有容器' : 'This project has no containers'}</p>
  return <Table>
    <TableHeader>
      <TableRow>
        <TableHead>{zh ? '服务' : 'Service'}</TableHead>
        <TableHead>{zh ? '容器' : 'Container'}</TableHead>
        <TableHead>{zh ? '镜像' : 'Image'}</TableHead>
        <TableHead>{zh ? '状态' : 'Status'}</TableHead>
        <TableHead className="w-52">{zh ? '操作' : 'Actions'}</TableHead>
      </TableRow>
    </TableHeader>
    <TableBody>
      {data.map((row) => (
        <TableRow key={row.id}>
          <TableCell><Link to="/containers/$containerId" params={{ containerId: row.id }} className="font-medium hover:underline">{row.labels['com.docker.compose.service'] || row.name}</Link></TableCell>
          <TableCell className="text-muted-foreground">{row.name}</TableCell>
          <TableCell><TooltipHint content={row.image}><span className="block max-w-56 truncate text-muted-foreground">{row.image}</span></TooltipHint></TableCell>
          <TableCell><StatusBadge tone={containerTone(row.state)}>{row.state}</StatusBadge></TableCell>
          <TableCell><ServiceActions row={row} projectName={project.name} zh={zh} /></TableCell>
        </TableRow>
      ))}
    </TableBody>
  </Table>
}

function ServiceActions({ row, projectName, zh }: { row: ContainerSummary; projectName: string; zh: boolean }) {
  const nodeID = useUIStore((state) => state.currentNodeID)
  const client = useQueryClient()
  const action = useMutation({ mutationFn: (name: string) => api(nodePath(nodeID, `/containers/${encodeURIComponent(row.id)}/${name}`), { method: 'POST' }), onSuccess: () => { void client.invalidateQueries({ queryKey: ['compose', nodeID] }); void client.invalidateQueries({ queryKey: ['compose-services', nodeID, projectName] }); void client.invalidateQueries({ queryKey: ['containers', nodeID] }) } })
  const primary = row.state === 'running' ? 'stop' : row.state === 'paused' ? 'unpause' : 'start'
  const pending = (name: string) => action.isPending && action.variables === name
  return <div className="flex items-center gap-1">
    <ActionIcon label={zh ? '查看日志' : 'View logs'} onClick={() => location.assign(`/containers/${row.id}#logs`)}><FileText /></ActionIcon>
    <ActionIcon label={zh ? '打开终端' : 'Open terminal'} disabled={row.state !== 'running'} onClick={() => location.assign(`/containers/${row.id}#terminal`)}><SquareTerminal /></ActionIcon>
    <ActionIcon label={zh ? '重启容器' : 'Restart container'} disabled={pending('restart')} onClick={() => action.mutate('restart')}>{pending('restart') ? <Spinner /> : <RefreshCw />}</ActionIcon>
    <ActionIcon label={primary === 'stop' ? (zh ? '停止容器' : 'Stop container') : (zh ? '启动容器' : 'Start container')} disabled={pending(primary)} onClick={() => action.mutate(primary)}>{pending(primary) ? <Spinner /> : primary === 'stop' ? <Square /> : <Play />}</ActionIcon>
  </div>
}
