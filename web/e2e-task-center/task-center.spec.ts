import { expect, test, type Page } from '@playwright/test'
import type { CDConfiguration } from '../src/features/delivery/types'
import type { Schedule, TaskRecord } from '../src/features/task-center/types'

const created = '2026-11-12T00:00:00Z'
const rule = (id: number, kind: string, node_id?: string): Schedule => ({
  id, kind, name: `rule-${id}`, subject: kind === 'system.cleanup' ? '' : 'blog', node_id, node_name: node_id,
  params: kind === 'system.cleanup' ? {} : { project: 'blog', auto_apply: false, services: ['web'] },
  cron: kind.includes('cd.') ? '' : '0 4 * * *', timezone: 'UTC', enabled: true, timer_enabled: true,
  schedule_type: kind.includes('cd.') ? 'interval' : 'cron', interval_seconds: kind.includes('cd.') ? 30 : 0,
  webhook_enabled: kind.includes('cd.'), version: 2, effective_status: 'enabled', created_at: created, updated_at: created,
  next_run_at: '2026-11-14T04:00:00Z', last_run_status: 'skipped', last_run_reason: 'busy',
})

async function setup(page: Page, language = 'en-US', theme = 'dark') {
  await page.addInitScript(({ language, theme }) => {
    localStorage.setItem('suma-language', language); localStorage.setItem('suma-theme', theme)
    localStorage.setItem('suma-node', 'local'); localStorage.setItem('suma-list-page-size', '100')
  }, { language, theme })
  const rules = [rule(1, 'image.update', 'local'), rule(2, 'system.cleanup', 'local'), rule(3, 'cd.sync'), rule(4, 'project.cd.check', 'local')]
  const records: TaskRecord[] = Array.from({ length: 301 }, (_, i) => ({ id: `history-${i}`, name: `history-${i}`, scope: 'node', node_id: 'local', node_name: 'local', type: 'compose.update', status: 'success', progress: 100, message: '', created_at: created }))
  const parent: TaskRecord = { id: 'parent', name: 'Global parent', scope: 'control_plane', type: 'cd.sync', status: 'success', progress: 100, message: '', result_error: 'result_too_large', created_at: created }
  const child: TaskRecord = { ...records[0], id: 'child', name: 'Node child', parent_task_id: 'parent' }
  const active: TaskRecord = { ...records[0], id: 'active', name: 'Active check', status: 'running', progress: 30 }
  const configuration: CDConfiguration = { automation_version: 2, configured: true, automation_enabled: true, repository: { clone_url: 'https://git.example.test/blog.git', ref_type: 'branch', ref: 'main', authentication: { source: 'none' }, compose_files: ['compose.yml'], environment_file: '' }, reconcile_mode: 'manual', sync_interval_seconds: 30, desired_commit: '', observed_commit: '', auto_rollback: false, deployment_timeout: 60, webhook_enabled: true, webhook_id: 'hook', node_ids: ['local'], registry_credential_ids: [] }
  const state = { configuration, rules, records, requests: [] as URL[], writes: [] as Record<string, unknown>[], conflict: false }
  await page.route('**/api/v1/**', async route => {
    const req = route.request(), url = new URL(req.url()), path = url.pathname.replace('/api/v1', '')
    state.requests.push(url)
    let data: unknown = []
    if (path === '/auth/status') data = { needs_setup: false }
    else if (path === '/auth/session') data = { id: 1, username: 'admin', has_avatar: false }
    else if (path === '/settings') data = { 'general.timezone': 'UTC' }
    else if (path === '/nodes') data = ['local', 'edge'].map(id => ({ id, name: id, enabled: true, connection_type: 'unix', status: 'online', group_ids: [1] }))
    else if (path === '/node-groups') data = [{ id: 1, name: 'Default', is_default: true }]
    else if (path === '/notifications/inbox') data = { items: [], unread: 0 }
    else if (path === '/delivery-projects/blog') data = { id: 1, name: 'blog', configured: true, automation_enabled: true, automation_version: rules[2].version, node_ids: ['local'], created_at: created, updated_at: created }
    else if (path === '/delivery-projects/blog/configuration') {
      if (req.method() === 'PUT') {
        const body: Record<string, unknown> = req.postDataJSON(); state.writes.push(body)
        if (body.expected_automation_version !== rules[2].version) return route.fulfill({ status: 409, json: { code: 22003, message: 'Schedule changed concurrently; reload and retry', data: null } })
        Object.assign(configuration, body); rules[2].version++
      }
      data = { ...configuration, automation_version: rules[2].version, sync_interval_seconds: rules[2].interval_seconds }
    } else if (path === '/delivery-projects/blog/drift') data = { drifted: false, status: 'unknown', runtime_healthy: false, nodes: [], desired_commit: '', observed_commit: '', active_commit: '' }
    else if (path === '/task-center/kinds') data = rules.map(row => ({ type: row.kind, requires_node: !!row.node_id, schedule_types: [row.schedule_type], actions: [row.kind === 'system.cleanup' ? 'run' : 'check'], webhook: row.webhook_enabled, business_owned: row.kind !== 'image.update' }))
    else if (path === '/task-center/records') {
      const rows = (url.searchParams.get('status') === 'active' ? [active] : records).filter(row => (!url.searchParams.get('source') || row.triggered_by === url.searchParams.get('source')) && (!url.searchParams.get('schedule_id') || row.schedule_id === Number(url.searchParams.get('schedule_id'))) && (!url.searchParams.get('search') || row.name.includes(url.searchParams.get('search')!)))
      data = { total: rows.length, items: rows.slice(Number(url.searchParams.get('offset')), Number(url.searchParams.get('offset')) + Number(url.searchParams.get('limit'))) }
    } else if (path === '/task-center/schedules') data = rules.filter(row => (!url.searchParams.get('kind') || row.kind === url.searchParams.get('kind')) && (!url.searchParams.get('node_id') || row.node_id === url.searchParams.get('node_id')) && (url.searchParams.get('scope') !== 'control_plane' || !row.node_id))
    else if (path === '/task-center/schedules/preview') data = ['2026-11-14T04:00:00Z']
    else if (/^\/task-center\/schedules\/\d+$/.test(path)) {
      const row = rules.find(row => row.id === Number(path.split('/').pop()))!
      if (req.method() === 'PUT') {
        const body: Record<string, unknown> = req.postDataJSON(); state.writes.push(body)
        if (state.conflict) return route.fulfill({ status: 409, json: { code: 22003, message: 'Schedule changed concurrently; reload and retry', data: null } })
        Object.assign(row, body, { version: row.version + 1 })
      }
      data = row
    } else if (path.endsWith('/runs')) data = { total: 1, items: [{ id: 1, status: 'skipped', triggered_by: 'scheduled', reason_code: 'busy', error: 'Previous task active', task_id: '', created_at: created }] }
    else if (path === '/task-center/runs/1') data = { id: 1, status: 'skipped', triggered_by: 'scheduled', rule_snapshot: { kind: 'image.update', node_id: 'local' }, created_at: created }
    else if (path === '/task-center/image-updates') data = [{ project: 'blog', last_result: { project: 'blog', mode: 'check', services: [{ service: 'web', reference: 'nginx:latest', status: 'current', replicas: 1 }], finished_at: created } }]
    else if (path.endsWith('/children')) data = path.includes('/parent/') ? [child] : []
    else if (path.startsWith('/delivery-records/by-task/')) data = { records: { items: [], total: 0, page: 1, page_size: 20 }, releases: [] }
    else if (path === '/tasks/parent') data = parent
    else if (path === '/tasks/child') data = child
    else if (path === '/tasks/active') data = active
    else if (/^\/tasks\/history-/.test(path) && !path.endsWith('/logs') && !path.endsWith('/steps')) data = records.find(row => path.endsWith(row.id))
    return route.fulfill({ json: { code: 0, message: '', data } })
  })
  return state
}

