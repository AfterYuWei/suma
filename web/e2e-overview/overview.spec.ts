import { expect, test, type Page, type Locator } from '@playwright/test'

const chartModules = new WeakMap<Page, string>()

const nodes = ['local', 'edge', 'nas'].map((id, index) => ({ id, name: `node-${id}`, connection_type: 'unix', endpoint: `unix:///${id}.sock`, tls_mode: 'disabled', enabled: true, status: 'online', group_ids: [index + 1], created_at: '2026-10-11T00:00:00Z', updated_at: '2026-10-11T00:00:00Z' }))
const info = { name: 'docker-host', server_version: '28.5', operating_system: 'Ubuntu 24.04', os_version: '24.04', architecture: 'amd64', kernel_version: '6.8', containers_running: 1, containers_paused: 0, containers_stopped: 2, images: 5, cpus: 2, memory_bytes: 4096 * 1024 ** 2, storage_driver: 'overlay2', logging_driver: 'json-file', cgroup_driver: 'systemd', cgroup_version: '2', default_runtime: 'runc', live_restore: true, security_options: ['name=seccomp'] }
function metrics(size: string) {
  const summary = { metrics_available: true, container_cpu_percent: 80, container_memory_bytes: 896 * 1024 ** 2 }
  if (size === 'small') return summary
  const row = { id: 'app', name: 'app', image: 'app:latest', state: 'running', available: true, cpu_percent: 80, memory_bytes: 896 * 1024 ** 2 }
  return { ...summary, container_network_rx_bytes: 2048, container_network_tx_bytes: 4096, container_block_read_bytes: 8192, container_block_write_bytes: 16384, longest_container_uptime_seconds: 120, containers: [size === 'large' ? { ...row, network_rx_bytes: 2048, network_tx_bytes: 4096, block_read_bytes: 8192, block_write_bytes: 16384, uptime_seconds: 120 } : row] }
}
function gate() { let release!: () => void; const promise = new Promise<void>((resolve) => { release = resolve }); return { promise, release } }

async function setup(page: Page, language = 'en-US', theme = 'dark') {
  const state = { calls: [] as string[], gates: new Map<string, Promise<void>>(), failures: new Set<string>(), overrides: new Map<string, unknown>() }
  page.on('request', request => { if (new URL(request.url()).pathname.endsWith('/echarts_core.js')) chartModules.set(page, request.url()) })
  await page.addInitScript(({ language, theme }) => {
    localStorage.setItem('suma-language', language)
    localStorage.setItem('suma-theme', theme)
    localStorage.setItem('suma-node-group', 'all')
    localStorage.setItem('suma-node', 'local')
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
    if (state.overrides.has(key)) data = state.overrides.get(key)
    const fail = state.failures.has(key)
    try {
      await route.fulfill({ status: fail ? 503 : 200, contentType: 'application/json', body: JSON.stringify({ code: fail ? 20202 : 0, message: fail ? 'controlled failure' : 'success', data: fail ? null : data }) })
    } catch { /* A switched/filtered node may already have aborted this request. */ }
  })
  return state
}
const row = (page: Page, id: string) => page.locator(`[data-node-resource-id="${id}"]`)
const detail = (page: Page, id = 'local') => row(page, id).locator('[data-node-detail-id]')
const select = (page: Page, id: string) => row(page, id).locator('button[aria-expanded]').click()
const expand = async (page: Page, id = 'local') => {
  const button = row(page, id).locator('button[aria-expanded]')
  if (await button.getAttribute('aria-expanded') !== 'true') await button.click()
}
const load = (page: Page, metric = 'cpu', id = 'local') => row(page, id).locator(`[data-node-load="${metric}"]`)

async function chartOption(chart: Locator) {
  const moduleURL = chartModules.get(chart.page())
  return chart.evaluate(async (element, moduleURL) => {
    if (!moduleURL) throw new Error('Missing chart module')
    const core = await import(/* @vite-ignore */ moduleURL)
    return core.getInstanceByDom(element).getOption()
  }, moduleURL)
}

async function setGroup(page: Page, filter: string) {
  await page.evaluate(async filter => {
    const { useUIStore } = await import('/src/stores/ui.ts')
    useUIStore.getState().setCurrentGroupFilter(filter)
  }, filter)
}

test('healthy node detail renders while another node and delivery summary are loading', async ({ page }) => {
  const state = await setup(page), delayedInfo = gate(), delayedCD = gate()
  state.gates.set('/nodes/nas/overview/info', delayedInfo.promise)
  state.gates.set('/cd/overview', delayedCD.promise)
  await page.goto('/'); await expand(page)
  await expect(row(page, 'nas')).toContainText('Connecting')
  await expect(load(page)).toHaveAttribute('data-value', '40')
  await expect(detail(page).getByText('app', { exact: true })).toBeVisible()
  expect(state.calls.filter(path => path === '/nodes/local/overview/metrics?size=medium')).toHaveLength(1)
  expect(state.calls.some(path => /overview\/(disk|resources)$/.test(path))).toBe(false)
  expect(state.calls.some(path => path.startsWith('/fleet/overview'))).toBe(false)
  delayedInfo.release(); delayedCD.release()
  await expect(row(page, 'nas')).toContainText('Online')
})

