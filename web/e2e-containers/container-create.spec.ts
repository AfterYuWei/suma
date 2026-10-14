import { expect, test, type Page, type WebSocketRoute } from '@playwright/test'

const containerID = 'c'.repeat(64)
const taskID = 'container-create-task'

async function setup(page: Page, language = 'en-US', theme = 'dark', width = 1440) {
  const state = { input: undefined as Record<string, unknown> | undefined, status: 'pending', created: false, message: 'Checking image', fail: false, calls: [] as string[], socket: undefined as WebSocketRoute | undefined, sockets: 0, closed: 0 }
  await page.setViewportSize({ width, height: 900 })
  await page.addInitScript(({ language, theme }) => {
    localStorage.setItem('suma-language', language); localStorage.setItem('suma-theme', theme); localStorage.setItem('suma-node', 'local'); localStorage.setItem('suma-node-group', 'all')
  }, { language, theme })
  await page.routeWebSocket(/\/ws\/nodes\/[^/]+\/tasks\//, socket => { state.socket = socket; state.sockets++; socket.onClose(() => { state.closed++ }) })
  const nodes = ['local', 'edge'].map(id => ({ id, name: id, enabled: true, status: 'online', connection_type: 'unix', engine_id: 'engine-' + id, endpoint: 'unix:///var/run/docker.sock', tls_mode: 'disabled', group_ids: [] }))
  await page.route('**/api/v1/**', async route => {
    const request = route.request(), path = new URL(request.url()).pathname.replace('/api/v1', '')
    state.calls.push(request.method() + ' ' + path)
    let data: unknown = []
    let status = 200
    if (path === '/auth/status') data = { needs_setup: false }
    else if (path === '/auth/session') data = { id: 1, username: 'admin' }
    else if (path === '/settings') data = {}
    else if (path === '/nodes') data = nodes
    else if (path === '/notifications/inbox') data = { unread: 0, items: [] }
    else if (path === '/credentials/registries') data = [{ id: 1, name: 'Local credential', server_address: 'ghcr.io', authorized_node_ids: ['local'] }, { id: 2, name: 'Other credential', server_address: 'private.test', authorized_node_ids: ['edge'] }]
    else if (path.endsWith('/images')) data = [{ id: 'sha256:' + 'a'.repeat(64), tags: ['alpine:3.24'] }]
    else if (path.endsWith('/networks')) data = [{ name: 'custom-net' }]
    else if (path.endsWith('/volumes')) data = [{ name: 'app-data' }]
    else if (path.endsWith('/containers') && request.method() === 'POST') {
      state.input = request.postDataJSON()
      status = state.fail ? 409 : 202
      data = state.fail ? null : { id: taskID, status: 'pending', progress: 0, message: 'Accepted' }
    } else if (path.endsWith('/tasks/' + taskID + '/cancel')) { state.status = 'canceled'; state.message = 'Canceled'; data = {} }
    else if (path.endsWith('/tasks/' + taskID + '/steps')) data = state.created ? [{ id: 'container:' + containerID, status: 'Created' }] : []
    else if (path.endsWith('/tasks/' + taskID)) data = { id: taskID, status: state.status, progress: state.status === 'success' ? 100 : 35, message: state.message }
    else if (path.endsWith('/containers/' + containerID)) data = { id: containerID, name: state.input?.name ?? 'app', image: 'alpine:3.24', state: 'running', status: 'Up', labels: {}, created: new Date().toISOString(), environment: [], entrypoint: [], command: [], ports: [], networks: [], mounts: [], restart_policy: 'unless-stopped' }
    try { await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify({ code: state.fail && status === 409 ? 20262 : 0, message: status === 409 ? 'Container name is already in use' : 'success', data }) }) } catch { /* Requests may be canceled on navigation. */ }
  })
  return state
}

async function fillBasics(page: Page, zh = false) {
  await page.getByLabel(zh ? '容器名称' : 'Container name', { exact: true }).fill('new-app')
  await page.getByLabel(zh ? '镜像' : 'Image', { exact: true }).fill('alpine:3.24')
}

