import { expect, test, type Page } from '@playwright/test'
import type { ContainerSummary } from '../src/features/containers/types'
import type { ImageUpdateResult } from '../src/features/image-updates/types'

const created = '2026-10-06T04:43:40Z'
const container = (name: string, index: number, overrides: Partial<ContainerSummary> = {}): ContainerSummary => ({
  id: index.toString(16).padStart(64, '0'), name: `database-${name}-1`, image: `docker.io/library/${name}:latest`,
  command: '', created, state: 'running', status: 'Up 1 minute', uptime_seconds: 60,
  cpu_percent: 0.4, memory_bytes: 426 * 1024 ** 2,
  labels: { 'com.docker.compose.project': 'database', 'com.docker.compose.service': name, 'com.docker.compose.container-number': '1' },
  ports: [{ private_port: 3306, public_port: 3306, type: 'tcp', ip: '0.0.0.0' }], ...overrides
})
const containers = [
  container('mysql', 1, { image: 'mysql:8.4.6', ports: [
    { private_port: 3306, public_port: 3306, type: 'tcp', ip: '0.0.0.0' },
    { private_port: 3306, public_port: 3306, type: 'tcp', ip: '::' },
    { private_port: 33060, public_port: 33060, type: 'tcp', ip: '127.0.0.1' },
    { private_port: 33061, public_port: 33061, type: 'tcp', ip: '127.0.0.1' },
    { private_port: 80, public_port: 8080, type: 'udp', ip: '10.0.0.2' }
  ] }),
  container('postgres', 2, { image: 'postgres:16', cpu_percent: 0, memory_bytes: 30 * 1024 ** 2, ports: [{ private_port: 5432, type: 'tcp' }] }),
  container('redis', 3, { image: 'redis:alpine', cpu_percent: 0.2, memory_bytes: 16 * 1024 ** 2, ports: [] })
]

async function setup(page: Page, options: {
  language?: string; theme?: string; rows?: ContainerSummary[]; declared?: string[];
  managed?: boolean; error?: 'services' | 'image-updates'
} = {}) {
  const language = options.language ?? 'en-US'
  const theme = options.theme ?? 'dark'
  const rows = options.rows ?? containers
  const declared = options.declared ?? ['mysql', 'postgres', 'redis']
  const writes: { path: string; body: Record<string, unknown> }[] = []
  await page.addInitScript(({ language, theme }) => {
    localStorage.setItem('suma-language', language)
    localStorage.setItem('suma-theme', theme)
    localStorage.setItem('suma-node', 'local')
    localStorage.setItem('suma-list-page-size', '20')
  }, { language, theme })
  await page.route('**/api/v1/**', async route => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname.replace('/api/v1', '')
    if (request.method() !== 'GET') writes.push({ path, body: request.postDataJSON() ?? {} })
    const edge = path.startsWith('/nodes/edge-hk/')
    const nodeRows = edge ? [container('edge-only', 99)] : rows
    const project = {
      name: 'database', native_name: 'database', node_id: edge ? 'edge-hk' : 'local', backend: 'compose',
      managed: options.managed !== false, capabilities: ['view', 'edit', 'deploy', 'services', 'logs', 'takeover'],
      status: 'running', revision: 'r1', services: declared.length, containers: nodeRows.length,
      compose: 'services:\n' + declared.map(name => `  ${name}: {image: ${name}:latest}\n`).join(''), environment: '', metadata: { origin: 'created', last_deployed_at: created }
    }
    const updates: ImageUpdateResult[] = nodeRows.map((row, index) => ({
      reference: row.image.startsWith('docker.io/') ? row.image : `docker.io/library/${row.image}`, registry: 'docker.io',
      platform: { os: 'linux', architecture: 'amd64' }, local_image_id: `sha256:${row.id}`,
      status: index === 0 ? 'update_available' : 'current', stale: false, pull_required: index === 0, recreate_required: index === 0,
      checked_at: created, remote_manifest_digest: `sha256:${'ab'.repeat(32)}`,
      containers: [{ container_id: row.id, container_name: row.name, service: row.labels['com.docker.compose.service'], project: 'database', delivery_project: index === 0 ? 'database-delivery' : undefined, state: row.state, image_id: `sha256:${row.id}`, reference: row.image }]
    }))
    let data: unknown = []
    if (path === '/auth/status') data = { needs_setup: false }
    else if (path === '/auth/session') data = { id: 1, username: 'admin', nickname: 'Admin', has_avatar: false }
    else if (path === '/settings') data = { 'general.timezone': 'Asia/Shanghai' }
    else if (path === '/nodes') data = ['local', 'edge-hk'].map(id => ({ id, name: id, enabled: true, connection_type: id === 'local' ? 'unix' : 'tcp', status: 'online', group_ids: [1] }))
    else if (path === '/node-groups') data = [{ id: 1, name: 'Default', is_default: true }]
    else if (path === '/notifications/inbox') data = { items: [], unread: 0 }
    else if (path.endsWith('/projects/compose/database/services')) {
      if (options.error === 'services') return route.fulfill({ status: 503, json: { code: 1, message: 'Engine unavailable', data: null } })
      data = nodeRows
    } else if (path.endsWith('/projects/compose/database')) data = project
    else if (path.endsWith('/containers/metrics')) data = nodeRows.map(row => ({ id: row.id, cpu_percent: row.cpu_percent, memory_bytes: row.memory_bytes, uptime_seconds: row.uptime_seconds }))
    else if (path.endsWith('/image-updates')) {
      if (options.error === 'image-updates') return route.fulfill({ status: 503, json: { code: 1, message: 'Image checks unavailable', data: null } })
      data = { results: updates }
    } else if (path.endsWith('/image-updates/policy')) data = { enabled: false, interval_hours: 6, registry_credentials: {} }
    else if (path.endsWith('/image-updates/check')) data = { id: 'check-task', status: 'success', progress: 100, message: 'Checked project images' }
    else if (path.endsWith('/tasks/check-task')) data = { id: 'check-task', status: 'success', progress: 100, message: 'Checked project images' }
    else if (path.endsWith('/projects/compose/database/validate')) data = { valid: true }
    return route.fulfill({ json: { code: 0, message: '', data } })
  })
  await page.goto('/projects/compose/database')
  const zh = language === 'zh-CN'
  await page.getByRole('tab', { name: zh ? '服务' : 'Services', exact: true }).click()
  return { zh, writes, list: page.getByRole('region', { name: zh ? '项目服务' : 'Project services', exact: true }) }
}

