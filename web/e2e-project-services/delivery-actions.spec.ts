import { expect, test, type Page } from '@playwright/test'
import type { CDConfiguration, DeliveryRelease } from '../src/features/delivery/types'

const created = '2026-10-27T06:00:00Z'
const commit = 'a'.repeat(40)

async function openReleases(page: Page, language = 'zh-CN', theme = 'light', width = 1440) {
  await page.setViewportSize({ width, height: 1100 })
  await page.addInitScript(({ language, theme }) => {
    localStorage.setItem('suma-language', language)
    localStorage.setItem('suma-theme', theme)
    localStorage.setItem('suma-node', 'local')
  }, { language, theme })
  const configuration: CDConfiguration = {
    automation_enabled: true, configured: true,
    repository: { clone_url: 'https://git.example.test/app.git', ref_type: 'branch', ref: 'main', authentication: { source: 'none' }, compose_files: ['compose.yml'], environment_file: '' },
    reconcile_mode: 'manual', sync_interval_seconds: 300, desired_commit: commit, observed_commit: commit,
    active_release_id: 73, auto_rollback: false, deployment_timeout: 60, webhook_enabled: false, node_ids: ['local', 'other'], registry_credential_ids: [],
  }
  const release = (id: number, status: string, extra: Partial<DeliveryRelease> = {}): DeliveryRelease => ({
    id, project_id: 1, repository_url: configuration.repository.clone_url, git_ref: 'main', commit_sha: commit,
    commit_message: `fix(deploy): release ${id}`, commit_author: 'operator', config_hash: 'hash', image_references: '[]', status,
    trigger_type: 'manual', trigger_actor: 'operator', compose_files: '["compose.yml"]', created_at: created, updated_at: created,
    remediation: { retry_failed_node_ids: [], rollback_failed_node_ids: [] }, ...extra,
  })
  const releases = [
    release(73, 'succeeded', { approved_by: 1, approved_by_name: 'yuwei' }),
    release(72, 'awaiting_approval'),
    release(71, 'failed', { approved_by: 1, approved_by_name: 'yuwei', remediation: { retry_failed_node_ids: ['local'], rollback_failed_node_ids: ['local'] }, deployments: [{ id: 1, node_id: 'local', node_name: 'Aliyun', status: 'failed' }, { id: 2, node_id: 'other', node_name: 'Healthy node', status: 'succeeded' }] }),
    release(70, 'approved', { approved_by: 1, approved_by_name: 'reviewer' }),
    release(69, 'failed'),
    release(68, 'rejected', { failure_reason: 'Rejected by reviewer' }),
    release(67, 'succeeded'),
    release(66, 'partial_failed', { remediation: { retry_failed_node_ids: ['other'], rollback_failed_node_ids: [] } }),
    release(65, 'rollback_failed', { remediation: { retry_failed_node_ids: [], rollback_failed_node_ids: ['local'] } }),
  ]
  const writes: { path: string; method: string }[] = []
  const errors: string[] = []
  let finishWrite: (() => void) | undefined
  let holdWrites = false
  page.on('pageerror', error => errors.push(error.message))
  await page.route('**/api/v1/**', async route => {
    const request = route.request(), path = new URL(request.url()).pathname.replace('/api/v1', '')
    if (request.method() !== 'GET') {
      writes.push({ path, method: request.method() })
      if (holdWrites) await new Promise<void>(resolve => { finishWrite = resolve })
    }
    let data: unknown = []
    if (path === '/auth/status') data = { needs_setup: false }
    else if (path === '/auth/session') data = { id: 1, username: 'admin', has_avatar: false }
    else if (path === '/settings') data = { 'general.timezone': 'Asia/Shanghai' }
    else if (path === '/nodes') data = [{ id: 'local', name: 'Aliyun', enabled: true, connection_type: 'unix', status: 'online', group_ids: [] }]
    else if (path === '/notifications/inbox') data = { items: [], unread: 0 }
    else if (path === '/delivery-projects/app') data = { id: 1, name: 'app', configured: true, automation_enabled: true, node_ids: ['local'], created_at: created, updated_at: created }
    else if (path.endsWith('/configuration')) data = configuration
    else if (path.endsWith('/drift')) data = { drifted: false, status: 'healthy', runtime_healthy: true, active_release_id: 73, active_commit: commit, nodes: [], desired_commit: commit, observed_commit: commit }
    else if (path.endsWith('/releases')) data = releases
    await route.fulfill({ json: { code: 0, message: '', data } })
  })
  await page.goto('/continuous-delivery/app')
  await page.getByRole('tab', { name: language === 'zh-CN' ? 'Release' : 'Releases', exact: true }).click()
  return { configuration, releases, writes, errors, hold: () => { holdWrites = true }, finish: () => { holdWrites = false; finishWrite?.() } }
}

const row = (page: Page, id: number) => page.locator('[data-slot=collapsible]').filter({ has: page.locator('[data-slot=collapsible-trigger]').filter({ hasText: `release ${id}` }) })
const more = (page: Page, id: number, zh = true) => page.getByRole('button', { name: zh ? `Release #${id} 更多操作` : `More actions for release #${id}`, exact: true })

