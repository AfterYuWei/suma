import { expect, test, type Page } from '@playwright/test'
import type { ContainerSummary } from '../src/features/containers/types'

const created = '2026-10-13T00:00:00Z'
const container = (id: string, project: string, image = 'app:v1'): ContainerSummary => ({ id: id.repeat(64), name: `${project}-web-${id}`, image, command: '', created, state: 'running', status: 'Up 1 hour', labels: { 'com.docker.compose.project': project, 'com.docker.compose.service': 'web' }, ports: [{ ip: '::', public_port: 8080, private_port: 80, type: 'tcp' }, { ip: '127.0.0.1', public_port: 8081, private_port: 80, type: 'udp' }], cpu_percent: 0, memory_bytes: 0, uptime_seconds: 0 })
async function openContainerAction(page: Page, name: string, action: string, zh = false) {
 await page.getByRole('button', { name: `${zh ? '容器操作' : 'Container actions'} · ${name}`, exact: true }).click()
 await page.getByRole('menuitem', { name: `${action} · ${name}`, exact: true }).click()
}
async function setup(page: Page, language = 'en-US', theme = 'dark') {
 const state = { calls: [] as string[], writes: [] as { path: string; body: unknown }[], rows: [container('a', 'alpha'), container('b', 'alpha', 'app:v2'), container('c', 'beta'), { ...container('d', 'external'), state: 'exited' }, { ...container('e', 'alpha', 'job:v1'), labels: { 'com.docker.compose.project': 'alpha', 'com.docker.compose.service': 'job', 'com.docker.compose.oneoff': 'True' } }], unhealthy: 'beta', betaCPU: 20, extraCount: 0, failMetrics: false, sockets: [] as string[], closed: [] as string[] }
 const errors: string[] = []; page.on('pageerror', error => errors.push(error.message))
 await page.addInitScript(({ language, theme }) => { localStorage.setItem('suma-language', language); localStorage.setItem('suma-theme', theme); localStorage.setItem('suma-node', 'local'); localStorage.setItem('suma-list-page-size', '20') }, { language, theme })
 await page.routeWebSocket('**/ws/**', ws => { state.sockets.push(ws.url()); ws.onClose(() => state.closed.push(ws.url())); if (ws.url().includes('/logs')) ws.send('hello from container\n'); else ws.send('$ ready\r\n') })
 await page.route('**/api/v1/**', async route => {
  const request = route.request(), path = new URL(request.url()).pathname.replace('/api/v1', '')
  state.calls.push(path)
  if (request.method() !== 'GET') state.writes.push({ path, body: request.postDataJSON() })
  const edge = path.startsWith('/nodes/edge/'), rows = edge ? [container('f', 'edge-app')] : state.rows
  const projects = (edge ? ['edge-app'] : ['alpha', 'beta', 'external', 'idle', ...Array.from({ length: state.extraCount }, (_, index) => `extra-${String(index).padStart(2, '0')}`)]).map(name => ({ name, backend: 'compose', native_name: name, node_id: edge ? 'edge' : 'local', scope: { kind: 'engine', id: edge ? 'edge' : 'local' }, ref: { backend: 'compose', scope: { kind: 'engine', id: edge ? 'edge' : 'local' }, native_name: name }, managed: name !== 'external', source: name === 'external' ? 'external' : 'managed', capabilities: name === 'external' ? ['view', 'takeover', 'cleanup'] : ['view', 'edit', 'deploy', 'stop', 'restart', 'delete'], declared_service_names: name === 'external' ? undefined : ['web', 'worker'], status: name === 'idle' || name === 'external' ? 'stopped' : 'running', service_count: 1, instance_count: rows.filter(row => row.labels['com.docker.compose.project'] === name).length, created_at: created, updated_at: created }))
  let data: unknown = []
  if (path === '/auth/status') data = { needs_setup: false }
  else if (path === '/auth/session') data = { id: 1, username: 'admin', nickname: 'Admin' }
  else if (path === '/settings') data = { 'general.timezone': 'Asia/Shanghai' }
  else if (path === '/nodes') data = ['local', 'edge'].map(id => ({ id, name: id, enabled: true, connection_type: 'unix', status: 'online', group_ids: [] }))
  else if (path === '/notifications/inbox') data = { items: [], unread: 0 }
  else if (path.endsWith('/projects')) data = projects
  else if (path.endsWith('/projects/compose/alpha')) data = { ...projects.find(project => project.name === 'alpha'), compose: 'services:\n  web:\n    image: app:v1\n  worker:\n    image: worker:v1\n', environment: '', metadata: { origin: 'created' } }
  else if (path.endsWith('/containers')) data = rows
  else if (path.endsWith('/containers/runtime')) data = rows.filter(row => row.state === 'running').map(row => ({ id: row.id, state: row.state, health_status: row.labels['com.docker.compose.project'] === state.unhealthy ? 'unhealthy' : 'none', restart_count: 0, memory_limit_bytes: 0, observed_at: new Date().toISOString() }))
  else if (path.endsWith('/containers/metrics')) {
   if (state.failMetrics) return route.fulfill({ status: 503, json: { code: 1, message: 'Metrics unavailable', data: null } })
   data = rows.filter(row => row.state === 'running').map(row => ({ id: row.id, available: true, cpu_percent: row.id.startsWith('a') ? 200 : row.id.startsWith('b') ? 0 : row.id.startsWith('c') ? state.betaCPU : 20, memory_bytes: 1024 ** 2, sampled_at: new Date().toISOString(), uptime_seconds: 3600 }))
  } else if (path.endsWith('/files/content')) data = { path: '/app.txt', content: 'hello\n', etag: 'original', persistent: true, read_only: false, single_file_bind: false }
  else if (path.endsWith('/files')) data = { path: '/', entries: [{ name: 'app.txt', path: '/app.txt', type: 'file', size: 6, modified_at: Date.now() / 1000 }], mounts: [] }
  else if (/\/containers\/[a-f]{64}$/.test(path)) { const row = rows.find(row => path.endsWith(row.id))!; data = { ...row, pid: 1, entrypoint: [], working_directory: '/', restart_policy: 'no', read_only_rootfs: false, environment: [], mounts: [], networks: [] } }
  else if (path.endsWith('/runtime')) data = { runtime: { health_status: 'none', restart_count: 0, exit_code: 0 }, health: { configured: false, logs: [] } }
  else if (path.includes('/actions/')) data = { id: 'operation', status: 'success', message: 'done' }
  return route.fulfill({ json: { code: 0, message: '', data } })
 })
 await page.goto('/projects')
 await expect(page.getByTestId('project-row')).toHaveCount(4)
 return { state, errors, zh: language === 'zh-CN' }
}
for (const language of ['en-US', 'zh-CN']) for (const theme of ['dark', 'light']) for (const width of [390, 1440]) {
 test(`project list ${language} ${theme} ${width}`, async ({ page }) => {
  await page.setViewportSize({ width, height: 900 })
  const { state, errors, zh } = await setup(page, language, theme)
  const alpha = page.getByTestId('project-row').filter({ has: page.getByRole('link', { name: 'alpha', exact: true }) })
  await expect(alpha).toContainText('CPU 220.0%')
  await expect(page.getByTestId('project-row').first()).toHaveAttribute('data-project', 'beta')
  await page.screenshot({ path: `/tmp/suma-project-toolbar-${language}-${theme}-${width}.png`, fullPage: true })
  const expand = alpha.getByRole('button', { name: `${zh ? '展开服务' : 'Expand services'} · alpha` })
  const checkbox = alpha.getByRole('checkbox', { name: 'alpha', exact: true })
  const checkBoxBounds = (await checkbox.boundingBox())!, expandBounds = (await expand.boundingBox())!, nameBounds = (await alpha.getByRole('link', { name: 'alpha', exact: true }).boundingBox())!
  expect(expandBounds.x).toBeGreaterThanOrEqual(checkBoxBounds.x + checkBoxBounds.width)
  expect(nameBounds.x).toBeGreaterThanOrEqual(expandBounds.x + expandBounds.width)
  await checkbox.check(); await expect(expand).toHaveAttribute('aria-expanded', 'false')
  await checkbox.uncheck(); await expect(expand).toHaveAttribute('aria-expanded', 'false')
  await alpha.getByText('CPU 220.0%', { exact: true }).click()
  await expect(expand).toHaveAttribute('aria-expanded', 'true')
  await expect(page).toHaveURL(/\/projects$/)
  await alpha.getByText(zh ? '托管' : 'Managed', { exact: true }).click()
  await expect(expand).toHaveAttribute('aria-expanded', 'false')
  await expand.focus(); await page.keyboard.press('Enter')
  await expect(expand).toHaveAttribute('aria-expanded', 'true')
  const services = page.getByRole('region', { name: zh ? '项目服务' : 'Project services' })
  await expect(services).toContainText(zh ? '未创建' : 'Not created')
  await expect(services).toContainText(zh ? '一次性容器' : 'One-off containers')
  await expect(services).toContainText(zh ? '+1 版本' : '+1 versions')
  await page.screenshot({ path: `/tmp/suma-service-cards-${language}-${theme}-${width}.png`, fullPage: true })
  await services.getByRole('button', { name: `${zh ? '服务详情' : 'Service details'} web`, exact: true }).click()
  await expect(services.getByRole('link', { name: 'alpha-web-a', exact: true })).toBeVisible()
  await services.getByRole('button', { name: `${zh ? '查看完整端口映射' : 'View full port mappings'}` }).first().click()
  const ports = page.getByRole('dialog')
  await expect(ports).toContainText('[::]:8080:80/tcp')
  await expect(ports).toContainText('127.0.0.1:8081:80/udp')
  await ports.getByRole('button', { name: 'Close', exact: true }).click()
  await openContainerAction(page, 'alpha-web-a', zh ? '日志' : 'Logs', zh)
  const dialog = page.getByRole('dialog', { name: 'alpha-web-a', exact: true })
  await expect(dialog).toContainText('hello from container')
  await expect(page).toHaveURL(/\/projects$/)
  await dialog.getByRole('tab', { name: zh ? '终端' : 'Terminal', exact: true }).click()
  await expect(dialog.locator('.xterm')).toBeVisible()
  await expect.poll(() => state.closed.filter(url => url.includes('/logs')).length === state.sockets.filter(url => url.includes('/logs')).length).toBe(true)
  await dialog.getByRole('button', { name: 'Close', exact: true }).click()
  await expect.poll(() => state.closed.length === state.sockets.length).toBe(true)
  await expect(services.getByRole('button', { name: `${zh ? '容器操作' : 'Container actions'} · alpha-web-a`, exact: true })).toBeFocused()
  expect(state.calls.some(path => path.includes('/image-updates'))).toBe(false)
  expect(state.calls.some(path => path.endsWith('/services'))).toBe(false)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await page.screenshot({ path: `/tmp/suma-project-list-${language}-${theme}-${width}.png`, fullPage: true })
  if (width === 390) await alpha.getByRole('link', { name: 'alpha', exact: true }).click()
  else await alpha.getByRole('button', { name: zh ? '编辑项目' : 'Edit Project', exact: true }).click()
  await expect(page).toHaveURL(/\/projects\/compose\/alpha$/)
  await expect(page.getByRole('tab', { name: zh ? '配置' : 'Configuration', exact: true })).toBeVisible()
  expect(errors).toEqual([])
 })
}
test('search, filters, operations and per-node preferences', async ({ page }) => {
 const { state } = await setup(page)
 const search = page.getByRole('textbox', { name: 'Search projects, services or images' })
 await search.fill('app:v2'); await expect(page.getByTestId('project-row')).toHaveCount(1)
 await page.getByRole('button', { name: 'Sort', exact: true }).click(); await page.getByRole('menuitem', { name: 'CPU', exact: true }).click()
 await page.getByRole('button', { name: 'Clear filter', exact: true }).click(); await expect(search).toHaveValue(''); await expect(page.getByTestId('project-row')).toHaveCount(4)
 await expect(page.getByTestId('project-row').first()).toHaveAttribute('data-project', 'alpha')
 await search.fill('worker'); await expect(page.getByTestId('project-row')).toHaveCount(3)
 await page.getByRole('button', { name: 'Clear filters', exact: true }).click(); await expect(search).toHaveValue(''); await expect(page.getByTestId('project-row')).toHaveCount(4)
 await expect(page.getByTestId('project-row').first()).toHaveAttribute('data-project', 'alpha')
 await page.getByRole('button', { name: 'Abnormal · 1', exact: true }).click(); await expect(page.getByTestId('project-row')).toHaveCount(1)
 await page.getByRole('button', { name: 'Clear filters', exact: true }).click(); await expect(page.getByTestId('project-row')).toHaveCount(4)
 await page.getByRole('button', { name: 'Abnormal · 1', exact: true }).click(); await expect(page.getByTestId('project-row')).toHaveCount(1)
 const beta = page.getByTestId('project-row')
 await beta.getByRole('button', { name: 'Stop', exact: true }).click()
 await expect(beta.getByRole('button', { name: 'Expand services · beta' })).toHaveAttribute('aria-expanded', 'false')
 expect(state.writes.at(-1)?.path).toMatch(/actions\/stop$/)
 await beta.getByRole('button', { name: 'More project actions' }).click()
 await expect(page.getByRole('menu')).not.toContainText('Update')
 await page.getByRole('menu').click({ position: { x: 2, y: 10 } })
 await expect(page.getByRole('menu')).toBeVisible()
 await expect(beta.getByRole('button', { name: 'Expand services · beta' })).toHaveAttribute('aria-expanded', 'false')
 await page.keyboard.press('Escape')
 await expect(beta.getByRole('button', { name: 'Expand services · beta' })).toHaveAttribute('aria-expanded', 'false')
 await page.getByRole('combobox', { name: 'Current Docker node' }).click(); await page.getByRole('option', { name: /edge/ }).click()
 await expect(page.getByTestId('project-row')).toHaveCount(1); await expect(page.getByTestId('project-row')).toContainText('edge-app')
 await page.getByRole('combobox', { name: 'Current Docker node' }).click(); await page.getByRole('option', { name: /local/ }).click()
 await expect(page.getByTestId('project-row')).toHaveCount(1); await expect(page.getByTestId('project-row')).toContainText('beta')
})
async function editFileDraft(page: Page) {
 await page.getByRole('button', { name: 'Expand services · beta' }).click()
 await openContainerAction(page, 'beta-web-c', 'Files')
 const dialog = page.getByRole('dialog', { name: 'beta-web-c', exact: true })
 await dialog.getByRole('button', { name: 'app.txt 6 B', exact: true }).click()
 const editor = dialog.locator('.monaco-editor .view-lines').first()
 await expect(editor).toBeVisible(); await editor.click({ position: { x: 15, y: 10 } }); await page.keyboard.press('Control+Home'); await page.keyboard.insertText('changed ')
 await expect(dialog.getByRole('button', { name: 'Save', exact: true })).toBeEnabled()
 return dialog
}