for (const language of ['en-US', 'zh-CN']) for (const theme of ['dark', 'light']) for (const width of [390, 1440]) {
  test(`shared scope and registered kinds ${language} ${theme} ${width}`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 950 })
    const errors: string[] = []; page.on('pageerror', error => errors.push(error.message))
    const state = await setup(page, language, theme), zh = language === 'zh-CN'
    await page.goto('/tasks')
    await expect(page.getByText('Active check', { exact: true })).toBeVisible()
    await page.getByRole('tab', { name: zh ? '执行历史' : 'History', exact: true }).click()
    await expect(page.getByText('history-0', { exact: true })).toBeVisible()
    expect(state.requests.filter(url => url.pathname.endsWith('/records')).at(-1)?.searchParams.get('node_id')).toBe('local')
    await page.getByRole('tab', { name: zh ? '控制平面' : 'Control plane', exact: true }).click()
    await page.getByRole('tab', { name: zh ? '自动化' : 'Automation', exact: true }).click()
    await expect(page.getByText('rule-3', { exact: true })).toBeVisible()
    await expect(page.getByText('rule-1', { exact: true })).toHaveCount(0)
    await page.getByRole('tab', { name: zh ? '全部' : 'All', exact: true }).click()
    for (const row of state.rules) await expect(page.getByText(row.name, { exact: true })).toBeVisible()
    if (language === 'zh-CN' && theme === 'dark') await page.screenshot({ path: testInfo.outputPath(`suma-task-center-${width}.png`), fullPage: true, animations: 'disabled' })
    await page.getByRole('button', { name: zh ? '新建自动化' : 'New automation', exact: true }).click()
    const dialog = page.getByRole('dialog'), trigger = dialog.getByRole('combobox').first()
    await trigger.click()
    const menu = page.getByRole('listbox')
    await expect(menu).toBeVisible()
    await expect.poll(async () => { const t = await trigger.boundingBox(), m = await menu.boundingBox(); return { external: !!(t && m && (m.y >= t.y + t.height - 2 || m.y + m.height <= t.y + 2)), trigger: t, menu: m } }).toMatchObject({ external: true })
    await page.getByRole('option', { name: zh ? '全局 CD' : 'Global CD', exact: true }).click()
    await expect(dialog.getByRole('button', { name: zh ? '打开业务配置' : 'Open business settings' })).toHaveAttribute('href', '/continuous-delivery/new/project')
    expect(errors).toEqual([])
  })
}

