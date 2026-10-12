import { expect, test, type Locator, type Page } from '@playwright/test'
import type { ContainerMetrics, ContainerOverview, ContainerRuntime, ContainerSummary } from '../src/features/containers/types'

const started = '2026-10-12T00:00:00Z'
const names = ['suma', 'vaultwarden', 'derp-server', 'traefik', 'postgres', 'redis']
const containers: ContainerSummary[] = names.map((name, index) => ({
  id: String(index + 1).repeat(64), name, image: index === 0 ? 'ghcr.io/afteryuwei/a-very-long-container-image-name:2026.10.12' : `${name}:latest`,
  command: 'serve', created: started, state: index === 4 ? 'exited' : 'running', status: index === 4 ? 'Exited (0)' : 'Up 34 days',
  labels: index === 5 ? {} : { 'com.docker.compose.project': `project-${name}`, 'com.docker.compose.service': name },
  ports: index === 0 ? [
    { ip: '0.0.0.0', public_port: 32830, private_port: 8080, type: 'tcp' },
    { ip: '::', public_port: 32830, private_port: 8080, type: 'tcp' },
    { ip: '127.0.0.1', public_port: 32831, private_port: 8081, type: 'tcp' },
    { ip: '10.0.0.5', public_port: 32832, private_port: 8082, type: 'udp' },
  ] : [{ private_port: 80 + index, type: 'tcp' }],
  cpu_percent: 0, memory_bytes: 0, uptime_seconds: 0,
}))
const runtime: ContainerRuntime[] = containers.map((row, index) => ({ id: row.id, health_status: ['healthy', 'none', 'unhealthy', 'starting', 'none', null][index], restart_count: index === 5 ? null : index === 2 ? 3 : 0, memory_limit_bytes: index === 5 ? null : index % 2 ? 0 : 1024 ** 3 }))
const overviews: ContainerOverview[] = containers.map(row => ({
  id: row.id, network_mode: 'bridge', counts: { networks: 1, mounts: 1, ports: row.ports.length },
  ports: row.ports.slice(0, 4).map(port => ({ ...port, host_binding: !!port.public_port })),
  networks: [{ name: 'app_default', ip_address: '172.20.0.2', ipv6_address: 'fd00::2' }],
  mounts: [{ type: 'volume', name: 'app_data', destination: '/data', read_write: true, anonymous: false }],
}))
const nodes = ['local', 'edge'].map(id => ({ id, name: id, connection_type: 'unix', endpoint: 'unix:///var/run/docker.sock', tls_mode: 'disabled', enabled: true, status: 'online', last_latency_ms: 12, group_ids: [] }))
const gate = () => { let resolve!: () => void; const promise = new Promise<void>(done => { resolve = done }); return { promise, resolve } }

async function setup(page: Page, language = 'en-US', theme = 'dark') {
  const state = { calls: [] as string[], gates: new Map<string, Promise<void>>(), failures: new Set<string>(), rows: structuredClone(containers), metadata: structuredClone(runtime), overviews: structuredClone(overviews), seconds: 0, start: started, rollback: false, cpu: 0.2, memory: 19 * 1024 ** 2 }
  await page.addInitScript(({ language, theme }) => {
    localStorage.setItem('suma-language', language)
    localStorage.setItem('suma-theme', theme)
    localStorage.setItem('suma-node-group', 'all')
    localStorage.setItem('suma-node', 'local')
  }, { language, theme })
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname.replace('/api/v1', '')
    state.calls.push(`${route.request().method()} ${path}`)
    await state.gates.get(path)
    let data: unknown = []
    if (path === '/auth/status') data = { needs_setup: false }
    else if (path === '/auth/session') data = { id: 1, username: 'admin', nickname: 'Admin', email: 'admin@example.test' }
    else if (path === '/settings') data = {}
    else if (path === '/nodes') data = nodes
    else if (path === '/node-groups') data = []
    else if (path === '/notifications/inbox') data = { unread: 0, events: [] }
    else if (path.endsWith('/image-updates')) data = { results: [] }
    else if (path.endsWith('/containers/runtime')) data = state.metadata
    else if (/\/containers\/[^/]+\/overview$/.test(path)) data = state.overviews.find(row => row.id === path.split('/').at(-2))
    else if (/\/containers\/[^/]+\/runtime$/.test(path)) {
      const id = path.split('/').at(-2)!, row = state.rows.find(r => r.id === id)!, meta = state.metadata.find(r => r.id === id)!
      data = { runtime: { ...meta, state: row.state, started_at: started, finished_at: row.state === 'exited' ? started : null, exit_code: row.state === 'exited' ? 0 : null, oom_killed: false, observed_at: started }, restart_policy: 'unless-stopped', health: { configured: meta.health_status !== 'none', failing_streak: meta.health_status === 'unhealthy' ? 3 : 0, last_checked_at: started, command: 'CMD check token=[REDACTED]', interval_ms: 30000, timeout_ms: 3000, start_period_ms: 0, start_interval_ms: 0, retries: 3, logs: meta.health_status === 'none' ? [] : [{ started_at: started, finished_at: started, exit_code: meta.health_status === 'unhealthy' ? 1 : 0, output: 'controlled health output token=[REDACTED]', output_redacted: true, output_truncated: false }] } }
    }
    else if (/\/containers\/[^/]+\/events$/.test(path)) data = { source: 'docker', coverage: 'engine_retained', events: [], truncated: false }
    else if (/\/containers\/(?!metrics$)[^/]+$/.test(path) && route.request().method() === 'GET') {
      const row = state.rows.find(r => r.id === path.split('/').at(-1))!
      data = { ...row, pid: 12, entrypoint: [], working_directory: '/app', restart_policy: 'unless-stopped', read_only_rootfs: false, environment: [], network_mode: 'bridge', networks: [{ name: 'app_default', ip_address: '172.20.0.2', ipv6_address: 'fd00::2', gateway: '172.20.0.1', mac_address: '02:42:ac:14:00:02' }], mounts: [{ type: 'volume', name: 'app_data', source: '/var/lib/docker/volumes/app_data/_data', destination: '/data', read_write: true }] }
    }
    else if (path.endsWith('/containers/metrics')) {
      state.seconds += 5
      data = state.rows.filter(row => row.state === 'running').map(row => ({ id: row.id, available: true, cpu_percent: state.cpu, memory_bytes: state.memory, uptime_seconds: 86400 * 34,
        network_rx_bytes: state.rollback ? 0 : state.seconds * 2048, network_tx_bytes: state.rollback ? 0 : state.seconds * 1024,
        sampled_at: new Date(Date.parse(started) + 60_000 + state.seconds * 1000).toISOString(), started_at: state.start,
      } satisfies ContainerMetrics))
    } else if (path.endsWith('/containers')) data = path.includes('/edge/') ? state.rows.map(row => ({ ...row, name: `edge-${row.name}` })) : state.rows
    else if (path.endsWith('/containers/batch')) data = { results: JSON.parse(route.request().postData() ?? '{}').ids.map((id: string) => ({ id, success: true })) }
    const fail = state.failures.has(path)
    try { await route.fulfill({ status: fail ? 503 : 200, contentType: 'application/json', body: JSON.stringify({ code: fail ? 20209 : 0, message: fail ? 'controlled failure' : 'success', data: fail ? null : data }) }) } catch { /* Cancellation is expected when leaving this node/page. */ }
  })
  return state
}