test('Dialog protects file drafts on tab switch and close', async ({ page }) => {
 await setup(page)
 const dialog = await editFileDraft(page)
 await dialog.getByRole('tab', { name: 'Logs', exact: true }).click()
 const confirm = page.getByRole('dialog', { name: 'Discard unsaved file changes?', exact: true })
 await expect(confirm).toBeVisible(); await confirm.getByRole('button', { name: 'Cancel', exact: true }).click()
 await expect(dialog.getByRole('tab', { name: 'Files', exact: true })).toHaveAttribute('aria-selected', 'true')
 await dialog.getByRole('button', { name: 'Close', exact: true }).click()
 await expect(confirm).toBeVisible(); await confirm.getByRole('button', { name: 'Discard', exact: true }).click()
 await expect(dialog).toHaveCount(0)
})
test('metric failure and container replacement do not show fabricated resource values', async ({ page }) => {
 const { state } = await setup(page)
 state.failMetrics = true
 await page.waitForResponse(response => response.url().endsWith('/containers/metrics') && response.status() === 503)
 await expect(page.getByTestId('project-row').filter({ hasText: 'alpha' })).toContainText('CPU —')
 await page.getByRole('button', { name: 'Expand services · beta' }).click()
 await openContainerAction(page, 'beta-web-c', 'Logs')
 state.rows = state.rows.filter(row => row.id !== 'c'.repeat(64))
 await expect(page.getByRole('dialog')).toContainText('Container removed or recreated', { timeout: 15_000 })
 await expect.poll(() => state.closed.length).toBe(state.sockets.length)
})