test('multiple nodes expand independently, collapse cancels detail and keeps global node context', async ({ page }) => {
  const state = await setup(page), delayed = gate(), aborted: string[] = []
  state.gates.set('/nodes/local/overview/metrics?size=medium', delayed.promise)
  page.on('requestfailed', request => aborted.push(request.url()))
  await page.goto('/')
  await expect(page.locator('[data-node-detail-id]')).toHaveCount(0)
  await expect(load(page)).toHaveAttribute('data-value', '40')
  expect(state.calls.some(path => path.includes('size=medium'))).toBe(false)
  await expand(page)
  await expect.poll(() => state.calls.includes('/nodes/local/overview/metrics?size=medium')).toBe(true)
  await expand(page, 'edge')
  await expect(detail(page, 'edge').getByText('app', { exact: true })).toBeVisible()
  await expect(page.locator('[data-node-detail-id]')).toHaveCount(2)
  expect(state.calls.filter(path => path === '/nodes/edge/overview/metrics?size=medium')).toHaveLength(1)
  await select(page, 'local')
  await expect(detail(page)).toHaveCount(0)
  await expect(detail(page, 'edge')).toBeVisible()
  await expect.poll(() => aborted.some(path => path.includes('/nodes/local/overview/metrics?size=medium'))).toBe(true)
  await expect(row(page, 'local').locator('[data-node-trend]')).toHaveCount(0)
  await expect(load(page)).toHaveAttribute('data-value', '40')
  expect(await page.evaluate(() => localStorage.getItem('suma-node'))).toBe('local')
  expect(state.calls.some(path => path.includes('size=large'))).toBe(false)
  delayed.release()
  await page.reload()
  await expect(page.locator('[data-node-detail-id]')).toHaveCount(0)
})

test('errors remain isolated, retry independently, and never become empty container lists', async ({ page }) => {
  const state = await setup(page)
  state.failures.add('/nodes/edge/overview/metrics?size=small')
  state.failures.add('/nodes/edge/overview/metrics?size=medium')
  state.failures.add('/nodes/nas/overview/info')
  await page.goto('/'); await expand(page)
  await expect(row(page, 'edge')).toContainText('Metrics unavailable')
  await expect(row(page, 'nas')).toContainText('Offline')
  await expect(load(page)).toHaveAttribute('data-value', '40')
  await select(page, 'edge')
  await expect(row(page, 'edge')).toContainText('Metrics unavailable')
  await expect(detail(page, 'edge')).not.toContainText('No running containers')
  state.failures.delete('/nodes/edge/overview/metrics?size=medium')
  await row(page, 'edge').getByRole('button', { name: 'Retry: Metrics unavailable' }).click()
  await expect(detail(page, 'edge').getByText('app', { exact: true })).toBeVisible()
  await select(page, 'nas')
  state.failures.delete('/nodes/nas/overview/info')
  await row(page, 'nas').getByRole('button', { name: 'Retry: Node information failed to load' }).click()
  await expect(load(page, 'cpu', 'nas')).toHaveAttribute('data-value', '40')
})

test('group filtering cancels removed nodes, clears removed expansions and preserves keyboard order', async ({ page }) => {
  const state = await setup(page), delayed = gate(), aborted: string[] = []
  state.gates.set('/nodes/nas/overview/metrics?size=small', delayed.promise)
  page.on('requestfailed', request => aborted.push(request.url()))
  await page.goto('/'); await expand(page)
  await expect.poll(() => state.calls.includes('/nodes/nas/overview/metrics?size=small')).toBe(true)
  await row(page, 'local').getByRole('button', { name: 'Reorder node-local node' }).focus()
  await page.keyboard.press('ArrowDown')
  await expect(page.locator('[data-node-resource-id]').first()).toHaveAttribute('data-node-resource-id', 'edge')
  await select(page, 'edge')
  await setGroup(page, 'group:1')
  await expect(page.locator('[data-node-resource-id]')).toHaveCount(1)
  await expect(detail(page)).toHaveAttribute('data-node-detail-id', 'local')
  await expect.poll(() => aborted.some(path => path.includes('/nodes/nas/overview/metrics'))).toBe(true)
  delayed.release()
  await setGroup(page, 'all')
  await expect(page.locator('[data-node-resource-id]').first()).toHaveAttribute('data-node-resource-id', 'edge')
  await expect(detail(page, 'edge')).toHaveCount(0)
})

test('all visible node counters refresh, with only expanded container details and no hidden disk accounting', async ({ page }) => {
  const state = await setup(page)
  await page.clock.install(); await page.goto('/'); await expand(page)
  await expect(load(page)).toHaveAttribute('data-value', '40')
  await expect(load(page, 'cpu', 'edge')).toHaveAttribute('data-value', '40')
  state.overrides.set('/nodes/local/overview/info', { ...info, containers_running: 4 })
  await page.clock.runFor(10_001)
  await expect.poll(() => state.calls.filter(path => path === '/nodes/local/overview/metrics?size=medium').length).toBeGreaterThan(1)
  await expect.poll(() => state.calls.filter(path => path === '/nodes/edge/overview/metrics?size=small').length).toBeGreaterThan(1)
  await expect(row(page, 'local').locator('[data-node-container-states]')).toContainText('Running4')
  expect(state.calls.some(path => /overview\/(disk|resources)$/.test(path))).toBe(false)
})