const rowFor = (page: Page, name: string) => page.locator('tbody tr').filter({ has: page.getByRole('link', { name, exact: true }) })

async function strokeBounds(chart: Locator, sparkline = true) {
  return chart.evaluate((element, sparkline) => {
    const host = element as HTMLElement
    let minX = Infinity, maxX = -Infinity, minY = Infinity, maxY = -Infinity
    const left = sparkline ? 3 : 46, right = sparkline ? 3 : 12
    const top = sparkline ? 3 : 26, bottom = sparkline ? 3 : 24
    const cpu = element.getAttribute('aria-label')?.startsWith('CPU')
    const token = sparkline ? cpu ? '--foreground' : '--muted-foreground' : cpu ? '--chart-1' : '--chart-2'
    const swatch = document.createElement('canvas'); swatch.width = swatch.height = 1
    const context = swatch.getContext('2d')!
    context.fillStyle = getComputedStyle(document.documentElement).getPropertyValue(token).trim()
    context.fillRect(0, 0, 1, 1)
    const color = context.getImageData(0, 0, 1, 1).data
    for (const canvas of element.querySelectorAll('canvas')) {
      const scale = canvas.width / host.clientWidth
      const rgba = canvas.getContext('2d')!.getImageData(0, 0, canvas.width, canvas.height).data
      for (let y = Math.ceil(top * scale); y < (host.clientHeight - bottom) * scale; y++) {
        for (let x = Math.ceil(left * scale); x < (host.clientWidth - right) * scale; x++) {
          const offset = (y * canvas.width + x) * 4
          if (rgba[offset + 3] < 128 || color.slice(0, 3).some((value, channel) => Math.abs(value - rgba[offset + channel]) > 2)) continue
          minX = Math.min(minX, x / scale); maxX = Math.max(maxX, x / scale)
          minY = Math.min(minY, y / scale); maxY = Math.max(maxY, y / scale)
        }
      }
    }
    return { width: (maxX - minX) / (host.clientWidth - left - right), height: maxY - minY }
  }, sparkline)
}

test('diagnostics and health disclose controlled facts and close with focus restored', async ({ page }) => {
  await setup(page)
  await page.goto('/containers')
  const trigger = rowFor(page, 'derp-server').getByRole('button', { name: 'Runtime diagnostics derp-server' })
  await trigger.click()
  const popup = page.locator('[data-slot="popover-content"]')
  await expect(popup).toContainText('Restarts')
  await expect(popup).toContainText('unless-stopped')
  await expect(popup).toContainText('controlled health output token=[REDACTED]')
  await page.keyboard.press('Escape')
  await expect(trigger).toBeFocused()
  await rowFor(page, 'vaultwarden').getByRole('button', { name: 'Health check vaultwarden' }).click()
  await expect(popup).toContainText('No health check configured')
})

test('network and mount disclosures are lazy and mobile uses a sheet', async ({ page }) => {
  const state = await setup(page)
  await page.goto('/containers')
  const detailPath = `/nodes/local/containers/${containers[0].id}`
  expect(state.calls).not.toContain(`GET ${detailPath}`)
  expect(state.calls.some(call => call.endsWith('/overview'))).toBe(false)
  await rowFor(page, 'suma').getByRole('button', { name: 'Container overview suma' }).click()
  const expansion = page.locator(`#container-expansion-${containers[0].id}`)
  await expect(expansion).toContainText('172.20.0.2')
  await expect(expansion).toContainText('fd00::2')
  await expect(expansion).toContainText('app_data')
  await expect(expansion).toContainText('Read/write')
  await expect(expansion).not.toContainText('2.5 CPU')
  await expect(expansion).not.toContainText('1000:1000')
  await expect(expansion.getByRole('status')).toHaveCount(0)
  await expect(expansion).toContainText('[::]:32830 → 8080')
  expect(state.calls).toContain(`GET ${detailPath}/overview`)
  expect(state.calls).not.toContain(`GET ${detailPath}`)
  await page.setViewportSize({ width: 390, height: 844 })
  await expect(page.locator('[data-slot="sheet-content"]')).toContainText('app_data')
  await page.keyboard.press('Escape')
  await expect(page.locator('[data-slot="sheet-content"]')).not.toBeVisible()
  await page.setViewportSize({ width: 1440, height: 1000 })
  await rowFor(page, 'derp-server').getByRole('button', { name: 'Container overview derp-server' }).click()
  await expect(page.locator(`#container-expansion-${containers[2].id}`).getByRole('status').filter({ hasText: 'Health check failed' })).toBeVisible()
})

