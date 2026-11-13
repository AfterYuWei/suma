import type {ProjectCDConfiguration,ProjectCDRecord} from '../features/project-cd/queries'
import { createMonitoringMock } from '../features/monitoring/mock'
import type { Instance } from '../features/monitoring/types'
import { mockNotifications, persistMockNotifications } from '../features/notifications/mock'
import { validTimeZone } from './date-time'
import { operationsDemo, demoProjectLogStream } from './mock-operations'
import type { LogEvent } from '../features/project-logs/types'
import { composeProblems, configKeys, configValue } from '../features/compose/document'
import { createMockCleanup } from './mock-cleanup'
import { createMockTaskCenter } from './mock-task-center'
import { configuration as cleanupConfiguration, type Policy as CleanupPolicy } from '../features/cleanup/types'
import type { TaskRecord } from '../features/task-center/types'
import type { User } from '../features/auth/types'
import type { Project, ProjectSummary, ProjectTakeoverDraft, ShadowAssessment, ShadowPreviewSession, ShadowPreviewStatus } from '../features/compose/types'
import type { ContainerDetail, ContainerDiagnostics, ContainerMetrics, ContainerSummary } from '../features/containers/types'
import { containerCreateRequest, type ContainerCreateDraft, type CreateContainerRequest } from '../features/containers/create'
import type { CDConfiguration, CDConfigureInput, CDDrift, DeliveryProject, DeliveryRecord, DeliveryRelease, DeliveryHistoryRecord, GitCredential } from '../features/delivery/types'
import type { DockerNode, NodeGroup } from './nodes'
import { ApiError, type DemoCredentials, type DemoStream } from './api'

export const demoCredentials: DemoCredentials = { username: 'admin', password: 'admin123' }

const now = '2026-08-29T08:30:00.000Z'
const earlier = '2026-08-29T07:42:00.000Z'
const sessionKey = 'suma-demo-session'
const fleetOverviewRefreshSeconds = 10
let fleetOverviewTick = 0
const demoFileTrees = new Map<string, Map<string, { type: 'file' | 'directory'; content: string }>>()
const demoFileHistory = new Map<string, { id: number; content: string; created_at: string; baseline: boolean }[]>()
const demoFileHash = (value: string) => `demo-${value.length}-${value.split('').reduce((sum, char) => (sum * 31 + char.charCodeAt(0)) >>> 0, 0)}`

const user: User = { id: 1, username: 'admin', nickname: 'Demo Admin', email: 'admin@suma.demo', has_avatar: false, two_factor_enabled: false }
let demoRecoveryCodesRemaining = 0

const nodes: DockerNode[] = [
  { id: 'local', name: 'homelab-01', connection_type: 'unix', endpoint: 'unix:///var/run/docker.sock', tls_mode: 'disabled', enabled: true, engine_id: 'engine-homelab', engine_version: '28.3.3', status: 'online', last_latency_ms: 12, last_checked_at: now, created_at: earlier, updated_at: now, group_ids: [1, 2] },
  { id: 'edge-hk', name: 'edge-hk', connection_type: 'tcp', endpoint: 'tcp://10.20.0.8:2376', tls_mode: 'required', tls_credential_id: 1, enabled: true, engine_id: 'engine-edge-hk', engine_version: '28.3.3', status: 'online', last_latency_ms: 46, last_checked_at: now, created_at: earlier, updated_at: now, group_ids: [1, 3] },
  { id: 'nas-prod', name: 'nas-prod', connection_type: 'unix', endpoint: 'unix:///var/run/docker.sock', tls_mode: 'disabled', enabled: true, engine_id: 'engine-nas-prod', engine_version: '27.5.1', status: 'online', last_latency_ms: 21, last_checked_at: now, created_at: earlier, updated_at: now, group_ids: [1, 2] },
]

const mockCleanup = createMockCleanup(nodes)

const nodeGroups: NodeGroup[] = [
  { id: 1, name: 'Default', description: 'Default node group', is_default: true, node_count: 3, created_at: earlier, updated_at: now },
  { id: 2, name: 'HomeLab', description: 'Local home infrastructure', is_default: false, node_count: 2, created_at: earlier, updated_at: now },
  { id: 3, name: 'Edge', description: 'Remote edge engines', is_default: false, node_count: 1, created_at: earlier, updated_at: now },
]
const syncNodeGroupCounts = () => nodeGroups.forEach((group) => { group.node_count = nodes.filter((node) => node.group_ids.includes(group.id)).length })

const makeContainer = (id: string, name: string, image: string, state: string, cpu: number, memory: number, labels: Record<string, string>, ports: ContainerSummary['ports']): ContainerSummary => ({
  id: id.repeat(64).slice(0, 64), name, image, command: '/docker-entrypoint.sh', created: earlier, state, status: state === 'running' ? `Up 6 hours${name === 'gateway' ? ' (healthy)' : name === 'immich-server' ? ' (unhealthy)' : ''}` : 'Exited (0) 2 hours ago', ports, networks: [{ name: labels['com.docker.compose.project'] ? `${labels['com.docker.compose.project']}_default` : 'bridge', ip_address: state === 'running' ? `172.22.0.${parseInt(id, 16) + 2}` : undefined }], labels, cpu_percent: state === 'running' ? cpu : 0, memory_bytes: state === 'running' ? memory : 0, uptime_seconds: state === 'running' ? 21_840 : 0,
})

const containerMetricsEpoch = Date.now()

const containerSets: Record<string, ContainerSummary[]> = {
  local: [
    makeContainer('a', 'gateway', 'ghcr.io/afteryuwei/gateway:1.8.2', 'running', 4.7, 188_743_680, { 'com.docker.compose.project': 'gateway-prod', 'com.docker.compose.service': 'gateway' }, [{ private_port: 8080, public_port: 8080, type: 'tcp', ip: '0.0.0.0' }]),
    makeContainer('b', 'gateway-db', 'postgres:17-alpine', 'running', 2.1, 386_924_544, { 'com.docker.compose.project': 'gateway-prod', 'com.docker.compose.service': 'database' }, [{ private_port: 5432, type: 'tcp' }]),
    makeContainer('c', 'immich-server', 'ghcr.io/immich-app/immich-server:v1.138.0', 'running', 12.8, 1_126_400_000, { 'com.docker.compose.project': 'media-stack', 'com.docker.compose.service': 'server' }, [{ private_port: 2283, public_port: 2283, type: 'tcp', ip: '0.0.0.0' }]),
    makeContainer('d', 'redis-cache', 'redis:8-alpine', 'running', 0.8, 73_400_320, { 'com.docker.compose.project': 'media-stack', 'com.docker.compose.service': 'redis' }, [{ private_port: 6379, type: 'tcp' }]),
    makeContainer('e', 'legacy-worker', 'ghcr.io/example/worker:2026.07', 'exited', 0, 0, {}, []),
  ],
  'edge-hk': [
    makeContainer('f', 'edge-proxy', 'caddy:2.10-alpine', 'running', 3.2, 96_468_992, { 'com.docker.compose.project': 'edge-stack', 'com.docker.compose.service': 'proxy' }, [{ private_port: 443, public_port: 443, type: 'tcp', ip: '0.0.0.0' }]),
    makeContainer('1', 'edge-api', 'ghcr.io/afteryuwei/api:2.4.1', 'running', 8.6, 244_318_208, { 'com.docker.compose.project': 'edge-stack', 'com.docker.compose.service': 'api' }, [{ private_port: 3000, type: 'tcp' }]),
    makeContainer('2', 'uptime-agentless', 'louislam/uptime-kuma:1', 'running', 1.4, 164_626_432, {}, [{ private_port: 3001, public_port: 3001, type: 'tcp', ip: '127.0.0.1' }]),
    makeContainer('3', 'old-proxy', 'nginx:1.27-alpine', 'exited', 0, 0, {}, []),
  ],
  'nas-prod': [
    makeContainer('4', 'media-indexer', 'ghcr.io/example/indexer:4.2', 'running', 7.4, 534_773_760, { 'com.docker.compose.project': 'media-stack', 'com.docker.compose.service': 'indexer' }, []),
    makeContainer('5', 'minio', 'minio/minio:RELEASE.2026-08-13', 'running', 3.9, 427_819_008, { 'com.docker.compose.project': 'storage-stack', 'com.docker.compose.service': 'minio' }, [{ private_port: 9000, public_port: 9000, type: 'tcp', ip: '0.0.0.0' }]),
    makeContainer('6', 'backup-index', 'postgres:17-alpine', 'running', 1.7, 293_601_280, { 'com.docker.compose.project': 'storage-stack', 'com.docker.compose.service': 'database' }, [{ private_port: 5432, type: 'tcp' }]),
    makeContainer('7', 'transcoder', 'ghcr.io/example/transcoder:6.1', 'exited', 0, 0, { 'com.docker.compose.project': 'media-stack', 'com.docker.compose.service': 'transcoder' }, []),
  ],
}

const mockMonitoring = createMonitoringMock(() => Object.entries(containerSets).flatMap(([nodeID, rows], nodeIndex) => rows.map(row => ({ id: String(nodeIndex+1).padStart(4,'0')+row.id.slice(4), node_id: nodeID, engine_id: nodes.find(n=>n.id===nodeID)?.engine_id ?? ('demo-engine-'+nodeID), container_id: row.id, node_name: nodes.find(n=>n.id===nodeID)?.name ?? nodeID, name: row.name, image: row.image, project: row.labels['com.docker.compose.project'] ?? '', service: row.labels['com.docker.compose.service'] ?? '', first_seen: new Date(Date.now()-3*86400_000).toISOString(), last_seen: new Date().toISOString() } satisfies Instance))))
const projectCapabilities: ProjectSummary['capabilities'] = ['view', 'edit', 'deploy', 'stop', 'restart', 'update', 'delete', 'services', 'logs']
const projects: ProjectSummary[] = [
  { ref: { backend: 'compose', scope: { kind: 'engine', id: 'local' }, native_name: 'gateway-prod' }, backend: 'compose', scope: { kind: 'engine', id: 'local' }, node_id: 'local', name: 'gateway-prod', native_name: 'gateway-prod', managed: true, source: 'managed', capabilities: projectCapabilities, service_count: 2, instance_count: 2, status: 'running', created_at: earlier, updated_at: now },
  { ref: { backend: 'compose', scope: { kind: 'engine', id: 'local' }, native_name: 'media-stack' }, backend: 'compose', scope: { kind: 'engine', id: 'local' }, node_id: 'local', name: 'media-stack', native_name: 'media-stack', managed: true, source: 'managed', capabilities: projectCapabilities, service_count: 3, instance_count: 3, status: 'running', created_at: earlier, updated_at: now },
  { ref: { backend: 'compose', scope: { kind: 'engine', id: 'local' }, native_name: 'legacy-tools' }, backend: 'compose', scope: { kind: 'engine', id: 'local' }, node_id: 'local', name: 'legacy-tools', native_name: 'legacy-tools', managed: false, source: 'external', capabilities: ['view', 'services', 'logs', 'takeover', 'cleanup'], service_count: 1, instance_count: 1, status: 'stopped', created_at: earlier, updated_at: now },
]

for (const project of projects) if (project.managed) project.declared_service_names = [...new Set(containerSets.local.filter(row => row.labels['com.docker.compose.project'] === project.name).map(row => row.labels['com.docker.compose.service']).filter(Boolean))]

const projectConfigurations = new Map<string, Project>()
const projectCDConfigurations = new Map<string,ProjectCDConfiguration>()
const projectCDRecords = new Map<string,ProjectCDRecord[]>()
const projectCDValues = new Map<string,Record<string,string>>()
let projectRevision = 0
const projectDetail = (row: ProjectSummary): Project => ({ ...row, path: `/srv/compose/${row.name}`, can_manage: row.managed, config_files: ['compose.yml'], services: row.service_count, containers: row.instance_count, compose: 'services:\n' + (row.declared_service_names ?? ['app']).map(name => `  ${name}:\n    image: ghcr.io/example/${row.name}:latest\n    restart: unless-stopped\n`).join(''), environment: 'APP_ENV=production\n', metadata: { origin: row.managed ? 'created' : 'takeover', claimed_at: earlier, last_deployed_at: now } })

