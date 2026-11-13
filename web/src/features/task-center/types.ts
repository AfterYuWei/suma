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
	result_error?: string
	parent_task_id?: string
  run_id?: number
  schedule_id?: number
  schedule_name?: string
  triggered_by?: string
  created_at: string
  started_at?: string
  finished_at?: string
}

export interface TaskLog { id: number; level: string; message: string; created_at: string }
export interface TaskStep { id: string; status: string; current: number; total: number; progress: number; created_at: string; updated_at: string }

export interface KindInfo { type: string; requires_node: boolean; schedule_types: ('cron' | 'interval' | 'none')[]; actions: string[]; webhook: boolean; business_owned: boolean }

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
	schedule_type: 'cron' | 'interval' | 'none'
	interval_seconds: number
	timer_enabled: boolean
	webhook_enabled: boolean
	archived_at?: string
	effective_status?: string
  version: number
  next_run_at?: string
  last_run_at?: string
  last_error?: string
  created_by?: string
  created_at: string
  updated_at: string
  summary?: string
  last_task_id?: string
	last_run_id?: number
	last_run_status?: string
	last_run_reason?: string
	last_run_error?: string
  last_task_status?: string
  last_task_progress?: number
  last_task_message?: string
  last_task_finished_at?: string
}

export interface ScheduleRun {
  id: number
  schedule_id?: number
  task_id: string
  triggered_by: 'scheduled' | 'manual' | string
  error?: string
	status: 'dispatching' | 'started' | 'skipped' | 'start_failed' | 'interrupted'
	reason_code?: string
	scheduled_for?: string
	source_id?: string
  created_at: string
  actor_id?: number
  config_version?: string
  rule_snapshot?: unknown
  kind?: string
  node_id?: string
  node_name?: string
  rule_name?: string
  subject?: string
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
	schedule_type?: 'cron' | 'interval' | 'none'
	interval_seconds?: number
	timer_enabled?: boolean
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
