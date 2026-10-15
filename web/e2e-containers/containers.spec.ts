import { createMonitoringMock } from '../src/features/monitoring/mock'
import type { Instance, QueryResult } from '../src/features/monitoring/types'
import { expect, test, type Locator, type Page } from '@playwright/test'
import type { ContainerMetrics, ContainerRuntime, ContainerSummary } from '../src/features/containers/types'

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
  networks: [{ name: 'app_default', ip_address: `172.20.0.${index + 2}`, ipv6_address: `fd00::${index + 2}` }],
  cpu_percent: 0, memory_bytes: 0, uptime_seconds: 0,
}))
const runtime: ContainerRuntime[] = containers.map((row, index) => ({ id: row.id, health_status: ['healthy', 'none', 'unhealthy', 'starting', 'none', null][index], restart_count: index === 5 ? null : index === 2 ? 3 : 0, memory_limit_bytes: index === 5 ? null : index % 2 ? 0 : 1024 ** 3 }))
const nodes = ['local', 'edge'].map(id => ({ id, name: id, engine_id: 'engine-'+id, connection_type: 'unix', endpoint: 'unix:///var/run/docker.sock', tls_mode: 'disabled', enabled: true, status: 'online', last_latency_ms: 12, group_ids: [] }))
const gate = () => { let resolve!: () => void; const promise = new Promise<void>(done => { resolve = done }); return { promise, resolve } }

const browserErrors=new WeakMap<Page,string[]>()
test.afterEach(async({page})=>{expect(browserErrors.get(page)??[]).toEqual([])})