for (const language of ['zh-CN', 'en-US']) for (const theme of ['light', 'dark']) for (const width of [390, 1440]) {
  test(`delivery action hierarchy ${language} ${theme} ${width}`, async ({ page }) => {
    const zh = language === 'zh-CN'
    const state = await openReleases(page, language, theme, width)
    await expect(row(page, 73).getByRole('status')).toContainText('yuwei')
    await expect(row(page, 73).getByRole('group')).toHaveCount(0)
    await expect(row(page, 72).getByRole('button', { name: zh ? '批准' : 'Approve', exact: true })).toBeVisible()
    await expect(row(page, 71).getByRole('button', { name: zh ? '重试失败节点（1）' : 'Retry failed (1)', exact: true })).toBeVisible()
    await expect(row(page, 71).getByRole('button', { name: /重新发布|Redeploy/ })).toHaveCount(0)
    await expect(row(page, 70).getByRole('button', { name: zh ? '发布' : 'Deploy', exact: true })).toBeVisible()
    await expect(row(page, 69).getByRole('button', { name: zh ? '重新发布全部节点' : 'Redeploy all nodes', exact: true })).toBeVisible()
    await expect(row(page, 68).getByRole('status')).toContainText('reviewer')
    await expect(row(page, 68).getByRole('group')).toHaveCount(0)
    await expect(row(page, 67).getByRole('group').getByRole('button')).toHaveCount(1)
    await expect(row(page, 66).getByRole('button', { name: zh ? '重试失败节点（1）' : 'Retry failed (1)', exact: true })).toBeVisible()
    await expect(row(page, 65).getByRole('group').getByRole('button')).toHaveCount(1)

    const trigger = more(page, 71, zh)
    await trigger.click()
    const menu = page.getByRole('menu')
    await expect(menu.getByRole('menuitem')).toHaveCount(2)
    await expect(menu).toContainText(zh ? '重新发布全部节点' : 'Redeploy all nodes')
    await expect(menu).toContainText(zh ? '回滚失败节点（1）' : 'Roll back failed (1)')
    await expect.poll(async () => {
      const bounds = (await menu.boundingBox())!, anchor = (await trigger.boundingBox())!
      return bounds.x >= 0 && bounds.x + bounds.width <= width && (bounds.y >= anchor.y + anchor.height || bounds.y + bounds.height <= anchor.y)
    }).toBe(true)
    expect(await menu.evaluate(element => element.scrollWidth <= element.clientWidth)).toBe(true)
    await expect(row(page, 71).locator('[data-slot=collapsible-trigger]')).toHaveAttribute('aria-expanded', 'false')
    await page.screenshot({ path: `/private/tmp/suma-delivery-actions-menu-${language}-${theme}-${width}.png`, animations: 'disabled' })
    await page.keyboard.press('Escape')
    await expect(trigger).toBeFocused()
    await expect(menu).toHaveCount(0)

    await page.getByRole('button', { name: zh ? '项目更多操作' : 'More project actions', exact: true }).click()
    await expect(menu.getByRole('menuitem')).toHaveCount(2)
    await expect(menu).toContainText(zh ? '保留运行中的容器' : 'keep running containers')
    await expect(menu).toContainText(zh ? '默认删除命名卷和数据' : 'data are deleted by default')
    await page.keyboard.press('Escape')
    await expect(menu).toHaveCount(0)
    await page.screenshot({ path: `/private/tmp/suma-delivery-actions-${language}-${theme}-${width}.png`, animations: 'disabled' })
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    expect(state.writes).toEqual([])
    expect(state.errors).toEqual([])
  })
}

