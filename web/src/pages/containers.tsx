import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CircleAlert, FileText, MoreHorizontal, OctagonX, Pause, Pencil, Play, RefreshCw, Search, Square, SquareTerminal, Trash2, X } from 'lucide-react'
import { useState } from 'react'
import { Alert, AlertAction, AlertDescription } from '../components/ui/alert'
import { Badge } from '../components/ui/badge'
import { Button } from '../components/ui/button'
import { Checkbox } from '../components/ui/checkbox'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from '../components/ui/dropdown-menu'
import { ErrorState } from '../components/ui/error-state'
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupInput } from '../components/ui/input-group'
import { ListShell } from '../components/ui/list-shell'
import { LoadingState } from '../components/ui/loading-state'
import { Spinner } from '../components/ui/spinner'
import { StatusBadge } from '../components/ui/status-badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'
import type { ContainerMetrics, ContainerSummary } from '../features/containers/types'
import { api } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { nodePath } from '../lib/nodes'
import { confirmDialog, promptDialog } from '../stores/dialog'
import { useUIStore } from '../stores/ui'
import { EmptyState, ResourceFrame } from './images'

const ports = (value: ContainerSummary) => value.ports.slice(0, 2).map((port) => port.public_port ? `${port.public_port}→${port.private_port}/${port.type}` : `${port.private_port}/${port.type}`).join(', ') || '—'
const memory = (bytes: number) => bytes >= 1024 ** 3 ? `${(bytes / 1024 ** 3).toFixed(2)} GB` : `${(bytes / 1024 ** 2).toFixed(0)} MB`
const uptime = (seconds: number) => !seconds ? '—' : seconds >= 86400 ? `${Math.floor(seconds / 86400)}d` : seconds >= 3600 ? `${Math.floor(seconds / 3600)}h` : `${Math.floor(seconds / 60)}m`
const stateLabel = (state: string, zh: boolean) => zh ? ({ running: '运行中', paused: '已暂停', restarting: '重启中', exited: '已停止', dead: '异常', created: '已创建' }[state] ?? state) : state

