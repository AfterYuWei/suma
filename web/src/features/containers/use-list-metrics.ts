import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { api } from '../../lib/api'
import { nodePath } from '../../lib/nodes'
import { networkRate, type NetworkRate } from './network-rate'
import { appendHistory } from './observation'
import type { MetricPoint, ContainerMetrics } from './types'

interface ListMetrics extends ContainerMetrics { rate: NetworkRate }

export function useListMetrics(nodeID: string, enabled = true) {
  const client = useQueryClient()
  const session = useRef({ nodeID, fresh: true })
  if (session.current.nodeID !== nodeID) session.current = { nodeID, fresh: true }
  const [now, setNow] = useState(Date.now)
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [])
  const queryKey = ['container-metrics', nodeID, 'list']
  const query = useQuery({
    enabled,
    queryKey,
    queryFn: async ({ signal }) => {
      const identity = session.current
      const previous = !identity.fresh && client.getQueryState(queryKey)?.status === 'success'
        ? client.getQueryData<ListMetrics[]>(queryKey) : undefined
      const samples = await api<ContainerMetrics[]>(nodePath(nodeID, '/containers/metrics'), { signal })
      const byID = new Map(previous?.map(sample => [sample.id, sample]))
      const rows = samples.map(sample => ({ ...sample, rate: networkRate(sample, byID.get(sample.id)) }))
      if (session.current === identity && !signal.aborted) {
        identity.fresh = false
        client.setQueryData<Record<string, MetricPoint[]>>(['container-metric-history', nodeID], old => {
          const valid = samples.filter(sample => sample.available && sample.sampled_at && Number.isFinite(Date.parse(sample.sampled_at)))
          const latest = valid.length ? Math.max(...valid.map(sample => Date.parse(sample.sampled_at!))) : Date.now()
          const next = Object.fromEntries(Object.entries(old ?? {}).map(([id, points]) => [id, points.filter(point => point.time >= latest - 300_000)]).filter(([, points]) => (points as MetricPoint[]).length > 0)) as Record<string, MetricPoint[]>
          for (const sample of valid) next[sample.id] = appendHistory(next[sample.id] ?? [], { time: Date.parse(sample.sampled_at!), cpu: sample.cpu_available === false ? null : sample.cpu_percent, memory: sample.memory_available === false ? null : sample.memory_bytes, startedAt: sample.started_at })
          return next
        })
      }
      return rows
    },
    retry: false,
    refetchInterval: 5000,
    refetchOnMount: 'always',
    refetchOnWindowFocus: 'always',
  })
  const unavailable = query.isError || (query.dataUpdatedAt > 0 && now - query.dataUpdatedAt > 15_000)
  return { ...query, history: client.getQueryData<Record<string, MetricPoint[]>>(['container-metric-history', nodeID]) ?? {}, unavailable, samples: session.current.fresh ? undefined : query.data }
}