test('resource sorting stays stable during selection and an open action menu', async ({ page }) => {
 const { state } = await setup(page)
 await page.clock.install()
 await page.getByRole('button', { name: 'Sort', exact: true }).click(); await page.getByRole('menuitem', { name: 'CPU', exact: true }).click()
 await expect(page.getByTestId('project-row').first()).toHaveAttribute('data-project', 'alpha')
 await page.getByRole('checkbox', { name: 'beta', exact: true }).check()
 state.betaCPU = 300
 await page.clock.runFor(5100)
 await expect(page.getByTestId('project-row').filter({ hasText: 'beta' })).toContainText('CPU 300.0%')
 await expect(page.getByTestId('project-row').first()).toHaveAttribute('data-project', 'alpha')
 await page.getByRole('checkbox', { name: 'beta', exact: true }).uncheck()
 await expect(page.getByTestId('project-row').first()).toHaveAttribute('data-project', 'beta')
 await page.getByTestId('project-row').first().getByRole('button', { name: 'More project actions' }).click()
 state.betaCPU = 0
 await page.clock.runFor(5100)
 await expect(page.getByTestId('project-row').filter({ hasText: 'beta' })).toContainText('CPU 0.0%')
 await expect(page.getByTestId('project-row').first()).toHaveAttribute('data-project', 'beta')
 await page.keyboard.press('Escape')
 await expect(page.getByTestId('project-row').first()).toHaveAttribute('data-project', 'alpha')
})
test('search and status filters run before pagination', async ({ page }) => {
 const { state } = await setup(page)
 state.extraCount = 23
 await expect(page.getByRole('navigation', { name: 'List pagination' })).toContainText('27 items', { timeout: 10_000 })
 await page.getByRole('textbox', { name: 'Search projects, services or images' }).fill('extra-22')
 await expect(page.getByTestId('project-row')).toHaveCount(1)
 await expect(page.getByTestId('project-row')).toContainText('extra-22')
 await expect(page.getByRole('navigation', { name: 'List pagination' })).toHaveCount(0)
 await page.getByRole('textbox', { name: 'Search projects, services or images' }).fill('')
 await page.getByRole('button', { name: 'All states' }).click(); await page.getByRole('menuitem', { name: 'Not created', exact: true }).click()
 await expect(page.getByRole('navigation', { name: 'List pagination' })).toContainText('24 items')
 await page.getByRole('button', { name: 'Next page', exact: true }).click()
 await expect(page.getByTestId('project-row')).toHaveCount(4)
})

