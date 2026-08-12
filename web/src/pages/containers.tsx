import { Menu } from '@base-ui/react/menu'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Box, Clock3, Cpu, FileText, Layers3, LoaderCircle, MemoryStick, MoreHorizontal, Network, OctagonX, Package, Pause, Pencil, Play, RefreshCw, Search, Square, SquareTerminal, Trash2, type LucideIcon } from 'lucide-react'
import { useState } from 'react'
import { LoadingState } from '../components/ui/loading-state'
import type { ContainerMetrics, ContainerSummary } from '../features/containers/types'
import { api } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { confirmDialog, promptDialog } from '../stores/dialog'

const ports = (value: ContainerSummary) => value.ports.slice(0, 2).map((port) => port.public_port ? `${port.public_port}→${port.private_port}/${port.type}` : `${port.private_port}/${port.type}`).join(', ') || '—'
const memory = (bytes: number) => bytes >= 1024 ** 3 ? `${(bytes / 1024 ** 3).toFixed(2)} GB` : `${(bytes / 1024 ** 2).toFixed(0)} MB`
const uptime = (seconds: number) => !seconds ? '—' : seconds >= 86400 ? `${Math.floor(seconds / 86400)}d` : seconds >= 3600 ? `${Math.floor(seconds / 3600)}h` : `${Math.floor(seconds / 60)}m`
const stateLabel = (state: string, zh: boolean) => zh ? ({ running: '运行中', paused: '已暂停', restarting: '重启中', exited: '已停止', dead: '异常', created: '已创建' }[state] ?? state) : state
const stateColor = (state: string) => state === 'running' ? 'bg-success' : state === 'paused' || state === 'restarting' ? 'bg-amber-400' : state === 'dead' ? 'bg-red-400' : 'bg-text-subtle'
const stateTone = (state: string) => state === 'running' ? 'border-success/20 bg-success/10 text-success' : state === 'paused' || state === 'restarting' ? 'border-amber-400/20 bg-amber-400/10 text-amber-500' : state === 'dead' ? 'border-red-400/20 bg-red-400/10 text-red-400' : 'border-border bg-muted text-text-muted'