test('status filters, multi-filter intersection, sorting and persisted node views work', async ({ page }) => {
  await setup(page)
  await page.goto('/containers')
  await page.getByRole('button', { name: 'stopped 1', exact: true }).click()
  await expect(page.locator('tbody > tr')).toHaveCount(1)
  await expect(rowFor(page, 'postgres')).toBeVisible()
  await page.getByRole('button', { name: 'Clear filters' }).click()
  await page.getByRole('button', { name: 'Health', exact: true }).click()
  await page.getByRole('menuitemcheckbox', { name: 'Unhealthy', exact: true }).click()
  await page.keyboard.press('Escape')
  await expect(page.locator('tbody > tr')).toHaveCount(1)
  await expect(rowFor(page, 'derp-server')).toBeVisible()
  await page.reload()
  await expect(rowFor(page, 'derp-server')).toBeVisible()
  await expect(page.locator('tbody > tr')).toHaveCount(1)
})

test('overview cancellation and row switches keep queries independent', async ({ page }) => {
  const state = await setup(page), delayed = gate()
  const path = `/nodes/local/containers/${containers[0].id}/overview`
  state.gates.set(path, delayed.promise)
  await page.goto('/containers')
  const trigger = rowFor(page, 'suma').getByRole('button', { name: 'Container overview suma' })
  await trigger.click()
  const expansion = page.locator(`#container-expansion-${containers[0].id}`)
  await expect(expansion).toContainText('Loading')
  const canceled = page.waitForEvent('requestfailed', request => request.url().endsWith(path))
  await trigger.click()
  await canceled
  await expect(expansion).toHaveCount(0)
  delayed.resolve()
  await rowFor(page, 'vaultwarden').getByRole('button', { name: 'Container overview vaultwarden' }).click()
  const other = page.locator(`#container-expansion-${containers[1].id}`)
  await expect(other).toContainText('81')
  await expect(other).not.toContainText('32830')
  await expect(other.getByRole('link', { name: 'View details →', exact: true })).toHaveAttribute('href', `/containers/${containers[1].id}`)
  expect(state.calls.filter(call => /GET .*containers\/[^/]+$/.test(call) && !call.endsWith('/runtime') && !call.endsWith('/metrics'))).toHaveLength(0)
})

test('overview failures retry and the quick view stays focused', async ({ page }) => {
  const state = await setup(page), path = `/nodes/local/containers/${containers[0].id}/overview`
  state.failures.add(path)
  await page.goto('/containers')
  await rowFor(page, 'suma').getByRole('button', { name: 'Container overview suma' }).click()
  const expansion = page.locator(`#container-expansion-${containers[0].id}`)
  await expect(expansion).toContainText('Data unavailable')
  state.failures.delete(path)
  await expansion.getByRole('button', { name: 'Retry', exact: true }).click()
  await expect(expansion).toContainText('[::]:32830 → 8080')
  await expect(expansion.getByRole('heading')).toHaveText(['Resource trends', 'Networks', 'Mounts', 'Ports'])
  await expect(expansion.locator('summary')).toHaveCount(0)
  await expect(expansion).not.toContainText('DATABASE_PASSWORD')
  await expect(expansion).not.toContainText('serve --token')
  await expect(expansion).not.toContainText('PID limit')
  await expect(expansion).not.toContainText('Container ID')
  const readsBeforeRefresh = state.calls.filter(call => call === `GET ${path}`).length
  await expansion.getByRole('button', { name: 'Refresh container overview' }).click()
  await expect.poll(() => state.calls.filter(call => call === `GET ${path}`).length).toBeGreaterThan(readsBeforeRefresh)
})

test('unknown overview fields do not imply unlimited resources or defaults', async ({ page }) => {
  const state = await setup(page)
  state.overviews[0] = { id: containers[0].id, network_mode: null, counts: { networks: 0, mounts: 0, ports: 0 }, networks: [], mounts: [], ports: [] }

  await page.goto('/containers')
  await rowFor(page, 'suma').getByRole('button', { name: 'Container overview suma' }).click()
  const expansion = page.locator(`#container-expansion-${containers[0].id}`)
  await expect(expansion).toContainText('No network, mount or port information available')
  await expect(expansion).not.toContainText('∞')
  await expect(expansion).not.toContainText('Default (root)')
  await expect(expansion.getByRole('heading')).toHaveText(['Resource trends'])
})