for (const width of [320, 390, 1440]) for (const language of ['en-US', 'zh-CN']) for (const theme of ['dark', 'light']) {
  test(`compact node resources ${language} ${theme} ${width}`, async ({ page }) => {
    const state = await setup(page, language, theme), errors: string[] = []
    page.on('pageerror', error => errors.push(error.message))
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await page.setViewportSize({ width, height: 1000 })
    await page.goto('/')
    await expect(load(page)).toHaveAttribute('data-value', '40')
    await expect(page.locator('[data-node-detail-id]')).toHaveCount(0)
    await expect(page.locator('[data-node-trend]')).toHaveCount(0)
    const summary = await row(page, 'local').evaluate(element => {
      const cpu = element.querySelector('[data-node-summary-metric=cpu]')!.getBoundingClientRect()
      const memory = element.querySelector('[data-node-summary-metric=memory]')!.getBoundingClientRect()
      const name = element.querySelector('button[aria-expanded] > span')!.getBoundingClientRect()
      return { cpuTop: cpu.top, memoryTop: memory.top, nameTop: name.top, height: element.getBoundingClientRect().height }
    })
    expect(Math.abs(summary.cpuTop - summary.memoryTop)).toBeLessThanOrEqual(16)
    expect(width === 1440 ? summary.height < 100 : summary.cpuTop > summary.nameTop).toBe(true)
    await page.locator('[data-node-resources]').screenshot({ path: `/tmp/suma-node-compact-collapsed-${language}-${theme}-${width}.png` })
    await expand(page)
    await expect(load(page)).toHaveAttribute('data-value', '40')
    await expect(detail(page).locator('[data-top-container]')).toHaveCount(1)
    await expect(page.locator('[data-node-trend] canvas')).toHaveCount(2)
    await expect.poll(async () => (await chartOption(detail(page).locator('[data-node-trend=cpu]'))).series[0].data.length).toBeGreaterThan(0)
    const layout = await page.locator('[data-node-resources]').evaluate(root => {
      const bounds = root.getBoundingClientRect()
      return {
        metrics: [...root.querySelectorAll('[data-node-metric]')].map(element => ({ top: element.getBoundingClientRect().top, width: element.getBoundingClientRect().width })),
        overflow: [...root.querySelectorAll('button, [data-node-load], [data-node-trend], [data-top-container]')].some(element => { const rect = element.getBoundingClientRect(); return rect.left < bounds.left - 1 || rect.right > bounds.right + 1 }) }
    })
    expect(layout.metrics.every(metric => metric.width >= 200)).toBe(true)
    expect(width === 1440 ? Math.abs(layout.metrics[0].top - layout.metrics[1].top) < 1 : layout.metrics[0].top < layout.metrics[1].top).toBe(true)
    expect(layout.overflow).toBe(false)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    await page.locator('[data-node-resources]').screenshot({ path: `/tmp/suma-node-compact-expanded-${language}-${theme}-${width}.png` })
    state.overrides.set('/nodes', [nodes[0]])
    state.overrides.set('/nodes/local/overview/info', { ...info, cpus: 8, memory_bytes: Math.floor(11.7 * 1024 ** 3) })
    state.overrides.set('/nodes/local/overview/metrics?size=small', { metrics_available: true, container_cpu_percent: 0, container_memory_bytes: 246 * 1024 ** 2 })
    state.overrides.set('/nodes/local/overview/metrics?size=medium', { ...metrics('medium'), container_cpu_percent: 0, container_memory_bytes: 246 * 1024 ** 2, containers: [{ id: 'postgres', name: 'postgres', available: true, memory_bytes: 246 * 1024 ** 2, cpu_percent: 0.2 }] })
    await page.reload()
    await expect(page.locator('[data-node-detail-id]')).toHaveCount(0)
    await expand(page)
    await expect(load(page)).toHaveAttribute('data-value', '0')
    await expect(load(page, 'memory')).toContainText('2.1')
    await expect(detail(page)).toContainText('postgres')
    const option = await chartOption(detail(page).locator('[data-node-trend=cpu]'))
    expect(option.xAxis[0].max - option.xAxis[0].min).toBe(300_000)
    expect(option.series[0].data.every((sample: number[]) => sample[1] === 0)).toBe(true)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    expect((await detail(page).boundingBox())!.height).toBeLessThan(width === 1440 ? 540 : 850)
    await detail(page).screenshot({ path: `/tmp/suma-node-compact-single-${language}-${theme}-${width}.png` })
    expect(errors).toEqual([])
  })
}

test('information loads on demand, retries separately, and aborts and stops polling on close', async ({ page }) => {
  const state = await setup(page), delayed = gate(), aborted: string[] = []
  page.on('requestfailed', request => aborted.push(request.url()))
  state.gates.set('/nodes/local/overview/disk', delayed.promise)
  state.failures.add('/nodes/local/overview/resources')
  await page.clock.install(); await page.goto('/')
  await expect(load(page)).toHaveAttribute('data-value', '40')
  expect(state.calls.some(path => /overview\/(disk|resources)$/.test(path))).toBe(false)
  await row(page, 'local').getByRole('button', { name: 'Node information for node-local' }).click()
  const sheet = page.getByRole('dialog')
  await expect(sheet).toContainText('docker-host')
  await expect(sheet).toContainText('overlay2')
  await expect(sheet.getByRole('status', { name: 'Disk usage' })).toBeVisible()
  state.failures.clear()
  await sheet.getByRole('button', { name: 'Retry: Resource counts', exact: true }).first().click()
  await expect(sheet.getByText('Failed, retry', { exact: true })).toHaveCount(0)
  await sheet.getByRole('button', { name: 'Close', exact: true }).click()
  await expect.poll(() => aborted.some(path => path.endsWith('/nodes/local/overview/disk'))).toBe(true)
  delayed.release()
  const count = state.calls.filter(path => /overview\/(disk|resources)$/.test(path)).length
  await page.clock.runFor(61_000)
  expect(state.calls.filter(path => /overview\/(disk|resources)$/.test(path))).toHaveLength(count)
})