export function ContainersPage() {
  const [filter, setFilter] = useState('')
  const [selected, setSelected] = useState<Set<string>>(() => new Set())
  const [operationError, setOperationError] = useState('')
  const { t, language } = useI18n(); const zh = language === 'zh-CN'
  const queryClient = useQueryClient()
  const query = useQuery({ queryKey: ['containers'], queryFn: () => api<ContainerSummary[]>('/containers'), refetchInterval: 10_000 })
  const metrics = useQuery({ queryKey: ['container-metrics'], queryFn: () => api<ContainerMetrics[]>('/containers/metrics'), refetchInterval: 5_000 })
  const action = useMutation({ mutationFn: ({ id, name }: { id: string; name: string }) => api(`/containers/${id}/${name}`, { method: 'POST' }), onMutate: () => setOperationError(''), onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['containers'] }); queryClient.invalidateQueries({ queryKey: ['container-metrics'] }) }, onError: (error) => setOperationError(error.message) })
  const batch = useMutation({ mutationFn: ({ ids, name }: { ids: string[]; name: string }) => api<{ results: { id: string; success: boolean }[] }>('/containers/batch', { method: 'POST', body: JSON.stringify({ ids, action: name, remove_volumes: false }) }), onMutate: () => setOperationError(''), onSuccess: async (result) => { const failed = result.results.filter((item) => !item.success).length; setSelected(new Set()); if (failed) setOperationError(zh ? `${failed} 个容器操作失败，请检查容器当前状态。` : `${failed} container operations failed. Check their current state.`); await Promise.all([queryClient.invalidateQueries({ queryKey: ['containers'] }), queryClient.invalidateQueries({ queryKey: ['container-metrics'] })]) }, onError: (error) => setOperationError(error.message) })
  const metricsById = new Map(metrics.data?.map((row) => [row.id, row]))
  const rows = query.data?.map((row) => ({ ...row, ...metricsById.get(row.id) })).filter((row) => `${row.id} ${row.name} ${row.image} ${row.state} ${row.status} ${ports(row)}`.toLowerCase().includes(filter.toLowerCase()))
  const running = query.data?.filter((row) => row.state === 'running').length ?? 0
  const paused = query.data?.filter((row) => row.state === 'paused').length ?? 0
  const stopped = (query.data?.length ?? 0) - running - paused
  const selectedRows = query.data?.filter((row) => selected.has(row.id)) ?? []
  const allVisibleSelected = !!rows?.length && rows.every((row) => selected.has(row.id))
  const toggleSelected = (id: string) => setSelected((current) => { const next = new Set(current); if (next.has(id)) next.delete(id); else next.add(id); return next })
  const toggleVisible = () => setSelected((current) => { const next = new Set(current); if (allVisibleSelected) rows?.forEach((row) => next.delete(row.id)); else rows?.forEach((row) => next.add(row.id)); return next })
  const runBatch = async (name: string) => {
    if (!selectedRows.length) return
    if (name === 'remove') {
      const names = selectedRows.slice(0, 3).map((row) => row.name).join('、')
      if (!await confirmDialog({ title: zh ? `删除选中的 ${selectedRows.length} 个容器？` : `Remove ${selectedRows.length} selected containers?`, description: zh ? `${names}${selectedRows.length > 3 ? ' 等' : ''}。运行中的容器会删除失败，挂载卷将保留。` : `${names}${selectedRows.length > 3 ? ' and others' : ''}. Running containers will fail; attached volumes are preserved.`, confirmLabel: zh ? '批量删除' : 'Remove selected', danger: true })) return
    }
    batch.mutate({ ids: selectedRows.map((row) => row.id), name })
  }
  const renameContainer = async (row: ContainerSummary) => {
    const name = await promptDialog({ title: zh ? `重命名 ${row.name}` : `Rename ${row.name}`, description: zh ? '仅修改容器名称，不会重建容器或改变其配置。' : 'Only the container name changes; the container is not recreated.', confirmLabel: zh ? '保存名称' : 'Save name', input: { label: zh ? '新容器名称' : 'New container name', initialValue: row.name } })
    if (!name?.trim() || name.trim() === row.name) return
    setOperationError('')
    try {
      await api(`/containers/${row.id}`, { method: 'PATCH', body: JSON.stringify({ name: name.trim() }) })
      await queryClient.invalidateQueries({ queryKey: ['containers'] })
    } catch (error) { setOperationError(error instanceof Error ? error.message : String(error)) }
  }
  const removeContainer = async (row: ContainerSummary) => {
    const value = await promptDialog({ title: zh ? `永久删除 ${row.name}` : `Permanently remove ${row.name}`, description: zh ? '容器必须先停止。删除后无法恢复，已挂载的存储卷将保留。' : 'The container must be stopped first. This cannot be undone; attached volumes are preserved.', confirmLabel: zh ? '删除容器' : 'Remove container', danger: true, input: { label: zh ? `输入 ${row.name} 以确认` : `Type ${row.name} to confirm`, requiredValue: row.name } })
    if (value !== row.name) return
    setOperationError('')
    try {
      await api(`/containers/${row.id}?remove_volumes=false`, { method: 'DELETE' })
      await queryClient.invalidateQueries({ queryKey: ['containers'] })
      await queryClient.invalidateQueries({ queryKey: ['container-metrics'] })
    } catch (error) { setOperationError(error instanceof Error ? error.message : String(error)) }
  }
  const killContainer = async (row: ContainerSummary) => {
    if (!await confirmDialog({ title: zh ? `强制终止 ${row.name}？` : `Force kill ${row.name}?`, description: zh ? '容器主进程会被立即终止，不会等待正常退出。' : 'The main process will be killed immediately without a graceful shutdown.', confirmLabel: zh ? '强制终止' : 'Force kill', danger: true })) return
    action.mutate({ id: row.id, name: 'kill' })
  }

  return <div>
    <div className="mb-5 flex flex-wrap items-end gap-4">
      <div>
        <h1 className="text-xl font-semibold tracking-tight">{t('containers')}</h1>
        <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 font-mono text-[10px] uppercase tracking-wider text-text-subtle">
          <StatusCount color="bg-success" value={running} label={zh ? '运行中' : 'running'} />
          <StatusCount color="bg-amber-400" value={paused} label={zh ? '已暂停' : 'paused'} />
          <StatusCount color="bg-text-subtle" value={stopped} label={zh ? '已停止' : 'stopped'} />
          <span>{query.data?.length ?? 0} {zh ? '总计' : 'total'}</span>
        </div>
      </div>
      <div className="ml-auto flex h-9 w-[286px] items-center justify-end">
        <div aria-hidden={!selected.size} className={`flex h-9 items-center gap-1 rounded-xl border border-border bg-surface/70 px-1.5 transition-opacity ${selected.size ? 'opacity-100' : 'pointer-events-none opacity-0'}`}>
          <span className="whitespace-nowrap px-2 font-mono text-[9px] text-text-subtle">{selected.size} {zh ? '已选' : 'selected'}</span>
          <BatchAction label={zh ? '启动' : 'Start'} icon={Play} disabled={batch.isPending} run={() => void runBatch('start')} />
          <BatchAction label={zh ? '停止' : 'Stop'} icon={Square} disabled={batch.isPending} run={() => void runBatch('stop')} />
          <BatchAction label={zh ? '重启' : 'Restart'} icon={RefreshCw} disabled={batch.isPending} run={() => void runBatch('restart')} />
          <BatchAction label={zh ? '删除' : 'Remove'} icon={Trash2} danger disabled={batch.isPending} run={() => void runBatch('remove')} />
        </div>
      </div>
      <label className="flex h-9 w-full items-center gap-2 rounded-xl border border-border bg-surface px-3 text-xs text-text-subtle sm:w-64">
        <Search className="size-3.5 shrink-0" />
        <input value={filter} onChange={(event) => setFilter(event.target.value)} className="min-w-0 flex-1 bg-transparent outline-none" placeholder={zh ? '名称、镜像、状态、端口…' : 'Name, image, status, port…'} />
      </label>
    </div>

    {query.isPending && <LoadingState compact rows={7} label={zh ? '正在加载容器' : 'Loading containers'} />}
    {query.isError && <div className="rounded-xl border border-red-900/50 py-12 text-center text-sm text-red-400">{zh ? '无法连接 Docker Engine。' : 'Unable to reach the Docker Engine.'}</div>}
    {operationError && <div role="alert" className="mb-3 flex items-center rounded-xl border border-red-900/50 bg-red-950/20 px-3 py-2 text-xs text-red-400"><span>{operationError}</span><button type="button" onClick={() => setOperationError('')} className="ml-auto px-2 text-text-subtle hover:text-text">{zh ? '关闭' : 'Dismiss'}</button></div>}
    {rows && <div className="overflow-hidden rounded-2xl border border-border">
      <div className="hidden h-9 grid-cols-[minmax(220px,1fr)_150px_120px_58px_72px_58px_144px] items-center gap-3 border-b border-border bg-surface/45 px-3 font-mono text-[9px] uppercase tracking-[.14em] text-text-subtle lg:grid xl:grid-cols-[190px_minmax(150px,1fr)_110px_145px_130px_58px_72px_58px_220px] 2xl:grid-cols-[210px_minmax(170px,1fr)_120px_150px_140px_60px_76px_60px_252px]">
        <span className="flex items-center gap-2"><SelectionBox checked={allVisibleSelected} label={zh ? '选择全部可见容器' : 'Select all visible containers'} onChange={toggleVisible} />{zh ? '容器' : 'Container'}</span><span className="hidden xl:block">{zh ? '镜像' : 'Image'}</span><span className="hidden xl:block">{zh ? '来源' : 'Source'}</span><span>{zh ? '状态' : 'State'}</span><span>{zh ? '端口' : 'Ports'}</span><span>CPU</span><span>{zh ? '内存' : 'Memory'}</span><span>{zh ? '运行时间' : 'Uptime'}</span><span className="text-right">{zh ? '快捷操作' : 'Quick actions'}</span>
      </div>
      <div className="divide-y divide-border">
        {rows.map((row) => {
          const pending = action.isPending && action.variables?.id === row.id
          const primaryAction = row.state === 'running' ? 'stop' : row.state === 'paused' ? 'unpause' : 'start'
          const primaryLabel = row.state === 'running' ? (zh ? '停止' : 'Stop') : row.state === 'paused' ? (zh ? '恢复' : 'Unpause') : (zh ? '启动' : 'Start')
          const composeProject = row.labels['com.docker.compose.project']
          const composeService = row.labels['com.docker.compose.service']
          return <div key={row.id} className="group grid min-h-16 grid-cols-[minmax(0,1fr)_auto] items-center gap-3 px-3 transition-colors hover:bg-surface/55 lg:grid-cols-[minmax(220px,1fr)_150px_120px_58px_72px_58px_144px] xl:grid-cols-[190px_minmax(150px,1fr)_110px_145px_130px_58px_72px_58px_220px] 2xl:grid-cols-[210px_minmax(170px,1fr)_120px_150px_140px_60px_76px_60px_252px]">
            <div className="flex min-w-0 items-center gap-2 py-2">
              <SelectionBox checked={selected.has(row.id)} label={zh ? `选择 ${row.name}` : `Select ${row.name}`} onChange={() => toggleSelected(row.id)} />
              <Link to="/containers/$containerId" params={{ containerId: row.id }} className="flex min-w-0 flex-1 items-center gap-3">
                <span className="relative grid size-8 shrink-0 place-items-center rounded-xl border border-border bg-surface/70 text-text-subtle transition-colors group-hover:text-accent"><Box className="size-3.5" strokeWidth={1.6} /><span className={`absolute -bottom-0.5 -right-0.5 size-2 rounded-full ring-2 ring-background ${stateColor(row.state)}`} /></span>
                <span className="min-w-0">
                  <span className="block truncate text-[13px] font-medium">{row.name}</span>
                  <span className="mt-1 block truncate font-mono text-[9px] text-text-subtle"><span className="text-text-muted">{row.id.slice(0, 12)}</span><span className="xl:hidden"> · {row.image}{composeProject ? ` · Compose/${composeProject}` : ` · ${zh ? '独立容器' : 'Standalone'}`}</span></span>
                </span>
              </Link>
            </div>
            <div className="hidden min-w-0 items-center gap-2 xl:flex"><span className="grid size-7 shrink-0 place-items-center rounded-lg bg-muted text-text-subtle"><Package className="size-3.5" strokeWidth={1.6} /></span><span className="min-w-0"><span className="block truncate text-[11px] font-medium text-text-muted" title={row.image}>{row.image}</span><span className="mt-1 block truncate font-mono text-[9px] text-text-subtle" title={row.command}>{row.command || '—'}</span></span></div>
            <div className="hidden min-w-0 xl:block"><span className={`inline-flex max-w-full items-center gap-1.5 rounded-lg border px-2 py-1 text-[10px] ${composeProject ? 'border-accent/20 bg-accent/[.07] text-accent' : 'border-border bg-surface/55 text-text-muted'}`}>{composeProject ? <Layers3 className="size-3 shrink-0" /> : <Box className="size-3 shrink-0" />}<span className="truncate">{composeProject ? 'Compose' : (zh ? '独立容器' : 'Standalone')}</span></span><p className="mt-1 truncate pl-1 font-mono text-[9px] text-text-subtle" title={composeProject}>{composeProject ? composeService || composeProject : 'docker'}</p></div>
            <div className="hidden min-w-0 lg:block">
              <span className={`inline-flex items-center gap-1.5 rounded-lg border px-2 py-1 text-[10px] font-medium capitalize ${stateTone(row.state)}`}><span className={`size-1.5 rounded-full ${stateColor(row.state)}`} />{stateLabel(row.state, zh)}</span>
              <p className="mt-1 truncate pl-1 text-[9px] text-text-subtle" title={row.status}>{row.status}</p>
            </div>
            <div className="hidden min-w-0 items-center gap-2 lg:flex"><Network className="size-3.5 shrink-0 text-text-subtle" strokeWidth={1.5} /><span className="truncate rounded-md border border-border bg-background/50 px-1.5 py-1 font-mono text-[9px] text-text-muted" title={ports(row)}>{ports(row)}</span></div>
            <Metric icon={Cpu} value={row.state === 'running' ? `${row.cpu_percent.toFixed(1)}%` : '—'} />
            <Metric icon={MemoryStick} value={row.state === 'running' ? memory(row.memory_bytes) : '—'} />
            <Metric icon={Clock3} value={uptime(row.uptime_seconds)} />
            <div className="flex items-center justify-end gap-1">
              <QuickLink label={zh ? '日志' : 'Logs'} href={`/containers/${row.id}#logs`} icon={FileText} />
              <QuickLink label={zh ? '终端' : 'Terminal'} href={`/containers/${row.id}#terminal`} icon={SquareTerminal} disabled={row.state !== 'running'} />
              <QuickAction label={zh ? '重启' : 'Restart'} icon={RefreshCw} disabled={pending} run={() => action.mutate({ id: row.id, name: 'restart' })} />
              <QuickAction label={primaryLabel} icon={pending ? LoaderCircle : primaryAction === 'stop' ? Square : Play} active={primaryAction === 'start' || primaryAction === 'unpause'} spinning={pending} disabled={pending} run={() => action.mutate({ id: row.id, name: primaryAction })} />
              <span className="hidden xl:block"><QuickAction label={zh ? '重命名' : 'Rename'} icon={Pencil} run={() => void renameContainer(row)} /></span>
              {(row.state === 'running' || row.state === 'paused') && <span className="hidden xl:block"><QuickAction label={row.state === 'running' ? (zh ? '暂停' : 'Pause') : (zh ? '恢复' : 'Unpause')} icon={row.state === 'running' ? Pause : Play} run={() => action.mutate({ id: row.id, name: row.state === 'running' ? 'pause' : 'unpause' })} /></span>}
              {(row.state === 'running' || row.state === 'paused') && <span className="hidden 2xl:block"><QuickAction label={zh ? '强制终止' : 'Force kill'} icon={OctagonX} run={() => void killContainer(row)} /></span>}
              <span className="hidden 2xl:block"><QuickAction label={zh ? '删除容器' : 'Remove container'} icon={Trash2} run={() => void removeContainer(row)} /></span>
              <ContainerMenu row={row} zh={zh} rename={() => void renameContainer(row)} pause={() => action.mutate({ id: row.id, name: 'pause' })} unpause={() => action.mutate({ id: row.id, name: 'unpause' })} kill={() => void killContainer(row)} remove={() => void removeContainer(row)} />
            </div>
          </div>
        })}
      </div>
      {rows.length === 0 && <div className="grid min-h-40 place-items-center text-xs text-text-subtle">{filter ? (zh ? '没有匹配的容器' : 'No matching containers') : (zh ? '暂无容器' : 'No containers found')}</div>}
    </div>}
  </div>
}

