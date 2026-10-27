import { expect, test } from '@playwright/test'
import type { CDConfiguration, CDConfigureInput } from '../src/features/delivery/types'

for (const language of ['zh-CN', 'en-US']) for (const theme of ['dark', 'light']) for (const width of [390, 1440]) {
  test(`saved delivery environment ${language} ${theme} ${width}`, async ({ page }) => {
    const zh = language === 'zh-CN'
    await page.setViewportSize({ width, height: 1000 })
    await page.addInitScript(({ language, theme }) => {
      localStorage.setItem('suma-language', language)
      localStorage.setItem('suma-theme', theme)
      localStorage.setItem('suma-node', 'local')
    }, { language, theme })
    let saved: Record<string, string> = { EMPTY: '', NACOS_DATA_ID: 'admin-production.yaml', NACOS_PASSWORD: 'test-only-saved-value', NACOS_SERVER_ADDR: 'nacos.example.test:8848' }
    let configuration: CDConfiguration = {
      configured: true, automation_enabled: true, repository: { clone_url: 'https://git.example.test/app', ref_type: 'branch', ref: 'main', authentication: { source: 'none' }, compose_files: ['compose.yml'], environment_file: '' },
      environment: Object.keys(saved).map(name => ({ name, has_value: true })), reconcile_mode: 'manual', sync_interval_seconds: 300, desired_commit: '', observed_commit: '',
      auto_rollback: false, deployment_timeout: 60, webhook_enabled: false, node_ids: ['local'], registry_credential_ids: [],
    }
    let reads = 0, failRead = false, holdRead = false
    let releaseRead: (() => void) | undefined
    const writes: CDConfigureInput[] = [], unexpectedWrites: string[] = [], errors: string[] = []
    page.on('pageerror', error => errors.push(error.message))
    await page.route('**/api/v1/**', async route => {
      const request = route.request(), path = new URL(request.url()).pathname.replace('/api/v1', '')
      let data: unknown = []
      if (request.method() !== 'GET' && !(path.endsWith('/configuration') && request.method() === 'PUT')) unexpectedWrites.push(path)
      if (path === '/auth/status') data = { needs_setup: false }
      else if (path === '/auth/session') data = { id: 1, username: 'admin', has_avatar: false }
      else if (path === '/settings') data = { 'general.timezone': 'Asia/Shanghai' }
      else if (path === '/nodes') data = [{ id: 'local', name: 'Aliyun', enabled: true, connection_type: 'unix', status: 'online', group_ids: [] }]
      else if (path === '/notifications/inbox') data = { items: [], unread: 0 }
      else if (path === '/delivery-projects/app') data = { id: 1, name: 'app', configured: true, automation_enabled: true, node_ids: ['local'] }
      else if (path.endsWith('/configuration')) {
        if (request.method() === 'PUT') {
          const input = request.postDataJSON() as CDConfigureInput
          writes.push(input)
          saved = Object.fromEntries((input.environment ?? []).map(row => [row.name, row.value ?? saved[row.name]]))
          configuration = { ...configuration, ...input, environment: Object.keys(saved).map(name => ({ name, has_value: true })) }
        }
        data = configuration
      } else if (path.endsWith('/environment')) {
        reads++
        if (holdRead) await new Promise<void>(resolve => { releaseRead = resolve })
        if (failRead) {
          await route.fulfill({ status: 500, json: { code: 17718, message: 'test-only-private-provider-error', data: null } })
          return
        }
        data = saved
      } else if (path.endsWith('/drift')) data = { status: 'unknown', drifted: false, runtime_healthy: false, nodes: [] }
      await route.fulfill({ headers: { 'Cache-Control': 'no-store' }, json: { code: 0, message: '', data } })
    })
    await page.goto('/continuous-delivery/app')
    const settings = page.getByRole('tab', { name: zh ? '设置' : 'Settings', exact: true })
    await settings.click()
    const form = page.getByRole('form', { name: zh ? '交付项目配置' : 'Delivery project configuration', exact: true })
    const input = (index: number) => form.getByLabel(`${zh ? '变量值' : 'Variable value'} ${index}`, { exact: true })
    const show = () => form.getByRole('button', { name: zh ? '显示变量值' : 'Show variable values', exact: true })
    const hide = () => form.getByRole('button', { name: zh ? '隐藏变量值' : 'Hide variable values', exact: true })
    await expect(input(3)).toHaveValue('')
    await expect(input(3)).toHaveAttribute('type', 'password')
    expect(reads).toBe(0)
    failRead = true
    await show().click()
    await expect(form.getByRole('alert')).toHaveText(zh ? '无法读取已保存的变量值，请重试。' : 'Unable to read saved values. Try again.')
    await expect(form).not.toContainText('test-only-private-provider-error')
    await expect(input(3)).toHaveValue('')
    failRead = false
    holdRead = true
    releaseRead = undefined
    await show().click()
    await expect(form.getByRole('button', { name: zh ? '读取中…' : 'Loading…', exact: true })).toBeDisabled()
    await expect.poll(() => !!releaseRead).toBe(true)
    holdRead = false
    releaseRead!()
    await expect(input(3)).toHaveValue('test-only-saved-value')
    await expect(input(1)).toBeEditable()
    await hide().click()
    await expect(input(3)).toHaveValue('')
    await expect(input(3)).toHaveAttribute('type', 'password')
    expect(writes).toHaveLength(0)
    await show().click()
    await expect(input(3)).toHaveValue('test-only-saved-value')
    await input(1).fill('nonempty')
    await input(2).fill('')
    await input(3).fill('test-only-replacement')
    await hide().click()
    await expect(input(3)).toHaveAttribute('type', 'password')
    await expect(input(3)).toHaveValue('test-only-replacement')
    await show().click()
    await expect(input(3)).toHaveValue('test-only-replacement')
    await expect(input(2)).toHaveValue('')
    await expect(input(4)).toHaveValue('nacos.example.test:8848')
    await form.getByRole('button', { name: zh ? '保存 CD 配置' : 'Save CD settings', exact: true }).click()
    await expect(page.getByRole('tab', { name: zh ? '概览' : 'Overview', exact: true })).toHaveAttribute('aria-selected', 'true')
    await settings.click()
    await expect(input(3)).toHaveValue('')
    await expect(input(3)).toHaveAttribute('type', 'password')
    expect(writes).toHaveLength(1)
    expect(writes[0].environment).toEqual([{ name: 'EMPTY', has_value: true, value: 'nonempty' }, { name: 'NACOS_DATA_ID', has_value: true, value: '' }, { name: 'NACOS_PASSWORD', has_value: true, value: 'test-only-replacement' }, { name: 'NACOS_SERVER_ADDR', has_value: true }])
    await show().click()
    await expect(input(3)).toHaveValue('test-only-replacement')
    await expect(input(1)).toHaveValue('nonempty')
    await expect(input(2)).toHaveValue('')
    await hide().click()
    expect(await page.evaluate(() => JSON.stringify({ ...localStorage, ...sessionStorage }).includes('test-only'))).toBe(false)
    const section = form.locator('section').filter({ has: page.getByRole('heading', { name: zh ? '手动环境变量' : 'Manual environment variables', exact: true }) })
    await section.screenshot({ path: `/private/tmp/suma-environment-values-${language}-${theme}-${width}.png` })
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await page.reload()
    await settings.click()
    await expect(input(3)).toHaveValue('')
    holdRead = true
    releaseRead = undefined
    await show().click()
    await expect(form.getByRole('button', { name: zh ? '读取中…' : 'Loading…', exact: true })).toBeDisabled()
    await expect.poll(() => !!releaseRead).toBe(true)
    await page.getByRole('tab', { name: zh ? '概览' : 'Overview', exact: true }).click()
    holdRead = false
    releaseRead!()
    await settings.click()
    await expect(input(3)).toHaveValue('')
    await expect(show()).toHaveAttribute('aria-pressed', 'false')
    expect(unexpectedWrites).toEqual([])
    expect(errors).toEqual([])
  })
}
