export interface Instance { id: string; node_id: string; engine_id: string; container_id: string; node_name: string; name: string; image: string; project: string; service: string; first_seen: string; last_seen: string }
export interface Exclusion { kind: 'node' | 'container' | 'compose'; node_id: string; container_id?: string; project?: string; service?: string }
export interface Policy { enabled: boolean; interval_seconds: number; retention_days: number; exclusions: Exclusion[]; revision: number }
export const fields = ['cpu', 'memory', 'memory_limit', 'network_rx', 'network_tx', 'block_read', 'block_write', 'pids'] as const
export type Field = typeof fields[number]
export interface Aggregate { avg: number | null; min: number | null; max: number | null; count: number }
export interface Point { time: string; started_at: string; segment: string; values: Record<Field, Aggregate> }
export interface Target { node_id: string; engine_id: string; container_id: string; since: string; until: string }
export interface Gap { id: number; node_id: string; instance_id: string; since: string; until: string; reason: string }
export interface Series { instance: Instance; target: Target; points: Point[]; summary: Record<Field, Aggregate>; gaps: Gap[]; coverage: string; step_seconds: number; truncated: boolean }
export interface QueryResult { series: Series[]; retained_since: string }
export interface Instances { nodes?: {id:string;name:string}[]; items: Instance[]; total: number; page: number }
export interface MonitorEvent { id: string; instance_id: string; time: string; action: string; health_status?: string; exit_code: number | null; signal?: string }
export interface EventPage { items: MonitorEvent[]; next_cursor?: string }
export interface Status { policy: Policy; nodes: { node_id: string; streams: number; last_write: string | null; error?: string }[]; active_containers: number; estimated_daily_records: number; cleanup_task_id?: string }
