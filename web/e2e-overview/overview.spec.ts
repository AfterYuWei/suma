import { expect, test, type Page } from '@playwright/test'

const nodes = ['local', 'edge', 'nas'].map((id, index) => ({ id, name: `node-${id}`, connection_type: 'unix', endpoint: `unix:///${id}.sock`, tls_mode: 'disabled', enabled: true, status: 'online', group_ids: [index + 1], created_at: '2026-10-11T00:00:00Z', updated_at: '2026-10-11T00:00:00Z' }))
const info = { name: 'docker-host', server_version: '28.5', operating_system: 'Ubuntu 24.04', os_version: '24.04', architecture: 'amd64', kernel_version: '6.8', containers_running: 1, containers_paused: 0, containers_stopped: 2, images: 5, cpus: 2, memory_bytes: 4096, storage_driver: 'overlay2', logging_driver: 'json-file', cgroup_driver: 'systemd', cgroup_version: '2', default_runtime: 'runc', live_restore: true, security_options: ['name=seccomp'] }
function metrics(size: string) {
  const summary = { metrics_available: true, container_cpu_percent: 80, container_memory_bytes: 896 }
  if (size === 'small') return summary
  const row = { id: 'app', name: 'app', image: 'app:latest', state: 'running', available: true, cpu_percent: 80, memory_bytes: 896 }
  return { ...summary, container_network_rx_bytes: 2048, container_network_tx_bytes: 4096, container_block_read_bytes: 8192, container_block_write_bytes: 16384, longest_container_uptime_seconds: 120, containers: [size === 'large' ? { ...row, network_rx_bytes: 2048, network_tx_bytes: 4096, block_read_bytes: 8192, block_write_bytes: 16384, uptime_seconds: 120 } : row] }
}
function gate() { let release!: () => void; const promise = new Promise<void>((resolve) => { release = resolve }); return { promise, release } }

async function setup(page: Page, language = 'en-US', theme = 'dark') {
  const state = { calls: [] as string[], gates: new Map<string, Promise<void>>(), failures: new Set<string>() }
  await page.addInitScript(({ language, theme }) => {
    localStorage.setItem('suma-language', language)
    localStorage.setItem('suma-theme', theme)
    localStorage.setItem('suma-node-group', 'all')
    localStorage.setItem('suma-overview-node-card-sizes', JSON.stringify({ local: 'small', edge: 'medium', nas: 'large' }))
  }, { language, theme })
  await page.route('**/api/v1/**', async (route) => {
    const url = new URL(route.request().url()), path = url.pathname.replace('/api/v1', '')
    const key = path + url.search
    state.calls.push(key)
    await state.gates.get(key)
    let data: unknown = []
    if (path === '/auth/status') data = { needs_setup: false }
    else if (path === '/auth/session') data = { id: 1, username: 'admin', nickname: 'Admin', email: 'admin@example.test' }
    else if (path === '/settings') data = {}
    else if (path === '/nodes') data = nodes
    else if (path === '/node-groups') data = nodes.map((node, index) => ({ id: index + 1, name: `group-${node.id}`, is_default: false, node_count: 1 }))
    else if (path === '/notifications/inbox') data = { unread: 0, events: [] }
    else if (path === '/cd/overview') data = { projects: [], totals: { projects: 0, configured: 0, releasing: 0, awaiting_approval: 0, drifted: 0, healthy: 0 } }
    else if (path.endsWith('/overview/info')) data = info
    else if (path.endsWith('/overview/metrics')) data = metrics(url.searchParams.get('size') || 'medium')
    else if (path.endsWith('/overview/resources')) data = { networks: 3, volumes: 4 }
    else if (path.endsWith('/overview/disk')) data = { docker_disk_usage_bytes: 1024 ** 3 }
    const fail = state.failures.has(key)
    try {
      await route.fulfill({ status: fail ? 503 : 200, contentType: 'application/json', body: JSON.stringify({ code: fail ? 20202 : 0, message: fail ? 'controlled failure' : 'success', data: fail ? null : data }) })
    } catch { /* A resized/filtered card may already have aborted this request. */ }
  })
  return state
}
const card = (page: Page, id: string) => page.locator(`[data-node-card-id="${id}"]`)

test('metadata and healthy cards render while a node and disk are still loading', async ({ page }) => {
  const state = await setup(page), delayedInfo = gate(), delayedMetrics = gate(), delayedDisk = gate(), delayedCD = gate()
  state.gates.set('/nodes/nas/overview/info', delayedInfo.promise)
  state.gates.set('/nodes/edge/overview/metrics?size=medium', delayedMetrics.promise)
  state.gates.set('/nodes/edge/overview/disk', delayedDisk.promise)
  state.gates.set('/cd/overview', delayedCD.promise)
  await page.goto('/')
  await expect(card(page, 'nas').getByText('node-nas', { exact: true })).toBeVisible()
  await expect(card(page, 'nas').getByText('Connecting', { exact: true })).toBeVisible()
  await expect(card(page, 'local').getByText('40.0%', { exact: true })).toBeVisible()
  await expect(card(page, 'edge').getByRole('status', { name: 'Loading container data' }).last()).toBeVisible()
  await expect(card(page, 'edge')).not.toContainText('No running containers')
  await expect(card(page, 'edge').getByRole('status', { name: 'Loading disk usage' })).toBeVisible()
  expect(state.calls.some((path) => path.startsWith('/fleet/overview'))).toBe(false)
  await page.screenshot({ path: '/tmp/suma-overview-loading.png', fullPage: true })
  delayedMetrics.release()
  await expect(card(page, 'edge').getByText('app', { exact: true })).toBeVisible()
  await expect(card(page, 'edge').getByRole('status', { name: 'Loading disk usage' })).toBeVisible()
  delayedInfo.release(); delayedDisk.release(); delayedCD.release()
  await expect(card(page, 'nas').getByText('app', { exact: true })).toBeVisible()
})

