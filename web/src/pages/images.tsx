import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Download, Package, Tag as TagIcon, Trash2 } from 'lucide-react'
import { type ReactNode, useState } from 'react'
import { Button } from '../components/ui/button'
import { Input } from '../components/ui/input'
import { ListShell } from '../components/ui/list-shell'
import { LoadingState } from '../components/ui/loading-state'
import { ErrorState } from '../components/ui/error-state'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../components/ui/select'
import { Sheet, SheetContent, SheetHeader, SheetTitle } from '../components/ui/sheet'
import { Spinner } from '../components/ui/spinner'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'
import { api } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { nodePath } from '../lib/nodes'
import { confirmDialog, promptDialog } from '../stores/dialog'
import { useUIStore } from '../stores/ui'

interface Image { id: string; tags: string[]; digests: string[]; size: number; created: string; containers: number; architecture?: string; os?: string; author?: string; docker_version?: string; layers?: string[] }
interface RegistryCredential { id: number; name: string; server_address: string; authorized_node_ids: string[] }

export function ImagesPage() {
  const nodeID = useUIStore((state) => state.currentNodeID)
  const client = useQueryClient()
  const [reference, setReference] = useState('')
  const [selected, setSelected] = useState('')
  const [credentialID, setCredentialID] = useState('')
  const { t, language } = useI18n()
  const zh = language === 'zh-CN'
  const query = useQuery({ queryKey: ['images', nodeID], queryFn: () => api<Image[]>(nodePath(nodeID, '/images')) })
  const detail = useQuery({ queryKey: ['image', nodeID, selected], queryFn: () => api<Image>(nodePath(nodeID, `/images/${encodeURIComponent(selected)}`)), enabled: !!selected })
  const credentials = useQuery({ queryKey: ['registry-credentials'], queryFn: () => api<RegistryCredential[]>('/credentials/registries') })
  const pull = useMutation({ mutationFn: () => api(nodePath(nodeID, '/images/pull'), { method: 'POST', body: JSON.stringify({ reference, credential_id: credentialID ? Number(credentialID) : undefined }) }), onSuccess: () => { setReference(''); client.invalidateQueries({ queryKey: ['tasks', nodeID] }) } })
  const remove = useMutation({ mutationFn: (id: string) => api(nodePath(nodeID, `/images/${encodeURIComponent(id)}?force=false`), { method: 'DELETE' }), onSuccess: () => { setSelected(''); client.invalidateQueries({ queryKey: ['images', nodeID] }) } })
  const tag = async (row: Image) => { const value = await promptDialog({ title: t('newImageTag'), description: t('tagDescription'), confirmLabel: t('tag'), input: { label: t('newImageTag'), initialValue: row.tags?.[0] || 'repository:tag' } }); if (!value) return; await api(nodePath(nodeID, `/images/${encodeURIComponent(row.id)}/tag`), { method: 'POST', body: JSON.stringify({ reference: value }) }); await client.invalidateQueries({ queryKey: ['images', nodeID] }) }
  const removeImage = async (row: Image) => { const name = row.tags?.[0] || row.id; if (await confirmDialog({ title: t('removeImage'), description: t('removeImageDescription', { name }), confirmLabel: t('remove'), danger: true })) remove.mutate(row.id) }
  const size = (bytes: number) => `${(bytes / 1024 ** 2).toFixed(1)} MB`
  const credentialOptions = [{ value: '', label: zh ? '匿名拉取' : 'Anonymous' }, ...(credentials.data || []).filter((row) => row.authorized_node_ids?.includes(nodeID)).map((row) => ({ value: String(row.id), label: `${row.name} · ${row.server_address}` }))]

  const pullForm = <form onSubmit={(event) => { event.preventDefault(); if (reference) pull.mutate() }} className="flex flex-wrap items-center gap-2">
    <Input value={reference} onChange={(event) => setReference(event.target.value)} className="w-[210px]" placeholder="nginx:latest" aria-label={t('newImageTag')} />
    <Select items={credentialOptions} value={credentialID} onValueChange={(value) => setCredentialID(String(value ?? ''))}>
      <SelectTrigger aria-label={zh ? '镜像仓库凭据' : 'Registry credential'}><SelectValue /></SelectTrigger>
      <SelectContent>
        {credentialOptions.map((option) => <SelectItem key={option.value} value={option.value}>{option.label}</SelectItem>)}
      </SelectContent>
    </Select>
    <Button type="submit" disabled={!reference || pull.isPending}>{pull.isPending ? <Spinner /> : <Download />}Pull</Button>
  </form>

  return <ResourceFrame title={t('images')} detail={zh ? `${query.data?.length ?? 0} 个本地镜像` : `${query.data?.length ?? 0} local images`} action={pullForm}>
    {query.isPending ? <LoadingState compact rows={7} label={zh ? '正在加载镜像' : 'Loading images'} /> : query.isError ? <ErrorState description={query.error.message} /> : (query.data ?? []).length === 0 ? <EmptyState icon={<Package size={20} />} title={zh ? '暂无本地镜像' : 'No local images'} detail={zh ? '输入镜像引用并拉取后会显示在这里。' : 'Pull an image reference to see it here.'} /> :
      <ListShell>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{zh ? '镜像' : 'Image'}</TableHead>
              <TableHead className="min-w-[110px]">{zh ? '大小' : 'Size'}</TableHead>
              <TableHead className="min-w-[90px]">{zh ? '引用' : 'Usage'}</TableHead>
              <TableHead className="min-w-[180px]">{zh ? '创建时间' : 'Created'}</TableHead>
              <TableHead className="min-w-[96px]">{zh ? '操作' : 'Actions'}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {(query.data ?? []).map((row) => (
              <TableRow key={row.id}>
                <TableCell>
                  <button type="button" onClick={() => setSelected(row.id)} className="-ml-2 flex items-center gap-2.5 rounded-md px-2 py-1 text-left outline-none transition-colors focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50">
                    <Package className="size-5 shrink-0 text-muted-foreground" />
                    <span className="flex flex-col">
                      <span className="max-w-[300px] truncate font-medium" title={row.tags?.[0] || '<none>:<none>'}>{row.tags?.[0] || '<none>:<none>'}</span>
                      <span className="font-mono text-xs text-muted-foreground">{row.id.replace('sha256:', '').slice(0, 12)}</span>
                    </span>
                  </button>
                </TableCell>
                <TableCell>{size(row.size)}</TableCell>
                <TableCell>{row.containers < 0 ? '—' : String(row.containers)}</TableCell>
                <TableCell>{new Date(row.created).toLocaleString(language)}</TableCell>
                <TableCell>
                  <div className="flex items-center gap-1">
                    <Button variant="ghost" size="icon-sm" title={t('tag')} aria-label={t('tag')} onClick={() => void tag(row)}><TagIcon /></Button>
                    <Button variant="destructive" size="icon-sm" title={t('removeImage')} aria-label={t('removeImage')} onClick={() => void removeImage(row)}><Trash2 /></Button>
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </ListShell>}

    <Sheet open={!!selected} onOpenChange={(open) => { if (!open) setSelected('') }}>
      <SheetContent side="right" className="w-[448px] max-w-full gap-0 sm:max-w-[448px]">
        <SheetHeader className="border-b"><SheetTitle className="truncate pr-6">{detail.data?.tags?.[0] || selected.slice(0, 19)}</SheetTitle></SheetHeader>
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
