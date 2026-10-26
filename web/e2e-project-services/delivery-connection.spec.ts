import { expect, test } from '@playwright/test'
import type { GitConnectionInput } from '../src/features/delivery/types'

test('connection request uses draft username, hides stale results and preserves configuration', async ({ page }) => {
  await page.addInitScript(() => {
    localStorage.setItem('suma-language', 'en-US')
    localStorage.setItem('suma-theme', 'dark')
    localStorage.setItem('suma-node', 'local')
  })
  const writes: { path: string; body: GitConnectionInput }[] = []
  let finish: (() => void) | undefined
  let failure = false
  await page.route('**/api/v1/**', async route => {
    const request = route.request()
    const path = new URL(request.url()).pathname.replace('/api/v1', '')
    let data: unknown = []
    if (path === '/auth/status') data = { needs_setup: false }
    else if (path === '/auth/session') data = { id: 1, username: 'admin', has_avatar: false }
    else if (path === '/settings') data = { 'general.timezone': 'Asia/Shanghai' }
    else if (path === '/nodes') data = [{ id: 'local', name: 'local', enabled: true, connection_type: 'unix', status: 'online', group_ids: [] }]
    else if (path === '/notifications/inbox') data = { items: [], unread: 0 }
    if (request.method() !== 'GET') {
      writes.push({ path, body: request.postDataJSON() })
      if (path === '/delivery-projects/test-connection') {
        if (writes.length === 1) await new Promise<void>(resolve => { finish = resolve })
        if (failure) {
          await route.fulfill({ status: 422, json: { code: 17715, message: `git ls-remote failed: repository not found (404) https://git.example.test/${'long-repository-path'.repeat(20)}`, data: null } })
          return
        }
        data = { ref_checked: true, commit_sha: 'a'.repeat(40) }
      }
    }
    await route.fulfill({ json: { code: 0, message: 'success', data } })
  })
  await page.goto('/continuous-delivery/new/project')
  const url = page.getByLabel('Git Clone URL', { exact: true })
  await url.fill('https://git.example.test/team/repo')
  await page.getByRole('combobox', { name: 'Authentication source', exact: true }).click()
  await page.getByRole('option', { name: 'Use a project credential', exact: true }).click()
  await page.getByLabel('Name', { exact: true }).fill('draft')
  const username = page.getByLabel('Username (optional)', { exact: true })
  await username.fill('my-explicit-username')
  await page.getByLabel('Token', { exact: true }).fill('test-only-secret')
  await page.getByRole('button', { name: 'Test connection', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Testing…', exact: true })).toBeDisabled()
  await expect.poll(() => writes.length).toBe(1)
  expect(writes[0].body.repository.authentication.credential?.username).toBe('my-explicit-username')
  expect(writes[0].body.repository.authentication.credential?.secret).toBe('test-only-secret')
  await username.fill('changed-while-pending')
  finish!()
  await expect(page.getByRole('button', { name: 'Test connection', exact: true })).toBeEnabled()
  await expect(page.getByText('Connection successful', { exact: true })).toHaveCount(0)
  failure = true
  await page.getByRole('button', { name: 'Test connection', exact: true }).click()
  await expect(page.getByRole('alert').filter({ hasText: 'Connection failed' })).toContainText('404')
  await page.setViewportSize({ width: 320, height: 900 })
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await username.fill('')
  await expect(page.getByText('Connection failed', { exact: true })).toHaveCount(0)
  failure = false
  await page.getByRole('button', { name: 'Test connection', exact: true }).click()
  await expect(page.getByText('Connection successful', { exact: true })).toBeVisible()
  expect(writes[2].body.repository.authentication.credential?.username).toBe('')
  expect(writes.every(write => write.path === '/delivery-projects/test-connection')).toBe(true)
  await expect(page.getByLabel('Token', { exact: true })).toHaveValue('test-only-secret')
})