for (const language of ['en-US', 'zh-CN']) for (const theme of ['dark', 'light']) for (const width of [390, 1440]) {
  test(`unified project services ${language} ${theme} ${width}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    const errors: string[] = []
    page.on('pageerror', error => errors.push(error.message))
    const { zh, list, writes } = await setup(page, { language, theme })
    const mysql = list.getByRole('button', { name: /(?:Service details|服务详情) mysql/ })
    await expect(mysql).toHaveAttribute('aria-expanded', 'false')
    await expect(mysql).toContainText(zh ? '有更新' : 'Update available')
    await expect(list.getByRole('link', { name: 'database-mysql-1', exact: true })).toHaveCount(0)
    await expect(page.getByRole('button', { name: zh ? '保存' : 'Save', exact: true })).toHaveCount(0)
    await expect(page.getByRole('button', { name: zh ? '校验' : 'Validate', exact: true })).toHaveCount(0)
    await expect(page.getByRole('button', { name: zh ? '拉取并重建' : 'Pull & recreate', exact: true })).toHaveCount(1)
    await expect(page.getByRole('navigation', { name: zh ? '列表分页' : 'List pagination', exact: true })).toHaveCount(0)
    await mysql.focus()
    await page.keyboard.press('Enter')
    await expect(mysql).toHaveAttribute('aria-expanded', 'true')
    await expect(list.getByRole('link', { name: 'database-mysql-1', exact: true })).toHaveAttribute('href', `/containers/${containers[0].id}`)
    await expect(list.getByRole('link', { name: 'database-delivery', exact: true })).toHaveAttribute('href', '/continuous-delivery/database-delivery')
    await expect(list.getByText('docker.io/library/mysql:8.4.6', { exact: true })).toBeVisible()
    await expect(list.getByText('[::]:3306 → 3306/tcp', { exact: true })).toBeVisible()
    await expect(list.getByText('10.0.0.2:8080 → 80/udp', { exact: true })).toBeVisible()
    const date = await page.evaluate(({ language, created }) => new Intl.DateTimeFormat(language, { timeZone: 'Asia/Shanghai', year: 'numeric', month: 'numeric', day: 'numeric', hour: 'numeric', minute: '2-digit', second: '2-digit' }).format(new Date(created)), { language, created })
    await expect(list.getByText(date, { exact: true }).first()).toBeVisible()
    await list.getByRole('button', { name: /(?:Service details|服务详情) redis/ }).click()
    await expect(mysql).toHaveAttribute('aria-expanded', 'false')
    const nameWidth = await mysql.locator('span').filter({ hasText: /^mysql$/ }).first().evaluate(element => element.getBoundingClientRect().width)
    expect(nameWidth).toBeGreaterThan(25)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    await page.locator('main > div').first().evaluate(element => { element.scrollTop = 0 })
    await page.evaluate(() => window.scrollTo(0, 0))
    await page.screenshot({ path: `/tmp/suma-project-services-${language}-${theme}-${width}.png`, fullPage: true })
    await list.getByRole('button', { name: zh ? '检查镜像更新' : 'Check image updates', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: zh ? '检查镜像更新' : 'Check image updates', exact: true })
    await dialog.getByRole('button', { name: zh ? '开始检测' : 'Start check', exact: true }).click()
    await expect(dialog).toContainText('Checked project images')
    expect(writes.find(row => row.path.endsWith('/image-updates/check'))?.body.project_name).toBe('database')
    await dialog.getByRole('button', { name: zh ? '关闭' : 'Close', exact: true }).first().click()
    await page.getByRole('tab', { name: zh ? '配置' : 'Configuration', exact: true }).click()
    await expect(page.getByRole('button', { name: zh ? '校验' : 'Validate', exact: true })).toBeVisible()
    await expect(page.getByRole('button', { name: zh ? '保存' : 'Save', exact: true })).toBeVisible()
    expect(errors).toEqual([])
  })
}

test('replicas, stopped instances and undeployed services stay distinct', async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 900 })
  const replica = container('mysql', 4, { name: 'database-mysql-2', state: 'exited', ports: [], labels: { ...containers[0].labels, 'com.docker.compose.container-number': '2' } })
  const { list } = await setup(page, { rows: [...containers, replica], declared: ['mysql', 'postgres', 'redis', 'worker'] })
  await expect(list.getByRole('button', { name: /Service details mysql/ })).toHaveCount(2)
  const stopped = list.getByRole('button', { name: /Service details mysql · #2/ })
  await expect(stopped).toContainText('exited')
  await expect(stopped).not.toContainText('CPU')
  await stopped.click()
  await expect(list.getByRole('link', { name: 'database-mysql-2', exact: true })).toBeVisible()
  await expect(list).toContainText('Not deployed · no local runtime version to check')
  await expect(list).toContainText('4 services · 4 containers')
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
})

test('pagination includes undeployed services and resets on node change', async ({ page }) => {
  const { list } = await setup(page, { declared: [...['mysql', 'postgres', 'redis'], ...Array.from({ length: 22 }, (_, index) => `worker-${String(index).padStart(2, '0')}`)] })
  const pagination = page.getByRole('navigation', { name: 'List pagination', exact: true })
  await expect(pagination).toContainText('25 items')
  await pagination.getByRole('button', { name: 'Next page', exact: true }).click()
  await expect(list.getByText('worker-21', { exact: true })).toBeVisible()
  await page.getByRole('combobox', { name: 'Current Docker node', exact: true }).click()
  await page.getByRole('option', { name: /edge-hk/ }).click()
  await expect(list.getByRole('button', { name: /Service details edge-only/ })).toBeVisible()
  await expect(pagination).toContainText('1 / 2')
  await expect(list.getByRole('button', { name: /Service details mysql/ })).toHaveCount(0)
})

test('external projects preserve container navigation and takeover', async ({ page }) => {
  const { list } = await setup(page, { managed: false })
  await expect(page.getByRole('tab', { name: 'Configuration', exact: true })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Take over', exact: true }).first()).toBeVisible()
  await list.getByRole('button', { name: /Service details postgres/ }).click()
  await expect(list.getByRole('link', { name: 'database-postgres-1', exact: true })).toBeVisible()
})

for (const error of ['services', 'image-updates'] as const) {
  test(`${error} failure is visible`, async ({ page }) => {
    const { list } = await setup(page, { error })
    await expect(list).toContainText(error === 'services' ? 'Engine unavailable' : 'Image checks unavailable')
    if (error === 'image-updates') await expect(list.getByRole('button', { name: /Service details mysql/ })).toBeVisible()
  })
}