test('open information has ten-second resource and sixty-second disk polling', async ({ page }) => {
  const state = await setup(page)
  await page.clock.install(); await page.goto('/')
  await row(page, 'local').getByRole('button', { name: 'Node information for node-local' }).click()
  await expect(page.getByRole('dialog').getByText('1.0 GB', { exact: true })).toBeVisible()
  const count = (suffix: string) => state.calls.filter(path => path === `/nodes/local/overview/${suffix}`).length
  const resources = count('resources'), disk = count('disk')
  await page.clock.runFor(10_001)
  await expect.poll(() => count('resources')).toBe(resources + 1)
  expect(count('disk')).toBe(disk)
  await page.clock.runFor(50_001)
  await expect.poll(() => count('disk')).toBe(disk + 1)
})

test('changing visible group closes the old information sheet and cancels its requests', async ({ page }) => {
  const state = await setup(page), delayed = gate(), aborted: string[] = []
  state.gates.set('/nodes/edge/overview/disk', delayed.promise)
  page.on('requestfailed', request => aborted.push(request.url()))
  await page.goto('/')
  await row(page, 'edge').getByRole('button', { name: 'Node information for node-edge' }).click()
  await expect(page.getByRole('dialog')).toBeVisible()
  await expect.poll(() => state.calls.includes('/nodes/edge/overview/disk')).toBe(true)
  await setGroup(page, 'group:1')
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(page.locator('[data-node-detail-id]')).toHaveCount(0)
  await expect.poll(() => aborted.some(path => path.endsWith('/nodes/edge/overview/disk'))).toBe(true)
  delayed.release()
})

test('Top 5 filters missing metrics, uses stable memory ordering and omits secondary metadata', async ({ page }) => {
  const state = await setup(page)
  state.overrides.set('/nodes/local/overview/metrics?size=medium', { ...metrics('medium'), metrics_available: false, containers: [
    { id: 'missing', name: 'missing', available: false, memory_bytes: 99999, cpu_percent: 80 },
    ...[1, 5, 3, 6, 2, 4].map(i => ({ id: `c${i}`, name: `container-${i}`, image: 'secret-image-tag', available: true, memory_bytes: i * 1024 ** 2, cpu_percent: i * 100 })),
  ] })
  await page.goto('/'); await expand(page)
  await expect(detail(page).locator('[data-top-container]')).toHaveCount(5)
  expect(await detail(page).locator('[data-top-container]').evaluateAll(rows => rows.map(row => (row as HTMLElement).dataset.topContainer))).toEqual(['c6', 'c5', 'c4', 'c3', 'c2'])
  await expect(detail(page)).toContainText('Incomplete data')
  await expect(detail(page)).not.toContainText('secret-image-tag')
  await expect(detail(page)).not.toContainText('missing')
  await expect(load(page)).toHaveAttribute('data-value', '')
  await expect(detail(page)).toContainText('CPU 600.0%')
})

test('zero containers remain distinct from missing metrics, offline and disabled nodes', async ({ page }) => {
  const state = await setup(page)
  state.overrides.set('/nodes/edge/overview/info', { ...info, containers_running: 0, containers_stopped: 0, containers_paused: 0 })
  state.overrides.set('/nodes/edge/overview/metrics?size=medium', { ...metrics('medium'), container_cpu_percent: 0, container_memory_bytes: 0, containers: [] })
  state.overrides.set('/nodes', nodes.map(node => node.id === 'nas' ? { ...node, enabled: false } : node))
  state.failures.add('/nodes/local/overview/info')
  await page.goto('/'); await expand(page)
  await expect(row(page, 'local')).toContainText('Offline')
  await select(page, 'edge')
  await expect(detail(page, 'edge')).toContainText('No running containers')
  await expect(load(page, 'cpu', 'edge')).toHaveAttribute('data-value', '0')
  await expect(load(page, 'memory', 'edge')).toHaveAttribute('data-value', '0')
  await select(page, 'nas')
  await expect(row(page, 'nas')).toContainText('Disabled')
  await expect(load(page, 'cpu', 'nas')).toHaveAttribute('data-value', '')
  expect(state.calls.some(path => path.startsWith('/nodes/nas/overview'))).toBe(false)
})

