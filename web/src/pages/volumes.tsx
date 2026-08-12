import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Plus, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { LoadingState } from '../components/ui/loading-state'
import { api } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { promptDialog } from '../stores/dialog'
import { ResourceFrame } from './images'

interface Volume { name: string; driver: string; mountpoint: string; created_at: string; used_by: string[]; size: number }
export function VolumesPage() {
  const client = useQueryClient(); const [name, setName] = useState(''); const { t, language } = useI18n(); const query = useQuery({ queryKey: ['volumes'], queryFn: () => api<Volume[]>('/volumes') })
  const create = useMutation({ mutationFn: () => api('/volumes', { method: 'POST', body: JSON.stringify({ name, driver: 'local' }) }), onSuccess: () => { setName(''); client.invalidateQueries({ queryKey: ['volumes'] }) } })
  const remove = useMutation({ mutationFn: (name: string) => api(`/volumes/${encodeURIComponent(name)}?confirm=${encodeURIComponent(name)}`, { method: 'DELETE' }), onSuccess: () => client.invalidateQueries({ queryKey: ['volumes'] }) })
  const removeVolume = async (row: Volume) => { const value = await promptDialog({ title: t('deleteVolume'), description: t('deleteVolumeDescription', { name: row.name }), confirmLabel: t('remove'), danger: true, input: { label: t('typeToConfirm', { value: row.name }), requiredValue: row.name } }); if (value === row.name) remove.mutate(row.name) }
  return <ResourceFrame eyebrow="Docker" title={t('volumes')} detail={language === 'zh-CN' ? `${query.data?.length ?? 0} 个持久存储卷` : `${query.data?.length ?? 0} persistent volumes`} action={<form onSubmit={(event) => { event.preventDefault(); if (name) create.mutate() }} className="flex gap-2"><input value={name} onChange={(event) => setName(event.target.value)} className="h-8 w-48 rounded-md border border-border bg-surface px-2.5 text-xs outline-none" placeholder={language === 'zh-CN' ? '存储卷名称' : 'Volume name'} /><button className="flex h-8 items-center gap-2 rounded-md border border-border bg-surface px-3 text-xs"><Plus className="size-3.5" />{t('create')}</button></form>}>{query.isPending ? <LoadingState label={language === 'zh-CN' ? '正在加载存储卷' : 'Loading volumes'} /> : <div className="divide-y divide-border border-y border-border">{query.data?.map((row) => <div key={row.name} className="group grid min-h-16 grid-cols-[minmax(0,1fr)_90px_160px_36px] items-center gap-4 px-2 hover:bg-surface/60"><div className="min-w-0"><p className="truncate text-sm font-medium">{row.name}</p><p className="truncate font-mono text-[10px] text-text-subtle">{row.mountpoint}</p></div><p className="text-xs text-text-muted">{row.driver}</p><p className="truncate text-xs text-text-muted">{row.used_by.length ? `${language === 'zh-CN' ? '使用者' : 'Used by'} ${row.used_by.join(', ')}` : language === 'zh-CN' ? '未使用' : 'Unused'}</p><button disabled={row.used_by.length > 0} onClick={() => void removeVolume(row)} className="grid size-7 place-items-center rounded opacity-0 hover:bg-surface-hover disabled:cursor-not-allowed disabled:opacity-20 group-hover:opacity-100"><Trash2 className="size-3.5" /></button></div>)}</div>}</ResourceFrame>
}
