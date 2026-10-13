import { useQuery } from '@tanstack/react-query'
import type { ContainerRuntime, ContainerSummary } from '../containers/types'
import { useFreshness } from '../containers/use-freshness'
import { useListMetrics } from '../containers/use-list-metrics'
import { api } from '../../lib/api'
import { nodePath } from '../../lib/nodes'
import { observationMaps } from './observation'

export function useProjectObservation(nodeID: string, rows: ContainerSummary[], enabled = true) {
  const runtime = useQuery({ queryKey: ['container-runtime', nodeID], queryFn: ({ signal }) => api<ContainerRuntime[]>(nodePath(nodeID, '/containers/runtime'), { signal }), enabled, refetchInterval: 10_000, retry: false })
  const fresh = useFreshness(runtime.dataUpdatedAt)
  const metrics = useListMetrics(nodeID, enabled)
  return observationMaps(rows, !runtime.isError && fresh ? runtime.data ?? [] : [], !metrics.unavailable ? metrics.samples ?? [] : [])
}
