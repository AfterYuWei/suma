import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CircleAlert, Download, Package, Search, Tag as TagIcon, Trash2, X } from 'lucide-react'
import { type ReactNode, useEffect, useState } from 'react'
import { Alert, AlertAction, AlertDescription } from '../components/ui/alert'
import { Badge } from '../components/ui/badge'
import { Button } from '../components/ui/button'
import { Checkbox } from '../components/ui/checkbox'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '../components/ui/dialog'
import { Input } from '../components/ui/input'
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupInput } from '../components/ui/input-group'
import { Label } from '../components/ui/label'
import { ListShell } from '../components/ui/list-shell'
import { ListPagination } from '../components/ui/list-pagination'
import { useListPagination } from '../components/ui/use-list-pagination'
import { LoadingState } from '../components/ui/loading-state'
import { ErrorState } from '../components/ui/error-state'
import { Progress } from '../components/ui/progress'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../components/ui/select'
import { Sheet, SheetContent, SheetHeader, SheetTitle } from '../components/ui/sheet'
import { Spinner } from '../components/ui/spinner'
import { StatusBadge } from '../components/ui/status-badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'
import { TooltipHint } from '../components/ui/tooltip-hint'
import { api } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { nodePath } from '../lib/nodes'
import { confirmDialog, promptDialog } from '../stores/dialog'
import { useUIStore } from '../stores/ui'

interface Image { id: string; tags: string[]; digests: string[]; size: number; created: string; containers: number; architecture?: string; os?: string; author?: string; docker_version?: string; layers?: string[] }
interface RegistryCredential { id: number; name: string; server_address: string; authorized_node_ids: string[] }
interface PullTask { id: string; type: string; name: string; status: string; progress: number; message: string }
interface TaskStep { id: string; status: string; current: number; total: number; progress: number }