for (const language of ['zh-CN', 'en-US']) for (const theme of ['dark', 'light']) for (const width of [390, 1440]) {
  test(`container creation dropdown layout ${language} ${theme} ${width}`, async ({ page }) => {
    await setup(page, language, theme, width)
    const zh = language === 'zh-CN'
    const credential = 'production-registry-credential-with-a-very-long-name-' + 'a'.repeat(80)
    const network = 'application-network-' + 'b'.repeat(100)
    await page.route('**/api/v1/credentials/registries', route => route.fulfill({ json: { code: 0, data: [{ id: 1, name: credential, server_address: 'ghcr.io', authorized_node_ids: ['local'] }] } }))
    await page.route('**/api/v1/nodes/local/networks', route => route.fulfill({ json: { code: 0, data: [{ name: network }] } }))
    await page.goto('/containers/new')

    async function checkMenu(label: string, selected?: string, screenshot?: string, minimumWidth = 0) {
      const trigger = page.getByRole('combobox', { name: label, exact: true })
      await trigger.click()
      const menu = page.locator('[data-slot="select-content"]:visible')
      await expect(menu).toBeVisible()
      await expect(async () => {
        const anchor = (await trigger.boundingBox())!, popup = (await menu.boundingBox())!
        expect(Math.abs(popup.x - anchor.x)).toBeLessThanOrEqual(1)
        expect(Math.abs(popup.width - Math.max(anchor.width, minimumWidth))).toBeLessThanOrEqual(1)
        expect(popup.y >= anchor.y + anchor.height + 3 || popup.y + popup.height <= anchor.y - 3).toBe(true)
        expect(popup.x).toBeGreaterThanOrEqual(0)
        expect(popup.x + popup.width).toBeLessThanOrEqual(width)
        const first = (await menu.getByRole('option').first().boundingBox())!
        expect(first.x - popup.x).toBeGreaterThanOrEqual(4)
        expect(first.y - popup.y).toBeGreaterThanOrEqual(4)
      }).toPass({ timeout: 3000 })
      if (selected) {
        const option = menu.getByRole('option', { name: selected, exact: true })
        await expect(option).toHaveAttribute('aria-selected', 'true')
        expect(await option.evaluate(element => element.scrollWidth <= element.clientWidth)).toBe(true)
      }
      const last = menu.getByRole('option').last()
      await last.hover()
      await expect(last).toHaveAttribute('data-highlighted', '')
      expect(await last.evaluate(element => getComputedStyle(element).backgroundColor)).not.toBe('rgba(0, 0, 0, 0)')
      if (screenshot) await page.screenshot({ path: `/tmp/suma-container-dropdown-${screenshot}-${language}-${theme}-${width}.png`, fullPage: true })
      await page.keyboard.press('Escape')
      await expect(menu).toHaveCount(0)
      await expect(trigger).toBeFocused()
    }

    await checkMenu(zh ? '镜像拉取策略' : 'Image pull policy')
    const registry = page.getByRole('combobox', { name: zh ? '镜像仓库凭据' : 'Registry credential', exact: true })
    await registry.click(); await page.getByRole('option', { name: credential + ' · ghcr.io', exact: true }).click()
    await expect(registry).toContainText(credential)
    const value = registry.locator('[data-slot="select-value"]')
    expect(await value.evaluate(element => element.scrollWidth > element.clientWidth && getComputedStyle(element).textOverflow === 'ellipsis')).toBe(true)
    await checkMenu(zh ? '镜像仓库凭据' : 'Registry credential', credential + ' · ghcr.io', 'registry')
    await page.getByRole('tab', { name: zh ? '网络与存储' : 'Network & storage', exact: true }).click()
    await page.getByRole('combobox', { name: zh ? '网络' : 'Network', exact: true }).click()
    await page.getByRole('option', { name: network, exact: true }).click()
    await checkMenu(zh ? '网络' : 'Network', network, 'network')
    await page.getByRole('button', { name: zh ? '添加端口' : 'Add port', exact: true }).click()
    await checkMenu(zh ? '协议' : 'Protocol', 'TCP')
    await page.getByRole('button', { name: zh ? '添加挂载' : 'Add mount', exact: true }).click()
    await checkMenu(zh ? '挂载类型' : 'Mount type', zh ? '具名存储卷' : 'Named volume', 'mount')
    await page.getByRole('tab', { name: zh ? '运行配置' : 'Runtime', exact: true }).click()
    await checkMenu(zh ? '重启策略' : 'Restart policy')
    const restart = page.getByRole('combobox', { name: zh ? '重启策略' : 'Restart policy', exact: true })
    await page.mouse.move(0, 0)
    await restart.press('ArrowDown')
    const failure = page.getByRole('option', { name: zh ? '失败时重启' : 'On failure', exact: true })
    await expect(page.getByRole('option', { name: zh ? '除非手动停止' : 'Unless stopped', exact: true })).toBeFocused()
    await page.keyboard.press('End')
    await expect(failure).toHaveAttribute('data-highlighted', '')
    await page.keyboard.press('Enter')
    await expect(restart).toContainText(zh ? '失败时重启' : 'On failure')
    await checkMenu(zh ? '重启策略' : 'Restart policy', zh ? '失败时重启' : 'On failure', 'restart')
    await checkMenu(zh ? '当前 Docker 节点' : 'Current Docker node')
    await checkMenu(zh ? '节点 Group 筛选' : 'Node group filter', zh ? '全部 Group' : 'All groups', 'group', 144)
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width)
  })
}

