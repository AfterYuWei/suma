import { expect, test, type Page } from '@playwright/test'

const stamp = '2026-10-24T04:00:00Z'
const initial = ['Starting docker compose update', 'api Pulling', 'database Pulling', 'abcdef123456 Pull complete', 'fedcba654321 Downloading 10MB/100MB', 'database Pulled']
const makeLogs = (messages: string[]) => messages.map((message, id) => ({ id, message, level: 'info', created_at: stamp }))

async function setup(page: Page, language = 'en-US', theme = 'dark', width = 1440) {
  await page.setViewportSize({ width, height: 900 })
  await page.addInitScript(({ language, theme }) => {
    localStorage.setItem('suma-language', language)
    localStorage.setItem('suma-theme', theme)
    localStorage.setItem('suma-node', 'local')
  }, { language, theme })
  const state = {
    task: { id: 'progress-task', type: 'compose.update', name: 'Update database', status: 'running', progress: 55, message: 'fedcba654321 Downloading 42MB/100MB' },
    logs: makeLogs(initial),
    writes: [] as string[],
  }
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname.replace('/api/v1', '')
    if (route.request().method() !== 'GET') state.writes.push(path)
    let data: unknown = []
    if (path === '/auth/status') data = { needs_setup: false }
    else if (path === '/auth/session') data = { id: 1, username: 'admin', nickname: 'Admin' }
    else if (path === '/settings') data = { 'general.timezone': 'Asia/Shanghai' }
    else if (path === '/nodes') data = [{ id: 'local', name: 'local', enabled: true, connection_type: 'unix', status: 'online', group_ids: [1] }]
    else if (path === '/node-groups') data = [{ id: 1, name: 'Default', is_default: true }]
    else if (path === '/notifications/inbox') data = { items: [], unread: 0 }
    else if (path.endsWith('/projects/compose/database')) data = {
      name: 'database', native_name: 'database', node_id: 'local', backend: 'compose', managed: true,
      capabilities: ['view', 'edit', 'deploy', 'services', 'logs'], status: 'running', revision: 'r1',
      declared_service_names: ['api', 'database'], services: 2, containers: 2,
      compose: 'services:\n  api: {image: nginx:alpine}\n  database: {image: postgres:18}\n', environment: '',
    }
    else if (path.endsWith('/image-updates/policy')) data = { enabled: false, interval_hours: 6, registry_credentials: {} }
    else if (path.endsWith('/image-updates')) data = { results: [] }
    else if (path.endsWith('/tasks/progress-task/logs')) data = state.logs
    else if (path.endsWith('/tasks/progress-task/cancel')) { state.task.status = 'canceled'; state.task.message = 'Canceled'; data = {} }
    else if (path.endsWith('/tasks/progress-task')) data = state.task
    return route.fulfill({ json: { code: 0, message: '', data } })
  })
  await page.goto('/projects/compose/database?task=progress-task&action=update')
  const dialog = page.getByRole('dialog', { name: /progress|进度/ })
  await expect(dialog).toBeVisible()
  await expect(dialog.getByRole('region', { name: language === 'zh-CN' ? '项目镜像' : 'Project images' })).toBeVisible()
  await dialog.evaluate(async el => { await document.fonts.ready; await Promise.all(el.getAnimations({ subtree: true }).filter(animation => animation.effect?.getTiming().iterations !== Infinity).map(animation => animation.finished.catch(() => {}))) })
  return { state, dialog }
}

for (const language of ['zh-CN', 'en-US']) for (const theme of ['dark', 'light']) for (const width of [1024, 1440]) {
  test(`compose progress readable ${language} ${theme} ${width}`, async ({ page }) => {
    const zh = language === 'zh-CN'
    const { state, dialog } = await setup(page, language, theme, width)
    const stages = dialog.getByRole('list', { name: zh ? '任务阶段' : 'Task stages' })
    await expect(stages.locator('[aria-current=step]')).toHaveText(zh ? '1拉取镜像' : '1Pull images')
    await expect(dialog).not.toContainText('55%')
    await expect(dialog.getByRole('progressbar')).toHaveCount(0)
    await dialog.screenshot({ path: `/tmp/suma-compose-progress-desktop-${language}-${theme}-${width}.png` })
    const overview = await dialog.getByTestId('compose-task-overview').boundingBox()
    const output = dialog.getByRole('region', { name: zh ? '实时输出' : 'Live output' })
    const outputBounds = await output.boundingBox()
    expect(outputBounds!.x).toBeGreaterThanOrEqual(overview!.x + overview!.width + 12)
    expect(Math.abs(outputBounds!.y - overview!.y)).toBeLessThanOrEqual(1)
    expect(outputBounds!.width).toBeGreaterThan(400)
    await expect(output.getByText('Starting docker compose update', { exact: true })).toBeVisible()
    await expect(dialog.getByText('abcdef123456', { exact: true })).not.toBeVisible()
    await dialog.locator('summary').filter({ hasText: zh ? '镜像层详情' : 'Image layer details' }).click()
    await expect(dialog.getByText('42MB / 100MB', { exact: true })).toBeVisible()
    await expect(dialog.getByRole('progressbar', { name: /fedcba654321/ })).toHaveAttribute('aria-valuenow', '42')
    const expandedOutputBounds = await output.boundingBox()
    expect(expandedOutputBounds).toEqual(outputBounds)
    const bounds = await dialog.boundingBox()
    expect(bounds!.x).toBeGreaterThanOrEqual(0)
    expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(width)
    expect(bounds!.width / bounds!.height).toBeGreaterThan(1.4)
    expect(bounds!.height).toBeLessThanOrEqual(640)
    expect(await dialog.evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true)
    await expect(dialog.getByRole('button', { name: zh ? '后台运行' : 'Run in background', exact: true })).toBeInViewport()
    await page.screenshot({ path: `/tmp/suma-compose-progress-${language}-${theme}-${width}.png` })
    await dialog.getByRole('button', { name: zh ? '后台运行' : 'Run in background', exact: true }).click()
    await expect(dialog).not.toBeVisible()
    await page.getByRole('button', { name: zh ? '查看任务进度' : 'View task progress', exact: true }).click()
    await expect(dialog).toBeVisible()
    expect(state.writes).toEqual([])
    await page.keyboard.press('Escape')
    await expect(dialog).not.toBeVisible()
  })
}

