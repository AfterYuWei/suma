import { ApiError } from '../../lib/api-error'
import { fields, type Aggregate, type Instance, type Policy, type QueryResult, type Target } from './types'

export function createMonitoringMock(source: () => Instance[], now: () => number = Date.now) {
  let policy: Policy = { enabled: true, interval_seconds: 5, retention_days: 3, exclusions: [], revision: 1 }
  const captured = now()
  const event = (i: Instance) => ({ id: i.id, instance_id: i.id, time: new Date(captured - 20 * 60_000).toISOString(), action: 'health_status', health_status: 'unhealthy', exit_code: null })
  return (path: string, method: string, body: unknown, url: URL): unknown => {
    if (!path.startsWith('/monitoring')) return undefined
    const instances = source()
    if (path === '/monitoring/settings') {
      if (method === 'PUT') {
        const input = body as Policy & { confirm_retention_reduction?: boolean }
        if (input.revision !== policy.revision) throw new ApiError('Monitoring settings changed; reload before saving', 21203, 409)
        if (input.retention_days < policy.retention_days && !input.confirm_retention_reduction) throw new ApiError('Confirm removal of monitoring history', 21202, 409)
        policy = { enabled: input.enabled, interval_seconds: input.interval_seconds, retention_days: input.retention_days, exclusions: input.exclusions, revision: policy.revision + 1 }
        return { settings: policy, task_id: '' }
      }
      return policy
    }
    if (path === '/monitoring/status') return { policy, active_containers: policy.enabled ? instances.length : 0, estimated_daily_records: instances.length * 86400 / policy.interval_seconds, nodes: [...new Set(instances.map(i => i.node_id))].map(node => ({ node_id: node, streams: policy.enabled ? instances.filter(i => i.node_id === node).length : 0, last_write: new Date().toISOString() })) }
    if (path === '/monitoring/instances') {
      const search = (url.searchParams.get('search') ?? '').toLowerCase(), node = url.searchParams.get('node_id'), container = url.searchParams.get('container_id')
      const rows = instances.filter(i => (!node || i.node_id === node) && (!container || i.container_id === container) && [i.name, i.node_name, i.container_id, i.project].some(s => s.toLowerCase().includes(search)))
      const page = Number(url.searchParams.get('page') ?? 1), limit = Number(url.searchParams.get('limit') ?? 50)
      return { items: rows.slice((page - 1) * limit, page * limit), total: rows.length, page, nodes:[...new Map(instances.map(i=>[i.node_id,i.node_name])).entries()].map(([id,name])=>({id,name})) }
    }
    if (/^\/monitoring\/instances\/[^/]+\/events$/.test(path)) {
      const instance = instances.find(i => i.id === path.split('/').at(-2))
      const e = instance && event(instance)
      return { items: e && Date.parse(e.time) >= Date.parse(url.searchParams.get('since') ?? '') && Date.parse(e.time) <= Date.parse(url.searchParams.get('until') ?? '') ? [e] : [] }
    }
    if (path.startsWith('/monitoring/instances/')) return instances.find(i => i.id === path.split('/').at(-1))
    if (path === '/monitoring/query') {
      const input = body as { targets: Target[]; max_points: number }
      const cutoff = now() - policy.retention_days * 86400_000
      const step = Math.max(policy.interval_seconds, Math.ceil(Math.max(...input.targets.map(t => (Date.parse(t.until) - Date.parse(t.since)) / 1000)) / input.max_points))
      return {
        retained_since: new Date(cutoff).toISOString(),
        series: input.targets.map((target, index) => {
          const instance = instances.find(i => i.node_id === target.node_id && i.container_id === target.container_id) ?? { id: target.container_id, node_id: target.node_id, engine_id: target.engine_id, container_id: target.container_id, node_name: target.node_id, name: target.container_id.slice(0, 12), image: '', project: '', service: '', first_seen: new Date(captured).toISOString(), last_seen: new Date(captured).toISOString() }
          const start = Math.max(cutoff, Date.parse(target.since)), end = Date.parse(target.until), points = []
          for (let time = start; time <= end && points.length < 1440; time += step * 1000) {
            const x = Math.sin(time / 30_000 + index), values = {} as Record<typeof fields[number], Aggregate>
            for (const field of fields) {
              const v = field === 'cpu' ? 5 + (x + 1) * 12 : field === 'memory' ? (200 + (x + 1) * 25) * 1024 ** 2 : field === 'memory_limit' ? 1024 ** 3 : field === 'pids' ? 14 : (x + 1) * 8192
              values[field] = { avg: v, min: v * .8, max: v * 1.2, count: Math.max(1, Math.floor(step / policy.interval_seconds)) }
            }
            points.push({ time: new Date(time).toISOString(), started_at: instance.first_seen, segment: 'demo-1', values })
          }
          const summary = {} as Record<typeof fields[number], Aggregate>
          for (const field of fields) {
            const values = points.map(p => p.values[field].avg!)
            summary[field] = { avg: values.length ? values.reduce((a, b) => a + b, 0) / values.length : null, min: values.length ? Math.min(...values) : null, max: values.length ? Math.max(...points.map(p => p.values[field].max!)) : null, count: values.length }
          }
          return { instance, target, points, summary, gaps: [], coverage: Date.parse(target.since) < cutoff ? 'expired' : points.length ? 'recorded' : 'not_collected', step_seconds: step, truncated: false }
        }),
      } satisfies QueryResult
    }
    return undefined
  }
}
