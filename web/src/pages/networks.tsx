import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Box, ChevronRight, CircleAlert, Network as NetworkIcon, Plus, Trash2, X } from 'lucide-react'
import { Fragment, useState, type ChangeEvent, type FormEvent } from 'react'
import { Alert, AlertAction, AlertDescription } from '../components/ui/alert'
import { Badge } from '../components/ui/badge'
import { Button } from '../components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '../components/ui/card'
import { Checkbox } from '../components/ui/checkbox'
import { ErrorState } from '../components/ui/error-state'
import { Input } from '../components/ui/input'
import { Label } from '../components/ui/label'
import { ListShell } from '../components/ui/list-shell'
import { ListPagination } from '../components/ui/list-pagination'
import { useListPagination } from '../components/ui/use-list-pagination'
import { LoadingState } from '../components/ui/loading-state'
import { Spinner } from '../components/ui/spinner'
import { Switch } from '../components/ui/switch'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'
import { TooltipHint } from '../components/ui/tooltip-hint'
import { api } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { nodePath } from '../lib/nodes'
import { confirmDialog } from '../stores/dialog'
import { useUIStore } from '../stores/ui'
import { ResourceFrame } from './images'

interface AttachedContainer { id: string; name: string; ipv4_address: string; ipv6_address: string }
interface Network { id: string; name: string; driver: string; scope: string; ipv6: boolean; internal: boolean; ipam: { subnet: string; gateway: string }[]; containers: number; attached_containers?: AttachedContainer[] }
interface NetworkValues { name: string; driver: string; subnet: string; gateway: string; ipv6: boolean }

const emptyNetwork: NetworkValues = { name: '', driver: 'bridge', subnet: '', gateway: '', ipv6: false }

