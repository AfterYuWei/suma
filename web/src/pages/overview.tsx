import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Activity, ArrowUpRight, Boxes, Cpu, Gauge, HardDrive, Layers3, MemoryStick, Network, Workflow } from 'lucide-react'
import { motion } from 'motion/react'
import { LoadingState } from '../components/ui/loading-state'
import type { ContainerSummary } from '../features/containers/types'
import { api } from '../lib/api'
import { displayDockerId } from '../lib/docker-id'
import { useI18n } from '../lib/i18n'

interface Overview {
  host: { hostname: string; os: string; kernel: string; architecture: string; uptime_seconds: number; cpu_percent: number; cpus: number; memory_used: number; memory_total: number; disk_used: number; disk_total: number; network_rx: number; network_tx: number }
  docker: { server_version: string; containers_running: number; containers_stopped: number; images: number }
}
interface Audit { id: number; action: string; resource_type: string; resource_name: string; result: string; created_at: string }

const bytes = (value = 0) => value >= 1024 ** 3 ? `${(value / 1024 ** 3).toFixed(1)} GB` : `${(value / 1024 ** 2).toFixed(0)} MB`
const uptime = (seconds = 0, zh = false) => seconds >= 86400 ? `${Math.floor(seconds / 86400)} ${zh ? '天' : 'days'}` : `${Math.floor(seconds / 3600)} ${zh ? '小时' : 'hours'}`
const percent = (value = 0, total = 100) => total > 0 ? Math.max(0, Math.min(100, value / total * 100)) : 0

