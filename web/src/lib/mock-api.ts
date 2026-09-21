import type { User } from '../features/auth/types'
import type { Project, ProjectSummary, ProjectTakeoverDraft, ShadowAssessment, ShadowPreviewSession, ShadowPreviewStatus } from '../features/compose/types'
import type { ContainerDetail, ContainerMetrics, ContainerSummary } from '../features/containers/types'
import type { CDConfiguration, CDDrift, DeliveryProject, DeliveryRelease, GitCredential } from '../features/delivery/types'
import type { DockerNode, NodeGroup } from './nodes'
import { ApiError, type DemoCredentials, type DemoStream } from './api'

export const demoCredentials: DemoCredentials = { username: 'admin', password: 'admin123' }

const now = '2026-08-29T08:30:00.000Z'
const earlier = '2026-08-29T07:42:00.000Z'
const sessionKey = 'suma-demo-session'
const fleetOverviewRefreshSeconds = 10
let fleetOverviewTick = 0

const user: User = { id: 1, username: 'admin', nickname: 'Demo Admin', email: 'admin@suma.demo', has_avatar: false }

const nodes: DockerNode[] = [
  { id: 'local', name: 'homelab-01', connection_type: 'unix', endpoint: 'unix:///var/run/docker.sock', tls_mode: 'disabled', enabled: true, engine_id: 'engine-homelab', engine_version: '28.3.3', status: 'online', last_latency_ms: 12, last_checked_at: now, created_at: earlier, updated_at: now, group_ids: [1, 2] },
  { id: 'edge-hk', name: 'edge-hk', connection_type: 'tcp', endpoint: 'tcp://10.20.0.8:2376', tls_mode: 'required', tls_credential_id: 1, enabled: true, engine_id: 'engine-edge-hk', engine_version: '28.3.3', status: 'online', last_latency_ms: 46, last_checked_at: now, created_at: earlier, updated_at: now, group_ids: [1, 3] },
  { id: 'nas-prod', name: 'nas-prod', connection_type: 'unix', endpoint: 'unix:///var/run/docker.sock', tls_mode: 'disabled', enabled: true, engine_id: 'engine-nas-prod', engine_version: '27.5.1', status: 'online', last_latency_ms: 21, last_checked_at: now, created_at: earlier, updated_at: now, group_ids: [1, 2] },
]

const nodeGroups: NodeGroup[] = [
  { id: 1, name: 'Default', description: 'Default node group', is_default: true, node_count: 3, created_at: earlier, updated_at: now },
  { id: 2, name: 'HomeLab', description: 'Local home infrastructure', is_default: false, node_count: 2, created_at: earlier, updated_at: now },
  { id: 3, name: 'Edge', description: 'Remote edge engines', is_default: false, node_count: 1, created_at: earlier, updated_at: now },
]
const syncNodeGroupCounts = () => nodeGroups.forEach((group) => { group.node_count = nodes.filter((node) => node.group_ids.includes(group.id)).length })

const makeContainer = (id: string, name: string, image: string, state: string, cpu: number, memory: number, labels: Record<string, string>, ports: ContainerSummary['ports']): ContainerSummary => ({
  id: id.repeat(64).slice(0, 64), name, image, command: '/docker-entrypoint.sh', created: earlier, state, status: state === 'running' ? 'Up 6 hours (healthy)' : 'Exited (0) 2 hours ago', ports, labels, cpu_percent: state === 'running' ? cpu : 0, memory_bytes: state === 'running' ? memory : 0, uptime_seconds: state === 'running' ? 21_840 : 0,
})

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

