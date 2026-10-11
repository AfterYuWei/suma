import { useQuery } from '@tanstack/react-query'
import type { FleetNode } from '../../components/docker/node-overview-card'
import { api } from '../../lib/api'
import { nodePath, type DockerNode } from '../../lib/nodes'
import type { NodeCardSize } from '../../stores/ui'

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

export function useNodeOverview(metadata: DockerNode, size: NodeCardSize) {
  const identity = [metadata.id, metadata.updated_at, metadata.agent_connected_at]
  const options = { refetchInterval: 10_000, refetchOnWindowFocus: 'always' as const, refetchOnReconnect: 'always' as const, retry: false }
  const info = useQuery({
    ...options,
    queryKey: ['node-overview-info', ...identity],
    queryFn: ({ signal }) => api<NodeInfo>(nodePath(metadata.id, '/overview/info'), { signal }),
    enabled: metadata.enabled,
  })
  const enabled = metadata.enabled && info.isSuccess
  const metrics = useQuery({
    ...options,
    queryKey: ['node-overview-metrics', ...identity, size],
    queryFn: ({ signal }) => api<NodeMetrics>(nodePath(metadata.id, `/overview/metrics?size=${size}`), { signal }),
    enabled,
  })
  const resources = useQuery({
    ...options,
    queryKey: ['node-overview-resources', ...identity],
    queryFn: ({ signal }) => api<{ networks: number; volumes: number }>(nodePath(metadata.id, '/overview/resources'), { signal }),
    enabled,
  })
  const disk = useQuery({
    ...options,
    queryKey: ['node-overview-disk', ...identity],
    queryFn: ({ signal }) => api<{ docker_disk_usage_bytes: number }>(nodePath(metadata.id, '/overview/disk'), { signal }),
    enabled,
    staleTime: 60_000,
    refetchInterval: 60_000,
    refetchOnWindowFocus: true,
    refetchOnReconnect: true,
  })
  const details = info.data
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
    ...(enabled && !metrics.isError ? metrics.data : undefined),
    ...(enabled && !resources.isError ? resources.data : undefined),
    ...(enabled && !disk.isError ? disk.data : undefined),
  }
  return {
    node,
    loading: {
      info: metadata.enabled && info.isPending,
      metrics: metadata.enabled && !info.isError && metrics.isPending,
      resources: metadata.enabled && !info.isError && resources.isPending,
      disk: metadata.enabled && !info.isError && disk.isPending,
    },
    errors: { info: info.isError, metrics: enabled && metrics.isError, resources: enabled && resources.isError, disk: enabled && disk.isError },
    retry: { info: () => void info.refetch(), metrics: () => void metrics.refetch(), resources: () => void resources.refetch(), disk: () => void disk.refetch() },
  }
}