const images = [
  { id: `sha256:${'1'.repeat(64)}`, tags: ['ghcr.io/afteryuwei/gateway:1.8.2'], digests: ['sha256:7e992d8a'], size: 186_646_528, created: earlier, containers: 1, architecture: 'amd64', os: 'linux', author: 'SUMA Demo', docker_version: '28.3.3', layers: ['sha256:base', 'sha256:app'] },
  { id: `sha256:${'2'.repeat(64)}`, tags: ['postgres:17-alpine'], digests: ['sha256:39b2c10e'], size: 274_726_912, created: earlier, containers: 2, architecture: 'amd64', os: 'linux', author: 'PostgreSQL Global Development Group', docker_version: '28.3.3', layers: ['sha256:alpine', 'sha256:postgres'] },
  { id: `sha256:${'3'.repeat(64)}`, tags: ['redis:8-alpine'], digests: ['sha256:81f11d42'], size: 58_720_256, created: earlier, containers: 1, architecture: 'amd64', os: 'linux', author: 'Redis', docker_version: '28.3.3', layers: ['sha256:alpine', 'sha256:redis'] },
  { id: `sha256:${'4'.repeat(64)}`, tags: ['caddy:2.10-alpine'], digests: ['sha256:555f73aa'], size: 72_351_744, created: earlier, containers: 0, architecture: 'amd64', os: 'linux', author: 'Caddy', docker_version: '28.3.3', layers: ['sha256:alpine', 'sha256:caddy'] },
]

const networks = [
  { id: 'network-gateway', name: 'gateway-prod_default', driver: 'bridge', scope: 'local', ipv6: false, internal: false, ipam: [{ subnet: '172.22.0.0/16', gateway: '172.22.0.1' }], containers: 2, attached_containers: [{ id: containerSets.local[0].id, name: 'gateway', ipv4_address: '172.22.0.3/16', ipv6_address: '' }, { id: containerSets.local[1].id, name: 'gateway-db', ipv4_address: '172.22.0.2/16', ipv6_address: '' }] },
  { id: 'network-media', name: 'media-stack_default', driver: 'bridge', scope: 'local', ipv6: false, internal: false, ipam: [{ subnet: '172.23.0.0/16', gateway: '172.23.0.1' }], containers: 2, attached_containers: [] },
  { id: 'network-bridge', name: 'bridge', driver: 'bridge', scope: 'local', ipv6: false, internal: false, ipam: [{ subnet: '172.17.0.0/16', gateway: '172.17.0.1' }], containers: 0, attached_containers: [] },
]

const volumes = [
  { name: 'gateway-prod_db-data', driver: 'local', mountpoint: '/var/lib/docker/volumes/gateway-prod_db-data/_data', created_at: earlier, used_by: [containerSets.local[1].id], size: 1_876_541_440 },
  { name: 'media-stack_library', driver: 'local', mountpoint: '/var/lib/docker/volumes/media-stack_library/_data', created_at: earlier, used_by: [containerSets.local[2].id], size: 6_442_450_944 },
  { name: 'old-cache', driver: 'local', mountpoint: '/var/lib/docker/volumes/old-cache/_data', created_at: earlier, used_by: [], size: 94_371_840 },
]

const tasks: TaskRecord[] = [
  { id: 'task-release-184', scope: 'control_plane', type: 'delivery', name: 'Deploy gateway-prod release #184', status: 'success', progress: 100, message: 'Health gate passed on 2 nodes', created_at: '2026-08-29T08:14:00.000Z' },
  { id: 'task-pull-183', scope: 'node', node_id: 'local', node_name: 'homelab-01', type: 'image_pull', name: 'Pull ghcr.io/afteryuwei/gateway:1.8.2', status: 'success', progress: 100, message: 'Image pulled', created_at: '2026-08-29T07:52:00.000Z' },
  { id: 'task-prune-182', scope: 'node', node_id: 'local', node_name: 'homelab-01', type: 'system_prune', name: 'Docker system prune', status: 'success', progress: 100, message: 'Reclaimed 768 MB', created_at: '2026-08-28T22:30:00.000Z' },
]
const createdContainerIDs = new Map<string, string>()
const createdContainerDetails = new Map<string, ContainerDetail>()

const audits = [
  { id: 18, scope: 'node', node_id: 'local', node_name: 'homelab-01', user_id: 1, action: 'container.restart', resource_type: 'container', resource_name: containerSets.local[0].id, ip: '192.168.1.36', result: 'success', created_at: '2026-08-29T08:25:00.000Z' },
  { id: 17, scope: 'node', node_id: 'local', node_name: 'homelab-01', user_id: 1, action: 'project.deploy', resource_type: 'project', resource_name: 'gateway-prod', ip: '192.168.1.36', result: 'success', task_id: 'task-release-184', created_at: '2026-08-29T08:14:00.000Z' },
  { id: 16, scope: 'control_plane', user_id: 1, action: 'node.test', resource_type: 'node', resource_name: 'edge-hk', ip: '192.168.1.36', result: 'success', created_at: '2026-08-29T07:58:00.000Z' },
  { id: 15, scope: 'node', node_id: 'local', node_name: 'homelab-01', user_id: 1, action: 'image.pull', resource_type: 'image', resource_name: 'ghcr.io/afteryuwei/gateway:1.8.2', ip: '192.168.1.36', result: 'success', task_id: 'task-pull-183', created_at: '2026-08-29T07:52:00.000Z' },
]

const deliveryProjects: DeliveryProject[] = [
  { id: 1, name: 'gateway-prod', configured: true, automation_enabled: true, repository_url: 'https://github.com/example/gateway.git', git_ref: 'main', desired_commit: '7a31f0c87aa1', observed_commit: '7a31f0c87aa1', active_release_id: 184, created_at: earlier, updated_at: now, node_ids: ['local', 'edge-hk'] },
  { id: 2, name: 'media-stack', configured: true, automation_enabled: true, repository_url: 'https://github.com/example/media-stack.git', git_ref: 'stable', desired_commit: 'f4c9a82e10d2', observed_commit: 'f4c9a82e10d2', active_release_id: 181, created_at: earlier, updated_at: now, node_ids: ['local', 'nas-prod'] },
]

const deliveryConfigurations = new Map<string, CDConfiguration>()
const deliveryRecords = new Map<string, DeliveryRecord[]>()
const deliveryEnvironments = new Map<string, Record<string, string>>()
const cdConfiguration = (name: string): CDConfiguration => ({ configured: true, automation_enabled: true, repository: { clone_url: `https://github.com/example/${name}.git`, ref_type: 'branch', ref: name === 'gateway-prod' ? 'main' : 'stable', authentication: { source: 'center', credential_id: 1, summary: { name: 'GitHub Demo', auth_type: 'http_token', username: 'suma-demo' } }, compose_files: ['compose.yml'], environment_file: '.env' }, reconcile_mode: name === 'gateway-prod' ? 'auto' : 'manual', sync_interval_seconds: 300, desired_commit: name === 'gateway-prod' ? '7a31f0c87aa1' : 'f4c9a82e10d2', observed_commit: name === 'gateway-prod' ? '7a31f0c87aa1' : 'f4c9a82e10d2', active_release_id: name === 'gateway-prod' ? 184 : 181, auto_rollback: true, deployment_timeout: 180, webhook_enabled: true, webhook_id: `demo-${name}`, webhook_secret: '', node_ids: name === 'gateway-prod' ? ['local', 'edge-hk'] : ['local', 'nas-prod'], registry_credential_ids: [1] })

const releases: DeliveryRelease[] = [
  { id: 184, project_id: 1, repository_url: 'https://github.com/example/gateway.git', git_ref: 'main', commit_sha: '7a31f0c87aa1', commit_message: 'feat: expose fleet overview', commit_author: 'Demo Maintainer', config_hash: 'cfg-184', image_references: JSON.stringify(['ghcr.io/afteryuwei/gateway:1.8.2']), task_id: 'task-release-184', status: 'succeeded', trigger_type: 'webhook', trigger_actor: 'github', previous_release_id: 183, compose_files: JSON.stringify(['compose.yml']), approved_by: 1, approved_by_name: user.username, approved_at: earlier, started_at: earlier, finished_at: now, health_summary: '2/2 targets healthy', created_at: earlier, updated_at: now, remediation: { retry_failed_node_ids: [], rollback_failed_node_ids: [] }, deployments: [{ id: 1, node_id: 'local', node_name: 'homelab-01', status: 'succeeded', health_summary: 'healthy', started_at: earlier, finished_at: now, attempts: [{ id: 1, deployment_id: 1, operation: 'deploy', target_release_id: 184, status: 'succeeded', health_summary: 'healthy', started_at: earlier, finished_at: now, created_at: earlier }] }, { id: 2, node_id: 'edge-hk', node_name: 'edge-hk', status: 'succeeded', health_summary: 'healthy', started_at: earlier, finished_at: now }] },
  { id: 183, project_id: 1, repository_url: 'https://github.com/example/gateway.git', git_ref: 'main', commit_sha: 'a81d3e90d4c2', commit_message: 'fix: retain proxy headers', commit_author: 'Demo Maintainer', config_hash: 'cfg-183', image_references: JSON.stringify(['ghcr.io/afteryuwei/gateway:1.8.1']), status: 'succeeded', trigger_type: 'manual', trigger_actor: 'admin', compose_files: JSON.stringify(['compose.yml']), started_at: earlier, finished_at: earlier, health_summary: '2/2 targets healthy', created_at: earlier, updated_at: earlier, remediation: { retry_failed_node_ids: [], rollback_failed_node_ids: [] } },
  { id: 181, project_id: 2, repository_url: 'https://github.com/example/media-stack.git', git_ref: 'stable', commit_sha: 'f4c9a82e10d2', commit_message: 'Demo media deployment', commit_author: 'Demo Maintainer', config_hash: 'cfg-181', image_references: JSON.stringify(['ghcr.io/immich-app/immich-server:v1.138.0']), status: 'succeeded', trigger_type: 'manual', trigger_actor: 'admin', compose_files: JSON.stringify(['compose.yml']), started_at: earlier, finished_at: earlier, health_summary: 'Demo media services healthy', created_at: earlier, updated_at: earlier, remediation: { retry_failed_node_ids: [], rollback_failed_node_ids: [] } },
]

const gitCredentials: GitCredential[] = [{ id: 1, name: 'GitHub Demo', auth_type: 'http_token', username: 'suma-demo', fingerprint: 'sha256:3c92…7ab1', created_at: earlier, updated_at: now, last_used_at: now, used_by: 2, authorized_node_ids: ['local', 'edge-hk', 'nas-prod'] }]
const registryCredentials = [{ id: 1, name: 'GHCR Demo', server_address: 'ghcr.io', auth_type: 'token', username: 'suma-demo', fingerprint: 'sha256:8d14…2f09', created_at: earlier, updated_at: now, last_used_at: now, authorized_node_ids: ['local', 'edge-hk', 'nas-prod'] }]
const tlsCredentials = [{ id: 1, name: 'Edge Docker mTLS', fingerprint: 'SHA256:91:42:7A:DE:MO', authorized_node_ids: ['edge-hk'], created_at: earlier, updated_at: now }]

const settings = { 'general.server_name': 'SUMA Demo', 'general.timezone': sessionStorage.getItem('suma-demo-timezone') || 'system', 'docker.compose_command': 'docker compose', 'storage.compose_root': '/data/compose', 'storage.data_root': '/data', 'storage.backup_root': '/data/backups', 'security.browser_origin': '', 'security.trusted_proxies': '', 'registry.default': 'ghcr.io' }

const clone = <T,>(value: T): T => structuredClone(value)
const parseBody = (init?: RequestInit): Record<string, unknown> => {
  if (typeof init?.body !== 'string') return {}
  try { return JSON.parse(init.body) as Record<string, unknown> } catch { return {} }
}
const authenticated = () => typeof sessionStorage !== 'undefined' && sessionStorage.getItem(sessionKey) === '1'
const requireSession = () => { if (!authenticated()) throw new ApiError('请先登录演示环境', 40101, 401) }
const wait = () => new Promise((resolve) => setTimeout(resolve, 90))
const nodeContainers = (id: string) => containerSets[id] ?? containerSets.local

function completedDemoTask(input: Pick<TaskRecord, 'type' | 'name' | 'node_id' | 'params' | 'result' | 'parent_task_id'>): TaskRecord {
  const at = new Date().toISOString()
  const task: TaskRecord = { ...input, id: crypto.randomUUID(), scope: input.node_id ? 'node' : 'control_plane', node_name: nodes.find(node => node.id === input.node_id)?.name, status: 'success', progress: 100, message: 'Demo operation completed', created_at: at, started_at: at, finished_at: at }
  tasks.unshift(task)
  return task
}

function taskReference(task: TaskRecord | undefined, field: 'record_id' | 'release_id'): number | undefined {
  for (const value of [task?.result, task?.params]) {
    if (!value || typeof value !== 'object') continue
    if (field === 'record_id' && 'record_id' in value && typeof value.record_id === 'number') return value.record_id
    if (field === 'release_id' && 'release_id' in value && typeof value.release_id === 'number') return value.release_id
  }
}

