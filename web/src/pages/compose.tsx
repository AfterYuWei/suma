import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Boxes, Plus, Rocket } from 'lucide-react'
import { LoadingState } from '../components/ui/loading-state'
import { api } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { promptDialog } from '../stores/dialog'
import { ResourceFrame } from './images'

interface Project { id: number; name: string; status: string; services: number; containers: number; created_at: string; updated_at: string }
const starter = `services:\n  app:\n    image: nginx:alpine\n    ports:\n      - "8080:80"\n`

export function ComposePage() {
  const client = useQueryClient(); const { t, language } = useI18n()
  const query = useQuery({ queryKey: ['compose'], queryFn: () => api<Project[]>('/compose') })
  const create = useMutation({ mutationFn: (name: string) => api<Project>('/compose', { method: 'POST', body: JSON.stringify({ name, compose: starter, environment: '' }) }), onSuccess: () => client.invalidateQueries({ queryKey: ['compose'] }) })
  const add = async () => { const name = await promptDialog({ title: t('newProject'), description: t('projectNameDescription'), confirmLabel: t('create'), input: { label: t('projectName') } }); if (name) create.mutate(name) }
  return <ResourceFrame eyebrow="Docker" title="Compose" detail={language === 'zh-CN' ? `${query.data?.length ?? 0} 个托管项目` : `${query.data?.length ?? 0} managed projects`} action={<button onClick={() => void add()} className="flex h-8 items-center gap-2 rounded-md bg-accent px-3 text-xs font-semibold text-accent-foreground"><Plus className="size-3.5" />{t('newProject')}</button>}>{query.isPending ? <LoadingState label={language === 'zh-CN' ? '正在加载 Compose 项目' : 'Loading Compose projects'} /> : <div className="divide-y divide-border border-y border-border">{query.data?.map((row) => <Link to="/compose/$projectName" params={{ projectName: row.name }} key={row.id} className="group grid min-h-20 grid-cols-[minmax(0,1fr)_150px_150px_24px] items-center gap-4 px-2 hover:bg-surface/60"><div className="flex min-w-0 items-center gap-3"><span className="grid size-8 place-items-center rounded-md border border-border bg-surface"><Boxes className="size-4 text-text-muted" /></span><div><p className="text-sm font-medium">{row.name}</p><p className="text-xs text-text-subtle">{row.services} {language === 'zh-CN' ? '个服务' : 'services'} · {row.containers} {language === 'zh-CN' ? '个容器' : 'containers'}</p></div></div><p className="text-xs capitalize text-text-muted"><span className={`mr-2 inline-block size-1.5 rounded-full ${row.status === 'running' ? 'bg-success' : 'bg-text-subtle'}`} />{row.status}</p><p className="text-xs text-text-subtle">{language === 'zh-CN' ? '更新于' : 'Updated'} {new Date(row.updated_at).toLocaleDateString(language)}</p><Rocket className="size-4 text-text-subtle opacity-0 group-hover:opacity-100" /></Link>)}</div>}</ResourceFrame>
}
