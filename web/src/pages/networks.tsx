import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Network as NetworkIcon, Plus, Trash2 } from 'lucide-react'
import { useState, type ChangeEvent, type FormEvent } from 'react'
import { Badge } from '../components/ui/badge'
import { Button } from '../components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '../components/ui/card'
import { ErrorState } from '../components/ui/error-state'
import { Input } from '../components/ui/input'
import { Label } from '../components/ui/label'
import { ListShell } from '../components/ui/list-shell'
import { LoadingState } from '../components/ui/loading-state'
import { Spinner } from '../components/ui/spinner'
import { Switch } from '../components/ui/switch'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'
import { api } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { nodePath } from '../lib/nodes'
import { confirmDialog } from '../stores/dialog'
import { useUIStore } from '../stores/ui'
import { ResourceFrame } from './images'

interface Network { id: string; name: string; driver: string; scope: string; ipv6: boolean; internal: boolean; ipam: { subnet: string; gateway: string }[]; containers: number }
interface NetworkValues { name: string; driver: string; subnet: string; gateway: string; ipv6: boolean }

const emptyNetwork: NetworkValues = { name: '', driver: 'bridge', subnet: '', gateway: '', ipv6: false }

export function NetworksPage() {
  const nodeID = useUIStore((state) => state.currentNodeID)
  const client = useQueryClient()
  const { t, language } = useI18n()
  const zh = language === 'zh-CN'
  const [creating, setCreating] = useState(false)
  const [values, setValues] = useState<NetworkValues>(emptyNetwork)
  const query = useQuery({ queryKey: ['networks', nodeID], queryFn: () => api<Network[]>(nodePath(nodeID, '/networks')) })
  const create = useMutation({ mutationFn: (values: NetworkValues) => api(nodePath(nodeID, '/networks'), { method: 'POST', body: JSON.stringify(values) }), onSuccess: () => { setCreating(false); client.invalidateQueries({ queryKey: ['networks', nodeID] }) } })
  const remove = useMutation({ mutationFn: (id: string) => api(nodePath(nodeID, `/networks/${id}?confirm=true`), { method: 'DELETE' }), onSuccess: () => client.invalidateQueries({ queryKey: ['networks', nodeID] }) })
  const removeNetwork = async (row: Network) => { if (await confirmDialog({ title: t('deleteNetwork'), description: t('deleteNetworkDescription', { name: row.name }), confirmLabel: t('remove'), danger: true })) remove.mutate(row.id) }
  const toggleCreate = () => { setValues(emptyNetwork); setCreating(!creating) }
  const submitCreate = (event: FormEvent<HTMLFormElement>) => { event.preventDefault(); create.mutate(values) }
  const field = (key: keyof Omit<NetworkValues, 'ipv6'>) => ({ value: values[key], onChange: (event: ChangeEvent<HTMLInputElement>) => setValues({ ...values, [key]: event.target.value }) })

  return <ResourceFrame title={t('networks')} detail={zh ? `${query.data?.length ?? 0} 个网络` : `${query.data?.length ?? 0} networks`} action={<Button onClick={toggleCreate}><Plus size={16} />{t('create')}</Button>}>
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
      </div> : <ListShell>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{zh ? '网络' : 'Network'}</TableHead>
              <TableHead>{zh ? '驱动 / 范围' : 'Driver / scope'}</TableHead>
              <TableHead>{zh ? '子网 / 网关' : 'Subnet / gateway'}</TableHead>
              <TableHead>{zh ? '连接' : 'Attached'}</TableHead>
              <TableHead className="w-16"><span className="sr-only">{t('deleteNetwork')}</span></TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {(query.data ?? []).map((row) => (
              <TableRow key={row.id}>
                <TableCell><div><span className="font-medium">{row.name}</span><span className="block text-xs text-muted-foreground">{row.id.slice(0, 12)}</span></div></TableCell>
                <TableCell><div className="flex items-center gap-2"><Badge variant="outline">{row.driver}</Badge><span className="text-sm text-muted-foreground">{row.scope}</span></div></TableCell>
                <TableCell className="text-muted-foreground">{row.ipam?.[0]?.subnet || '—'} · {row.ipam?.[0]?.gateway || '—'}</TableCell>
                <TableCell><div className="flex items-center gap-2">{row.containers}{row.ipv6 && <Badge variant="secondary">IPv6</Badge>}{row.internal && <Badge variant="outline">{zh ? '内部' : 'Internal'}</Badge>}</div></TableCell>
                <TableCell><Button variant="ghost" size="icon-sm" className="text-red-600 hover:text-red-600 dark:text-red-400 dark:hover:text-red-400" onClick={() => void removeNetwork(row)} title={t('deleteNetwork')} aria-label={t('deleteNetwork')}><Trash2 /></Button></TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </ListShell>}
      {create.isError && <ErrorState description={create.error.message} />}
    </div>
  </ResourceFrame>
}
