import type { Field, QueryResult } from './types'

export function localInput(time: number, timeZone: string) {
  const parts = Object.fromEntries(new Intl.DateTimeFormat('en-GB', { timeZone, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).formatToParts(time).map(p => [p.type, p.value]))
  return parts.year + '-' + parts.month + '-' + parts.day + 'T' + parts.hour + ':' + parts.minute
}
export function parseInput(value: string, timeZone: string): number {
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/.test(value)) throw new Error('Invalid time')
  const wall = Date.parse(value + ':00Z')
  let candidate = wall
  for (let i = 0; i < 4; i++) candidate += wall - Date.parse(localInput(candidate, timeZone) + ':00Z')
  if (!Number.isFinite(candidate) || localInput(candidate, timeZone) !== value) throw new Error('Invalid time')
  return candidate
}
export function metricLabel(field: Field, zh: boolean) {
  return ({ cpu: 'CPU', memory: zh ? '内存' : 'Memory', memory_limit: zh ? '内存限额' : 'Memory limit', network_rx: zh ? '网络接收' : 'Network RX', network_tx: zh ? '网络发送' : 'Network TX', block_read: zh ? '磁盘读取' : 'Block read', block_write: zh ? '磁盘写入' : 'Block write', pids: 'PIDs' })[field]
}
export function metricValue(field: Field, value: number | null | undefined): string {
  if (value == null || !Number.isFinite(value)) return '—'
  if (field === 'cpu') return value.toFixed(1) + '%'
  if (field === 'pids') return value.toFixed(0)
  if (field === 'memory_limit' && value === 0) return '∞'
  const rate = field.startsWith('network') || field.startsWith('block')
  const n = value >= 1024 ** 3 ? (value / 1024 ** 3).toFixed(2) + ' GiB' : value >= 1024 ** 2 ? (value / 1024 ** 2).toFixed(1) + ' MiB' : value >= 1024 ? (value / 1024).toFixed(1) + ' KiB' : value.toFixed(0) + ' B'
  return n + (rate ? '/s' : '')
}
export function monitoringCSV(result: QueryResult) {
  const columns = ['node_id', 'engine_id', 'container_id', 'name', 'time_utc', 'started_at', 'segment', 'step_seconds', 'metric', 'unit', 'average', 'minimum', 'maximum', 'valid_samples']
  const quote = (v: unknown) => '"' + String(v ?? '').replace(/"/g, '""').replace(/^[=+@-]/, "'$&") + '"'
  const rows: unknown[][] = [columns]
  for (const series of result.series) for (const point of series.points) for (const [field, values] of Object.entries(point.values)) rows.push([series.instance.node_id, series.instance.engine_id, series.instance.container_id, series.instance.name, point.time, point.started_at, point.segment, series.step_seconds, field, field === 'cpu' ? '%' : field === 'pids' ? 'count' : field.startsWith('network') || field.startsWith('block') ? 'B/s' : 'B', values.avg, values.min, values.max, values.count])
  return '\ufeff' + rows.map(row => row.map(quote).join(',')).join('\r\n')
}