function matchesTaskEvidence(row: DeliveryHistoryRecord, taskID: string) {
  if (row.task_id === taskID) return true
  const task = tasks.find(task => task.id === taskID)
  const source = task?.type.startsWith('project.cd.') ? 'project_cd' : task?.type.startsWith('cd.') ? 'continuous_delivery' : undefined
  return row.source === source && row.record_id === taskReference(task, 'record_id')
}

const taskCenter = createMockTaskCenter({
  nodes, tasks,
  validateImage(nodeID, name, params) {
    const project = projects.find(project => project.name === name)
    if (!project || params.auto_apply === true && !project.managed) throw new ApiError('A managed project is required for updates', 22007, 400)
    if (Object.keys(params).some(key => !['project', 'services', 'auto_apply', 'allow_docker_socket'].includes(key)) || params.auto_apply !== undefined && typeof params.auto_apply !== 'boolean' || params.allow_docker_socket !== undefined && typeof params.allow_docker_socket !== 'boolean') throw new ApiError('Invalid image update parameters', 22007, 400)
    if (params.services !== undefined && (!Array.isArray(params.services) || params.services.some(value => typeof value !== 'string' || !/^[a-zA-Z0-9_-]{1,128}$/.test(value)) || new Set(params.services).size !== params.services.length)) throw new ApiError('Invalid service selection', 22007, 400)
    const configuration = projectConfigurations.get(`${nodeID}/${name}`) ?? projectDetail(project)
    if (params.auto_apply && configuration.compose.includes('/var/run/docker.sock') && params.allow_docker_socket !== true) throw new ApiError('Docker socket mount requires explicit confirmation', 22007, 400)
  },
  change(next, input) {
    if (next.kind === 'cd.sync') {
      const configuration = deliveryConfigurations.get(next.subject), project = deliveryProjects.find(project => project.name === next.subject)
      if (!configuration || !project) throw new ApiError('Delivery project not found', 22005, 404)
      Object.assign(configuration, { automation_enabled: next.enabled, sync_interval_seconds: next.interval_seconds, automation_version: next.version })
      Object.assign(project, { automation_enabled: next.enabled, automation_version: next.version })
    } else if (next.kind === 'project.cd.check') {
      const configuration = projectCDConfigurations.get(`${next.node_id}/${next.subject}`)
      if (!configuration?.enabled) throw new ApiError('Project CD is not configured', 22005, 404)
      Object.assign(configuration, { automation_enabled: next.enabled, sync_interval_seconds: next.interval_seconds, revision: configuration.revision + 1 })
    } else if (next.kind === 'system.cleanup' && next.node_id) {
      const policy = mockCleanup.policy(next.node_id), parts = next.cron.split(/\s+/)
      if (parts.length !== 5 || parts[2] !== '*' || parts[3] !== '*' || !/^\d+$/.test(parts[0]) || !/^\d+$/.test(parts[1]) || parts[4] !== '*' && !/^[0-6]$/.test(parts[4])) throw new ApiError('Cleanup supports daily or weekly schedules', 22001, 400)
      mockCleanup.savePolicy(next.node_id, { ...cleanupConfiguration(policy), version: policy.version, enabled: next.enabled && next.timer_enabled, schedule: { frequency: parts[4] === '*' ? 'daily' : 'weekly', weekday: parts[4] === '*' ? 0 : Number(parts[4]), hour: Number(parts[1]), minute: Number(parts[0]), timezone: next.timezone }, confirmation_name: input.confirmation_name, authorize: input.authorize })
    }
  },
  run(kind, nodeID, params, taskID) {
    const name = String(params.project)
    if (kind === 'image.update' && nodeID) {
      const selected = Array.isArray(params.services) ? params.services : []
      const projectCD = projectCDConfigurations.get(`${nodeID}/${name}`)
      const services = nodeContainers(nodeID).filter(row => row.labels['com.docker.compose.project'] === name && (!selected.length || selected.includes(row.labels['com.docker.compose.service']))).map(row => ({ service: row.labels['com.docker.compose.service'], reference: row.image, status: projectCD?.enabled ? 'blocked' : 'update_available', reason_code: projectCD?.enabled ? 'managed_by_cd' : undefined, replicas: 1, remote_manifest_digest: `sha256:${'d'.repeat(64)}` }))
      const updated = params.auto_apply ? services.filter(row => row.status === 'update_available').map(row => row.service) : []
      const childID = updated.length ? crypto.randomUUID() : undefined
      if (childID) tasks.unshift({ id: childID, parent_task_id: taskID, type: 'compose.update', name: `Recreate ${name}`, scope: 'node', node_id: nodeID, node_name: nodes.find(node => node.id === nodeID)?.name, status: 'success', progress: 100, message: 'Demo services recreated', params: { project: name }, result: { updated }, created_at: new Date().toISOString(), finished_at: new Date().toISOString() })
      return { project: name, mode: params.auto_apply ? 'auto' : 'check', services, updated, update_task_id: childID, finished_at: new Date().toISOString() }
    }
    if (kind === 'cd.sync') {
      const project = deliveryProjects.find(project => project.name === name), configuration = deliveryConfigurations.get(name)
      if (!project || !configuration?.configured) throw new ApiError('Delivery project not found', 22005, 404)
      const release: DeliveryRelease = { ...structuredClone(releases[0]), id: Math.max(...releases.map(row => row.id), 0) + 1, project_id: project.id, repository_url: configuration.repository.clone_url, git_ref: configuration.repository.ref, commit_sha: 'd'.repeat(40), commit_message: 'Demo candidate checked', task_id: taskID, status: 'awaiting_approval', latest_record_id: undefined, deployments: [], started_at: undefined, approved_at: undefined, approved_by: undefined, approved_by_name: undefined, health_summary: '', created_at: new Date().toISOString(), finished_at: undefined }
      releases.unshift(release)
      return { project: name, operation: 'check', release_id: release.id, trigger: 'manual' }
    }
    if (kind === 'project.cd.check' && nodeID) {
      const key = `${nodeID}/${name}`, configuration = projectCDConfigurations.get(key)
      if (!configuration?.enabled) throw new ApiError('Project CD is not configured', 22005, 404)
      const rows = projectCDRecords.get(key) ?? []
      const record: ProjectCDRecord = { id: Math.max(...rows.map(row => row.id), 0) + 1, operation: 'check', status: 'succeeded', task_id: taskID, actor: user.username, revision: configuration.revision, commit_sha: 'd'.repeat(40), commit_message: 'Demo Git configuration', config_hash: 'e'.repeat(64), created_at: new Date().toISOString(), failure_reason: '' }
      rows.unshift(record); projectCDRecords.set(key, rows); configuration.candidate = record
      return { project: name, operation: 'check', record_id: record.id, commit: record.commit_sha, config_hash: record.config_hash }
    }
    throw new ApiError('Unsupported task kind', 22002, 400)
  },
})

function syncDeliveryRule(name: string, configuration: CDConfiguration) {
  const rule = taskCenter.upsert({ name: `Sync ${name}`, kind: 'cd.sync', subject: name, params: { project: name }, cron: '', timezone: 'UTC', schedule_type: 'interval', interval_seconds: configuration.sync_interval_seconds, enabled: configuration.automation_enabled, webhook_enabled: configuration.webhook_enabled })
  configuration.automation_version = rule.version
  const project = deliveryProjects.find(project => project.name === name)
  if (project) project.automation_version = rule.version
}
function syncProjectRule(nodeID: string, name: string, configuration: ProjectCDConfiguration) {
  if (!configuration.enabled) { taskCenter.archive('project.cd.check', nodeID, name); return }
  taskCenter.upsert({ name: `Check ${name}`, kind: 'project.cd.check', node_id: nodeID, subject: name, params: { project: name }, cron: '', timezone: 'UTC', schedule_type: 'interval', interval_seconds: configuration.sync_interval_seconds, enabled: configuration.automation_enabled, webhook_enabled: configuration.webhook_enabled })
}
function syncCleanupRule(policy: CleanupPolicy) {
  taskCenter.upsert({ name: `Storage cleanup ${policy.node_id}`, kind: 'system.cleanup', node_id: policy.node_id, params: {}, cron: `${policy.schedule.minute} ${policy.schedule.hour} * * ${policy.schedule.frequency === 'weekly' ? policy.schedule.weekday : '*'}`, timezone: policy.schedule.timezone, schedule_type: 'cron', enabled: policy.enabled, timer_enabled: policy.enabled, next_run_at: policy.next_run_at })
}
for (const project of deliveryProjects) {
  const configuration = cdConfiguration(project.name)
  deliveryConfigurations.set(project.name, configuration); syncDeliveryRule(project.name, configuration)
}
taskCenter.upsert({ name: 'media-stack image check', kind: 'image.update', node_id: 'local', subject: 'media-stack', params: { project: 'media-stack', auto_apply: false }, cron: '0 4 * * *', timezone: 'UTC', schedule_type: 'cron', enabled: false })

function demoDeliveryHistory(): DeliveryHistoryRecord[] {
    const history: DeliveryHistoryRecord[] = []
    for (const [name, records] of deliveryRecords) for (const record of records) history.push({id:`continuous_delivery:${name}:${record.id}`,source:'continuous_delivery',record_id:record.id,project_id:record.project_id,project_name:name,release_id:record.release_id,node_exists:false,node_ids:record.node_ids,operation:record.operation,status:record.status,actor_name:record.actor_name,task_id:record.task_id||'',source_record_id:record.source_record_id,commit_sha:record.commit_sha,config_hash:record.config_hash,failure_reason:record.failure_reason||'',created_at:record.created_at,finished_at:record.finished_at})
    for (const [scope, records] of projectCDRecords) {
      const slash=scope.indexOf('/'),nodeID=scope.slice(0,slash),name=scope.slice(slash+1),node=nodes.find(row=>row.id===nodeID)
      for (const record of records) history.push({id:`project_cd:${scope}:${record.id}`,source:'project_cd',record_id:record.id,project_id:0,project_name:name,node_id:nodeID,node_name:node?.name||nodeID,node_exists:!!node,node_ids:[nodeID],operation:record.operation==='apply'?'deploy':record.operation==='rollback'?'manual_rollback':record.operation,status:record.status,actor_name:record.actor,task_id:record.task_id,commit_sha:record.commit_sha,config_hash:record.config_hash,failure_reason:record.failure_reason,created_at:record.created_at})
    }
    return history
}