test('resource trends use shared samples and do not open per-row streams', async ({ page }) => {
  await setup(page)
  await page.clock.install({ time: new Date('2026-10-12T00:10:00Z') })
  const sockets: string[] = []
  page.on('websocket', ws => sockets.push(ws.url()))
  await page.goto('/containers')
  await expect(rowFor(page, 'suma')).toContainText('19 MB / 1.00 GB')
  await page.clock.runFor(5000)
  await rowFor(page, 'suma').getByRole('button', { name: 'Resource trends suma' }).click()
  await expect(page.locator('[data-slot="popover-content"]')).toContainText('View full statistics')
  await expect(page.locator('[data-slot="popover-content"] canvas')).toHaveCount(2)
  expect(sockets.filter(url => new URL(url).pathname.startsWith('/ws/nodes/'))).toHaveLength(0)
})

test('quick-view charts share list samples and topology never polls', async ({ page }) => {
  const state = await setup(page)
  await page.clock.install({ time: new Date('2026-10-12T00:10:00Z') })
  const sockets: string[] = []
  page.on('websocket', socket => sockets.push(socket.url()))
  await page.goto('/containers')
  const row = rowFor(page, 'suma'), path = `/nodes/local/containers/${containers[0].id}/overview`
  const sampleReads = () => state.calls.filter(call => call === 'GET /nodes/local/containers/metrics').length
  const topologyReads = () => state.calls.filter(call => call === `GET ${path}`).length
  await expect(row).toContainText('19 MB / 1.00 GB')
  await page.clock.runFor(5000)
  const before = sampleReads()
  const trigger = row.getByRole('button', { name: 'Container overview suma' })
  await trigger.click()
  const expansion = page.locator(`#container-expansion-${containers[0].id}`)
  const trends = expansion.getByRole('region', { name: 'Resource trends' })
  await expect(expansion.locator('canvas')).toHaveCount(2)
  await expect(expansion).toContainText('app_data')
  expect(sampleReads()).toBe(before)
  const loaded = topologyReads()
  state.cpu = 200; state.memory = 32 * 1024 ** 2
  await page.clock.runFor(15_000)
  await expect(trends).toContainText('200.0%')
  await expect(trends).toContainText('32 MB')
  expect(topologyReads()).toBe(loaded)
  expect(sampleReads()).toBe(before + 3)
  await trigger.click(); await trigger.click()
  await expect(expansion).toContainText('app_data')
  expect(topologyReads()).toBe(loaded)
  state.failures.add(path)
  await expansion.getByRole('button', { name: 'Refresh container overview' }).click()
  await expect(expansion).toContainText('Data unavailable')
  await expect(trends).toContainText('200.0%')
  await expect(expansion.locator('canvas')).toHaveCount(2)
  state.failures.add('/nodes/local/containers/metrics')
  await page.clock.runFor(5000)
  await expect(trends).toContainText('Samples unavailable')
  await expect(trends).not.toContainText('200.0%')
  await expect(trends).not.toContainText('0.0%')
  expect(sockets.filter(url => new URL(url).pathname.startsWith('/ws/nodes/'))).toHaveLength(0)
})

for (const theme of ['dark', 'light']) {
  test(`quick-view charts fill the width from the first sample and reshape ${theme}`, async ({ page }) => {
    const state = await setup(page, 'en-US', theme)
    state.cpu = 20; state.memory = 296 * 1024 ** 2
    await page.clock.install({ time: new Date('2026-10-12T00:10:00Z') })
    await page.goto('/containers')
    await expect(rowFor(page, 'suma')).toContainText('296 MB')
    await rowFor(page, 'suma').getByRole('button', { name: 'Container overview suma' }).click()
    const expansion = page.locator(`#container-expansion-${containers[0].id}`)
    await expect(expansion.locator('canvas')).toHaveCount(2)
    await page.clock.pauseAt(await page.evaluate(() => new Date(Date.now() + 500).toISOString()))
    await page.clock.runFor(100)
    await expect(expansion.getByRole('region', { name: 'Resource trends' })).toContainText('Sampling')
    const charts = expansion.getByRole('img')
    for (const index of [0, 1]) {
      const first = await strokeBounds(charts.nth(index))
      expect(first.width).toBeGreaterThan(0.95)
      expect(first.height).toBeLessThan(3)
      const bounds = await charts.nth(index).boundingBox()
      await page.mouse.move(bounds!.x + bounds!.width / 4, bounds!.y + bounds!.height / 2)
      await page.clock.runFor(100)
      await expect(page.locator('[data-slot="chart-tooltip"]:visible')).toContainText(index === 0 ? '20.0%' : '296.0 MB')
    }
    await page.screenshot({ path: `/tmp/suma-chart-first-sample-${theme}.png`, animations: 'disabled' })
    await page.mouse.move(0, 0)
    state.cpu = 80; state.memory = 128 * 1024 ** 2
    await page.clock.runFor(5000)
    await expect(rowFor(page, 'suma')).toContainText('128 MB')
    await page.clock.runFor(100)
    for (const index of [0, 1]) {
      const next = await strokeBounds(charts.nth(index))
      expect(next.width).toBeGreaterThan(0.95)
      expect(next.height).toBeGreaterThan(20)
    }
    await page.screenshot({ path: `/tmp/suma-chart-growing-samples-${theme}.png`, animations: 'disabled' })
  })
}

