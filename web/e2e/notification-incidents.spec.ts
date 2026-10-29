import { expect, test } from '@playwright/test'

for (const language of ['zh-CN', 'en-US']) for (const theme of ['dark', 'light']) for (const width of [390, 1440]) {
 test(`notification incident evidence ${language} ${theme} ${width}`, async ({ page }) => {
  await page.setViewportSize({ width, height: 900 })
  await page.addInitScript(({ language, theme }) => {
   sessionStorage.setItem('suma-demo-session', '1')
   localStorage.setItem('suma-language', language)
   localStorage.setItem('suma-theme', theme)
   if (sessionStorage.getItem('suma-demo-notifications-v2')) return
   const base = { type: 'container.unhealthy', severity: 'warning', time: '2026-10-26T01:00:00Z', node_id: 'local', node_name: 'Production', resource_id: 'a'.repeat(64), task_id: '', message: 'Health check failed. '.repeat(12) }
   sessionStorage.setItem('suma-demo-notifications-v2', JSON.stringify({ serial: 0, channels: [], rules: [], deliveries: [], inbox: { unread: 1, items: [
    { ...base, id: 'ongoing', title: 'Current fault', read: false, occurrences: 1234, last_occurred_at: '2026-10-26T02:00:00Z' },
    { ...base, id: 'resolved', title: 'Previous fault', read: true, occurrences: 8, last_occurred_at: '2026-10-26T01:30:00Z', resolved_at: '2026-10-26T01:45:00Z' },
    { ...base, id: 'legacy', title: 'Historical notification', read: true },
   ] } }))
  }, { language, theme })
  await page.goto('/notifications')
  const ongoing = page.locator('article[data-event-id="ongoing"]')
  const resolved = page.locator('article[data-event-id="resolved"]')
  const legacy = page.locator('article[data-event-id="legacy"]')
  const zh = language === 'zh-CN'
  await expect(page.locator('article')).toHaveCount(3)
  await expect(ongoing).toContainText(zh ? '持续异常' : 'Ongoing fault')
  await expect(ongoing).toContainText(zh ? '累计 1234 次' : '1234 observations')
  await expect(ongoing).toContainText(zh ? '最近发生' : 'Last observed')
  await expect(resolved).toContainText(zh ? '已恢复' : 'Recovered')
  await expect(resolved).toContainText(zh ? '恢复于' : 'Recovered at')
  await expect(legacy).not.toContainText(zh ? '累计' : 'observations')
  await expect(ongoing.getByRole('heading')).toHaveText(zh ? '容器健康检查失败' : 'Container health check failed')
  await expect(ongoing.locator('details')).not.toHaveAttribute('open')
  await expect(ongoing.getByText('Health check failed. '.repeat(12).trim(),{exact:true})).not.toBeVisible()
  await ongoing.getByText(zh?'原始详情':'Original details',{exact:true}).click()
  await expect(ongoing.getByText('Health check failed. '.repeat(12).trim(),{exact:true})).toBeVisible()
  await ongoing.getByRole('button', { name: zh ? '标为已读' : 'Mark read', exact: true }).click()
  await expect(ongoing.getByRole('button')).toHaveCount(0)
  await page.reload()
  await expect(ongoing).toContainText(zh ? '累计 1234 次' : '1234 observations')
  await expect(ongoing.getByRole('button')).toHaveCount(0)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy()
  if (language === 'zh-CN' && theme === 'dark' && width === 1440) await page.screenshot({ path: '/private/tmp/suma-notification-incidents-desktop.png', fullPage: true })
 })
}

test('notification inbox has a coherent empty state', async ({ page }) => {
 await page.addInitScript(() => {
  sessionStorage.setItem('suma-demo-session', '1')
  localStorage.setItem('suma-language', 'en-US')
  sessionStorage.setItem('suma-demo-notifications-v2', JSON.stringify({ serial: 0, channels: [], rules: [], deliveries: [], inbox: { unread: 0, items: [] } }))
 })
 await page.goto('/notifications')
 await expect(page.getByText('Your inbox is empty', { exact: true })).toBeVisible()
 await expect(page.locator('article')).toHaveCount(0)
})

test('inbox bulk read persists across reloads', async ({ page }) => {
 await page.addInitScript(() => {
  sessionStorage.setItem('suma-demo-session', '1')
  localStorage.setItem('suma-language', 'zh-CN')
  if (sessionStorage.getItem('suma-demo-notifications-v2')) return
  const items = Array.from({ length: 3 }, (_, index) => ({ id: `bulk-${index}`, type: 'container.oom', severity: 'error', time: '2026-10-29T04:00:00Z', node_id: 'local', node_name: 'Local', resource_id: `container-${index}`, title: 'Memory limit exceeded', message: 'Memory limit exceeded', task_id: '', read: index === 2 }))
  sessionStorage.setItem('suma-demo-notifications-v2', JSON.stringify({ serial: 0, channels: [], rules: [], deliveries: [], inbox: { unread: 2, items } }))
 })
 await page.goto('/notifications')
 await page.getByRole('button', { name: '全部已读', exact: true }).click()
 await expect(page.getByText('0 条未读', { exact: true })).toBeVisible()
 await page.reload()
 await expect(page.getByRole('button', { name: '全部已读', exact: true })).toBeDisabled()
 await expect(page.getByRole('button', { name: '标为已读', exact: true })).toHaveCount(0)
 await expect(page.locator('article')).toHaveCount(3)
})
