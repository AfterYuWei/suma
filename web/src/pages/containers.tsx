import { useSmallScreen } from '../features/containers/use-small-screen'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { ChevronDown, ChevronRight, CircleAlert, FileText, FolderOpen, HeartPulse, MoreHorizontal, OctagonX, Pause, Pencil, Play, RefreshCw, Search, Square, SquareTerminal, Trash2, X } from 'lucide-react'
import { Fragment, useEffect, useMemo, useRef, useState } from 'react'
import { ContainerFilters, SortMenu } from '../components/docker/container-filters'
import { DiagnosticsContent, MemoryBar, ObservationOverlay, ResourceTrend } from '../components/docker/container-observation'
import { ContainerOverviewContent } from '../components/docker/container-overview'
import { defaultContainerView, useContainerViewStore, type ContainerView } from '../stores/container-view'
import { isAbnormal, selectContainers } from '../features/containers/observation'
import { Sheet, SheetContent, SheetHeader, SheetTitle } from '../components/ui/sheet'
import { ContainerPorts } from '../components/docker/container-ports'
import { Alert, AlertAction, AlertDescription } from '../components/ui/alert'
import { Badge } from '../components/ui/badge'
import { Button } from '../components/ui/button'
import { Checkbox } from '../components/ui/checkbox'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from '../components/ui/dropdown-menu'
import { ErrorState } from '../components/ui/error-state'
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupInput } from '../components/ui/input-group'
import { ListShell } from '../components/ui/list-shell'
import { ListPagination } from '../components/ui/list-pagination'
import { useListPagination } from '../components/ui/use-list-pagination'
import { LoadingState } from '../components/ui/loading-state'
import { Spinner } from '../components/ui/spinner'
import { StatusBadge } from '../components/ui/status-badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'
import { TooltipHint } from '../components/ui/tooltip-hint'
import { rateLabel } from '../features/containers/network-rate'
import { useListMetrics } from '../features/containers/use-list-metrics'
import type { ContainerRuntime, ContainerSummary } from '../features/containers/types'
import { api } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { nodePath } from '../lib/nodes'
import { useDateTime } from '../lib/time-zone'
import { confirmDialog, promptDialog } from '../stores/dialog'
import { useUIStore } from '../stores/ui'
import { EmptyState, ResourceFrame } from './images'

const memory = (bytes: number) => bytes >= 1024 ** 3 ? `${(bytes / 1024 ** 3).toFixed(2)} GB` : `${(bytes / 1024 ** 2).toFixed(0)} MB`
const uptime = (seconds: number) => {
  if (!Number.isFinite(seconds) || seconds < 0) return '—'
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor(seconds % 86400 / 3600)
  const minutes = Math.floor(seconds % 3600 / 60)
  const remaining = Math.floor(seconds % 60)
  if (days > 0) return `Up ${days}d ${hours}h`
  if (hours > 0) return `Up ${hours}h ${minutes}m`
  if (minutes > 0) return `Up ${minutes}m ${remaining}s`
  return 'Up <1m'
}
const creationTime = (value: string, formatter: Intl.DateTimeFormat) => {
  const date = new Date(value)
  if (!Number.isFinite(date.getTime())) return '—'
  const parts = Object.fromEntries(formatter.formatToParts(date).map(({ type, value }) => [type, value]))
  return `${parts.month}-${parts.day} ${parts.hour}:${parts.minute}`
}
const stateLabel = (state: string, zh: boolean) => zh ? ({ running: '运行中', paused: '已暂停', restarting: '重启中', exited: '已停止', dead: '异常', created: '已创建' }[state] ?? state) : state
const healthLabel = (health: string | null | undefined, zh: boolean) => ({ healthy: zh ? '健康' : 'Healthy', unhealthy: zh ? '不健康' : 'Unhealthy', starting: zh ? '检查启动中' : 'Starting', syncing: zh ? '同步中' : 'Syncing', none: zh ? '未配置' : 'No check' }[health ?? ''] ?? (zh ? '未知' : 'Unknown'))

