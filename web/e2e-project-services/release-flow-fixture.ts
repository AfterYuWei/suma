import type { Page } from '@playwright/test'
import type { ReleasePlan } from '../src/features/delivery/release-flow'
import type { CDConfiguration, DeliveryRecord, DeliveryRelease } from '../src/features/delivery/types'

export async function setupReleaseFlow(page: Page, options: {language?: string; theme?: string; width?: number; fail?: boolean; changed?: boolean; expired?: boolean; conflict?: boolean; sameCommit?: boolean; running?: boolean; legacy?: boolean; failureReason?: string} = {}) {
  const language = options.language ?? 'zh-CN', theme = options.theme ?? 'dark'
  await page.setViewportSize({width: options.width ?? 1440, height: 1000})
  await page.addInitScript(({language, theme}) => {localStorage.setItem('suma-language', language); localStorage.setItem('suma-theme', theme); localStorage.setItem('suma-node', 'local')}, {language, theme})
  const writes: {path: string; body: Record<string, unknown>}[] = []
  const errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  const configuration: CDConfiguration = {configured: true, automation_enabled: false, repository: {clone_url: 'https://git.example.test/app.git', ref_type: 'branch', ref: 'main', authentication: {source: 'none'}, compose_files: ['compose.yml'], environment_file: ''}, reconcile_mode: 'manual', sync_interval_seconds: 300, desired_commit: 'b'.repeat(40), observed_commit: 'b'.repeat(40), deployment_timeout: 120, auto_rollback: false, webhook_enabled: false, node_ids: ['local', 'other'], registry_credential_ids: []}
  const record: DeliveryRecord = {id: 184, project_id: 1, release_id: 1, operation: 'deploy', actor_name: 'Operator', trigger_type: 'manual', status: 'succeeded', snapshot_complete: true, images_pinned: options.legacy !== true, config_hash: 'baseline', image_references: '[]', compose_files: '["compose.yml"]', environment_file: '', environment: [], node_ids: ['local', 'other'], registry_credential_ids: [], deployment_timeout: 120, auto_rollback: false, commit_sha: 'a'.repeat(40), commit_message: 'Baseline', created_at: '2026-11-14T00:00:00Z', attempts: []}
  record.attempts = ['local', 'other'].map((node_id, index) => ({id: index + 1, deployment_id: index + 1, operation: 'deploy', target_release_id: 1, node_id, node_name: node_id, status: 'succeeded', created_at: record.created_at}))
  const records: DeliveryRecord[] = [record]
  const release: DeliveryRelease = {id: 1, project_id: 1, repository_url: configuration.repository.clone_url, git_ref: 'main', commit_sha: 'b'.repeat(40), commit_message: 'Fix session recovery', commit_author: 'Operator', config_hash: 'next', image_references: '[]', status: 'ready', trigger_type: 'manual', trigger_actor: 'Operator', compose_files: '["compose.yml"]', created_at: record.created_at, updated_at: record.created_at, remediation: {retry_failed_node_ids: [], rollback_failed_node_ids: []}}
  const tasks = new Map<string, {id: string; status: string; message: string; result?: {record_id: number}}>()
  const plans = new Map<string, ReleasePlan>()
  let preparations = 0, confirmations = 0, runningID = ''
  await page.route('**/api/v1/**', async route => {
    const request = route.request(), url = new URL(request.url()), path = url.pathname.replace('/api/v1', '')
    const body: Record<string, unknown> = request.method() === 'GET' ? {} : request.postDataJSON() ?? {}
    if (request.method() !== 'GET') writes.push({path, body})
    let data: unknown = []
    if (path === '/auth/status') data = {needs_setup: false}
    else if (path === '/auth/session') data = {id: 1, username: 'admin', has_avatar: false}
    else if (path === '/settings') data = {'general.timezone': 'Asia/Shanghai'}
    else if (path === '/nodes') data = ['local', 'other'].map(id => ({id, name: id === 'local' ? 'Local' : 'Other', enabled: true, connection_type: 'unix', status: 'online', group_ids: []}))
    else if (path === '/notifications/inbox') data = {items: [], unread: 0}
    else if (path === '/delivery-projects/app') data = {id: 1, name: 'app', configured: true, automation_enabled: false, node_ids: ['local', 'other'], created_at: record.created_at, updated_at: record.created_at}
    else if (path.endsWith('/configuration')) data = configuration
    else if (path.endsWith('/releases')) data = [release]
    else if (path.endsWith('/drift')) data = {status: 'healthy', runtime_healthy: true, checked_at: record.created_at, nodes: ['local', 'other'].map(node_id => ({node_id, node_name: node_id, status: 'healthy', runtime_available: true, runtime_healthy: true, health_checks: 1, active_commit: record.commit_sha}))}
    else if (path.endsWith('/records')) data = {items: records.slice(0, Number(url.searchParams.get('page_size') ?? 20)), total: records.length, page: 1, page_size: 20, counts: {deploy: records.length}}
    else if (path.endsWith('/release-plans') && request.method() === 'POST') {
      preparations++
      const operation = typeof body.operation === 'string' ? body.operation : 'deploy'
      const id = String(preparations).padStart(32, '0'), taskID = `prepare-${preparations}`
      const targetIDs = operation === 'retry' || operation === 'restore-failed' ? ['other'] : ['local', 'other']
      const plan: ReleasePlan = {id, expires_at: new Date(Date.now() + (options.expired ? -1000 : 600000)).toISOString(), operation, source_record_id: typeof body.source_record_id === 'number' ? body.source_record_id : undefined, release_id: 1, commit_sha: (options.sameCommit || options.changed === false || operation === 'restore' || operation === 'restore-failed' ? 'a' : 'b').repeat(40), commit_message: 'Fix session recovery', compose_files: ['compose.yml'], images_pinned: true, auto_rollback: false, deployment_timeout: 120, nodes: targetIDs.map(node_id => ({node_id, node_name: node_id === 'local' ? 'Local' : 'Other', previous_record_id: 184, previous_commit: 'a'.repeat(40), changed: options.changed !== false, runtime_healthy: true, health_checked: true, can_restore: true, compose_before: '{"services":{"app":{"image":"example/app:latest"}}}', compose_after: '{"services":{"app":{"image":"example/app:latest"}}}', images: [{service: 'app', declared: 'example/app:latest', previous: 'example/app@sha256:' + '6'.repeat(64), reference: 'example/app@sha256:' + (options.changed === false || operation === 'restore' ? '6' : '7').repeat(64), platform: 'linux/amd64', changed: options.changed !== false}], services: [{name: 'app', action: 'recreate'}], environment: [{name: 'API_TOKEN', action: 'modify'}]}))}
      plans.set(id, plan); tasks.set(taskID, {id: taskID, status: 'success', message: 'Prepared'}); data = tasks.get(taskID)
    } else if (path.endsWith('/release-plans') && request.method() === 'GET') data = plans.get((url.searchParams.get('task_id')?.replace('prepare-', '') ?? '').padStart(32, '0'))
    else if (/\/release-plans\/[^/]+$/.test(path)) data = plans.get(path.split('/').at(-1) ?? '')
    else if (path.endsWith('/confirm')) {
      if (options.conflict && preparations === 1) return route.fulfill({status: 409, json: {code: 17730, message: 'Configuration changed; prepare again', data: null}})
      confirmations++
      const requestPlan = plans.get(path.split('/').at(-2) ?? '')
      const nodeIDs = Array.isArray(body.node_ids) ? body.node_ids.filter((value): value is string => typeof value === 'string') : []
      const failed = options.fail && confirmations === 1
      const id = `publish-${confirmations}`, recordID = 184 + confirmations
      tasks.set(id, {id, status: options.running ? 'running' : failed ? 'failed' : 'success', message: failed ? 'Other: ' + (options.failureReason ?? 'health check timed out') : options.running ? 'Pulling deployment images' : 'Completed', result: {record_id: recordID}})
      const attempts: DeliveryRecord['attempts'] = nodeIDs.map((node_id, index) => ({id: recordID * 10 + index, deployment_id: index + 1, operation: 'deploy', target_release_id: 1, node_id, node_name: node_id === 'local' ? 'Local' : 'Other', status: failed && node_id === 'other' ? 'failed' : 'succeeded', failure_reason: failed && node_id === 'other' ? options.failureReason ?? 'Health check timed out' : '', created_at: record.created_at}))
      records.unshift({...record, id: recordID, operation: requestPlan?.operation === 'retry' ? 'retry' : requestPlan?.operation === 'restore' || requestPlan?.operation === 'reapply' ? 'manual_rollback' : requestPlan?.operation === 'restore-failed' ? 'rollback_failed' : 'deploy', source_record_id: requestPlan?.source_record_id, task_id: id, status: failed ? 'partial_failed' : 'succeeded', failure_reason: failed ? options.failureReason ?? 'Health check timed out' : '', attempts, commit_sha: requestPlan?.commit_sha ?? record.commit_sha})
      runningID = id
      data = {task: tasks.get(id), record_id: recordID}
    } else if (path.endsWith('/children')) data = records[0].attempts.map(attempt => ({id: `node-${attempt.id}`, node_name: attempt.node_name, status: attempt.status === 'succeeded' ? 'success' : 'failed', message: attempt.failure_reason || 'Completed'}))
    else if (path.endsWith('/logs')) data = [{id: 1, message: 'Pulling pinned image'}, {id: 2, message: 'Health check completed'}]
    else if (path.startsWith('/tasks/')) data = tasks.get(path.split('/').at(-1) ?? '')
    await route.fulfill({json: {code: 0, message: '', data}})
  })
  await page.goto('/continuous-delivery/app')
  return {writes, errors, plans, records, configuration, get preparations() {return preparations}, get confirmations() {return confirmations}, finish: () => {const task = tasks.get(runningID); if (task) {task.status = 'success'; task.message = 'Completed'}}}
}

