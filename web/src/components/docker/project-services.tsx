import { Link } from '@tanstack/react-router'
import { Box, ChevronRight, FileText, FolderOpen, MoreHorizontal, SquareTerminal } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import type { ContainerSummary } from '../../features/containers/types'
import { fullPortLabel, portLabel } from '../../features/containers/ports'
import { aggregate, groupServices, isOneOff, serviceName, type Aggregate, type ProjectObservation } from '../../features/compose/observation'
import { effectiveHealth, isAbnormal, sizeLabel } from '../../features/containers/observation'
import { ImageUpdateCheck, ImageUpdateDetails, UpdateStatus } from '../../features/image-updates/image-updates'
import type { ImageUpdateResult } from '../../features/image-updates/types'
import { Button } from '../ui/button'
import { Card } from '../ui/card'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '../ui/collapsible'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '../ui/dialog'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '../ui/dropdown-menu'
import { ErrorState } from '../ui/error-state'
import { ListPagination } from '../ui/list-pagination'
import { LoadingState } from '../ui/loading-state'
import { StatusBadge } from '../ui/status-badge'
import { TooltipHint } from '../ui/tooltip-hint'
import { useListPagination } from '../ui/use-list-pagination'
import { DiagnosticsContent } from './container-observation'
import { ContainerPortTag } from './container-ports'

const emptyObservation: ProjectObservation = { runtime: new Map(), metrics: new Map() }
const stateLabel = (state: string, zh: boolean) => zh ? ({ running: '运行中', paused: '已暂停', restarting: '重启中', exited: '已停止', dead: '异常', created: '已创建' }[state] ?? state) : state
const healthLabel = (health: string, zh: boolean) => ({ healthy: zh ? '健康' : 'Healthy', unhealthy: zh ? '不健康' : 'Unhealthy', starting: zh ? '检查启动中' : 'Starting', none: zh ? '未配置检查' : 'No health check', unknown: zh ? '健康未知' : 'Health unknown', inactive: zh ? '未运行' : 'Not running' }[health] ?? health)
export function ProjectResourceSummary({ summary, zh, inline = false, aggregated = false }: { summary: Aggregate; zh: boolean; inline?: boolean; aggregated?: boolean }) {
  const coverage = (count: number) => summary.running > 0 && count < summary.running && <TooltipHint content={zh ? `${count}/${summary.running} 个运行容器有有效采样` : `${count}/${summary.running} running containers have valid samples`}><span className="ml-1 text-[11px] text-muted-foreground">· {count}/{summary.running}</span></TooltipHint>
  const cpuHint = aggregated ? zh ? '该服务下运行容器的有效 CPU 采样之和；100% 约等于占满一个 CPU 核心。' : 'Sum of valid CPU samples from running containers in this service; 100% is approximately one CPU core.' : undefined
  const memoryHint = aggregated ? zh ? '该服务下运行容器的有效内存采样之和。' : 'Sum of valid memory samples from running containers in this service.' : undefined
  if (inline) return <div className="grid min-w-0 grid-cols-2 gap-x-4 gap-y-2 text-xs tabular-nums"><p className="min-w-0"><span className="flex flex-wrap items-center text-muted-foreground"><TooltipHint content={cpuHint}><span>{aggregated ? zh ? 'CPU 合计' : 'Total CPU' : 'CPU'}</span></TooltipHint>{coverage(summary.cpuCoverage)}</span>{' '}<span className="mt-1 block break-all">{summary.cpu === null ? '—' : `${summary.cpu.toFixed(1)}%`}</span></p><p className="min-w-0"><span className="flex flex-wrap items-center text-muted-foreground"><TooltipHint content={memoryHint}><span>{aggregated ? zh ? '内存合计' : 'Total memory' : zh ? '内存' : 'Memory'}</span></TooltipHint>{coverage(summary.memoryCoverage)}</span>{' '}<span className="mt-1 block break-all">{summary.memory === null ? '—' : sizeLabel(summary.memory)}</span></p></div>
  return <div className="flex flex-col gap-1 text-xs tabular-nums"><span>CPU {summary.cpu === null ? '—' : `${summary.cpu.toFixed(1)}%`}{coverage(summary.cpuCoverage)}</span><span className="text-muted-foreground">{zh ? '内存' : 'Memory'} {summary.memory === null ? '—' : sizeLabel(summary.memory)}{coverage(summary.memoryCoverage)}</span></div>
}