test('server pagination reaches record 301 and detail works outside scope', async ({ page }) => {
  await setup(page)
  await page.goto('/tasks?tab=records')
  for (let i = 0; i < 3; i++) { await page.getByRole('button', { name: 'Next page', exact: true }).click(); await expect(page.getByText(`history-${(i + 1) * 100}`, { exact: true })).toBeVisible() }
  await page.getByText('history-300', { exact: true }).click()
  await expect(page.getByRole('dialog').getByRole('heading', { name: /^history-300/ })).toBeVisible()
  await page.goto('/tasks#parent')
  await expect(page.getByText('Result evidence unavailable', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: /Node child/ }).click()
  await expect(page.getByRole('button', { name: 'View parent task', exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'View parent task', exact: true }).click()
  await expect(page.getByRole('heading', { name: /^Global parent/ })).toBeVisible()
})

for (const width of [390, 1440]) test(`history filters reset pagination and preserve rule metadata at ${width}px`, async ({ page }) => {
  await page.setViewportSize({ width, height: 1000 })
  const state = await setup(page)
  Object.assign(state.records[0], { triggered_by: 'manual', schedule_id: 1, schedule_name: 'Archived rule', run_id: 10 })
  state.rules[0].archived_at = created
  await page.goto('/tasks?tab=records')
  await page.getByRole('button', { name: 'Next page', exact: true }).click()
  await expect(page.getByText('history-100', { exact: true })).toBeVisible()
  const trigger = page.getByRole('combobox', { name: 'Trigger source', exact: true })
  await trigger.click()
  await expect.poll(async () => { const t = await trigger.boundingBox(), m = await page.getByRole('listbox').boundingBox(); return !!(t && m && Math.abs(t.width - m.width) < 2 && m.y >= t.y + t.height - 2) }).toBe(true)
  await page.getByRole('option', { name: 'Manual', exact: true }).click()
  await expect(page.getByText('history-0', { exact: true })).toBeVisible()
  await page.getByRole('combobox', { name: 'Automation rule filter', exact: true }).click()
  await page.getByRole('option', { name: /#1 rule-1.*Archived/ }).click()
  await page.getByRole('textbox', { name: 'Search tasks', exact: true }).fill('history-0')
  await expect.poll(() => {
    const url = state.requests.filter(url => url.pathname.endsWith('/task-center/records')).at(-1)!
    return { offset: url.searchParams.get('offset'), source: url.searchParams.get('source'), rule: url.searchParams.get('schedule_id'), search: url.searchParams.get('search') }
  }).toEqual({ offset: '0', source: 'manual', rule: '1', search: 'history-0' })
  await expect(page.getByText('Manual · #1 Archived rule', { exact: true })).toBeVisible()
  await expect(page.getByRole('navigation', { name: 'List pagination' })).toContainText('1 / 1')
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
})

test('all trigger history filters scope and loads snapshots on demand', async ({ page }) => {
  const state = await setup(page)
  await page.goto('/tasks?tab=schedules')
  await page.getByRole('button', { name: 'All trigger history', exact: true }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByText('busy · Previous task active', { exact: true })).toBeVisible()
  expect(state.requests.some(url => url.pathname.endsWith('/runs/1'))).toBe(false)
  await dialog.getByRole('combobox', { name: 'Trigger source', exact: true }).click()
  await page.getByRole('option', { name: 'Scheduled', exact: true }).click()
  await dialog.getByRole('combobox', { name: 'Trigger status', exact: true }).click()
  await page.getByRole('option', { name: 'skipped', exact: true }).click()
  await expect.poll(() => {
    const url = state.requests.filter(url => url.pathname.endsWith('/task-center/runs')).at(-1)!
    return { scope: url.searchParams.get('scope'), node: url.searchParams.get('node_id'), source: url.searchParams.get('source'), status: url.searchParams.get('status'), view: url.searchParams.get('view') }
  }).toEqual({ scope: 'node', node: 'local', source: 'scheduled', status: 'skipped', view: 'summary' })
  await dialog.getByText('View trigger snapshot', { exact: true }).click()
  await expect(dialog.locator('pre')).toContainText('image.update')
})

test('image editor keeps the draft and version through polling and conflict', async ({ page }) => {
  const state = await setup(page)
  await page.goto('/tasks?tab=schedules')
  const row = page.getByRole('row').filter({ hasText: 'rule-1' })
  await row.getByRole('button', { name: 'Rule actions' }).click()
  await page.getByRole('menuitem', { name: 'Edit', exact: true }).click()
  const dialog = page.getByRole('dialog'), cron = dialog.getByRole('textbox').first()
  await expect(cron).toHaveValue('0 4 * * *')
  await cron.fill('0 7 * * *')
  await dialog.getByLabel('Service selection', { exact: true }).fill('web, worker')
  await page.clock.install(); await page.clock.fastForward(11000)
  await expect(cron).toHaveValue('0 7 * * *')
  state.conflict = true
  await dialog.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(dialog.getByRole('alert')).toContainText('concurrently')
  await expect(cron).toHaveValue('0 7 * * *')
  expect(state.writes.at(-1)).toMatchObject({ expected_version: 2, params: { services: ['web', 'worker'], auto_apply: false } })
  await dialog.getByRole('button', { name: 'Reload plan', exact: true }).click()
  await expect(cron).toHaveValue('0 4 * * *')
})

test('CD timer pause sends only timer intent and skipped triggers remain visible', async ({ page }) => {
  const state = await setup(page)
  await page.goto('/tasks?tab=schedules')
  const row = page.getByRole('row').filter({ hasText: 'rule-4' })
  await row.getByRole('button', { name: 'Rule actions' }).click()
  await page.getByRole('menuitem', { name: 'Pause timer', exact: true }).click()
  await expect.poll(() => state.writes.length).toBe(1)
  expect(state.writes[0]).toEqual({ expected_version: 2, timer_enabled: false })
  await row.getByRole('button', { name: 'Rule actions' }).click()
  await page.getByRole('menuitem', { name: 'Trigger history', exact: true }).click()
  await expect(page.getByRole('dialog').getByText('busy · Previous task active', { exact: true })).toBeVisible()
})


test('business CD editor keeps its draft and shared version when center changes it', async ({ page }) => {
  const state = await setup(page)
  await page.goto('/continuous-delivery/blog')
  await page.getByRole('tab', { name: 'Settings', exact: true }).click()
  const cloneURL = page.getByLabel('Git Clone URL', { exact: true })
  await cloneURL.fill('https://git.example.test/unsaved-draft.git')
  state.rules[2].version = 3; state.rules[2].interval_seconds = 35
  await page.clock.install(); await page.clock.fastForward(6000)
  await expect(page.getByText('Configuration changed elsewhere. Your draft and its starting version are preserved.', { exact: true })).toBeVisible()
  await expect(cloneURL).toHaveValue('https://git.example.test/unsaved-draft.git')
  await page.getByRole('button', { name: 'Save CD settings', exact: true }).click()
  await expect(page.getByRole('alert').filter({ hasText: 'concurrently' })).toBeVisible()
  expect(state.writes.at(-1)).toMatchObject({ expected_automation_version: 2, repository: { clone_url: 'https://git.example.test/unsaved-draft.git' } })
  await expect(cloneURL).toHaveValue('https://git.example.test/unsaved-draft.git')
  await page.getByRole('button', { name: 'Reload configuration', exact: true }).click()
  await expect(cloneURL).toHaveValue('https://git.example.test/blog.git')
  await expect(page.getByLabel('Polling interval (seconds)', { exact: true })).toHaveValue('35')
})

test('delivery result shows original candidate and operation identities', async ({ page }) => {
  await setup(page)
  await page.route('**/api/v1/delivery-records/by-task/parent', route => route.fulfill({ json: { code: 0, message: '', data: {
    records: { total: 1, page: 1, page_size: 20, items: [{ id: 'continuous_delivery:37', record_id: 37, source: 'continuous_delivery', project_id: 2, project_name: 'blog', node_ids: ['local'], node_exists: false, operation: 'deploy', status: 'succeeded', task_id: 'parent', source_record_id: 36, commit_sha: 'd'.repeat(40), config_hash: 'cfg-37', actor_name: 'admin', created_at: created }] },
    releases: [{ id: 41, project_id: 2, project_name: 'blog', status: 'succeeded', commit_sha: 'd'.repeat(40), commit_message: 'Original commit message', created_at: created }],
  } } }))
  await page.goto('/tasks#parent')
  const result = page.getByTestId('task-delivery-evidence')
  await expect(result.getByText('blog · Release #41 · succeeded', { exact: true })).toBeVisible()
  await expect(result.getByText('Source record #36', { exact: true })).toBeVisible()
  await expect(result.getByRole('button', { name: 'View linked business history (1)', exact: true })).toHaveAttribute('href', '/continuous-delivery?view=records&task=parent')
})

test('cleanup result retains partial evidence for a deleted node without runtime reads', async ({ page }) => {
  const state = await setup(page)
  const policy = { enabled: false, schedule: { frequency: 'daily', weekday: 0, hour: 4, minute: 0, timezone: 'UTC' }, images: { enabled: true, retention_days: 7, include_tagged: false }, cache: { enabled: false, retention_days: 7, reserved_bytes: 0 }, containers: { enabled: false, retention_days: 7 }, networks: { enabled: false, retention_days: 7 }, scan_volumes: true, protected: {} }
  await page.route('**/api/v1/tasks/cleanup-history', route => route.fulfill({ json: { code: 0, message: '', data: { id: 'cleanup-history', name: 'Historical cleanup', scope: 'node', node_id: 'removed-node', node_name: 'Original node', type: 'system.cleanup', status: 'failed', progress: 100, message: 'One resource failed', created_at: created, result: { run_id: 'old-run', result: { stats: { image: { deleted: 1, failed: 1 } } } } } } }))
  await page.route('**/api/v1/cleanup/runs/by-task/cleanup-history', route => route.fulfill({ json: { code: 0, message: '', data: { id: 'old-run', node_id: 'removed-node', node_name: 'Original node', task_id: 'cleanup-history', policy_version: 9, trigger: 'manual', status: 'partial_failed', message: 'One resource failed', created_at: created, policy, result: { outcomes: [{ kind: 'image', id: 'old-image', name: 'old-image', status: 'deleted' }], stats: { image: { deleted: 1, skipped: 0, failed: 1, scanned: 0 } }, image_layers_before: 1024, image_layers_after: 512 } } } }))
  await page.goto('/tasks#cleanup-history')
  const result = page.getByTestId('task-cleanup-evidence')
  await expect(result.getByText('partial failed', { exact: true })).toBeVisible()
  await expect(result.getByText('1 deleted · 0 skipped · 1 failed · 0 scanned', { exact: true })).toBeVisible()
  await expect(result.getByText('old-image', { exact: true })).toBeVisible()
  expect(state.requests.some(url => url.pathname.startsWith('/api/v1/nodes/removed-node'))).toBe(false)
})
