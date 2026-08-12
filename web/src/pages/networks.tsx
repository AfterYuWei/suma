import { Switch } from '@base-ui/react/switch'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Plus, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { LoadingState } from '../components/ui/loading-state'
import { api } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { confirmDialog } from '../stores/dialog'
import { ResourceFrame } from './images'

interface Network { id: string; name: string; driver: string; scope: string; ipv6: boolean; internal: boolean; ipam: { subnet: string; gateway: string }[]; containers: number }

export function NetworksPage() {
  const client = useQueryClient(); const { t, language } = useI18n()
  const [creating, setCreating] = useState(false); const [name, setName] = useState(''); const [driver, setDriver] = useState('bridge'); const [subnet, setSubnet] = useState(''); const [gateway, setGateway] = useState(''); const [ipv6, setIPv6] = useState(false)
  const query = useQuery({ queryKey: ['networks'], queryFn: () => api<Network[]>('/networks') })
  const create = useMutation({ mutationFn: () => api('/networks', { method: 'POST', body: JSON.stringify({ name, driver, subnet, gateway, ipv6 }) }), onSuccess: () => { setName(''); setSubnet(''); setGateway(''); setCreating(false); client.invalidateQueries({ queryKey: ['networks'] }) } })
  const remove = useMutation({ mutationFn: (id: string) => api(`/networks/${id}?confirm=true`, { method: 'DELETE' }), onSuccess: () => client.invalidateQueries({ queryKey: ['networks'] }) })
  const removeNetwork = async (row: Network) => { if (await confirmDialog({ title: t('deleteNetwork'), description: t('deleteNetworkDescription', { name: row.name }), confirmLabel: t('remove'), danger: true })) remove.mutate(row.id) }

  return <ResourceFrame eyebrow="Docker" title={t('networks')} detail={language === 'zh-CN' ? `${query.data?.length ?? 0} 个网络` : `${query.data?.length ?? 0} networks`} action={<button onClick={() => setCreating(!creating)} className="flex h-8 items-center gap-2 rounded-md border border-border bg-surface px-3 text-xs"><Plus className="size-3.5" />{t('create')}</button>}>
    {creating && <form onSubmit={(event) => { event.preventDefault(); if (name) create.mutate() }} className="mb-5 grid gap-3 border-y border-border bg-surface/50 p-4 sm:grid-cols-2 lg:grid-cols-5"><Field label={language === 'zh-CN' ? '名称' : 'Name'} value={name} set={setName} placeholder="app-network" /><Field label={language === 'zh-CN' ? '驱动' : 'Driver'} value={driver} set={setDriver} /><Field label={language === 'zh-CN' ? '子网' : 'Subnet'} value={subnet} set={setSubnet} placeholder="172.24.0.0/16" /><Field label={language === 'zh-CN' ? '网关' : 'Gateway'} value={gateway} set={setGateway} placeholder="172.24.0.1" /><div className="flex items-end gap-3"><label className="flex h-8 items-center gap-2 text-xs"><Switch.Root checked={ipv6} onCheckedChange={setIPv6} className="relative h-5 w-9 rounded-full bg-muted transition-colors data-[checked]:bg-accent"><Switch.Thumb className="block size-4 translate-x-0.5 rounded-full bg-white shadow transition-transform data-[checked]:translate-x-[18px]" /></Switch.Root>IPv6</label><button className="ml-auto h-8 rounded-md bg-accent px-3 text-xs font-semibold text-accent-foreground">{t('create')}</button></div></form>}
    {query.isPending ? <LoadingState label={language === 'zh-CN' ? '正在加载网络' : 'Loading networks'} /> : <div className="divide-y divide-border border-y border-border">{query.data?.map((row) => <div key={row.id} className="group grid min-h-16 grid-cols-[minmax(0,1fr)_100px_180px_100px_36px] items-center gap-4 px-2 hover:bg-surface/60"><div><p className="text-sm font-medium">{row.name}</p><p className="font-mono text-[10px] text-text-subtle">{row.id.slice(0, 12)}</p></div><p className="text-xs text-text-muted">{row.driver} · {row.scope}</p><p className="truncate text-xs text-text-muted">{row.ipam?.[0]?.subnet || '—'} · {row.ipam?.[0]?.gateway || '—'}</p><p className="text-xs text-text-muted">{row.containers} {language === 'zh-CN' ? '个连接' : 'attached'}{row.ipv6 ? ' · IPv6' : ''}</p><button onClick={() => void removeNetwork(row)} className="grid size-7 place-items-center rounded opacity-0 hover:bg-surface-hover group-hover:opacity-100"><Trash2 className="size-3.5" /></button></div>)}</div>}
  </ResourceFrame>
}

function Field({ label, value, set, placeholder }: { label: string; value: string; set: (value: string) => void; placeholder?: string }) { return <label><span className="mb-1 block text-[10px] uppercase tracking-wider text-text-subtle">{label}</span><input required={label === 'Name' || label === '名称'} value={value} onChange={(event) => set(event.target.value)} placeholder={placeholder} className="h-8 w-full rounded-md border border-border bg-background px-2 text-xs outline-none" /></label> }