const projectCapabilities: ProjectSummary['capabilities'] = ['view', 'edit', 'deploy', 'stop', 'restart', 'update', 'delete', 'services', 'logs']
const projects: ProjectSummary[] = [
  { ref: { backend: 'compose', scope: { kind: 'engine', id: 'local' }, native_name: 'gateway-prod' }, backend: 'compose', scope: { kind: 'engine', id: 'local' }, node_id: 'local', name: 'gateway-prod', native_name: 'gateway-prod', managed: true, source: 'managed', capabilities: projectCapabilities, service_count: 2, instance_count: 2, status: 'running', created_at: earlier, updated_at: now },
  { ref: { backend: 'compose', scope: { kind: 'engine', id: 'local' }, native_name: 'media-stack' }, backend: 'compose', scope: { kind: 'engine', id: 'local' }, node_id: 'local', name: 'media-stack', native_name: 'media-stack', managed: true, source: 'managed', capabilities: projectCapabilities, service_count: 3, instance_count: 3, status: 'running', created_at: earlier, updated_at: now },
  { ref: { backend: 'compose', scope: { kind: 'engine', id: 'local' }, native_name: 'legacy-tools' }, backend: 'compose', scope: { kind: 'engine', id: 'local' }, node_id: 'local', name: 'legacy-tools', native_name: 'legacy-tools', managed: false, source: 'external', capabilities: ['view', 'services', 'logs', 'takeover', 'cleanup'], service_count: 1, instance_count: 1, status: 'stopped', created_at: earlier, updated_at: now },
]

const projectDetail = (row: ProjectSummary): Project => ({ ...row, path: `/srv/compose/${row.name}`, can_manage: row.managed, config_files: ['compose.yml'], services: row.service_count, containers: row.instance_count, compose: `services:\n  app:\n    image: ghcr.io/example/${row.name}:latest\n    restart: unless-stopped\n`, environment: 'APP_ENV=production\n', metadata: { origin: row.managed ? 'created' : 'takeover', claimed_at: earlier, last_deployed_at: now } })

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

const tasks = [
  { id: 'task-release-184', scope: 'control_plane', type: 'delivery', name: 'Deploy gateway-prod release #184', status: 'success', progress: 100, message: 'Health gate passed on 2 nodes', created_at: '2026-08-29T08:14:00.000Z' },
  { id: 'task-pull-183', scope: 'node', node_id: 'local', node_name: 'homelab-01', type: 'image_pull', name: 'Pull ghcr.io/afteryuwei/gateway:1.8.2', status: 'success', progress: 100, message: 'Image pulled', created_at: '2026-08-29T07:52:00.000Z' },
  { id: 'task-prune-182', scope: 'node', node_id: 'local', node_name: 'homelab-01', type: 'system_prune', name: 'Docker system prune', status: 'success', progress: 100, message: 'Reclaimed 768 MB', created_at: '2026-08-28T22:30:00.000Z' },
]

const audits = [
  { id: 18, scope: 'node', node_id: 'local', node_name: 'homelab-01', user_id: 1, action: 'container.restart', resource_type: 'container', resource_name: containerSets.local[0].id, ip: '192.168.1.36', result: 'success', created_at: '2026-08-29T08:25:00.000Z' },
  { id: 17, scope: 'node', node_id: 'local', node_name: 'homelab-01', user_id: 1, action: 'project.deploy', resource_type: 'project', resource_name: 'gateway-prod', ip: '192.168.1.36', result: 'success', created_at: '2026-08-29T08:14:00.000Z' },
  { id: 16, scope: 'control_plane', user_id: 1, action: 'node.test', resource_type: 'node', resource_name: 'edge-hk', ip: '192.168.1.36', result: 'success', created_at: '2026-08-29T07:58:00.000Z' },
  { id: 15, scope: 'node', node_id: 'local', node_name: 'homelab-01', user_id: 1, action: 'image.pull', resource_type: 'image', resource_name: 'ghcr.io/afteryuwei/gateway:1.8.2', ip: '192.168.1.36', result: 'success', created_at: '2026-08-29T07:52:00.000Z' },
]

const deliveryProjects: DeliveryProject[] = [
  { id: 1, name: 'gateway-prod', configured: true, repository_url: 'https://github.com/example/gateway.git', git_ref: 'main', desired_commit: '7a31f0c87aa1', observed_commit: '7a31f0c87aa1', active_release_id: 184, created_at: earlier, updated_at: now, node_ids: ['local', 'edge-hk'] },
  { id: 2, name: 'media-stack', configured: true, repository_url: 'https://github.com/example/media-stack.git', git_ref: 'stable', desired_commit: 'f4c9a82e10d2', observed_commit: 'f4c9a82e10d2', active_release_id: 181, created_at: earlier, updated_at: now, node_ids: ['local', 'nas-prod'] },
]

