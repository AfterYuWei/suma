import { useQuery } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { Container, Layers3, Package, Search, Search as Command } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { useMemo, useState } from 'react'
import type { ContainerSummary } from '../../features/containers/types'
import { api } from '../../lib/api'
import { useI18n } from '../../lib/i18n'
import { confirmDialog } from '../../stores/dialog'

interface Project { name: string }
interface Image { id: string; tags: string[] }
interface Result { id: string; label: string; detail: string; type: string; icon: typeof Container; run: () => void }

export function CommandPalette({ open, close }: { open: boolean; close: () => void }) {
  const navigate = useNavigate(); const [search, setSearch] = useState(''); const { language } = useI18n(); const zh = language === 'zh-CN'
  const containers = useQuery({ queryKey: ['containers'], queryFn: () => api<ContainerSummary[]>('/containers'), enabled: open })
  const projects = useQuery({ queryKey: ['compose'], queryFn: () => api<Project[]>('/compose'), enabled: open })
  const images = useQuery({ queryKey: ['images'], queryFn: () => api<Image[]>('/images'), enabled: open })
  const results = useMemo<Result[]>(() => {
    const values: Result[] = [
      { id: 'containers', label: zh ? '打开容器' : 'Open containers', detail: zh ? '导航' : 'Navigation', type: zh ? '操作' : 'Actions', icon: Container, run: () => { close(); void navigate({ to: '/containers' }) } },
      { id: 'compose', label: zh ? '创建 Compose 项目' : 'Create Compose project', detail: zh ? '新部署' : 'New deployment', type: zh ? '操作' : 'Actions', icon: Layers3, run: () => { close(); void navigate({ to: '/compose' }) } },
      { id: 'pull', label: zh ? '拉取镜像' : 'Pull an image', detail: zh ? 'Docker 镜像' : 'Docker image', type: zh ? '操作' : 'Actions', icon: Package, run: () => { close(); void navigate({ to: '/images' }) } },
    ]
    containers.data?.forEach((row) => {
      values.push({ id: `container-${row.id}`, label: row.name, detail: `${row.state} · ${row.image}`, type: zh ? '容器' : 'Containers', icon: Container, run: () => { close(); void navigate({ to: '/containers/$containerId', params: { containerId: row.id } }) } })
      values.push({ id: `logs-${row.id}`, label: zh ? `打开 ${row.name} 日志` : `Open ${row.name} logs`, detail: row.image, type: zh ? '容器操作' : 'Container actions', icon: Container, run: () => { close(); location.assign(`/containers/${row.id}#logs`) } })
      values.push({ id: `terminal-${row.id}`, label: zh ? `打开 ${row.name} 终端` : `Open ${row.name} terminal`, detail: row.image, type: zh ? '容器操作' : 'Container actions', icon: Container, run: () => { close(); location.assign(`/containers/${row.id}#terminal`) } })
      values.push({ id: `restart-${row.id}`, label: zh ? `重启 ${row.name}` : `Restart ${row.name}`, detail: zh ? '容器操作' : 'Container action', type: zh ? '容器操作' : 'Container actions', icon: Container, run: () => { close(); void api(`/containers/${row.id}/restart`, { method: 'POST' }) } })
      if (row.state === 'running') values.push({ id: `stop-${row.id}`, label: `${zh ? '停止' : 'Stop'} ${row.name}`, detail: zh ? '容器操作' : 'Container action', type: zh ? '容器操作' : 'Container actions', icon: Container, run: () => { void confirmDialog({ title: zh ? `停止 ${row.name}？` : `Stop ${row.name}?`, description: zh ? '容器将收到正常停止信号。' : 'The container will receive a graceful stop signal.', confirmLabel: zh ? '停止' : 'Stop' }).then((confirmed) => { if (confirmed) { close(); void api(`/containers/${row.id}/stop`, { method: 'POST' }) } }) } })
    })
    projects.data?.forEach((row) => values.push({ id: `project-${row.name}`, label: row.name, detail: zh ? 'Compose 项目' : 'Compose project', type: 'Compose', icon: Layers3, run: () => { close(); void navigate({ to: '/compose/$projectName', params: { projectName: row.name } }) } }))
    images.data?.forEach((row) => values.push({ id: `image-${row.id}`, label: row.tags?.[0] || row.id.slice(0, 19), detail: zh ? '本地镜像' : 'Local image', type: zh ? '镜像' : 'Images', icon: Package, run: () => { close(); void navigate({ to: '/images' }) } }))
    const term = search.trim().toLowerCase(); return term ? values.filter((row) => `${row.label} ${row.detail} ${row.type}`.toLowerCase().includes(term)) : values
  }, [containers.data, projects.data, images.data, search, close, navigate, zh])
  return <AnimatePresence>{open && <motion.div className="fixed inset-0 z-50 flex items-start justify-center bg-black/60 px-4 pt-[12vh] backdrop-blur-xl" initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }} onMouseDown={close}><motion.div role="dialog" aria-modal="true" className="glass-panel w-full max-w-2xl overflow-hidden rounded-[1.4rem] shadow-[0_32px_120px_rgba(0,0,0,.5)]" initial={{ y: -16, scale: .975, opacity: 0 }} animate={{ y: 0, scale: 1, opacity: 1 }} exit={{ y: -10, scale: .985, opacity: 0 }} transition={{ duration: .22 }} onMouseDown={(event) => event.stopPropagation()}><div className="flex h-16 items-center gap-3 border-b border-border px-5"><span className="grid size-8 place-items-center rounded-xl border border-accent/20 bg-accent/10 text-accent"><Command className="size-4" /></span><input autoFocus value={search} onChange={(event) => setSearch(event.target.value)} className="min-w-0 flex-1 bg-transparent text-sm outline-none placeholder:text-text-subtle" placeholder={zh ? '搜索容器、项目、镜像或操作' : 'Search containers, projects, images, or actions'} /><kbd className="rounded-md border border-border bg-background/70 px-2 py-1 font-mono text-[9px] tracking-wider text-text-subtle">ESC</kbd></div><div className="max-h-[420px] overflow-auto p-2.5">{results.map((row, index) => { const Icon = row.icon; const showHeading = index === 0 || results[index - 1].type !== row.type; return <div key={row.id}>{showHeading && <p className="px-3 pb-1.5 pt-4 font-mono text-[9px] font-medium uppercase tracking-[.2em] text-text-subtle first:pt-2">{row.type}</p>}<div className="overflow-hidden rounded-xl"><button onClick={row.run} className="group flex h-12 w-full items-center gap-3 rounded-xl px-3 text-left transition-[background-color,box-shadow,transform] hover:bg-surface-hover focus:bg-surface-hover focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-accent/50 active:scale-[.992] active:bg-surface-hover"><Icon className="size-4 text-text-subtle transition-colors group-hover:text-accent group-focus:text-accent" strokeWidth={1.6} /><span className="min-w-0"><span className="block truncate text-[13px]">{row.label}</span><span className="mt-0.5 block truncate font-mono text-[9px] uppercase tracking-wider text-text-subtle">{row.detail}</span></span><span className="ml-auto size-1.5 rounded-full bg-border transition-colors group-hover:bg-accent group-focus:bg-accent" /></button></div></div>})}{results.length === 0 && <div className="py-14 text-center text-xs text-text-subtle"><Search className="mx-auto mb-3 size-5" />{zh ? '没有匹配的命令或资源' : 'No matching command or resource'}</div>}</div><footer className="flex h-10 items-center gap-4 border-t border-border px-5 font-mono text-[8px] uppercase tracking-[.15em] text-text-subtle"><span>{results.length} results</span><span className="ml-auto">DockPort command center</span></footer></motion.div></motion.div>}</AnimatePresence>
}