export function ContainersPage() {
  const navigate = useNavigate()
  const nodeID = useUIStore((state) => state.currentNodeID)
  const view = useContainerViewStore(state => state.views[nodeID]) ?? defaultContainerView
  const updateView = useContainerViewStore(state => state.update)
  const filter = view.search
  const [expanded, setExpanded] = useState('')
  const [overlay, setOverlay] = useState('')
  const [interacting, setInteracting] = useState(false)
  const small = useSmallScreen()
  const update = (patch: Partial<ContainerView>) => { updateView(nodeID, patch); setSelected(new Set()); setExpanded(''); setOverlay(''); order.current = [] }
  const setFilter = (search: string) => update({ search })
  const order = useRef<string[]>([])
  const reconciledAt = useRef(0)
  const [clock, setClock] = useState(Date.now)
  useEffect(() => { const timer = setInterval(() => setClock(Date.now()), 1000); return () => clearInterval(timer) }, [])
  const [selected, setSelected] = useState<Set<string>>(() => new Set())
  const [operationError, setOperationError] = useState('')
  const { t, language } = useI18n()
  const { formatDateTime, timeZone } = useDateTime()
  const createdFormatter = useMemo(() => new Intl.DateTimeFormat('en-GB', { timeZone, month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }), [timeZone])
  const zh = language === 'zh-CN'
  const queryClient = useQueryClient()
  const query = useQuery({ queryKey: ['containers', nodeID], queryFn: ({ signal }) => api<ContainerSummary[]>(nodePath(nodeID, '/containers'), { signal }), refetchInterval: 10_000 })
  const runtime = useQuery({ queryKey: ['container-runtime', nodeID], queryFn: ({ signal }) => api<ContainerRuntime[]>(nodePath(nodeID, '/containers/runtime'), { signal }), refetchInterval: 10_000, retry: false })
  const metrics = useListMetrics(nodeID)
  const invalidateContainers = () => Promise.all(['containers', 'container-metrics', 'container-runtime', 'container-diagnostics', 'container-overview', 'container'].map(key => queryClient.invalidateQueries({ queryKey: [key, nodeID] })))
  const action = useMutation({ mutationFn: ({ id, name }: { id: string; name: string }) => api(nodePath(nodeID, `/containers/${id}/${name}`), { method: 'POST' }), onMutate: () => setOperationError(''), onSuccess: invalidateContainers, onError: (error) => setOperationError(error.message) })
  useEffect(() => { setSelected(new Set()); setOperationError(''); setExpanded(''); setOverlay(''); order.current = [] }, [nodeID])
  const batch = useMutation({
    mutationFn: ({ ids, name }: { ids: string[]; name: string }) => api<{ results: { id: string; success: boolean; error?: string }[] }>(nodePath(nodeID, '/containers/batch'), { method: 'POST', body: JSON.stringify({ ids, action: name, remove_volumes: false, force: name === 'remove' }) }),
    onMutate: () => setOperationError(''),
    onSuccess: async (result) => {
      const failedRows = result.results.filter((item) => !item.success)
      setSelected(new Set(failedRows.map((item) => item.id)))
      if (failedRows.length) {
        const reason = failedRows.find((item) => item.error)?.error
        setOperationError(zh ? `${failedRows.length} 个容器操作失败${reason ? `：${reason}` : '。'}` : `${failedRows.length} container operations failed${reason ? `: ${reason}` : '.'}`)
      }
      await invalidateContainers()
    },
    onError: (error) => setOperationError(error.message),
  })
  const metricsById = new Map(metrics.samples?.map(row => [row.id, { ...row, available: row.available && !metrics.unavailable }]))
  const runtimeFresh = !runtime.isError && (!runtime.dataUpdatedAt || clock - runtime.dataUpdatedAt <= 30_000)
  const baseByID = new Map(query.data?.map(row => [row.id, row]))
  const snapshotsByID = new Map(runtimeFresh ? runtime.data?.map(row => [row.id, row]) : [])
  const runtimeById = new Map(runtimeFresh ? runtime.data?.filter(meta => !meta.state || baseByID.get(meta.id)?.state === meta.state).map(row => [row.id, row]) : [])
  const rowsSorted = selectContainers(query.data ?? [], view, runtimeById, metricsById, zh)
  const frozen = (interacting || selected.size > 0 || !!expanded || !!overlay) && ['cpu', 'memory', 'memory_ratio', 'receive', 'transmit', 'uptime'].includes(view.sort)
  if (!frozen || !order.current.length) order.current = rowsSorted.map(row => row.id)
  const ranks = new Map(order.current.map((id, i) => [id, i]))
  const rows = frozen ? [...rowsSorted].sort((a, b) => (ranks.get(a.id) ?? Infinity) - (ranks.get(b.id) ?? Infinity)) : rowsSorted
  const resetKey = `${nodeID}:${JSON.stringify(view)}`
  const pagination = useListPagination(rows, resetKey)
  const running = query.data?.filter(row => row.state === 'running').length ?? 0
  const paused = query.data?.filter(row => row.state === 'paused').length ?? 0
  const stopped = query.data?.filter(row => row.state === 'exited').length ?? 0
  const abnormal = query.data?.filter(row => isAbnormal(row, runtimeById.get(row.id))).length ?? 0
  const unknown = query.data?.filter(row => (!runtimeById.get(row.id) || runtimeById.get(row.id)?.health_status == null || runtimeById.get(row.id)?.restart_count == null) && !isAbnormal(row)).length ?? 0
  const projects = [...new Set(query.data?.map(row => row.labels['com.docker.compose.project']).filter(Boolean))].sort()
  useEffect(() => { const ids = new Set(query.data?.map(row => row.id)); setSelected(current => { const next = new Set([...current].filter(id => ids.has(id))); return next.size === current.size ? current : next }); if (expanded && !ids.has(expanded)) setExpanded('') }, [query.data, expanded])
  useEffect(() => { setExpanded(''); setOverlay('') }, [pagination.page, resetKey])
  useEffect(() => { const states = new Map(query.data?.map(row => [row.id, row.state])); if (runtimeFresh && reconciledAt.current !== runtime.dataUpdatedAt && runtime.data?.some(meta => meta.state && states.has(meta.id) && states.get(meta.id) !== meta.state)) { reconciledAt.current = runtime.dataUpdatedAt; void queryClient.invalidateQueries({ queryKey: ['containers', nodeID] }) } }, [runtime.data, runtime.dataUpdatedAt, runtimeFresh, nodeID, queryClient, query.data])
  const selectedRows = query.data?.filter((row) => selected.has(row.id)) ?? []
  const allSelected = pagination.items.length > 0 && pagination.items.every((row) => selected.has(row.id))
  const someSelected = !allSelected && pagination.items.some((row) => selected.has(row.id))

  const toggleAll = (checked: boolean | 'indeterminate') => setSelected((current) => { const next = new Set(current); for (const row of pagination.items) { if (checked === true) next.add(row.id); else next.delete(row.id) }; return next })
  const toggleOne = (id: string, checked: boolean) => setSelected((current) => { const next = new Set(current); if (checked) next.add(id); else next.delete(id); return next })

  const runBatch = async (name: string) => {
    if (!selectedRows.length) return
    if (name === 'remove') {
      const names = selectedRows.slice(0, 3).map((row) => row.name).join('、')
      if (!await confirmDialog({ title: zh ? `删除选中的 ${selectedRows.length} 个容器？` : `Remove ${selectedRows.length} selected containers?`, description: zh ? `${names}${selectedRows.length > 3 ? ' 等' : ''}。运行中的容器将被强制停止并删除；已挂载的存储卷会保留。` : `${names}${selectedRows.length > 3 ? ' and others' : ''}. Running containers will be forcibly stopped and removed; attached volumes are preserved.`, confirmLabel: zh ? '批量删除' : 'Remove selected', danger: true })) return
    }
    batch.mutate({ ids: selectedRows.map((row) => row.id), name })
  }
  const renameContainer = async (row: ContainerSummary) => {
    const name = await promptDialog({ title: zh ? `重命名 ${row.name}` : `Rename ${row.name}`, description: zh ? '仅修改容器名称，不会重建容器或改变其配置。' : 'Only the container name changes; the container is not recreated.', confirmLabel: zh ? '保存名称' : 'Save name', input: { label: zh ? '新容器名称' : 'New container name', initialValue: row.name } })
    if (!name?.trim() || name.trim() === row.name) return
    setOperationError('')
    try { await api(nodePath(nodeID, `/containers/${row.id}`), { method: 'PATCH', body: JSON.stringify({ name: name.trim() }) }); await queryClient.invalidateQueries({ queryKey: ['containers', nodeID] }) } catch (error) { setOperationError(error instanceof Error ? error.message : String(error)) }
  }
  const removeContainer = async (row: ContainerSummary) => {
    const value = await promptDialog({ title: zh ? `永久删除 ${row.name}` : `Permanently remove ${row.name}`, description: zh ? '容器必须先停止。删除后无法恢复，已挂载的存储卷将保留。' : 'The container must be stopped first. This cannot be undone; attached volumes are preserved.', confirmLabel: zh ? '删除容器' : 'Remove container', danger: true, input: { label: zh ? `输入 ${row.name} 以确认` : `Type ${row.name} to confirm`, requiredValue: row.name } })
    if (value !== row.name) return
    setOperationError('')
    try { await api(nodePath(nodeID, `/containers/${row.id}?remove_volumes=false`), { method: 'DELETE' }); await invalidateContainers() } catch (error) { setOperationError(error instanceof Error ? error.message : String(error)) }
  }
  const killContainer = async (row: ContainerSummary) => { if (await confirmDialog({ title: zh ? `强制终止 ${row.name}？` : `Force kill ${row.name}?`, description: zh ? '容器主进程会被立即终止，不会等待正常退出。' : 'The main process will be killed immediately without a graceful shutdown.', confirmLabel: zh ? '强制终止' : 'Force kill', danger: true })) action.mutate({ id: row.id, name: 'kill' }) }

  const selectionBar = selected.size > 0 && <div className="flex flex-wrap items-center gap-2">
    <Badge variant="outline">{selected.size} {zh ? '已选' : 'selected'}{selectedRows.filter(row => !pagination.items.some(item => item.id === row.id)).length > 0 && ` (${zh ? '含其他页' : 'other pages'} ${selectedRows.filter(row => !pagination.items.some(item => item.id === row.id)).length})`}</Badge>
    <Button size="sm" variant="outline" disabled={batch.isPending} onClick={() => void runBatch('start')}>{batch.isPending ? <Spinner /> : <Play />}{zh ? '启动' : 'Start'}</Button>
    <Button size="sm" variant="outline" disabled={batch.isPending} onClick={() => void runBatch('stop')}>{batch.isPending ? <Spinner /> : <Square />}{zh ? '停止' : 'Stop'}</Button>
    <Button size="sm" variant="outline" disabled={batch.isPending} onClick={() => void runBatch('restart')}>{batch.isPending ? <Spinner /> : <RefreshCw />}{zh ? '重启' : 'Restart'}</Button>
    <Button size="sm" variant="destructive" disabled={batch.isPending} onClick={() => void runBatch('remove')}>{batch.isPending ? <Spinner /> : <Trash2 />}{zh ? '删除' : 'Remove'}</Button>
  </div>

  const toolbar = <div className="flex flex-wrap items-center gap-2">
    {selectionBar}
    <ContainerFilters view={view} update={update} projects={projects} zh={zh} />
    {(view.search || view.states.length > 0 || view.health.length > 0 || view.source || view.project || view.diagnostic) && <Button size="sm" variant="ghost" className="text-destructive hover:text-destructive" onClick={() => update({ ...defaultContainerView, sort: view.sort, descending: view.descending })}>{zh ? '清除筛选' : 'Clear filters'}<X data-icon="inline-end" aria-hidden="true" /></Button>}
    <InputGroup className="w-[260px]">
      <InputGroupAddon align="inline-start"><Search /></InputGroupAddon>
      <InputGroupInput value={filter} onChange={(event) => setFilter(event.target.value)} placeholder={zh ? '名称、镜像、状态、端口…' : 'Name, image, status, port…'} aria-label={zh ? '筛选容器' : 'Filter containers'} />
      {!!filter && <InputGroupAddon align="inline-end"><InputGroupButton size="icon-xs" aria-label={zh ? '清空筛选' : 'Clear filter'} onClick={() => setFilter('')}><X /></InputGroupButton></InputGroupAddon>}
    </InputGroup>
    {frozen && <span className="text-xs text-muted-foreground">{zh ? '排序待更新' : 'Sort pending'}</span>}
  </div>

  const statusStrip = <>
    {([['running', running, zh ? '运行中' : 'running'], ['paused', paused, zh ? '已暂停' : 'paused'], ['exited', stopped, zh ? '已停止' : 'stopped']] as const).map(([state, count, label]) => <button key={state} type="button" aria-label={`${label} ${count}`} aria-pressed={view.states.includes(state)} onClick={() => update({ states: view.states.length === 1 && view.states[0] === state ? [] : [state] })}><StatusBadge tone={state === 'running' ? 'success' : state === 'paused' ? 'warning' : 'neutral'}>{count} {label}</StatusBadge></button>)}
    <button type="button" aria-pressed={view.diagnostic === 'abnormal'} onClick={() => update({ diagnostic: view.diagnostic === 'abnormal' ? '' : 'abnormal' })}><StatusBadge tone={abnormal ? 'critical' : 'neutral'}>{abnormal} {zh ? '异常' : 'abnormal'}{unknown > 0 && ` · ${unknown} ${zh ? '待确认' : 'unknown'}`}</StatusBadge></button>
  </>

  return <ResourceFrame compact title={t('containers')} detail={zh ? `${rows.length === query.data?.length ? '' : `${rows.length} / `}${query.data?.length ?? 0} 个容器` : `${rows.length === query.data?.length ? '' : `${rows.length} / `}${query.data?.length ?? 0} containers`} lead={statusStrip} action={toolbar}>
      {query.isPending && <LoadingState compact rows={7} label={zh ? '正在加载容器' : 'Loading containers'} />}
      {query.isError && <ErrorState description={zh ? '无法连接 Docker Engine。' : 'Unable to reach the Docker Engine.'} />}
      {!!operationError && (
        <Alert variant="destructive" className="w-full">
          <CircleAlert />
          <AlertDescription>{operationError}</AlertDescription>
          <AlertAction>
            <Button variant="ghost" size="icon-xs" aria-label={zh ? '关闭' : 'Dismiss'} onClick={() => setOperationError('')}><X /></Button>
          </AlertAction>
        </Alert>
      )}
      {!query.isPending && !query.isError && (rows.length === 0 ? <EmptyState icon={<Search className="size-5" />} title={zh ? '没有匹配的容器' : 'No matching containers'} detail={zh ? '调整筛选条件或在本节点创建新的容器后再试。' : 'Adjust the filter or create a container on this node.'} /> :
        <><ListShell className="@container">
          <Table onMouseEnter={() => setInteracting(true)} onMouseLeave={() => setInteracting(false)} onFocus={() => setInteracting(true)} onBlur={event => { if (!event.currentTarget.contains(event.relatedTarget)) setInteracting(false) }} className="min-w-[1500px] table-fixed [&_td]:py-[7px] [&_td]:text-center [&_th]:h-8 [&_th]:text-center">
            <TableHeader>
              <TableRow>
                <TableHead className="w-16 px-0">
                  <div className="flex items-center justify-center gap-3">
                    <Checkbox checked={allSelected} indeterminate={someSelected} onCheckedChange={(checked) => toggleAll(checked)} aria-label={allSelected ? (zh ? '取消选择本页' : 'Deselect this page') : (zh ? '选择本页' : 'Select this page')} />
                    <span aria-hidden="true" className="size-6 shrink-0" />
                  </div>
                </TableHead>
                <TableHead className="w-[176px]"><SortMenu view={view} update={update} zh={zh} label={zh ? '容器' : 'Container'} fields={['name']} /></TableHead>
                <TableHead className="w-[160px] text-center">{zh ? '状态' : 'Status'}</TableHead>
                <TableHead className="w-[160px]"><SortMenu view={view} update={update} zh={zh} label={zh ? '资源' : 'Resources'} fields={['cpu', 'memory', 'memory_ratio']} /></TableHead>
                <TableHead className="w-[128px]"><SortMenu view={view} update={update} zh={zh} label={zh ? '网络' : 'Network'} fields={['receive', 'transmit']} /></TableHead>
                <TableHead className="w-[184px]">{zh ? '端口' : 'Ports'}</TableHead>
                <TableHead className="w-[300px]"><SortMenu view={view} update={update} zh={zh} label={zh ? '镜像' : 'Image'} fields={['image']} /></TableHead>
                <TableHead className="w-[136px]"><SortMenu view={view} update={update} zh={zh} label={zh ? '时间' : 'Time'} fields={['created', 'uptime']} /></TableHead>
                <TableHead>{zh ? '操作' : 'Actions'}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {pagination.items.map((row) => {
                const sample = metricsById.get(row.id)
                const meta = runtimeById.get(row.id)
                const snapshot = snapshotsByID.get(row.id)
                const syncing = !!snapshot?.state && snapshot.state !== row.state
                const available = !syncing && row.state === 'running' && sample?.available && !metrics.unavailable
                const cpuAvailable = available && sample.cpu_available !== false
                const memoryAvailable = available && sample.memory_available !== false
                const uptimeAvailable = available && !!sample.started_at && Number.isFinite(Date.parse(sample.started_at))
                const startedAt = uptimeAvailable && sample.started_at ? formatDateTime(sample.started_at) : '—'
                const createdAt = creationTime(row.created, createdFormatter)
                const health = syncing ? 'syncing' : row.state === 'running' ? meta?.health_status : meta?.health_status === 'none' ? 'none' : undefined
                const project = row.labels['com.docker.compose.project']
                const service = row.labels['com.docker.compose.service']
                const limit = meta?.memory_limit_bytes
                const rate = metrics.unavailable ? { status: 'unavailable' as const } : sample?.rate
                const networkLabel = rate?.status === 'ready' ? undefined : rate?.status === 'unavailable' ? (zh ? '不可用' : 'Unavailable') : (zh ? '采样中' : 'Sampling')
                return (
                <Fragment key={row.id}><TableRow data-state={selected.has(row.id) ? 'selected' : undefined}>
                  <TableCell className="px-0">
                    <div className="flex items-center justify-center gap-3">
                      <Checkbox checked={selected.has(row.id)} onCheckedChange={(checked) => toggleOne(row.id, Boolean(checked))} aria-label={`${zh ? '选择' : 'Select'} ${row.name}`} />
                      <button type="button" className="inline-flex size-6 shrink-0 items-center justify-center rounded-sm text-muted-foreground hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" aria-label={`${zh ? '容器概览' : 'Container overview'} ${row.name}`} aria-expanded={expanded === row.id} aria-controls={`container-expansion-${row.id}`} onClick={() => { setExpanded(expanded === row.id ? '' : row.id); setOverlay('') }}>{expanded === row.id ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}</button>
                    </div>
                  </TableCell>
                  <TableCell>
                    <div className="grid h-10 min-w-0 grid-rows-2 justify-items-center leading-5">
                      <TooltipHint content={row.name}><Link to="/containers/$containerId" params={{ containerId: row.id }} className="truncate text-left font-medium hover:underline">{row.name}</Link></TooltipHint>
                      <TooltipHint content={row.id}><span className="font-mono text-xs leading-5 text-muted-foreground">{row.id.slice(0, 12)}</span></TooltipHint>
                    </div>
                  </TableCell>
                  <TableCell>
                    <div className="grid h-10 min-w-0 grid-rows-2 justify-items-center leading-5">
                      <div className="flex items-center justify-center gap-1"><StatusBadge tone="neutral" className="h-[18px] self-center justify-start rounded-sm border-border/60 bg-muted/40 px-1.5 py-0 text-sm leading-4 text-foreground">
                        <span aria-hidden="true" className="inline-flex size-3 shrink-0 items-center justify-center"><span className={`size-1.5 rounded-full ${row.state === 'running' ? 'bg-emerald-500' : row.state === 'paused' || row.state === 'restarting' ? 'bg-amber-500' : row.state === 'dead' ? 'bg-red-500' : 'bg-muted-foreground'}`} /></span>
                        <span className={row.state === 'dead' ? 'text-red-600 dark:text-red-400' : undefined}>{stateLabel(row.state, zh)}</span>
                      </StatusBadge>
                      {((meta?.restart_count ?? 0) > 0 || isAbnormal(row, meta)) && <ObservationOverlay open={overlay === `${row.id}:diagnostics`} onOpenChange={open => setOverlay(open ? `${row.id}:diagnostics` : '')} title={zh ? '运行诊断' : 'Runtime diagnostics'} trigger={<button type="button" aria-label={`${zh ? '运行诊断' : 'Runtime diagnostics'} ${row.name}`} className={`inline-flex items-center gap-0.5 text-[10px] ${isAbnormal(row, meta) ? 'text-destructive' : 'text-muted-foreground'}`}>{isAbnormal(row, meta) ? <CircleAlert className="size-3" /> : <RefreshCw className="size-3" />}{(meta?.restart_count ?? 0) > 0 && meta?.restart_count}</button>}><DiagnosticsContent nodeID={nodeID} containerID={row.id} zh={zh} /></ObservationOverlay>}</div>
                      <ObservationOverlay open={overlay === `${row.id}:health`} onOpenChange={open => setOverlay(open ? `${row.id}:health` : '')} title={zh ? '健康检查' : 'Health check'} trigger={<button type="button" aria-label={`${zh ? '健康检查' : 'Health check'} ${row.name}`}><StatusBadge tone={health === 'healthy' ? 'success' : health === 'unhealthy' ? 'critical' : health === 'starting' ? 'warning' : 'neutral'} className="h-4 self-center justify-start rounded-sm border-border/60 bg-transparent px-1.5 py-0 text-xs font-normal leading-[14px]">
                        <HeartPulse aria-hidden="true" />
                        <span className={health === 'unhealthy' ? undefined : 'text-muted-foreground'}>{row.state === 'running' || health === 'none' ? healthLabel(health, zh) : '—'}</span>
                      </StatusBadge></button>}><DiagnosticsContent nodeID={nodeID} containerID={row.id} zh={zh} healthOnly /></ObservationOverlay>
                    </div>
                  </TableCell>
                  <TableCell>
                    <ObservationOverlay open={overlay === `${row.id}:resources`} onOpenChange={open => setOverlay(open ? `${row.id}:resources` : '')} title={zh ? '资源趋势' : 'Resource trends'} trigger={<button type="button" className="w-full" aria-label={`${zh ? '资源趋势' : 'Resource trends'} ${row.name}`}><div className="grid h-10 min-w-0 grid-rows-2 justify-items-center leading-5 tabular-nums"><span className="text-sm leading-5">CPU {cpuAvailable ? `${sample.cpu_percent.toFixed(1)}%` : '—'}</span><span className="relative text-xs leading-5 text-muted-foreground">{memoryAvailable ? memory(sample.memory_bytes) : '—'} / {limit == null ? '—' : limit === 0 ? '∞' : memory(limit)}<MemoryBar used={memoryAvailable ? sample.memory_bytes : undefined} limit={limit} zh={zh} /></span></div></button>}><ResourceTrend points={metrics.history[row.id] ?? []} limit={limit} unavailable={!available} nodeID={nodeID} containerID={row.id} zh={zh} /></ObservationOverlay>
                  </TableCell>
                  <TableCell className="tabular-nums"><div className="grid h-10 min-w-0 grid-rows-2 justify-items-center leading-5"><span className="text-sm leading-5">↓ {row.state !== 'running' || syncing ? '—' : rate?.status === 'ready' ? rateLabel(rate.receive) : networkLabel}</span><span className="text-xs leading-5 text-muted-foreground">↑ {row.state !== 'running' || syncing ? '—' : rate?.status === 'ready' ? rateLabel(rate.transmit) : networkLabel}</span></div></TableCell>
                  <TableCell><ContainerPorts ports={row.ports} zh={zh} /></TableCell>
                  <TableCell>
                    <div className="grid h-10 min-w-0 grid-rows-2 justify-items-center leading-5">
                      <TooltipHint content={row.image}><span className="truncate text-left">{row.image}</span></TooltipHint>
                      {project ? <TooltipHint content={`Compose · ${project}${service ? ` / ${service}` : ''}`}><span className="truncate text-left text-xs leading-5 text-muted-foreground"><Link to="/projects/$backend/$projectName" params={{ backend: 'compose', projectName: project }} className="hover:underline">{project}</Link>{service && <> / {service}</>}</span></TooltipHint> : <span className="text-xs leading-5 text-muted-foreground">{zh ? '独立容器' : 'Standalone'}</span>}
                    </div>
                  </TableCell>
                  <TableCell>
                    <div className="grid h-10 min-w-0 grid-rows-2 justify-items-center leading-5 tabular-nums">
                      <TooltipHint content={createdAt !== '—' ? `${zh ? '创建时间' : 'Created at'}: ${formatDateTime(row.created)}` : undefined}><span className="truncate text-sm leading-5">{createdAt}</span></TooltipHint>
                      <TooltipHint content={startedAt !== '—' ? `${zh ? '启动时间' : 'Started at'}: ${startedAt}` : undefined} className="h-5"><span className="block h-5 min-w-0 truncate text-xs leading-5 text-muted-foreground">{uptimeAvailable ? uptime(sample.uptime_seconds) : '—'}</span></TooltipHint>
                    </div>
                  </TableCell>
                  <TableCell>
                    <ContainerActions row={row} zh={zh} pending={action.isPending && action.variables?.id === row.id} open={(tab) => void navigate({ to: '/containers/$containerId', params: { containerId: row.id }, hash: tab })} run={(name) => action.mutate({ id: row.id, name })} rename={() => void renameContainer(row)} kill={() => void killContainer(row)} remove={() => void removeContainer(row)} />
                  </TableCell>
                </TableRow>
                {expanded === row.id && !small && <TableRow><TableCell colSpan={9} id={`container-expansion-${row.id}`} className="bg-muted/20 p-5"><div className="sticky left-5 w-[calc(100cqw-2.5rem)]"><ContainerOverviewContent nodeID={nodeID} containerID={row.id} zh={zh} pending={metrics.isPending} points={metrics.history[row.id] ?? []} available={!!available} state={syncing ? 'syncing' : row.state} runtime={meta} /></div></TableCell></TableRow>}
                </Fragment>
              )})}
            </TableBody>
          </Table>
        </ListShell><ListPagination {...pagination} zh={zh} /></>)}
    {small && <Sheet open={!!expanded} onOpenChange={open => { if (!open) setExpanded('') }}><SheetContent side="bottom" className="max-h-[85dvh] overflow-y-auto"><SheetHeader><SheetTitle>{zh ? '快速查看' : 'Quick view'}</SheetTitle></SheetHeader><div className="px-4 pb-5">{expanded && <ContainerOverviewContent nodeID={nodeID} containerID={expanded} zh={zh} pending={metrics.isPending} points={metrics.history[expanded] ?? []} available={!!metricsById.get(expanded)?.available && !metrics.unavailable && baseByID.get(expanded)?.state === 'running' && (!snapshotsByID.get(expanded)?.state || snapshotsByID.get(expanded)?.state === baseByID.get(expanded)?.state)} state={snapshotsByID.get(expanded)?.state && snapshotsByID.get(expanded)?.state !== baseByID.get(expanded)?.state ? 'syncing' : baseByID.get(expanded)?.state ?? 'unknown'} runtime={runtimeById.get(expanded)} />}</div></SheetContent></Sheet>}
  </ResourceFrame>
}