export async function demoApi<T>(path: string, init?: RequestInit): Promise<T> {
  await wait()
  const method = (init?.method || 'GET').toUpperCase()
  const url = new URL(path, 'https://demo.suma.local')
  const pathname = decodeURIComponent(url.pathname)
  const body = parseBody(init)

  if (pathname === '/auth/status') return clone({ needs_setup: false }) as T
  if (pathname === '/auth/login' && method === 'POST') {
    if (body.username !== demoCredentials.username || body.password !== demoCredentials.password) throw new ApiError('用户名或密码错误', 40102, 401)
    if (user.two_factor_enabled) return clone({ requires_two_factor: true, challenge_token: 'demo-two-factor-challenge' }) as T
    sessionStorage.setItem(sessionKey, '1')
    return clone({ requires_two_factor: false, user }) as T
  }
  if (pathname === '/auth/two-factor' && method === 'POST') {
    if (body.challenge_token !== 'demo-two-factor-challenge' || body.code !== '123456') throw new ApiError('验证码无效或已过期', 11008, 401)
    sessionStorage.setItem(sessionKey, '1')
    return clone(user) as T
  }
  if (pathname === '/auth/logout' && method === 'POST') { sessionStorage.removeItem(sessionKey); return {} as T }
  if (pathname === '/auth/session') { requireSession(); return clone(user) as T }

  requireSession()
  const centerResult = await taskCenter.handle(pathname, method, body, url)
  if (centerResult !== undefined) return clone(centerResult) as T
  const monitoringResult = mockMonitoring(pathname, method, body, url)
  if (monitoringResult !== undefined) return monitoringResult as T
  const operationsResult = mockNotifications(pathname, method, body)
 if (operationsResult !== undefined) { persistMockNotifications(); return clone(operationsResult) as T }
 const cleanupResult = mockCleanup.handle(pathname, method, body, url)
  if (cleanupResult !== undefined) {
    const policyWrite = pathname.match(/^\/nodes\/([^/]+)\/cleanup\/policy$/)
    if (policyWrite && method === 'PUT') syncCleanupRule(mockCleanup.policy(policyWrite[1]))
    const runWrite = pathname.match(/^\/nodes\/([^/]+)\/cleanup\/run$/)
    if (runWrite && method === 'POST') {
      const run = mockCleanup.latestRun(runWrite[1])
      if (run?.task_id) {
        tasks.unshift({ id: run.task_id, scope: 'node', node_id: run.node_id, node_name: run.node_name, type: 'system.cleanup', name: 'Docker storage cleanup', status: run.status === 'success' ? 'success' : 'failed', progress: 100, message: run.message, params: { trigger: run.trigger, policy_version: run.policy_version }, result: { run_id: run.id, result: run.result }, created_at: run.created_at, finished_at: run.finished_at })
        taskCenter.record('system.cleanup', run.node_id, '', run.task_id)
      }
    }
    return clone(cleanupResult) as T
  }

  if (pathname === '/account/profile' && method === 'PUT') return clone({ ...user, ...body }) as T
  if (pathname === '/account/passkeys' && method === 'GET') return [] as T
  if (pathname === '/account/two-factor' && method === 'GET') return clone({ enabled: user.two_factor_enabled, recovery_codes_remaining: demoRecoveryCodesRemaining }) as T
  if (pathname === '/account/two-factor/setup' && method === 'POST') {
    if (body.current_password !== demoCredentials.password) throw new ApiError('当前密码无效', 11113, 403)
    return clone({ secret: 'JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP', otpauth_uri: 'otpauth://totp/SUMA:admin@suma.demo?secret=JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP&issuer=SUMA', qr_code_data_url: '' }) as T
  }
  if (pathname === '/account/two-factor/enable' && method === 'POST') {
    if (body.code !== '123456') throw new ApiError('验证码无效或已过期', 11115, 400)
    user.two_factor_enabled = true
    demoRecoveryCodesRemaining = 10
    return clone({ recovery_codes: ['DEMO-1111-AAAA', 'DEMO-2222-BBBB', 'DEMO-3333-CCCC', 'DEMO-4444-DDDD', 'DEMO-5555-EEEE', 'DEMO-6666-FFFF', 'DEMO-7777-GGGG', 'DEMO-8888-HHHH', 'DEMO-9999-JJJJ', 'DEMO-0000-KKKK'] }) as T
  }
  if (pathname === '/account/two-factor/recovery-codes' && method === 'POST') {
    if (body.current_password !== demoCredentials.password || body.code !== '123456') throw new ApiError('密码或验证码无效', 11117, 403)
    demoRecoveryCodesRemaining = 10
    return clone({ recovery_codes: ['NEW1-1111-AAAA', 'NEW2-2222-BBBB', 'NEW3-3333-CCCC', 'NEW4-4444-DDDD', 'NEW5-5555-EEEE', 'NEW6-6666-FFFF', 'NEW7-7777-GGGG', 'NEW8-8888-HHHH', 'NEW9-9999-JJJJ', 'NEW0-0000-KKKK'] }) as T
  }
  if (pathname === '/account/two-factor' && method === 'DELETE') {
    if (body.current_password !== demoCredentials.password || body.code !== '123456') throw new ApiError('密码或验证码无效', 11119, 403)
    user.two_factor_enabled = false
    demoRecoveryCodesRemaining = 0
    return {} as T
  }
  if (pathname.startsWith('/account/')) return clone(user) as T
  if (pathname === '/node-groups' && method === 'GET') return clone(nodeGroups) as T
  if (pathname === '/node-groups' && method === 'POST') {
    const group: NodeGroup = { id: Math.max(0, ...nodeGroups.map((item) => item.id)) + 1, name: String(body.name || ''), description: String(body.description || ''), is_default: false, node_count: 0, created_at: now, updated_at: now }
    nodeGroups.push(group)
    return clone(group) as T
  }
  const groupMatch = pathname.match(/^\/node-groups\/(\d+)$/)
  if (groupMatch) {
    const id = Number(groupMatch[1])
    const index = nodeGroups.findIndex((item) => item.id === id)
    if (index < 0) throw new ApiError('Node group not found', 20305, 404)
    if (method === 'PUT') {
      nodeGroups[index] = { ...nodeGroups[index], name: String(body.name || ''), description: String(body.description || ''), updated_at: now }
      return clone(nodeGroups[index]) as T
    }
    if (method === 'DELETE') {
      nodeGroups.splice(index, 1)
      nodes.forEach((node) => { node.group_ids = node.group_ids.filter((groupID) => groupID !== id) })
      syncNodeGroupCounts()
      return { id } as T
    }
    return clone(nodeGroups[index]) as T
  }
  if (pathname === '/nodes' && method === 'GET') return clone(nodes) as T
  const agentEngineReplacementMatch = pathname.match(/^\/nodes\/([^/]+)\/agent\/replace-engine$/)
  if (agentEngineReplacementMatch && method === 'POST') {
    const node = nodes.find((item) => item.id === agentEngineReplacementMatch[1])
    if (!node) throw new ApiError('Docker node not found', 20504, 404)
    if (node.connection_type !== 'agent' || !node.engine_id || body.confirmation !== node.name || body.previous_engine_id !== node.engine_id || typeof body.engine_id !== 'string' || !/^[A-Za-z0-9][A-Za-z0-9:._-]{0,127}$/.test(body.engine_id) || body.engine_id === node.engine_id) throw new ApiError('Confirm the node name and a different Docker Engine ID', 20514, 422)
    if (nodes.some((item) => item.id !== node.id && item.engine_id === body.engine_id)) throw new ApiError('Docker Engine is already registered', 20514, 422)
    const expiresAt = new Date(Date.now() + 10 * 60_000).toISOString()
    node.engine_id = body.engine_id
    node.engine_version = undefined
    node.agent_version = undefined
    node.agent_connected_at = undefined
    node.last_checked_at = undefined
    node.last_latency_ms = 0
    node.status = 'offline'
    node.last_error = ''
    node.updated_at = new Date().toISOString()
    node.agent_enrollment = { node_id: node.id, expires_at: expiresAt }
    return clone({ node_id: node.id, token: 'c'.repeat(64), expires_at: expiresAt, public_url: 'https://suma.example.com' }) as T
  }
  if (pathname === '/agent-enrollments' && method === 'POST') {
    const expiresAt = new Date(Date.now() + 10 * 60_000).toISOString()
    const existing = nodes.find((item) => item.id === body.node_id)
    const id = existing?.id ?? `${String(body.name || 'agent').toLowerCase().replace(/[^a-z0-9]+/g, '-')}-${nodes.length + 1}`
    if (existing) existing.agent_enrollment = { node_id: id, expires_at: expiresAt }
    else nodes.push({ id, name: String(body.name || id), connection_type: 'agent', endpoint: `agent://${id}`, tls_mode: 'disabled', enabled: false, status: 'pairing', created_at: now, updated_at: now, group_ids: Array.isArray(body.group_ids) ? body.group_ids.filter((value): value is number => typeof value === 'number') : [], agent_enrollment: { node_id: id, expires_at: expiresAt } })
    syncNodeGroupCounts()
    return clone({ node_id: id, token: 'a'.repeat(64), expires_at: expiresAt, public_url: 'https://suma.example.com' }) as T
  }
  const agentEnrollmentMatch = pathname.match(/^\/agent-enrollments\/([^/]+)(\/reissue)?$/)
  if (agentEnrollmentMatch && method === 'POST' && agentEnrollmentMatch[2]) {
    const node = nodes.find((item) => item.id === agentEnrollmentMatch[1])
    if (!node) throw new ApiError('Docker node not found', 20004, 404)
    const expiresAt = new Date(Date.now() + 10 * 60_000).toISOString()
    node.agent_enrollment = { node_id: node.id, expires_at: expiresAt }
    if (node.connection_type === 'agent') { node.status = 'offline'; node.last_error = 'Agent disconnected' }
    return clone({ node_id: node.id, token: 'b'.repeat(64), expires_at: expiresAt, public_url: 'https://suma.example.com' }) as T
  }
  const agentRevokeMatch = pathname.match(/^\/nodes\/([^/]+)\/agent\/revoke$/)
  if (agentRevokeMatch && method === 'POST') {
    const node = nodes.find((item) => item.id === agentRevokeMatch[1])
    if (!node) throw new ApiError('Docker node not found', 20004, 404)
    node.status = 'offline'
    node.last_error = 'Agent credential revoked'
    return clone({ node_id: node.id }) as T
  }
  if (pathname === '/nodes' && method === 'POST') {
    const id = `${String(body.name || 'node').toLowerCase().replace(/[^a-z0-9]+/g, '-')}-${nodes.length + 1}`
    const node: DockerNode = { id, name: String(body.name || id), connection_type: body.connection_type === 'tcp' ? 'tcp' : 'unix', endpoint: String(body.endpoint || 'unix:///var/run/docker.sock'), tls_mode: body.tls_mode === 'required' ? 'required' : 'disabled', tls_credential_id: typeof body.tls_credential_id === 'number' ? body.tls_credential_id : undefined, enabled: body.enabled !== false, status: 'online', last_latency_ms: 18, last_checked_at: now, created_at: now, updated_at: now, group_ids: Array.isArray(body.group_ids) ? body.group_ids.filter((id): id is number => typeof id === 'number') : [] }
    nodes.push(node)
    syncNodeGroupCounts()
    return clone(node) as T
  }
  const nodeRecordMatch = pathname.match(/^\/nodes\/([^/]+)(\/test)?$/)
  if (nodeRecordMatch) {
    const index = nodes.findIndex((item) => item.id === nodeRecordMatch[1])
    if (index < 0) throw new ApiError('Docker node not found', 20004, 404)
    if (nodeRecordMatch[2]) return clone(nodes[index]) as T
    if (method === 'PUT') {
      nodes[index] = { ...nodes[index], ...body, id: nodes[index].id, group_ids: Array.isArray(body.group_ids) ? body.group_ids.filter((id): id is number => typeof id === 'number') : nodes[index].group_ids, updated_at: now } as DockerNode
      syncNodeGroupCounts()
      return clone(nodes[index]) as T
    }
    if (method === 'DELETE') {
      const [removed] = nodes.splice(index, 1)
      taskCenter.archive(undefined, removed.id)
      syncNodeGroupCounts()
      return { id: removed.id } as T
    }
    return clone(nodes[index]) as T
  }

  if (pathname === '/fleet/overview') {
    const refreshTick = fleetOverviewTick++
    const capacity: Record<string, { cpus: number; memory: number }> = {
      local: { cpus: 8, memory: 16 * 1024 ** 3 },
      'edge-hk': { cpus: 4, memory: 8 * 1024 ** 3 },
      'nas-prod': { cpus: 12, memory: 32 * 1024 ** 3 },
    }
    const groupID = Number(url.searchParams.get('group_id'))
    const fleetSource = groupID > 0 ? nodes.filter((node) => node.group_ids.includes(groupID)) : nodes
    const fleetNodes = fleetSource.map((node, index) => {
      const containers = nodeContainers(node.id)
      const metricContainers = containers
        .filter((item) => item.state === 'running')
        .map((item, containerIndex) => {
          const sample = refreshTick + index + containerIndex
          const cpuScale = 0.85 + sample % 6 * 0.06
          const memoryScale = 0.99 + sample % 5 * 0.005
          return {
            id: item.id,
            name: item.name,
            image: item.image,
            state: item.state,
            available: true,
            cpu_percent: item.cpu_percent * cpuScale,
            memory_bytes: Math.round(item.memory_bytes * memoryScale),
            network_rx_bytes: (containerIndex + 1) * 768 * 1024 ** 2 + refreshTick * (containerIndex + 1) * 8 * 1024 ** 2,
            network_tx_bytes: (containerIndex + 1) * 256 * 1024 ** 2 + refreshTick * (containerIndex + 1) * 3 * 1024 ** 2,
            block_read_bytes: (containerIndex + 1) * 512 * 1024 ** 2 + refreshTick * (containerIndex + 1) * 2 * 1024 ** 2,
            block_write_bytes: (containerIndex + 1) * 192 * 1024 ** 2 + refreshTick * (containerIndex + 1) * 1024 ** 2,
            uptime_seconds: item.uptime_seconds + refreshTick * fleetOverviewRefreshSeconds,
          }
        })
      return {
        ...node,
        last_checked_at: new Date().toISOString(),
        hostname: node.name,
        os: 'Ubuntu 24.04 LTS',
        os_version: '24.04',
        architecture: 'x86_64',
        kernel_version: '6.8.0-71-generic',
        cpus: capacity[node.id]?.cpus ?? 4,
        memory_total_bytes: capacity[node.id]?.memory ?? 8 * 1024 ** 3,
        containers_running: containers.filter((item) => item.state === 'running').length,
        containers_paused: 0,
        containers_stopped: containers.filter((item) => item.state !== 'running').length,
        images: images.length,
        networks: 5 + index,
        volumes: 8 + index * 3,
        docker_disk_usage_bytes: (18 + index * 7) * 1024 ** 3,
        storage_driver: 'overlay2',
        logging_driver: 'json-file',
        cgroup_driver: 'systemd',
        cgroup_version: '2',
        default_runtime: 'runc',
        live_restore: index !== 1,
        security_options: ['name=apparmor', 'name=seccomp,profile=builtin'],
        metrics_available: true,
        container_cpu_percent: metricContainers.reduce((sum, item) => sum + item.cpu_percent, 0),
        container_memory_bytes: metricContainers.reduce((sum, item) => sum + item.memory_bytes, 0),
        container_network_rx_bytes: metricContainers.reduce((sum, item) => sum + item.network_rx_bytes, 0),
        container_network_tx_bytes: metricContainers.reduce((sum, item) => sum + item.network_tx_bytes, 0),
        container_block_read_bytes: metricContainers.reduce((sum, item) => sum + item.block_read_bytes, 0),
        container_block_write_bytes: metricContainers.reduce((sum, item) => sum + item.block_write_bytes, 0),
        longest_container_uptime_seconds: metricContainers.reduce((longest, item) => Math.max(longest, item.uptime_seconds), 0),
        containers: metricContainers.sort((left, right) => right.memory_bytes - left.memory_bytes),
      }
    })
    return clone({ nodes: fleetNodes }) as T
  }
  if (pathname === '/cd/overview') return clone({ projects: deliveryProjects.map((project) => ({ name: project.name, configured: project.configured, automation_enabled: project.automation_enabled, repository_url: project.repository_url, git_ref: project.git_ref, reconcile_mode: project.name === 'gateway-prod' ? 'auto' : 'manual', node_ids: project.node_ids, drifted: false, runtime_healthy: true, active_release: { id: project.active_release_id, status: 'succeeded', commit_sha: project.desired_commit, trigger_type: 'webhook', created_at: now }, latest_release: { id: project.active_release_id, status: 'succeeded', commit_sha: project.desired_commit, trigger_type: 'webhook', created_at: now }, awaiting_approval: false, releasing: false })), totals: { projects: 2, configured: 2, releasing: 0, awaiting_approval: 0, drifted: 0, healthy: 2 } }) as T

  if (pathname === '/tasks') {
    const scope = url.searchParams.get('scope') || 'control_plane'
    const rows = scope === 'all' ? tasks : tasks.filter((item) => item.scope === scope)
    return clone(url.searchParams.get('view') === 'summary' ? rows.map(({ params: _params, result: _result, ...row }) => row) : rows) as T
  }
  if (/^\/tasks\/[^/]+\/logs$/.test(pathname)) return clone([{ id: 1, level: 'info', message: 'Task accepted by SUMA task service', created_at: earlier }, { id: 2, level: 'info', message: 'Docker operation completed successfully', created_at: now }]) as T
  if (/^\/tasks\/[^/]+\/steps$/.test(pathname)) return clone([{ id: 'download', status: 'success', current: 1, total: 1, progress: 100 }, { id: 'extract', status: 'success', current: 1, total: 1, progress: 100 }]) as T
  if (/^\/tasks\/[^/]+\/cancel$/.test(pathname)) return {} as T
  if (pathname === '/audit-logs') {
    const scope = url.searchParams.get('scope') || 'all'
    const entries = audits
    return clone(scope === 'all' ? entries : entries.filter((item) => item.scope === scope)) as T
  }

  if (pathname === '/credentials/git' && method === 'GET') return clone(gitCredentials) as T
  if (pathname === '/credentials/registries' && method === 'GET') return clone(registryCredentials) as T
  if (pathname === '/credentials/docker-tls' && method === 'GET') return clone(tlsCredentials) as T
  if (pathname.startsWith('/credentials/git')) return clone(gitCredentials[0]) as T
  if (pathname.startsWith('/credentials/registries')) return clone(registryCredentials[0]) as T
  if (pathname.startsWith('/credentials/docker-tls')) return clone(tlsCredentials[0]) as T
  if (pathname === '/settings' && method === 'GET') return clone(settings) as T
  if (pathname === '/settings' && method === 'PUT') {
    if ('general.timezone' in body && (typeof body['general.timezone'] !== 'string' || (body['general.timezone'] !== 'system' && !validTimeZone(body['general.timezone'])))) throw new ApiError('Invalid timezone', 400, 400)
    for (const [key, value] of Object.entries(body)) {
      if (key in settings && typeof value === 'string') (settings as Record<string, string>)[key] = value
    }
    sessionStorage.setItem('suma-demo-timezone', settings['general.timezone'])
    return clone(settings) as T
  }

  const taskEvidence = pathname.match(/^\/delivery-records\/by-task\/([^/]+)$/)
  if (taskEvidence) {
    const task = tasks.find(task => task.id === taskEvidence[1])
    const releaseID = task?.type.startsWith('cd.') ? taskReference(task, 'release_id') : undefined
    const history = demoDeliveryHistory().filter(row => matchesTaskEvidence(row, taskEvidence[1]))
    return clone({ records: { items: history.slice(0, 20), total: history.length, page: 1, page_size: 20 }, releases: releases.filter(row => row.task_id === taskEvidence[1] || row.id === releaseID).map(row => ({ id: row.id, project_id: row.project_id, project_name: deliveryProjects.find(project => project.id === row.project_id)?.name ?? '', status: row.status, commit_sha: row.commit_sha, commit_message: row.commit_message, created_at: row.created_at })) }) as T
  }
  if (pathname === '/delivery-records' && method === 'GET') {
    const history = demoDeliveryHistory()
    const search=(url.searchParams.get('search')||'').toLowerCase()
    const filtered=history.filter(row=>(!url.searchParams.has('task_id')||matchesTaskEvidence(row,url.searchParams.get('task_id')!))&&(!url.searchParams.has('source')||row.source===url.searchParams.get('source'))&&(!url.searchParams.has('operation')||row.operation===url.searchParams.get('operation'))&&(!url.searchParams.has('status')||row.status===url.searchParams.get('status'))&&(!search||[row.project_name,row.node_name||'',row.node_id||''].some(value=>value.toLowerCase().includes(search)))).sort((a,b)=>b.created_at.localeCompare(a.created_at)||a.id.localeCompare(b.id))
    const page=Number(url.searchParams.get('page')||1),pageSize=Number(url.searchParams.get('page_size')||20)
    return clone({items:filtered.slice((page-1)*pageSize,page*pageSize),total:filtered.length,page,page_size:pageSize}) as T
  }
  if (pathname === '/delivery-projects' && method === 'GET') return clone(deliveryProjects) as T
  if (pathname === '/delivery-projects' && method === 'POST') {
    const name = String(body.name || '')
    if (!/^[a-z0-9][a-z0-9_-]{0,127}$/.test(name)) throw new ApiError('Invalid delivery project name', 17713, 409)
    if (deliveryProjects.some(project => project.name === name)) throw new ApiError('Delivery project already exists', 17713, 409)
    const input = body.configuration as CDConfigureInput | undefined
    const targetIDs = input?.node_ids || body.node_ids as string[] || ['local']
    if (!targetIDs.length || targetIDs.some(id => !nodes.some(node => node.id === id && node.enabled))) throw new ApiError('Select enabled target nodes', 17713, 409)
    if (input && (!input.repository?.clone_url || !input.repository.compose_files?.length || input.repository.compose_files.some(file => !file || file.startsWith('/') || file.split('/').includes('..')))) throw new ApiError('Invalid repository or Compose file path', 17713, 409)
    let configuration: CDConfiguration | undefined
    let generatedSecret = ''
    if (input) {
      const authentication = input.repository.authentication
      const material = authentication.credential
      let sanitizedAuthentication = authentication.source === 'project' && material
        ? { source: 'project' as const, summary: { name: material.name, auth_type: material.auth_type, username: material.username, fingerprint: 'demo-project-credential' } }
        : { source: authentication.source, credential_id: authentication.credential_id }
      if (authentication.source === 'project' && material && authentication.save_to_center) {
        const credential = { ...gitCredentials[0], id: Math.max(...gitCredentials.map(row => row.id), 0) + 1, name: material.name, auth_type: material.auth_type, username: material.username, authorized_node_ids: [...targetIDs], used_by: 1 }
        gitCredentials.push(credential)
        sanitizedAuthentication = { source: 'center', credential_id: credential.id }
      }
      if (input.webhook_enabled && !input.webhook_secret) generatedSecret = crypto.randomUUID().replaceAll('-', '') + crypto.randomUUID().replaceAll('-', '')
      configuration = { ...input, environment: (input.environment ?? []).map(variable => ({ name: variable.name, has_value: true })), automation_enabled: input.automation_enabled !== false, repository: { ...input.repository, authentication: sanitizedAuthentication }, configured: true, webhook_secret: '', webhook_id: input.webhook_enabled ? `demo-${name}` : undefined, desired_commit: '', observed_commit: '' }
      deliveryConfigurations.set(name, configuration)
      deliveryEnvironments.set(name, Object.fromEntries((input.environment ?? []).map(variable => [variable.name, variable.value ?? ''])))
    }
    const project: DeliveryProject = { id: Math.max(...deliveryProjects.map(row => row.id), 0) + 1, name, deployment_name: name, configured: !!input, automation_enabled: input?.automation_enabled !== false, repository_url: input?.repository.clone_url, git_ref: input?.repository.ref, node_ids: [...targetIDs], created_at: new Date().toISOString(), updated_at: new Date().toISOString() }
    deliveryProjects.push(project)
    if (configuration) syncDeliveryRule(name, configuration)
    return clone({ ...project, ...(configuration ? { configuration: { ...configuration, webhook_secret: generatedSecret } } : {}) }) as T
  }
  if ((pathname === '/delivery-projects/test-connection' || /^\/delivery-projects\/[^/]+\/test-connection$/.test(pathname)) && method === 'POST') {
    const repository = body.repository as { clone_url: string; ref: string; ref_type: string }
    if (!repository?.clone_url || !repository.ref) throw new ApiError('Invalid Git connection configuration', 17714, 422)
    if (repository.clone_url.includes('missing')) throw new ApiError('git ls-remote failed: repository not found (HTTP 404)', 17715, 422)
    if (repository.ref === 'missing') throw new ApiError('remote branch "missing" was not found; check the reference name and credential access', 17715, 422)
    return { ref_checked: repository.ref_type !== 'commit', ...(repository.ref_type !== 'commit' ? { commit_sha: '7a31f0c87aa1b239536f00223344556677889900' } : {}) } as T
  }
  const deliveryMatch = pathname.match(/^\/delivery-projects\/([^/]+)(\/.*)?$/)
  if (deliveryMatch) {
    const name = deliveryMatch[1]
    const suffix = deliveryMatch[2] || ''
    const project = deliveryProjects.find((item) => item.name === name)
    if (!project) throw new ApiError('Delivery project not found', 17701, 404)
    if (suffix === '/environment' && method === 'GET') return clone(deliveryEnvironments.get(name) ?? {}) as T
    if (!suffix && method === 'GET') return clone(project) as T
    if (suffix === '/sync' && method === 'POST') { const run = taskCenter.dispatch('cd.sync', undefined, { project: name }, 'check'); return clone(tasks.find(task => task.id === run.task_id)) as T }
    if (!suffix && method === 'DELETE') { taskCenter.archive('cd.sync', undefined, name); deliveryProjects.splice(deliveryProjects.indexOf(project), 1); deliveryConfigurations.delete(name); return { name } as T }
    if (suffix === '/automation' && method === 'PUT') {
      if (body.expected_automation_version !== project.automation_version && body.enabled !== project.automation_enabled) throw new ApiError('Schedule changed concurrently; reload and retry', 22003, 409)
      project.automation_enabled = body.enabled === true
      const configuration = deliveryConfigurations.get(name) || cdConfiguration(name)
      configuration.automation_enabled = project.automation_enabled
      deliveryConfigurations.set(name, configuration)
      syncDeliveryRule(name, configuration)
      return clone(project) as T
    }
    if (suffix === '/configuration') {
      if (method === 'PUT') {
        const input = body as unknown as CDConfigureInput
        const current = deliveryConfigurations.get(name)
        if (current && (input.expected_automation_version !== undefined ? input.expected_automation_version !== current.automation_version : input.automation_enabled !== current.automation_enabled || input.sync_interval_seconds !== current.sync_interval_seconds || input.webhook_enabled !== current.webhook_enabled)) throw new ApiError('Schedule changed concurrently; reload and retry', 22003, 409)
        if (input.environment) {
          const current = deliveryEnvironments.get(name) ?? {}
          deliveryEnvironments.set(name, Object.fromEntries(input.environment.map(variable => [variable.name, variable.value ?? current[variable.name] ?? ''])))
        }
        const saved = { ...deliveryConfigurations.get(name) || cdConfiguration(name), ...input, environment: input.environment?.map(variable => ({ name: variable.name, has_value: true })) ?? deliveryConfigurations.get(name)?.environment ?? [], configured: true, repository: { ...input.repository, authentication: input.repository.authentication.source === 'project' ? { source: 'project' as const, summary: { name: input.repository.authentication.credential?.name || 'Project credential', auth_type: input.repository.authentication.credential?.auth_type || 'http_token' as const } } : { source: input.repository.authentication.source, credential_id: input.repository.authentication.credential_id } }, webhook_secret: '' }
        deliveryConfigurations.set(name, saved)
        project.configured = true
        project.automation_enabled = saved.automation_enabled
        project.repository_url = saved.repository.clone_url
        project.git_ref = saved.repository.ref
        project.node_ids = [...saved.node_ids]
        syncDeliveryRule(name, saved)
      }
      return clone(deliveryConfigurations.get(name) || cdConfiguration(name)) as T
    }
    if (suffix === '/drift') return clone({ drifted: false, status: 'healthy', desired_commit: project.desired_commit ?? '', observed_commit: project.observed_commit ?? '', active_commit: project.observed_commit ?? '', active_release_id: project.active_release_id, runtime_healthy: true, checked_at: now, nodes: project.node_ids.map((nodeID) => ({ node_id: nodeID, node_name: nodes.find((item) => item.id === nodeID)?.name ?? nodeID, status: 'healthy' as const, drifted: false, active_release_id: project.active_release_id, active_commit: project.observed_commit, health_summary: '[{"State":"running","Health":"healthy"}]', checked_at: now })) } satisfies CDDrift) as T
    const recordRows: DeliveryRecord[] = releases.filter(release => release.project_id === project.id).map(release => ({
      id: release.id + 1000, project_id: project.id, release_id: release.id, operation: 'deploy', actor_name: release.trigger_actor, trigger_type: release.trigger_type, status: release.status,
      snapshot_complete: true, config_hash: release.config_hash, image_references: release.image_references, compose_files: release.compose_files, environment_file: '.env', environment: [], node_ids: [...project.node_ids], registry_credential_ids: [1], deployment_timeout: 180, auto_rollback: true, commit_sha: release.commit_sha, commit_message: release.commit_message, created_at: release.created_at,
      task_id: release.task_id, attempts: (release.deployments || []).flatMap(deployment => (deployment.attempts || []).map(attempt => ({ ...attempt, node_id: deployment.node_id, node_name: deployment.node_name }))),
    }))
    const projectRecords = deliveryRecords.get(name) || recordRows.filter(record => record.status !== 'awaiting_approval')
    deliveryRecords.set(name, projectRecords)
    if (suffix === '/records') {
      const filtered = projectRecords.filter(record => (!url.searchParams.has('release_id') || record.release_id === Number(url.searchParams.get('release_id'))) && (!url.searchParams.has('operation') || record.operation === url.searchParams.get('operation')) && (!url.searchParams.has('status') || record.status === url.searchParams.get('status')))
      const page = Number(url.searchParams.get('page') || 1), pageSize = Number(url.searchParams.get('page_size') || 20)
      return clone({ items: filtered.slice((page - 1) * pageSize, page * pageSize), total: filtered.length, page, page_size: pageSize, counts: Object.fromEntries(['deploy', 'retry', 'manual_rollback', 'auto_rollback', 'rollback_failed'].map(operation => [operation, projectRecords.filter(record => record.operation === operation).length])) }) as T
    }
    const recordMatch = suffix.match(/^\/records\/(\d+)(\/environment)?$/)
    if (recordMatch) return clone(recordMatch[2] ? {} : projectRecords.find(record => record.id === Number(recordMatch[1]))) as T
    const actionMatch = suffix.match(/^\/releases\/(\d+)\/(deploy|rollback|reject)$/)
    if (actionMatch && method === 'POST') {
      const release = releases.find(release => release.id === Number(actionMatch[1]) && release.project_id === project.id)
      if (!release) throw new ApiError('Release not found', 17706, 404)
      if (actionMatch[2] === 'reject') { release.status = 'rejected'; return clone(release) as T }
      const source = actionMatch[2] === 'rollback' ? projectRecords.find(record => record.id === body.record_id && record.release_id === release.id) : undefined
      if (actionMatch[2] === 'rollback' && !source) throw new ApiError('Configuration record not found', 17706, 404)
      const baseRecord = source || recordRows.find(record => record.release_id === release.id)
      if (!baseRecord) throw new ApiError('Configuration record not found', 17706, 404)
      const overrideNodes = Array.isArray(body.node_ids) && body.node_ids.every((node): node is string => typeof node === 'string') ? body.node_ids : undefined
      const record: DeliveryRecord = { ...baseRecord, id: Math.max(...projectRecords.map(record => record.id), 1000) + 1, operation: source ? 'manual_rollback' : 'deploy', source_record_id: source?.id, status: 'succeeded', actor_name: user.username, node_ids: overrideNodes || source?.node_ids || cdConfiguration(name).node_ids, created_at: new Date().toISOString() }
      const evidence = { project: name, operation: record.operation, record_id: record.id, release_id: release.id }
      const task = completedDemoTask({ type: `cd.${record.operation}`, name: `Deliver ${name}`, params: evidence, result: evidence })
      for (const nodeID of record.node_ids) completedDemoTask({ type: 'cd.deploy.node', name: `Deliver ${name} to ${nodeID}`, node_id: nodeID, parent_task_id: task.id, params: evidence, result: evidence })
      record.task_id = task.id; release.task_id = task.id
      projectRecords.unshift(record); release.latest_record_id = record.id; release.status = 'succeeded'; project.active_release_id = release.id
      return clone({ task, record_id: record.id }) as T
    }
    if (suffix === '/releases') return clone(releases.filter(release => release.project_id === project.id)) as T
    return {} as T
  }

  const nodeMatch = pathname.match(/^\/nodes\/([^/]+)(\/.*)?$/)
  if (nodeMatch) {
    const nodeID = nodeMatch[1]
    const suffix = nodeMatch[2] || ''
    const containers = nodeContainers(nodeID)
    if (suffix === '/containers' && method === 'POST') {
      const node = nodes.find(node => node.id === nodeID && node.enabled && node.status !== 'offline')
      if (!node) throw new ApiError('Docker node is unavailable', 20008, 503)
      const raw = body as unknown as CreateContainerRequest
      const input = containerCreateRequest({ ...raw, command: raw.command.join('\n'), entrypoint: raw.entrypoint.join('\n'), memory_mb: String(raw.memory_mb), cpus: String(raw.cpus) } satisfies ContainerCreateDraft, false)
      if (containers.some(row => row.name === input.name)) throw new ApiError('Container name is already in use', 20262, 409)
      const id = [...crypto.getRandomValues(new Uint8Array(32))].map(value => value.toString(16).padStart(2, '0')).join('')
      const created = new Date().toISOString()
      const row: ContainerSummary = { id, name: input.name, image: input.image, command: input.command.join(' '), created, state: input.start ? 'running' : 'created', status: input.start ? 'Up' : 'Created', ports: input.ports.map(port => ({ private_port: port.container_port, public_port: port.host_port || 32768, type: port.protocol, ip: port.host_ip })), networks: input.network === 'none' ? [] : [{ name: input.network }], labels: {}, cpu_percent: 0, memory_bytes: 0, uptime_seconds: 0 }
      containers.push(row)
      createdContainerDetails.set(nodeID + ':' + id, { ...row, pid: input.start ? 1 : 0, entrypoint: input.entrypoint, working_directory: input.working_directory, restart_policy: input.restart_policy, read_only_rootfs: input.read_only_rootfs, network_mode: input.network, environment: input.environment.map(variable => ({ key: variable.key, sensitive: true })), mounts: input.mounts.map(mount => ({ type: mount.type, name: mount.type === 'volume' ? mount.source : undefined, source: mount.source, destination: mount.target, read_write: !mount.read_only })), networks: (row.networks ?? []).map(network => ({ name: network.name, ip_address: '', gateway: '', mac_address: '' })) })
      const task: TaskRecord = { id: crypto.randomUUID(), scope: 'node', node_id: nodeID, node_name: node.name, type: 'container.create', name: 'Create ' + input.name, status: 'success', progress: 100, message: 'Container ready: ' + id, created_at: created }
      createdContainerIDs.set(task.id, id)
      tasks.unshift(task)
      audits.unshift({ id: Math.max(...audits.map(row => row.id)) + 1, scope: 'node', node_id: nodeID, node_name: node.name, user_id: 1, action: 'container.create', resource_type: 'container', resource_name: id, task_id: task.id, ip: '127.0.0.1', result: 'success', created_at: created })
      return clone(task) as T
    }
    const creationSteps = suffix.match(/^\/tasks\/([^/]+)\/steps$/)
    if (creationSteps && createdContainerIDs.has(creationSteps[1])) return clone([{ id: 'container:' + createdContainerIDs.get(creationSteps[1]), status: 'Created', current: 1, total: 1, progress: 100 }]) as T
    if (['/overview/info', '/overview/metrics', '/overview/resources', '/overview/disk'].includes(suffix)) {
      const fleet = await demoApi<{ nodes: import('../features/overview/types').FleetNode[] }>('/fleet/overview')
      const node = fleet.nodes.find((item) => item.id === nodeID)
      if (!node || !node.enabled || node.status !== 'online') throw new Error('Docker node is unavailable')
      if (suffix === '/overview/info') return clone({ name: node.hostname, server_version: node.engine_version, operating_system: node.os, os_version: node.os_version, architecture: node.architecture, kernel_version: node.kernel_version, containers_running: node.containers_running, containers_paused: node.containers_paused, containers_stopped: node.containers_stopped, images: node.images, cpus: node.cpus, memory_bytes: node.memory_total_bytes, storage_driver: node.storage_driver, logging_driver: node.logging_driver, cgroup_driver: node.cgroup_driver, cgroup_version: node.cgroup_version, default_runtime: node.default_runtime, live_restore: node.live_restore, security_options: node.security_options }) as T
      if (suffix === '/overview/resources') return clone({ networks: node.networks, volumes: node.volumes }) as T
      if (suffix === '/overview/disk') return clone({ docker_disk_usage_bytes: node.docker_disk_usage_bytes }) as T
      const size = url.searchParams.get('size') || 'medium'
      if (!['small', 'medium', 'large'].includes(size)) throw new Error('Invalid overview card size')
      const metrics = { metrics_available: node.metrics_available, container_cpu_percent: node.container_cpu_percent, container_memory_bytes: node.container_memory_bytes }
      if (size === 'small') return clone(metrics) as T
      return clone({ ...metrics, container_network_rx_bytes: node.container_network_rx_bytes, container_network_tx_bytes: node.container_network_tx_bytes, container_block_read_bytes: node.container_block_read_bytes, container_block_write_bytes: node.container_block_write_bytes, longest_container_uptime_seconds: node.longest_container_uptime_seconds, containers: size === 'large' ? node.containers : node.containers.map(({ id, name, image, state, available, cpu_percent, memory_bytes }) => ({ id, name, image, state, available, cpu_percent, memory_bytes })) }) as T
    }
    const operation = operationsDemo(suffix, url)
    if (operation.handled) return clone(operation.value) as T
    if (suffix === '/tasks') {
      const rows = tasks.filter(item => item.scope === 'node' && item.node_id === nodeID)
      return clone(url.searchParams.get('view') === 'summary' ? rows.map(({ params: _params, result: _result, ...row }) => row) : rows) as T
    }
    if (/^\/tasks\/[^/]+\/logs$/.test(suffix)) return clone([{ id: 1, level: 'info', message: 'Task accepted by SUMA task service', created_at: earlier }, { id: 2, level: 'info', message: 'Docker operation completed successfully', created_at: now }]) as T
    if (/^\/tasks\/[^/]+\/steps$/.test(suffix)) return clone([{ id: 'download', status: 'success', current: 1, total: 1, progress: 100 }, { id: 'extract', status: 'success', current: 1, total: 1, progress: 100 }]) as T
    if (/^\/tasks\/[^/]+\/cancel$/.test(suffix)) return {} as T
    if (/^\/tasks\/[^/]+$/.test(suffix)) return clone(tasks.find((item) => suffix.endsWith(item.id)) ?? { id: suffix.split('/').pop(), scope: 'node', node_id: nodeID, node_name: nodes.find((item) => item.id === nodeID)?.name ?? nodeID, type: 'compose.pull', name: 'Compose operation', status: 'success', progress: 100, message: 'Operation completed', created_at: now }) as T
    if (suffix === '/audit-logs') return clone(audits.filter((item) => item.scope === 'node' && item.node_id === nodeID)) as T
    if (suffix === '/overview') {
      const running = containers.filter((item) => item.state === 'running')
      return clone({ host: { hostname: nodes.find((item) => item.id === nodeID)?.name ?? nodeID, os: 'Ubuntu 24.04.3 LTS', kernel: '6.8.0-71-generic', architecture: 'x86_64', cpus: 8, uptime_seconds: 1_284_220, cpu_percent: 31.4, memory_used: 6_978_321_408, memory_total: 17_179_869_184, disk_used: 184_683_593_728, disk_total: 512_110_190_592 }, containers: { cpu_percent: running.reduce((sum, item) => sum + item.cpu_percent, 0), memory_bytes: running.reduce((sum, item) => sum + item.memory_bytes, 0) }, docker: { server_version: nodes.find((item) => item.id === nodeID)?.engine_version ?? '28.3.3', containers_running: running.length, containers_stopped: containers.length - running.length, images: images.length }, docker_disk_usage_bytes: 14_495_514_624 }) as T
    }
    if (suffix === '/containers' && method === 'GET') return clone(containers) as T
    if (suffix === '/containers/runtime') return clone(containers.map((row, index) => ({ ...demoContainerDiagnostics(row).runtime, memory_limit_bytes: index % 2 ? 0 : 2 ** 30 }))) as T
    if (suffix === '/containers/metrics') {
      const sampled = Date.now()
      return clone(containers.filter(row => row.state === 'running').map(({ id, cpu_percent, memory_bytes, uptime_seconds }, index) => ({ id, available: true, cpu_percent, memory_bytes, uptime_seconds, sampled_at: new Date(sampled).toISOString(), started_at: earlier, network_rx_bytes: 2 ** 20 + Math.floor((sampled - containerMetricsEpoch) / 1000 * (index + 1) * 2048), network_tx_bytes: 2 ** 19 + Math.floor((sampled - containerMetricsEpoch) / 1000 * (index + 1) * 1024) } satisfies ContainerMetrics))) as T
    }
    if (suffix === '/containers/batch') return clone({ results: (body.ids as string[] || []).map((id) => ({ id, success: true })) }) as T
    const fileMatch = suffix.match(/^\/containers\/([^/]+)\/files(?:\/(content|history|history\/\d+|restore|actions))?$/)
    if (fileMatch) {
      const treeKey = `${nodeID}:${fileMatch[1]}`
      if (!demoFileTrees.has(treeKey)) demoFileTrees.set(treeKey, new Map([['/', { type: 'directory', content: '' }], ['/data', { type: 'directory', content: '' }], ['/data/config.yaml', { type: 'file', content: 'server:\n  port: 8080\n  mode: production\n' }], ['/data/notes.md', { type: 'file', content: '# Persistent files\n\nEdit this file to preview version history.\n' }], ['/etc', { type: 'directory', content: '' }], ['/etc/suma-demo.conf', { type: 'file', content: 'mode=container-layer\n' }]]))
      const tree = demoFileTrees.get(treeKey)!
      const section = fileMatch[2] || ''
      const filePath = String(url.searchParams.get('path') || body.path || '/')
      const mount = { type: 'volume', name: 'gateway-prod_data', source: '/var/lib/docker/volumes/gateway-prod_data/_data', destination: '/data', read_write: true, is_directory: true }
      const contentView = (name: string) => ({ path: name, content: tree.get(name)?.content ?? '', etag: demoFileHash(tree.get(name)?.content ?? ''), persistent: name.startsWith('/data/'), read_only: false, single_file_bind: false })
      if (!section && method === 'GET') {
        const folder = filePath.replace(/\/$/, '') || '/'
        const entries = [...tree.entries()].filter(([name]) => name !== folder && (name.slice(0, name.lastIndexOf('/')) || '/') === folder).map(([name, value]) => ({ name: name.split('/').pop(), path: name, type: value.type, size: value.content.length, modified_at: 1780020000, mount: name.startsWith('/data') ? mount : undefined }))
        return clone({ path: folder, entries, mounts: [mount], current_mount: folder.startsWith('/data') ? mount : undefined }) as T
      }
      if (section === 'content' && method === 'GET') return clone(contentView(filePath)) as T
      if (section === 'content' && method === 'PUT') {
        const name = String(body.path)
        const before = tree.get(name)?.content ?? ''
        if (body.etag !== demoFileHash(before)) throw new ApiError('File changed since it was opened', 20400, 409)
        const key = `${treeKey}:${name}`
        const rows = demoFileHistory.get(key) ?? []
        if (!rows.length) rows.push({ id: 1, content: before, created_at: earlier, baseline: true })
        rows.push({ id: rows.length + 1, content: String(body.content ?? ''), created_at: new Date().toISOString(), baseline: false })
        demoFileHistory.set(key, rows)
        tree.set(name, { type: 'file', content: String(body.content ?? '') })
        return clone(contentView(name)) as T
      }
      if (section === 'history') return clone((demoFileHistory.get(`${treeKey}:${filePath}`) ?? []).slice().reverse().map((item) => ({ id: item.id, hash: demoFileHash(item.content), created_at: item.created_at, baseline: item.baseline }))) as T
      if (section.startsWith('history/')) {
        const item = (demoFileHistory.get(`${treeKey}:${filePath}`) ?? []).find((row) => row.id === Number(section.split('/')[1]))
        return clone({ path: filePath, content: item?.content ?? '', etag: demoFileHash(item?.content ?? ''), persistent: true }) as T
      }
      if (section === 'restore') {
        const name = String(body.path)
        const item = (demoFileHistory.get(`${treeKey}:${name}`) ?? []).find((row) => row.id === Number(body.revision_id))
        if (!item) throw new ApiError('Revision not found', 20400, 404)
        const before = tree.get(name)?.content ?? ''
        if (body.etag !== demoFileHash(before)) throw new ApiError('File changed since it was opened', 20400, 409)
        const rows = demoFileHistory.get(`${treeKey}:${name}`) ?? []
        rows.push({ id: rows.length + 1, content: item.content, created_at: new Date().toISOString(), baseline: false })
        demoFileHistory.set(`${treeKey}:${name}`, rows)
        tree.set(name, { type: 'file', content: item.content })
        return clone(contentView(name)) as T
      }
      if (section === 'actions') {
        const name = String(body.path)
        const target = String(body.target || '')
        if (body.action === 'create_file' || body.action === 'create_directory') tree.set(name, { type: body.action === 'create_file' ? 'file' : 'directory', content: '' })
        if (body.action === 'delete') for (const key of tree.keys()) if (key === name || key.startsWith(`${name}/`)) tree.delete(key)
        if (body.action === 'copy' || body.action === 'move' || body.action === 'rename') {
          for (const [key, value] of [...tree.entries()]) if (key === name || key.startsWith(`${name}/`)) { tree.set(target + key.slice(name.length), { ...value }); if (body.action !== 'copy') tree.delete(key) }
        }
        return clone(['copy', 'move', 'delete'].includes(String(body.action)) ? completedDemoTask({ type: 'container.files', name: `File ${String(body.action)} ${name}`, node_id: nodeID, params: { action: body.action, path: name, target }, result: { path: name, target } }) : { path: name, target }) as T
      }
    }
    const observationMatch = suffix.match(/^\/containers\/([^/]+)\/(runtime|events)$/)
    if (observationMatch) {
      const row = containers.find(item => item.id === observationMatch[1]) ?? containers[0]
      if (observationMatch[2] === 'runtime') return clone(demoContainerDiagnostics(row)) as T
      const actions = ['create', 'start', 'health_status', 'restart', 'health_status']
      return clone({ source: 'docker', coverage: 'engine_retained', requested_since: url.searchParams.get('since'), requested_until: url.searchParams.get('until'), truncated: false, events: actions.map((action, i) => { const time = Date.now() - (actions.length - i) * 60_000; return { id: `demo-${row.id}-${i}`, node_id: nodeID, container_id: row.id, time: new Date(time).toISOString(), time_nano: String(BigInt(time) * 1000000n), action, exit_code: null, health_status: action === 'health_status' ? i === 2 ? 'unhealthy' : 'healthy' : undefined } }) }) as T
    }
    const containerMatch = suffix.match(/^\/containers\/([^/]+)(?:\/(start|stop|restart|pause|unpause))?$/)
    if (containerMatch) {
      const row = containers.find((item) => item.id === containerMatch[1]) ?? containers[0]
      const action = containerMatch[2]
      if (action) { row.state = action === 'stop' ? 'exited' : action === 'pause' ? 'paused' : 'running'; row.status = row.state === 'running' ? 'Up a few seconds' : row.state }
      if (method === 'PATCH' && typeof body.name === 'string') row.name = body.name
      const detail: ContainerDetail = createdContainerDetails.get(nodeID + ':' + row.id) ?? { ...row, pid: row.state === 'running' ? 12984 : 0, entrypoint: ['/docker-entrypoint.sh'], working_directory: '/app', restart_policy: 'unless-stopped', read_only_rootfs: false, network_mode: 'bridge', environment: [{ key: 'APP_ENV', value: 'production', sensitive: false }, { key: 'DATABASE_PASSWORD', sensitive: true }], mounts: [{ type: 'volume', name: 'gateway-prod_data', source: '/var/lib/docker/volumes/gateway-prod_data/_data', destination: '/data', read_write: true }], networks: (row.networks ?? []).map(network => ({ ...network, ip_address: network.ip_address ?? '', gateway: '172.22.0.1', mac_address: '02:42:ac:16:00:03' })) }
      return clone(detail) as T
    }

    if (suffix === '/images' && method === 'GET') return clone(images) as T
    if (suffix === '/images/pull') {
      const reference = String(body.reference ?? body.image ?? 'demo image')
      return clone(completedDemoTask({ type: 'image.pull', name: `Pull ${reference}`, node_id: nodeID, params: { reference }, result: { reference } })) as T
    }
    if (suffix.startsWith('/images/') && method === 'GET') return clone(images.find((item) => suffix.includes(item.id)) ?? images[0]) as T
    if (suffix.startsWith('/images/')) return {} as T

    if (suffix === '/networks' && method === 'GET') return clone(networks) as T
    if (suffix.startsWith('/networks/') && method === 'GET') return clone(networks.find((item) => item.id === suffix.slice('/networks/'.length)) ?? networks[0]) as T
    if (suffix.startsWith('/networks')) return {} as T
    if (suffix === '/volumes' && method === 'GET') return clone(volumes) as T
    if (suffix.startsWith('/volumes')) return {} as T

    if (suffix === '/projects' && method === 'GET') return clone(projects.map((item) => ({ ...item, declared_service_names: projectConfigurations.get(`${nodeID}/${item.name}`)?.declared_service_names ?? item.declared_service_names, node_id: nodeID, scope: { kind: 'engine', id: nodeID }, ref: { ...item.ref, scope: { kind: 'engine', id: nodeID } } }))) as T
    if (suffix === '/projects/validate') {
      const content = String(body.compose ?? '')
      if (composeProblems(content).length || !configKeys(content, ['services']).length || configKeys(content, ['services']).some((service) => !configValue(content, ['services', service, 'image']) && !configValue(content, ['services', service, 'build']))) throw new ApiError('Compose validation failed', 20412, 422)
      return { valid: true } as T
    }
    if (suffix === '/projects' && method === 'POST') {
      const name = String(body.name ?? '')
      if (!/^[a-z0-9][a-z0-9_-]*$/.test(name) || composeProblems(String(body.compose)).length) throw new ApiError('Invalid Project configuration', 20403, 422)
      if (projects.some((row) => row.name === name)) throw new ApiError('Project already exists', 20405, 409)
      const summary: ProjectSummary = { ...clone(projects[0]), name, native_name: name, node_id: nodeID, managed: true, source: 'managed', declared_service_names: configKeys(String(body.compose), ['services']), service_count: 0, instance_count: 0, status: 'stopped', scope: { kind: 'engine', id: nodeID }, ref: { backend: 'compose', scope: { kind: 'engine', id: nodeID }, native_name: name }, created_at: new Date().toISOString(), updated_at: new Date().toISOString() }
      projects.push(summary)
      const detail: Project = { ...projectDetail(summary), compose: String(body.compose), environment: String(body.environment ?? ''), revision: `demo-config-${++projectRevision}` }
      projectConfigurations.set(`${nodeID}/${name}`, detail)
      return clone(detail) as T
    }
    if (suffix === '/projects/batch') return {} as T
    const projectMatch = suffix.match(/^\/projects\/compose\/([^/]+)(\/.*)?$/)
    if (projectMatch) {
      const name = projectMatch[1]
      const rest = projectMatch[2] || ''
      const row = projects.find((item) => item.name === name) ?? projects[0]
      const configurationKey = `${nodeID}/${name}`
      const detail = projectConfigurations.get(configurationKey) ?? { ...projectDetail(row), revision: 'demo-config-0' }
      const cd=projectCDConfigurations.get(configurationKey) ?? {configured:false,enabled:false,revision:0,repository:{clone_url:'',ref_type:'branch',ref:'main',compose_files:['compose.yml'],environment_file:'',authentication:{source:'none'}},environment:[],registry_credential_ids:[],deployment_timeout:120,sync_interval_seconds:300,automation_enabled:false,reconcile_mode:'manual',auto_rollback:false,initial_application_required:true,webhook_enabled:false} satisfies ProjectCDConfiguration
      const records=projectCDRecords.get(configurationKey) ?? []
      if(rest.startsWith('/gitops')){
        const action=rest.slice('/gitops'.length)
        if(action==='' && method==='GET')return clone(cd) as T
        if(action==='/test-connection')return {ref_checked:true,commit_sha:'d'.repeat(40)} as T
        if(action==='/records')return clone({items:records.slice((Number(url.searchParams.get('page')||1)-1)*20,Number(url.searchParams.get('page')||1)*20),total:records.length}) as T
        if(action==='/preview')return clone({record_id:cd.candidate?.id ?? cd.applied?.id,commit_sha:'d'.repeat(40),applied:!!cd.applied,compose:detail.compose,files:cd.repository.compose_files.map(path=>({path,content:detail.compose})),repository_environment:'APP_ENV=production\n'}) as T
        if(action==='/environment/values')return clone(projectCDValues.get(configurationKey) ?? {}) as T
        if(action==='/environment' && method==='GET')return clone(cd.environment) as T
        if(body.expected_revision!==undefined && body.expected_revision!==cd.revision)throw new ApiError('CD configuration changed',20902,409)
        if(action==='' && method==='PUT'){
          const next:ProjectCDConfiguration={...cd,...body,configured:true,enabled:true,revision:cd.revision+1,repository:{...cd.repository,...(typeof body.repository==='object'&&body.repository!==null?body.repository:{})}}
          next.repository.authentication={...next.repository.authentication,credential:undefined}
          if(Array.isArray(body.environment)){const values:Record<string,string>={};for(const variable of body.environment)if(typeof variable.name==='string' && typeof variable.value==='string')values[variable.name]=variable.value;projectCDValues.set(configurationKey,values);next.environment=Object.keys(values).map(name=>({name,has_value:true}))}
          projectCDConfigurations.set(configurationKey,next);syncProjectRule(nodeID,name,next);return clone(next) as T
        }
        if(action==='/environment' && method==='PUT'){
          const values=projectCDValues.get(configurationKey) ?? {};const nextValues:Record<string,string>={}
          if(Array.isArray(body.environment))for(const variable of body.environment)if(typeof variable.name==='string')nextValues[variable.name]=typeof variable.value==='string'?variable.value:values[variable.name]??''
          projectCDValues.set(configurationKey,nextValues);cd.environment=Object.keys(nextValues).map(name=>({name,has_value:true}));cd.revision++;cd.candidate=undefined;projectCDConfigurations.set(configurationKey,cd);return clone(cd) as T
        }
        if(action==='/check'){const run=taskCenter.dispatch('project.cd.check',nodeID,{project:name},'check');return clone(tasks.find(task=>task.id===run.task_id)) as T}
        if(action==='/automation'){if(body.enabled===true && cd.initial_application_required)throw new ApiError('Confirm first application',20902,422);cd.automation_enabled=body.enabled===true;cd.revision++;projectCDConfigurations.set(configurationKey,cd);syncProjectRule(nodeID,name,cd);return clone(cd) as T}
        if(action==='/detach' || action==='/apply' || action.includes('/rollback')){
          const operation=action==='/detach'?'detach':action==='/apply'?'apply':'rollback'
          const recordID = Math.max(...records.map(record => record.id), 0) + 1
          const task = completedDemoTask({ type: `project.cd.${operation}`, name: `${operation} ${name}`, node_id: nodeID, params: { project: name }, result: { project: name, operation, record_id: recordID } })
          const record:ProjectCDRecord={id:recordID,operation,status:'succeeded',task_id:task.id,actor:'Demo admin',revision:cd.revision,commit_sha:'d'.repeat(40),commit_message:'Demo Git configuration',config_hash:'e'.repeat(64),created_at:new Date().toISOString(),failure_reason:''}
          records.unshift(record);projectCDRecords.set(configurationKey,records)
          if(operation==='detach'){cd.enabled=false;cd.automation_enabled=false}
          else {cd.applied=record;cd.initial_application_required=false;if(operation==='rollback')cd.automation_enabled=false}
          projectCDConfigurations.set(configurationKey,cd);syncProjectRule(nodeID,name,cd);return clone(task) as T
        }
        throw new ApiError('Unknown project CD operation', 20901, 400)
      }
      if (!rest && method === 'GET') return clone({...detail,deployment_mode:cd.enabled?'cd':'manual'}) as T
      if (!rest && method === 'PUT') {
        if(cd.enabled)throw new ApiError('Project uses CD deployment mode',20902,409)
        if (body.expected_revision && body.expected_revision !== detail.revision) throw new ApiError('Project configuration changed', 20411, 409)
        if (composeProblems(String(body.compose)).length) throw new ApiError('Compose YAML cannot be parsed', 20404, 422)
        const saved = { ...detail, declared_service_names: configKeys(String(body.compose), ['services']), compose: String(body.compose), environment: String(body.environment ?? ''), revision: `demo-config-${++projectRevision}` }
        projectConfigurations.set(configurationKey, saved)
        return clone(saved) as T
      }
      if (rest === '/validate') {
        const content = String(body.compose)
        if (composeProblems(content).length || !configKeys(content, ['services']).length || configKeys(content, ['services']).some((service) => !configValue(content, ['services', service, 'image']) && !configValue(content, ['services', service, 'build']))) throw new ApiError('Compose validation failed', 20412, 422)
        return { valid: true } as T
      }
      if (!rest && method === 'DELETE') {
        taskCenter.archive('image.update', nodeID, name); taskCenter.archive('project.cd.check', nodeID, name)
        projectConfigurations.delete(configurationKey)
        const index = projects.findIndex((item) => item.name === name)
        if (index >= 0) projects.splice(index, 1)
        return clone({ name }) as T
      }
      if (rest === '/services') return clone(containers.filter((item) => item.labels['com.docker.compose.project'] === name)) as T
      if (rest === '/logs') return clone({ logs: 'gateway  | SUMA demo service ready\ngateway  | GET /health 200 1ms\ndatabase | checkpoint complete' }) as T
      if (rest === '/takeover/preview' || rest === '/takeover/render') {
        const draft: ProjectTakeoverDraft = { project_name: name, backend: 'compose', source: 'runtime', confidence: 'high', fingerprint: 'demo-fingerprint', compose: projectDetail(row).compose, environment: 'APP_ENV=production\n', variables: [], warnings: ['演示数据：配置由 Docker 运行态重建。'], blockers: [], capabilities: projectCapabilities, observation: { name, services: [], one_off_containers: [], orphan_containers: [], fingerprint: 'demo-fingerprint' } }
        return clone(draft) as T
      }
      if (rest === '/takeover/shadow/assess') return clone({ eligible: true, reasons: [], warnings: [] } satisfies ShadowAssessment) as T
      if (rest === '/takeover/shadow' && method === 'POST') return clone({ session_id: 'shadow-demo', preview_project: `${name}-preview`, expires_at: now, task: completedDemoTask({ type: 'compose.shadow.preview', name: `Preview ${name}`, node_id: nodeID, params: { project: name }, result: { session_id: 'shadow-demo' } }) } satisfies ShadowPreviewSession) as T
      if (rest.startsWith('/takeover/shadow/')) return clone({ session_id: 'shadow-demo', preview_project: `${name}-preview`, expires_at: now, containers: '1 running', logs: 'Preview service is healthy' } satisfies ShadowPreviewStatus) as T
      if (rest.startsWith('/actions/')) {
        if (body.expected_revision && body.expected_revision !== detail.revision) throw new ApiError('Project configuration changed', 20416, 409)
        const task = completedDemoTask({ type: `compose.${rest.slice('/actions/'.length)}`, name: `Compose ${name}`, node_id: nodeID, params: { project: name }, result: { project: name } })
        return clone(task) as T
      }
      if (rest === '/cleanup' && method === 'POST') return clone(completedDemoTask({ type: 'project.cleanup', name: `Clean ${name}`, node_id: nodeID, params: { project: name }, result: { project: name } })) as T
      return {} as T
    }
    if (suffix === '/system/prune') return clone(completedDemoTask({ type: 'system.prune', name: 'Docker system prune', node_id: nodeID, params: {}, result: { reclaimed_bytes: 512 * 1024 * 1024 } })) as T
  }

  return {} as T
}