test('node changes consult the file draft guard before closing the Dialog', async ({ page }) => {
 await setup(page)
 const dialog = await editFileDraft(page)
 const changeNode = () => page.evaluate(async () => {
  const source = performance.getEntriesByType('resource').map(entry => entry.name).find(url => new URL(url).pathname === '/src/stores/ui.ts') ?? '/src/stores/ui.ts'
  const { useUIStore } = await import(source)
  useUIStore.getState().setCurrentNodeID('edge')
 })
 await changeNode()
 const confirm = page.getByRole('dialog', { name: 'Discard unsaved file changes?', exact: true })
 await expect(confirm).toBeVisible(); await confirm.getByRole('button', { name: 'Cancel', exact: true }).click()
 await expect(dialog).toBeVisible(); await expect(dialog.locator('.monaco-editor')).toContainText('changed')
 await changeNode(); await confirm.getByRole('button', { name: 'Discard', exact: true }).click()
 await expect(dialog).toHaveCount(0)
 await expect(page.getByTestId('project-row')).toHaveCount(1)
 await expect(page.getByTestId('project-row')).toContainText('edge-app')
})
test('stopping a container retains the file draft and disables file writes and terminal', async ({ page }) => {
 const { state } = await setup(page)
 const dialog = await editFileDraft(page)
 state.rows = state.rows.map(row => row.id.startsWith('c') ? { ...row, state: 'exited' } : row)
 await expect(dialog).toContainText('Container is not running', { timeout: 15_000 })
 await expect(dialog.locator('.monaco-editor')).toContainText('changed')
 await expect(dialog.getByRole('button', { name: 'Save', exact: true })).toBeDisabled()
 await expect(dialog.getByRole('tab', { name: 'Terminal', exact: true })).toBeDisabled()
 await dialog.getByRole('button', { name: 'Close', exact: true }).click()
 await page.getByRole('dialog', { name: 'Discard unsaved file changes?', exact: true }).getByRole('button', { name: 'Discard', exact: true }).click()
 await expect(dialog).toHaveCount(0)
})

