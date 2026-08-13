import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Box, Boxes, ChevronDown, Download, FileText, FolderCog, LoaderCircle, Play, Plus, Power, RefreshCw, Square, SquareTerminal, type LucideIcon } from 'lucide-react'
import { type MouseEvent, useState } from 'react'
import { LoadingState } from '../components/ui/loading-state'
import type { ComposeProject } from '../features/compose/types'
import type { ContainerSummary } from '../features/containers/types'
import { api } from '../lib/api'
import { nodePath } from '../lib/nodes'
import { useI18n } from '../lib/i18n'
import { confirmDialog, promptDialog } from '../stores/dialog'
import { useUIStore } from '../stores/ui'

const starter = `services:\n  app:\n    image: nginx:alpine\n    ports:\n      - "8080:80"\n`

export function ComposePage() {
	const nodeID = useUIStore((state) => state.currentNodeID)
  const client = useQueryClient()
  const [selected, setSelected] = useState<Set<string>>(() => new Set())
  const [operationError, setOperationError] = useState('')
  const { t, language } = useI18n()
  const zh = language === 'zh-CN'
  const query = useQuery({ queryKey: ['compose', nodeID], queryFn: () => api<ComposeProject[]>(nodePath(nodeID, '/compose')), refetchInterval: 5_000 })
  const create = useMutation({
    mutationFn: (name: string) => api<ComposeProject>(nodePath(nodeID, '/compose'), { method: 'POST', body: JSON.stringify({ name, compose: starter, environment: '' }) }),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['compose', nodeID] })
    },
  })
  const batch = useMutation({
    mutationFn: ({ names, action }: { names: string[]; action: string }) => api<{ results: { name: string; task_id?: string; success: boolean }[] }>(nodePath(nodeID, '/compose/batch'), { method: 'POST', body: JSON.stringify({ names, action }) }),
    onMutate: () => setOperationError(''),
    onSuccess: async (result) => {
      const failed = result.results.filter((item) => !item.success).length
      setSelected(new Set())
      if (failed) setOperationError(zh ? `${failed} 个 Compose 项目未能启动操作。` : `${failed} Compose project operations could not be started.`)
      await Promise.all([client.invalidateQueries({ queryKey: ['compose', nodeID] }), client.invalidateQueries({ queryKey: ['tasks', nodeID] })])
    },
    onError: (error) => setOperationError(error.message),
  })
  const add = async () => {
    const name = await promptDialog({ title: t('newProject'), description: zh ? '创建一个本地管理的 Compose 项目。Git 持续交付请在独立的“持续交付”菜单配置。' : 'Create a locally managed Compose project. Configure Git delivery from the separate Continuous Delivery menu.', confirmLabel: t('create'), input: { label: t('projectName') } })
    if (name) create.mutate(name)
  }
  const rows = query.data ?? []
  const selectedRows = rows.filter((row) => selected.has(row.name))
  const allSelected = !!rows.length && rows.every((row) => selected.has(row.name))
  const running = rows.filter((row) => row.status === 'running').length
  const degraded = rows.filter((row) => row.status === 'degraded').length
  const stopped = rows.length - running - degraded
  const toggleSelected = (name: string) => setSelected((current) => { const next = new Set(current); if (next.has(name)) next.delete(name); else next.add(name); return next })
  const toggleAll = () => setSelected(allSelected ? new Set() : new Set(rows.map((row) => row.name)))
  const runBatch = async (action: string) => {
    if (!selectedRows.length) return
    if (action === 'down') {
      const names = selectedRows.slice(0, 3).map((row) => row.name).join('、')
      if (!await confirmDialog({ title: zh ? `Down 选中的 ${selectedRows.length} 个项目？` : `Down ${selectedRows.length} selected projects?`, description: zh ? `${names}${selectedRows.length > 3 ? ' 等' : ''}。这会停止并移除项目容器和网络，但保留项目文件与命名卷。` : `${names}${selectedRows.length > 3 ? ' and others' : ''}. This stops and removes project containers and networks while keeping project files and named volumes.`, confirmLabel: 'Down', danger: true })) return
    }
    batch.mutate({ names: selectedRows.map((row) => row.name), action })
  }

  return <div>
    <div className="mb-5 flex flex-wrap items-end gap-4"><div><h1 className="text-xl font-semibold tracking-tight">Compose</h1><div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 font-mono text-[10px] uppercase tracking-wider text-text-subtle"><StatusCount color="bg-success" value={running} label={zh ? '运行中' : 'running'} /><StatusCount color="bg-warning" value={degraded} label={zh ? '异常' : 'degraded'} /><StatusCount color="bg-text-subtle" value={stopped} label={zh ? '已停止' : 'stopped'} /><span>{rows.length} {zh ? '总计' : 'total'}</span></div></div><div className="ml-auto flex items-center gap-2"><div aria-hidden={!selectedRows.length} className={`flex h-9 items-center gap-1 rounded-xl border border-border bg-surface/70 px-1.5 transition-opacity ${selectedRows.length ? 'opacity-100' : 'pointer-events-none opacity-0'}`}><span className="whitespace-nowrap px-2 font-mono text-[9px] text-text-subtle">{selectedRows.length} {zh ? '已选' : 'selected'}</span><BatchAction label={zh ? '启动' : 'Start'} icon={Play} disabled={batch.isPending} run={() => void runBatch('start')} /><BatchAction label={zh ? '停止' : 'Stop'} icon={Square} disabled={batch.isPending} run={() => void runBatch('stop')} /><BatchAction label={zh ? '重启' : 'Restart'} icon={RefreshCw} disabled={batch.isPending} run={() => void runBatch('restart')} /><BatchAction label={zh ? '更新' : 'Update'} icon={Download} disabled={batch.isPending} run={() => void runBatch('update')} /><BatchAction label="Down" icon={Power} danger disabled={batch.isPending} run={() => void runBatch('down')} /></div><button disabled={create.isPending} onClick={() => void add()} className="flex h-9 items-center gap-2 rounded-xl bg-accent px-3 text-xs font-semibold text-accent-foreground disabled:opacity-60"><Plus className="size-3.5" />{t('newProject')}</button></div></div>
    {operationError && <div role="alert" className="mb-3 flex items-center rounded-xl border border-danger/30 bg-danger-subtle px-3 py-2 text-xs text-danger"><span>{operationError}</span><button type="button" onClick={() => setOperationError('')} className="ml-auto px-2 text-text-subtle hover:text-text">{zh ? '关闭' : 'Dismiss'}</button></div>}
    {query.isPending ? <LoadingState compact rows={7} label={zh ? '正在加载 Compose 项目' : 'Loading Compose projects'} /> : query.isError ? <div className="rounded-xl border border-danger/30 bg-danger-subtle py-12 text-center text-sm text-danger">{query.error.message}</div> : rows.length ? <div className="overflow-hidden rounded-2xl border border-border"><div className="hidden h-9 grid-cols-[minmax(220px,1fr)_110px_58px_58px_118px_184px_28px] items-center gap-2 border-b border-border bg-surface/45 px-3 font-mono text-[9px] uppercase tracking-[.14em] text-text-subtle lg:grid"><span className="flex items-center gap-2"><SelectionBox checked={allSelected} label={zh ? '选择全部 Compose 项目' : 'Select all Compose projects'} onChange={toggleAll} />{zh ? '项目 / 路径' : 'Project / path'}</span><span>{zh ? '状态' : 'Status'}</span><span className="text-right">{zh ? '服务' : 'Services'}</span><span className="text-right">{zh ? '容器' : 'Containers'}</span><span className="text-right">{zh ? '更新时间' : 'Updated'}</span><span className="text-right">{zh ? '快捷操作' : 'Quick actions'}</span><span /></div><div className="divide-y divide-border">{rows.map((row) => <ProjectRow key={row.id} row={row} zh={zh} selected={selected.has(row.name)} toggleSelected={() => toggleSelected(row.name)} />)}</div></div> : <div className="rounded-2xl border border-border py-16 text-center"><Boxes className="mx-auto size-5 text-text-subtle" /><p className="mt-3 text-sm font-medium">{zh ? '还没有 Compose 项目' : 'No Compose projects yet'}</p><p className="mt-1 text-xs text-text-subtle">{zh ? '创建项目后可以在这里编写和部署 Compose。' : 'Create one to author and deploy Compose here.'}</p></div>}
    {create.isError && <p className="mt-3 text-xs text-danger">{create.error.message}</p>}
  </div>
}

