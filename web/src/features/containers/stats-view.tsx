import { useEffect, useState } from 'react'
import { Button } from '../../components/ui/button'
import { demoMode, subscribeDemoStream } from '../../lib/api'
import { useI18n } from '../../lib/i18n'
import { useObservationStream } from './use-observation-stream'
import { MetricCharts } from './metric-charts'
import { sizeLabel } from './observation'
import type { ContainerMetrics, MetricPoint } from './types'

export function StatsView({ nodeID, containerId }: { nodeID: string; containerId: string }) {
  const { language } = useI18n(), zh = language === 'zh-CN'
  const [points, setPoints] = useState<MetricPoint[]>([]), [last, setLast] = useState<ContainerMetrics>(), [minutes, setMinutes] = useState(1), [received, setReceived] = useState(0), [now, setNow] = useState(Date.now)
  useEffect(() => { setPoints([]); setLast(undefined); setReceived(0) }, [nodeID, containerId])
  useEffect(() => { const timer = setInterval(() => setNow(Date.now()), 1000); return () => clearInterval(timer) }, [])
  const consume = (payload: string) => {
    const sample = JSON.parse(payload) as ContainerMetrics & { type?: string }
    if (sample.type === 'error') throw new Error('Stats unavailable')
    if (!sample.available || !sample.sampled_at) return
    const time = Date.parse(sample.sampled_at)
    if (!Number.isFinite(time)) return
    setReceived(Date.now()); setLast(sample)
    setPoints(old => {
      const prev = old.at(-1)
      if (prev && prev.startedAt === sample.started_at && prev.time >= time) return old
      const next = !sample.started_at || prev?.startedAt !== sample.started_at ? [] : old.filter(p => p.time >= time - 300_000)
      if (prev && next.length && time - prev.time > 3000) next.push({ time: prev.time + 1, cpu: null, memory: null, startedAt: sample.started_at })
      return [...next, { time, cpu: sample.cpu_available === false ? null : sample.cpu_percent, memory: sample.memory_available === false ? null : sample.memory_bytes, startedAt: sample.started_at }].slice(-301)
    })
  }
  const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:'
  const status = useObservationStream(demoMode ? null : `${protocol}//${location.host}/ws/nodes/${encodeURIComponent(nodeID)}/containers/${encodeURIComponent(containerId)}/stats?format=normalized`, consume)
  useEffect(() => {
    if (!demoMode) return
    let disposed = false, cleanup = () => undefined as void
    void subscribeDemoStream('normalized-stats', consume).then(fn => { if (disposed) fn(); else cleanup = fn })
    return () => { disposed = true; cleanup() }
  }, [nodeID, containerId])
  const available = (demoMode || status === 'live') && received > 0 && now - received <= 15_000
  return <section className="space-y-4"><div className="flex items-center justify-between"><span className="text-xs text-muted-foreground">{!received ? zh ? '采样中' : 'Sampling' : !available ? zh ? '数据不可用' : 'Data unavailable' : zh ? '实时统计' : 'Live statistics'}</span><div className="flex gap-1">{[1, 5].map(m => <Button key={m} size="sm" variant={m === minutes ? 'secondary' : 'ghost'} onClick={() => setMinutes(m)}>{m} min</Button>)}</div></div><dl className="flex flex-wrap gap-x-10 gap-y-3 text-sm">{[['CPU', available && last?.cpu_available !== false ? `${last!.cpu_percent.toFixed(1)}%` : '—'], [zh ? '内存' : 'Memory', available && last?.memory_available !== false ? sizeLabel(last!.memory_bytes) : '—'], [zh ? '网络 RX / TX' : 'Network RX / TX', available ? `${sizeLabel(last!.network_rx_bytes)} / ${sizeLabel(last!.network_tx_bytes)}` : '—'], ['PIDs', available ? String(last?.pids ?? '—') : '—'], [zh ? '磁盘读 / 写' : 'Block read / write', available ? `${sizeLabel(last?.block_read_bytes ?? 0)} / ${sizeLabel(last?.block_write_bytes ?? 0)}` : '—']].map(([label, value]) => <div key={label}><dt className="text-xs text-muted-foreground">{label}</dt><dd className="mt-1 font-mono text-xs">{value}</dd></div>)}</dl><MetricCharts points={points} minutes={minutes} /></section>
}