for (const language of ['zh-CN', 'en-US']) for (const theme of ['dark', 'light']) {
  test(`chart hover keeps strokes visible ${language} ${theme}`, async ({ page }) => {
    const state = await setup(page, language, theme)
    state.cpu = 0; state.memory = 296 * 1024 ** 2
    await page.clock.install({ time: new Date('2026-10-12T00:10:00Z') })
    await page.setViewportSize({ width: 1870, height: 1000 })
    await page.goto('/containers')
    await expect(rowFor(page, 'suma')).toContainText('296 MB')
    await page.clock.runFor(5000)
    await rowFor(page, 'suma').getByRole('button', { name: `${language === 'zh-CN' ? '容器概览' : 'Container overview'} suma` }).click()
    const expansion = page.locator(`#container-expansion-${containers[0].id}`)
    await expect(expansion.locator('canvas')).toHaveCount(2)
    await page.clock.pauseAt(await page.evaluate(() => new Date(Date.now() + 1000).toISOString()))
    await page.mouse.move(0, 0); await page.clock.runFor(500)
    const charts = expansion.getByRole('img')
    const opaquePixels = async (index: number) => charts.nth(index).evaluate(element => {
      let pixels = 0
      for (const canvas of element.querySelectorAll('canvas')) {
        const context = canvas.getContext('2d')!
        const rgba = context.getImageData(0, 0, canvas.width, canvas.height).data
        for (let i = 3; i < rgba.length; i += 4) if (rgba[i] >= 220) pixels++
      }
      return pixels
    })
    for (const index of [0, 1]) {
      await page.mouse.move(0, 0); await page.clock.runFor(300)
      const before = await opaquePixels(index)
      expect(before).toBeGreaterThan(100)
      const bounds = await charts.nth(index).boundingBox()
      await page.mouse.move(bounds!.x + bounds!.width / 2, bounds!.y + bounds!.height / 2)
      await page.clock.runFor(300)
      const during = await opaquePixels(index)
      expect(during).toBeGreaterThanOrEqual(before * 0.9)
      await charts.nth(index).evaluate(element => {
        const host = element as HTMLElement
        host.dataset.canvasClears = '0'
        for (const canvas of element.querySelectorAll('canvas')) {
          const context = canvas.getContext('2d')!
          const clear = context.clearRect.bind(context)
          context.clearRect = (x, y, width, height) => {
            host.dataset.canvasClears = String(Number(host.dataset.canvasClears) + 1)
            clear(x, y, width, height)
          }
        }
      })
      const idleSamples = gate()
      state.gates.set('/nodes/local/containers/metrics', idleSamples.promise)
      await page.clock.runFor(1000)
      expect(await opaquePixels(index)).toBeGreaterThanOrEqual(before * 0.9)
      await expect(charts.nth(index)).toHaveAttribute('data-canvas-clears', '0')
      state.gates.delete('/nodes/local/containers/metrics'); idleSamples.resolve()
      await page.clock.runFor(5000)
      expect(await opaquePixels(index)).toBeGreaterThanOrEqual(before * 0.9)
      await page.screenshot({ path: `/tmp/suma-chart-hover-${language}-${theme}-${index}.png`, animations: 'disabled' })
    }
  })
}

test('events merge replay with nanosecond precision and disconnect when leaving the tab', async ({ page }) => {
  await setup(page)
  let closed = false
  const now = Date.now(), id = containers[0].id
  await page.routeWebSocket('**/ws/nodes/local/containers/*/events?*', ws => {
    ws.onClose(() => { closed = true })
    const event = { id: 'one', node_id: 'local', container_id: id, time: new Date(now).toISOString(), time_nano: String(BigInt(now) * 1000000n + 1n), action: 'die', exit_code: 137 }
    ws.send(JSON.stringify({ type: 'events', entries: [event, event] }))
  })
  await page.goto(`/containers/${id}#events`)
  await expect(page.getByText('Process exited · exit 137', { exact: true })).toHaveCount(1)
  await expect(page.getByText('Recent events retained by Docker; the selected time range may be incomplete.')).toBeVisible()
  await page.getByRole('tab', { name: 'Overview', exact: true }).click()
  await expect.poll(() => closed).toBe(true)
})

test('normalized statistics render both charts, allow CPU above 100 and hide unavailable counters', async ({ page }) => {
  await setup(page)
  let closed = false
  let send: (payload: string) => void = () => undefined
  const now = Date.now(), id = containers[0].id
  const sample = { id, available: true, cpu_available: true, memory_available: true, cpu_percent: 200, memory_bytes: 64 * 1024 ** 2, network_rx_bytes: 1000, network_tx_bytes: 2000, pids: 4, block_read_bytes: 0, block_write_bytes: 0, sampled_at: new Date(now).toISOString(), started_at: started, uptime_seconds: 100 }
  await page.routeWebSocket('**/ws/nodes/local/containers/*/stats?format=normalized', ws => {
    send = payload => ws.send(payload)
    ws.onClose(() => { closed = true })
    send(JSON.stringify(sample))
  })
  await page.goto(`/containers/${id}#stats`)
  await expect(page.getByText('200.0%', { exact: true })).toBeVisible()
  await expect(page.getByText('64 MB', { exact: true })).toBeVisible()
  await expect(page.locator('canvas')).toHaveCount(2)
  const charts = page.getByRole('img')
  for (const index of [0, 1]) {
    await expect.poll(async () => (await strokeBounds(charts.nth(index), false)).width).toBeGreaterThan(0.95)
    expect((await strokeBounds(charts.nth(index), false)).height).toBeLessThan(3)
  }
  send(JSON.stringify({ ...sample, cpu_percent: 50, memory_bytes: 128 * 1024 ** 2, sampled_at: new Date(now + 1000).toISOString() }))
  await expect(page.getByText('50.0%', { exact: true })).toBeVisible()
  for (const index of [0, 1]) {
    await expect.poll(async () => (await strokeBounds(charts.nth(index), false)).height).toBeGreaterThan(20)
    expect((await strokeBounds(charts.nth(index), false)).width).toBeGreaterThan(0.95)
  }
  send(JSON.stringify({ ...sample, cpu_available: false, cpu_percent: 0, sampled_at: new Date(now + 2000).toISOString() }))
  await expect(page.getByText('50.0%', { exact: true })).not.toBeVisible()
  await expect(page.getByText('0.0%', { exact: true })).not.toBeVisible()
  await expect.poll(async () => (await strokeBounds(charts.nth(0), false)).width).toBeLessThan(0.6)
  await page.getByRole('tab', { name: 'Events', exact: true }).click()
  await expect.poll(() => closed).toBe(true)
})