export function OverviewPage() {
  const { language } = useI18n()
  const zh = language === 'zh-CN'
  const overview = useQuery({ queryKey: ['overview'], queryFn: () => api<Overview>('/overview'), refetchInterval: 10_000 })
  const containers = useQuery({ queryKey: ['containers'], queryFn: () => api<ContainerSummary[]>('/containers'), refetchInterval: 10_000 })
  const audits = useQuery({ queryKey: ['audit-logs'], queryFn: () => api<Audit[]>('/audit-logs') })
  const networks = useQuery({ queryKey: ['networks'], queryFn: () => api<unknown[]>('/networks') })
  const volumes = useQuery({ queryKey: ['volumes'], queryFn: () => api<unknown[]>('/volumes') })
  const projects = useQuery({ queryKey: ['compose'], queryFn: () => api<unknown[]>('/compose') })
  const data = overview.data
  const cpu = data?.host.cpu_percent ?? 0

  const resources = [
    { label: 'CPU', value: `${data ? cpu.toFixed(1) : '—'}%`, detail: `${data?.host.cpus ?? '—'} ${zh ? '核心' : 'cores'}`, progress: cpu, icon: Cpu },
    { label: zh ? '内存' : 'Memory', value: bytes(data?.host.memory_used), detail: `${zh ? '总计' : 'of'} ${bytes(data?.host.memory_total)}`, progress: percent(data?.host.memory_used, data?.host.memory_total), icon: MemoryStick },
    { label: zh ? '磁盘' : 'Disk', value: bytes(data?.host.disk_used), detail: `${zh ? '总计' : 'of'} ${bytes(data?.host.disk_total)}`, progress: percent(data?.host.disk_used, data?.host.disk_total), icon: HardDrive },
    { label: zh ? '网络流量' : 'Network I/O', value: bytes(data?.host.network_rx), detail: `RX / ${bytes(data?.host.network_tx)} TX`, progress: 0, icon: Activity },
  ]

  return <div className="space-y-8 lg:space-y-10">
    <motion.section className="glass-panel scanline data-grid relative overflow-hidden rounded-[1.75rem]" initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: .55, ease: [0.22, 1, 0.36, 1] }}>
      <div className="pointer-events-none absolute -right-24 -top-28 size-80 rounded-full bg-accent/[.07] blur-3xl" />
      <div className="grid min-h-[410px] lg:grid-cols-[minmax(0,1.15fr)_minmax(380px,.85fr)]">
        <div className="relative flex flex-col justify-between border-b border-border p-6 sm:p-9 lg:border-b-0 lg:border-r lg:p-12">
          <div className="flex flex-1 flex-col">
            <div className="flex flex-wrap items-center gap-3">
              <span className={`signal-dot size-2 rounded-full ${overview.isSuccess ? 'bg-success' : 'bg-text-subtle'}`} />
              <span className="font-mono text-[10px] uppercase tracking-[.2em] text-text-muted">{overview.isSuccess ? (zh ? '引擎在线' : 'Engine online') : (zh ? '正在建立连接' : 'Establishing connection')}</span>
              <span className="h-px w-8 bg-border" />
              <span className="font-mono text-[10px] text-text-subtle">{data?.host.architecture ?? 'ARCH_PENDING'}</span>
            </div>
            <div className="flex flex-1 flex-col justify-center py-10 lg:translate-y-2">
              <p className="mb-3 text-xs font-medium uppercase tracking-[.18em] text-text-subtle">{zh ? '主机控制平面' : 'Host control plane'}</p>
              <h1 className="max-w-3xl break-words text-[clamp(2.5rem,6vw,5.8rem)] font-medium leading-[.88] tracking-[-.075em]">
                {data?.host.hostname ?? (zh ? '等待主机' : 'Awaiting host')}<span className="text-accent">.</span>
              </h1>
            </div>
          </div>
          <div className="flex flex-wrap gap-x-8 gap-y-4 border-t border-border pt-5 font-mono text-[10px] uppercase tracking-[.12em] text-text-subtle">
            <span><b className="mr-2 font-medium text-text-muted">OS</b>{data?.host.os ?? '—'}</span>
            <span><b className="mr-2 font-medium text-text-muted">Kernel</b>{data?.host.kernel ?? '—'}</span>
            <span><b className="mr-2 font-medium text-text-muted">Uptime</b>{uptime(data?.host.uptime_seconds, zh)}</span>
          </div>
        </div>

        <div className="relative grid place-items-center p-8 sm:p-12">
          <div className="relative grid aspect-square w-full max-w-[310px] place-items-center rounded-full border border-border bg-background/40">
            <div className="absolute inset-5 rounded-full border border-dashed border-border" />
            <div className="absolute inset-9 rounded-full" style={{ background: `conic-gradient(var(--accent) ${cpu * 3.6}deg, var(--muted) 0deg)`, maskImage: 'radial-gradient(transparent 62%, black 63%)' }} />
            <div className="absolute inset-[4.65rem] rounded-full border border-border bg-elevated/80 shadow-[0_0_60px_var(--ambient)] backdrop-blur-xl" />
            <div className="relative text-center">
              <Gauge className="mx-auto mb-3 size-5 text-accent" strokeWidth={1.5} />
              <p className="text-4xl font-medium tracking-[-.06em] tabular-nums">{data ? cpu.toFixed(1) : '—'}<span className="ml-1 text-base text-text-subtle">%</span></p>
              <p className="mt-2 font-mono text-[9px] uppercase tracking-[.2em] text-text-subtle">CPU load</p>
            </div>
            <span className="absolute left-1/2 top-3 -translate-x-1/2 rounded-full border border-border bg-background px-2 py-1 font-mono text-[8px] text-text-subtle">LIVE</span>
          </div>
          <div className="absolute bottom-5 left-6 right-6 flex justify-between font-mono text-[9px] uppercase tracking-wider text-text-subtle sm:bottom-8 sm:left-9 sm:right-9">
            <span>Docker {data?.docker.server_version ?? '—'}</span><span>{data?.host.cpus ?? 0} vCPU</span>
          </div>
        </div>
      </div>
    </motion.section>

    <section className="grid gap-px overflow-hidden rounded-[1.4rem] border border-border bg-border sm:grid-cols-2 xl:grid-cols-4">
      {resources.map(({ label, value, detail, progress, icon: Icon }, index) => <motion.article key={label} className="group relative bg-background/90 p-5 transition-colors hover:bg-surface-hover sm:p-6" initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }} transition={{ delay: .12 + index * .06 }}>
        <div className="mb-8 flex items-center justify-between"><span className="font-mono text-[10px] uppercase tracking-[.18em] text-text-subtle">0{index + 1} / {label}</span><Icon className="size-4 text-text-subtle transition-colors group-hover:text-accent" strokeWidth={1.5} /></div>
        <div className="flex items-end justify-between gap-3"><div><strong className="text-2xl font-medium tracking-[-.04em] tabular-nums">{value}</strong><p className="mt-1 text-[11px] text-text-subtle">{detail}</p></div>{index < 3 && <span className="font-mono text-[9px] text-text-subtle">{progress.toFixed(0).padStart(2, '0')}%</span>}</div>
        {index < 3 && <div className="mt-5 h-px overflow-hidden bg-muted"><motion.div className="h-full bg-accent shadow-[0_0_8px_var(--accent)]" initial={{ width: 0 }} animate={{ width: `${progress}%` }} transition={{ duration: .8, delay: .2 + index * .08 }} /></div>}
      </motion.article>)}
    </section>

    <section className="grid gap-6 xl:grid-cols-[minmax(0,1.5fr)_minmax(320px,.5fr)]">
      <div className="min-w-0">
        <div className="mb-4 flex items-end gap-4 px-1">
          <div><p className="font-mono text-[9px] uppercase tracking-[.2em] text-text-subtle">Runtime inventory</p><h2 className="mt-1 text-lg font-medium tracking-[-.025em]">{zh ? '活跃容器' : 'Active containers'}</h2></div>
          <div className="ml-auto flex items-center gap-3 text-[10px] text-text-subtle"><span><b className="mr-1 font-medium text-success">{data?.docker.containers_running ?? 0}</b>{zh ? '运行' : 'running'}</span><span><b className="mr-1 font-medium text-text-muted">{data?.docker.containers_stopped ?? 0}</b>{zh ? '停止' : 'stopped'}</span></div>
          <Link to="/containers" className="grid size-9 place-items-center rounded-xl border border-border bg-surface/60 text-text-muted transition-all hover:border-accent/30 hover:text-accent" aria-label={zh ? '查看所有容器' : 'View all containers'}><ArrowUpRight className="size-4" /></Link>
        </div>
        <div className="glass-panel overflow-hidden rounded-[1.4rem]">
          {containers.isPending ? <LoadingState embedded rows={6} label={zh ? '正在加载容器' : 'Loading containers'} /> : containers.data?.slice(0, 6).map((row, index) => <Link key={row.id} to="/containers/$containerId" params={{ containerId: row.id }} className="group grid min-h-[72px] grid-cols-[minmax(0,1fr)_auto] items-center gap-4 border-b border-border px-5 transition-colors last:border-b-0 hover:bg-surface-hover sm:grid-cols-[minmax(0,1fr)_130px_140px_auto]">
            <div className="flex min-w-0 items-center gap-3.5"><span className="font-mono text-[9px] text-text-subtle">{String(index + 1).padStart(2, '0')}</span><span className={`size-1.5 rounded-full ${row.state === 'running' ? 'signal-dot bg-success' : 'bg-text-subtle'}`} /><div className="min-w-0"><p className="truncate text-[13px] font-medium">{row.name}</p><p className="mt-1 truncate font-mono text-[9px] text-text-subtle">{row.image}</p></div></div>
            <p className="hidden truncate font-mono text-[10px] text-text-muted sm:block">{row.ports?.[0]?.public_port ? `${row.ports[0].public_port}:${row.ports[0].private_port}` : 'NO PUBLIC PORT'}</p>
            <div className="hidden sm:block"><p className="text-[11px] capitalize text-text-muted">{row.state}</p><p className="mt-1 truncate text-[9px] text-text-subtle">{row.status}</p></div>
            <ArrowUpRight className="size-3.5 text-text-subtle opacity-0 transition-all group-hover:text-accent group-hover:opacity-100" />
          </Link>)}
          {!containers.isPending && containers.data?.length === 0 && <div className="grid min-h-40 place-items-center text-center"><div><Boxes className="mx-auto mb-3 size-5 text-text-subtle" /><p className="text-xs text-text-muted">{zh ? '暂无容器' : 'No containers found'}</p></div></div>}
        </div>
      </div>

      <aside className="glass-panel flex min-h-[360px] flex-col rounded-[1.4rem] p-5 sm:p-6">
        <div className="flex items-start"><div><p className="font-mono text-[9px] uppercase tracking-[.2em] text-text-subtle">Event stream</p><h2 className="mt-1 text-lg font-medium tracking-[-.025em]">{zh ? '最近活动' : 'Recent activity'}</h2></div><Link to="/audit-logs" className="ml-auto grid size-9 place-items-center rounded-xl border border-border text-text-subtle hover:text-accent" aria-label={zh ? '打开审计日志' : 'Open audit log'}><ArrowUpRight className="size-4" /></Link></div>
        {audits.isPending ? <div className="mt-7 flex-1"><LoadingState embedded compact rows={4} label={zh ? '正在加载最近活动' : 'Loading recent activity'} /></div> : <div className="mt-7 flex-1 space-y-5 border-l border-border pl-5">
          {audits.data?.slice(0, 5).map((row) => <div key={row.id} className="relative"><span className="absolute -left-[23px] top-1.5 size-1.5 rounded-full bg-text-subtle ring-4 ring-background" /><div className="flex gap-3"><Workflow className="mt-0.5 size-3.5 shrink-0 text-text-subtle" strokeWidth={1.5} /><div className="min-w-0"><p className="truncate text-xs font-medium">{row.resource_name ? row.resource_type === 'container' ? displayDockerId(row.resource_name) : row.resource_name : row.action}</p><p className="mt-1 font-mono text-[9px] uppercase tracking-wider text-text-subtle">{new Date(row.created_at).toLocaleTimeString(language)} / {row.action}</p></div></div></div>)}
        </div>}
        <div className="mt-7 grid grid-cols-3 gap-px overflow-hidden rounded-xl border border-border bg-border text-center">
          {[{ icon: Network, value: networks.data?.length ?? 0, label: zh ? '网络' : 'Networks' }, { icon: HardDrive, value: volumes.data?.length ?? 0, label: zh ? '存储卷' : 'Volumes' }, { icon: Layers3, value: projects.data?.length ?? 0, label: 'Compose' }].map(({ icon: Icon, value, label }) => <div key={label} className="bg-background/80 px-2 py-3"><Icon className="mx-auto mb-2 size-3.5 text-text-subtle" strokeWidth={1.5} /><b className="block text-sm font-medium tabular-nums">{value}</b><span className="text-[9px] text-text-subtle">{label}</span></div>)}
        </div>
      </aside>
    </section>
  </div>
}