function ProjectRow({ row, zh, selected, toggleSelected }: { row: ComposeProject; zh: boolean; selected: boolean; toggleSelected: () => void }) {
	const nodeID = useUIStore((state) => state.currentNodeID)
  const client = useQueryClient()
  const [expanded, setExpanded] = useState(false)
  const locale = zh ? 'zh-CN' : 'en-US'
  const updated = new Date(row.updated_at)
  const statusLabel = row.status === 'running' ? (zh ? '运行中' : 'Running') : row.status === 'degraded' ? (zh ? '异常' : 'Degraded') : (zh ? '已停止' : 'Stopped')
  const services = useQuery({ queryKey: ['compose-services', nodeID, row.name], queryFn: () => api<ContainerSummary[]>(nodePath(nodeID, `/compose/${encodeURIComponent(row.name)}/services`)), enabled: expanded, refetchInterval: expanded ? 5_000 : false })
  const projectAction = useMutation({
    mutationFn: (action: string) => api(nodePath(nodeID, `/compose/${encodeURIComponent(row.name)}/${action}`), { method: 'POST' }),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['compose', nodeID] })
      void client.invalidateQueries({ queryKey: ['compose-services', nodeID, row.name] })
      void client.invalidateQueries({ queryKey: ['tasks', nodeID] })
    },
    onError: () => setExpanded(true),
  })
  const runProjectAction = async (action: string) => {
    if (action === 'down' && !await confirmDialog({ title: zh ? `Down ${row.name}？` : `Down ${row.name}?`, description: zh ? '这会停止并移除该 Compose 项目的容器和网络，但不会删除项目文件或命名卷。' : 'This stops and removes the Compose project containers and networks, but keeps project files and named volumes.', confirmLabel: 'Down', danger: true })) return
    projectAction.mutate(action)
  }
  const toggleFromRow = (event: MouseEvent<HTMLDivElement>) => {
    if ((event.target as HTMLElement).closest('a, button, input, label')) return
    setExpanded((value) => !value)
  }
  return <article className={expanded ? 'bg-surface/20' : undefined}>
    <div onClick={toggleFromRow} className="group grid min-h-16 cursor-pointer grid-cols-[minmax(0,1fr)_28px] items-center gap-2 px-3 transition-colors hover:bg-surface/55 lg:grid-cols-[minmax(220px,1fr)_110px_58px_58px_118px_184px_28px]">
      <div className="flex min-w-0 items-center gap-2 py-2">
        <SelectionBox checked={selected} label={zh ? `选择 ${row.name}` : `Select ${row.name}`} onChange={toggleSelected} />
        <span className="grid size-8 shrink-0 place-items-center rounded-xl border border-border bg-surface/70 text-text-subtle transition-colors group-hover:text-accent"><FolderCog className="size-3.5" strokeWidth={1.6} /></span>
        <span className="min-w-0 flex-1"><Link to="/compose/$projectName" params={{ projectName: row.name }} className="inline-block max-w-full truncate rounded-sm align-top text-[13px] font-medium leading-4 outline-none hover:text-accent focus-visible:ring-1 focus-visible:ring-accent">{row.name}</Link><span className="mt-1 block truncate font-mono text-[9px] leading-3 text-text-subtle" title={row.path}>{row.path}</span><span className="mt-1 flex items-center gap-3 font-mono text-[9px] text-text-subtle lg:hidden"><span className="flex items-center gap-1.5 font-sans text-[10px] text-text-muted"><span className={`size-1.5 rounded-full ${statusColor(row.status)}`} />{statusLabel}</span><span>{row.services} {zh ? '服务' : 'svc'}</span><span>{row.containers} {zh ? '容器' : 'ctr'}</span><span>{updated.toLocaleString(locale, { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })}</span></span></span>
      </div>
      <div className="hidden min-w-0 lg:block"><span className={`inline-flex items-center gap-1.5 rounded-lg border px-2 py-1 text-[10px] font-medium ${projectStatusTone(row.status)}`}><span className={`size-1.5 rounded-full ${statusColor(row.status)}`} />{statusLabel}</span></div>
      <Metric value={row.services} label={zh ? '服务' : 'svc'} />
      <Metric value={row.containers} label={zh ? '容器' : 'ctr'} />
      <div className="hidden text-right lg:block"><p className="font-mono text-[10px] text-text-muted">{updated.toLocaleDateString(locale)}</p><p className="mt-0.5 font-mono text-[9px] text-text-subtle">{updated.toLocaleTimeString(locale, { hour: '2-digit', minute: '2-digit' })}</p></div>
      <div className="hidden justify-end lg:flex"><ProjectActions row={row} zh={zh} pending={projectAction.isPending} activeAction={projectAction.variables} run={(action) => void runProjectAction(action)} /></div>
      <button type="button" aria-expanded={expanded} aria-controls={`compose-project-${row.id}`} aria-label={expanded ? (zh ? `收起 ${row.name}` : `Collapse ${row.name}`) : (zh ? `展开 ${row.name}` : `Expand ${row.name}`)} title={expanded ? (zh ? '收起容器' : 'Collapse containers') : (zh ? '展开容器' : 'Expand containers')} onClick={() => setExpanded((value) => !value)} className="grid size-7 place-items-center rounded-lg border border-transparent text-text-subtle transition-colors hover:border-border hover:bg-surface-hover hover:text-text"><ChevronDown className={`size-3.5 transition-transform ${expanded ? 'rotate-180' : ''}`} /></button>
    </div>
    {expanded && <div id={`compose-project-${row.id}`} className="border-t border-border bg-background/45 px-3 py-2 lg:px-12">
      <div className="mb-2 flex items-center gap-1 border-b border-border pb-2 lg:hidden"><span className="mr-2 text-[10px] font-medium text-text-muted">{zh ? '项目操作' : 'Project actions'}</span><ProjectActions row={row} zh={zh} pending={projectAction.isPending} activeAction={projectAction.variables} run={(action) => void runProjectAction(action)} /></div>
      {projectAction.isError && <InlineError value={projectAction.error.message} />}
      {services.isPending ? <LoadingState compact rows={2} label={zh ? '正在加载项目容器' : 'Loading project containers'} /> : services.isError ? <InlineError value={services.error.message} /> : services.data?.length ? <div className="divide-y divide-border">{services.data.map((container) => <ContainerRow key={container.id} row={container} projectName={row.name} zh={zh} />)}</div> : <p className="py-5 text-center text-[11px] text-text-subtle">{zh ? '该项目当前没有容器。可使用“更新”创建并启动服务。' : 'This project has no containers. Use Update to create and start its services.'}</p>}
    </div>}
  </article>
}

