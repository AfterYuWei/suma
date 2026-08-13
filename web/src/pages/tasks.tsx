import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Check, ChevronDown, CircleAlert, LoaderCircle, Timer } from 'lucide-react'
import { useState } from 'react'
import { LoadingState } from '../components/ui/loading-state'
import { api } from '../lib/api'
import { nodePath } from '../lib/nodes'
import { useI18n } from '../lib/i18n'
import { promptDialog } from '../stores/dialog'
import { useUIStore } from '../stores/ui'
import { ResourceFrame } from './images'

interface Task { id: string; type: string; name: string; status: string; progress: number; message: string; created_at: string }
interface Log { id: number; level: string; message: string; created_at: string }

export function TasksPage() {
	const nodeID = useUIStore((state) => state.currentNodeID)
  const client = useQueryClient(); const { t, language } = useI18n()
  const query = useQuery({ queryKey: ['tasks', nodeID], queryFn: () => api<Task[]>(`/tasks?node_id=${encodeURIComponent(nodeID)}`), refetchInterval: 2_000 })
  const prune = useMutation({ mutationFn: () => api(nodePath(nodeID, '/system/prune'), { method: 'POST', body: JSON.stringify({ confirm: 'PRUNE' }) }), onSuccess: () => client.invalidateQueries({ queryKey: ['tasks', nodeID] }) })
  const startPrune = async () => { const value = await promptDialog({ title: t('systemPrune'), description: t('systemPruneDescription'), confirmLabel: t('systemPrune'), danger: true, input: { label: t('typeToConfirm', { value: 'PRUNE' }), requiredValue: 'PRUNE' } }); if (value === 'PRUNE') prune.mutate() }
  return <ResourceFrame eyebrow={t('operations')} title={t('tasks')} detail={language === 'zh-CN' ? '长时间运行的 Docker 与 Compose 操作' : 'Long-running Docker and Compose operations'} action={<button onClick={() => void startPrune()} className="h-8 rounded-md border border-danger/30 bg-surface px-3 text-xs text-danger hover:bg-danger-subtle">{t('systemPrune')}</button>}>{query.isPending ? <LoadingState label={language === 'zh-CN' ? '正在加载任务' : 'Loading tasks'} /> : <div className="divide-y divide-border border-y border-border">{query.data?.map((row) => <TaskRow key={row.id} row={row} />)}</div>}</ResourceFrame>
}

function TaskRow({ row }: { row: Task }) {
  const [open, setOpen] = useState(false); const { language } = useI18n()
  const logs = useQuery({ queryKey: ['task-logs', row.id], queryFn: () => api<Log[]>(`/tasks/${row.id}/logs`), enabled: open, refetchInterval: row.status === 'running' ? 1_000 : false })
  const Icon = row.status === 'success' ? Check : row.status === 'failed' ? CircleAlert : row.status === 'running' ? LoaderCircle : Timer
  return <div><button onClick={() => setOpen(!open)} className="grid min-h-16 w-full grid-cols-[24px_minmax(0,1fr)_120px_100px_24px] items-center gap-3 px-2 text-left hover:bg-surface/60"><Icon className={`size-4 ${row.status === 'running' ? 'animate-spin text-accent' : row.status === 'success' ? 'text-success' : 'text-text-subtle'}`} /><div className="min-w-0"><p className="truncate text-sm font-medium">{row.name}</p><p className="truncate text-[10px] text-text-subtle">{row.message || row.type}</p></div><div className="h-1 overflow-hidden rounded-full bg-muted"><div className="h-full bg-accent" style={{ width: `${row.progress}%` }} /></div><p className="text-right text-xs capitalize text-text-muted">{row.status}</p><ChevronDown className={`size-3.5 text-text-subtle transition-transform ${open ? 'rotate-180' : ''}`} /></button>{open && <div className="max-h-64 overflow-auto border-t border-border bg-[var(--code-background)] px-4 py-3 font-mono text-[11px] leading-5 text-text-muted">{logs.isPending && <span className="flex items-center gap-2 text-text-subtle"><LoaderCircle className="size-3.5 animate-spin text-accent" />{language === 'zh-CN' ? '正在加载任务输出…' : 'Loading task output…'}</span>}{logs.data?.map((log) => <div key={log.id}><span className="mr-3 text-text-subtle">{new Date(log.created_at).toLocaleTimeString(language)}</span><span className={log.level === 'error' ? 'text-danger' : ''}>{log.message}</span></div>)}{logs.data?.length === 0 && <span className="text-text-subtle">{language === 'zh-CN' ? '等待任务输出…' : 'Waiting for task output…'}</span>}</div>}</div>
}
