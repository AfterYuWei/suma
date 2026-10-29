import { expect, test } from '@playwright/test'
import type { CDConfiguration, CDDrift, DeliveryRelease } from '../src/features/delivery/types'

const commit = 'a'.repeat(40)
const created = '2026-10-26T06:00:00Z'
const reason = `Error response from daemon: invalid mount config for type "bind": bind source path does not exist: /Data/Docker/DockerData/eizhu/${'configuration-directory-'.repeat(12)}application.yml`

for (const language of ['zh-CN', 'en-US']) for (const theme of ['light', 'dark']) for (const width of [390, 1440]) {
  test(`delivery failure reason ${language} ${theme} ${width}`, async ({ page }) => {
    const zh = language === 'zh-CN'
    await page.setViewportSize({ width, height: 1000 })
    await page.addInitScript(({ language, theme }) => {
      localStorage.setItem('suma-language', language)
      localStorage.setItem('suma-theme', theme)
      localStorage.setItem('suma-node', 'local')
    }, { language, theme })
    const configuration: CDConfiguration = {
      configured: true, repository: { clone_url: 'https://git.example.test/team/app', ref_type: 'branch', ref: 'main', authentication: { source: 'none' }, compose_files: ['compose.yml'], environment_file: '' },
      reconcile_mode: 'auto', sync_interval_seconds: 300, desired_commit: commit, observed_commit: commit,
      auto_rollback: false, deployment_timeout: 60, webhook_enabled: false, node_ids: ['local'], registry_credential_ids: [],
    }
    const release: DeliveryRelease = {
      id: 1, project_id: 1, repository_url: configuration.repository.clone_url, git_ref: 'main', commit_sha: commit,
      commit_message: 'Deploy application', commit_author: 'operator', config_hash: 'hash', image_references: '[]', status: 'failed',
      trigger_type: 'manual', trigger_actor: 'operator', compose_files: '["compose.yml"]', failure_reason: 'one or more deployment nodes failed',
      created_at: created, updated_at: created, remediation: { retry_failed_node_ids: [], rollback_failed_node_ids: [] },
      deployments: [{ id: 1, node_id: 'local', node_name: 'Aliyun', status: 'failed', failure_reason: reason }],
    }
    const drift: CDDrift = {
      drifted: true, status: 'degraded', desired_commit: commit, observed_commit: commit, active_commit: '',
      reason: 'no release is active', runtime_healthy: false, checked_at: created,
      nodes: [{ node_id: 'local', node_name: 'Aliyun', status: 'degraded', drifted: true, reason: 'no release is active', checked_at: created }],
    }
    const writes: string[] = []
    const errors: string[] = []
    page.on('pageerror', error => errors.push(error.message))
    await page.route('**/api/v1/**', async route => {
      const request = route.request(), path = new URL(request.url()).pathname.replace('/api/v1', '')
      if (request.method() !== 'GET') writes.push(path)
      let data: unknown = []
      if (path === '/auth/status') data = { needs_setup: false }
      else if (path === '/auth/session') data = { id: 1, username: 'admin', has_avatar: false }
      else if (path === '/settings') data = { 'general.timezone': 'Asia/Shanghai' }
      else if (path === '/nodes') data = [{ id: 'local', name: 'Aliyun', enabled: true, connection_type: 'unix', status: 'online', group_ids: [] }]
      else if (path === '/notifications/inbox') data = { items: [], unread: 0 }
      else if (path === '/delivery-projects/eizhu') data = { id: 1, name: 'eizhu', configured: true, node_ids: ['local'], created_at: created, updated_at: created }
      else if (path.endsWith('/configuration')) data = configuration
      else if (path.endsWith('/drift')) data = drift
      else if (path.endsWith('/releases')) data = [release]
      await route.fulfill({ json: { code: 0, message: '', data } })
    })
    await page.goto('/continuous-delivery/eizhu')
    const notice = page.getByRole('alert').first()
    await expect(notice).toContainText(zh ? '交付失败' : 'Delivery failed')
    await expect(notice).toContainText(`Aliyun: ${reason}`)
    await expect(notice).not.toContainText('运行状态与 Git 期望不一致')
    await expect(notice).not.toContainText('one or more deployment nodes failed')
    const description = notice.locator('[data-slot=alert-description]')
    const bounds = await description.boundingBox()
    expect(bounds!.x).toBeGreaterThanOrEqual(0)
    expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(width)
    expect(await description.evaluate(element => element.scrollWidth <= element.clientWidth)).toBe(true)
    await notice.scrollIntoViewIfNeeded()
    await page.screenshot({ path: `/private/tmp/suma-delivery-reason-${language}-${theme}-${width}.png` })

    release.status = 'succeeded'
    release.failure_reason = ''
    release.deployments = []
    Object.assign(drift, { status: 'healthy', drifted: false, runtime_healthy: true, active_release_id: 1, active_commit: commit, reason: '', nodes: [] })
    await page.reload()
    const status = page.getByRole('status').filter({ hasText: zh ? '运行状态与 Git 已对齐' : 'Runtime is aligned with Git' })
    await expect(status).toBeVisible()
    await expect(page.getByRole('alert')).toHaveCount(0)
    const header = status.locator('xpath=..')
    await expect(header).toHaveAttribute('data-slot', 'card-header')
    const title = header.locator('[data-slot=card-title]')
    if (width === 1440) {
      const statusBounds = await status.boundingBox(), titleBounds = await title.boundingBox()
      expect(Math.abs(statusBounds!.y + statusBounds!.height / 2 - titleBounds!.y - titleBounds!.height / 2)).toBeLessThan(2)
    }
    await status.focus()
    await page.keyboard.press('Shift+Tab')
    await page.keyboard.press('Tab')
    await expect(page.getByRole('tooltip')).toContainText(zh ? '当前活动 Release 使用期望 Commit。' : 'The active release uses the desired commit.')
    await page.keyboard.press('Escape')
    await expect(page.getByRole('tooltip')).toHaveCount(0)
    await title.click()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await page.screenshot({ path: `/private/tmp/suma-delivery-compact-${language}-${theme}-${width}.png` })
    expect(writes).toEqual([])
    expect(errors).toEqual([])
  })
}