for (const language of ['zh-CN', 'en-US']) for (const theme of ['dark', 'light']) for (const width of [390, 1440]) {
  test(`container creation form and completion ${language} ${theme} ${width}`, async ({ page }) => {
    const state = await setup(page, language, theme, width), zh = language === 'zh-CN'
    const errors: string[] = []; page.on('pageerror', error => errors.push(error.message))
    await page.goto('/containers')
    const search = page.getByRole('textbox', { name: zh ? '筛选容器' : 'Filter containers', exact: true })
    const button = page.getByRole('button', { name: zh ? '新增容器' : 'New container', exact: true })
    await expect(button).toBeVisible()
    const [input, action] = await Promise.all([search.boundingBox(), button.boundingBox()])
    expect(action!.y).toBeGreaterThanOrEqual(input!.y + input!.height)
    const searchGroup = await search.evaluate(input => { const rect = input.closest('[data-slot="input-group"]')!.getBoundingClientRect(); return rect.right })
    expect(Math.abs(action!.x + action!.width - searchGroup)).toBeLessThanOrEqual(1)
    await button.click(); await expect(page).toHaveURL('/containers/new')
    await fillBasics(page, zh)
    await page.getByRole('combobox', { name: zh ? '镜像仓库凭据' : 'Registry credential', exact: true }).click()
    await expect(page.getByRole('option', { name: /Local credential/ })).toBeVisible()
    await expect(page.getByRole('option', { name: /Other credential/ })).toHaveCount(0)
    await page.keyboard.press('Escape')
    await page.getByRole('tab', { name: zh ? '网络与存储' : 'Network & storage', exact: true }).click()
    await page.getByRole('button', { name: zh ? '添加端口' : 'Add port', exact: true }).click()
    await page.getByLabel(zh ? '宿主机端口 1' : 'Host port 1').fill('8080')
    await page.getByLabel(zh ? '容器端口 1' : 'Container port 1').fill('80')
    await page.getByRole('button', { name: zh ? '添加挂载' : 'Add mount', exact: true }).click()
    await page.getByLabel(zh ? '挂载来源 1' : 'Mount source 1').fill('app-data')
    await page.getByLabel(zh ? '挂载目标 1' : 'Mount target 1').fill('/data')
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width)
    await page.screenshot({ path: `/tmp/suma-container-create-storage-${language}-${theme}-${width}.png`, fullPage: true })
    await page.getByRole('tab', { name: zh ? '运行配置' : 'Runtime', exact: true }).click()
    await page.getByRole('button', { name: zh ? '添加环境变量' : 'Add variable', exact: true }).click()
    await page.getByLabel(zh ? '变量名 1' : 'Variable name 1').fill('PASSWORD')
    const secret = page.getByLabel(zh ? '变量值 1' : 'Variable value 1')
    await secret.fill('browser-creation-secret'); await expect(secret).toHaveAttribute('type', 'password')
    await page.getByLabel(zh ? '内存上限（MiB）' : 'Memory limit (MiB)').fill('128')
    await page.getByLabel(zh ? 'CPU 上限（核）' : 'CPU limit (cores)').fill('0.5')
    await page.getByText(zh ? '命令与高级配置' : 'Command and advanced options', { exact: true }).click()
    await page.getByLabel(zh ? '命令参数' : 'Command arguments').fill('sleep\n3600')
    await page.screenshot({ path: `/tmp/suma-container-create-runtime-${language}-${theme}-${width}.png`, fullPage: true })
    await page.getByRole('button', { name: zh ? '创建并启动' : 'Create and start', exact: true }).click()
    await expect(page.getByRole('region', { name: zh ? '创建进度' : 'Creation progress' })).toBeVisible()
    expect(state.input).toMatchObject({ name: 'new-app', image: 'alpine:3.24', pull_policy: 'missing', start: true, ports: [{ host_ip: '127.0.0.1', host_port: 8080, container_port: 80 }], mounts: [{ type: 'volume', source: 'app-data', target: '/data' }], command: ['sleep', '3600'], memory_mb: 128, cpus: .5, environment: [{ key: 'PASSWORD', value: 'browser-creation-secret' }] })
    await expect.poll(() => state.sockets).toBe(1)
    state.created = true; state.status = 'success'; state.message = 'Container ready'
    state.socket?.send(JSON.stringify({ type: 'status', status: 'success' }))
    await expect(page.getByText(zh ? '容器创建完成' : 'Container created', { exact: true })).toBeVisible()
    await expect(page.getByRole('button', { name: zh ? '查看容器' : 'View container', exact: true })).toBeVisible()
    await expect.poll(() => state.closed).toBe(1)
    await page.getByRole('button', { name: zh ? '返回容器' : 'Back to containers', exact: true }).click()
    await expect(page).toHaveURL('/containers')
    expect(errors).toEqual([])
  })
}