test('contradictory snapshots show syncing and suppress old live values until the list catches up', async ({ page }) => {
  const state = await setup(page)
  await page.clock.install({ time: new Date('2026-10-12T00:10:00Z') })
  await page.goto('/containers')
  const row = rowFor(page, 'suma')
  await expect(row).toContainText('19 MB / 1.00 GB')
  await page.clock.runFor(5000)
  const delayed = gate()
  state.gates.set('/nodes/local/containers', delayed.promise)
  state.metadata[0].state = 'exited'
  state.metadata[0].exit_code = 7
  await page.clock.runFor(5000)
  await expect(row).toContainText('Syncing')
  await expect(row).toContainText('CPU —')
  state.rows[0].state = 'exited'
  state.gates.clear(); delayed.resolve()
  await expect(row.getByRole('cell').nth(3)).toContainText('exited')
  await expect(row).not.toContainText('Syncing')
})

test('basic rows render independently of runtime metadata and metrics', async ({ page }) => {
  const state = await setup(page), delayedRuntime = gate(), delayedMetrics = gate()
  state.gates.set('/nodes/local/containers/runtime', delayedRuntime.promise)
  state.gates.set('/nodes/local/containers/metrics', delayedMetrics.promise)
  await page.goto('/containers')
  const row = rowFor(page, 'suma')
  await expect(row).toBeVisible()
  await expect(row).toContainText('Unknown')
  await expect(row).toContainText('Sampling')
  delayedRuntime.resolve(); delayedMetrics.resolve()
  await expect(row).toContainText('Healthy')
  await expect(row).toContainText('19 MB / 1.00 GB')
})

test('health, limits, stopped containers and partial metadata remain distinct', async ({ page }) => {
  const state = await setup(page)
  state.rows[0].created = '2026-10-11T06:20:30Z'
  await page.goto('/containers')
  await expect(page.getByRole('columnheader', { name: 'Status', exact: true })).toBeVisible()
  await expect(page.getByRole('columnheader', { name: 'Image', exact: true })).toBeVisible()
  await expect(page.getByRole('columnheader', { name: 'Time', exact: true })).toBeVisible()
  await expect(rowFor(page, 'suma')).toContainText('Healthy')
  await expect(rowFor(page, 'suma').getByRole('cell').nth(4)).toContainText('Up 34d 0h')
  await expect(rowFor(page, 'suma').getByRole('cell').nth(4)).toContainText('10-11')
  await expect(rowFor(page, 'suma').getByRole('cell').nth(3)).not.toContainText('Up 34d 0h')
  await expect(rowFor(page, 'suma').getByRole('cell').nth(3).locator('[data-slot="badge"]')).toHaveText(['running', 'Healthy'])
  await expect(rowFor(page, 'vaultwarden')).toContainText('No check')
  await expect(rowFor(page, 'vaultwarden')).toContainText('19 MB / ∞')
  await expect(rowFor(page, 'derp-server')).toContainText('Unhealthy')
  await expect(rowFor(page, 'derp-server')).toContainText('Up 34d 0h')
  await expect(rowFor(page, 'derp-server')).not.toContainText('Restarts')
  await expect(rowFor(page, 'traefik')).toContainText('Starting')
  await expect(rowFor(page, 'redis')).toContainText('Unknown')
  await expect(rowFor(page, 'postgres')).toContainText('CPU —')
  await expect(rowFor(page, 'postgres')).toContainText('— / 1.00 GB')
  await expect(rowFor(page, 'postgres').getByRole('button', { name: 'Files', exact: true })).toBeDisabled()
  await rowFor(page, 'suma').getByText('Up 34d 0h', { exact: true }).hover()
  await expect(page.locator('[data-slot="tooltip-content"]')).toBeVisible()
  await expect(page.locator('[data-slot="tooltip-content"]')).toContainText('Started at:')
  await expect(page.locator('[data-slot="tooltip-content"]')).not.toContainText('Up 34 days')
})

test('rates recover after failures and reset on restart or rollback', async ({ page }) => {
  const state = await setup(page)
  await page.clock.install({ time: new Date('2026-10-12T00:10:00Z') })
  await page.goto('/containers')
  const row = rowFor(page, 'suma')
  await expect(row).toContainText('Sampling')
  await page.clock.runFor(5000); await expect(row).toContainText('↓ 2.0 KB/s')
  await expect(row).toContainText('↑ 1.0 KB/s')
  state.failures.add('/nodes/local/containers/metrics')
  await page.clock.runFor(5000); await expect(row).toContainText('Unavailable')
  await expect(row).toContainText('CPU —')
  state.failures.clear()
  await page.clock.runFor(5000); await expect(row).toContainText('Sampling')
  await page.clock.runFor(5000); await expect(row).toContainText('↓ 2.0 KB/s')
  state.start = '2026-10-12T00:01:10Z'
  await page.clock.runFor(5000); await expect(row).toContainText('Sampling')
  await page.clock.runFor(5000); await expect(row).toContainText('↓ 2.0 KB/s')
  state.rollback = true
  await page.clock.runFor(5000); await expect(row).toContainText('Sampling')
  await page.clock.runFor(5000); await expect(row).toContainText('↓ 0 B/s')
})