test('switching node details preserves recent history and identity changes reset it', async ({ page }) => {
  const state = await setup(page)
  await page.clock.install(); await page.goto('/'); await expand(page)
  await expect(load(page)).toHaveAttribute('data-value', '40')
  const cpu = detail(page).locator('[data-node-trend=cpu]')
  await expect(cpu.locator('canvas')).toHaveCount(1)
  const count = (await chartOption(cpu)).series[0].data.length
  await page.clock.runFor(10_001)
  await expect.poll(async () => (await chartOption(cpu)).series[0].data.length).toBeGreaterThan(count)
  const times = (await chartOption(cpu)).series[0].data.map((sample: number[]) => sample[0])
  await select(page, 'local'); await expect(detail(page)).toHaveCount(0)
  await expand(page, 'edge'); await expect(detail(page, 'edge')).toBeVisible()
  await select(page, 'local'); await expect(detail(page)).toHaveAttribute('data-node-detail-id', 'local')
  await expect.poll(async () => (await chartOption(cpu)).series[0].data.map((sample: number[]) => sample[0])).toEqual(expect.arrayContaining(times))
  state.overrides.set('/nodes', nodes.map(node => node.id === 'local' ? { ...node, updated_at: '2026-10-14T01:00:00Z' } : node))
  await page.clock.runFor(10_001)
  await expect(cpu).toHaveAttribute('data-sample-count', '1')
})

test('failure, stale sampling and recovery form chart gaps while over-100 values remain truthful', async ({ page }) => {
  const state = await setup(page)
  await page.clock.install(); await page.goto('/'); await expand(page)
  const cpu = detail(page).locator('[data-node-trend=cpu]')
  await expect(cpu.locator('canvas')).toHaveCount(1)
  state.failures.add('/nodes/local/overview/metrics?size=medium')
  await page.clock.runFor(10_001)
  await expect(row(page, 'local')).toContainText('Metrics unavailable')
  await expect.poll(async () => (await chartOption(cpu)).series[0].data.some((p: unknown[]) => p[1] === null)).toBe(true)
  state.failures.clear()
  state.overrides.set('/nodes/local/overview/metrics?size=medium', { ...metrics('medium'), container_cpu_percent: 280, container_memory_bytes: 6144 * 1024 ** 2 })
  await row(page, 'local').getByRole('button', { name: 'Retry: Metrics unavailable' }).click()
  await expect(load(page)).toHaveAttribute('data-value', '140')
  await expect(load(page, 'memory')).toHaveAttribute('data-value', '150')
  await expect.poll(() => cpu.evaluate(async (element, moduleURL) => {
    const core = await import(/* @vite-ignore */ moduleURL!)
    return core.getInstanceByDom(element).getModel().getComponent('yAxis').axis.scale.getExtent()[1]
  }, chartModules.get(page))).toBeGreaterThanOrEqual(140)
  const delayed = gate(); state.gates.set('/nodes/local/overview/metrics?size=medium', delayed.promise)
  await page.clock.runFor(31_001)
  await expect(row(page, 'local')).toContainText('Data is stale')
  await expect(load(page)).toHaveAttribute('data-value', '')
  delayed.release(); state.gates.clear()
  await expect(load(page)).toHaveAttribute('data-value', '140')
  const option = await chartOption(cpu)
  expect(option.series[0].connectNulls).toBe(false)
  expect(option.series[0].data.some((p: unknown[]) => p[1] === null)).toBe(true)
})

test('five-minute history clips old observations and reload starts a new history', async ({ page }) => {
  await setup(page)
  await page.clock.install(); await page.goto('/'); await expand(page)
  const cpu = detail(page).locator('[data-node-trend=cpu]')
  await expect(cpu.locator('canvas')).toHaveCount(1)
  await expect.poll(async () => (await chartOption(cpu)).series[0].data.length).toBeGreaterThan(0)
  const initial = (await chartOption(cpu)).series[0].data.at(-1)[0]
  await page.clock.runFor(10_001)
  await page.clock.fastForward(300_001)
  await expect.poll(async () => (await chartOption(cpu)).series[0].data.at(-1)?.[0] ?? 0).toBeGreaterThan(initial + 300_000)
  const times = (await chartOption(cpu)).series[0].data.map((point: number[]) => point[0])
  expect(Math.max(...times) - Math.min(...times)).toBeLessThanOrEqual(300_000)
  await page.clock.runFor(1000)
  const reloadedAt = await page.evaluate(() => Date.now())
  await page.reload(); await expand(page)
  await expect(cpu.locator('canvas')).toHaveCount(1)
  await expect.poll(async () => {
    const data = (await chartOption(cpu)).series[0].data
    return data.length > 0 && data.every((point: number[]) => point[0] >= reloadedAt)
  }).toBe(true)
})

test('all containers respects rejected node guards then switches target before navigation', async ({ page }) => {
  const state = await setup(page)
  await page.goto('/'); await select(page, 'edge')
  await page.evaluate(async () => {
    const { registerProjectNavigationGuard } = await import('/src/lib/project-navigation-guard.ts')
    ;(window as unknown as { removeGuard: () => void }).removeGuard = registerProjectNavigationGuard(async () => false)
  })
  await detail(page, 'edge').getByRole('button', { name: 'All containers', exact: true }).click()
  await expect(page).toHaveURL(/\/$/)
  expect(await page.evaluate(() => localStorage.getItem('suma-node'))).toBe('local')
  expect(state.calls.some(path => path.endsWith('/containers'))).toBe(false)
  await page.evaluate(() => (window as unknown as { removeGuard: () => void }).removeGuard())
  await detail(page, 'edge').getByRole('button', { name: 'All containers', exact: true }).click()
  await expect(page).toHaveURL(/\/containers$/)
  expect(await page.evaluate(() => localStorage.getItem('suma-node'))).toBe('edge')
  await expect.poll(() => state.calls.includes('/nodes/edge/containers')).toBe(true)
  expect(state.calls.includes('/nodes/local/containers')).toBe(false)
})

