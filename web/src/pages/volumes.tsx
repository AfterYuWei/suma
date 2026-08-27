import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Database, Plus, Trash2 } from 'lucide-react'
import { useState, type FormEvent } from 'react'
import { Badge } from '../components/ui/badge'
import { Button } from '../components/ui/button'
import { ErrorState } from '../components/ui/error-state'
import { Input } from '../components/ui/input'
import { ListShell } from '../components/ui/list-shell'
import { LoadingState } from '../components/ui/loading-state'
import { Spinner } from '../components/ui/spinner'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'
import { api } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { nodePath } from '../lib/nodes'
import { promptDialog } from '../stores/dialog'
import { useUIStore } from '../stores/ui'
import { ResourceFrame } from './images'

interface Volume { name: string; driver: string; mountpoint: string; created_at: string; used_by: string[]; size: number }
interface VolumeValues { name: string }

export function VolumesPage() {
  const nodeID = useUIStore((state) => state.currentNodeID)
  const client = useQueryClient()
  const { t, language } = useI18n()
  const zh = language === 'zh-CN'
  const [name, setName] = useState('')
  const query = useQuery({ queryKey: ['volumes', nodeID], queryFn: () => api<Volume[]>(nodePath(nodeID, '/volumes')) })
  const create = useMutation({ mutationFn: ({ name }: VolumeValues) => api(nodePath(nodeID, '/volumes'), { method: 'POST', body: JSON.stringify({ name, driver: 'local' }) }), onSuccess: () => client.invalidateQueries({ queryKey: ['volumes', nodeID] }) })
  const remove = useMutation({ mutationFn: (name: string) => api(nodePath(nodeID, `/volumes/${encodeURIComponent(name)}?confirm=${encodeURIComponent(name)}`), { method: 'DELETE' }), onSuccess: () => client.invalidateQueries({ queryKey: ['volumes', nodeID] }) })
  const removeVolume = async (row: Volume) => { const value = await promptDialog({ title: t('deleteVolume'), description: t('deleteVolumeDescription', { name: row.name }), confirmLabel: t('remove'), danger: true, input: { label: t('typeToConfirm', { value: row.name }), requiredValue: row.name } }); if (value === row.name) remove.mutate(row.name) }
  const formatSize = (bytes: number) => bytes > 0 ? `${(bytes / 1024 ** 2).toFixed(1)} MB` : '—'
  const submitCreate = (event: FormEvent<HTMLFormElement>) => { event.preventDefault(); create.mutate({ name }); setName('') }

  const action = <form onSubmit={submitCreate} className="flex items-center gap-2">
    <Input value={name} onChange={(event) => setName(event.target.value)} required aria-label={zh ? '存储卷名称' : 'Volume name'} placeholder={zh ? '存储卷名称' : 'Volume name'} className="w-44" />
    <Button type="submit" disabled={create.isPending}>{create.isPending ? <Spinner className="size-4" /> : <Plus size={16} />}{t('create')}</Button>
  </form>

  return <ResourceFrame title={t('volumes')} detail={zh ? `${query.data?.length ?? 0} 个持久存储卷` : `${query.data?.length ?? 0} persistent volumes`} action={action}>
    <div className="flex w-full flex-col items-start gap-4">
      {query.isPending ? <LoadingState compact rows={7} label={zh ? '正在加载存储卷' : 'Loading volumes'} /> : query.isError ? <ErrorState description={query.error.message} /> : (query.data ?? []).length === 0 ? <div className="flex w-full flex-col items-center gap-1 rounded-xl bg-card px-4 py-10 text-center ring-1 ring-foreground/10">
        <Database className="size-5 text-muted-foreground" />
        <p className="text-sm font-medium">{zh ? '暂无存储卷' : 'No volumes'}</p>
        <p className="text-sm text-muted-foreground">{zh ? '创建持久存储卷后会显示在这里。' : 'Create a persistent volume to see it here.'}</p>
      </div> : <ListShell>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{zh ? '存储卷' : 'Volume'}</TableHead>
              <TableHead>{zh ? '驱动' : 'Driver'}</TableHead>
              <TableHead>{zh ? '大小' : 'Size'}</TableHead>
              <TableHead>{zh ? '使用者' : 'Used by'}</TableHead>
              <TableHead>{zh ? '创建时间' : 'Created'}</TableHead>
              <TableHead className="w-16"><span className="sr-only">{t('deleteVolume')}</span></TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {(query.data ?? []).map((row) => (
              <TableRow key={row.name}>
                <TableCell><div><span className="font-medium">{row.name}</span><span title={row.mountpoint} className="block max-w-72 truncate text-xs text-muted-foreground">{row.mountpoint}</span></div></TableCell>
                <TableCell><Badge variant="outline">{row.driver}</Badge></TableCell>
                <TableCell>{formatSize(row.size)}</TableCell>
                <TableCell>{row.used_by.length ? <div className="flex max-w-64 flex-wrap gap-1">{row.used_by.map((used) => <Badge key={used} variant="secondary">{used}</Badge>)}</div> : <span className="text-sm text-muted-foreground">{zh ? '未使用' : 'Unused'}</span>}</TableCell>
                <TableCell className="text-muted-foreground">{row.created_at ? new Date(row.created_at).toLocaleString(language) : '—'}</TableCell>
                <TableCell><Button variant="ghost" size="icon-sm" className="text-red-600 hover:text-red-600 dark:text-red-400 dark:hover:text-red-400" disabled={row.used_by.length > 0} onClick={() => void removeVolume(row)} title={row.used_by.length ? (zh ? '存储卷正在使用中' : 'Volume is in use') : t('deleteVolume')} aria-label={t('deleteVolume')}><Trash2 /></Button></TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </ListShell>}
      {create.isError && <ErrorState description={create.error.message} />}
    </div>
  </ResourceFrame>
}
