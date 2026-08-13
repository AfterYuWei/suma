import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useParams } from '@tanstack/react-router'
import { ChevronLeft, Copy, MoreHorizontal, OctagonX, Pause, Play, RefreshCw, Square, Trash2 } from 'lucide-react'
import { lazy, useState } from 'react'
import { LoadingState } from '../components/ui/loading-state'
import type { ContainerDetail } from '../features/containers/types'
import { api } from '../lib/api'
import { nodePath } from '../lib/nodes'
import { useI18n } from '../lib/i18n'
import { confirmDialog, promptDialog } from '../stores/dialog'
import { useUIStore } from '../stores/ui'

const LogViewer = lazy(() => import('../features/containers/log-viewer').then((module) => ({ default: module.LogViewer })))
const StatsView = lazy(() => import('../features/containers/stats-view').then((module) => ({ default: module.StatsView })))
const TerminalView = lazy(() => import('../features/containers/terminal-view').then((module) => ({ default: module.TerminalView })))
const tabs = ['Overview', 'Logs', 'Terminal', 'Stats', 'Inspect'] as const

export function ContainerDetailPage() {
	const nodeID = useUIStore((state) => state.currentNodeID)
  const { containerId } = useParams({ from: '/containers/$containerId' }); const navigate = useNavigate(); const client = useQueryClient(); const { t, language } = useI18n(); const zh = language === 'zh-CN'
  const hash = location.hash.slice(1); const initial = tabs.find((name) => name.toLowerCase() === hash) ?? 'Overview'
  const [tab, setTab] = useState<(typeof tabs)[number]>(initial); const [menu, setMenu] = useState(false)
  const query = useQuery({ queryKey: ['container', nodeID, containerId], queryFn: () => api<ContainerDetail>(nodePath(nodeID, `/containers/${containerId}`)) })
  const action = useMutation({ mutationFn: (name: string) => api(nodePath(nodeID, `/containers/${containerId}/${name}`), { method: 'POST' }), onSuccess: () => { setMenu(false); client.invalidateQueries({ queryKey: ['container', nodeID, containerId] }); client.invalidateQueries({ queryKey: ['containers', nodeID] }) } })
  const rename = async () => { const name = await promptDialog({ title: t('renameContainer'), confirmLabel: t('save'), input: { label: t('newContainerName'), initialValue: query.data?.name } }); if (!name) return; await api(nodePath(nodeID, `/containers/${containerId}`), { method: 'PATCH', body: JSON.stringify({ name }) }); await client.invalidateQueries({ queryKey: ['container', nodeID, containerId] }); setMenu(false) }
  const remove = async () => { const name = query.data?.name ?? containerId; if (!await confirmDialog({ title: t('removeContainer'), description: t('removeContainerDescription', { name }), confirmLabel: t('remove'), danger: true })) return; await api(nodePath(nodeID, `/containers/${containerId}`), { method: 'DELETE' }); void navigate({ to: '/containers' }) }
  const kill = async () => { const name = query.data?.name ?? containerId; if (await confirmDialog({ title: t('killContainer'), description: t('killContainerDescription', { name }), confirmLabel: zh ? '强制终止' : 'Force kill', danger: true })) action.mutate('kill') }
  if (query.isPending) return <LoadingState label={zh ? '正在加载容器详情' : 'Loading container details'} rows={6} />
  if (!query.data) return <p className="py-12 text-sm text-danger">{zh ? '未找到容器。' : 'Container not found.'}</p>
  const row = query.data
  const label = (name: (typeof tabs)[number]) => zh ? ({ Overview: '概览', Logs: '日志', Terminal: '终端', Stats: '统计', Inspect: '检查' } as const)[name] : name

  return <div>
    <a href="/containers" className="mb-6 inline-flex items-center gap-1 text-xs text-text-muted"><ChevronLeft className="size-3.5" />{t('containers')}</a>
    <header className="flex items-start border-b border-border pb-6"><span className={`mt-2 size-2 rounded-full ${row.state === 'running' ? 'bg-success' : 'bg-text-subtle'}`} /><div className="ml-3"><h1 className="text-xl font-semibold">{row.name}</h1><p className="mt-1 text-xs text-text-muted">{row.image} · {row.status}</p></div><div className="relative ml-auto flex gap-2">{row.state === 'running' ? <button onClick={() => action.mutate('stop')} className="flex h-8 items-center gap-2 rounded-md border border-border bg-surface px-3 text-xs"><Square className="size-3.5" />{zh ? '停止' : 'Stop'}</button> : <button onClick={() => action.mutate('start')} className="flex h-8 items-center gap-2 rounded-md bg-accent px-3 text-xs font-semibold text-accent-foreground"><Play className="size-3.5" />{zh ? '启动' : 'Start'}</button>}<button onClick={() => action.mutate('restart')} className="flex h-8 items-center gap-2 rounded-md border border-border bg-surface px-3 text-xs"><RefreshCw className="size-3.5" />{zh ? '重启' : 'Restart'}</button><button onClick={() => setMenu(!menu)} className="grid size-8 place-items-center rounded-md border border-border bg-surface"><MoreHorizontal className="size-4" /></button>{menu && <div className="absolute right-0 top-10 z-10 w-44 rounded-md border border-border bg-elevated p-1 shadow-xl">{row.state === 'paused' ? <MenuAction label={zh ? '恢复' : 'Unpause'} icon={Play} run={() => action.mutate('unpause')} /> : <MenuAction label={zh ? '暂停' : 'Pause'} icon={Pause} run={() => action.mutate('pause')} />}<MenuAction label={zh ? '强制终止' : 'Kill process'} icon={OctagonX} run={() => void kill()} /><MenuAction label={zh ? '重命名' : 'Rename'} icon={RefreshCw} run={() => void rename()} /><MenuAction label={t('remove')} icon={Trash2} danger run={() => void remove()} /></div>}</div></header>
    <nav className="flex h-12 items-center gap-6 border-b border-border text-xs">{tabs.map((name) => <button onClick={() => { setTab(name); location.hash = name.toLowerCase() }} key={name} className={`h-full ${tab === name ? 'border-b border-accent font-medium' : 'text-text-muted'}`}>{label(name)}</button>)}</nav>
    {tab === 'Overview' && <div className="grid gap-10 py-8 lg:grid-cols-2"><section><h2 className="mb-4 text-xs font-semibold uppercase tracking-wider text-text-subtle">{zh ? '运行信息' : 'Runtime'}</h2><dl className="divide-y divide-border border-y border-border">{[[zh ? '容器 ID' : 'Container ID', row.id], [zh ? '状态' : 'Status', row.status], ['PID', row.pid], [zh ? '工作目录' : 'Working directory', row.working_directory || '—'], [zh ? '重启策略' : 'Restart policy', row.restart_policy || '—']].map(([itemLabel, value]) => <div key={itemLabel} className="grid min-h-11 grid-cols-[140px_1fr] items-center text-xs"><dt className="text-text-muted">{itemLabel}</dt><dd className="flex min-w-0 items-center gap-2 truncate font-mono">{value}<Copy className="size-3 text-text-subtle" /></dd></div>)}</dl></section><section><h2 className="mb-4 text-xs font-semibold uppercase tracking-wider text-text-subtle">{zh ? '环境变量' : 'Environment'}</h2><div className="divide-y divide-border border-y border-border">{row.environment.map((item) => <div key={item.key} className="grid min-h-10 grid-cols-2 items-center gap-4 text-xs"><span className="truncate font-mono text-text-muted">{item.key}</span><span className="truncate font-mono">{item.sensitive ? '••••••••' : item.value}</span></div>)}</div></section></div>}
    {tab === 'Logs' && <div className="py-6"><LogViewer nodeID={nodeID} containerId={containerId} /></div>}{tab === 'Terminal' && <div className="py-6"><TerminalView nodeID={nodeID} containerId={containerId} /></div>}{tab === 'Stats' && <div className="py-6"><StatsView nodeID={nodeID} containerId={containerId} /></div>}{tab === 'Inspect' && <pre className="my-6 max-h-[60vh] overflow-auto rounded-md border border-border bg-[var(--code-background)] p-4 text-[11px] text-text-muted">{JSON.stringify(row, null, 2)}</pre>}
  </div>
}

function MenuAction({ label, icon: Icon, run, danger = false }: { label: string; icon: typeof Play; run: () => void; danger?: boolean }) { return <button onClick={run} className={`flex h-8 w-full items-center gap-2 rounded px-2 text-xs hover:bg-surface-hover ${danger ? 'text-danger' : ''}`}><Icon className="size-3.5" />{label}</button> }