test('metadata failure clears old values and independently recovers', async ({ page }) => {
  const state = await setup(page)
  await page.clock.install({ time: new Date('2026-10-12T00:10:00Z') })
  await page.goto('/containers'); const row = rowFor(page, 'suma')
  await expect(row).toContainText('Healthy')
  state.failures.add('/nodes/local/containers/runtime')
  await page.clock.runFor(10_000); await expect(row).toContainText('Unknown')
  await expect(row).toContainText('19 MB / —')
  state.failures.clear(); await page.clock.runFor(10_000); await expect(row).toContainText('Healthy')
})

test('a stalled metrics read expires displayed rates and restarts sampling', async ({ page }) => {
  const state = await setup(page), delayed = gate()
  await page.clock.install({ time: new Date('2026-10-12T00:10:00Z') })
  await page.goto('/containers'); const row = rowFor(page, 'suma')
  await expect(row).toContainText('Sampling')
  await page.clock.runFor(5000); await expect(row).toContainText('↓ 2.0 KB/s')
  state.gates.set('/nodes/local/containers/metrics', delayed.promise)
  await page.clock.runFor(16_000); await expect(row).toContainText('Unavailable')
  state.seconds += 16; state.gates.clear(); delayed.resolve()
  await expect(row).toContainText('Sampling')
  await page.clock.runFor(5000); await expect(row).toContainText('↓ 2.0 KB/s')
})

test('leaving the page cancels both metrics and metadata reads', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  const state = await setup(page), delayedMetrics = gate(), delayedRuntime = gate()
  state.gates.set('/nodes/local/containers/metrics', delayedMetrics.promise)
  state.gates.set('/nodes/local/containers/runtime', delayedRuntime.promise)
  await page.goto('/containers'); await expect(rowFor(page, 'suma')).toBeVisible()
  await expect.poll(() => state.calls).toContain('GET /nodes/local/containers/metrics')
  const canceled = ['metrics', 'runtime'].map(suffix => page.waitForEvent('requestfailed', request => request.url().endsWith(`/nodes/local/containers/${suffix}`)))
  await page.getByRole('button', { name: 'Images', exact: true }).click()
  await Promise.all(canceled)
  delayedMetrics.resolve(); delayedRuntime.resolve()
  await expect(page).toHaveURL('/images')
  await expect(page.getByRole('heading', { name: 'Images', exact: true })).toBeVisible()
  expect(errors).toEqual([])
})

test('port summaries group duplicate bindings while the popover keeps every IP', async ({ page }) => {
  await setup(page); await page.goto('/containers')
  const row = rowFor(page, 'suma')
  const ports = row.getByRole('button', { name: 'View full port mappings' })
  await expect(ports.locator('[data-slot="port-summary"]')).toHaveText(['32830→ 8080tcp', '32831→ 8081tcp+1'])
  await ports.click()
  await expect(page.getByText('0.0.0.0:32830→8080/tcp', { exact: true })).toBeVisible()
  await expect(page.getByText('[::]:32830→8080/tcp', { exact: true })).toBeVisible()
  await expect(page.getByText('127.0.0.1:32831→8081/tcp', { exact: true })).toBeVisible()
  await expect(page.getByText('10.0.0.5:32832→8082/udp', { exact: true })).toBeVisible()
  await page.keyboard.press('Escape'); await expect(ports).toBeFocused()
})

test('segmented port summaries fit five-digit mappings and keep protocols distinct', async ({ page }) => {
  const state = await setup(page)
  state.rows[0].ports = [
    { ip: '0.0.0.0', public_port: 65535, private_port: 65535, type: 'tcp' },
    { ip: '::', public_port: 65535, private_port: 65535, type: 'tcp' },
    { ip: '0.0.0.0', public_port: 65535, private_port: 65535, type: 'udp' },
    { private_port: 443, type: 'tcp' },
  ]
  state.rows[5].ports = []
  await page.goto('/containers')
  const ports = rowFor(page, 'suma').getByRole('button', { name: 'View full port mappings' })
  await expect(ports.locator('[data-slot="port-summary"]')).toHaveText(['65535→ 65535tcp', '65535→ 65535udp+1'])
  const layout = await ports.evaluate(button => {
    const bounds = button.getBoundingClientRect(), center = (bounds.left + bounds.right) / 2
    return [...button.children].map(line => {
      const rect = line.getBoundingClientRect()
      return { height: rect.height, centered: Math.abs((rect.left + rect.right) / 2 - center) < 0.5, fits: rect.left >= bounds.left && rect.right <= bounds.right }
    })
  })
  expect(layout).toEqual([{ height: 20, centered: true, fits: true }, { height: 20, centered: true, fits: true }])
  await expect(rowFor(page, 'vaultwarden').getByRole('cell').nth(7)).toHaveText('81tcp')
  await expect(rowFor(page, 'redis').getByRole('cell').nth(7)).toHaveText('—')
  await ports.click()
  await expect(page.getByText('[::]:65535→65535/tcp', { exact: true })).toBeVisible()
  await expect(page.getByText('0.0.0.0:65535→65535/udp', { exact: true })).toBeVisible()
  await expect(page.getByText('443/tcp', { exact: true })).toBeVisible()
})