export function ContainersPage() {
  const nodeID = useUIStore((state) => state.currentNodeID)
  const [filter, setFilter] = useState('')
  const [selected, setSelected] = useState<Set<string>>(() => new Set())
  const [operationError, setOperationError] = useState('')
  const { t, language } = useI18n()
  const zh = language === 'zh-CN'
  const queryClient = useQueryClient()
  const query = useQuery({ queryKey: ['containers', nodeID], queryFn: () => api<ContainerSummary[]>(nodePath(nodeID, '/containers')), refetchInterval: 10_000 })
  const metrics = useQuery({ queryKey: ['container-metrics', nodeID], queryFn: () => api<ContainerMetrics[]>(nodePath(nodeID, '/containers/metrics')), refetchInterval: 5_000 })
  const action = useMutation({ mutationFn: ({ id, name }: { id: string; name: string }) => api(nodePath(nodeID, `/containers/${id}/${name}`), { method: 'POST' }), onMutate: () => setOperationError(''), onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['containers', nodeID] }); queryClient.invalidateQueries({ queryKey: ['container-metrics', nodeID] }) }, onError: (error) => setOperationError(error.message) })
  const batch = useMutation({ mutationFn: ({ ids, name }: { ids: string[]; name: string }) => api<{ results: { id: string; success: boolean }[] }>(nodePath(nodeID, '/containers/batch'), { method: 'POST', body: JSON.stringify({ ids, action: name, remove_volumes: false }) }), onMutate: () => setOperationError(''), onSuccess: async (result) => { const failed = result.results.filter((item) => !item.success).length; setSelected(new Set()); if (failed) setOperationError(zh ? `${failed} 个容器操作失败，请检查容器当前状态。` : `${failed} container operations failed. Check their current state.`); await Promise.all([queryClient.invalidateQueries({ queryKey: ['containers', nodeID] }), queryClient.invalidateQueries({ queryKey: ['container-metrics', nodeID] })]) }, onError: (error) => setOperationError(error.message) })
  const metricsById = new Map(metrics.data?.map((row) => [row.id, row]))
  const rows = query.data?.map((row) => ({ ...row, ...metricsById.get(row.id) })).filter((row) => `${row.id} ${row.name} ${row.image} ${row.state} ${row.status} ${ports(row)}`.toLowerCase().includes(filter.toLowerCase())) ?? []
  const running = query.data?.filter((row) => row.state === 'running').length ?? 0
  const paused = query.data?.filter((row) => row.state === 'paused').length ?? 0
  const stopped = (query.data?.length ?? 0) - running - paused
  const selectedRows = query.data?.filter((row) => selected.has(row.id)) ?? []
  const allSelected = rows.length > 0 && rows.every((row) => selected.has(row.id))
  const someSelected = !allSelected && rows.some((row) => selected.has(row.id))

  const toggleAll = (checked: boolean | 'indeterminate') => setSelected(checked === true ? new Set(rows.map((row) => row.id)) : new Set())
  const toggleOne = (id: string, checked: boolean) => setSelected((current) => { const next = new Set(current); if (checked) next.add(id); else next.delete(id); return next })

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
    try { await api(nodePath(nodeID, `/containers/${row.id}`), { method: 'PATCH', body: JSON.stringify({ name: name.trim() }) }); await queryClient.invalidateQueries({ queryKey: ['containers', nodeID] }) } catch (error) { setOperationError(error instanceof Error ? error.message : String(error)) }
  }
  const removeContainer = async (row: ContainerSummary) => {
    const value = await promptDialog({ title: zh ? `永久删除 ${row.name}` : `Permanently remove ${row.name}`, description: zh ? '容器必须先停止。删除后无法恢复，已挂载的存储卷将保留。' : 'The container must be stopped first. This cannot be undone; attached volumes are preserved.', confirmLabel: zh ? '删除容器' : 'Remove container', danger: true, input: { label: zh ? `输入 ${row.name} 以确认` : `Type ${row.name} to confirm`, requiredValue: row.name } })
    if (value !== row.name) return
    setOperationError('')
    try { await api(nodePath(nodeID, `/containers/${row.id}?remove_volumes=false`), { method: 'DELETE' }); await Promise.all([queryClient.invalidateQueries({ queryKey: ['containers', nodeID] }), queryClient.invalidateQueries({ queryKey: ['container-metrics', nodeID] })]) } catch (error) { setOperationError(error instanceof Error ? error.message : String(error)) }
  }
  const killContainer = async (row: ContainerSummary) => { if (await confirmDialog({ title: zh ? `强制终止 ${row.name}？` : `Force kill ${row.name}?`, description: zh ? '容器主进程会被立即终止，不会等待正常退出。' : 'The main process will be killed immediately without a graceful shutdown.', confirmLabel: zh ? '强制终止' : 'Force kill', danger: true })) action.mutate({ id: row.id, name: 'kill' }) }

  const selectionBar = selected.size > 0 && <div className="flex flex-wrap items-center gap-2">
    <Badge variant="outline">{selected.size} {zh ? '已选' : 'selected'}</Badge>
    <Button size="sm" variant="outline" disabled={batch.isPending} onClick={() => void runBatch('start')}>{batch.isPending ? <Spinner /> : <Play />}{zh ? '启动' : 'Start'}</Button>
    <Button size="sm" variant="outline" disabled={batch.isPending} onClick={() => void runBatch('stop')}>{batch.isPending ? <Spinner /> : <Square />}{zh ? '停止' : 'Stop'}</Button>
    <Button size="sm" variant="outline" disabled={batch.isPending} onClick={() => void runBatch('restart')}>{batch.isPending ? <Spinner /> : <RefreshCw />}{zh ? '重启' : 'Restart'}</Button>
    <Button size="sm" variant="destructive" disabled={batch.isPending} onClick={() => void runBatch('remove')}>{batch.isPending ? <Spinner /> : <Trash2 />}{zh ? '删除' : 'Remove'}</Button>
  </div>

  const toolbar = <div className="flex flex-wrap items-center gap-2">
    {selectionBar}
    <InputGroup className="w-[260px]">
      <InputGroupAddon align="inline-start"><Search /></InputGroupAddon>
      <InputGroupInput value={filter} onChange={(event) => setFilter(event.target.value)} placeholder={zh ? '名称、镜像、状态、端口…' : 'Name, image, status, port…'} aria-label={zh ? '筛选容器' : 'Filter containers'} />
      {!!filter && <InputGroupAddon align="inline-end"><InputGroupButton size="icon-xs" aria-label={zh ? '清空筛选' : 'Clear filter'} onClick={() => setFilter('')}><X /></InputGroupButton></InputGroupAddon>}
    </InputGroup>
  </div>

  const statusStrip = <>
    <StatusBadge tone="success">{running} {zh ? '运行中' : 'running'}</StatusBadge>
    <StatusBadge tone="warning">{paused} {zh ? '已暂停' : 'paused'}</StatusBadge>
    <Badge variant="outline" className="text-muted-foreground">{stopped} {zh ? '已停止' : 'stopped'}</Badge>
  </>

  return <ResourceFrame title={t('containers')} detail={zh ? `${query.data?.length ?? 0} 个容器` : `${query.data?.length ?? 0} containers`} lead={statusStrip} action={toolbar}>
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
        <ListShell>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-10 pl-3">
                  <Checkbox checked={allSelected} indeterminate={someSelected} onCheckedChange={(checked) => toggleAll(checked)} aria-label={allSelected ? (zh ? '取消全选' : 'Deselect all') : (zh ? '全选本页' : 'Select all')} />
                </TableHead>
                <TableHead className="min-w-[190px]">{zh ? '容器' : 'Container'}</TableHead>
                <TableHead>{zh ? '镜像 / 来源' : 'Image / source'}</TableHead>
                <TableHead className="min-w-[140px]">{zh ? '状态 / 运行时间' : 'State / uptime'}</TableHead>
                <TableHead className="min-w-[130px]">{zh ? '资源' : 'Resources'}</TableHead>
                <TableHead className="min-w-[160px]">{zh ? '端口' : 'Ports'}</TableHead>
                <TableHead className="min-w-[210px] text-right">{zh ? '操作' : 'Actions'}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((row) => (
                <TableRow key={row.id} data-state={selected.has(row.id) ? 'selected' : undefined}>
                  <TableCell className="pl-3">
                    <Checkbox checked={selected.has(row.id)} onCheckedChange={(checked) => toggleOne(row.id, Boolean(checked))} aria-label={`${zh ? '选择' : 'Select'} ${row.name}`} />
                  </TableCell>
                  <TableCell>
                    <div className="flex flex-col">
                      <a href={`/containers/${row.id}`} className="font-medium hover:underline">{row.name}</a>
                      <span className="font-mono text-xs text-muted-foreground">{row.id.slice(0, 12)}</span>
                    </div>
                  </TableCell>
                  <TableCell>
                    <div className="flex flex-col items-start gap-1">
                      <span className="max-w-[240px] truncate" title={row.image}>{row.image}</span>
                      {row.labels['com.docker.compose.project']
                        ? <Badge variant="outline">Compose · {row.labels['com.docker.compose.service'] || row.labels['com.docker.compose.project']}</Badge>
                        : <Badge variant="outline" className="text-muted-foreground">{zh ? '独立容器' : 'Standalone'}</Badge>}
                    </div>
                  </TableCell>
                  <TableCell>
                    <div className="flex flex-col items-start gap-1">
                      <StatusBadge tone={row.state === 'running' ? 'success' : row.state === 'paused' || row.state === 'restarting' ? 'warning' : row.state === 'dead' ? 'critical' : 'neutral'}>{stateLabel(row.state, zh)}</StatusBadge>
                      <span className="text-xs text-muted-foreground">{uptime(row.uptime_seconds)}</span>
                    </div>
                  </TableCell>
                  <TableCell>{row.state === 'running' ? (
                    <div className="flex flex-col">
                      <span>CPU {row.cpu_percent.toFixed(1)}%</span>
                      <span className="text-xs text-muted-foreground">{memory(row.memory_bytes)}</span>
                    </div>
                  ) : '—'}
                  </TableCell>
                  <TableCell className="font-mono text-xs">{ports(row)}</TableCell>
                  <TableCell>
                    <ContainerActions row={row} zh={zh} pending={action.isPending && action.variables?.id === row.id} run={(name) => action.mutate({ id: row.id, name })} rename={() => void renameContainer(row)} kill={() => void killContainer(row)} remove={() => void removeContainer(row)} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </ListShell>)}
  </ResourceFrame>
}

