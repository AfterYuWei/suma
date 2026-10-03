import { readFile } from 'node:fs/promises'
import { expect, test } from '@playwright/test'

test.use({ timezoneId: 'America/Los_Angeles' })

for (const language of ['en-US', 'zh-CN']) for (const theme of ['dark', 'light']) for (const width of [390, 1440]) {
  test(`timezone and cleanup ${language} ${theme} ${width}`, async ({ page }) => {
    const zh = language === 'zh-CN'
    await page.addInitScript(({ language, theme }) => {
      sessionStorage.setItem('suma-demo-session', '1')
      localStorage.setItem('suma-language', language)
      localStorage.setItem('suma-theme', theme)
      localStorage.setItem('suma-node', 'local')
    }, { language, theme })
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/settings')
    const zone = page.getByLabel(zh ? '时区' : 'Timezone', { exact: true })
    await expect(zone).toHaveValue('America/Los_Angeles')
    await expect(page.getByText(zh ? '跟随当前设备：America/Los_Angeles' : 'Following this device: America/Los_Angeles', { exact: true })).toBeVisible()
    await zone.fill('Not/AZone')
    await expect(page.getByRole('button', { name: zh ? '保存更改' : 'Save changes', exact: true })).toBeDisabled()
    await expect(page.getByRole('alert')).toContainText(zh ? '有效的 IANA 时区' : 'valid IANA timezone')
    await zone.fill('Asia/Kolkata')
    await page.getByRole('button', { name: zh ? '保存更改' : 'Save changes', exact: true }).click()
    await expect(page.getByText(zh ? '设置已保存' : 'Settings saved', { exact: true })).toBeVisible()
    // A full navigation/reload must retain the saved zone rather than the device zone.
    await page.getByRole('tab', { name: zh ? '存储清理' : 'Storage cleanup', exact: true }).click()
    await expect(page.getByText('Asia/Kolkata', { exact: true }).first()).toBeVisible()
    await page.goto('/tasks')
    const expected = await page.evaluate(async language => {
      // @ts-expect-error Vite serves source imports in the browser.
      const { api } = await import('/src/lib/api.ts')
      const rows = await api('/nodes/local/tasks')
      return new Intl.DateTimeFormat(language, { timeZone: 'Asia/Kolkata', year: 'numeric', month: 'numeric', day: 'numeric', hour: 'numeric', minute: '2-digit', second: '2-digit' }).format(new Date(rows[0].created_at))
    }, language)
    await expect(page.getByRole('cell', { name: expected, exact: true }).first()).toBeVisible()
    await page.goto('/settings#cleanup')
    await page.getByRole('button', { name: zh ? 'homelab-01 操作' : 'homelab-01 actions', exact: true }).click()
    await expect(page.getByRole('menuitem', { name: zh ? '配置／预览／历史' : 'Configure / preview / history', exact: true })).toHaveCount(0)
    await page.getByRole('menuitem', { name: zh ? '管理清理' : 'Manage cleanup', exact: true }).click()
    await expect(page.getByRole('switch', { name: zh ? '开启定时清理' : 'Enable scheduled cleanup', exact: true })).toBeVisible()
    await page.screenshot({ path: `/tmp/suma-timezone-cleanup-${language}-${theme}-${width}.png`, fullPage: true })
    await page.goto('/settings')
    await expect(zone).toHaveValue('Asia/Kolkata')
    await page.getByRole('button', { name: zh ? '使用系统时区' : 'Use system timezone', exact: true }).click()
    await page.getByRole('button', { name: zh ? '保存更改' : 'Save changes', exact: true }).click()
    await expect(page.getByText(zh ? '设置已保存' : 'Settings saved', { exact: true })).toBeVisible()
    await page.reload()
    await expect(zone).toHaveValue('America/Los_Angeles')
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  })
}

test('custom project log range and export use the saved timezone', async ({ page }) => {
  await page.addInitScript(() => {
    sessionStorage.setItem('suma-demo-session', '1')
    sessionStorage.setItem('suma-demo-timezone', 'Asia/Kolkata')
    localStorage.setItem('suma-language', 'en-US')
    localStorage.setItem('suma-node', 'local')
  })
  await page.goto('/projects/compose/gateway-prod')
  await page.getByRole('tab', { name: 'Logs', exact: true }).click()
  await page.getByRole('combobox', { name: 'Log time range', exact: true }).click()
  await page.getByRole('option', { name: 'Custom range', exact: true }).click()
  await expect(page.getByText('Input times use timezone: Asia/Kolkata', { exact: true })).toBeVisible()
  const wallTime = (date: Date) => {
    const fields = new Intl.DateTimeFormat('en-GB', { timeZone: 'Asia/Kolkata', year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).formatToParts(date)
    const part = (name: string) => fields.find(field => field.type === name)!.value
    return `${part('year')}-${part('month')}-${part('day')}T${part('hour')}:${part('minute')}`
  }
  await page.getByLabel('Start time', { exact: true }).fill(wallTime(new Date(Date.now() - 3600000)))
  await page.getByLabel('End time', { exact: true }).fill(wallTime(new Date(Date.now() + 60000)))
  await page.getByRole('button', { name: 'Query range', exact: true }).click()
  await expect(page.getByTestId('project-log-lines')).toContainText('request')
  const download = page.waitForEvent('download')
  await page.getByRole('button', { name: 'Download filtered logs', exact: true }).click()
  const artifact = await download
  const contents = await readFile((await artifact.path())!, 'utf8')
  expect(contents).toContain('Asia/Kolkata web/gateway-prod-web-1')
})
