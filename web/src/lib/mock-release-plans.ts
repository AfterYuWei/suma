import { ApiError } from './api-error'
import type { ReleasePlan, ReleaseRequest } from '../features/delivery/release-flow'
import type { TaskRecord } from '../features/task-center/types'

interface MockPlan {
  owner: string
  version: string
  view: ReleasePlan
  preparation: TaskRecord
  result?: TaskRecord
  execute: (nodes: string[], force: boolean) => TaskRecord
}
interface PlanDraft { view: Omit<ReleasePlan, 'id' | 'expires_at'>; execute: MockPlan['execute'] }
const plans = new Map<string, MockPlan>()

export function mockReleasePlans(owner: string, suffix: string, method: string, body: Record<string, unknown>, taskID: string | null, version: string, createTask: (result: {plan_id: string}) => TaskRecord, prepare: (request: ReleaseRequest) => PlanDraft) {
  if (suffix === '/release-plans' && method === 'POST') {
    const operation = body.operation ?? 'deploy'
    if (operation !== 'deploy' && operation !== 'retry' && operation !== 'restore' && operation !== 'restore-failed' && operation !== 'reapply') throw new ApiError('Invalid release operation', 17731, 400)
    const request: ReleaseRequest = {operation, source_record_id: typeof body.source_record_id === 'number' ? body.source_record_id : undefined, release_id: typeof body.release_id === 'number' ? body.release_id : undefined}
    const draft = prepare(request)
    const id = crypto.randomUUID().replaceAll('-', '')
    const view: ReleasePlan = {...draft.view, id, expires_at: new Date(Date.now() + 600000).toISOString()}
    const preparation = createTask({plan_id: id})
    plans.set(id, {owner, version, view, preparation, execute: draft.execute})
    return preparation
  }
  const id = suffix.split('/')[2]
  const plan = id ? plans.get(id) : [...plans.values()].find(plan => plan.owner === owner && plan.preparation.id === taskID)
  if (!plan || plan.owner !== owner) throw new ApiError('Release plan not found', 17730, 404)
  if (method === 'GET') return plan.view
  if (suffix.endsWith('/confirm') && method === 'POST') {
    if (plan.result) return plan.result
    if (plan.version !== version || new Date(plan.view.expires_at).getTime() <= Date.now()) throw new ApiError('Release plan expired or configuration changed; prepare again', 17730, 409)
    const selected = body.node_ids === undefined ? plan.view.nodes.map(node => node.node_id) : body.node_ids
    if (!Array.isArray(selected) || !selected.length || !selected.every((id): id is string => typeof id === 'string') || new Set(selected).size !== selected.length || selected.some(id => !plan.view.nodes.some(node => node.node_id === id))) throw new ApiError('Invalid release targets', 17731, 400)
    if (plan.view.operation === 'deploy' && !body.force_recreate && plan.view.nodes.filter(node => selected.includes(node.node_id)).every(node => !node.changed)) throw new ApiError('No pending changes; explicitly confirm recreation', 17730, 422)
    plan.result = plan.execute(selected, body.force_recreate === true)
    return plan.result
  }
  throw new ApiError('Unknown release operation', 17731, 400)
}

export function mockPlanNode(id: string, name: string, commit: string, previousRecord?: number): ReleasePlan['nodes'][number] {
  return {node_id: id, node_name: name, previous_record_id: previousRecord, previous_commit: previousRecord ? commit : '', changed: true, runtime_healthy: !!previousRecord, health_checked: true, can_restore: !!previousRecord, compose_before: '', compose_after: '{"services":{"app":{"image":"busybox:stable"}}}', images: [{service: 'app', declared: 'busybox:stable', previous: '', reference: `docker.io/library/busybox@sha256:${'7'.repeat(64)}`, platform: 'linux/amd64', changed: true}], services: [{name: 'app', action: previousRecord ? 'recreate' : 'create'}], environment: []}
}