function Metric({ value, label }: { value: number; label: string }) {
  return <p className="hidden text-right lg:block"><span className="font-mono text-xs font-medium tabular-nums text-text">{value}</span><span className="ml-1 text-[9px] text-text-subtle">{label}</span></p>
}

function ProjectActions({ row, zh, pending, activeAction, run }: { row: ComposeProject; zh: boolean; pending: boolean; activeAction?: string; run: (action: string) => void }) {
  const actions: { name: string; label: string; icon: LucideIcon; danger?: boolean; disabled?: boolean }[] = [
    { name: 'start', label: zh ? '启动项目' : 'Start project', icon: Play, disabled: row.status === 'running' || row.containers === 0 },
    { name: 'stop', label: zh ? '停止项目' : 'Stop project', icon: Square, disabled: row.status === 'stopped' },
    { name: 'restart', label: zh ? '重启项目' : 'Restart project', icon: RefreshCw, disabled: row.containers === 0 },
    { name: 'update', label: zh ? '更新项目' : 'Update project', icon: Download },
    { name: 'down', label: 'Down', icon: Power, danger: true },
  ]
  return <div className="flex items-center gap-0.5">{actions.map((action) => <ActionButton key={action.name} label={action.label} icon={pending && activeAction === action.name ? LoaderCircle : action.icon} spinning={pending && activeAction === action.name} danger={action.danger} disabled={pending || action.disabled} run={() => run(action.name)} />)}</div>
}

