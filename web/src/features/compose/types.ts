export type ProjectBackend = 'compose' | 'swarm'
export type ProjectCapability = 'view' | 'edit' | 'deploy' | 'start' | 'stop' | 'restart' | 'update' | 'delete' | 'services' | 'logs' | 'networks' | 'volumes' | 'takeover' | 'cleanup' | 'shadow_preview'

export interface ProjectSummary {
  ref: { backend: ProjectBackend; scope: { kind: 'engine' | 'swarm'; id: string }; native_name: string }
  backend: ProjectBackend
  scope: { kind: 'engine' | 'swarm'; id: string }
  node_id: string
  name: string
  native_name: string
  managed: boolean
  source: 'managed' | 'external'
  capabilities: ProjectCapability[]
  service_count: number
  instance_count: number
  status: string
  created_at: string
  updated_at: string
}

export interface Project extends ProjectSummary {
  path: string
  can_manage: boolean
  config_files: string[]
  services: number
  containers: number
  compose: string
  environment: string
  metadata?: { origin: 'created' | 'takeover' | 'legacy'; takeover_source?: 'mapped' | 'runtime'; claimed_at: string; last_deployed_at?: string }
}

export interface ProjectContainerInstance {
  container_id: string
  container_name: string
  container_number: number
  config_hash: string
  state: string
  created_at: string
  one_off: boolean
  variant: string
}

export interface ObservedProjectService {
  name: string
  declared: boolean
  desired_replicas: number
  instances: ProjectContainerInstance[]
  config_variants: { fingerprint: string; instances: string[]; difference_fields?: string[] }[]
  canonical_variant: string
  drift_status: 'in_sync' | 'runtime_drift' | 'orphan' | 'not_created'
  drift_reasons?: ('runtime_drift' | 'stale_container' | 'manual_modification' | 'partial_recreate' | 'abnormal_state')[]
  drift_fields?: string[]
}

export interface EnvironmentCandidate {
  id: string
  service: string
  key: string
  value: string
  source: 'compose_explicit' | 'explicit_inferred' | 'image_default' | 'unknown'
  sensitive: boolean
  destination: 'compose' | 'env' | 'exclude'
  reason: string
}

export interface ProjectTakeoverDraft {
  project_name: string
  backend: 'compose'
  source: 'mapped' | 'runtime'
  confidence: 'high' | 'medium' | 'low'
  fingerprint: string
  compose: string
  environment: string
  variables: EnvironmentCandidate[]
  warnings: string[]
  blockers: string[]
  capabilities: ProjectCapability[]
  observation: {
    name: string
    services: ObservedProjectService[]
    one_off_containers: ProjectContainerInstance[]
    orphan_containers: ProjectContainerInstance[]
    warnings?: string[]
    fingerprint: string
  }
}

export interface ShadowAssessment {
  eligible: boolean
  reasons: string[]
  warnings: string[]
}

export interface ShadowPreviewSession {
  session_id: string
  preview_project: string
  expires_at: string
  task: { id: string; status: string; progress: number; message: string }
}

export interface ShadowPreviewStatus {
  session_id: string
  preview_project: string
  expires_at: string
  containers: string
  logs: string
}
