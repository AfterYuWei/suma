import { ApiError } from './api'
import type { ImageUpdatePolicy, ImageUpdateResult } from '../features/image-updates/types'
import type { LogEvent, LogRecord, LogSource } from '../features/project-logs/types'
const policies = new Map<string, ImageUpdatePolicy>()
const checked = new Set<string>()
export function demoLogSources(project: string): LogSource[] {
 return ['web', 'api'].map(service => ({ container_id: `demo-${project}-${service}`, container_name: `${project}-${service}-1`, service, instance: 1, state: 'running', one_off: false, orphan: false }))
}
function records(project: string, count = 20): LogRecord[] { const sources = demoLogSources(project); return Array.from({ length: count }, (_, index) => { const source = sources[index % sources.length]; return { id: `${project}/history/${index}`, time: new Date(Date.now() - (count - index) * 1000).toISOString(), service: source.service, container_id: source.container_id, container_name: source.container_name, stream: index % 5 === 0 ? 'stderr' : 'stdout', text: index % 5 === 0 ? 'retrying upstream connection' : `request ${index} completed status=200` } }) }
export function operationsDemo(nodeID: string, suffix: string, method: string, body: Record<string, unknown>, url: URL, images: { id: string; tags: string[] }[]): { handled: boolean; value?: unknown } {
 if (suffix.startsWith('/image-updates')) {
  const policy = policies.get(nodeID) || { version: 0, enabled: false, interval_hours: 6, registry_credentials: {} }
  if (suffix === '/image-updates/policy') {
   if (method === 'PUT') { if (body.expected_version !== policy.version) throw new ApiError('Policy changed; reload before saving', 20702, 409); const next = { version: policy.version + 1, enabled: Boolean(body.enabled), interval_hours: Number(body.interval_hours), registry_credentials: (body.registry_credentials || {}) as Record<string, number>, next_run_at: body.enabled ? new Date(Date.now() + Number(body.interval_hours) * 3600000).toISOString() : undefined }; policies.set(nodeID, next); if (next.enabled) checked.add(nodeID); return { handled: true, value: next } }
   return { handled: true, value: policy }
  }
  if (suffix === '/image-updates/check') { checked.add(nodeID); return { handled: true, value: { id: `image-check-${Date.now()}`, status: 'success', progress: 100, message: 'Image check complete' } } }
  const project = url.searchParams.get('project_name')
  const results: ImageUpdateResult[] = images.map((image, index) => { const reference = image.tags?.[0] || ''; const registry = reference.includes('ghcr.io/') ? 'ghcr.io' : 'docker.io'; const update = checked.has(nodeID) && index === 0; return { reference, registry, platform: { os: 'linux', architecture: 'amd64' }, local_image_id: image.id, status: checked.has(nodeID) ? update ? 'update_available' : 'current' : 'unchecked', stale: false, pull_required: update, recreate_required: update, checked_at: checked.has(nodeID) ? new Date().toISOString() : undefined, remote_manifest_digest: checked.has(nodeID) ? `sha256:${'ab'.repeat(32)}` : undefined, containers: project ? [{ container_id: `demo-${project}-web`, container_name: `${project}-web-1`, project, service: 'web', state: 'running', image_id: image.id, reference }] : [] } })
  return { handled: true, value: { results } }
 }
 const match = suffix.match(/^\/projects\/compose\/([^/]+)\/(log-sources|logs\/history)$/)
 if (match) {
  const project = match[1]; const sources = demoLogSources(project)
  if (match[2] === 'log-sources') return { handled: true, value: sources }
  const selectedServices = url.searchParams.get('services')?.split(','); const selectedContainers = url.searchParams.get('containers')?.split(',')
  const since = url.searchParams.get('since'); const until = url.searchParams.get('until')
  const entries = records(project).filter(row => (!since || row.time >= since) && (!until || row.time <= until) && (!selectedServices || selectedServices.includes(row.service)) && (!selectedContainers || selectedContainers.includes(row.container_id))).slice(-Number(url.searchParams.get('tail') || 200))
  return { handled: true, value: { entries, sources, errors: [], truncated: false } }
 }
 return { handled: false }
}
export function demoProjectLogStream(project: string, onEvent: (event: LogEvent) => void, tail: number): () => void {
 const sources = demoLogSources(project); let index = 0
 onEvent({ type: 'snapshot', entries: records(project).slice(-tail), sources, errors: [], truncated: false })
 const timer = setInterval(() => { const source = sources[index % sources.length]; index++; onEvent({ type: 'entries', entries: [{ id: `${project}/live/${Date.now()}/${index}`, time: new Date().toISOString(), service: source.service, container_id: source.container_id, container_name: source.container_name, stream: index % 4 ? 'stdout' : 'stderr', text: index % 4 ? `live request ${index} completed status=200` : 'retrying upstream connection' }] }) }, 700)
 return () => clearInterval(timer)
}