export function ImagesPage() {
  const nodeID = useUIStore((state) => state.currentNodeID)
  const client = useQueryClient()
  const [pullOpen, setPullOpen] = useState(false)
  const [pullTaskID, setPullTaskID] = useState('')
  const [reference, setReference] = useState('')
  const [detailImageID, setDetailImageID] = useState('')
  const [filter, setFilter] = useState('')
  const [selected, setSelected] = useState<Set<string>>(() => new Set())
  const [operationError, setOperationError] = useState('')
  const [credentialID, setCredentialID] = useState('')
  const { t, language } = useI18n()
  const zh = language === 'zh-CN'
  const query = useQuery({ queryKey: ['images', nodeID], queryFn: () => api<Image[]>(nodePath(nodeID, '/images')) })
  const detail = useQuery({ queryKey: ['image', nodeID, detailImageID], queryFn: () => api<Image>(nodePath(nodeID, `/images/${encodeURIComponent(detailImageID)}`)), enabled: !!detailImageID })
  const credentials = useQuery({ queryKey: ['registry-credentials'], queryFn: () => api<RegistryCredential[]>('/credentials/registries') })
  const pull = useMutation({ mutationFn: () => api<PullTask>(nodePath(nodeID, '/images/pull'), { method: 'POST', body: JSON.stringify({ reference: reference.trim(), credential_id: credentialID ? Number(credentialID) : undefined }) }), onSuccess: (task) => { setPullTaskID(task.id); client.invalidateQueries({ queryKey: ['tasks', 'current', nodeID] }) } })
  const pullTasks = useQuery({ queryKey: ['tasks', 'current', nodeID], queryFn: () => api<PullTask[]>(nodePath(nodeID, '/tasks')), enabled: !!pullTaskID, refetchInterval: (result) => {
    const task = result.state.data?.find((row) => row.id === pullTaskID)
    return task && ['success', 'failed', 'canceled'].includes(task.status) ? false : 1_000
  } })
  const cancelPull = useMutation({ mutationFn: (id: string) => api(nodePath(nodeID, `/tasks/${encodeURIComponent(id)}/cancel`), { method: 'POST' }), onSuccess: () => client.invalidateQueries({ queryKey: ['tasks'] }) })
  const remove = useMutation({ mutationFn: (id: string) => api(nodePath(nodeID, `/images/${encodeURIComponent(id)}?force=false`), { method: 'DELETE' }), onMutate: () => setOperationError(''), onSuccess: (_result, id) => { if (detailImageID === id) setDetailImageID(''); client.invalidateQueries({ queryKey: ['images', nodeID] }) }, onError: (error) => setOperationError(error.message) })
  const batchRemove = useMutation({
    mutationFn: async (ids: string[]) => Promise.allSettled(ids.map((id) => api(nodePath(nodeID, `/images/${encodeURIComponent(id)}?force=false`), { method: 'DELETE' }))),
    onMutate: () => setOperationError(''),
    onSuccess: async (results) => {
      const failed = results.filter((result) => result.status === 'rejected').length
      setSelected(new Set())
      if (failed) setOperationError(zh ? `${failed} 个镜像删除失败，正在使用或存在多个标签的镜像无法直接删除。` : `${failed} images could not be removed because they are in use or have multiple tags.`)
      await client.invalidateQueries({ queryKey: ['images', nodeID] })
    },
    onError: (error) => setOperationError(error.message),
  })
  const tag = async (row: Image) => { const value = await promptDialog({ title: t('newImageTag'), description: t('tagDescription'), confirmLabel: t('tag'), input: { label: t('newImageTag'), initialValue: row.tags?.[0] || 'repository:tag' } }); if (!value) return; await api(nodePath(nodeID, `/images/${encodeURIComponent(row.id)}/tag`), { method: 'POST', body: JSON.stringify({ reference: value }) }); await client.invalidateQueries({ queryKey: ['images', nodeID] }) }
  const removeImage = async (row: Image) => { const name = row.tags?.[0] || row.id; if (await confirmDialog({ title: t('removeImage'), description: t('removeImageDescription', { name }), confirmLabel: t('remove'), danger: true })) remove.mutate(row.id) }
  const size = (bytes: number) => `${(bytes / 1024 ** 2).toFixed(1)} MB`
  const credentialOptions = [{ value: '', label: zh ? '不使用凭据（公开镜像）' : 'No credential (public image)' }, ...(credentials.data || []).filter((row) => row.authorized_node_ids?.includes(nodeID)).map((row) => ({ value: String(row.id), label: `${row.name} · ${row.server_address}` }))]
  const rows = (query.data ?? []).filter((row) => `${row.id} ${(row.tags ?? []).join(' ')} ${(row.digests ?? []).join(' ')}`.toLowerCase().includes(filter.toLowerCase()))
  const pagination = useListPagination(rows, filter)
  const used = query.data?.filter((row) => row.containers > 0).length ?? 0
  const unused = (query.data?.length ?? 0) - used
  const trackedPull = pullTasks.data?.find((row) => row.id === pullTaskID) ?? (pull.data?.id === pullTaskID ? pull.data : undefined)
  const pullRunning = !!trackedPull && (trackedPull.status === 'pending' || trackedPull.status === 'running')
  const pullSteps = useQuery({ queryKey: ['task-steps', nodeID, pullTaskID], queryFn: () => api<TaskStep[]>(nodePath(nodeID, `/tasks/${encodeURIComponent(pullTaskID)}/steps`)), enabled: !!pullTaskID, refetchInterval: pullRunning ? 1_000 : false })
  const selectedRows = query.data?.filter((row) => selected.has(row.id)) ?? []
  const allSelected = pagination.items.length > 0 && pagination.items.every((row) => selected.has(row.id))
  const someSelected = !allSelected && pagination.items.some((row) => selected.has(row.id))

  const toggleAll = (checked: boolean | 'indeterminate') => setSelected((current) => { const next = new Set(current); for (const row of pagination.items) { if (checked === true) next.add(row.id); else next.delete(row.id) }; return next })
  const toggleOne = (id: string, checked: boolean) => setSelected((current) => { const next = new Set(current); if (checked) next.add(id); else next.delete(id); return next })
  const openPull = () => {
    if (!pullRunning) {
      setPullTaskID('')
      setReference('')
      setCredentialID('')
      pull.reset()
      cancelPull.reset()
    }
    setPullOpen(true)
  }
  const removeSelected = async () => {
    if (!selectedRows.length) return
    const names = selectedRows.slice(0, 3).map((row) => row.tags?.[0] || row.id.replace('sha256:', '').slice(0, 12)).join('、')
    if (!await confirmDialog({ title: zh ? `删除选中的 ${selectedRows.length} 个镜像？` : `Remove ${selectedRows.length} selected images?`, description: zh ? `${names}${selectedRows.length > 3 ? ' 等' : ''}。正在使用的镜像不会被强制删除。` : `${names}${selectedRows.length > 3 ? ' and others' : ''}. Images in use will not be force removed.`, confirmLabel: zh ? '批量删除' : 'Remove selected', danger: true })) return
    batchRemove.mutate(selectedRows.map((row) => row.id))
  }

  useEffect(() => {
    if (trackedPull?.status === 'success') {
      void client.invalidateQueries({ queryKey: ['images', nodeID] })
    }
  }, [client, nodeID, trackedPull?.status])

  const selectionBar = selected.size > 0 && <div className="flex flex-wrap items-center gap-2">
    <Badge variant="outline">{selected.size} {zh ? '已选' : 'selected'}</Badge>
    <Button size="sm" variant="destructive" disabled={batchRemove.isPending} onClick={() => void removeSelected()}>{batchRemove.isPending ? <Spinner /> : <Trash2 />}{zh ? '删除' : 'Remove'}</Button>
  </div>

  const toolbar = <div className="flex flex-wrap items-center gap-2">
    {selectionBar}
    <InputGroup className="w-[260px]">
      <InputGroupAddon align="inline-start"><Search /></InputGroupAddon>
      <InputGroupInput value={filter} onChange={(event) => setFilter(event.target.value)} placeholder={zh ? '名称、标签或摘要…' : 'Name, tag, or digest…'} aria-label={zh ? '筛选镜像' : 'Filter images'} />
      {!!filter && <InputGroupAddon align="inline-end"><InputGroupButton size="icon-xs" aria-label={zh ? '清空筛选' : 'Clear filter'} onClick={() => setFilter('')}><X /></InputGroupButton></InputGroupAddon>}
    </InputGroup>
    <Button onClick={openPull}><Download />Pull</Button>
  </div>

  const statusStrip = <>
    <Badge variant="outline">{used} {zh ? '已使用镜像' : 'used images'}</Badge>
    <Badge variant="outline" className="text-muted-foreground">{unused} {zh ? '未使用镜像' : 'unused images'}</Badge>
  </>

  return <ResourceFrame title={t('images')} detail={zh ? `${query.data?.length ?? 0} 个本地镜像` : `${query.data?.length ?? 0} local images`} lead={statusStrip} action={toolbar}>
    {query.isPending ? <LoadingState compact rows={7} label={zh ? '正在加载镜像' : 'Loading images'} /> : query.isError ? <ErrorState description={query.error.message} /> : (query.data ?? []).length === 0 ? <EmptyState icon={<Package size={20} />} title={zh ? '暂无本地镜像' : 'No local images'} detail={zh ? '输入镜像引用并拉取后会显示在这里。' : 'Pull an image reference to see it here.'} /> : rows.length === 0 ? <EmptyState icon={<Search size={20} />} title={zh ? '没有匹配的镜像' : 'No matching images'} detail={zh ? '调整筛选条件后再试。' : 'Adjust the filter and try again.'} /> :
      <><ListShell>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-10 pl-3">
                <Checkbox checked={allSelected} indeterminate={someSelected} onCheckedChange={(checked) => toggleAll(checked)} aria-label={allSelected ? (zh ? '取消选择本页' : 'Deselect this page') : (zh ? '选择本页' : 'Select this page')} />
              </TableHead>
              <TableHead>{zh ? '镜像' : 'Image'}</TableHead>
              <TableHead className="min-w-[110px]">{zh ? '大小' : 'Size'}</TableHead>
              <TableHead className="min-w-[90px]">{zh ? '引用' : 'Usage'}</TableHead>
              <TableHead className="min-w-[180px]">{zh ? '创建时间' : 'Created'}</TableHead>
              <TableHead className="min-w-[96px]">{zh ? '操作' : 'Actions'}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {pagination.items.map((row) => (
              <TableRow key={row.id} data-state={selected.has(row.id) ? 'selected' : undefined}>
                <TableCell className="pl-3">
                  <Checkbox checked={selected.has(row.id)} onCheckedChange={(checked) => toggleOne(row.id, Boolean(checked))} aria-label={`${zh ? '选择' : 'Select'} ${row.tags?.[0] || row.id}`} />
                </TableCell>
                <TableCell>
                  <button type="button" onClick={() => setDetailImageID(row.id)} className="-ml-2 flex items-center gap-2.5 rounded-md px-2 py-1 text-left outline-none transition-colors focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50">
                    <Package className="size-5 shrink-0 text-muted-foreground" />
                    <span className="flex flex-col">
                      <TooltipHint content={row.tags?.[0] || '<none>:<none>'}><span className="max-w-[300px] truncate font-medium">{row.tags?.[0] || '<none>:<none>'}</span></TooltipHint>
                      <span className="font-mono text-xs text-muted-foreground">{row.id.replace('sha256:', '').slice(0, 12)}</span>
                    </span>
                  </button>
                </TableCell>
                <TableCell>{size(row.size)}</TableCell>
                <TableCell>{row.containers < 0 ? '—' : String(row.containers)}</TableCell>
                <TableCell>{new Date(row.created).toLocaleString(language)}</TableCell>
                <TableCell>
                  <div className="flex items-center gap-1">
                    <TooltipHint content={t('tag')}><Button variant="ghost" size="icon-sm" aria-label={t('tag')} onClick={() => void tag(row)}><TagIcon /></Button></TooltipHint>
                    <TooltipHint content={t('removeImage')}><Button variant="destructive" size="icon-sm" aria-label={t('removeImage')} onClick={() => void removeImage(row)}><Trash2 /></Button></TooltipHint>
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </ListShell><ListPagination {...pagination} zh={zh} /></>}

    {!!operationError && (
      <Alert variant="destructive" className="mt-4 w-full">
        <CircleAlert />
        <AlertDescription>{operationError}</AlertDescription>
        <AlertAction><Button variant="ghost" size="icon-xs" aria-label={zh ? '关闭' : 'Dismiss'} onClick={() => setOperationError('')}><X /></Button></AlertAction>
      </Alert>
    )}

    <Dialog open={pullOpen} onOpenChange={setPullOpen}>
      {pullOpen && <DialogContent className="sm:max-w-md">
        {pullTaskID ? <PullProgressView task={trackedPull} steps={pullSteps.data ?? []} loading={pullTasks.isPending} stepsLoading={pullSteps.isPending} queryError={pullTasks.error?.message || pullSteps.error?.message} cancelError={cancelPull.error?.message} canceling={cancelPull.isPending} zh={zh} reference={reference} close={() => setPullOpen(false)} cancel={() => cancelPull.mutate(pullTaskID)} /> : <>
          <DialogHeader>
            <DialogTitle>{zh ? '拉取镜像' : 'Pull image'}</DialogTitle>
            <DialogDescription>{zh ? '公开镜像无需凭据；拉取私有镜像时，请选择认证中心中已授权给当前节点的镜像仓库凭据。' : 'Public images need no credential. For a private image, select a registry credential authorized for this node.'}</DialogDescription>
          </DialogHeader>
          <form onSubmit={(event) => { event.preventDefault(); if (reference.trim()) pull.mutate() }}>
            <div className="flex flex-col gap-4">
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="pull-image-reference">{zh ? '镜像引用' : 'Image reference'}</Label>
                <Input id="pull-image-reference" autoFocus required value={reference} onChange={(event) => setReference(event.target.value)} placeholder="nginx:latest" />
                <p className="text-xs text-muted-foreground">{zh ? '格式示例：nginx:latest 或 registry.example.com/team/app:1.0' : 'Examples: nginx:latest or registry.example.com/team/app:1.0'}</p>
              </div>
              <div className="flex flex-col gap-1.5">
                <Label>{zh ? '镜像仓库凭据（可选）' : 'Registry credential (optional)'}</Label>
                <Select items={credentialOptions} value={credentialID} modal={false} onValueChange={(value) => setCredentialID(String(value ?? ''))}>
                  <SelectTrigger className="w-full" aria-label={zh ? '镜像仓库凭据' : 'Registry credential'}><SelectValue /></SelectTrigger>
                  <SelectContent align="start" alignItemWithTrigger={false}>
                    {credentialOptions.map((option) => <SelectItem key={option.value} value={option.value}>{option.label}</SelectItem>)}
                  </SelectContent>
                </Select>
              </div>
              {pull.isError && <ErrorState description={pull.error.message} />}
            </div>
            <DialogFooter className="mt-4">
              <Button type="button" variant="outline" onClick={() => setPullOpen(false)}>{zh ? '关闭' : 'Close'}</Button>
              <Button type="submit" disabled={!reference.trim() || pull.isPending}>{pull.isPending ? <Spinner /> : <Download />}Pull</Button>
            </DialogFooter>
          </form>
        </>}
      </DialogContent>}
    </Dialog>

    <Sheet open={!!detailImageID} onOpenChange={(open) => { if (!open) setDetailImageID('') }}>
      <SheetContent side="right" className="w-[448px] max-w-full gap-0 sm:max-w-[448px]">
        <SheetHeader className="border-b"><SheetTitle className="truncate pr-6">{detail.data?.tags?.[0] || detailImageID.slice(0, 19)}</SheetTitle></SheetHeader>
        <div className="flex-1 overflow-y-auto p-4">
          {detail.isPending ? <LoadingState compact embedded rows={5} label={zh ? '正在加载镜像详情' : 'Loading image details'} /> : <div className="divide-y divide-border">
            {([[ 'ID', detail.data?.id ?? '—' ], [zh ? '大小' : 'Size', detail.data ? size(detail.data.size) : '—'], [zh ? '平台' : 'Platform', `${detail.data?.os || '—'} / ${detail.data?.architecture || '—'}`], [zh ? '创建时间' : 'Created', detail.data?.created ? new Date(detail.data.created).toLocaleString(language) : '—'], [zh ? '层数' : 'Layers', String(detail.data?.layers?.length ?? 0)]] as [string, string][]).map(([key, value]) => (
              <div key={key} className="flex items-start justify-between gap-6 py-2.5 first:pt-0 last:pb-0">
                <span className="shrink-0 text-xs text-muted-foreground">{key}</span>
                <span className="min-w-0 break-all text-right font-mono text-xs">{value}</span>
              </div>
            ))}
          </div>}
        </div>
      </SheetContent>
    </Sheet>
  </ResourceFrame>
}

function PullProgressView({ task, steps, loading, stepsLoading, queryError, cancelError, canceling, zh, reference, close, cancel }: { task?: PullTask; steps: TaskStep[]; loading: boolean; stepsLoading: boolean; queryError?: string; cancelError?: string; canceling: boolean; zh: boolean; reference: string; close: () => void; cancel: () => void }) {
  const pagination = useListPagination(steps)
  const status = task?.status || 'pending'
  const running = status === 'pending' || status === 'running'
  const label = zh ? ({ pending: '等待中', running: '拉取中', success: '已完成', failed: '失败', canceled: '已取消' }[status] ?? status) : ({ pending: 'Pending', running: 'Pulling', success: 'Completed', failed: 'Failed', canceled: 'Canceled' }[status] ?? status)
  const tone = status === 'success' ? 'success' : status === 'failed' || status === 'canceled' ? 'critical' : status === 'running' ? 'warning' : 'neutral'
  return <>
    <DialogHeader>
      <div className="flex items-center gap-2 pr-8">
        <DialogTitle>{zh ? '镜像拉取进度' : 'Image pull progress'}</DialogTitle>
        <StatusBadge tone={tone}>{label}</StatusBadge>
      </div>
      <DialogDescription className="break-all">{reference}</DialogDescription>
    </DialogHeader>
    {loading && !task ? <LoadingState embedded compact rows={3} label={zh ? '正在读取拉取进度' : 'Loading pull progress'} /> : <div className="flex flex-col gap-3">
      <div className="flex items-center justify-between gap-4 text-sm">
        <span className="text-muted-foreground">{zh ? '任务进度' : 'Task progress'}</span>
        <span className="font-medium tabular-nums">{task?.progress ?? 0}%</span>
      </div>
      <Progress value={task?.progress ?? 0} />
      <p className="min-h-5 break-all text-sm text-muted-foreground">{task?.message || (zh ? '等待 Docker 返回进度…' : 'Waiting for Docker progress…')}</p>
      <div className="flex items-center justify-between gap-4 border-t pt-3">
        <span className="text-sm font-medium">Layers</span>
        <span className="text-xs text-muted-foreground">{steps.length}</span>
      </div>
      {stepsLoading && steps.length === 0 ? <LoadingState embedded compact rows={2} label={zh ? '正在读取 Layer 进度' : 'Loading layer progress'} /> : steps.length === 0 ? <p className="rounded-lg border border-dashed px-3 py-5 text-center text-xs text-muted-foreground">{zh ? '等待 Docker 返回 Layer 信息…' : 'Waiting for Docker layer information…'}</p> : <><div className="flex max-h-72 flex-col gap-3 overflow-y-auto overscroll-contain pr-1">
        {pagination.items.map((step) => <div key={step.id} className="flex flex-col gap-1.5 rounded-lg border p-3">
          <div className="flex items-center justify-between gap-3">
            <span className="truncate font-mono text-xs font-medium">{step.id}</span>
            <span className="shrink-0 text-xs font-medium tabular-nums">{step.progress}%</span>
          </div>
          <Progress value={step.progress} />
          <div className="flex items-center justify-between gap-3 text-xs text-muted-foreground">
            <span>{layerStatus(step.status, zh)}</span>
            {step.total > 0 && <span className="shrink-0 tabular-nums">{formatBytes(step.current)} / {formatBytes(step.total)}</span>}
          </div>
        </div>)}
      </div><ListPagination {...pagination} zh={zh} /></>}
      {queryError && <ErrorState description={queryError} />}
      {cancelError && <ErrorState description={cancelError} />}
      <p className="text-xs text-muted-foreground">{running ? (zh ? '直接关闭此窗口不会停止拉取，任务会继续在后台运行。' : 'Closing this window does not stop the pull; the task continues in the background.') : (zh ? '可在任务中心查看本次拉取记录。' : 'You can review this pull in the Task Center.')}</p>
    </div>}
    <DialogFooter className="mt-1">
      {running && <Button type="button" variant="destructive" disabled={canceling} onClick={cancel}>{canceling ? <Spinner /> : <X />}{zh ? '取消拉取' : 'Cancel pull'}</Button>}
      <Button type="button" variant="outline" onClick={close}>{zh ? '关闭窗口' : 'Close window'}</Button>
    </DialogFooter>
  </>
}

function layerStatus(status: string, zh: boolean) {
  if (!zh) return status
  return ({ 'Pulling fs layer': '准备 Layer', Waiting: '排队等待', Downloading: '下载中', 'Verifying Checksum': '校验中', 'Download complete': '下载完成', Extracting: '解压中', 'Pull complete': '已完成', 'Already exists': '本地已存在', Canceled: '已取消', Failed: '失败' } as Record<string, string>)[status] || status
}

function formatBytes(bytes: number) {
  if (bytes >= 1024 ** 3) return `${(bytes / 1024 ** 3).toFixed(2)} GB`
  if (bytes >= 1024 ** 2) return `${(bytes / 1024 ** 2).toFixed(1)} MB`
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${bytes} B`
}

export function ResourceFrame({ title, detail, lead, action, children }: { eyebrow?: string; title: string; detail: string; lead?: ReactNode; action?: ReactNode; children: ReactNode }) {
  return <div className="flex w-full flex-col items-start gap-5">
    <header className="flex w-full flex-wrap items-center gap-4">
      <div className="min-w-0">
        <h2 className="cn-font-heading text-lg leading-none font-semibold tracking-tight">{title}</h2>
        <p className="mt-1.5 text-sm text-muted-foreground">{detail}</p>
      </div>
      {lead}
      {action && <div className="ml-auto">{action}</div>}
    </header>
    <div className="w-full">{children}</div>
  </div>
}

export function EmptyState({ icon, title, detail }: { icon: ReactNode; title: string; detail: string }) {
  return <div className="flex w-full flex-col items-center justify-center gap-1 rounded-xl border border-dashed py-12 text-center">
    <div className="mb-1.5 flex size-10 items-center justify-center rounded-full bg-muted text-muted-foreground [&_svg]:size-5">{icon}</div>
    <p className="text-sm font-medium">{title}</p>
    <p className="max-w-sm text-sm text-muted-foreground">{detail}</p>
  </div>
}
