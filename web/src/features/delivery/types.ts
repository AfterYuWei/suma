export type GitRefType = 'branch' | 'tag' | 'commit'
export type ReconcileMode = 'observe' | 'manual' | 'auto'
export type GitAuthType = 'none' | 'http_token' | 'http_basic' | 'ssh_key'

export interface DeliveryProject {
  id: number
  name: string
  configured: boolean
  repository_url?: string
  git_ref?: string
  desired_commit?: string
  observed_commit?: string
  active_release_id?: number
  created_at: string
  updated_at: string
	node_ids: string[]
}

export interface GitRepositoryConfiguration {
  clone_url: string
  ref_type: GitRefType
  ref: string
  authentication: GitAuthentication
  compose_files: string[]
  environment_file: string
}

export type GitCredentialSource = 'none' | 'center' | 'project'
export interface GitAuthentication {
  source: GitCredentialSource
  credential_id?: number
  credential?: GitCredentialInput
  summary?: Pick<GitCredential, 'name' | 'auth_type' | 'username' | 'fingerprint'>
  save_to_center?: boolean
}

export interface CDConfiguration {
  configured: boolean
  repository: GitRepositoryConfiguration
  reconcile_mode: ReconcileMode
  sync_interval_seconds: number
  desired_commit: string
  observed_commit: string
  active_release_id?: number
  auto_rollback: boolean
  deployment_timeout: number
  webhook_enabled: boolean
  webhook_id?: string
  webhook_secret?: string
	node_ids: string[]
	registry_credential_ids: number[]
}

export interface CDConfigureInput {
  repository: GitRepositoryConfiguration
  reconcile_mode: ReconcileMode
  sync_interval_seconds: number
  auto_rollback: boolean
  deployment_timeout: number
  webhook_enabled: boolean
  webhook_secret: string
	node_ids: string[]
	registry_credential_ids: number[]
}

export interface CDDrift {
  drifted: boolean
  status: 'healthy' | 'degraded' | 'unknown'
  desired_commit: string
  observed_commit: string
  active_commit: string
  active_release_id?: number
  reason?: string
  reason_code?: string
  runtime_healthy: boolean
  checked_at: string
  nodes: CDNodeDrift[]
}

export interface CDNodeDrift {
  node_id: string
  node_name: string
  status: 'healthy' | 'degraded' | 'unknown'
  drifted: boolean
  active_release_id?: number
  active_commit?: string
  reason_code?: string
  reason?: string
  health_summary?: string
  checked_at: string
}

export interface DeliveryDeploymentAttempt {
  id: number
  deployment_id: number
  operation: 'deploy' | 'retry' | 'manual_rollback' | 'auto_rollback'
  target_release_id: number
  task_id?: string
  status: string
  failure_reason?: string
  health_summary?: string
  started_at?: string
  finished_at?: string
  created_at: string
  progress?: number
  message?: string
}

export interface DeliveryRelease {
  id: number
  project_id: number
  repository_url: string
  git_ref: string
  commit_sha: string
  commit_message: string
  commit_author: string
  config_hash: string
  image_references: string
  task_id?: string
  status: string
  trigger_type: string
  trigger_actor: string
  previous_release_id?: number
  compose_files: string
  approved_by?: number
  approved_at?: string
  started_at?: string
  finished_at?: string
  failure_reason?: string
  health_summary?: string
  created_at: string
  updated_at: string
  deployments?: { id: number; node_id: string; node_name: string; task_id?: string; status: string; previous_release_id?: number; failure_reason?: string; rollback_result?: string; health_summary?: string; started_at?: string; finished_at?: string; progress?: number; message?: string; attempts?: DeliveryDeploymentAttempt[] }[]
  remediation: {
    retry_failed_node_ids: string[]
    rollback_failed_node_ids: string[]
  }
}

export interface GitCredential {
  id: number
  name: string
  auth_type: GitAuthType
  username?: string
  fingerprint?: string
  created_at: string
  updated_at: string
  last_used_at?: string
  used_by: number
	authorized_node_ids: string[]
}

export interface GitCredentialInput {
  name: string
  auth_type: GitAuthType
  username: string
  secret: string
  private_key: string
  passphrase: string
  known_hosts: string
  custom_ca: string
  fingerprint?: string
	authorized_node_ids?: string[]
}

export const defaultRepository = (): GitRepositoryConfiguration => ({
  clone_url: '',
  ref_type: 'branch',
  ref: 'main',
  authentication: { source: 'none' },
  compose_files: ['compose.yml'],
  environment_file: '',
})

export function normalizedCDConfiguration(configuration: CDConfiguration): CDConfiguration {
  return {
    ...configuration,
    repository: {
      ...defaultRepository(),
      ...configuration.repository,
      authentication: configuration.repository?.authentication || { source: 'none' },
      compose_files: configuration.repository?.compose_files?.length ? configuration.repository.compose_files : ['compose.yml'],
    },
    reconcile_mode: configuration.reconcile_mode || 'manual',
    sync_interval_seconds: configuration.sync_interval_seconds || 300,
    deployment_timeout: configuration.deployment_timeout || 120,
	node_ids: configuration.node_ids?.length ? configuration.node_ids : ['local'],
	registry_credential_ids: configuration.registry_credential_ids || [],
  }
}

export function shortCommit(value?: string) {
  return value ? value.slice(0, 8) : '—'
}

export function hasActiveRelease(releases?: DeliveryRelease[]) {
  const active = new Set(['validating', 'pulling', 'deploying', 'verifying', 'rolling_back'])
  return releases?.some((release) => active.has(release.status)) ?? false
}

export function parseStringList(value?: string) {
  if (!value) return []
  try {
    const parsed: unknown = JSON.parse(value)
    return Array.isArray(parsed) ? parsed.filter((item): item is string => typeof item === 'string') : []
  } catch {
    return []
  }
}

export function repositoryName(value: string) {
  const clean = value.replace(/\.git$/, '').replace(/\/$/, '')
  const separator = Math.max(clean.lastIndexOf('/'), clean.lastIndexOf(':'))
  return separator >= 0 ? clean.slice(separator + 1) : clean
}