function demoContainerDiagnostics(row: ContainerSummary): ContainerDiagnostics {
  const now = new Date().toISOString(), started = new Date(Date.now() - 3600_000).toISOString()
  const unhealthy = row.name === 'immich-server', configured = unhealthy || row.name === 'gateway'
  return { runtime: { id: row.id, state: row.state, health_status: configured ? unhealthy ? 'unhealthy' : 'healthy' : 'none', restart_count: unhealthy ? 3 : 0, memory_limit_bytes: 2 ** 30, started_at: started, finished_at: row.state === 'exited' ? now : null, exit_code: row.state === 'exited' ? 0 : null, oom_killed: false, state_error: '', observed_at: now }, restart_policy: 'unless-stopped', health: { configured, failing_streak: unhealthy ? 3 : 0, last_checked_at: configured ? now : null, command: configured ? 'CMD curl -f http://localhost/health' : null, interval_ms: 30000, timeout_ms: 3000, start_period_ms: 10000, start_interval_ms: 5000, retries: 3, logs: configured ? [{ started_at: new Date(Date.now() - 120).toISOString(), finished_at: now, exit_code: unhealthy ? 1 : 0, output: unhealthy ? 'Connection refused; token=[REDACTED]' : 'HTTP 200', output_redacted: unhealthy, output_truncated: false }] : [] } }
}

