import type { DockerNode } from '../../lib/nodes'
import type { FleetContainer } from './types'

export interface NodeMetricPoint { time: number; cpu: number | null; memory: number | null }
export const nodeHistoryWindow = 300_000
export const nodeSampleTimeout = 30_000
export const nodeIdentity = (node: DockerNode) => [node.id, node.updated_at, node.agent_connected_at ?? null]

export function nodeLoads(metrics: { metrics_available: boolean; container_cpu_percent: number; container_memory_bytes: number } | undefined, cpus: number, memory: number) {
  const cpu = metrics?.metrics_available && cpus > 0 && Number.isFinite(metrics.container_cpu_percent) && metrics.container_cpu_percent >= 0 ? metrics.container_cpu_percent / cpus : null
  const used = metrics?.container_memory_bytes
  return { cpu, memory: metrics?.metrics_available && memory > 0 && used != null && Number.isFinite(used) && used >= 0 ? used / memory * 100 : null }
}

export function appendNodeHistory(history: NodeMetricPoint[], point: NodeMetricPoint): NodeMetricPoint[] {
  if (!Number.isFinite(point.time) || (history.length && point.time <= history.at(-1)!.time)) return history
  const next = history.filter(sample => sample.time >= point.time - nodeHistoryWindow)
  const last = next.at(-1)
  if (last && point.time - last.time > nodeSampleTimeout) next.push({ time: last.time + 1, cpu: null, memory: null })
  return [...next, point].slice(-121)
}

export function visibleNodeHistory(history: NodeMetricPoint[], now: number) {
  const first = history.findIndex(point => point.time >= now - nodeHistoryWindow)
  const points = first < 0 ? [] : first === 0 ? history : history.slice(first)
  const last = points.at(-1)
  if (last && now - last.time > nodeSampleTimeout && (last.cpu != null || last.memory != null)) {
    return [...points, { time: last.time + nodeSampleTimeout, cpu: null, memory: null }]
  }
  return points
}

export function memoryTopFive(containers: FleetContainer[]) {
  return containers.filter(row => row.available && Number.isFinite(row.memory_bytes) && row.memory_bytes >= 0)
    .sort((a, b) => b.memory_bytes - a.memory_bytes || a.name.localeCompare(b.name) || a.id.localeCompare(b.id)).slice(0, 5)
}

export function nodeMetricSummary(points: NodeMetricPoint[], metric: 'cpu' | 'memory') {
  const values = points.map(point => point[metric]).filter((value): value is number => value != null && Number.isFinite(value))
  return { count: values.length, average: values.length ? values.reduce((sum, value) => sum + value, 0) / values.length : null, peak: values.length ? Math.max(...values) : null }
}

export function overviewDuration(seconds: number, zh: boolean) {
  if (seconds <= 0) return '—'
  const days = Math.floor(seconds / 86400), hours = Math.floor(seconds % 86400 / 3600), minutes = Math.floor(seconds % 3600 / 60)
  if (days > 0) return zh ? `${days} 天 ${hours} 小时` : `${days}d ${hours}h`
  if (hours > 0) return zh ? `${hours} 小时 ${minutes} 分钟` : `${hours}h ${minutes}m`
  return zh ? `${minutes} 分钟` : `${minutes}m`
}

export const overviewBytes = (value: number) => {
  if (value >= 1024 ** 4) return `${(value / 1024 ** 4).toFixed(1)} TB`
  if (value >= 1024 ** 3) return `${(value / 1024 ** 3).toFixed(1)} GB`
  if (value >= 1024 ** 2) return `${(value / 1024 ** 2).toFixed(0)} MB`
  if (value >= 1024) return `${(value / 1024).toFixed(0)} KB`
  return `${value} B`
}