test('all containers waits for pending guard resolution and ignores repeated clicks', async ({ page }) => {
  const state = await setup(page)
  await page.goto('/'); await select(page, 'edge')
  await page.evaluate(async () => {
    const { registerProjectNavigationGuard } = await import('/src/lib/project-navigation-guard.ts')
    registerProjectNavigationGuard(() => new Promise<boolean>(resolve => { (window as unknown as { releaseGuard: () => void }).releaseGuard = () => resolve(true) }))
  })
  const button = detail(page, 'edge').getByRole('button', { name: 'All containers', exact: true })
  await button.click(); await expect(button).toBeDisabled()
  await expect(page).toHaveURL(/\/$/)
  expect(state.calls.some(path => path.endsWith('/containers'))).toBe(false)
  await page.evaluate(() => (window as unknown as { releaseGuard: () => void }).releaseGuard())
  await expect(page).toHaveURL(/\/containers$/)
  await expect.poll(() => state.calls.includes('/nodes/edge/containers')).toBe(true)
})

test('pointer reorder saves node list order across reload', async ({ page }) => {
  await setup(page); await page.emulateMedia({ reducedMotion: 'reduce' }); await page.goto('/'); await expand(page)
  const source = row(page, 'local').getByRole('button', { name: 'Reorder node-local node' })
  const from = (await source.boundingBox())!, target = (await row(page, 'edge').boundingBox())!
  await page.mouse.move(from.x + from.width / 2, from.y + from.height / 2)
  await page.mouse.down(); await page.mouse.move(target.x + target.width / 2, target.y + 20, { steps: 12 }); await page.mouse.up()
  await expect(page.locator('[data-node-resource-id]').first()).toHaveAttribute('data-node-resource-id', 'edge')
  expect(await page.evaluate(() => JSON.parse(localStorage.getItem('suma-overview-node-card-order') || '[]'))).toEqual(['edge', 'local', 'nas'])
  await page.reload()
  await expect(page.locator('[data-node-resource-id]').first()).toHaveAttribute('data-node-resource-id', 'edge')
})