const cdConfiguration = (name: string): CDConfiguration => ({ configured: true, repository: { clone_url: `https://github.com/example/${name}.git`, ref_type: 'branch', ref: name === 'gateway-prod' ? 'main' : 'stable', authentication: { source: 'center', credential_id: 1, summary: { name: 'GitHub Demo', auth_type: 'http_token', username: 'suma-demo' } }, compose_files: ['compose.yml'], environment_file: '.env' }, reconcile_mode: name === 'gateway-prod' ? 'auto' : 'manual', sync_interval_seconds: 300, desired_commit: name === 'gateway-prod' ? '7a31f0c87aa1' : 'f4c9a82e10d2', observed_commit: name === 'gateway-prod' ? '7a31f0c87aa1' : 'f4c9a82e10d2', active_release_id: name === 'gateway-prod' ? 184 : 181, auto_rollback: true, deployment_timeout: 180, webhook_enabled: true, webhook_id: `demo-${name}`, webhook_secret: '', node_ids: name === 'gateway-prod' ? ['local', 'edge-hk'] : ['local', 'nas-prod'], registry_credential_ids: [1] })

const releases: DeliveryRelease[] = [
  { id: 184, project_id: 1, repository_url: 'https://github.com/example/gateway.git', git_ref: 'main', commit_sha: '7a31f0c87aa1', commit_message: 'feat: expose fleet overview', commit_author: 'Demo Maintainer', config_hash: 'cfg-184', image_references: JSON.stringify(['ghcr.io/afteryuwei/gateway:1.8.2']), task_id: 'task-release-184', status: 'succeeded', trigger_type: 'webhook', trigger_actor: 'github', previous_release_id: 183, compose_files: JSON.stringify(['compose.yml']), approved_by: 1, approved_at: earlier, started_at: earlier, finished_at: now, health_summary: '2/2 targets healthy', created_at: earlier, updated_at: now, remediation: { retry_failed_node_ids: [], rollback_failed_node_ids: [] }, deployments: [{ id: 1, node_id: 'local', node_name: 'homelab-01', status: 'succeeded', health_summary: 'healthy', started_at: earlier, finished_at: now, attempts: [{ id: 1, deployment_id: 1, operation: 'deploy', target_release_id: 184, status: 'succeeded', health_summary: 'healthy', started_at: earlier, finished_at: now, created_at: earlier }] }, { id: 2, node_id: 'edge-hk', node_name: 'edge-hk', status: 'succeeded', health_summary: 'healthy', started_at: earlier, finished_at: now }] },
  { id: 183, project_id: 1, repository_url: 'https://github.com/example/gateway.git', git_ref: 'main', commit_sha: 'a81d3e90d4c2', commit_message: 'fix: retain proxy headers', commit_author: 'Demo Maintainer', config_hash: 'cfg-183', image_references: JSON.stringify(['ghcr.io/afteryuwei/gateway:1.8.1']), status: 'succeeded', trigger_type: 'manual', trigger_actor: 'admin', compose_files: JSON.stringify(['compose.yml']), started_at: earlier, finished_at: earlier, health_summary: '2/2 targets healthy', created_at: earlier, updated_at: earlier, remediation: { retry_failed_node_ids: [], rollback_failed_node_ids: [] } },
]

const gitCredentials: GitCredential[] = [{ id: 1, name: 'GitHub Demo', auth_type: 'http_token', username: 'suma-demo', fingerprint: 'sha256:3c92…7ab1', created_at: earlier, updated_at: now, last_used_at: now, used_by: 2, authorized_node_ids: ['local', 'edge-hk', 'nas-prod'] }]
const registryCredentials = [{ id: 1, name: 'GHCR Demo', server_address: 'ghcr.io', auth_type: 'token', username: 'suma-demo', fingerprint: 'sha256:8d14…2f09', created_at: earlier, updated_at: now, last_used_at: now, authorized_node_ids: ['local', 'edge-hk', 'nas-prod'] }]
const tlsCredentials = [{ id: 1, name: 'Edge Docker mTLS', fingerprint: 'SHA256:91:42:7A:DE:MO', authorized_node_ids: ['edge-hk'], created_at: earlier, updated_at: now }]

