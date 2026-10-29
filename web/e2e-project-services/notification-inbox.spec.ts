import { expect, test, type Page } from '@playwright/test'
import type { Inbox, NotificationEvent } from '../src/features/notifications/types'

async function openInbox(page: Page, language = 'en-US', theme = 'dark', width = 1440) {
  await page.setViewportSize({ width, height: 900 })
  await page.addInitScript(({ language, theme }) => {
    localStorage.setItem('suma-language', language)
    localStorage.setItem('suma-theme', theme)
    localStorage.setItem('suma-node', 'local')
  }, { language, theme })
  const items: NotificationEvent[] = Array.from({ length: 3 }, (_, index) => ({ id: `inbox-${index}`, type: 'container.oom', severity: 'error', time: '2026-10-29T04:00:00Z', node_id: 'local', node_name: 'Local', resource_id: `container-${index}`, title: `Notification ${index}`, message: 'Memory limit exceeded', task_id: '', read: index === 2 }))
  const state = { inbox: { items, unread: 321 } satisfies Inbox, writes: [] as string[], blocked: false, fail: false }
  let release: (() => void) | undefined
  await page.route('**/api/v1/**', async route => {
    const request = route.request(), path = new URL(request.url()).pathname.replace('/api/v1', '')
    let data: unknown = []
    if (path === '/auth/status') data = { needs_setup: false }
    else if (path === '/auth/session') data = { id: 1, username: 'admin', has_avatar: false }
    else if (path === '/settings') data = { 'general.timezone': 'Asia/Shanghai' }
    else if (path === '/nodes') data = [{ id: 'local', name: 'Local', enabled: true, connection_type: 'unix', status: 'online', group_ids: [] }]
    else if (path === '/notifications/inbox') data = state.inbox
    if (request.method() === 'POST') {
      state.writes.push(path)
      if (path === '/notifications/inbox/read-all') {
        if (state.blocked) await new Promise<void>(resolve => { release = resolve })
        if (state.fail) return route.fulfill({ status: 500, json: { code: 20801, message: 'Unable to mark notifications as read', data: null } })
        state.inbox.items.forEach(item => { item.read = true })
        state.inbox.unread = 0
        data = { read: true }
      }
    }
    await route.fulfill({ json: { code: 0, message: '', data } })
  })
  await page.goto('/notifications')
  return { state, finish: () => { state.blocked = false; release?.() } }
}

for (const language of ['zh-CN', 'en-US']) for (const theme of ['dark', 'light']) for (const width of [390, 1440]) {
  test(`bulk inbox read ${language} ${theme} ${width}`, async ({ page }) => {
    const { state } = await openInbox(page, language, theme, width), zh = language === 'zh-CN'
    const all = page.getByRole('button', { name: zh ? '全部已读' : 'Mark all read', exact: true })
    const bell = page.getByRole('button', { name: zh ? '站内消息' : 'Inbox', exact: true })
    await expect(bell).toContainText('321')
    await expect(page.getByRole('button', { name: zh ? '标为已读' : 'Mark read', exact: true })).toHaveCount(2)
    await all.click()
    await expect(all).toBeDisabled()
    await expect(page.getByText(zh ? '0 条未读' : '0 unread', { exact: true })).toBeVisible()
    await expect(bell).toHaveText('')
    await expect(page.getByRole('button', { name: zh ? '标为已读' : 'Mark read', exact: true })).toHaveCount(0)
    await expect(page.locator('article')).toHaveCount(3)
    expect(state.writes).toEqual(['/notifications/inbox/read-all'])
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await page.screenshot({ path: `/private/tmp/suma-inbox-read-all-${language}-${theme}-${width}.png` })
    state.inbox.items.unshift({ ...state.inbox.items[0], id: 'later-event', read: false })
    state.inbox.unread = 1
    await page.reload()
    await expect(all).toBeEnabled()
    await expect(bell).toContainText('1')
    await expect(page.locator('article[data-event-id="later-event"]').getByRole('button', { name: zh ? '标为已读' : 'Mark read', exact: true })).toBeVisible()
  })
}

test('bulk read disables concurrent actions and allows retry after failure', async ({ page }) => {
  const { state, finish } = await openInbox(page)
  state.blocked = true
  state.fail = true
  const all = page.getByRole('button', { name: 'Mark all read', exact: true })
  await all.click()
  await expect(all).toBeDisabled()
  await expect(page.getByRole('button', { name: 'Mark read', exact: true }).first()).toBeDisabled()
  await expect.poll(() => state.writes.length).toBe(1)
  finish()
  await expect(page.getByRole('alert').filter({ hasText: 'Unable to mark notifications as read' })).toBeVisible()
  await expect(page.getByText('321 unread', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Mark read', exact: true })).toHaveCount(2)
  await expect(all).toBeEnabled()
  state.fail = false
  await all.click()
  await expect(page.getByText('0 unread', { exact: true })).toBeVisible()
  await expect(page.getByRole('alert').filter({ hasText: 'Unable to mark notifications as read' })).toHaveCount(0)
})

test('empty inbox has no bulk writes', async ({ page }) => {
  const { state } = await openInbox(page)
  state.inbox = { items: [], unread: 0 }
  await page.reload()
  await expect(page.getByRole('button', { name: 'Mark all read', exact: true })).toBeDisabled()
  await expect(page.getByText('Your inbox is empty', { exact: true })).toBeVisible()
  expect(state.writes).toEqual([])
})