function ContainerRow({ row, projectName, zh }: { row: ContainerSummary; projectName: string; zh: boolean }) {
	const nodeID = useUIStore((state) => state.currentNodeID)
  const client = useQueryClient()
  const action = useMutation({
    mutationFn: (name: string) => api(nodePath(nodeID, `/containers/${encodeURIComponent(row.id)}/${name}`), { method: 'POST' }),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['compose', nodeID] })
      void client.invalidateQueries({ queryKey: ['compose-services', nodeID, projectName] })
      void client.invalidateQueries({ queryKey: ['containers', nodeID] })
    },
  })
  const primaryAction = row.state === 'running' ? 'stop' : row.state === 'paused' ? 'unpause' : 'start'
  const primaryLabel = row.state === 'running' ? (zh ? '停止容器' : 'Stop container') : row.state === 'paused' ? (zh ? '恢复容器' : 'Resume container') : (zh ? '启动容器' : 'Start container')
  const serviceName = row.labels['com.docker.compose.service'] || row.name
  return <div className="grid min-h-12 grid-cols-[minmax(0,1fr)_auto] items-center gap-3 py-1.5">
    <Link to="/containers/$containerId" params={{ containerId: row.id }} className="flex min-w-0 items-center gap-2.5 rounded-md outline-none focus-visible:ring-1 focus-visible:ring-accent"><span className="relative grid size-7 shrink-0 place-items-center rounded-md border border-border bg-surface text-text-subtle"><Box className="size-3.5" /><span className={`absolute -bottom-0.5 -right-0.5 size-2 rounded-full ring-2 ring-background ${containerStateColor(row.state)}`} /></span><span className="min-w-0"><span className="flex min-w-0 items-center gap-2"><span className="truncate text-[12px] font-medium">{serviceName}</span><span className="shrink-0 font-mono text-[9px] text-text-subtle">{row.id.slice(0, 12)}</span></span><span className="mt-0.5 block truncate font-mono text-[9px] text-text-subtle" title={`${row.name} · ${row.image}`}>{row.name} · {row.image}</span><span className="mt-0.5 block truncate text-[9px] text-text-muted lg:hidden">{containerStateLabel(row.state, zh)} · {row.status}</span></span></Link>
    <div className="flex items-center gap-1"><span className="mr-2 hidden max-w-48 truncate text-[10px] text-text-muted lg:block" title={row.status}>{containerStateLabel(row.state, zh)} · {row.status}</span><a href={`/containers/${row.id}#logs`} title={zh ? '查看日志' : 'View logs'} aria-label={zh ? `查看 ${row.name} 日志` : `View logs for ${row.name}`} className="grid size-7 place-items-center rounded-md text-text-subtle hover:bg-surface-hover hover:text-text"><FileText className="size-3.5" /></a><a href={row.state === 'running' ? `/containers/${row.id}#terminal` : undefined} title={zh ? '打开终端' : 'Open terminal'} aria-label={zh ? `打开 ${row.name} 终端` : `Open terminal for ${row.name}`} aria-disabled={row.state !== 'running'} className={`grid size-7 place-items-center rounded-md ${row.state === 'running' ? 'text-text-subtle hover:bg-surface-hover hover:text-text' : 'cursor-not-allowed text-text-subtle opacity-25'}`}><SquareTerminal className="size-3.5" /></a><ActionButton label={zh ? '重启容器' : 'Restart container'} icon={action.isPending && action.variables === 'restart' ? LoaderCircle : RefreshCw} spinning={action.isPending && action.variables === 'restart'} disabled={action.isPending} run={() => action.mutate('restart')} /><ActionButton label={primaryLabel} icon={action.isPending && action.variables === primaryAction ? LoaderCircle : primaryAction === 'stop' ? Square : Play} spinning={action.isPending && action.variables === primaryAction} active={primaryAction !== 'stop'} disabled={action.isPending} run={() => action.mutate(primaryAction)} /></div>
    {action.isError && <div className="col-span-2"><InlineError value={action.error.message} /></div>}
  </div>
}