test('search covers project and all ports, and shortcuts preserve node actions', async ({ page }) => {
  const state = await setup(page); await page.goto('/containers')
  const search = page.getByRole('textbox', { name: 'Filter containers' })
  await search.fill('project-vaultwarden'); await expect(page.locator('tbody tr')).toHaveCount(1)
  await search.fill('32832'); await expect(rowFor(page, 'suma')).toBeVisible()
  await search.fill(''); const row = rowFor(page, 'suma')
  await expect(row.getByRole('link', { name: 'project-suma', exact: true })).toHaveAttribute('href', '/projects/compose/project-suma')
  await row.getByRole('button', { name: 'Restart', exact: true }).click()
  await expect.poll(() => state.calls).toContain(`POST /nodes/local/containers/${containers[0].id}/restart`)
  await row.getByRole('button', { name: 'More actions', exact: true }).click()
  await expect(page.getByRole('menuitem', { name: 'Rename', exact: true })).toBeVisible()
  await page.keyboard.press('Escape')
  await row.getByRole('button', { name: 'Logs', exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`/containers/${containers[0].id}#logs$`))
})

test('pagination resets after filtering and selecting a different page size', async ({ page }) => {
  const state = await setup(page)
  state.rows = Array.from({ length: 26 }, (_, index) => ({ ...containers[0], id: index.toString(16).padStart(64, '0'), name: `app-${index}` }))
  await page.goto('/containers'); await expect(page.locator('tbody tr')).toHaveCount(20)
  await page.getByRole('button', { name: 'Next page', exact: true }).click(); await expect(page.locator('tbody tr')).toHaveCount(6)
  await page.getByRole('textbox', { name: 'Filter containers' }).fill('app-25'); await expect(page.locator('tbody tr')).toHaveCount(1)
  await page.getByRole('textbox', { name: 'Filter containers' }).fill('')
  await page.getByRole('combobox', { name: 'Items per page' }).click(); await page.getByRole('option', { name: '10', exact: true }).click()
  await expect(page.locator('tbody tr')).toHaveCount(10)
})

test('node switch cancels pending metadata and resets network samples', async ({ page }) => {
  const state = await setup(page), delayed = gate()
  state.gates.set('/nodes/local/containers/runtime', delayed.promise)
  await page.goto('/containers'); await expect(rowFor(page, 'suma')).toBeVisible()
  const canceled = page.waitForEvent('requestfailed', request => request.url().endsWith('/nodes/local/containers/runtime'))
  await page.getByRole('combobox', { name: 'Current Docker node' }).click(); await page.getByRole('option', { name: 'edge', exact: false }).click()
  await canceled; delayed.resolve()
  await expect(rowFor(page, 'edge-suma')).toBeVisible()
  await expect(rowFor(page, 'edge-suma')).toContainText('Sampling')
  await expect(rowFor(page, 'suma')).toHaveCount(0)
})

for (const width of [390, 1440, 1870]) for (const language of ['zh-CN', 'en-US']) for (const theme of ['dark', 'light']) {
  test(`dense rows ${language} ${theme} ${width}`, async ({ page }) => {
    await setup(page, language, theme)
    await page.clock.install({ time: new Date('2026-10-12T00:10:00Z') })
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/containers'); const row = rowFor(page, 'suma')
    await expect(row).toContainText(language === 'zh-CN' ? '健康' : 'Healthy')
    await page.clock.runFor(5000); await expect(row).toContainText('↓ 2.0 KB/s')
    const height = await row.evaluate(element => element.getBoundingClientRect().height)
    expect(height).toBeGreaterThanOrEqual(52); expect(height).toBeLessThanOrEqual(58)
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width)
    const scroll = page.locator('[data-slot="table-container"]')
    const dimensions = await scroll.evaluate(element => ({ visible: element.clientWidth, total: element.scrollWidth }))
    if (width === 1870) expect(dimensions.total).toBe(dimensions.visible)
    else expect(dimensions.total).toBeGreaterThan(dimensions.visible)
    await scroll.evaluate(element => { element.scrollLeft = element.scrollWidth })
    await expect(row.getByRole('button', { name: language === 'zh-CN' ? '更多操作' : 'More actions', exact: true })).toBeInViewport()
    await row.getByRole('button', { name: language === 'zh-CN' ? '更多操作' : 'More actions', exact: true }).click()
    await expect(page.getByRole('menuitem', { name: language === 'zh-CN' ? '重命名' : 'Rename', exact: true })).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(page.getByRole('menu')).toBeHidden()
    await scroll.evaluate(element => { element.scrollLeft = 0 })
    await page.mouse.move(0, 0)
    await page.clock.runFor(500)
    await expect(page.getByRole('tooltip')).toBeHidden()
    await page.screenshot({ path: `/tmp/suma-containers-${language}-${theme}-${width}.png` })
    await row.getByRole('button', { name: `${language === 'zh-CN' ? '容器概览' : 'Container overview'} suma` }).click()
    const overview = width === 390 ? page.locator('[data-slot="sheet-content"]') : page.locator(`#container-expansion-${containers[0].id}`)
    await expect(overview).toContainText('172.20.0.2')
    await expect(overview).toContainText('app_data')
    await expect(overview.locator('canvas')).toHaveCount(2)
    await page.clock.runFor(500)
    await expect(overview).toBeVisible()
    if (width === 390) await expect(overview).toHaveCSS('opacity', '1')
    const fieldOverflow = await overview.locator('section button').evaluateAll(fields => fields.some(field => field.scrollWidth > field.clientWidth + 1))
    expect(fieldOverflow).toBe(false)
    const summaryHeight = await overview.evaluate(element => element.getBoundingClientRect().height)
    expect(summaryHeight).toBeLessThan(width === 390 ? 600 : 260)
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width)
    await page.screenshot({ path: `/tmp/suma-container-overview-${language}-${theme}-${width}.png`, animations: 'disabled' })
  })
}
