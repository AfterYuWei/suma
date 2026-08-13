import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { ArrowRight, GitBranch, GitCommitHorizontal, GitPullRequest, Plus, Settings2 } from 'lucide-react'
import { useState } from 'react'
import { LoadingState } from '../components/ui/loading-state'
import type { DeliveryProject } from '../features/delivery/types'
import { shortCommit } from '../features/delivery/types'
import { api } from '../lib/api'
import { useI18n } from '../lib/i18n'
import type { DockerNode } from '../lib/nodes'
import { useUIStore } from '../stores/ui'

export function ContinuousDeliveryPage() {
  const client = useQueryClient()
  const navigate = useNavigate()
  const { language } = useI18n()
  const zh = language === 'zh-CN'
  const currentNodeID = useUIStore((state) => state.currentNodeID)
  const [createOpen, setCreateOpen] = useState(false)
  const [projectName, setProjectName] = useState('')
  const [targetNodeIDs, setTargetNodeIDs] = useState<string[]>([])
  const query = useQuery({ queryKey: ['delivery-projects'], queryFn: () => api<DeliveryProject[]>('/delivery-projects') })
  const nodes = useQuery({ queryKey: ['nodes'], queryFn: () => api<DockerNode[]>('/nodes') })
  const create = useMutation({
    mutationFn: (input: { name: string; node_ids: string[] }) => api<DeliveryProject>('/delivery-projects', { method: 'POST', body: JSON.stringify(input) }),
    onSuccess: async (project) => {
      setCreateOpen(false)
      await client.invalidateQueries({ queryKey: ['delivery-projects'] })
      void navigate({ to: '/continuous-delivery/$projectName', params: { projectName: project.name } })
    },
  })
  const add = () => {
    setProjectName('')
    const enabled = (nodes.data || []).filter((node) => node.enabled)
    setTargetNodeIDs(currentNodeID && enabled.some((node) => node.id === currentNodeID) ? [currentNodeID] : enabled[0] ? [enabled[0].id] : [])
    setCreateOpen(true)
  }
  const rows = query.data ?? []
  const synchronized = rows.filter((project) => deliveryState(project) === 'synchronized').length
  const pending = rows.filter((project) => deliveryState(project) === 'pending').length
  const setup = rows.length - synchronized - pending

  return <div>
    <div className="mb-5 flex flex-wrap items-end gap-4">
      <div><h1 className="text-xl font-semibold tracking-tight">{zh ? '持续交付' : 'Continuous Delivery'}</h1><div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 font-mono text-[10px] uppercase tracking-wider text-text-subtle"><StatusCount color="bg-success" value={synchronized} label={zh ? '已同步' : 'synced'} /><StatusCount color="bg-warning" value={pending} label={zh ? '待对账' : 'pending'} /><StatusCount color="bg-text-subtle" value={setup} label={zh ? '待配置' : 'setup'} /><span>{rows.length} {zh ? '总计' : 'total'}</span></div></div>
      <button disabled={create.isPending} onClick={add} className="ml-auto flex h-9 items-center gap-2 rounded-xl bg-accent px-3 text-xs font-semibold text-accent-foreground disabled:opacity-60"><Plus className="size-3.5" />{zh ? '新建项目' : 'New project'}</button>
    </div>
    {query.isPending ? <LoadingState compact rows={7} label={zh ? '正在加载持续交付项目' : 'Loading delivery projects'} /> : query.isError ? <div className="rounded-xl border border-danger/30 bg-danger-subtle py-12 text-center text-sm text-danger">{query.error.message}</div> : rows.length ? <div className="overflow-hidden rounded-2xl border border-border">
      <div className="hidden h-9 grid-cols-[minmax(220px,1fr)_110px_130px_86px_86px_118px_28px] items-center gap-3 border-b border-border bg-surface/45 px-3 font-mono text-[9px] uppercase tracking-[.14em] text-text-subtle lg:grid"><span>{zh ? '项目 / 仓库' : 'Project / repository'}</span><span>{zh ? '状态' : 'Status'}</span><span>{zh ? '引用' : 'Reference'}</span><span>{zh ? '期望版本' : 'Desired'}</span><span>{zh ? '已观测' : 'Observed'}</span><span className="text-right">{zh ? '更新时间' : 'Updated'}</span><span /></div>
      <div className="divide-y divide-border">{rows.map((project) => <DeliveryRow key={project.id} project={project} zh={zh} />)}</div>
    </div> : <div className="rounded-2xl border border-border py-16 text-center"><GitPullRequest className="mx-auto size-5 text-text-subtle" /><p className="mt-3 text-sm font-medium">{zh ? '还没有持续交付项目' : 'No delivery projects yet'}</p><p className="mt-1 text-xs text-text-subtle">{zh ? '直接在这里创建项目并连接 Git 仓库。' : 'Create a project here and connect its Git repository.'}</p></div>}
    {create.isError && <p className="mt-3 text-xs text-danger">{create.error.message}</p>}
    {createOpen && <><button aria-label="Close" className="fixed inset-0 z-30 bg-black/35" onClick={() => setCreateOpen(false)} /><form onSubmit={(event) => { event.preventDefault(); if (projectName.trim() && targetNodeIDs.length) create.mutate({ name: projectName.trim(), node_ids: targetNodeIDs }) }} className="fixed left-1/2 top-1/2 z-40 w-[min(92vw,480px)] -translate-x-1/2 -translate-y-1/2 rounded-2xl border border-border bg-elevated p-6 shadow-2xl"><h2 className="text-base font-semibold">{zh ? '新建交付项目' : 'New delivery project'}</h2><label className="mt-5 block text-xs text-text-muted">{zh ? '项目名称' : 'Project name'}<input required value={projectName} onChange={(event) => setProjectName(event.target.value)} className="mt-2 h-10 w-full rounded-xl border border-border bg-background px-3 outline-none focus:border-accent" /></label><fieldset className="mt-5"><legend className="mb-2 text-xs text-text-muted">{zh ? '目标节点（可多选）' : 'Target nodes (multiple allowed)'}</legend><div className="space-y-2">{(nodes.data || []).filter((node) => node.enabled).map((node) => <label key={node.id} className="flex items-center gap-3 rounded-xl border border-border px-3 py-2 text-xs"><input type="checkbox" checked={targetNodeIDs.includes(node.id)} onChange={() => setTargetNodeIDs((current) => current.includes(node.id) ? current.filter((id) => id !== node.id) : [...current, node.id])} /><span className="flex-1">{node.name}</span><span className="font-mono text-[9px] text-text-subtle">{node.connection_type}</span></label>)}</div></fieldset><div className="mt-6 flex justify-end gap-2"><button type="button" onClick={() => setCreateOpen(false)} className="h-9 rounded-xl border border-border px-4 text-xs">{zh ? '取消' : 'Cancel'}</button><button disabled={create.isPending || !projectName.trim() || targetNodeIDs.length === 0} className="h-9 rounded-xl bg-accent px-4 text-xs font-semibold text-accent-foreground disabled:opacity-50">{zh ? '创建' : 'Create'}</button></div></form></>}
  </div>
}