test('delivery actions confirm scope, cancel safely, and lock while pending', async ({ page }) => {
  const state = await openReleases(page)
  const dialog = page.getByRole('dialog')
  await row(page, 71).getByRole('button', { name: '重试失败节点（1）', exact: true }).click()
  await expect(dialog).toContainText('只重试：Aliyun')
  await expect(dialog).not.toContainText('Healthy node')
  expect(state.writes).toEqual([])
  await dialog.getByRole('button', { name: '取消', exact: true }).click()
  expect(state.writes).toEqual([])

  await more(page, 71).click()
  await page.getByRole('menuitem').filter({ hasText: '重新发布全部节点' }).click()
  await expect(dialog).toContainText('包括已成功的节点')
  await expect(dialog).toContainText('按保存的自动回滚策略')
  await dialog.getByRole('button', { name: '重新发布全部节点', exact: true }).click()
  await expect.poll(() => state.writes).toEqual([{ path: '/delivery-projects/app/releases/71/deploy', method: 'POST' }])
  const feedback = page.getByRole('status').filter({ hasText: '交付任务已启动。' })
  await expect(feedback).toBeVisible()
  const feedbackBounds = await feedback.boundingBox()
  const tabsBounds = await page.getByRole('tablist').boundingBox()
  expect(Math.abs(feedbackBounds!.y + feedbackBounds!.height / 2 - tabsBounds!.y - tabsBounds!.height / 2)).toBeLessThan(3)
  await page.getByRole('button', { name: '关闭操作提示' }).click()
  await expect(feedback).toHaveCount(0)

  state.hold()
  await row(page, 71).getByRole('button', { name: '重试失败节点（1）', exact: true }).click()
  await dialog.getByRole('button', { name: '重试失败节点', exact: true }).click()
  await expect(row(page, 72).getByRole('button', { name: '批准', exact: true })).toBeDisabled()
  await expect(more(page, 71)).toBeDisabled()
  await expect(row(page, 71).getByRole('button', { name: /重试失败节点/ }).locator('[data-slot=spinner]')).toHaveCount(1)
  await expect(page.getByRole('button', { name: '同步 Git', exact: true })).toBeDisabled()
  state.finish()
  await expect(row(page, 72).getByRole('button', { name: '批准', exact: true })).toBeEnabled()
  expect(state.writes.at(-1)).toEqual({ path: '/delivery-projects/app/releases/71/remediations/retry-failed', method: 'POST' })

  await more(page, 71).click()
  await page.getByRole('menuitem').filter({ hasText: '回滚失败节点' }).click()
  await expect(dialog).toContainText('仅处理：Aliyun')
  await dialog.getByRole('button', { name: '回滚失败节点', exact: true }).click()
  await expect.poll(() => state.writes.at(-1)?.path).toBe('/delivery-projects/app/releases/71/remediations/rollback-failed')

  await more(page, 72).click()
  await page.getByRole('menuitem').filter({ hasText: '拒绝此版本' }).click()
  await expect(dialog).toContainText('该候选 Release 将不能再发布')
  state.hold()
  await dialog.getByRole('button', { name: '拒绝', exact: true }).click()
  await expect.poll(() => state.writes.at(-1)?.path).toBe('/delivery-projects/app/releases/72/reject')
  await expect(more(page, 72).locator('[data-slot=spinner]')).toHaveCount(1)
  await expect(row(page, 72).getByRole('button', { name: '批准', exact: true }).locator('[data-slot=spinner]')).toHaveCount(0)
  state.finish()
  await expect(more(page, 72)).toBeEnabled()

  await more(page, 67).click()
  await page.getByRole('menuitem').filter({ hasText: '回滚到此版本' }).click()
  await expect(dialog).toContainText('不会回滚存储卷中的数据')
  await dialog.getByRole('button', { name: '取消', exact: true }).click()
  expect(state.errors).toEqual([])
})

test('project deletion keeps typed confirmation and volume warning', async ({ page }) => {
  const state = await openReleases(page)
  const dialog = page.getByRole('dialog')
  await page.getByRole('button', { name: '项目更多操作', exact: true }).click()
  await page.getByRole('menuitem').filter({ hasText: '强制删除' }).click()
  await expect(dialog).toContainText('永久删除命名存储卷及其中的数据')
  await expect(dialog.getByRole('button', { name: '强制删除', exact: true })).toBeDisabled()
  await dialog.getByRole('textbox').fill('app')
  await expect(dialog.getByRole('button', { name: '强制删除', exact: true })).toBeEnabled()
  await dialog.getByRole('button', { name: '取消', exact: true }).click()
  await page.getByRole('button', { name: '项目更多操作', exact: true }).click()
  await page.getByRole('menuitem').filter({ hasText: '删除项目', hasNotText: '强制删除' }).click()
  await expect(dialog).toContainText('不会停止或删除仍在运行的容器')
  await expect(dialog.getByRole('button', { name: '删除项目', exact: true })).toBeDisabled()
  await dialog.getByRole('button', { name: '取消', exact: true }).click()
  expect(state.writes).toEqual([])
})

test('delivery busy state and observe mode retain appropriate controls', async ({ page }) => {
  const state = await openReleases(page)
  state.releases[0].status = 'deploying'
  await page.reload()
  await page.getByRole('tab', { name: 'Release', exact: true }).click()
  await expect(row(page, 72).getByRole('button', { name: '批准', exact: true })).toBeDisabled()
  await expect(more(page, 71)).toBeDisabled()
  await page.getByRole('button', { name: '项目更多操作', exact: true }).click()
  for (const item of await page.getByRole('menuitem').all()) await expect(item).toHaveAttribute('aria-disabled', 'true')
  await page.keyboard.press('Escape')

  state.releases[0].status = 'succeeded'
  state.configuration.reconcile_mode = 'observe'
  state.releases[1].commit_message = 'LongCommitWithoutSpaces'.repeat(20)
  state.releases[0].approved_by_name = 'very-long-approval-name-'.repeat(18)
  await page.setViewportSize({ width: 320, height: 1100 })
  await page.reload()
  await page.getByRole('tab', { name: 'Release', exact: true }).click()
  await expect(page.getByRole('button', { name: '批准', exact: true })).toHaveCount(0)
  await expect(page.getByRole('button', { name: '发布', exact: true })).toHaveCount(0)
  await expect(more(page, 67)).toHaveCount(0)
  await expect(row(page, 73).getByRole('status')).toContainText(state.releases[0].approved_by_name!)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  expect(state.writes).toEqual([])
  expect(state.errors).toEqual([])
})
