import { expect, test, type Page } from '@playwright/test'

async function setup(page: Page, language = 'en-US', theme = 'dark') {
  await page.addInitScript(({ language, theme }) => {
    sessionStorage.setItem('suma-demo-session', '1')
    localStorage.setItem('suma-language', language)
    localStorage.setItem('suma-theme', theme)
    localStorage.setItem('suma-node', 'local')
  }, { language, theme })
}

async function openCreation(page: Page, zh = false) {
  await page.goto('/continuous-delivery')
  await page.getByRole('button', { name: zh ? '新建项目' : 'New project', exact: true }).click()
  await expect(page).toHaveURL('/continuous-delivery/new/project')
  await expect(page.getByRole('dialog')).toHaveCount(0)
  const form = page.getByRole('form', { name: zh ? '新建交付项目配置' : 'New delivery project configuration', exact: true })
  await expect(form.getByLabel(zh ? '项目名称' : 'Project name', { exact: true })).toBeVisible()
  return form
}

async function basics(page: Page, name: string, zh = false) {
  const form = await openCreation(page, zh)
  await form.getByLabel(zh ? '项目名称' : 'Project name', { exact: true }).fill(name)
  await form.getByLabel('Git Clone URL', { exact: true }).fill('https://git.example.com/team/deploy.git')
  return form
}

async function configuration(page: Page, name: string) {
  return page.evaluate(async name => {
    // @ts-expect-error Vite serves source imports in the browser.
    const { api } = await import('/src/lib/api.ts')
    return api(`/delivery-projects/${encodeURIComponent(name)}/configuration`)
  }, name)
}

for (const language of ['en-US', 'zh-CN']) for (const theme of ['dark', 'light']) for (const width of [390, 1440]) {
  test(`complete delivery creation ${language} ${theme} ${width}`, async ({ page }) => {
    await setup(page, language, theme)
    const zh = language === 'zh-CN'
    await page.setViewportSize({ width, height: 900 })
    const errors: string[] = []
    page.on('pageerror', error => errors.push(error.message))
    const name = `delivery-${language}-${theme}-${width}`
    const form = await basics(page, name, zh)
    await expect(form.getByRole('heading', { name: zh ? 'Git 认证' : 'Git authentication', exact: true })).toBeVisible()
    await expect(form.getByRole('heading', { name: zh ? '目标节点' : 'Target nodes', exact: true })).toBeVisible()
    await expect(form.getByRole('combobox', { name: zh ? '认证来源' : 'Authentication source', exact: true })).toContainText(zh ? '无需认证（公开仓库）' : 'No authentication (public repository)')
    await form.getByRole('combobox', { name: zh ? '引用类型' : 'Reference type', exact: true }).click()
    await page.getByRole('option', { name: 'Tag', exact: true }).click()
    await expect(form.getByRole('combobox', { name: zh ? '引用类型' : 'Reference type', exact: true })).toContainText('Tag')
    await form.getByLabel(zh ? '引用名称' : 'Reference', { exact: true }).fill('v1.2.3')
    await form.getByRole('button', { name: zh ? '添加 Compose 文件' : 'Add Compose file', exact: true }).click()
    await form.getByLabel(zh ? 'Compose 文件 2' : 'Compose file 2', { exact: true }).fill('deploy/production.yml')
    await form.getByLabel(zh ? '环境变量文件（仓库根目录相对路径）' : 'Environment file (relative to repository root)', { exact: true }).fill('env/production.env')
    await form.getByRole('checkbox', { name: /edge-hk/ }).click()
    await form.getByLabel(zh ? '轮询间隔（秒）' : 'Polling interval (seconds)', { exact: true }).fill('600')
    await form.getByLabel(zh ? '部署超时（秒）' : 'Deployment timeout (seconds)', { exact: true }).fill('180')
    await form.getByRole('checkbox', { name: /部署失败时自动回滚|Automatically roll back failed deployments/ }).click()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    await page.locator('main > div').first().evaluate(element => { element.scrollTop = 0 })
    await page.screenshot({ path: `/tmp/suma-delivery-create-${language}-${theme}-${width}.png`, fullPage: true })
    await form.getByRole('button', { name: zh ? '创建交付项目' : 'Create delivery project', exact: true }).click()
    await expect(page).toHaveURL(`/continuous-delivery/${name}`)
    const saved = await configuration(page, name)
    expect(saved.configured).toBe(true)
    expect(saved.repository.clone_url).toBe('https://git.example.com/team/deploy.git')
    expect(saved.repository.ref_type).toBe('tag')
    expect(saved.repository.ref).toBe('v1.2.3')
    expect(saved.repository.compose_files).toEqual(['compose.yml', 'deploy/production.yml'])
    expect(saved.repository.environment_file).toBe('env/production.env')
    expect(saved.node_ids).toEqual(['local', 'edge-hk'])
    expect(saved.sync_interval_seconds).toBe(600)
    expect(saved.deployment_timeout).toBe(180)
    expect(saved.auto_rollback).toBe(true)
    expect(saved.reconcile_mode).toBe('manual')
    expect(errors).toEqual([])
  })
}