test('resizing cancels the old profile and uses the current card size', async ({ page }) => {
  const state = await setup(page), delayed = gate()
  state.gates.set('/nodes/local/overview/metrics?size=small', delayed.promise)
  const aborted: string[] = []
  page.on('requestfailed', (request) => aborted.push(request.url()))
  await page.goto('/')
  await expect.poll(() => state.calls.includes('/nodes/local/overview/metrics?size=small')).toBe(true)
  await card(page, 'local').getByRole('button', { name: 'Switch node-local card from small to medium' }).click()
  await expect(card(page, 'local').getByText('app', { exact: true })).toBeVisible()
  await expect.poll(() => aborted.some((path) => path.includes('metrics?size=small'))).toBe(true)
  delayed.release()
  await card(page, 'local').getByRole('button', { name: 'Switch node-local card from medium to large' }).click()
  await expect(card(page, 'local').getByText('Network', { exact: true })).toBeVisible()
  expect(state.calls).toContain('/nodes/local/overview/metrics?size=large')
  await card(page, 'local').getByRole('button', { name: 'Switch node-local card from large to small' }).click()
  await expect(card(page, 'local').getByText('40.0%', { exact: true })).toBeVisible()
  await expect(card(page, 'local').getByText('app', { exact: true })).toHaveCount(0)
  expect(await page.evaluate(() => JSON.parse(localStorage.getItem('suma-overview-node-card-sizes') || '{}').local)).toBe('small')
})

test('failed sections retry independently and do not become empty container lists', async ({ page }) => {
  const state = await setup(page)
  state.failures.add('/nodes/edge/overview/metrics?size=medium')
  state.failures.add('/nodes/edge/overview/disk')
  state.failures.add('/nodes/nas/overview/info')
  await page.goto('/')
  await expect(card(page, 'edge').getByText('Container data failed to load', { exact: true })).toBeVisible()
  await expect(card(page, 'edge')).not.toContainText('No running containers')
  await expect(card(page, 'local').getByText('40.0%', { exact: true })).toBeVisible()
  await expect(card(page, 'nas').getByText('Offline', { exact: true })).toBeVisible()
  expect(state.calls.some((path) => path.startsWith('/nodes/nas/overview/metrics'))).toBe(false)
  state.failures.delete('/nodes/edge/overview/metrics?size=medium')
  await card(page, 'edge').getByRole('button', { name: 'Retry: Container data failed to load' }).click()
  await expect(card(page, 'edge').getByText('app', { exact: true })).toBeVisible()
  await expect(card(page, 'edge').getByText('Disk usage failed to load', { exact: true })).toBeVisible()
  state.failures.clear()
  await card(page, 'nas').getByRole('button', { name: 'Retry: Node information failed to load' }).click()
  await expect(card(page, 'nas').getByText('app', { exact: true })).toBeVisible()
})

test('group filtering aborts removed cards and preserves keyboard ordering', async ({ page }) => {
  const state = await setup(page), delayed = gate()
  state.gates.set('/nodes/edge/overview/metrics?size=medium', delayed.promise)
  const aborted: string[] = []
  page.on('requestfailed', (request) => aborted.push(request.url()))
  await page.goto('/')
  await expect.poll(() => state.calls.includes('/nodes/edge/overview/metrics?size=medium')).toBe(true)
  await card(page, 'local').getByRole('button', { name: 'Reorder node-local card' }).focus()
  await page.keyboard.press('ArrowRight')
  await expect(page.locator('[data-node-card-id]').first()).toHaveAttribute('data-node-card-id', 'edge')
  await page.evaluate(async () => { const { useUIStore } = await import('/src/stores/ui.ts'); useUIStore.getState().setCurrentGroupFilter('group:1') })
  await expect(card(page, 'edge')).toHaveCount(0)
  await expect(card(page, 'local')).toBeVisible()
  await expect.poll(() => aborted.some((path) => path.includes('/nodes/edge/overview/metrics'))).toBe(true)
  delayed.release()
})

test('container counters refresh independently of slower disk accounting', async ({ page }) => {
  const state = await setup(page)
  await page.clock.install()
  await page.goto('/')
  await expect(card(page, 'local').getByText('40.0%', { exact: true })).toBeVisible()
  await expect(card(page, 'local').getByText('1.0 GB', { exact: true })).toBeVisible()
  const count = (suffix: string) => state.calls.filter((path) => path === `/nodes/local/overview/${suffix}`).length
  await page.clock.runFor(10_001)
  await expect.poll(() => count('metrics?size=small')).toBeGreaterThan(1)
  expect(count('disk')).toBe(1)
  await page.clock.fastForward(50_001)
  await expect.poll(() => count('disk')).toBe(2)
})

for (const width of [390, 1440]) for (const language of ['en-US', 'zh-CN']) for (const theme of ['dark', 'light']) {
  test(`overview sizes ${language} ${theme} ${width}`, async ({ page }) => {
    await setup(page, language, theme)
    await page.setViewportSize({ width, height: 1000 })
    await page.goto('/')
    await expect(card(page, 'edge').getByText('app', { exact: true })).toBeVisible()
    await expect(card(page, 'nas').getByText('Network', { exact: true })).toBeVisible()
    await expect(card(page, 'local').getByText('40.0%', { exact: true })).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    await page.screenshot({ path: `/tmp/suma-overview-${language}-${theme}-${width}.png`, fullPage: true })
  })
}