test('phase transition and successful completion retain resource identity', async ({ page }) => {
  const { state, dialog } = await setup(page)
  state.logs = makeLogs([...initial, 'Images ready; recreating Compose services', 'Container database-api-1 Recreating', 'Container database-db-1 Running', 'Container database-api-1 Starting'])
  state.task = { ...state.task, progress: 60, message: 'Container database-api-1 Starting' }
  const stages = dialog.getByRole('list', { name: 'Task stages' })
  await expect(stages.locator('[aria-current=step]')).toContainText('Recreate & start')
  await expect(dialog.getByRole('region', { name: 'Containers & resources' }).getByText('database-api-1', { exact: true })).toBeVisible()
  await expect(dialog.getByText('Starting', { exact: true })).toBeVisible()
  state.logs = makeLogs([...state.logs.map(row => row.message), 'Container database-api-1 Started'])
  state.task = { ...state.task, status: 'success', progress: 100, message: 'Completed' }
  await expect(dialog.getByRole('heading', { name: 'Operation completed', exact: true })).toBeVisible()
  await expect(stages.locator('[aria-current]')).toHaveCount(0)
  await expect(dialog.getByRole('button', { name: 'Cancel task', exact: true })).toHaveCount(0)
  await expect(dialog.getByRole('progressbar')).toHaveCount(0)
  await dialog.getByRole('button', { name: 'View task', exact: true }).click()
  await expect(page).toHaveURL(/\/tasks$/)
})

test('failure exposes the Docker cause beside live output and handles long names', async ({ page }) => {
  const { state, dialog } = await setup(page, 'en-US', 'light', 1280)
  const name = `ghcr.io/team/${'very-long-image-name-'.repeat(8)}:latest`
  state.task = { ...state.task, status: 'failed', progress: 100, message: 'docker compose pull: exit status 1' }
  state.logs = makeLogs([...initial, `Image ${name} Error pull access denied: unauthorized`])
  await expect(dialog.getByRole('alert')).toContainText('pull access denied: unauthorized')
  await expect(dialog.getByRole('heading', { name: 'Failed · Pull images', exact: true })).toBeVisible()
  await expect(dialog.getByRole('list', { name: 'Task stages' }).locator('[aria-current=step]')).toContainText('Pull images')
  await expect(dialog.getByText(name, { exact: true })).toBeVisible()
  expect(await dialog.evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true)
  await page.screenshot({ path: '/tmp/suma-compose-progress-desktop-failed-1280.png' })
})

test('cancel targets this task and preserves the interrupted phase', async ({ page }) => {
  const { state, dialog } = await setup(page, 'zh-CN')
  await dialog.getByRole('button', { name: '取消任务', exact: true }).click()
  await expect(dialog.getByRole('heading', { name: '已取消 · 拉取镜像', exact: true })).toBeVisible()
  expect(state.writes).toEqual(['/nodes/local/tasks/progress-task/cancel'])
  await expect(dialog.getByText('操作已停止，已完成的变更会保留。', { exact: true })).toBeVisible()
  await expect(dialog.getByRole('progressbar')).toHaveCount(0)
  await dialog.locator('summary').filter({ hasText: '镜像层详情' }).click()
  await expect(dialog.getByTestId('compose-layer-details').getByText('已中断', { exact: true })).toBeVisible()
})