const settings = { 'general.server_name': 'SUMA Demo', 'general.timezone': 'Asia/Shanghai', 'docker.compose_command': 'docker compose', 'storage.compose_root': '/data/compose', 'storage.data_root': '/data', 'storage.backup_root': '/data/backups', 'security.cookie_secure': 'true', 'registry.default': 'ghcr.io' }

const clone = <T,>(value: T): T => structuredClone(value)
const parseBody = (init?: RequestInit): Record<string, unknown> => {
  if (typeof init?.body !== 'string') return {}
  try { return JSON.parse(init.body) as Record<string, unknown> } catch { return {} }
}
const authenticated = () => typeof sessionStorage !== 'undefined' && sessionStorage.getItem(sessionKey) === '1'
const requireSession = () => { if (!authenticated()) throw new ApiError('请先登录演示环境', 40101, 401) }
const wait = () => new Promise((resolve) => setTimeout(resolve, 90))
const nodeContainers = (id: string) => containerSets[id] ?? containerSets.local

export async function demoApi<T>(path: string, init?: RequestInit): Promise<T> {
  await wait()
  const method = (init?.method || 'GET').toUpperCase()
  const url = new URL(path, 'https://demo.suma.local')
  const pathname = decodeURIComponent(url.pathname)
  const body = parseBody(init)

  if (pathname === '/auth/status') return clone({ needs_setup: false }) as T
  if (pathname === '/auth/login' && method === 'POST') {
    if (body.username !== demoCredentials.username || body.password !== demoCredentials.password) throw new ApiError('用户名或密码错误', 40102, 401)
    sessionStorage.setItem(sessionKey, '1')
    return clone(user) as T
  }
  if (pathname === '/auth/logout' && method === 'POST') { sessionStorage.removeItem(sessionKey); return {} as T }
  if (pathname === '/auth/session') { requireSession(); return clone(user) as T }

  requireSession()

  if (pathname === '/account/profile' && method === 'PUT') return clone({ ...user, ...body }) as T
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
  if (pathname === '/cd/overview') return clone({ projects: deliveryProjects.map((project) => ({ name: project.name, configured: true, repository_url: project.repository_url, git_ref: project.git_ref, reconcile_mode: project.name === 'gateway-prod' ? 'auto' : 'manual', node_ids: project.node_ids, drifted: false, runtime_healthy: true, active_release: { id: project.active_release_id, status: 'succeeded', commit_sha: project.desired_commit, trigger_type: 'webhook', created_at: now }, latest_release: { id: project.active_release_id, status: 'succeeded', commit_sha: project.desired_commit, trigger_type: 'webhook', created_at: now }, awaiting_approval: false, releasing: false })), totals: { projects: 2, configured: 2, releasing: 0, awaiting_approval: 0, drifted: 0, healthy: 2 } }) as T

  if (pathname === '/tasks') {
    const scope = url.searchParams.get('scope') || 'control_plane'
    return clone(scope === 'all' ? tasks : tasks.filter((item) => item.scope === scope)) as T
  }
  if (/^\/tasks\/[^/]+\/logs$/.test(pathname)) return clone([{ id: 1, level: 'info', message: 'Task accepted by SUMA task service', created_at: earlier }, { id: 2, level: 'info', message: 'Docker operation completed successfully', created_at: now }]) as T
  if (/^\/tasks\/[^/]+\/steps$/.test(pathname)) return clone([{ id: 'download', status: 'success', current: 1, total: 1, progress: 100 }, { id: 'extract', status: 'success', current: 1, total: 1, progress: 100 }]) as T
  if (/^\/tasks\/[^/]+\/cancel$/.test(pathname)) return {} as T
  if (/^\/tasks\/[^/]+$/.test(pathname)) return clone(tasks.find((item) => pathname.endsWith(item.id)) ?? { id: pathname.split('/').pop(), scope: 'node', node_id: 'local', type: 'compose.pull', name: 'Compose operation', status: 'success', progress: 100, message: 'Operation completed', created_at: now }) as T
  if (pathname === '/audit-logs') {
    const scope = url.searchParams.get('scope') || 'control_plane'
    return clone(scope === 'all' ? audits : audits.filter((item) => item.scope === scope)) as T
  }

  if (pathname === '/credentials/git' && method === 'GET') return clone(gitCredentials) as T
  if (pathname === '/credentials/registries' && method === 'GET') return clone(registryCredentials) as T
  if (pathname === '/credentials/docker-tls' && method === 'GET') return clone(tlsCredentials) as T
  if (pathname.startsWith('/credentials/git')) return clone(gitCredentials[0]) as T
  if (pathname.startsWith('/credentials/registries')) return clone(registryCredentials[0]) as T
  if (pathname.startsWith('/credentials/docker-tls')) return clone(tlsCredentials[0]) as T
  if (pathname === '/settings' && method === 'GET') return clone(settings) as T
  if (pathname === '/settings' && method === 'PUT') return clone(body) as T

  if (pathname === '/delivery-projects' && method === 'GET') return clone(deliveryProjects) as T
  if (pathname === '/delivery-projects' && method === 'POST') return clone(deliveryProjects[0]) as T
  const deliveryMatch = pathname.match(/^\/delivery-projects\/([^/]+)(\/.*)?$/)
  if (deliveryMatch) {
    const name = deliveryMatch[1]
    const suffix = deliveryMatch[2] || ''
    const project = deliveryProjects.find((item) => item.name === name) ?? deliveryProjects[0]
    if (!suffix && method === 'GET') return clone(project) as T
    if (suffix === '/configuration') return clone(cdConfiguration(name)) as T
    if (suffix === '/drift') return clone({ drifted: false, status: 'healthy', desired_commit: project.desired_commit ?? '', observed_commit: project.observed_commit ?? '', active_commit: project.observed_commit ?? '', active_release_id: project.active_release_id, runtime_healthy: true, checked_at: now, nodes: project.node_ids.map((nodeID) => ({ node_id: nodeID, node_name: nodes.find((item) => item.id === nodeID)?.name ?? nodeID, status: 'healthy' as const, drifted: false, active_release_id: project.active_release_id, active_commit: project.observed_commit, health_summary: '[{"State":"running","Health":"healthy"}]', checked_at: now })) } satisfies CDDrift) as T
    if (suffix === '/releases') return clone(releases.map((release) => ({ ...release, project_id: project.id }))) as T
    return {} as T
  }

  const nodeMatch = pathname.match(/^\/nodes\/([^/]+)(\/.*)?$/)
  if (nodeMatch) {
    const nodeID = nodeMatch[1]
    const suffix = nodeMatch[2] || ''
    const containers = nodeContainers(nodeID)
    if (suffix === '/tasks') return clone(tasks.filter((item) => item.scope === 'node' && item.node_id === nodeID)) as T
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
    if (suffix === '/containers/metrics') return clone(containers.map(({ id, cpu_percent, memory_bytes, uptime_seconds }) => ({ id, cpu_percent, memory_bytes, uptime_seconds } satisfies ContainerMetrics))) as T
    if (suffix === '/containers/batch') return clone({ results: (body.ids as string[] || []).map((id) => ({ id, success: true })) }) as T
    const containerMatch = suffix.match(/^\/containers\/([^/]+)(?:\/(start|stop|restart|pause|unpause))?$/)
    if (containerMatch) {
      const row = containers.find((item) => item.id === containerMatch[1]) ?? containers[0]
      const action = containerMatch[2]
      if (action) { row.state = action === 'stop' ? 'exited' : action === 'pause' ? 'paused' : 'running'; row.status = row.state === 'running' ? 'Up a few seconds' : row.state }
      if (method === 'PATCH' && typeof body.name === 'string') row.name = body.name
      const detail: ContainerDetail = { ...row, pid: row.state === 'running' ? 12984 : 0, entrypoint: ['/docker-entrypoint.sh'], working_directory: '/app', restart_policy: 'unless-stopped', environment: [{ key: 'APP_ENV', value: 'production', sensitive: false }, { key: 'DATABASE_PASSWORD', sensitive: true }], mounts: [{ type: 'volume', name: 'gateway-prod_data', source: '/var/lib/docker/volumes/gateway-prod_data/_data', destination: '/data', read_write: true }], networks: [{ name: 'gateway-prod_default', ip_address: '172.22.0.3', gateway: '172.22.0.1', mac_address: '02:42:ac:16:00:03' }] }
      return clone(detail) as T
    }

    if (suffix === '/images' && method === 'GET') return clone(images) as T
    if (suffix === '/images/pull') return clone({ id: 'task-pull-demo', type: 'image_pull', name: 'Pull demo image', status: 'success', progress: 100, message: 'Image pulled' }) as T
    if (suffix.startsWith('/images/') && method === 'GET') return clone(images.find((item) => suffix.includes(item.id)) ?? images[0]) as T
    if (suffix.startsWith('/images/')) return {} as T

    if (suffix === '/networks' && method === 'GET') return clone(networks) as T
    if (suffix.startsWith('/networks/') && method === 'GET') return clone(networks.find((item) => item.id === suffix.slice('/networks/'.length)) ?? networks[0]) as T
    if (suffix.startsWith('/networks')) return {} as T
    if (suffix === '/volumes' && method === 'GET') return clone(volumes) as T
    if (suffix.startsWith('/volumes')) return {} as T

    if (suffix === '/projects' && method === 'GET') return clone(projects.map((item) => ({ ...item, node_id: nodeID, scope: { kind: 'engine', id: nodeID }, ref: { ...item.ref, scope: { kind: 'engine', id: nodeID } } }))) as T
    if (suffix === '/projects' && method === 'POST') return clone(projectDetail(projects[0])) as T
    if (suffix === '/projects/batch') return {} as T
    const projectMatch = suffix.match(/^\/projects\/compose\/([^/]+)(\/.*)?$/)
    if (projectMatch) {
      const name = projectMatch[1]
      const rest = projectMatch[2] || ''
      const row = projects.find((item) => item.name === name) ?? projects[0]
      if (!rest && method === 'GET') return clone(projectDetail(row)) as T
      if (!rest && method === 'DELETE') {
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
      if (rest === '/takeover/shadow' && method === 'POST') return clone({ session_id: 'shadow-demo', preview_project: `${name}-preview`, expires_at: now, task: { id: 'task-shadow-demo', status: 'success', progress: 100, message: 'Preview ready' } } satisfies ShadowPreviewSession) as T
      if (rest.startsWith('/takeover/shadow/')) return clone({ session_id: 'shadow-demo', preview_project: `${name}-preview`, expires_at: now, containers: '1 running', logs: 'Preview service is healthy' } satisfies ShadowPreviewStatus) as T
      if (rest.startsWith('/actions/')) return clone({ id: `task-${name}`, scope: 'node', node_id: nodeID, type: `compose.${rest.slice('/actions/'.length)}`, name: `Compose ${name}`, status: 'success', progress: 100, message: 'Operation completed', created_at: now }) as T
      if (rest === '/cleanup' && method === 'POST') return clone({ id: `task-cleanup-${name}`, scope: 'node', node_id: nodeID, type: 'project.cleanup', name: `Clean ${name}`, status: 'success', progress: 100, message: 'Project resources removed', created_at: now }) as T
      return {} as T
    }
    if (suffix === '/system/prune') return clone({ id: 'task-prune-demo', type: 'system_prune', name: 'Docker system prune', status: 'success', progress: 100, message: 'Reclaimed 512 MB', created_at: now }) as T
  }

  return {} as T
}

export function subscribeDemoStream(stream: DemoStream, onMessage: (value: string) => void): () => void {
  if (stream === 'logs') {
    const lines = ['2026-08-29T16:30:01Z INFO  SUMA demo container started', '2026-08-29T16:30:03Z INFO  connected to gateway-prod_default', '2026-08-29T16:30:05Z INFO  GET /health 200 1ms']
    lines.forEach((line, index) => setTimeout(() => onMessage(line), index * 120))
    const timer = window.setInterval(() => onMessage(`${new Date().toISOString()} INFO  GET /api/health 200 2ms`), 4_000)
    return () => window.clearInterval(timer)
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
