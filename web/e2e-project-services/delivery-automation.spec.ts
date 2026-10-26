import { expect, test } from '@playwright/test'
import type { CDConfiguration } from '../src/features/delivery/types'

for (const language of ['zh-CN', 'en-US']) for (const theme of ['light', 'dark']) for (const width of [390, 1440]) {
  test(`delivery automation ${language} ${theme} ${width}`, async ({ page }) => {
    const zh = language === 'zh-CN'
    await page.setViewportSize({ width, height: 1000 })
    await page.addInitScript(({ language, theme }) => {
      localStorage.setItem('suma-language', language)
      localStorage.setItem('suma-theme', theme)
      localStorage.setItem('suma-node', 'local')
    }, { language, theme })
    const configuration: CDConfiguration = {
      automation_enabled: true, configured: true,
      repository: { clone_url: 'https://git.example.test/app.git', ref_type: 'branch', ref: 'main', authentication: { source: 'none' }, compose_files: ['compose.yml'], environment_file: '' },
      reconcile_mode: 'auto', sync_interval_seconds: 300, desired_commit: '', observed_commit: '',
      auto_rollback: false, deployment_timeout: 60, webhook_enabled: true, webhook_id: 'retained-hook', node_ids: ['local'], registry_credential_ids: [],
    }
    const project = { id: 1, name: 'app', configured: true, automation_enabled: true, node_ids: ['local'], created_at: '2026-10-26T06:00:00Z', updated_at: '2026-10-26T06:00:00Z' }
    const writes: { path: string; body: Record<string, unknown> }[] = []
    const errors: string[] = []
    let fail = false
    let releaseToggle: (() => void) | undefined
    let holdToggle = true
    page.on('pageerror', error => errors.push(error.message))
    await page.route('**/api/v1/**', async route => {
      const request = route.request(), path = new URL(request.url()).pathname.replace('/api/v1', '')
      let data: unknown = []
      if (request.method() !== 'GET') writes.push({ path, body: request.postDataJSON() || {} })
      if (path === '/auth/status') data = { needs_setup: false }
      else if (path === '/auth/session') data = { id: 1, username: 'admin', has_avatar: false }
      else if (path === '/settings') data = { 'general.timezone': 'Asia/Shanghai' }
      else if (path === '/nodes') data = [{ id: 'local', name: 'Local', enabled: true, connection_type: 'unix', status: 'online', group_ids: [] }]
      else if (path === '/notifications/inbox') data = { items: [], unread: 0 }
      else if (path === '/delivery-projects') data = [project]
      else if (path === '/delivery-projects/app') data = project
      else if (path.endsWith('/automation')) {
        if (holdToggle) await new Promise<void>(resolve => { releaseToggle = resolve })
        if (fail) {
          await route.fulfill({ status: 503, json: { code: 17717, message: 'Switch save failed' } })
          return
        }
        project.automation_enabled = request.postDataJSON().enabled
        configuration.automation_enabled = project.automation_enabled
        data = project
      } else if (path.endsWith('/configuration')) {
        if (request.method() === 'PUT') Object.assign(configuration, request.postDataJSON())
        data = configuration
      } else if (path.endsWith('/drift')) data = { drifted: false, status: 'unknown', runtime_healthy: false, nodes: [], desired_commit: '', observed_commit: '', active_commit: '' }
      await route.fulfill({ json: { code: 0, message: '', data } })
    })
    const switchName = zh ? 'app 自动交付触发' : 'app automatic delivery triggers'
    await page.goto('/continuous-delivery')
    const toggle = page.getByRole('switch', { name: switchName })
    await expect(toggle).toBeChecked()
    await toggle.focus()
    await toggle.press('Space')
    await expect(toggle).toBeDisabled()
    expect(writes).toHaveLength(1)
    holdToggle = false
    releaseToggle!()
    await expect(toggle).not.toBeChecked()
    await expect(page.getByText(zh ? '已暂停' : 'Paused', { exact: true })).toBeVisible()
    expect(writes).toEqual([{ path: '/delivery-projects/app/automation', body: { enabled: false } }])
    await page.getByRole('link', { name: 'app', exact: true }).click()
    await expect(toggle).not.toBeChecked()
    await expect(page.getByText(zh ? '轮询与 Webhook 已暂停。' : 'Polling and webhooks are paused.', { exact: false })).toBeVisible()
    const sync = page.getByRole('button', { name: zh ? '同步 Git' : 'Sync Git', exact: true })
    await expect(sync).toBeEnabled()
    await sync.click()
    await expect(page.getByText(zh ? '同步任务已启动。' : 'Synchronization task started.')).toBeVisible()
    await page.reload()
    await expect(toggle).not.toBeChecked()
    await page.screenshot({ path: `/private/tmp/suma-delivery-automation-${language}-${theme}-${width}.png` })
    await page.getByRole('tab', { name: zh ? '设置' : 'Settings', exact: true }).click()
    const setting = page.getByRole('switch', { name: zh ? '启用自动交付触发' : 'Enable automatic delivery triggers', exact: true })
    await expect(setting).not.toBeChecked()
    await expect(page.getByRole('radio', { name: zh ? '自动 验证通过后立即发布' : 'Auto Deploy immediately after validation', exact: true })).toBeChecked()
    await setting.click()
    await page.getByRole('button', { name: zh ? '保存 CD 配置' : 'Save CD settings', exact: true }).click()
    await expect(toggle).toBeChecked()
    expect(configuration.reconcile_mode).toBe('auto')
    expect(configuration.webhook_id).toBe('retained-hook')
    expect(writes.map(write => write.path)).toEqual(['/delivery-projects/app/automation', '/delivery-projects/app/sync', '/delivery-projects/app/configuration'])
    fail = true
    await toggle.click()
    await expect(page.getByRole('alert').filter({ hasText: 'Switch save failed' })).toBeVisible()
    await expect(toggle).toBeChecked()
    expect(errors).toEqual([])
  })
}
