export interface ContainerPort { private_port: number; public_port?: number; type: string; ip?: string }
export interface ContainerSummary { id: string; name: string; image: string; command: string; created: string; state: string; status: string; ports: ContainerPort[]; labels: Record<string, string>; cpu_percent: number; memory_bytes: number; uptime_seconds: number }
export interface ContainerMetrics { id: string; cpu_percent: number; memory_bytes: number; uptime_seconds: number }
export interface Environment { key: string; value?: string; sensitive: boolean }
export interface ContainerDetail extends ContainerSummary { pid: number; entrypoint: string[]; working_directory: string; restart_policy: string; environment: Environment[]; mounts: { type: string; name?: string; source: string; destination: string; read_write: boolean }[]; networks: { name: string; ip_address: string; gateway: string; mac_address: string }[] }