function DeliveryRow({ project, zh }: { project: DeliveryProject; zh: boolean }) {
  const state = deliveryState(project)
  const locale = zh ? 'zh-CN' : 'en-US'
  const updated = new Date(project.updated_at)
  const stateLabel = state === 'synchronized' ? (zh ? '已同步' : 'Synchronized') : state === 'pending' ? (zh ? '待对账' : 'Pending') : (zh ? '待配置' : 'Setup')
  return <Link to="/continuous-delivery/$projectName" params={{ projectName: project.name }} className="group grid min-h-16 grid-cols-[minmax(0,1fr)_28px] items-center gap-3 px-3 transition-colors hover:bg-surface/55 lg:grid-cols-[minmax(220px,1fr)_110px_130px_86px_86px_118px_28px]">
    <div className="flex min-w-0 items-center gap-3 py-2"><span className={`grid size-8 shrink-0 place-items-center rounded-xl border ${project.configured ? 'border-accent/20 bg-accent-subtle text-accent' : 'border-border bg-surface/70 text-text-subtle'}`}>{project.configured ? <GitBranch className="size-3.5" strokeWidth={1.6} /> : <Settings2 className="size-3.5" strokeWidth={1.6} />}</span><span className="min-w-0"><span className="block truncate text-[13px] font-medium">{project.name}</span><span className="mt-1 block truncate font-mono text-[9px] text-text-subtle" title={project.repository_url}>{project.configured ? project.repository_url || (zh ? '已连接 Git 仓库' : 'Git repository connected') : (zh ? '尚未连接 Git 仓库' : 'Git repository not configured')}</span><span className="mt-1 flex items-center gap-3 text-[9px] text-text-subtle lg:hidden"><span className="flex items-center gap-1.5 text-[10px]"><span className={`size-1.5 rounded-full ${deliveryStateColor(state)}`} />{stateLabel}</span><span className="font-mono">{project.git_ref || '—'} · {shortCommit(project.desired_commit)}</span></span></span></div>
    <div className="hidden lg:block"><span className={`inline-flex items-center gap-1.5 rounded-lg border px-2 py-1 text-[10px] font-medium ${deliveryStateTone(state)}`}><span className={`size-1.5 rounded-full ${deliveryStateColor(state)}`} />{stateLabel}</span></div>
    <div className="hidden min-w-0 items-center gap-2 lg:flex"><GitBranch className="size-3.5 shrink-0 text-text-subtle" strokeWidth={1.5} /><span className="truncate text-[10px] text-text-muted" title={project.git_ref}>{project.git_ref || '—'}</span></div>
    <Commit value={project.desired_commit} />
    <Commit value={project.observed_commit} />
    <div className="hidden text-right lg:block"><p className="font-mono text-[10px] text-text-muted">{updated.toLocaleDateString(locale)}</p><p className="mt-0.5 font-mono text-[9px] text-text-subtle">{updated.toLocaleTimeString(locale, { hour: '2-digit', minute: '2-digit' })}</p></div>
    <span className="grid size-7 place-items-center rounded-lg border border-transparent text-text-subtle transition-colors group-hover:border-border group-hover:bg-surface-hover group-hover:text-text"><ArrowRight className="size-3.5" /></span>
  </Link>
}

function Commit({ value }: { value?: string }) { return <span className="hidden min-w-0 items-center gap-1.5 lg:flex"><GitCommitHorizontal className="size-3.5 shrink-0 text-text-subtle" strokeWidth={1.5} /><span className="truncate font-mono text-[10px] text-text-muted">{shortCommit(value)}</span></span> }
function StatusCount({ color, value, label }: { color: string; value: number; label: string }) { return <span className="flex items-center gap-1.5"><span className={`size-1.5 rounded-full ${color}`} /><b className="font-medium text-text-muted">{value}</b>{label}</span> }
function deliveryState(project: DeliveryProject) { return !project.configured ? 'setup' : project.desired_commit && project.desired_commit === project.observed_commit ? 'synchronized' : 'pending' }
function deliveryStateColor(state: string) { return state === 'synchronized' ? 'bg-success' : state === 'pending' ? 'bg-warning' : 'bg-text-subtle' }
function deliveryStateTone(state: string) { return state === 'synchronized' ? 'border-success/20 bg-success/10 text-success' : state === 'pending' ? 'border-warning/20 bg-warning/10 text-warning' : 'border-border bg-surface/55 text-text-muted' }
