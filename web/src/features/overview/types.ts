export type NodeMetricProfile = 'small' | 'medium' | 'large'

export interface FleetContainer {
  id: string
  name: string
  image: string
  state: string
  available: boolean
  cpu_percent: number
  memory_bytes: number
  network_rx_bytes: number
  network_tx_bytes: number
  block_read_bytes: number
  block_write_bytes: number
  uptime_seconds: number
}

export interface FleetNode {
  id: string
  name: string
  connection_type: string
  tls_mode: string
  enabled: boolean
  status: string
  engine_version?: string
  last_latency_ms?: number
  last_checked_at?: string
  last_error?: string
  hostname?: string
  os?: string
  os_version?: string
  architecture?: string
  kernel_version?: string
  containers_running: number
  containers_paused: number
  containers_stopped: number
  images: number
  networks?: number
  volumes?: number
  cpus: number
  memory_total_bytes: number
  docker_disk_usage_bytes?: number
  storage_driver?: string
  logging_driver?: string
  cgroup_driver?: string
  cgroup_version?: string
  default_runtime?: string
  live_restore: boolean
  security_options?: string[]
  metrics_available: boolean
  container_cpu_percent: number
  container_memory_bytes: number
  container_network_rx_bytes: number
  container_network_tx_bytes: number
  container_block_read_bytes: number
  container_block_write_bytes: number
  longest_container_uptime_seconds: number
  containers: FleetContainer[]
}
