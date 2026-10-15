import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useMemo, useState } from 'react'
import type { FleetNode, NodeMetricProfile } from './types'
import { appendNodeHistory, nodeIdentity, nodeLoads, nodeSampleTimeout, visibleNodeHistory, type NodeMetricPoint } from './observation'
import { api } from '../../lib/api'
import { nodePath, type DockerNode } from '../../lib/nodes'

interface NodeInfo {
  name: string
  server_version: string
  operating_system: string
  os_version: string
  architecture: string
  kernel_version: string
  containers_running: number
  containers_paused: number
  containers_stopped: number
  images: number
  cpus: number
  memory_bytes: number
  storage_driver: string
  logging_driver: string
  cgroup_driver: string
  cgroup_version: string
  default_runtime: string
  live_restore: boolean
  security_options: string[]
}

type NodeMetrics = Pick<FleetNode, 'metrics_available' | 'container_cpu_percent' | 'container_memory_bytes'> & Partial<FleetNode>
const emptyHistory: NodeMetricPoint[] = []
const refreshOptions = { refetchInterval: 10_000, refetchOnWindowFocus: 'always' as const, refetchOnReconnect: 'always' as const, retry: false }

export function useNodeOverview(metadata: DockerNode, size: NodeMetricProfile) {
  const client = useQueryClient()
  const identity = nodeIdentity(metadata)
  const historyKey = ['node-overview-history', ...identity]
  const history = useQuery<NodeMetricPoint[]>({ queryKey: historyKey, queryFn: () => emptyHistory, enabled: false, staleTime: Infinity, gcTime: 300_000 })
  const [now, setNow] = useState(Date.now)
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [])
  const record = (point: NodeMetricPoint) => client.setQueryData<NodeMetricPoint[]>(historyKey, old => appendNodeHistory(old ?? [], point))
  const info = useQuery({
    ...refreshOptions,
    queryKey: ['node-overview-info', ...identity],
    queryFn: async ({ signal }) => {
      try { return await api<NodeInfo>(nodePath(metadata.id, '/overview/info'), { signal }) }
      catch (error) { if (!signal.aborted) record({ time: Date.now(), cpu: null, memory: null }); throw error }
    },
    enabled: metadata.enabled,
  })
  const enabled = metadata.enabled && info.isSuccess
  const metrics = useQuery({
    ...refreshOptions,
    queryKey: ['node-overview-metrics', ...identity, size],
    queryFn: async ({ signal }) => {
      try {
        const sample = await api<NodeMetrics>(nodePath(metadata.id, `/overview/metrics?size=${size}`), { signal })
        if (!signal.aborted) record({ time: Date.now(), ...nodeLoads(sample, info.data?.cpus ?? 0, info.data?.memory_bytes ?? 0) })
        return sample
      } catch (error) { if (!signal.aborted) record({ time: Date.now(), cpu: null, memory: null }); throw error }
    },
    enabled,
  })
  const details = info.data
  const stale = metrics.dataUpdatedAt > 0 && now - metrics.dataUpdatedAt > nodeSampleTimeout
  const node: FleetNode = {
    ...metadata,
    status: metadata.enabled ? info.isError ? 'offline' : details ? 'online' : 'unknown' : metadata.status,
    hostname: details?.name,
    os: details?.operating_system,
    os_version: details?.os_version,
    architecture: details?.architecture,
    kernel_version: details?.kernel_version,
    engine_version: details?.server_version ?? metadata.engine_version,
    containers_running: details?.containers_running ?? 0,
    containers_paused: details?.containers_paused ?? 0,
    containers_stopped: details?.containers_stopped ?? 0,
    images: details?.images ?? 0,
    cpus: details?.cpus ?? 0,
    memory_total_bytes: details?.memory_bytes ?? 0,
    storage_driver: details?.storage_driver,
    logging_driver: details?.logging_driver,
    cgroup_driver: details?.cgroup_driver,
    cgroup_version: details?.cgroup_version,
    default_runtime: details?.default_runtime,
    live_restore: details?.live_restore ?? false,
    security_options: details?.security_options,
    metrics_available: false,
    container_cpu_percent: 0,
    container_memory_bytes: 0,
    container_network_rx_bytes: 0,
    container_network_tx_bytes: 0,
    container_block_read_bytes: 0,
    container_block_write_bytes: 0,
    longest_container_uptime_seconds: 0,
    containers: [],
    ...(enabled && !metrics.isError && !stale ? metrics.data : undefined),
  }
  const points = useMemo(() => visibleNodeHistory(history.data ?? emptyHistory, now), [history.data, now])
  return {
    node, points, stale,
    loading: { info: metadata.enabled && info.isPending, metrics: metadata.enabled && !info.isError && metrics.isPending },
    errors: { info: info.isError, metrics: enabled && metrics.isError },
    retry: { info: () => void info.refetch(), metrics: () => void metrics.refetch() },
  }
}

// Mount only while the information sheet is open so consumed signals are
// aborted on close and hidden disk accounting never polls the Engine.
export function useNodeOverviewDetails(metadata: DockerNode, enabled: boolean) {
  const identity = nodeIdentity(metadata)
  const resources = useQuery({
    ...refreshOptions,
    queryKey: ['node-overview-resources', ...identity],
    queryFn: ({ signal }) => api<{ networks: number; volumes: number }>(nodePath(metadata.id, '/overview/resources'), { signal }),
    enabled,
    staleTime: 10_000,
  })
  const disk = useQuery({
    ...refreshOptions,
    queryKey: ['node-overview-disk', ...identity],
    queryFn: ({ signal }) => api<{ docker_disk_usage_bytes: number }>(nodePath(metadata.id, '/overview/disk'), { signal }),
    enabled,
    staleTime: 60_000,
    refetchInterval: 60_000,
    refetchOnWindowFocus: true,
    refetchOnReconnect: true,
  })
  return { resources, disk }
}
