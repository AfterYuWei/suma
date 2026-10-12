export interface ContainerPort { private_port: number; public_port?: number; type: string; ip?: string }
export interface ContainerSummary { id: string; name: string; image: string; command: string; created: string; state: string; status: string; ports: ContainerPort[]; labels: Record<string, string>; cpu_percent: number; memory_bytes: number; uptime_seconds: number }
export interface ContainerMetrics { cpu_available?: boolean; memory_available?: boolean; id: string; available: boolean; cpu_percent: number; memory_bytes: number; uptime_seconds: number; network_rx_bytes: number; network_tx_bytes: number; sampled_at?: string; started_at?: string; pids?: number; block_read_bytes?: number; block_write_bytes?: number }
export interface ContainerRuntime { id: string; health_status: string | null; restart_count: number | null; memory_limit_bytes: number | null; state?: string | null; started_at?: string | null; finished_at?: string | null; exit_code?: number | null; oom_killed?: boolean | null; state_error?: string | null; observed_at?: string | null }
export interface Environment { key: string; value?: string; sensitive: boolean }
export interface ContainerDetail extends ContainerSummary { pid: number; entrypoint: string[]; working_directory: string; restart_policy: string; read_only_rootfs: boolean; environment: Environment[]; mounts: { anonymous?: boolean | null; type: string; name?: string; source: string; destination: string; read_write: boolean }[]; network_mode?: string; networks: { ipv6_address?: string; ipv6_gateway?: string; name: string; ip_address: string; gateway: string; mac_address: string }[] }

export interface HealthCheckRecord { started_at: string; finished_at: string; exit_code: number; output: string; output_redacted: boolean; output_truncated: boolean }
export interface ContainerDiagnostics { runtime: ContainerRuntime; restart_policy: string | null; health: { configured: boolean | null; failing_streak: number | null; last_checked_at: string | null; command: string | null; interval_ms: number | null; timeout_ms: number | null; start_period_ms: number | null; start_interval_ms: number | null; retries: number | null; logs: HealthCheckRecord[] } }
export interface MetricPoint { time: number; cpu: number | null; memory: number | null; startedAt?: string }
export interface ContainerOverview {
  id: string; network_mode: string | null; counts: { networks: number; mounts: number; ports: number }
  ports: (ContainerPort & { host_binding: boolean })[]
  networks: { name: string; ip_address?: string; ipv6_address?: string }[]
  mounts: { type: string; name?: string; source?: string; destination: string; read_write: boolean; anonymous?: boolean | null }[]
}
export interface ContainerEvent { id: string; node_id: string; container_id: string; time: string; time_nano: string; action: string; exit_code: number | null; signal?: string; health_status?: string }
export interface EventHistory { events: ContainerEvent[]; source: 'docker'; coverage: 'engine_retained'; requested_since: string; requested_until: string; truncated: boolean }