test('container creation protects drafts and resets them only after approved node changes', async ({ page }) => {
  const state = await setup(page)
  await page.goto('/containers/new'); await fillBasics(page)
  await page.getByRole('button', { name: 'Back to containers', exact: true }).click()
  await expect(page.getByRole('dialog')).toContainText('Discard unsaved container configuration')
  await page.getByRole('button', { name: 'Cancel', exact: true }).click()
  await expect(page.getByLabel('Container name', { exact: true })).toHaveValue('new-app')
  await page.getByRole('combobox', { name: 'Current Docker node', exact: true }).click()
  await page.getByRole('option', { name: /edge · UNIX/ }).click()
  await expect(page.getByRole('dialog')).toContainText('Discard container configuration and change node')
  await page.getByRole('button', { name: 'Cancel', exact: true }).click()
  await expect(page.getByLabel('Container name', { exact: true })).toHaveValue('new-app')
  await page.getByRole('combobox', { name: 'Current Docker node', exact: true }).click()
  await page.getByRole('option', { name: /edge · UNIX/ }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Confirm', exact: true }).click()
  await expect(page.getByLabel('Container name', { exact: true })).toHaveValue('')
  await fillBasics(page)
  await page.getByRole('button', { name: 'Create and start', exact: true }).click()
  await expect.poll(() => state.calls).toContain('POST /nodes/edge/containers')
  await expect(state.calls.filter(call => call === 'POST /nodes/local/containers')).toHaveLength(0)
})

test('container creation preserves failed request data and keeps an existing container after failed start', async ({ page }) => {
  const state = await setup(page)
  await page.goto('/containers/new'); await fillBasics(page)
  state.fail = true
  await page.getByRole('button', { name: 'Create and start', exact: true }).click()
  await expect(page.getByRole('alert')).toContainText('Container name is already in use')
  await expect(page.getByLabel('Container name', { exact: true })).toHaveValue('new-app')
  state.fail = false
  await page.getByRole('button', { name: 'Create and start', exact: true }).click()
  await expect(page.getByRole('region', { name: 'Creation progress' })).toBeVisible()
  state.created = true; state.status = 'failed'; state.message = 'Port already in use; container retained'
  state.socket?.send(JSON.stringify({ type: 'status', status: 'failed' }))
  await expect(page.getByText('Creation failed', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'View container', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Edit and retry', exact: true })).toHaveCount(0)
})

test('container creation validates hidden-tab values and confirms Docker socket access', async ({ page }) => {
  const state = await setup(page)
  await page.goto('/containers/new'); await fillBasics(page)
  await page.getByRole('tab', { name: 'Network & storage', exact: true }).click()
  await page.getByRole('button', { name: 'Add mount', exact: true }).click()
  await page.getByRole('combobox', { name: 'Mount type', exact: true }).click()
  await page.getByRole('option', { name: 'Node path', exact: true }).click()
  await page.getByLabel('Mount source 1').fill('./data'); await page.getByLabel('Mount target 1').fill('/data')
  await page.getByRole('tab', { name: 'Basics', exact: true }).click()
  await page.getByRole('button', { name: 'Create and start', exact: true }).click()
  await expect(page.getByRole('alert')).toContainText('Bind mounts need non-interpolated absolute node paths')
  expect(state.input).toBeUndefined()
  await page.getByRole('tab', { name: 'Network & storage', exact: true }).click()
  await page.getByLabel('Mount source 1').fill('/run/docker.sock'); await page.getByLabel('Mount target 1').fill('/socket')
  await page.getByRole('button', { name: 'Create and start', exact: true }).click()
  await expect(page.getByRole('dialog')).toContainText('full control of this Docker node')
  await page.getByRole('button', { name: 'Cancel', exact: true }).click()
  expect(state.input).toBeUndefined()
  await expect(page.getByLabel('Mount source 1')).toHaveValue('/run/docker.sock')
})

test('canceling a container creation task closes progress streaming and permits editing', async ({ page }) => {
  const state = await setup(page)
  await page.goto('/containers/new'); await fillBasics(page)
  await page.getByRole('button', { name: 'Create and start', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Cancel task', exact: true })).toBeVisible()
  await expect.poll(() => state.sockets).toBe(1)
  await page.getByRole('button', { name: 'Cancel task', exact: true }).click()
  await expect(page.getByText('Creation canceled', { exact: true })).toBeVisible()
  await expect.poll(() => state.closed).toBe(1)
  await page.getByRole('button', { name: 'Edit and retry', exact: true }).click()
  await expect(page.getByLabel('Container name', { exact: true })).toHaveValue('new-app')
})