for (const language of ['en-US', 'zh-CN']) for (const theme of ['dark', 'light']) {
  test(`hover stays in viewport, remains stable and disposes on collapse ${language} ${theme}`, async ({ page }) => {
    await setup(page, language, theme)
    await page.clock.install(); await page.goto('/'); await expand(page)
    const cpu = detail(page).locator('[data-node-trend=cpu]')
    await expect(cpu.locator('canvas')).toHaveCount(1)
    await expect.poll(async () => (await chartOption(cpu)).series[0].data.length).toBeGreaterThan(0)
    const moduleURL = chartModules.get(page)!
    await cpu.evaluate(async (element, moduleURL) => { const core = await import(/* @vite-ignore */ moduleURL); core.getInstanceByDom(element).dispatchAction({ type: 'showTip', seriesIndex: 0, dataIndex: 0 }) }, moduleURL)
    await expect(page.locator('[data-slot=chart-tooltip]')).toContainText(language === 'zh-CN' ? '观测时间' : 'Observed')
    await expect(page.locator('[data-slot=chart-tooltip]')).toContainText('40.0%')
    const bounds = (await page.locator('[data-slot=chart-tooltip]').boundingBox())!
    expect(bounds.x).toBeGreaterThanOrEqual(0); expect(bounds.x + bounds.width).toBeLessThanOrEqual(1440)
    await cpu.evaluate(element => {
      const host = element as HTMLElement; host.dataset.clears = '0'
      for (const canvas of element.querySelectorAll('canvas')) { const context = canvas.getContext('2d')!, clear = context.clearRect.bind(context); context.clearRect = (...args: Parameters<typeof clear>) => { host.dataset.clears = String(Number(host.dataset.clears) + 1); clear(...args) } }
    })
    await page.clock.runFor(3000)
    await expect(cpu).toHaveAttribute('data-clears', '0')
    expect((await chartOption(cpu)).series[0].lineStyle.color).toMatch(/^rgba\(/)
    await row(page, 'edge').getByRole('button', { name: language === 'zh-CN' ? '查看 node-edge 资源' : 'View node-edge resources', exact: true }).click()
    await select(page, 'local')
    await expect(detail(page)).toHaveCount(0)
    await expect(detail(page, 'edge')).toBeVisible()
    await expect(page.locator('[data-slot=chart-tooltip]')).toHaveCount(0)
    await expect(page.locator('[data-node-trend] canvas')).toHaveCount(2)
  })
}

test('mobile sheet wraps long values and its title stays clear of the close control', async ({ page }) => {
  const state = await setup(page, 'zh-CN', 'light')
  await page.setViewportSize({ width: 320, height: 900 })
  state.overrides.set('/nodes/local/overview/info', { ...info, name: 'very-long-hostname-'.repeat(8), security_options: ['seccomp:'.repeat(20)] })
  await page.goto('/'); await expand(page)
  await row(page, 'local').getByRole('button', { name: '查看 node-local 节点信息' }).click()
  const sheet = page.getByRole('dialog')
  await expect(sheet.getByText('1.0 GB', { exact: true })).toBeVisible()
  expect(await sheet.evaluate(element => element.scrollWidth <= element.clientWidth)).toBe(true)
  expect((await sheet.boundingBox())!.width).toBeCloseTo(320, 1)
  const title = (await sheet.getByRole('heading', { name: 'node-local · 节点信息' }).boundingBox())!, close = (await sheet.getByRole('button', { name: 'Close', exact: true }).boundingBox())!
  expect(title.x + title.width).toBeLessThanOrEqual(close.x)
  await page.keyboard.press('Escape'); await expect(sheet).toHaveCount(0)
})

test('many nodes use page scrolling and long names fit with several independent chart pairs', async ({ page }) => {
  const state = await setup(page)
  state.overrides.set('/nodes', Array.from({ length: 20 }, (_, i) => ({ ...nodes[0], id: `n${i}`, name: `long-node-name-${i}-`.repeat(6) })))
  state.overrides.set('/nodes/n0/overview/metrics?size=medium', { ...metrics('medium'), containers: Array.from({ length: 16 }, (_, i) => ({ id: `c${i}`, name: `long-container-name-${i}-`.repeat(8), available: true, memory_bytes: i * 1024 ** 2, cpu_percent: i })) })
  await page.goto('/')
  await expect(page.locator('[data-node-resource-id]')).toHaveCount(20)
  await expect(page.locator('[data-node-detail-id]')).toHaveCount(0)
  expect(state.calls.some(path => path.includes('size=medium'))).toBe(false)
  await expand(page, 'n0'); await expand(page, 'n1')
  await expect(detail(page, 'n0').locator('[data-top-container]')).toHaveCount(5)
  await expect(page.locator('[data-node-trend] canvas')).toHaveCount(4)
  expect(await page.locator('[data-node-resources]').evaluate(root => [...root.querySelectorAll('*')].every(element => !['auto', 'scroll'].includes(getComputedStyle(element).overflowY)))).toBe(true)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await page.setViewportSize({ width: 320, height: 900 })
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
})

test('keyboard expansion and auxiliary controls stay independent, collapsed details stop polling', async ({ page }) => {
  const state = await setup(page)
  await page.clock.install(); await page.goto('/')
  const button = row(page, 'local').locator('button[aria-expanded]')
  await expect(button).toHaveAttribute('aria-expanded', 'false')
  await button.focus(); await page.keyboard.press('Enter')
  await expect(button).toHaveAttribute('aria-expanded', 'true')
  await expect(detail(page).getByText('app', { exact: true })).toBeVisible()
  await expand(page, 'edge')
  await expect(detail(page, 'edge').getByText('app', { exact: true })).toBeVisible()
  await page.clock.runFor(10_001)
  const count = (id: string, size: string) => state.calls.filter(path => path === `/nodes/${id}/overview/metrics?size=${size}`).length
  expect(count('local', 'medium')).toBeGreaterThan(1)
  expect(count('edge', 'medium')).toBeGreaterThan(1)
  await button.focus(); await page.keyboard.press('Space')
  await expect(detail(page)).toHaveCount(0)
  const medium = count('local', 'medium'), edgeMedium = count('edge', 'medium'), small = count('local', 'small')
  await page.clock.runFor(10_001)
  expect(count('local', 'medium')).toBe(medium)
  await expect.poll(() => count('local', 'small')).toBeGreaterThan(small)
  await expect.poll(() => count('edge', 'medium')).toBeGreaterThan(edgeMedium)
  await row(page, 'local').getByRole('button', { name: 'Node information for node-local' }).click()
  await expect(page.getByRole('dialog')).toBeVisible()
  await expect(button).toHaveAttribute('aria-expanded', 'false')
  await page.keyboard.press('Escape')
  await row(page, 'local').getByRole('button', { name: 'Reorder node-local node' }).focus()
  await page.keyboard.press('ArrowDown')
  await expect(page.locator('[data-node-resource-id]').first()).toHaveAttribute('data-node-resource-id', 'edge')
  await expect(button).toHaveAttribute('aria-expanded', 'false')
  await expect(detail(page, 'edge')).toBeVisible()
})

for (const language of ['en-US', 'zh-CN']) for (const theme of ['dark', 'light']) {
  test(`touch summary controls fit and stay separate ${language} ${theme}`, async ({ browser }) => {
    const context = await browser.newContext({ hasTouch: true, viewport: { width: 320, height: 900 } })
    const page = await context.newPage()
    try {
      const state = await setup(page, language, theme)
      await page.goto('/')
      await expect(load(page)).toHaveAttribute('data-value', '40')
      const summary = row(page, 'local').locator('button[aria-expanded]')
      const drag = row(page, 'local').getByRole('button', { name: /^(Reorder|调整)/ })
      const infoButton = row(page, 'local').getByRole('button', { name: /^(Node information|查看.*节点信息)/ })
      const dragBounds = (await drag.boundingBox())!, infoBounds = (await infoButton.boundingBox())!
      const nameBounds = (await summary.locator('> span').first().locator('> span').first().boundingBox())!
      const chevronBounds = (await summary.locator('> span').first().locator('> svg').boundingBox())!
      expect(dragBounds.width).toBeGreaterThanOrEqual(44)
      expect(infoBounds.width).toBeGreaterThanOrEqual(44)
      expect(nameBounds.x).toBeGreaterThanOrEqual(dragBounds.x + dragBounds.width)
      expect(chevronBounds.x + chevronBounds.width).toBeLessThanOrEqual(infoBounds.x)
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
      await row(page, 'local').screenshot({ path: `/tmp/suma-node-compact-touch-${language}-${theme}.png` })
      await infoButton.tap()
      await expect(page.getByRole('dialog')).toBeVisible()
      await expect(summary).toHaveAttribute('aria-expanded', 'false')
      expect(state.calls.some(path => path.includes('size=medium'))).toBe(false)
      await page.getByRole('button', { name: 'Close', exact: true }).tap()
      await summary.tap()
      await expect(detail(page).getByText('app', { exact: true })).toBeVisible()
      await summary.tap()
      await expect(detail(page)).toHaveCount(0)
    } finally {
      await context.close()
    }
  })
}

test('initial summary and detail observations retain a truthful current-value reference', async ({ page }) => {
  await setup(page)
  await page.clock.install(); await page.goto('/')
  await expect(load(page)).toHaveAttribute('data-value', '40')
  await page.clock.runFor(1000)
  await expand(page)
  const cpu = detail(page).locator('[data-node-trend=cpu]')
  await expect(cpu.locator('canvas')).toHaveCount(1)
  await expect.poll(async () => (await chartOption(cpu)).series[0].data.length).toBeGreaterThanOrEqual(2)
  expect((await chartOption(cpu)).series[0].markLine.data[0].yAxis).toBe(40)
  await expect(detail(page)).toContainText('Collecting trend')
  await page.clock.runFor(10_001)
  await expect.poll(async () => (await chartOption(cpu)).series[0].markLine.data.length).toBe(0)
  await expect(detail(page)).not.toContainText('Collecting trend')
})

for (const width of [320, 1440]) for (const language of ['en-US', 'zh-CN']) for (const theme of ['dark', 'light']) {
  test(`node expansion keeps overview headings and text stable ${language} ${theme} ${width}`, async ({ page }) => {
    await setup(page, language, theme)
    await page.emulateMedia({ reducedMotion: 'no-preference' })
    await page.setViewportSize({ width, height: 1000 })
    await page.goto('/')
    await expect(load(page)).toHaveAttribute('data-value', '40')
    await expect(page.locator('[data-slot=card-content]')).toContainText(language === 'zh-CN' ? '暂无交付项目' : 'No delivery projects')
    await page.evaluate(() => document.fonts.ready)
    const changes = await page.evaluate(async () => {
      const heading = document.getElementById('overview-nodes-heading')!
      const stationary = [heading, heading.nextElementSibling!, heading.parentElement!.parentElement!.querySelector('button')!]
      const summary = document.querySelector('[data-node-resource-id="local"] button[aria-expanded]')!
      const name = summary.querySelector('span > span > span')!
      const delivery = document.querySelector('[data-slot=card-title]')!
      const sizes = [name, delivery]
      const rect = (element: Element) => { const box = element.getBoundingClientRect(); return { x: box.x, y: box.y, width: box.width, height: box.height } }
      const origin = stationary.map(rect), originalSizes = sizes.map(rect)
      let movement = 0, distortion = 0
      for (let toggle = 0; toggle < 2; toggle++) {
        ;(summary as HTMLButtonElement).click()
        const started = performance.now()
        do {
          await new Promise<void>(resolve => requestAnimationFrame(() => resolve()))
          stationary.forEach((element, index) => {
            const box = rect(element), before = origin[index]
            movement = Math.max(movement, Math.abs(box.x - before.x), Math.abs(box.y - before.y))
            distortion = Math.max(distortion, Math.abs(box.width - before.width), Math.abs(box.height - before.height))
          })
          sizes.forEach((element, index) => {
            const box = rect(element), before = originalSizes[index]
            distortion = Math.max(distortion, Math.abs(box.width - before.width), Math.abs(box.height - before.height))
          })
        } while (performance.now() - started < 550)
      }
      return { movement, distortion }
    })
    expect(changes.movement).toBeLessThan(1)
    expect(changes.distortion).toBeLessThan(1)
    await expect(detail(page)).toHaveCount(0)
  })
}

test('reduced motion completes node position changes immediately', async ({ page }) => {
  await setup(page, 'zh-CN', 'dark')
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await page.goto('/')
  await expect(load(page)).toHaveAttribute('data-value', '40')
  const heading = page.locator('#overview-nodes-heading'), before = (await heading.boundingBox())!
  await expand(page); await expand(page, 'edge')
  await expect(page.locator('[data-node-detail-id]')).toHaveCount(2)
  expect(await page.locator('[data-node-resources]').evaluate(async root => {
    await new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve())))
    return [...root.querySelectorAll('[data-node-resource-id]')].every(element => {
      const transform = getComputedStyle(element).transform
      return transform === 'none' || new DOMMatrixReadOnly(transform).isIdentity
    })
  })).toBe(true)
  const after = (await heading.boundingBox())!
  expect(after.x).toBeCloseTo(before.x, 1); expect(after.y).toBeCloseTo(before.y, 1)
  expect(after.height).toBeCloseTo(before.height, 1)
})