function ContainerActions({ row, zh, pending, run, rename, kill, remove }: { row: ContainerSummary; zh: boolean; pending: boolean; run: (name: string) => void; rename: () => void; kill: () => void; remove: () => void }) {
  const primary = row.state === 'running' ? 'stop' : row.state === 'paused' ? 'unpause' : 'start'
  const primaryLabel = row.state === 'running' ? (zh ? '停止' : 'Stop') : row.state === 'paused' ? (zh ? '恢复' : 'Unpause') : (zh ? '启动' : 'Start')
  return <div className="flex items-center justify-end gap-1">
    <Button variant="ghost" size="icon-sm" title={zh ? '日志' : 'Logs'} aria-label={zh ? '日志' : 'Logs'} onClick={() => location.assign(`/containers/${row.id}#logs`)}><FileText /></Button>
    <Button variant="ghost" size="icon-sm" disabled={row.state !== 'running'} title={zh ? '终端' : 'Terminal'} aria-label={zh ? '终端' : 'Terminal'} onClick={() => location.assign(`/containers/${row.id}#terminal`)}><SquareTerminal /></Button>
    <Button variant={primary === 'stop' ? 'outline' : 'secondary'} size="sm" disabled={pending} onClick={() => run(primary)}>
      {pending ? <Spinner /> : primary === 'stop' ? <Square /> : <Play />}
      {primaryLabel}
    </Button>
    <Button variant="ghost" size="icon-sm" disabled={pending} title={zh ? '重启' : 'Restart'} aria-label={zh ? '重启' : 'Restart'} onClick={() => run('restart')}><RefreshCw /></Button>
    <DropdownMenu>
      <DropdownMenuTrigger render={<Button variant="ghost" size="icon-sm" aria-label={zh ? '更多操作' : 'More actions'}><MoreHorizontal /></Button>} />
      <DropdownMenuContent align="end" className="w-44">
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