function ContainerActions({ row, zh, pending, open, run, rename, kill, remove }: { row: ContainerSummary; zh: boolean; pending: boolean; open: (tab: 'logs' | 'files' | 'terminal' | 'events') => void; run: (name: string) => void; rename: () => void; kill: () => void; remove: () => void }) {
  const primary = row.state === 'running' ? 'stop' : row.state === 'paused' ? 'unpause' : 'start'
  const primaryLabel = row.state === 'running' ? (zh ? '停止' : 'Stop') : row.state === 'paused' ? (zh ? '恢复' : 'Unpause') : (zh ? '启动' : 'Start')
  return <div className="flex items-center justify-center gap-0.5">
    <TooltipHint content={zh ? '日志' : 'Logs'}><Button variant="ghost" size="icon-sm" aria-label={zh ? '日志' : 'Logs'} onClick={() => open('logs')}><FileText /></Button></TooltipHint>
    <TooltipHint content={zh ? '文件' : 'Files'}><Button variant="ghost" size="icon-sm" disabled={row.state !== 'running'} aria-label={zh ? '文件' : 'Files'} onClick={() => open('files')}><FolderOpen /></Button></TooltipHint>
    <TooltipHint content={zh ? '终端' : 'Terminal'}><Button variant="ghost" size="icon-sm" disabled={row.state !== 'running'} aria-label={zh ? '终端' : 'Terminal'} onClick={() => open('terminal')}><SquareTerminal /></Button></TooltipHint>
    <TooltipHint content={primaryLabel}><Button variant={primary === 'stop' ? 'outline' : 'secondary'} size="icon-sm" disabled={pending} aria-label={primaryLabel} onClick={() => run(primary)}>{pending ? <Spinner /> : primary === 'stop' ? <Square /> : <Play />}</Button></TooltipHint>
    <TooltipHint content={zh ? '重启' : 'Restart'}><Button variant="ghost" size="icon-sm" disabled={pending} aria-label={zh ? '重启' : 'Restart'} onClick={() => run('restart')}><RefreshCw /></Button></TooltipHint>
    <DropdownMenu>
      <DropdownMenuTrigger render={<Button variant="ghost" size="icon-sm" aria-label={zh ? '更多操作' : 'More actions'}><MoreHorizontal /></Button>} />
      <DropdownMenuContent align="end" className="w-44">
        <DropdownMenuItem onClick={() => open('events')}><FileText />{zh ? '查看事件' : 'View events'}</DropdownMenuItem>
        <DropdownMenuItem onClick={rename}><Pencil />{zh ? '重命名' : 'Rename'}</DropdownMenuItem>
        {row.state === 'running' && <DropdownMenuItem onClick={() => run('pause')}><Pause />{zh ? '暂停' : 'Pause'}</DropdownMenuItem>}
        {row.state === 'paused' && <DropdownMenuItem onClick={() => run('unpause')}><Play />{zh ? '恢复' : 'Unpause'}</DropdownMenuItem>}
        {(row.state === 'running' || row.state === 'paused') && <DropdownMenuItem variant="destructive" onClick={kill}><OctagonX />{zh ? '强制终止' : 'Force kill'}</DropdownMenuItem>}
        <DropdownMenuSeparator />
        <DropdownMenuItem variant="destructive" onClick={remove}><Trash2 />{zh ? '删除容器' : 'Remove container'}</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  </div>
}