export function NetworksPage() {
  const nodeID = useUIStore((state) => state.currentNodeID)
  const client = useQueryClient()
  const { t, language } = useI18n()
  const zh = language === 'zh-CN'
  const [creating, setCreating] = useState(false)
  const [values, setValues] = useState<NetworkValues>(emptyNetwork)
  const [selected, setSelected] = useState<Set<string>>(() => new Set())
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set())
  const [operationError, setOperationError] = useState('')
  const query = useQuery({ queryKey: ['networks', nodeID], queryFn: () => api<Network[]>(nodePath(nodeID, '/networks')) })
  const create = useMutation({ mutationFn: (values: NetworkValues) => api(nodePath(nodeID, '/networks'), { method: 'POST', body: JSON.stringify(values) }), onSuccess: () => { setCreating(false); client.invalidateQueries({ queryKey: ['networks', nodeID] }) } })
  const remove = useMutation({ mutationFn: (id: string) => api(nodePath(nodeID, `/networks/${encodeURIComponent(id)}?confirm=true`), { method: 'DELETE' }), onMutate: () => setOperationError(''), onSuccess: () => client.invalidateQueries({ queryKey: ['networks', nodeID] }), onError: (error) => setOperationError(error.message) })
  const batchRemove = useMutation({
    mutationFn: async (ids: string[]) => Promise.allSettled(ids.map((id) => api(nodePath(nodeID, `/networks/${encodeURIComponent(id)}?confirm=true`), { method: 'DELETE' }))),
    onMutate: () => setOperationError(''),
    onSuccess: async (results) => {
      const failed = results.filter((result) => result.status === 'rejected').length
      setSelected(new Set())
      if (failed) setOperationError(zh ? `${failed} 个网络删除失败。系统网络或仍有容器连接的网络无法删除。` : `${failed} networks could not be removed. System networks and networks with attached containers cannot be removed.`)
      await client.invalidateQueries({ queryKey: ['networks', nodeID] })
    },
    onError: (error) => setOperationError(error.message),
  })
  const removeNetwork = async (row: Network) => { if (await confirmDialog({ title: t('deleteNetwork'), description: t('deleteNetworkDescription', { name: row.name }), confirmLabel: t('remove'), danger: true })) remove.mutate(row.id) }
  const rows = query.data ?? []
  const pagination = useListPagination(rows)
  const selectedRows = rows.filter((row) => selected.has(row.id))
  const allSelected = pagination.items.length > 0 && pagination.items.every((row) => selected.has(row.id))
  const someSelected = !allSelected && pagination.items.some((row) => selected.has(row.id))
  const toggleAll = (checked: boolean | 'indeterminate') => setSelected((current) => { const next = new Set(current); for (const row of pagination.items) { if (checked === true) next.add(row.id); else next.delete(row.id) }; return next })
  const toggleOne = (id: string, checked: boolean) => setSelected((current) => { const next = new Set(current); if (checked) next.add(id); else next.delete(id); return next })
  const toggleExpanded = (id: string) => setExpanded((current) => { const next = new Set(current); if (next.has(id)) next.delete(id); else next.add(id); return next })
  const removeSelected = async () => {
    if (!selectedRows.length) return
    const names = selectedRows.slice(0, 3).map((row) => row.name).join('、')
    if (!await confirmDialog({ title: zh ? `删除选中的 ${selectedRows.length} 个网络？` : `Remove ${selectedRows.length} selected networks?`, description: zh ? `${names}${selectedRows.length > 3 ? ' 等' : ''}。系统网络或仍有容器连接的网络不会被删除。` : `${names}${selectedRows.length > 3 ? ' and others' : ''}. System networks and networks with attached containers will not be removed.`, confirmLabel: zh ? '批量删除' : 'Remove selected', danger: true })) return
    batchRemove.mutate(selectedRows.map((row) => row.id))
  }
  const toggleCreate = () => { setValues(emptyNetwork); setCreating(!creating) }
  const submitCreate = (event: FormEvent<HTMLFormElement>) => { event.preventDefault(); create.mutate(values) }
  const field = (key: keyof Omit<NetworkValues, 'ipv6'>) => ({ value: values[key], onChange: (event: ChangeEvent<HTMLInputElement>) => setValues({ ...values, [key]: event.target.value }) })

  const action = <div className="flex flex-wrap items-center gap-2">
    {!!selected.size && <><Badge variant="outline">{selected.size} {zh ? '已选' : 'selected'}</Badge><Button size="sm" variant="destructive" disabled={batchRemove.isPending} onClick={() => void removeSelected()}>{batchRemove.isPending ? <Spinner /> : <Trash2 />}{zh ? '删除' : 'Remove'}</Button></>}
    <Button onClick={toggleCreate}><Plus size={16} />{t('create')}</Button>
  </div>

  return <ResourceFrame title={t('networks')} detail={zh ? `${query.data?.length ?? 0} 个网络` : `${query.data?.length ?? 0} networks`} action={action}>
    <div className="flex w-full flex-col items-start gap-4">
      {creating && <Card className="w-full">
        <CardHeader><CardTitle>{zh ? '创建网络' : 'Create network'}</CardTitle></CardHeader>
        <CardContent>
          <form onSubmit={submitCreate} className="flex flex-col gap-4">
            <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="network-name">{zh ? '名称' : 'Name'}</Label>
                <Input id="network-name" required placeholder="app-network" {...field('name')} />
              </div>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="network-driver">{zh ? '驱动' : 'Driver'}</Label>
                <Input id="network-driver" {...field('driver')} />
              </div>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="network-subnet">{zh ? '子网' : 'Subnet'}</Label>
                <Input id="network-subnet" placeholder="172.24.0.0/16" {...field('subnet')} />
              </div>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="network-gateway">{zh ? '网关' : 'Gateway'}</Label>
                <Input id="network-gateway" placeholder="172.24.0.1" {...field('gateway')} />
              </div>
            </div>
            <div className="flex items-center justify-between gap-3">
              <Label htmlFor="network-ipv6"><Switch id="network-ipv6" checked={values.ipv6} onCheckedChange={(checked) => setValues({ ...values, ipv6: checked === true })} />IPv6</Label>
              <Button type="submit" disabled={create.isPending}>{create.isPending && <Spinner className="size-4" />}{t('create')}</Button>
            </div>
          </form>
        </CardContent>
      </Card>}
      {query.isPending ? <LoadingState compact rows={7} label={zh ? '正在加载网络' : 'Loading networks'} /> : query.isError ? <ErrorState description={query.error.message} /> : (query.data ?? []).length === 0 ? <div className="flex w-full flex-col items-center gap-1 rounded-xl bg-card px-4 py-10 text-center ring-1 ring-foreground/10">
        <NetworkIcon className="size-5 text-muted-foreground" />
        <p className="text-sm font-medium">{zh ? '暂无网络' : 'No networks'}</p>
        <p className="text-sm text-muted-foreground">{zh ? '创建网络后会显示在这里。' : 'Create a network to see it here.'}</p>
      </div> : <><ListShell>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-10 pl-3"><Checkbox checked={allSelected} indeterminate={someSelected} onCheckedChange={toggleAll} aria-label={allSelected ? (zh ? '取消选择本页' : 'Deselect this page') : (zh ? '选择本页' : 'Select this page')} /></TableHead>
              <TableHead className="w-9"><span className="sr-only">{zh ? '展开' : 'Expand'}</span></TableHead>
              <TableHead>{zh ? '网络' : 'Network'}</TableHead>
              <TableHead>{zh ? '驱动 / 范围' : 'Driver / scope'}</TableHead>
              <TableHead>{zh ? '子网 / 网关' : 'Subnet / gateway'}</TableHead>
              <TableHead>{zh ? '连接' : 'Attached'}</TableHead>
              <TableHead className="w-16"><span className="sr-only">{t('deleteNetwork')}</span></TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {pagination.items.map((row) => <NetworkRow key={row.id} row={row} nodeID={nodeID} open={expanded.has(row.id)} selected={selected.has(row.id)} zh={zh} deleteLabel={t('deleteNetwork')} removePending={remove.isPending} toggle={() => toggleExpanded(row.id)} select={(checked) => toggleOne(row.id, checked)} remove={() => void removeNetwork(row)} />)}
          </TableBody>
        </Table>
      </ListShell><ListPagination {...pagination} zh={zh} /></>}
      {!!operationError && <Alert variant="destructive" className="w-full"><CircleAlert /><AlertDescription>{operationError}</AlertDescription><AlertAction><Button variant="ghost" size="icon-xs" aria-label={zh ? '关闭' : 'Dismiss'} onClick={() => setOperationError('')}><X /></Button></AlertAction></Alert>}
      {create.isError && <ErrorState description={create.error.message} />}
    </div>
  </ResourceFrame>
}