test('raw output follows new messages and preserves the position when reading earlier output', async ({ page }) => {
  const { state, dialog } = await setup(page)
  state.logs = makeLogs([...initial, ...Array.from({ length: 90 }, (_, index) => `output line ${index}`)])
  const viewport = dialog.getByTestId('compose-output-viewport')
  await expect(viewport.getByText('output line 89', { exact: true })).toBeAttached()
  await expect.poll(() => viewport.evaluate(el => el.scrollHeight - el.scrollTop - el.clientHeight)).toBeLessThan(2)
  await viewport.evaluate(el => { el.scrollTop = 0; el.dispatchEvent(new Event('scroll', { bubbles: true })) })
  state.logs = makeLogs([...state.logs.map(row => row.message), 'newest output'])
  await expect(dialog.getByText('newest output', { exact: true })).toBeAttached()
  expect(await viewport.evaluate(el => el.scrollTop)).toBe(0)
})

test('live output refreshes byte counters without new historical logs', async ({ page }) => {
  const { state, dialog } = await setup(page)
  const output = dialog.getByRole('region', { name: 'Live output' })
  await expect(output.getByText('fedcba654321 Downloading 42MB/100MB', { exact: true })).toBeVisible()
  await expect(output.getByText('fedcba654321 Downloading 10MB/100MB', { exact: true })).toHaveCount(0)
  await expect(output.getByText('6 entries', { exact: true })).toBeVisible()

  state.task.message = 'fedcba654321 Downloading 68MB/100MB'
  await expect(output.getByText(state.task.message, { exact: true })).toBeVisible()
  await expect(output.getByText('fedcba654321 Downloading 42MB/100MB', { exact: true })).toHaveCount(0)
  await expect(output.getByText('6 entries', { exact: true })).toBeVisible()
  const historicalBounds = await output.getByText('Starting docker compose update', { exact: true }).boundingBox()
  const liveBounds = await output.getByText(state.task.message, { exact: true }).boundingBox()
  expect(liveBounds!.x).toBe(historicalBounds!.x)
  await dialog.screenshot({ path: '/tmp/suma-compose-progress-live-counter.png' })

  await dialog.getByRole('button', { name: 'Run in background', exact: true }).click()
  state.task.message = 'fedcba654321 Downloading 90MB/100MB'
  await page.getByRole('button', { name: 'View task progress', exact: true }).click()
  await expect(output.getByText(state.task.message, { exact: true })).toBeVisible()
  expect(state.writes).toEqual([])

  // A newer log response must win over a task response from the prior stage.
  state.logs = makeLogs([...initial, 'fedcba654321 Download complete'])
  await expect(output.getByText('fedcba654321 Download complete', { exact: true })).toBeVisible()
  await expect(output.getByText(state.task.message, { exact: true })).toHaveCount(0)
})

test('counter-only updates follow the bottom and keep earlier output readable', async ({ page }) => {
  const { state, dialog } = await setup(page)
  state.logs = makeLogs([...initial, ...Array.from({ length: 90 }, (_, index) => `output line ${index}`)])
  const viewport = dialog.getByTestId('compose-output-viewport')
  await expect(viewport.getByText('output line 89', { exact: true })).toBeAttached()
  state.task.message = 'fedcba654321 Downloading 68MB/100MB'
  await expect(viewport.getByText(state.task.message, { exact: true })).toBeAttached()
  await expect.poll(() => viewport.evaluate(el => el.scrollHeight - el.scrollTop - el.clientHeight)).toBeLessThan(2)

  await viewport.evaluate(el => { el.scrollTop = 0; el.dispatchEvent(new Event('scroll', { bubbles: true })) })
  state.task.message = 'fedcba654321 Downloading 90MB/100MB'
  await expect(viewport.getByText(state.task.message, { exact: true })).toBeAttached()
  expect(await viewport.evaluate(el => el.scrollTop)).toBe(0)
})

test('desktop panels scroll independently and keep the footer visible on a laptop', async ({ page }) => {
  const { state, dialog } = await setup(page, 'zh-CN', 'dark', 1366)
  await page.setViewportSize({ width: 1366, height: 768 })
  state.logs = makeLogs([...initial, ...Array.from({ length: 24 }, (_, index) => `service-${index} Pulling`), ...Array.from({ length: 70 }, (_, index) => `output line ${index}`)])
  await expect(dialog.getByText('service-23', { exact: true })).toBeAttached()
  const overview = dialog.getByTestId('compose-task-overview')
  const viewport = dialog.getByTestId('compose-output-viewport')
  await expect.poll(() => overview.evaluate(el => el.scrollHeight > el.clientHeight)).toBe(true)
  const outputTop = await viewport.evaluate(el => el.scrollTop)
  await overview.evaluate(el => { el.scrollTop = el.scrollHeight })
  expect(await viewport.evaluate(el => el.scrollTop)).toBe(outputTop)
  await expect(dialog.getByRole('button', { name: '取消任务', exact: true })).toBeInViewport()
  await expect(dialog.getByRole('button', { name: '后台运行', exact: true })).toBeInViewport()
  const bounds = await dialog.boundingBox()
  expect(bounds!.width / bounds!.height).toBeGreaterThan(1.4)
  await page.screenshot({ path: '/tmp/suma-compose-progress-desktop-laptop.png' })
})
