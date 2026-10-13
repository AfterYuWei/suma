import type { ContainerRuntime, ContainerSummary, MetricPoint } from './types'
import type { NetworkRate } from './network-rate'
import type { ContainerView } from '../../stores/container-view'
import { fullPortLabel } from './ports'

export const sizeLabel = (bytes: number) => bytes >= 1024 ** 3 ? `${(bytes / 1024 ** 3).toFixed(2)} GB` : `${(bytes / 1024 ** 2).toFixed(0)} MB`
export function effectiveHealth(row: Pick<ContainerSummary, 'state'>, meta?: ContainerRuntime): string {
  if (row.state !== 'running') return meta?.health_status === 'none' ? 'none' : 'inactive'
  return meta?.health_status ?? 'unknown'
}
export function isAbnormal(row: Pick<ContainerSummary, 'state'>, meta?: ContainerRuntime) {
  return row.state === 'dead' || row.state === 'restarting' || (row.state === 'running' && meta?.health_status === 'unhealthy') || meta?.oom_killed === true || (meta?.exit_code != null && meta.exit_code !== 0) || !!meta?.state_error
}
export function appendHistory(history: MetricPoint[], point: MetricPoint): MetricPoint[] {
  if (!Number.isFinite(point.time)) return history
  const last = history.at(-1)
  if (last && point.time <= last.time) return history
  let next = !point.startedAt || last?.startedAt !== point.startedAt ? [] : history.filter(p => p.time >= point.time - 300_000)
  if (last && next.length && point.time - last.time > 15_000) next = [...next, { time: last.time + 1, cpu: null, memory: null, startedAt: point.startedAt }]
  return [...next, point].slice(-61)
}
type Sample = { started_at?: string; cpu_available?: boolean; memory_available?: boolean; available: boolean; cpu_percent: number; memory_bytes: number; uptime_seconds: number; rate: NetworkRate }
export function selectContainers(rows: ContainerSummary[], view: ContainerView, metadata: Map<string, ContainerRuntime>, samples: Map<string, Sample>, zh: boolean) {
  const names: Record<string, string> = { running: '运行中', exited: '已停止', paused: '已暂停', restarting: '重启中', created: '已创建', dead: '异常', healthy: '健康', unhealthy: '不健康', starting: '检查启动中', none: '未配置', unknown: '未知', inactive: '未运行' }
  const filtered = rows.filter(row => {
    const meta = metadata.get(row.id), health = effectiveHealth(row, meta), project = row.labels['com.docker.compose.project'] ?? ''
    const text = `${row.name} ${row.id} ${row.image} ${row.state} ${row.status} ${zh ? names[row.state] : ''} ${health} ${zh ? names[health] : ''} ${row.ports.map(fullPortLabel).join(' ')} ${row.networks?.map(network => `${network.name} ${network.ip_address ?? ''} ${network.ipv6_address ?? ''}`).join(' ') ?? ''} ${project} ${row.labels['com.docker.compose.service'] ?? ''}`
    return text.toLowerCase().includes(view.search.toLowerCase()) && (!view.states.length || view.states.includes(row.state)) && (!view.health.length || view.health.includes(health)) && (!view.source || (view.source === 'compose' ? !!project : !project)) && (!view.project || project === view.project) && (!view.diagnostic || (view.diagnostic === 'abnormal' ? isAbnormal(row, meta) : (meta?.restart_count ?? 0) > 0))
  })
  const value = (row: ContainerSummary): string | number | null => {
    const m = metadata.get(row.id), s = samples.get(row.id), available = row.state === 'running' && s?.available
    switch (view.sort) {
      case 'name': return row.name
      case 'image': return row.image
      case 'created': return Number.isFinite(Date.parse(row.created)) ? Date.parse(row.created) : null
      case 'restarts': return m?.restart_count ?? null
      case 'cpu': return available && s.cpu_available !== false ? s.cpu_percent : null
      case 'memory': return available && s.memory_available !== false ? s.memory_bytes : null
      case 'uptime': return available && !!s.started_at && Number.isFinite(Date.parse(s.started_at)) ? s.uptime_seconds : null
      case 'memory_ratio': return available && s.memory_available !== false && m?.memory_limit_bytes != null && m.memory_limit_bytes > 0 ? s.memory_bytes / m.memory_limit_bytes : null
      case 'receive': return available && s.rate.status === 'ready' ? s.rate.receive : null
      case 'transmit': return available && s.rate.status === 'ready' ? s.rate.transmit : null
    }
  }
  return filtered.sort((a, b) => {
    const av = value(a), bv = value(b)
    if (av == null || bv == null) { if (av !== bv) return av == null ? 1 : -1 }
    else { const delta = typeof av === 'string' && typeof bv === 'string' ? av.localeCompare(bv) : Number(av) - Number(bv); if (delta) return view.descending ? -delta : delta }
    return a.name.localeCompare(b.name) || a.id.localeCompare(b.id)
  })
}