function NetworkRow({ row, nodeID, open, selected, zh, deleteLabel, removePending, toggle, select, remove }: { row: Network; nodeID: string; open: boolean; selected: boolean; zh: boolean; deleteLabel: string; removePending: boolean; toggle: () => void; select: (checked: boolean) => void; remove: () => void }) {
  const detail = useQuery({ queryKey: ['network', nodeID, row.id], queryFn: () => api<Network>(nodePath(nodeID, `/networks/${encodeURIComponent(row.id)}`)), enabled: open })
  const containers = detail.data?.attached_containers ?? row.attached_containers ?? []
  const pagination = useListPagination(containers)
  return <Fragment>
    <TableRow data-state={selected ? 'selected' : undefined} aria-expanded={open}>
      <TableCell className="pl-3"><Checkbox checked={selected} onCheckedChange={(checked) => select(Boolean(checked))} aria-label={`${zh ? '选择' : 'Select'} ${row.name}`} /></TableCell>
      <TableCell><Button variant="ghost" size="icon-xs" onClick={toggle} aria-label={open ? (zh ? `收起 ${row.name}` : `Collapse ${row.name}`) : (zh ? `展开 ${row.name}` : `Expand ${row.name}`)}><ChevronRight className={open ? 'rotate-90 transition-transform' : 'transition-transform'} /></Button></TableCell>
      <TableCell><button type="button" className="text-left" onClick={toggle}><span className="font-medium">{row.name}</span><span className="block text-xs text-muted-foreground">{row.id.slice(0, 12)}</span></button></TableCell>
      <TableCell><div className="flex items-center gap-2"><Badge variant="outline">{row.driver}</Badge><span className="text-sm text-muted-foreground">{row.scope}</span></div></TableCell>
      <TableCell className="text-muted-foreground">{row.ipam?.[0]?.subnet || '—'} · {row.ipam?.[0]?.gateway || '—'}</TableCell>
      <TableCell><div className="flex items-center gap-2">{row.containers}{row.ipv6 && <Badge variant="secondary">IPv6</Badge>}{row.internal && <Badge variant="outline">{zh ? '内部' : 'Internal'}</Badge>}</div></TableCell>
      <TableCell><TooltipHint content={deleteLabel}><Button variant="ghost" size="icon-sm" className="text-red-600 hover:text-red-600 dark:text-red-400 dark:hover:text-red-400" disabled={removePending} onClick={remove} aria-label={deleteLabel}><Trash2 /></Button></TooltipHint></TableCell>
    </TableRow>
    {open && <TableRow><TableCell colSpan={7} className="bg-muted/30 p-4">
      {detail.isPending ? <LoadingState compact embedded rows={2} label={zh ? '正在加载网络内的容器' : 'Loading attached containers'} /> : detail.isError ? <ErrorState description={detail.error.message} /> : containers.length === 0 ? <div className="flex items-center gap-2 text-sm text-muted-foreground"><Box className="size-4" />{zh ? '此网络内暂无容器' : 'No containers attached to this network'}</div> : <><div className="max-h-72 overflow-y-auto overscroll-contain rounded-lg border bg-background">
        <div className="grid grid-cols-[minmax(180px,1fr)_minmax(150px,1fr)_minmax(150px,1fr)] gap-4 border-b bg-muted/40 px-3 py-2 text-xs font-medium text-muted-foreground"><span>{zh ? '容器' : 'Container'}</span><span>IPv4</span><span>IPv6</span></div>
        {pagination.items.map((container) => <div key={container.id} className="grid grid-cols-[minmax(180px,1fr)_minmax(150px,1fr)_minmax(150px,1fr)] gap-4 border-b px-3 py-2.5 text-sm last:border-b-0"><span className="min-w-0"><span className="block truncate font-medium">{container.name || container.id.slice(0, 12)}</span><span className="block font-mono text-xs text-muted-foreground">{container.id.slice(0, 12)}</span></span><span className="self-center font-mono text-xs">{container.ipv4_address || '—'}</span><span className="self-center font-mono text-xs">{container.ipv6_address || '—'}</span></div>)}
      </div><ListPagination {...pagination} zh={zh} /></>}
    </TableCell></TableRow>}
  </Fragment>
}
