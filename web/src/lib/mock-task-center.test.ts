import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import type { Schedule, ScheduleRun, TaskRecord, KindInfo } from '../features/task-center/types'
import type { CDConfiguration, CDConfigureInput } from '../features/delivery/types'
import type { ProjectCDConfiguration } from '../features/project-cd/queries'
import type { View as CleanupView, Run as CleanupRun, Preview } from '../features/cleanup/types'
import type { DockerNode } from './nodes'
import type { DeliveryTaskEvidence } from '../features/task-center/business-results'

// Notification presentation owns browser theme state; these integration cases
// exercise the real task-center, cleanup and CD mocks without that UI store.
vi.mock('../features/notifications/mock', () => ({ mockNotifications: () => undefined, persistMockNotifications: () => {} }))

let api: typeof import('./mock-api').demoApi
const write = (method: string, body: unknown): RequestInit => ({ method, body: JSON.stringify(body) })

beforeEach(async () => {
  vi.resetModules()
  const saved = new Map([['suma-demo-session', '1']])
  vi.stubGlobal('sessionStorage', { getItem: (key: string) => saved.get(key) ?? null, setItem: (key: string, value: string) => saved.set(key, value), removeItem: (key: string) => saved.delete(key) })
  api = (await import('./mock-api')).demoApi
})
afterEach(() => vi.unstubAllGlobals())