test('drafts survive canceled navigation and global node switches', async ({ page }) => {
  await setup(page)
  const form = await basics(page, 'preserved-delivery')
  await form.getByRole('button', { name: 'Cancel', exact: true }).click()
  const guard = page.getByRole('dialog', { name: 'Discard unsaved delivery configuration?', exact: true })
  await guard.getByRole('button', { name: 'Cancel', exact: true }).click()
  await expect(form.getByLabel('Project name', { exact: true })).toHaveValue('preserved-delivery')
  await page.getByRole('combobox', { name: 'Current Docker node', exact: true }).click()
  await page.getByRole('option').filter({ hasText: 'edge-hk' }).click()
  await expect(form.getByLabel('Git Clone URL', { exact: true })).toHaveValue('https://git.example.com/team/deploy.git')
  await expect(form.getByRole('checkbox', { name: /homelab-01/ })).toBeChecked()
  await form.getByRole('button', { name: 'Cancel', exact: true }).click()
  await guard.getByRole('button', { name: 'Confirm', exact: true }).click()
  await expect(page).toHaveURL('/continuous-delivery')
})

test('failed creation preserves the full form and allows retry without an empty project', async ({ page }) => {
  await setup(page)
  const name = 'invalid-path-delivery'
  const form = await basics(page, name)
  await form.getByLabel('Compose file 1', { exact: true }).fill('../outside.yml')
  await form.getByRole('button', { name: 'Create delivery project', exact: true }).click()
  await expect(form.getByRole('alert')).toContainText('Invalid repository or Compose file path')
  await expect(form.getByLabel('Project name', { exact: true })).toHaveValue(name)
  const names = await page.evaluate(async () => {
    // @ts-expect-error Vite serves source imports in the browser.
    const { api } = await import('/src/lib/api.ts')
    return (await api('/delivery-projects')).map((project: { name: string }) => project.name)
  })
  expect(names).not.toContain(name)
  await form.getByLabel('Compose file 1', { exact: true }).fill('compose.yml')
  await form.getByRole('button', { name: 'Create delivery project', exact: true }).click()
  await expect(page).toHaveURL(`/continuous-delivery/${name}`)
})

for (const center of [false, true]) {
  test(`inline project credential ${center ? 'shared with center' : 'private'}`, async ({ page }) => {
    await setup(page)
    const name = center ? 'shared-credential-delivery' : 'private-credential-delivery'
    const form = await basics(page, name)
    await form.getByRole('combobox', { name: 'Authentication source', exact: true }).click()
    await page.getByRole('option', { name: 'Use a project credential', exact: true }).click()
    await form.getByLabel('Name', { exact: true }).fill('browser-git-credential')
    await form.getByLabel('Token', { exact: true }).fill('test-only-browser-token')
    await expect(form.getByLabel('Token', { exact: true })).toHaveAttribute('type', 'password')
    if (center) await form.getByRole('checkbox', { name: 'Also save to Authentication Center and authorize selected nodes', exact: true }).click()
    await form.getByRole('button', { name: 'Create delivery project', exact: true }).click()
    await expect(page).toHaveURL(`/continuous-delivery/${name}`)
    await expect(page.getByRole('dialog')).toHaveCount(0)
    const saved = await configuration(page, name)
    expect(saved.repository.authentication.source).toBe(center ? 'center' : 'project')
    expect(saved.repository.authentication.credential).toBeUndefined()
  })
}

test('generated webhook secret can be copied once before opening the project', async ({ page }) => {
  await setup(page)
  const name = 'webhook-delivery'
  const form = await basics(page, name)
  await form.getByRole('checkbox', { name: 'Enable repository webhook', exact: true }).click()
  await form.getByRole('button', { name: 'Create delivery project', exact: true }).click()
  await expect(form.getByRole('button', { name: 'Open delivery project', exact: true })).toBeVisible()
  await expect(page).toHaveURL('/continuous-delivery/new/project')
  await expect(form.getByLabel('Project name', { exact: true })).toBeDisabled()
  await expect(form.getByRole('textbox', { name: 'Webhook URL', exact: true })).toHaveValue(new RegExp(`/api/v1/webhooks/git/demo-${name}$`))
  await expect(form.getByRole('textbox', { name: 'Webhook URL', exact: true })).toBeEnabled()
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  await form.getByRole('button', { name: 'Copy secret', exact: true }).click()
  const secretLength = await page.evaluate(async () => (await navigator.clipboard.readText()).length)
  expect(secretLength).toBe(64)
  await form.getByRole('button', { name: 'Open delivery project', exact: true }).click()
  await expect(page).toHaveURL(`/continuous-delivery/${name}`)
  expect((await configuration(page, name)).webhook_secret).toBe('')
})

test('complete form remains available when editing an existing delivery project', async ({ page }) => {
  await setup(page)
  await page.goto('/continuous-delivery/gateway-prod')
  await page.getByRole('tab', { name: 'Settings', exact: true }).click()
  const form = page.getByRole('form', { name: 'Delivery project configuration', exact: true })
  await expect(form.getByLabel('Project name', { exact: true })).toHaveCount(0)
  await form.getByLabel('Git Clone URL', { exact: true }).fill('https://git.example.com/updated/deploy.git')
  await form.getByRole('button', { name: 'Save CD settings', exact: true }).click()
  await expect(page.getByRole('tab', { name: 'Overview', exact: true })).toHaveAttribute('aria-selected', 'true')
  expect((await configuration(page, 'gateway-prod')).repository.clone_url).toBe('https://git.example.com/updated/deploy.git')
})

test('a project named new remains reachable independently of the creation page', async ({ page }) => {
  await setup(page)
  const form = await basics(page, 'new')
  await form.getByRole('button', { name: 'Create delivery project', exact: true }).click()
  await expect(page).toHaveURL('/continuous-delivery/new')
  await expect(page.getByRole('heading', { name: 'new', exact: true })).toBeVisible()
  await expect(page.getByRole('form', { name: 'New delivery project configuration', exact: true })).toHaveCount(0)
})
