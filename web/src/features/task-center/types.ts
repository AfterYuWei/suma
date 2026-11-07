// Task Center module types. Server state always comes from /api/v1/task-center
// and the existing task endpoints.

export interface TaskRecord {
  id: string
  scope: 'control_plane' | 'node'
  node_id?: string
  node_name?: string
  type: string
  name: string
  status: 'pending' | 'running' | 'success' | 'failed' | 'canceled' | string
  progress: number
  message: string
  params?: unknown
  result?: unknown
  created_at: string
  started_at?: string
  finished_at?: string
}

export interface TaskLog { id: number; level: string; message: string; created_at: string }
export interface TaskStep { id: string; status: string; current: number; total: number; progress: number; created_at: string; updated_at: string }

export interface KindInfo { type: string; requires_node: boolean }

export interface Schedule {
  id: number
  name: string
  kind: string
  node_id?: string
  node_name?: string
  subject: string
  params?: { project?: string; auto_apply?: boolean } & Record<string, unknown>
  cron: string
  timezone: string
  enabled: boolean
  version: number
  next_run_at?: string
  last_run_at?: string
  last_error?: string
  created_by?: string
  created_at: string
  updated_at: string
  summary?: string
  last_task_id?: string
  last_task_status?: string
  last_task_progress?: number
  last_task_message?: string
  last_task_finished_at?: string
}

export interface ScheduleRun {
  id: number
  schedule_id: number
  task_id: string
  triggered_by: 'scheduled' | 'manual' | string
  error?: string
  created_at: string
  task_status?: string
  task_progress?: number
  task_finished_at?: string
}

export interface ScheduleInput {
  name: string
  kind: string
  node_id?: string
  subject?: string
  params?: Record<string, unknown>
  cron: string
  timezone: string
  enabled?: boolean
}

export interface ImageUpdateServiceCheck {
  service: string
  reference?: string
  registry?: string
  status: 'current' | 'update_available' | 'blocked' | 'unavailable' | string
  reason_code?: string
  platform?: string
  local_image_id?: string
  remote_manifest_digest?: string
  remote_config_digest?: string
  replicas: number
}

export interface ImageUpdateSummary {
  project: string
  mode: 'check' | 'auto' | string
  services: ImageUpdateServiceCheck[]
  updated?: string[]
  update_task_id?: string
  finished_at: string
}

export interface ImageUpdateProjectStatus {
  project: string
  last_task_id?: string
  last_task_status?: string
  last_task_finished_at?: string
  running_task_id?: string
  last_result?: ImageUpdateSummary | null
}