describe('demo unified automation', () => {
  test('global CD editors share versions and preserve timer intent', async () => {
    const kinds = await api<KindInfo[]>('/task-center/kinds')
    expect(kinds.map(kind => kind.type).sort()).toEqual(['cd.sync', 'image.update', 'project.cd.check', 'system.cleanup'])
    const configuration = await api<CDConfiguration>('/delivery-projects/gateway-prod/configuration')
    const [rule] = await api<Schedule[]>('/task-center/schedules?kind=cd.sync&scope=control_plane')
    expect(configuration.automation_version).toBe(rule.version)
    const changed = await api<Schedule>(`/task-center/schedules/${rule.id}`, write('PUT', { expected_version: rule.version, interval_seconds: 35, timer_enabled: false }))
    const current = await api<CDConfiguration>('/delivery-projects/gateway-prod/configuration')
    expect(current).toMatchObject({ automation_version: changed.version, sync_interval_seconds: 35, automation_enabled: true, webhook_enabled: true })
    const stale: CDConfigureInput = { ...configuration, webhook_secret: '', expected_automation_version: configuration.automation_version }
    await expect(api('/delivery-projects/gateway-prod/configuration', write('PUT', stale))).rejects.toMatchObject({ status: 409 })
    const saved: CDConfigureInput = { ...current, webhook_secret: '', sync_interval_seconds: 37, expected_automation_version: current.automation_version }
    await api('/delivery-projects/gateway-prod/configuration', write('PUT', saved))
    const latest = await api<Schedule>(`/task-center/schedules/${rule.id}`)
    expect(latest).toMatchObject({ interval_seconds: 37, timer_enabled: false, enabled: true })
  })

  test('manual CD checks retain candidate evidence without publishing', async () => {
    const before = await api<CDConfiguration>('/delivery-projects/gateway-prod/configuration')
    const task = await api<TaskRecord>('/delivery-projects/gateway-prod/sync', write('POST', {}))
    await vi.waitFor(async () => expect((await api<TaskRecord>(`/tasks/${task.id}`)).status).toBe('success'))
    const evidence = await api<{ records: { total: number }; releases: { id: number; status: string }[] }>(`/delivery-records/by-task/${task.id}`)
    expect(evidence.records.total).toBe(0)
    expect(evidence.releases).toHaveLength(1)
    expect(evidence.releases[0].status).toBe('awaiting_approval')
    expect((await api<CDConfiguration>('/delivery-projects/gateway-prod/configuration')).active_release_id).toBe(before.active_release_id)
    const rule = (await api<Schedule[]>('/task-center/schedules?kind=cd.sync')).find(row => row.subject === 'gateway-prod')!
    const history = await api<{ total: number; items: ScheduleRun[] }>(`/task-center/schedules/${rule.id}/runs?offset=0`)
    expect(history.items[0]).toMatchObject({ task_id: task.id, status: 'started', triggered_by: 'manual', actor_id: 1 })
  })

  test('project CD config creates one paused rule and center edits invalidate its revision', async () => {
    const path = '/nodes/local/projects/compose/media-stack/gitops'
    const configuration = await api<ProjectCDConfiguration>(path, write('PUT', { expected_revision: 0, automation_enabled: false, sync_interval_seconds: 35, webhook_enabled: true, repository: { clone_url: 'https://git.example.test/project.git', ref_type: 'branch', ref: 'main', compose_files: ['compose.yml'], environment_file: '', authentication: { source: 'none' } } }))
    const [rule] = await api<Schedule[]>('/task-center/schedules?kind=project.cd.check&node_id=local')
    expect(rule).toMatchObject({ enabled: false, interval_seconds: 35, subject: 'media-stack' })
    await api(`/task-center/schedules/${rule.id}`, write('PUT', { expected_version: rule.version, interval_seconds: 37 }))
    await expect(api(`${path}/automation`, write('PUT', { expected_revision: configuration.revision, enabled: false }))).rejects.toMatchObject({ status: 409 })
    expect((await api<ProjectCDConfiguration>(path)).sync_interval_seconds).toBe(37)
  })

  test('cleanup keeps deletion authorization and separates timer pause from master pause', async () => {
    const view = await api<CleanupView>('/nodes/local/cleanup/policy')
    let policy = await api<CleanupView['policy']>('/nodes/local/cleanup/policy', write('PUT', { ...view.policy, enabled: false }))
    let [rule] = await api<Schedule[]>('/task-center/schedules?kind=system.cleanup&node_id=local')
    await expect(api(`/task-center/schedules/${rule.id}`, write('PUT', { expected_version: rule.version, enabled: true, timer_enabled: true }))).rejects.toMatchObject({ status: 422 })
    expect((await api<CleanupView>('/nodes/local/cleanup/policy')).policy.enabled).toBe(false)
    const node = (await api<DockerNode[]>('/nodes')).find(node => node.id === 'local')!
    policy = await api('/nodes/local/cleanup/policy', write('PUT', { ...policy, enabled: true, confirmation_name: node.name, authorize: true }))
    ;[rule] = await api<Schedule[]>('/task-center/schedules?kind=system.cleanup&node_id=local')
    const paused = await api<Schedule>(`/task-center/schedules/${rule.id}`, write('PUT', { expected_version: rule.version, timer_enabled: false }))
    expect(paused).toMatchObject({ enabled: true, timer_enabled: false })
    expect((await api<CleanupView>('/nodes/local/cleanup/policy')).policy).toMatchObject({ enabled: false, authorized_at: policy.authorized_at })
  })

  test('cleanup Task links original evidence and preserves volumes', async () => {
    const node = (await api<DockerNode[]>('/nodes')).find(node => node.id === 'edge-hk')!
    const preview = await api<Preview>(`/nodes/${node.id}/cleanup/preview`, write('POST', {}))
    const task = await api<TaskRecord>(`/nodes/${node.id}/cleanup/run`, write('POST', { preview_id: preview.id, confirmation_name: node.name }))
    const evidence = await api<CleanupRun>(`/cleanup/runs/by-task/${task.id}`)
    expect(evidence.task_id).toBe(task.id)
    expect(evidence.result.outcomes.filter(outcome => outcome.kind === 'volume').every(outcome => outcome.status === 'scanned')).toBe(true)
    const records = await api<{ items: TaskRecord[] }>('/task-center/records?scope=node&node_id=edge-hk&status=finished')
    expect(records.items.some(row => row.id === task.id)).toBe(true)
  })

  test('image checks, updates, archive and replacement preserve task history', async () => {
    const [rule] = await api<Schedule[]>('/task-center/schedules?kind=image.update&node_id=local')
    expect(rule.enabled).toBe(false)
    const started = await api<{ task_id: string }>('/task-center/tasks', write('POST', { kind: 'image.update', node_id: 'local', params: { project: 'media-stack', services: ['server'], auto_apply: true } }))
    await vi.waitFor(async () => expect((await api<TaskRecord>(`/tasks/${started.task_id}`)).status).toBe('success'))
    expect(await api<TaskRecord[]>(`/tasks/${started.task_id}/children`)).toHaveLength(1)
    await expect(api(`/task-center/schedules/${rule.id}?expected_version=0`, { method: 'DELETE' })).rejects.toMatchObject({ status: 409 })
    await api(`/task-center/schedules/${rule.id}?expected_version=${rule.version}`, { method: 'DELETE' })
    expect((await api<Schedule>(`/task-center/schedules/${rule.id}`)).archived_at).toBeTruthy()
    expect((await api<{ total: number }>(`/task-center/schedules/${rule.id}/runs?offset=0`)).total).toBe(1)
    const replacement = await api<Schedule>('/task-center/schedules', write('POST', { name: 'Replacement', kind: rule.kind, node_id: rule.node_id, subject: rule.subject, params: rule.params, cron: '0 4 * * *', timezone: 'UTC', enabled: false }))
    expect(replacement.id).not.toBe(rule.id)
    expect(replacement.enabled).toBe(false)
  })

  test('history filters inherit trigger evidence and retain archived rule references', async () => {
    const [rule] = await api<Schedule[]>('/task-center/schedules?kind=image.update&node_id=local')
    const started = await api<{ task_id: string }>(`/task-center/schedules/${rule.id}/run`, write('POST', { action: 'update' }))
    await vi.waitFor(async () => expect((await api<TaskRecord>(`/tasks/${started.task_id}`)).status).toBe('success'))
    await api(`/task-center/schedules/${rule.id}?expected_version=${rule.version}`, { method: 'DELETE' })
    const page = await api<{ items: TaskRecord[]; total: number }>(`/task-center/records?scope=node&node_id=local&source=manual&schedule_id=${rule.id}&view=summary`)
    expect(page.total).toBe(2)
    expect(page.items.every(task => task.triggered_by === 'manual' && task.schedule_id === rule.id && task.run_id && !task.result && !task.params)).toBe(true)
    const child = page.items.find(task => task.parent_task_id === started.task_id)!
    expect((await api<TaskRecord>(`/tasks/${child.id}`)).triggered_by).toBe('manual')
    const found = await api<{ total: number }>(`/task-center/records?source=manual&schedule_id=${rule.id}&search=${encodeURIComponent(child.name.toUpperCase())}`)
    expect(found.total).toBe(1)
    expect((await api<{ total: number }>(`/task-center/records?source=unrecorded&schedule_id=${rule.id}`)).total).toBe(0)
  })

  test('trigger history exposes ad-hoc busy refusals without a Task', async () => {
    const input = { kind: 'image.update', node_id: 'local', params: { project: 'gateway-prod', auto_apply: false } }
    const admitted = await Promise.allSettled([api('/task-center/tasks', write('POST', input)), api('/task-center/tasks', write('POST', input))])
    expect(admitted[0].status).toBe('fulfilled')
    expect(admitted[1]).toMatchObject({ status: 'rejected', reason: { status: 409 } })
    const page = await api<{ items: ScheduleRun[]; total: number }>('/task-center/runs?scope=node&node_id=local&source=manual&status=skipped&view=summary&limit=1&offset=0')
    expect(page.total).toBe(1)
    expect(page.items[0]).toMatchObject({ status: 'skipped', reason_code: 'busy', task_id: '', kind: 'image.update', subject: 'gateway-prod' })
    expect(page.items[0].schedule_id).toBeUndefined()
    expect(page.items[0].rule_snapshot).toBeUndefined()
    const detail = await api<ScheduleRun>(`/task-center/runs/${page.items[0].id}`)
    expect(detail.rule_snapshot).toMatchObject({ kind: 'image.update', node_id: 'local' })
    expect((await api<{ total: number }>('/task-center/runs?scope=control_plane&status=skipped')).total).toBe(0)
  })

  test('manual CD publication records its Task and retains the original candidate check', async () => {
    const checked = await api<TaskRecord>('/delivery-projects/gateway-prod/sync', write('POST', {}))
    await vi.waitFor(async () => expect((await api<TaskRecord>(`/tasks/${checked.id}`)).status).toBe('success'))
    const before = await api<DeliveryTaskEvidence>(`/delivery-records/by-task/${checked.id}`)
    expect(before.releases).toHaveLength(1)
    expect(before.records.total).toBe(0)
    const releaseID = before.releases[0].id
    const published = await api<{ task: TaskRecord; record_id: number }>(`/delivery-projects/gateway-prod/releases/${releaseID}/deploy`, write('POST', {}))
    expect((await api<TaskRecord>(`/tasks/${published.task.id}`)).result).toMatchObject({ release_id: releaseID, record_id: published.record_id, operation: 'deploy' })
    const children = await api<TaskRecord[]>(`/tasks/${published.task.id}/children`)
    expect(children.map(child => child.node_id).sort()).toEqual(['edge-hk', 'local'])
    expect(children.every(child => child.parent_task_id === published.task.id && !child.result)).toBe(true)
    expect((await api<DeliveryTaskEvidence>(`/delivery-records/by-task/${checked.id}`)).releases[0].id).toBe(releaseID)
    const after = await api<DeliveryTaskEvidence>(`/delivery-records/by-task/${published.task.id}`)
    expect(after.records.items[0]).toMatchObject({ record_id: published.record_id, task_id: published.task.id, source: 'continuous_delivery' })
    expect((await api<{ total: number }>(`/delivery-records?task_id=${published.task.id}`)).total).toBe(1)
    await expect(api(`/delivery-projects/media-stack/releases/${releaseID}/deploy`, write('POST', {}))).rejects.toMatchObject({ status: 404 })
  })

  test('manual project-CD apply returns the same Task recorded in business history', async () => {
    const path = '/nodes/local/projects/compose/media-stack/gitops'
    await api(path, write('PUT', { expected_revision: 0, automation_enabled: false, sync_interval_seconds: 35, repository: { clone_url: 'https://git.example.test/project.git', ref_type: 'branch', ref: 'main', compose_files: ['compose.yml'], environment_file: '', authentication: { source: 'none' } } }))
    const task = await api<TaskRecord>(`${path}/apply`, write('POST', {}))
    expect(task).toMatchObject({ scope: 'node', node_id: 'local', type: 'project.cd.apply' })
    expect((await api<TaskRecord>(`/tasks/${task.id}`)).result).toMatchObject({ operation: 'apply', record_id: 1 })
    const evidence = await api<DeliveryTaskEvidence>(`/delivery-records/by-task/${task.id}`)
    expect(evidence.records.items[0]).toMatchObject({ record_id: 1, task_id: task.id, source: 'project_cd' })
    expect((await api<{ items: TaskRecord[] }>('/task-center/records?scope=node&node_id=local&status=finished')).items.some(row => row.id === task.id)).toBe(true)
    const detached = await api<TaskRecord>(`${path}/detach`, write('POST', {}))
    expect((await api<DeliveryTaskEvidence>(`/delivery-records/by-task/${detached.id}`)).records.items[0].operation).toBe('detach')
    expect((await api<Schedule[]>('/task-center/schedules?kind=project.cd.check&include_archived=true')).every(rule => !!rule.archived_at)).toBe(true)
  })

  test('ordinary operations enter Task history and unknown IDs never fabricate success', async () => {
    await expect(api('/tasks/nonexistent-task')).rejects.toMatchObject({ status: 404 })
    const pulled = await api<TaskRecord>('/nodes/local/images/pull', write('POST', { reference: 'nginx:alpine' }))
    const pruned = await api<TaskRecord>('/nodes/local/system/prune', write('POST', {}))
    for (const task of [pulled, pruned]) expect((await api<TaskRecord>(`/tasks/${task.id}`)).id).toBe(task.id)
    const rows = await api<TaskRecord[]>('/nodes/local/tasks?view=summary')
    expect(rows.filter(row => row.id === pulled.id || row.id === pruned.id)).toHaveLength(2)
    expect(rows.every(row => !row.params && !row.result)).toBe(true)
  })
})