async function setup(page: Page, language = 'en-US', theme = 'dark') {
  const state = { calls: [] as string[], gates: new Map<string, Promise<void>>(), failures: new Set<string>(), rows: structuredClone(containers), metadata: structuredClone(runtime), seconds: 0, start: started, rollback: false, cpu: 0.2, memory: 19 * 1024 ** 2 }
  const errors:string[]=[];browserErrors.set(page,errors);page.on('pageerror',error=>errors.push(error.message))
  // Match the controlled browser clock so fixed samples do not age out in Node.
  const monitoringNow = () => Date.parse(started) + 10 * 60_000
  const monitoring = createMonitoringMock(() => nodes.flatMap((node, index) => state.rows.map(row => ({ id: String(index+1).padStart(4,'0')+row.id.slice(4), node_id: node.id, node_name: node.name, engine_id: node.engine_id, container_id: row.id, name: row.name, image: row.image, project: row.labels['com.docker.compose.project'] ?? '', service: row.labels['com.docker.compose.service'] ?? '', first_seen: new Date(monitoringNow()-3*86400_000).toISOString(), last_seen: new Date(monitoringNow()).toISOString() } satisfies Instance))), monitoringNow)
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
    else if (/^\/nodes\/[^/]+$/.test(path)) data=nodes.find(n=>n.id===path.split('/').at(-1))
    else if (path.startsWith('/monitoring')) data=monitoring(path,route.request().method(),JSON.parse(route.request().postData()??'{}'),new URL(route.request().url()))
    else if (path === '/node-groups') data = []
    else if (path === '/notifications/inbox') data = { unread: 0, events: [] }
    else if (path.endsWith('/image-updates')) data = { results: [] }
    else if (path.endsWith('/containers/runtime')) data = state.metadata
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

async function hoverPlotEnd(page: Page, chart: Locator) {
  const point = await chart.evaluate(async element => {
    const resource = performance.getEntriesByType('resource').find(entry => new URL(entry.name).pathname.endsWith('/echarts_core.js'))
    if (!resource) throw new Error('Monitoring chart engine was not loaded')
    const core = await import(resource.name)
    const instance = core.getInstanceByDom(element)
    const rect = instance.getModel().getComponent('grid').coordinateSystem.getRect()
    const x = rect.x + rect.width - 10, y = rect.y + rect.height / 2
    if (!instance.containPixel({ gridIndex: 0 }, [x, y])) throw new Error('Hover point is outside the plotted data')
    return { x, y }
  })
  const bounds = await chart.boundingBox()
  await page.mouse.move(bounds!.x + point.x, bounds!.y + point.y)
}

async function strokeBounds(chart: Locator) {
  return chart.evaluate(element => {
    const host = element as HTMLElement
    let minX = Infinity, maxX = -Infinity, minY = Infinity, maxY = -Infinity, pixels = 0
    const left = 46, right = 12
    const top = 26, bottom = 24
    const cpu = element.getAttribute('aria-label')?.startsWith('CPU')
    const token = cpu ? '--chart-1' : '--chart-2'
    const swatch = document.createElement('canvas'); swatch.width = swatch.height = 1
    const context = swatch.getContext('2d')!
    context.fillStyle = getComputedStyle(document.documentElement).getPropertyValue(token).trim()
    context.fillRect(0, 0, 1, 1)
    const color = context.getImageData(0, 0, 1, 1).data
    for (const canvas of element.querySelectorAll('canvas')) {
      const scale = canvas.width / host.clientWidth
      const rgba = canvas.getContext('2d')!.getImageData(0, 0, canvas.width, canvas.height).data
      for (let y = Math.ceil(top * scale); y <= (host.clientHeight - bottom) * scale; y++) {
        for (let x = Math.ceil(left * scale); x <= (host.clientWidth - right) * scale; x++) {
          const offset = (y * canvas.width + x) * 4
          // Anti-aliasing blends a stroke at the axis maximum with its grid line.
          if (rgba[offset + 3] < 128 || color.slice(0, 3).some((value, channel) => Math.abs(value - rgba[offset + channel]) > 8)) continue
          pixels++
          minX = Math.min(minX, x / scale); maxX = Math.max(maxX, x / scale)
          minY = Math.min(minY, y / scale); maxY = Math.max(maxY, y / scale)
        }
      }
    }
    return { width: (maxX - minX) / (host.clientWidth - left - right), height: maxY - minY, pixels }
  })
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

test('network summaries use list data and show all addresses without extra reads', async ({ page }) => {
  const state = await setup(page)
  state.rows[0].networks!.push({ name: 'private', ipv6_address: 'fd01::2' })
  await page.goto('/containers')
  const row = rowFor(page, 'suma'), network = row.getByRole('cell').nth(5)
  await expect(page.locator('tbody tr')).toHaveCount(containers.length)
  await expect(row.getByRole('cell')).toHaveCount(10)
  await expect(row.getByRole('cell').first().getByRole('button')).toHaveCount(0)
  await expect(network).toHaveText('app_default+1172.20.0.2')
  await network.getByText('app_default', { exact: true }).hover()
  const tooltip = page.locator('[data-slot="tooltip-content"]')
  await expect(tooltip).toContainText('172.20.0.2')
  await expect(tooltip).toContainText('fd00::2')
  await expect(tooltip).toContainText('private')
  await expect(tooltip).toContainText('fd01::2')
  expect(state.calls.some(call => call.endsWith('/overview'))).toBe(false)
  expect(state.calls.filter(call => /GET .*containers\/[^/]+$/.test(call) && !call.endsWith('/runtime') && !call.endsWith('/metrics'))).toHaveLength(0)
  const search = page.getByRole('textbox', { name: 'Filter containers' })
  for (const text of ['private', '172.20.0.2', 'fd01::2']) {
    await search.fill(text)
    await expect(page.locator('tbody tr')).toHaveCount(1)
    await expect(row).toBeVisible()
  }
})

test('missing, empty, host, stopped and IPv6-only networks remain distinct and refresh with the list', async ({ page }) => {
  const state = await setup(page)
  state.rows[0].networks = null
  state.rows[1].networks = []
  state.rows[2].networks = [{ name: 'host' }]
  state.rows[3].networks = [{ name: 'private-v6', ipv6_address: 'fd01::4' }]
  state.rows[4].networks = [{ name: 'app_default' }]
  delete state.rows[5].networks
  await page.clock.install({ time: new Date('2026-10-12T00:10:00Z') })
  await page.goto('/containers')
  for (const [name, text] of [['suma', '——'], ['vaultwarden', 'No network—'], ['derp-server', 'host—'], ['traefik', 'private-v6fd01::4'], ['postgres', 'app_default—'], ['redis', '——']]) {
    await expect(rowFor(page, name).getByRole('cell').nth(5)).toHaveText(text)
  }
  state.rows[0].networks = [{ name: 'new_default', ip_address: '10.1.0.2' }]
  await page.clock.runFor(10_000)
  await expect(rowFor(page, 'suma').getByRole('cell').nth(5)).toHaveText('new_default10.1.0.2')
  expect(state.calls.some(call => call.endsWith('/overview'))).toBe(false)
})

test('container detail retains full networks and mounts', async ({ page }) => {
  const state = await setup(page)
  await page.goto(`/containers/${containers[0].id}`)
  await expect(page.getByRole('button', { name: 'Copy app_default', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Copy 172.20.0.2', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Copy fd00::2', exact: true })).toBeVisible()
  await expect(page.getByText('volume app_data', { exact: true })).toBeVisible()
  expect(state.calls).toContain(`GET /nodes/local/containers/${containers[0].id}`)
  expect(state.calls.some(call => call.endsWith('/overview'))).toBe(false)
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

test('resource trends use shared samples and do not open per-row streams', async ({ page }) => {
  await setup(page)
  await page.clock.install({ time: new Date('2026-10-12T00:10:00Z') })
  const sockets: string[] = []
  let chartModuleURL = ''
  // The controlled clock replaces performance entries; capture the module request.
  page.on('request', request => { if (new URL(request.url()).pathname.endsWith('/echarts_core.js')) chartModuleURL = request.url() })
  page.on('websocket', ws => sockets.push(ws.url()))
  await page.goto('/containers')
  await expect(rowFor(page, 'suma')).toContainText('19 MB / 1.00 GB')
  await page.clock.runFor(5000)
  const historyResponse = page.waitForResponse(response => new URL(response.url()).pathname === '/api/v1/monitoring/query')
  await rowFor(page, 'suma').getByRole('button', { name: 'Resource trends suma', exact: true }).click()
  const monitoring = page.getByRole('region',{name:'Container monitoring'})
  await expect(monitoring).toContainText('View full monitoring')
  await expect(monitoring.locator('canvas')).toHaveCount(3)
  const payload = await (await historyResponse).json() as { data: QueryResult }
  for (const point of payload.data.series[0].points) point.values.network_tx = { avg: 10240, min: 10240, max: 10240, count: 1 }
  await page.route('**/api/v1/monitoring/query', route => route.fulfill({ json: payload }))
  await monitoring.getByRole('button', { name: 'Refresh monitoring', exact: true }).click()
  await expect(monitoring).toContainText('10.0 KiB/s')
  await page.clock.runFor(500)
  expect(chartModuleURL).toBeTruthy()
  const network = await monitoring.getByRole('img', { name: 'Network traffic trend', exact: true }).evaluate(async (element, moduleURL) => {
    const core = await import(moduleURL), options = core.getInstanceByDom(element).getOption()
    return { unit: options.yAxis[0].name, labels: options.series.map((line: { name: string }) => options.legend[0].formatter(line.name)), firstValues: options.series.map((line: { data: [number, number][] }) => line.data[0][1]) }
  }, chartModuleURL)
  expect(network.unit).toBe('KiB/s')
  expect(network.labels).toEqual(['Receive', 'Send'])
  expect(network.firstValues[0]).toBeCloseTo(payload.data.series[0].points[0].values.network_rx!.avg! / 1024)
  expect(network.firstValues[1]).toBe(10)
  expect(sockets.filter(url => new URL(url).pathname.startsWith('/ws/nodes/'))).toHaveLength(0)
})

for (const theme of ['dark', 'light']) {
 test('persisted one-hour charts remain after reopening '+theme, async ({page})=>{
  await setup(page,'en-US',theme)
  await page.goto('/containers')
  const trigger=rowFor(page,'suma').getByRole('button',{name:'Resource trends suma',exact:true})
  await trigger.click()
  const monitoring=page.getByRole('region',{name:'Container monitoring'})
  await expect(monitoring.locator('canvas')).toHaveCount(3)
  await expect(monitoring).toContainText('Last hour')
  await trigger.click();await expect(monitoring).toHaveCount(0)
  await trigger.click();await expect(monitoring.locator('canvas')).toHaveCount(3)
  await page.screenshot({path:'/tmp/suma-monitoring-hour-'+theme+'.png'})
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
    await rowFor(page, 'suma').getByRole('button', { name: `${language === 'zh-CN' ? '资源趋势' : 'Resource trends'} suma`, exact: true }).click()
    const popup = page.getByRole('region', { name: /Container monitoring|容器监控/ })
    await expect(popup.locator('canvas')).toHaveCount(3)
    await page.clock.pauseAt(await page.evaluate(() => new Date(Date.now() + 1000).toISOString()))
    await page.mouse.move(0, 0); await page.clock.runFor(500)
    const charts = popup.getByRole('img')
    const seriesPixels = async (index: number) => (await strokeBounds(charts.nth(index))).pixels
    for (const index of [0, 1]) {
      await page.mouse.move(0, 0); await page.clock.runFor(300)
      const before = await seriesPixels(index)
      expect(before).toBeGreaterThan(100)
      const bounds = await charts.nth(index).boundingBox()
      await page.mouse.move(bounds!.x + bounds!.width / 2, bounds!.y + bounds!.height / 2)
      await page.clock.runFor(300)
      const during = await seriesPixels(index)
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
      expect(await seriesPixels(index)).toBeGreaterThanOrEqual(before * 0.9)
      await expect(charts.nth(index)).toHaveAttribute('data-canvas-clears', '0')
      state.gates.delete('/nodes/local/containers/metrics'); idleSamples.resolve()
      await page.clock.runFor(5000)
      expect(await seriesPixels(index)).toBeGreaterThanOrEqual(before * 0.9)
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

test('legacy stats links open persisted monitoring without a dedicated stats stream', async ({page})=>{
 const state=await setup(page)
 const sockets:string[]=[];page.on('websocket',ws=>sockets.push(ws.url()))
 await page.goto('/containers/'+containers[0].id+'#stats')
 await expect(page).toHaveURL(/#monitoring$/)
 await expect(page.getByRole('region',{name:'Container monitoring'}).locator('canvas')).toHaveCount(5)
 expect(state.calls.some(call=>call==='POST /monitoring/query')).toBe(true)
 expect(sockets.filter(url=>url.includes('/stats'))).toHaveLength(0)
 await page.getByRole('tab',{name:'Events',exact:true}).click()
 await expect(page.getByRole('region',{name:'Container monitoring'})).toHaveCount(0)
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
  await expect(row.getByRole('cell').nth(2)).toContainText('exited')
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
  await expect(rowFor(page, 'suma').getByRole('cell').nth(8)).toContainText('Up 34d 0h')
  await expect(rowFor(page, 'suma').getByRole('cell').nth(8)).toContainText('10-11')
  await expect(rowFor(page, 'suma').getByRole('cell').nth(2)).not.toContainText('Up 34d 0h')
  await expect(rowFor(page, 'suma').getByRole('cell').nth(2).locator('[data-slot="badge"]')).toHaveText(['running', 'Healthy'])
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
  await expect(ports.locator('[data-slot="port-summary"]')).toHaveText(['32830:8080', '32831:8081+1'])
  await ports.click()
  await expect(page.getByText('0.0.0.0:32830:8080/tcp', { exact: true })).toBeVisible()
  await expect(page.getByText('[::]:32830:8080/tcp', { exact: true })).toBeVisible()
  await expect(page.getByText('127.0.0.1:32831:8081/tcp', { exact: true })).toBeVisible()
  await expect(page.getByText('10.0.0.5:32832:8082/udp', { exact: true })).toBeVisible()
  await page.keyboard.press('Escape'); await expect(ports).toBeFocused()
})

test('port tags fit five-digit mappings and keep protocols distinct', async ({ page }) => {
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
  await expect(ports.locator('[data-slot="port-summary"]')).toHaveText(['65535:65535', '65535:65535/udp+1'])
  const layout = await ports.evaluate(button => {
    const bounds = button.getBoundingClientRect(), center = (bounds.left + bounds.right) / 2
    return [...button.children].map(line => {
      const rect = line.getBoundingClientRect()
      return { height: rect.height, centered: Math.abs((rect.left + rect.right) / 2 - center) < 0.5, fits: rect.left >= bounds.left && rect.right <= bounds.right }
    })
  })
  expect(layout).toEqual([{ height: 20, centered: true, fits: true }, { height: 20, centered: true, fits: true }])
  await expect(rowFor(page, 'vaultwarden').getByRole('cell').nth(6)).toHaveText('81')
  await expect(rowFor(page, 'redis').getByRole('cell').nth(6)).toHaveText('—')
  await ports.click()
  await expect(page.getByText('[::]:65535:65535/tcp', { exact: true })).toBeVisible()
  await expect(page.getByText('0.0.0.0:65535:65535/udp', { exact: true })).toBeVisible()
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

  })
}

test('monitoring history queries without live container reads and compares selected instances', async ({ page }) => {
  const state = await setup(page)
  await page.goto('/monitoring-history')
  await expect(page.getByRole('heading', { name: 'Monitoring history', exact: true })).toBeVisible()
  await expect(page.getByRole('link', { name: 'suma', exact: true })).toHaveCount(2)
  expect(state.calls.filter(call => /nodes\/[^/]+\/containers/.test(call))).toHaveLength(0)
  await page.getByRole('checkbox', { name: 'Select historical instance suma', exact: true }).first().check()
  await page.getByRole('checkbox', { name: 'Select historical instance vaultwarden', exact: true }).first().check()
  await page.getByRole('button', { name: /Compare selected/ }).click()
  await expect(page).toHaveURL(/\/monitoring-history\/.*compare=/)
  await expect(page.getByRole('combobox', { name: 'Comparison mode' })).toContainText('Compare containers')
  await expect(page.locator('canvas')).toHaveCount(5)
  expect(state.calls.filter(call => /nodes\/[^/]+\/containers/.test(call))).toHaveLength(0)
})

test('monitoring retains charts and scroll while a new range loads', async ({ page }) => {
  const state = await setup(page)
  await page.setViewportSize({ width: 1440, height: 700 })
  await page.goto('/containers/' + containers[0].id + '#monitoring')
  await expect(page.locator('canvas')).toHaveCount(5)
  const scroller = page.locator('main > div')
  await scroller.evaluate(element => { element.scrollTop = 400 })
  const before = await scroller.evaluate(element => element.scrollTop)
  expect(before).toBeGreaterThan(100)
  const delayed = gate()
  const previousCalls = state.calls.filter(call => call === 'POST /monitoring/query').length
  state.gates.set('/monitoring/query', delayed.promise)
  await page.getByRole('button', { name: '6h', exact: true }).evaluate(button => (button as HTMLButtonElement).click())
  await expect.poll(() => state.calls.filter(call => call === 'POST /monitoring/query').length).toBeGreaterThan(previousCalls)
  await expect(page.locator('canvas')).toHaveCount(5)
  await expect(page.getByRole('button', { name: 'CSV', exact: true })).toBeDisabled()
  expect(await scroller.evaluate(element => element.scrollTop)).toBeCloseTo(before, 0)
  delayed.resolve()
  await expect(page.getByRole('button', { name: 'CSV', exact: true })).toBeEnabled()
  expect(await scroller.evaluate(element => element.scrollTop)).toBeCloseTo(before, 0)
})

test('monitoring automatic and manual refresh preserve reading position', async ({ page }) => {
  const state = await setup(page)
  await page.clock.install()
  await page.setViewportSize({ width: 1440, height: 700 })
  await page.goto('/containers/' + containers[0].id + '#monitoring')
  const monitoring = page.getByRole('region', { name: 'Container monitoring' })
  await expect(monitoring.locator('canvas')).toHaveCount(5)
  const canvas = await monitoring.locator('canvas').first().elementHandle()
  const scroller = page.locator('main > div')
  await scroller.evaluate(element => { element.scrollTop = 400 })
  const before = await scroller.evaluate(element => element.scrollTop)
  expect(before).toBeGreaterThan(100)
  const calls = () => state.calls.filter(call => call === 'POST /monitoring/query').length
  const initialCalls = calls()
  await page.clock.runFor(11000)
  await expect.poll(calls).toBeGreaterThan(initialCalls)
  expect(state.calls.filter(call => call === 'GET /nodes/local/containers/' + containers[0].id).length).toBeGreaterThan(1)
  expect(await scroller.evaluate(element => element.scrollTop)).toBeCloseTo(before, 0)
  expect(await canvas!.evaluate(element => element.isConnected)).toBe(true)
  await expect(page.getByText('Following latest · refresh every 5s', { exact: true })).toBeVisible()
  const beforeManual = calls()
  await page.getByRole('button', { name: 'Refresh monitoring', exact: true }).evaluate(button => (button as HTMLButtonElement).click())
  await expect.poll(calls).toBeGreaterThan(beforeManual)
  await expect(monitoring).toHaveAttribute('aria-busy', 'false')
  expect(await scroller.evaluate(element => element.scrollTop)).toBeCloseTo(before, 0)
  expect(await canvas!.evaluate(element => element.isConnected)).toBe(true)
})

for (const [language, theme, width] of [['zh-CN', 'light', 1440], ['en-US', 'dark', 390]] as const) {
  test('monitoring organized controls and chart columns ' + width, async ({ page }) => {
    await setup(page, language, theme)
    await page.setViewportSize({ width, height: 1000 })
    await page.goto('/containers/' + containers[0].id + '#monitoring')
    const region = page.getByRole('region', { name: language === 'zh-CN' ? '容器监控' : 'Container monitoring' })
    await expect(region.locator('canvas')).toHaveCount(5)
    await expect(region.getByRole('group', { name: language === 'zh-CN' ? /^时间范围/ : /^Time range/ })).toContainText('Asia/Shanghai')
    await expect(region.getByRole('group', { name: language === 'zh-CN' ? '对比方式' : 'Comparison', exact: true }).getByRole('combobox')).toHaveCount(1)
    const summary = region.getByRole('region', { name: language === 'zh-CN' ? '范围统计' : 'Range statistics' })
    const tables = summary.getByRole('table')
    await expect(tables).toHaveCount(2)
    let rowHeight: number | undefined
    for (const table of await tables.all()) {
      await expect(table.getByRole('columnheader')).toHaveText(language === 'zh-CN' ? ['指标', '平均值', '峰值'] : ['Metric', 'Average', 'Peak'])
      const rows = table.getByRole('row').filter({ has: page.getByRole('rowheader') })
      const reference = await rows.first().getByRole('cell').first().boundingBox()
      for (const row of await rows.all()) {
        await expect(row.getByRole('cell')).toHaveCount(2)
        const rowBox = await row.boundingBox()
        rowHeight ??= rowBox!.height
        expect(Math.abs(rowBox!.height - rowHeight)).toBeLessThanOrEqual(1)
        const cell = await row.getByRole('cell').first().boundingBox()
        expect(cell!.x + cell!.width).toBeCloseTo(reference!.x + reference!.width, 0)
      }
    }
    const charts = region.getByRole('img')
    const boxes = await Promise.all((await charts.all()).map(chart => chart.boundingBox()))
    if (width === 1440) {
      expect(boxes[0]!.y).toBeCloseTo(boxes[1]!.y, 0)
      expect(boxes[2]!.y).toBeCloseTo(boxes[3]!.y, 0)
      expect(boxes[1]!.x).toBeGreaterThan(boxes[0]!.x + boxes[0]!.width)
      expect(boxes[2]!.y).toBeGreaterThan(boxes[0]!.y + boxes[0]!.height)
      expect(boxes[4]!.width).toBeCloseTo(boxes[0]!.width, 0)
    } else {
      for (let i = 1; i < boxes.length; i++) expect(boxes[i]!.y).toBeGreaterThan(boxes[i - 1]!.y + boxes[i - 1]!.height)
    }
    expect(await region.evaluate(element => element.scrollWidth <= element.clientWidth)).toBe(true)
    await page.screenshot({ path: '/tmp/suma-monitoring-organized-' + width + '.png', animations: 'disabled' })
    await charts.nth(2).scrollIntoViewIfNeeded()
    await page.screenshot({ path: '/tmp/suma-monitoring-organized-charts-' + width + '.png', animations: 'disabled' })
  })
}

test('monitoring custom ranges freeze following and CSV includes queried data', async ({ page }) => {
  await setup(page)
  await page.goto('/containers/' + containers[0].id + '#monitoring')
  await expect(page.locator('canvas')).toHaveCount(5)
  await page.getByRole('button', { name: '6h', exact: true }).click()
  await expect(page.getByRole('region', { name: 'Container monitoring' })).toContainText('Resolution')
  await page.getByRole('button', { name: 'Apply range', exact: true }).click()
  await expect(page.getByText('Fixed range', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Back to latest' })).toBeVisible()
  const download = page.waitForEvent('download')
  await page.getByRole('button', { name: 'CSV', exact: true }).click()
  const file = await download
  expect(file.suggestedFilename()).toBe('container-monitoring.csv')
  await page.getByRole('button', { name: 'Back to latest' }).click()
  await expect(page.getByText('Following latest · refresh every 5s', { exact: true })).toBeVisible()
})

test('monitoring period comparisons retain actual timestamps and surface expired history', async ({ page }) => {
  await setup(page)
  await page.goto('/containers/' + containers[0].id + '#monitoring')
  await page.getByRole('combobox', { name: 'Comparison mode' }).click()
  await page.getByRole('option', { name: 'Compare periods', exact: true }).click()
  await expect(page.getByRole('combobox', { name: 'Comparison period' })).toContainText('Previous period')
  await page.getByRole('combobox', { name: 'Comparison period' }).click()
  await page.getByRole('option', { name: 'Yesterday', exact: true }).click()
  await page.getByRole('button', { name: '3d', exact: true }).click()
  await expect(page.getByRole('region', { name: 'Container monitoring' })).toContainText('History partly or fully expired')
})

test('monitoring retention reduction requires confirmation and sends a versioned policy', async ({ page }) => {
  const state = await setup(page)
  await page.goto('/settings#monitoring')
  await expect(page.getByRole('button', { name: 'Save monitoring settings' })).toBeVisible()
  await page.getByRole('combobox', { name: 'Retention', exact: true }).click()
  await page.getByRole('option', { name: '1 days', exact: true }).click()
  await page.getByRole('button', { name: 'Save monitoring settings' }).click()
  await expect(page.getByRole('dialog')).toContainText('Reduce monitoring history retention')
  await page.getByRole('button', { name: 'Cancel', exact: true }).click()
  expect(state.calls.filter(call => call === 'PUT /monitoring/settings')).toHaveLength(0)
  await page.getByRole('button', { name: 'Save monitoring settings' }).click()
  const request = page.waitForRequest(r => r.method() === 'PUT' && r.url().endsWith('/monitoring/settings'))
  await page.getByRole('button', { name: 'Save and clean history', exact: true }).click()
  const input = (await request).postDataJSON()
  expect(input).toMatchObject({ retention_days: 1, revision: 1, confirm_retention_reduction: true })
  await expect(page.getByText('Settings saved', { exact: true })).toBeVisible()
})

test('monitoring settings persist Compose service exclusions', async ({ page }) => {
  await setup(page)
  await page.goto('/settings#monitoring')
  await page.getByRole('combobox', { name: 'Exclusion kind' }).click()
  await page.getByRole('option', { name: 'Compose project/service', exact: true }).click()
  await page.getByRole('combobox', { name: 'Excluded node' }).click()
  await page.getByRole('option', { name: 'local', exact: true }).click()
  await page.getByRole('textbox', { name: 'Excluded Compose project' }).fill('project')
  await page.getByRole('textbox', { name: 'Excluded Compose service' }).fill('web')
  await page.getByRole('button', { name: 'Add rule' }).click()
  await page.getByRole('button', { name: 'Save monitoring settings' }).click()
  await expect(page.getByText('Settings saved', { exact: true })).toBeVisible()
  await page.reload()
  await expect(page.getByText('local · project / web', { exact: true })).toBeVisible()
})

for (const language of ['zh-CN', 'en-US']) for (const theme of ['dark', 'light']) for (const width of [390, 1440, 2112]) {
  test('monitoring layout ' + language + ' ' + theme + ' ' + width, async ({ page }) => {
    await setup(page, language, theme)
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/containers')
    const trend = rowFor(page, 'suma').getByRole('button', { name: (language === 'zh-CN' ? '资源趋势' : 'Resource trends') + ' suma', exact: true })
    await trend.click()
    const region = page.getByRole('region', { name: language === 'zh-CN' ? '容器监控' : 'Container monitoring' })
    await expect(region.locator('canvas')).toHaveCount(3)
    if (width === 390) await expect(page.getByRole('dialog')).toBeVisible()
    const charts = region.getByRole('img')
    const boxes = await Promise.all((await charts.all()).map(chart => chart.boundingBox()))
    expect(boxes).toHaveLength(3)
    for (let index = 1; index < boxes.length; index++) {
      if (width === 390) {
        expect(boxes[index]!.x).toBeCloseTo(boxes[0]!.x, 0)
        expect(boxes[index]!.y).toBeGreaterThan(boxes[index - 1]!.y + boxes[index - 1]!.height)
      } else {
        expect(boxes[index]!.y).toBeCloseTo(boxes[0]!.y, 0)
        expect(boxes[index]!.x).toBeGreaterThan(boxes[index - 1]!.x + boxes[index - 1]!.width)
      }
    }
    const memoryChart = charts.nth(1)
    await memoryChart.scrollIntoViewIfNeeded()
    await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
    const bounds = await memoryChart.boundingBox()
    expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(width)
    const ticks = await memoryChart.evaluate(async element => {
      const resource = performance.getEntriesByType('resource').find(entry => new URL(entry.name).pathname.endsWith('/echarts_core.js'))
      if (!resource) throw new Error('Monitoring chart engine was not loaded')
      const core = await import(resource.name)
      const chart = core.getInstanceByDom(element)
      const labels: { y: number; height: number }[] = []
      for (const item of chart.getZr().storage.getDisplayList()) {
        if (item.type !== 'tspan' || !/^\d+(\.\d+)?$/.test(item.style.text) || item.ignore || item.invisible) continue
        const rect = item.getBoundingRect().clone()
        rect.applyTransform(item.transform)
        if (rect.x >= 0 && rect.x + rect.width < 55) labels.push({ y: rect.y, height: rect.height })
      }
      return labels.sort((a, b) => a.y - b.y)
    })
    expect(ticks.length).toBeGreaterThanOrEqual(2)
    expect(ticks.length).toBeLessThanOrEqual(4)
    for (let index = 1; index < ticks.length; index++) expect(ticks[index].y).toBeGreaterThanOrEqual(ticks[index - 1].y + ticks[index - 1].height)
    await hoverPlotEnd(page, memoryChart)
    const tooltips = page.locator('[data-slot="chart-tooltip"]:visible')
    await expect(tooltips).toHaveCount(3)
    for (const [index, tooltip] of (await tooltips.all()).entries()) {
      const area = await charts.nth(index).boundingBox()
      const wrapping = await tooltip.evaluate(element => {
        const item = element.firstElementChild!, header = item.firstElementChild!, time = item.querySelector('time')!
        const tokens = [...item.querySelectorAll('[data-slot="chart-tooltip-value"], time > span')]
        return {
          timestampOnNewLine: time.getBoundingClientRect().top >= header.getBoundingClientRect().bottom,
          wholeTokens: tokens.every(token => { const range = document.createRange(); range.selectNodeContents(token); return range.getClientRects().length === 1 }),
          noOverflow: element.scrollWidth <= element.clientWidth && element.scrollHeight <= element.clientHeight,
        }
      })
      expect(wrapping).toEqual({ timestampOnNewLine: true, wholeTokens: true, noOverflow: true })
      await expect.poll(async () => {
        const box = await tooltip.boundingBox()
        return !!box && box.x >= area!.x + 55 && box.x + box.width <= area!.x + area!.width - 15 && box.y >= area!.y + 10 && box.y + box.height <= area!.y + area!.height - 25
      }).toBe(true)
    }
    if (width > 390) {
      const scroller = page.locator('[data-slot="table-container"]')
      await scroller.evaluate(element => { element.scrollLeft = element.scrollWidth })
      const visible = await scroller.boundingBox()
      const plot = await memoryChart.boundingBox()
      expect(plot!.x).toBeGreaterThanOrEqual(visible!.x)
      expect(plot!.x + plot!.width).toBeLessThanOrEqual(visible!.x + visible!.width)
    }
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width)
    await page.screenshot({ path: '/tmp/suma-monitoring-layout-' + language + '-' + theme + '-' + width + '.png', animations: 'disabled' })
    await page.goto('/containers/' + containers[0].id + '#monitoring')
    await expect(page.locator('canvas')).toHaveCount(5)
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width)
    await page.screenshot({ path: '/tmp/suma-monitoring-detail-' + language + '-' + theme + '-' + width + '.png', animations: 'disabled' })
    await page.goto('/settings#monitoring')
    await expect(page.getByRole('button', { name: language === 'zh-CN' ? '保存监控设置' : 'Save monitoring settings' })).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width)
    await page.screenshot({ path: '/tmp/suma-monitoring-settings-' + language + '-' + theme + '-' + width + '.png', animations: 'disabled' })
  })
}

for (const width of [390, 1440]) {
  test('monitoring tooltips wrap long names and keep comparison content accessible ' + width, async ({ page }) => {
    const state = await setup(page)
    const longName = 'postgres-container-for-resource-monitoring-'.repeat(3)
    state.rows[0].name = longName
    state.rows[1].name = longName + 'other'
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/containers')
    await rowFor(page, longName).getByRole('button', { name: 'Resource trends ' + longName, exact: true }).click()
    const chart = page.getByRole('region', { name: 'Container monitoring' }).getByRole('img', { name: 'Memory trend', exact: true })
    await expect(chart.locator('canvas')).toHaveCount(1)
    await chart.scrollIntoViewIfNeeded()
    await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
    const area = await chart.boundingBox()
    await hoverPlotEnd(page, chart)
    const tooltip = page.locator('[data-slot="chart-tooltip"]:visible').filter({ hasText: 'Memory:' })
    await expect(tooltip).toContainText(longName)
    expect(await tooltip.evaluate(element => element.scrollWidth <= element.clientWidth)).toBe(true)
    await tooltip.hover()
    await tooltip.evaluate(element => { element.scrollTop = element.scrollHeight })
    expect(await tooltip.evaluate(element => element.scrollTop)).toBeGreaterThan(0)
    const box = await tooltip.boundingBox()
    expect(box!.x).toBeGreaterThanOrEqual(area!.x + 55)
    expect(box!.x + box!.width).toBeLessThanOrEqual(area!.x + area!.width - 15)
    expect(box!.y + box!.height).toBeLessThanOrEqual(area!.y + area!.height - 25)
    await page.screenshot({ path: '/tmp/suma-monitoring-tooltip-long-name-' + width + '.png', animations: 'disabled' })
    if (width === 1440) {
      await page.goto('/monitoring-history')
      const selected = page.getByRole('checkbox', { name: /Select historical instance/ })
      for (let index = 0; index < 4; index++) await selected.nth(index).check()
      await page.getByRole('button', { name: /Compare selected/ }).click()
      const comparison = page.getByRole('region', { name: 'Container monitoring' }).getByRole('img', { name: 'CPU trend', exact: true })
      await expect(comparison.locator('canvas')).toHaveCount(1)
      await comparison.scrollIntoViewIfNeeded()
      await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
      const plot = await comparison.boundingBox()
      await hoverPlotEnd(page, comparison)
      const popup = page.locator('[data-slot="chart-tooltip"]:visible').filter({ hasText: 'CPU:' })
      await expect(popup).toContainText('other')
      expect(await popup.locator(':scope > div').count()).toBe(8)
      expect(await popup.evaluate(element => element.scrollWidth <= element.clientWidth && element.scrollHeight > element.clientHeight)).toBe(true)
      await popup.hover()
      await popup.evaluate(element => { element.scrollTop = element.scrollHeight })
      expect(await popup.evaluate(element => element.scrollTop)).toBeGreaterThan(0)
      const popupBounds = await popup.boundingBox()
      expect(popupBounds!.x + popupBounds!.width).toBeLessThanOrEqual(plot!.x + plot!.width - 15)
      expect(popupBounds!.y).toBeGreaterThanOrEqual(plot!.y + 35)
      expect(popupBounds!.y + popupBounds!.height).toBeLessThanOrEqual(plot!.y + plot!.height - 49)
      await page.screenshot({ path: '/tmp/suma-monitoring-tooltip-comparison.png', animations: 'disabled' })
    }
  })
}

for (const theme of ['light', 'dark']) {
  test('monitoring hover preserves outer scroll size and renders distinct colors ' + theme, async ({ page }) => {
    await setup(page, 'en-US', theme)
    await page.setViewportSize({ width: 1440, height: 700 })
    await page.goto('/containers/' + containers[0].id + '#monitoring')
    const region = page.getByRole('region', { name: 'Container monitoring' })
    await expect(region.locator('canvas')).toHaveCount(5)
    const chart = region.getByRole('img', { name: 'CPU trend', exact: true })
    await chart.scrollIntoViewIfNeeded()
    await chart.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
    const size = () => page.evaluate(() => ({ width: document.documentElement.scrollWidth, height: document.documentElement.scrollHeight, bodyHeight: document.body.scrollHeight, panelHeight: document.querySelector('main > div')!.scrollHeight }))
    const before = await size()
    await hoverPlotEnd(page, chart)
    await expect(page.locator('[data-slot="chart-tooltip"]:visible')).toHaveCount(5)
    expect(await size()).toEqual(before)
    const colors = await region.getByRole('img').evaluateAll(elements => elements.map(element => {
      const host = element as HTMLElement
      const hits = new Set<number>()
      const swatch = document.createElement('canvas'); swatch.width = swatch.height = 1
      const context = swatch.getContext('2d')!
      const palette = [1, 2, 3, 4, 5].map(index => {
        context.clearRect(0, 0, 1, 1)
        context.fillStyle = getComputedStyle(document.documentElement).getPropertyValue('--chart-' + index).trim()
        context.fillRect(0, 0, 1, 1)
        return [...context.getImageData(0, 0, 1, 1).data].slice(0, 3)
      })
      for (const canvas of element.querySelectorAll('canvas')) {
        const scale = canvas.width / host.clientWidth
        const pixels = canvas.getContext('2d')!.getImageData(0, 0, canvas.width, canvas.height).data
        for (let y = Math.ceil(35 * scale); y < (host.clientHeight - 50) * scale; y++) for (let x = Math.ceil(55 * scale); x < (host.clientWidth - 16) * scale; x++) {
          const offset = (y * canvas.width + x) * 4, rgb = [...pixels.slice(offset, offset + 3)]
          if (pixels[offset + 3] < 180 || Math.max(...rgb) - Math.min(...rgb) < 45) continue
          palette.forEach((color, index) => { if (color.every((value, channel) => Math.abs(value - rgb[channel]) < 12)) hits.add(index) })
        }
      }
      return [...hits]
    }))
    expect(colors[0]).toContain(0)
    expect(colors[1]).toContain(1)
    expect(colors[2]).toEqual(expect.arrayContaining([2, 3]))
    expect(colors[3]).toEqual(expect.arrayContaining([3, 4]))
    expect(colors[4]).toContain(4)
    await page.screenshot({ path: '/tmp/suma-monitoring-color-' + theme + '.png', animations: 'disabled' })
  })
}

test('mouse wheel over monitoring charts scrolls the page without changing the hour range', async ({ page }) => {
  await setup(page)
  const durations: number[] = []
  page.on('request', request => {
    if (request.method() === 'POST' && request.url().endsWith('/monitoring/query')) {
      const targets = request.postDataJSON().targets as { since: string; until: string }[]
      durations.push(...targets.map(target => Date.parse(target.until) - Date.parse(target.since)))
    }
  })
  await page.goto('/containers/' + containers[0].id + '#monitoring')
  const region = page.getByRole('region', { name: 'Container monitoring' })
  const chart = region.getByRole('img', { name: 'CPU trend', exact: true })
  await expect(chart.locator('canvas')).toHaveCount(1)
  await expect(region).toHaveAttribute('aria-busy', 'false')
  await chart.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
  await chart.scrollIntoViewIfNeeded()
  await hoverPlotEnd(page, chart)
  const scroller = page.locator('main > div')
  const before = await scroller.evaluate(element => element.scrollTop)
  await page.mouse.wheel(0, 240)
  await expect.poll(() => scroller.evaluate(element => element.scrollTop)).toBeGreaterThan(before + 100)
  await chart.scrollIntoViewIfNeeded()
  await hoverPlotEnd(page, chart)
  const beforeUp = await scroller.evaluate(element => element.scrollTop)
  await page.mouse.wheel(0, -160)
  await expect.poll(() => scroller.evaluate(element => element.scrollTop)).toBeLessThan(beforeUp)
  expect(durations.length).toBeGreaterThan(0)
  expect(durations.every(duration => duration === 3600_000)).toBe(true)
  await expect(page.getByText('Following latest · refresh every 5s', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Back to latest' })).toHaveCount(0)
})

test('monitoring opens and reopens with a selected one-hour range', async ({ page }) => {
  await setup(page)
  const initialQuery = page.waitForRequest(request => request.method() === 'POST' && request.url().endsWith('/monitoring/query'))
  await page.goto('/containers/' + containers[0].id + '#monitoring')
  const target = (await initialQuery).postDataJSON().targets[0]
  expect(Date.parse(target.until) - Date.parse(target.since)).toBe(3600_000)
  const region = page.getByRole('region', { name: 'Container monitoring' })
  await expect(region.locator('canvas')).toHaveCount(5)
  await expect(page.getByRole('button', { name: '1h', exact: true })).toHaveAttribute('aria-pressed', 'true')
  await page.getByRole('button', { name: '6h', exact: true }).click()
  await expect(page.getByRole('button', { name: '6h', exact: true })).toHaveAttribute('aria-pressed', 'true')
  await expect(page.getByRole('button', { name: 'CSV', exact: true })).toBeEnabled()
  await page.getByRole('tab', { name: 'Overview', exact: true }).click()
  await expect(region).toHaveCount(0)
  await page.getByRole('tab', { name: 'Monitoring', exact: true }).click()
  await expect(region.locator('canvas')).toHaveCount(5)
  await expect(page.getByRole('button', { name: '1h', exact: true })).toHaveAttribute('aria-pressed', 'true')
  const duration = await region.getByRole('img', { name: 'CPU trend', exact: true }).evaluate(async element => {
    const resource = performance.getEntriesByType('resource').find(entry => new URL(entry.name).pathname.endsWith('/echarts_core.js'))!
    const core = await import(resource.name)
    const axis = core.getInstanceByDom(element).getOption().xAxis[0]
    return axis.max - axis.min
  })
  expect(duration).toBe(3600_000)
})

test('chart range selection freezes following and queries the selected interval', async ({ page }) => {
  await setup(page)
  await page.goto('/containers/' + containers[0].id + '#monitoring')
  const chart = page.getByRole('region', { name: 'Container monitoring' }).getByRole('img', { name: 'CPU trend', exact: true })
  await expect(chart.locator('canvas')).toHaveCount(1)
  await chart.scrollIntoViewIfNeeded()
  const box = await chart.boundingBox()
  await chart.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))))
  const queried = page.waitForRequest(request => {
    if (!request.url().endsWith('/monitoring/query') || request.method() !== 'POST') return false
    const target = request.postDataJSON().targets[0]
    const duration = Date.parse(target.until) - Date.parse(target.since)
    return duration > 1000 && duration < 3500_000
  })
  await page.mouse.move(box!.x + 59, box!.y + box!.height - 7)
  await page.mouse.down()
  await page.mouse.move(box!.x + box!.width * 0.3, box!.y + box!.height - 7, { steps: 8 })
  await page.mouse.up()
  await queried
  await expect(page.getByText('Fixed range', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Back to latest' })).toBeVisible()
})

test('leaving monitoring cancels an in-flight historical query', async ({ page }) => {
  const state = await setup(page)
  const delayed = gate()
  state.gates.set('/monitoring/query', delayed.promise)
  await page.goto('/containers/' + containers[0].id + '#monitoring')
  await expect.poll(() => state.calls).toContain('POST /monitoring/query')
  const canceled = page.waitForEvent('requestfailed', request => request.url().endsWith('/monitoring/query'))
  await page.getByRole('tab', { name: 'Events', exact: true }).click()
  await canceled
  delayed.resolve()
})