function ContainerActions({ row, zh, onOpenContainer, onOpenChange }: {
  row: ContainerSummary; zh: boolean; onOpenContainer: (row: ContainerSummary, tab: 'logs' | 'files' | 'terminal', opener?: HTMLElement) => void; onOpenChange: (open: boolean) => void
}) {
  const trigger = useRef<HTMLButtonElement>(null)
  const openingWorkspace = useRef(false)
  return <DropdownMenu onOpenChange={open => { if (open) openingWorkspace.current = false; onOpenChange(open) }}>
    <DropdownMenuTrigger render={<Button ref={trigger} variant="ghost" size="icon-sm" aria-label={`${zh ? '容器操作' : 'Container actions'} · ${row.name}`}><MoreHorizontal /></Button>} />
    <DropdownMenuContent align="end" finalFocus={() => openingWorkspace.current ? false : trigger.current}>
      {([['logs', FileText, zh ? '日志' : 'Logs'], ['files', FolderOpen, zh ? '文件' : 'Files'], ['terminal', SquareTerminal, zh ? '终端' : 'Terminal']] as const).map(([tab, Icon, label]) => <DropdownMenuItem key={tab} disabled={tab !== 'logs' && row.state !== 'running'} aria-label={`${label} · ${row.name}`} onClick={() => { openingWorkspace.current = true; onOpenContainer(row, tab, trigger.current ?? undefined) }}><Icon />{label}</DropdownMenuItem>)}
    </DropdownMenuContent>
  </DropdownMenu>
}
export function ProjectServices({ nodeID, projectName, rows, updates = [], declared, loading, error, updateError, zh, observation = emptyObservation, showUpdates = true, declarationUnavailable = false, onOpenContainer, onOverlayChange }: {
  nodeID: string; projectName: string; rows?: ContainerSummary[]; updates?: ImageUpdateResult[]; declared: string[]; loading: boolean; error?: string; updateError?: string; zh: boolean;
  observation?: ProjectObservation; showUpdates?: boolean; declarationUnavailable?: boolean;
  onOpenContainer: (row: ContainerSummary, tab: 'logs' | 'files' | 'terminal', opener?: HTMLElement) => void;
  onOverlayChange?: (open: boolean) => void;
}) {
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set())
  const [diagnostic, setDiagnostic] = useState<ContainerSummary | null>(null)
  const [diagnosticRows, setDiagnosticRows] = useState<ContainerSummary[]>([])
  const [portRows, setPortRows] = useState<ContainerSummary[] | null>(null)
  const [imageDetails, setImageDetails] = useState<{ name: string; rows: ImageUpdateResult[] } | null>(null)
  const [containerMenuOpen, setContainerMenuOpen] = useState(false)
  const overlayOpen = !!diagnostic || !!portRows || !!imageDetails || containerMenuOpen
  useEffect(() => { onOverlayChange?.(overlayOpen); return () => onOverlayChange?.(false) }, [overlayOpen, onOverlayChange])
  const containers = rows ?? []
  const groups = groupServices(containers, declared)
  const oneOff = containers.filter(isOneOff)
  const pagination = useListPagination(groups, `${nodeID}/${projectName}`)
  const summary = aggregate(containers, observation)
  const matchingUpdates = (rows: ContainerSummary[]) => updates.filter(update => update.containers.some(container => rows.some(row => row.id === container.container_id)))
  const ports = (rows: ContainerSummary[]) => {
    const values = [...new Map(rows.flatMap(row => row.ports).map(port => [portLabel(port), port])).values()]
    return values.length ? <Button variant="ghost" size="sm" className="group h-auto min-w-0 max-w-full justify-start gap-1 p-0 text-left font-mono text-xs" onClick={() => setPortRows(rows)} aria-label={zh ? '查看完整端口映射' : 'View full port mappings'}><ContainerPortTag port={values[0]} className="text-[11px] font-normal" />{values.length > 1 && <span className="shrink-0 text-[11px] text-muted-foreground">+{values.length - 1}</span>}</Button> : <span className="text-xs text-muted-foreground">—</span>
  }
  const resources = (row: ContainerSummary, compact: boolean) => <div className={`col-span-2 row-start-2 grid min-w-0 items-start gap-3 @min-[36rem]/services:col-span-1 @min-[36rem]/services:col-start-2 @min-[36rem]/services:row-start-1 ${compact ? 'grid-cols-1' : 'grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)]'}`}>
    {!compact && <ProjectResourceSummary summary={aggregate([row], observation)} zh={zh} inline />}
    <div className="min-w-0"><p className="mb-1 text-xs text-muted-foreground">{zh ? '端口' : 'Ports'}</p>{ports([row])}</div>
  </div>
  const updateStatus = (name: string, rows: ImageUpdateResult[]) => showUpdates && (rows.length ? <Button variant="ghost" size="sm" className="h-auto max-w-full justify-start p-0" aria-label={`${zh ? '镜像更新详情' : 'Image update details'} · ${name}`} onClick={() => setImageDetails({ name, rows })}><UpdateStatus rows={rows} zh={zh} /></Button> : <UpdateStatus rows={rows} zh={zh} />)
  const health = (row: ContainerSummary) => {
    const meta = observation.runtime.get(row.id), abnormal = isAbnormal(row, meta)
    const reason = meta?.oom_killed ? 'OOM' : meta?.state_error ? (zh ? '运行错误' : 'Runtime error') : meta?.exit_code ? `${zh ? '退出码' : 'Exit'} ${meta.exit_code}` : row.state === 'restarting' ? stateLabel(row.state, zh) : healthLabel(effectiveHealth(row, meta), zh)
    return <Button variant="ghost" size="sm" className={`h-auto max-w-full justify-end whitespace-normal p-0 text-right text-xs ${abnormal ? 'text-destructive' : 'text-muted-foreground'}`} onClick={() => { setDiagnosticRows([row]); setDiagnostic(row) }} aria-label={`${zh ? '运行诊断' : 'Runtime diagnostics'} · ${row.name}`}>{reason}</Button>
  }
  const instance = (row: ContainerSummary, compact = false, showImage = false) => {
    const imageRows = matchingUpdates([row])
    return <div key={row.id} className={`grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-2 rounded-md bg-background/50 px-3 py-2.5 transition-colors hover:bg-background/70 ${compact ? '@min-[36rem]/services:grid-cols-[minmax(0,1fr)_8rem_auto]' : '@min-[36rem]/services:grid-cols-[minmax(0,1fr)_18rem_auto]'}`} data-testid="service-instance">
      <div className="min-w-0 space-y-1">
        <div className="flex min-w-0 items-start gap-1.5"><Box className="mt-0.5 size-3 shrink-0 text-muted-foreground" /><Link to="/containers/$containerId" params={{ containerId: row.id }} className="break-all text-xs hover:underline">{row.name}</Link></div>
        {!compact && <div className="flex flex-wrap items-center gap-x-2 gap-y-1"><StatusBadge tone={row.state === 'running' ? 'success' : row.state === 'dead' ? 'critical' : 'neutral'}>{stateLabel(row.state, zh)}</StatusBadge>{health(row)}</div>}
        {showImage && <><TooltipHint content={`${row.image}${row.image_id ? `\nImage ID · ${row.image_id}` : ''}`}><p className="truncate font-mono text-xs text-muted-foreground">{row.image}</p></TooltipHint>{updateStatus(row.name, imageRows)}</>}
      </div>
      {resources(row, compact)}
      <div className="col-start-2 row-start-1 justify-self-end @min-[36rem]/services:col-start-3"><ContainerActions row={row} zh={zh} onOpenContainer={onOpenContainer} onOpenChange={setContainerMenuOpen} /></div>
    </div>
  }
  return <section className="@container/services flex w-full min-w-0 flex-col gap-3" aria-label={zh ? '项目服务' : 'Project services'}>
    <div className="flex flex-wrap items-center justify-between gap-2"><div className="flex flex-wrap items-center gap-2"><h3 className="text-sm font-medium">{zh ? '服务' : 'Services'}</h3>{!loading && !error && <span className="text-xs text-muted-foreground">{groups.length} · {summary.running}/{summary.total} {zh ? '容器运行中' : 'containers running'}</span>}</div>{showUpdates && <ImageUpdateCheck nodeID={nodeID} target={{ project_name: projectName }} />}</div>
    {declarationUnavailable && <p className="text-xs text-muted-foreground">{zh ? '服务声明信息不可用，仅展示已发现的服务。' : 'Service declarations unavailable; showing discovered services.'}</p>}
    {showUpdates && updateError && <p role="alert" className="text-xs text-destructive">{updateError}</p>}
    {loading ? <LoadingState compact label={zh ? '正在加载项目服务' : 'Loading project services'} /> : error ? <ErrorState description={error} /> : <>
      {!groups.length && !oneOff.length && <p className="py-4 text-sm text-muted-foreground">{zh ? '项目尚未创建容器。' : 'No containers have been created.'}</p>}
      <div className="flex min-w-0 flex-col gap-3">{pagination.items.map(group => {
        const stats = aggregate(group.containers, observation), single = group.containers.length === 1, imageRows = matchingUpdates(group.containers)
        return <Collapsible key={group.name} open={expanded.has(group.name)} onOpenChange={open => setExpanded(old => { const next = new Set(old); if (open) next.add(group.name); else next.delete(group.name); return next })}>
          <Card size="sm" className="min-w-0 p-3" data-testid="service-group">
            <div className="grid min-w-0 items-start gap-3 @min-[36rem]/services:grid-cols-[minmax(0,1fr)_13rem]">
              <div className="min-w-0 flex-1 space-y-1">
                <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                  <CollapsibleTrigger disabled={group.containers.length < 2} className="flex min-w-0 items-center gap-1 text-left text-sm font-medium disabled:cursor-default" aria-label={`${zh ? '服务详情' : 'Service details'} ${group.name}`}>{group.containers.length > 1 && <ChevronRight className={`size-3 shrink-0 transition-transform ${expanded.has(group.name) ? 'rotate-90' : ''}`} />}<span className="break-all">{group.name}</span></CollapsibleTrigger>
                  <StatusBadge tone={stats.running ? stats.running === stats.total ? 'success' : 'warning' : 'neutral'}>{stats.total ? `${stats.running}/${stats.total} ${zh ? '运行中' : 'running'}` : zh ? '未创建' : 'Not created'}</StatusBadge>
                  {single ? health(group.containers[0]) : stats.total > 0 && <Button variant="ghost" size="sm" onClick={() => { setDiagnosticRows(group.containers); setDiagnostic(group.containers.find(row => isAbnormal(row, observation.runtime.get(row.id))) ?? group.containers[0]) }} className={`h-auto max-w-full justify-start whitespace-normal p-0 text-left text-xs ${stats.abnormal ? 'text-destructive' : 'text-muted-foreground'}`}>{stats.abnormal ? `${stats.abnormal} ${zh ? '异常' : 'abnormal'}${stats.unknown ? ` · ${stats.unknown} ${zh ? '未知' : 'unknown'}` : ''}` : stats.unknown ? `${stats.unknown} ${zh ? '健康未知' : 'health unknown'}` : group.containers.every(row => effectiveHealth(row, observation.runtime.get(row.id)) === 'none') ? healthLabel('none', zh) : `${zh ? '健康检查' : 'Health checks'} ${group.containers.filter(row => effectiveHealth(row, observation.runtime.get(row.id)) === 'healthy').length}/${stats.running}`}</Button>}
                </div>
                {group.images.length > 0 && <TooltipHint content={group.images.join('\n')}><p className="flex min-w-0 items-center gap-1 font-mono text-xs text-muted-foreground"><span className="truncate">{group.images[0].replace(/^docker\.io\/(library\/)?/, '')}</span>{group.images.length > 1 && <span className="shrink-0">+{group.images.length - 1} {zh ? '版本' : 'versions'}</span>}</p></TooltipHint>}
                {updateStatus(group.name, imageRows)}
              </div>
              <div className="w-full max-w-56 @min-[36rem]/services:max-w-none"><ProjectResourceSummary summary={stats} zh={zh} inline aggregated /></div>
            </div>
            {single && instance(group.containers[0], true)}
            {group.containers.length > 1 && <CollapsibleContent><div className="space-y-1.5">{group.containers.map(row => instance(row, false, group.images.length > 1))}</div></CollapsibleContent>}
          </Card>
        </Collapsible>
      })}</div>
      {!!oneOff.length && <Card size="sm" className="min-w-0 p-3"><h4 className="text-xs text-muted-foreground">{zh ? '一次性容器' : 'One-off containers'} · {oneOff.length}</h4><div className="space-y-1.5">{oneOff.map(row => <div key={row.id} className="min-w-0 space-y-2"><p className="break-all text-xs font-medium">{serviceName(row)}</p>{instance(row, false, true)}</div>)}</div></Card>}
      {pagination.total > pagination.pageSize && <ListPagination {...pagination} zh={zh} />}
    </>}
    <Dialog open={!!diagnostic} onOpenChange={open => { if (!open) setDiagnostic(null) }}><DialogContent className="max-h-[85dvh] overflow-auto sm:max-w-xl"><DialogHeader><DialogTitle className="break-all">{zh ? '运行诊断' : 'Runtime diagnostics'} · {diagnostic?.name}</DialogTitle></DialogHeader>{diagnosticRows.length > 1 && <DropdownMenu><DropdownMenuTrigger render={<Button variant="secondary" className="max-w-full truncate">{diagnostic?.name}</Button>} /><DropdownMenuContent className="max-h-64 overflow-auto">{diagnosticRows.map(row => <DropdownMenuItem key={row.id} onClick={() => setDiagnostic(row)}>{row.name}</DropdownMenuItem>)}</DropdownMenuContent></DropdownMenu>}{diagnostic && <DiagnosticsContent key={diagnostic.id} nodeID={nodeID} containerID={diagnostic.id} zh={zh} />}</DialogContent></Dialog>
    <Dialog open={!!portRows} onOpenChange={open => { if (!open) setPortRows(null) }}><DialogContent className="max-h-[85dvh] overflow-auto sm:max-w-xl"><DialogHeader><DialogTitle>{zh ? '完整端口映射' : 'Full port mappings'}</DialogTitle></DialogHeader>{portRows?.map(row => <div key={row.id} className="space-y-1"><p className="break-all text-xs text-muted-foreground">{row.name}</p>{row.ports.map((port, index) => <p key={index} className="break-all font-mono text-xs">{fullPortLabel(port)}</p>)}</div>)}</DialogContent></Dialog>
    <Dialog open={!!imageDetails} onOpenChange={open => { if (!open) setImageDetails(null) }}><DialogContent className="max-h-[85dvh] overflow-auto sm:max-w-xl"><DialogHeader><DialogTitle className="break-all">{zh ? '镜像更新详情' : 'Image update details'} · {imageDetails?.name}</DialogTitle></DialogHeader>{imageDetails && <ImageUpdateDetails rows={imageDetails.rows} zh={zh} />}</DialogContent></Dialog>
  </section>
}