export function subscribeDemoStream(stream: DemoStream, onMessage: (value: string) => void): () => void {
  if (stream === 'logs') {
    const lines = ['2026-08-29T16:30:01Z INFO  SUMA demo container started', '2026-08-29T16:30:03Z INFO  connected to gateway-prod_default', '2026-08-29T16:30:05Z INFO  GET /health 200 1ms']
    lines.forEach((line, index) => setTimeout(() => onMessage(line), index * 120))
    const timer = window.setInterval(() => onMessage(`${new Date().toISOString()} INFO  GET /api/health 200 2ms`), 4_000)
    return () => window.clearInterval(timer)
  }
  if (stream === 'normalized-stats') {
    let tick = 0
    const started = new Date(Date.now() - 3600_000).toISOString()
    const emit = () => { tick++; onMessage(JSON.stringify({ id: 'demo', available: true, cpu_percent: 18 + Math.sin(tick) * 5, memory_bytes: 188_743_680 + tick * 32768, network_rx_bytes: 12000000 + tick * 4096, network_tx_bytes: 8000000 + tick * 2048, block_read_bytes: 84000000, block_write_bytes: 31000000, pids: 14, uptime_seconds: 3600 + tick, sampled_at: new Date().toISOString(), started_at: started })) }
    emit(); const timer = setInterval(emit, 1000); return () => clearInterval(timer)
  }
  if (stream === 'stats') {
    let tick = 0
    const emit = () => {
      tick += 1
      onMessage(JSON.stringify({ cpu_stats: { cpu_usage: { total_usage: 2_000_000 + tick * 45_000 }, system_cpu_usage: 20_000_000 + tick * 1_000_000, online_cpus: 4 }, precpu_stats: { cpu_usage: { total_usage: 2_000_000 + (tick - 1) * 45_000 }, system_cpu_usage: 20_000_000 + (tick - 1) * 1_000_000 }, memory_stats: { usage: 188_743_680 + tick * 32_768 }, networks: { eth0: { rx_bytes: 12_000_000 + tick * 4_096, tx_bytes: 8_000_000 + tick * 2_048 } }, blkio_stats: { io_service_bytes_recursive: [{ op: 'read', value: 84_000_000 }, { op: 'write', value: 31_000_000 }] }, pids_stats: { current: 14 } }))
    }
    emit()
    const timer = window.setInterval(emit, 1_500)
    return () => window.clearInterval(timer)
  }
  onMessage('\r\n\x1b[1;36mSUMA demo terminal\x1b[0m\r\nContainer: gateway · Node: homelab-01\r\n\r\n$ ')
  return () => undefined
}

export function subscribeProjectLogs(_nodeID: string, projectName: string, onEvent: (event: LogEvent) => void, tail: number): () => void { return demoProjectLogStream(projectName, onEvent, tail) }