function ActionButton({ label, icon: Icon, run, active = false, danger = false, spinning = false, disabled = false }: { label: string; icon: LucideIcon; run: () => void; active?: boolean; danger?: boolean; spinning?: boolean; disabled?: boolean }) {
  return <button type="button" title={label} aria-label={label} disabled={disabled} onClick={run} className={`grid size-7 place-items-center rounded-lg border border-transparent transition-colors disabled:cursor-not-allowed disabled:opacity-30 ${danger ? 'text-danger hover:border-danger/20 hover:bg-danger-subtle' : active ? 'bg-accent/10 text-accent hover:bg-accent/15' : 'text-text-subtle hover:border-border hover:bg-surface-hover hover:text-text'}`}><Icon className={`size-3.5 ${spinning ? 'animate-spin' : ''}`} /></button>
}

function StatusCount({ color, value, label }: { color: string; value: number; label: string }) { return <span className="flex items-center gap-1.5"><span className={`size-1.5 rounded-full ${color}`} /><b className="font-medium text-text-muted">{value}</b>{label}</span> }
function SelectionBox({ checked, label, onChange }: { checked: boolean; label: string; onChange: () => void }) { return <label className="grid size-4 shrink-0 cursor-pointer place-items-center" title={label}><input type="checkbox" checked={checked} onChange={onChange} aria-label={label} className="peer sr-only" /><span className="grid size-3.5 place-items-center rounded-[5px] border border-border bg-background transition-colors peer-checked:border-accent peer-checked:bg-accent after:size-1.5 after:rounded-[2px] after:bg-accent-foreground after:opacity-0 after:content-[''] peer-checked:after:opacity-100" /></label> }
function BatchAction({ label, icon: Icon, run, danger = false, disabled = false }: { label: string; icon: LucideIcon; run: () => void; danger?: boolean; disabled?: boolean }) { return <button type="button" title={label} aria-label={label} disabled={disabled} onClick={run} className={`grid size-7 place-items-center rounded-lg transition-colors disabled:opacity-40 ${danger ? 'text-danger hover:bg-danger-subtle' : 'text-text-subtle hover:bg-surface-hover hover:text-text'}`}><Icon className={`size-3.5 ${disabled ? 'animate-pulse' : ''}`} /></button> }

function InlineError({ value }: { value: string }) {
  return <p role="alert" className="mb-2 border-l-2 border-danger bg-danger-subtle px-2 py-1.5 text-[10px] text-danger">{value}</p>
}

function statusColor(status: string) {
  if (status === 'running') return 'bg-success'
  if (status === 'degraded') return 'bg-warning'
  return 'bg-text-subtle'
}

function projectStatusTone(status: string) {
  if (status === 'running') return 'border-success/20 bg-success/10 text-success'
  if (status === 'degraded') return 'border-warning/20 bg-warning/10 text-warning'
  return 'border-border bg-surface/55 text-text-muted'
}

function containerStateColor(state: string) {
  if (state === 'running') return 'bg-success'
  if (state === 'paused') return 'bg-warning'
  if (state === 'restarting') return 'bg-accent'
  return 'bg-text-subtle'
}

function containerStateLabel(state: string, zh: boolean) {
  if (state === 'running') return zh ? '运行中' : 'Running'
  if (state === 'paused') return zh ? '已暂停' : 'Paused'
  if (state === 'restarting') return zh ? '重启中' : 'Restarting'
  return zh ? '已停止' : 'Stopped'
}