test('leaving the Files tab cancels its pending container detail request', async ({ page }) => {
 await setup(page)
 const endpoint = `/api/v1/nodes/local/containers/${'c'.repeat(64)}`
 let started = 0, aborted = 0, release!: () => void
 const gate = new Promise<void>(resolve => { release = resolve })
 page.on('requestfailed', request => { if (new URL(request.url()).pathname === endpoint && request.failure()?.errorText.includes('ERR_ABORTED')) aborted++ })
 await page.route(url => url.pathname === endpoint, async route => { started++; await gate; if (aborted < started) await route.fulfill({ json: { code: 0, data: {} } }) })
 await page.getByRole('button', { name: 'Expand services · beta' }).click()
 await openContainerAction(page, 'beta-web-c', 'Files')
 const dialog = page.getByRole('dialog', { name: 'beta-web-c', exact: true })
 await expect.poll(() => started).toBeGreaterThan(0)
 await dialog.getByRole('tab', { name: 'Logs', exact: true }).click()
 await expect.poll(() => aborted === started).toBe(true)
 release()
 await expect(dialog).toContainText('hello from container')
 await dialog.getByRole('button', { name: 'Close', exact: true }).click()
})

test('same-tag replicas show distinct image IDs and keep the version count visible', async ({ page }) => {
 const { state } = await setup(page)
 const reference = 'registry.example.test/team/a-very-long-application-image-reference:latest'
 state.rows = state.rows.map(row => row.labels['com.docker.compose.project'] === 'alpha' && row.labels['com.docker.compose.service'] === 'web' ? { ...row, image: reference, image_id: `sha256:${row.id}` } : row)
 await page.getByRole('button', { name: 'Expand services · alpha' }).click()
 const group = page.getByTestId('service-group').filter({ hasText: 'web' })
 await expect(group).toContainText('+1 versions')
 await group.getByRole('button', { name: 'Service details web', exact: true }).click()
 const instance = group.getByTestId('service-instance').filter({ hasText: 'alpha-web-a' })
 await expect(instance.getByText(reference, { exact: true })).toBeVisible({ timeout: 15_000 })
 await instance.getByText(reference, { exact: true }).hover()
 await expect(page.locator('[data-slot="tooltip-content"]')).toContainText(`sha256:${'a'.repeat(64)}`)
 await expect(group.getByText('+1 versions', { exact: true })).toBeVisible()
 expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
})