function StatusCount({ color, value, label }: { color: string; value: number; label: string }) { return <span className="flex items-center gap-1.5"><span className={`size-1.5 rounded-full ${color}`} /><b className="font-medium text-text-muted">{value}</b>{label}</span> }
function SelectionBox({ checked, label, onChange }: { checked: boolean; label: string; onChange: () => void }) { return <label className="grid size-4 shrink-0 cursor-pointer place-items-center" title={label}><input type="checkbox" checked={checked} onChange={onChange} aria-label={label} className="peer sr-only" /><span className="grid size-3.5 place-items-center rounded-[5px] border border-border bg-background transition-colors peer-checked:border-accent peer-checked:bg-accent after:size-1.5 after:rounded-[2px] after:bg-accent-foreground after:opacity-0 after:content-[''] peer-checked:after:opacity-100" /></label> }
function BatchAction({ label, icon: Icon, run, danger = false, disabled = false }: { label: string; icon: LucideIcon; run: () => void; danger?: boolean; disabled?: boolean }) { return <button type="button" title={label} aria-label={label} disabled={disabled} onClick={run} className={`grid size-7 place-items-center rounded-lg transition-colors disabled:opacity-40 ${danger ? 'text-red-400 hover:bg-red-400/10' : 'text-text-subtle hover:bg-surface-hover hover:text-text'}`}><Icon className={`size-3.5 ${disabled ? 'animate-pulse' : ''}`} /></button> }
function Metric({ icon: Icon, value }: { icon: LucideIcon; value: string }) { return <div className="hidden min-w-0 items-center gap-1.5 lg:flex"><Icon className="size-3 shrink-0 text-text-subtle" strokeWidth={1.6} /><span className="truncate font-mono text-[10px] tabular-nums text-text-muted">{value}</span></div> }
function QuickAction({ label, icon: Icon, run, active = false, spinning = false, disabled = false }: { label: string; icon: LucideIcon; run: () => void; active?: boolean; spinning?: boolean; disabled?: boolean }) { return <button type="button" title={label} aria-label={label} disabled={disabled} onClick={run} className={`grid size-7 place-items-center rounded-lg border transition-colors disabled:cursor-wait disabled:opacity-50 ${active ? 'border-accent/25 bg-accent/10 text-accent hover:bg-accent/15' : 'border-transparent text-text-subtle hover:border-border hover:bg-surface-hover hover:text-text'}`}><Icon className={`size-3.5 ${spinning ? 'animate-spin' : ''}`} /></button> }
function QuickLink({ label, href, icon: Icon, disabled = false }: { label: string; href: string; icon: LucideIcon; disabled?: boolean }) { return <a href={disabled ? undefined : href} title={label} aria-label={label} aria-disabled={disabled} className={`grid size-7 place-items-center rounded-lg border border-transparent text-text-subtle transition-colors ${disabled ? 'cursor-not-allowed opacity-25' : 'hover:border-border hover:bg-surface-hover hover:text-text'}`}><Icon className="size-3.5" /></a> }
function ContainerMenu({ row, zh, rename, pause, unpause, kill, remove }: { row: ContainerSummary; zh: boolean; rename: () => void; pause: () => void; unpause: () => void; kill: () => void; remove: () => void }) { return <Menu.Root><Menu.Trigger title={zh ? '更多操作' : 'More actions'} aria-label={zh ? '更多操作' : 'More actions'} className="grid size-7 place-items-center rounded-lg border border-transparent text-text-subtle transition-colors hover:border-border hover:bg-surface-hover hover:text-text data-[popup-open]:border-accent/25 data-[popup-open]:bg-accent/10 data-[popup-open]:text-accent 2xl:hidden"><MoreHorizontal className="size-3.5" /></Menu.Trigger><Menu.Portal><Menu.Positioner side="bottom" align="end" sideOffset={6} collisionPadding={10} className="z-[90] outline-none"><Menu.Popup className="w-44 origin-[var(--transform-origin)] overflow-hidden rounded-xl border border-border bg-elevated p-1.5 shadow-[0_18px_50px_rgba(0,0,0,.34)] outline-none transition-[transform,opacity] duration-150 data-[ending-style]:scale-[.97] data-[ending-style]:opacity-0 data-[starting-style]:scale-[.97] data-[starting-style]:opacity-0"><MenuAction className="xl:hidden" icon={Pencil} label={zh ? '重命名' : 'Rename'} run={rename} />{row.state === 'running' && <MenuAction className="xl:hidden" icon={Pause} label={zh ? '暂停' : 'Pause'} run={pause} />}{row.state === 'paused' && <MenuAction className="xl:hidden" icon={Play} label={zh ? '恢复' : 'Unpause'} run={unpause} />}{(row.state === 'running' || row.state === 'paused') && <MenuAction icon={OctagonX} label={zh ? '强制终止' : 'Force kill'} danger run={kill} />}<Menu.Separator className="my-1 h-px bg-border" /><MenuAction icon={Trash2} label={zh ? '删除容器' : 'Remove container'} danger run={remove} /></Menu.Popup></Menu.Positioner></Menu.Portal></Menu.Root> }
function MenuAction({ label, icon: Icon, run, danger = false, className = '' }: { label: string; icon: LucideIcon; run: () => void; danger?: boolean; className?: string }) { return <Menu.Item onClick={run} className={`flex h-8 cursor-default items-center gap-2 rounded-lg px-2.5 text-left text-xs outline-none transition-colors data-[highlighted]:bg-surface-hover ${danger ? 'text-red-400' : 'text-text-muted data-[highlighted]:text-text'} ${className}`}><Icon className="size-3.5" />{label}</Menu.Item> }
