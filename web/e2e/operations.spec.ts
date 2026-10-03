import { readFile } from 'node:fs/promises'
import { expect, test, type Page } from '@playwright/test'
async function setup(page: Page, language = 'en-US', theme = 'dark') {
 await page.addInitScript(({ language, theme }) => { sessionStorage.setItem('suma-demo-session', '1'); localStorage.setItem('suma-language', language); localStorage.setItem('suma-theme', theme); localStorage.setItem('suma-node', 'local') }, { language, theme })
}
test('image detection and per-node schedule', async ({ page }) => {
 await setup(page); await page.goto('/images')
 await page.getByRole('button', { name: 'Check image updates', exact: true }).first().click()
 const dialog = page.getByRole('dialog', { name: 'Check image updates', exact: true }); await dialog.getByRole('button', { name: 'Start check', exact: true }).click()
 await expect(dialog.getByText('100%', { exact: false })).toBeVisible(); await dialog.getByRole('button', { name: 'Close', exact: true }).first().click()
 await expect(page.getByText('Update available', { exact: true }).first()).toBeVisible()
 await page.goto('/settings#image-updates'); const enabled = page.getByRole('switch', { name: 'Enable scheduled checks', exact: true }); await expect(enabled).not.toBeChecked(); await enabled.click()
 await page.getByRole('combobox', { name: 'Check interval', exact: true }).click(); await page.getByRole('option', { name: 'Every 1 hours', exact: true }).click()
 await page.getByRole('button', { name: 'Save policy', exact: true }).click(); await expect(page.getByText('Saved', { exact: true })).toBeVisible(); await expect(enabled).toBeChecked(); await expect(page.getByText('Next check:', { exact: false })).toBeVisible()
})
test('aggregate logs preserve pause, filters, historical range and export', async ({ page }) => {
 await setup(page); await page.goto('/projects/compose/gateway-prod'); await page.getByRole('tab', { name: 'Logs', exact: true }).click()
 const lines = page.getByTestId('project-log-lines'); await expect(lines).toContainText('request'); await expect(lines).toContainText('gateway-prod-web-1'); await expect(lines).toContainText('gateway-prod-api-1')
 await page.getByRole('button', { name: 'Pause', exact: true }).click(); const paused = await lines.innerText(); await expect.poll(() => lines.innerText(), { timeout: 1500 }).toBe(paused)
 await page.waitForTimeout(900); expect(await lines.innerText()).toBe(paused); await page.getByRole('button', { name: 'Resume', exact: true }).click(); await expect(lines).toContainText('live request')
 await page.getByRole('textbox', { name: 'Search project logs', exact: true }).fill('retrying'); await expect(lines).toContainText('retrying'); await expect(lines).not.toContainText('status=200')
 const download = page.waitForEvent('download'); await page.getByRole('button', { name: 'Download filtered logs', exact: true }).click(); const artifact = await download; expect(artifact.suggestedFilename()).toBe('gateway-prod.log'); const path = await artifact.path(); expect(path).toBeTruthy(); const exported = await readFile(path!, 'utf8'); expect(exported).toContain('retrying'); expect(exported).toContain('gateway-prod'); expect(exported).not.toContain('status=200')
 await page.getByRole('textbox', { name: 'Search project logs', exact: true }).fill(''); await page.getByRole('combobox', { name: 'Log time range', exact: true }).click(); await page.getByRole('option', { name: 'Last hour', exact: true }).click(); await expect(page.getByText('History', { exact: true })).toBeVisible()
 await page.getByRole('button', { name: 'Services', exact: true }).click(); await page.getByText('web', { exact: true }).last().click(); await page.keyboard.press('Escape'); await expect(lines).toContainText('gateway-prod-web-1'); await expect(lines).not.toContainText('gateway-prod-api-1')
})
for (const width of [390, 1440]) for (const language of ['en-US', 'zh-CN']) for (const theme of ['dark', 'light']) {
 test(`operations responsive ${language} ${theme} ${width}`, async ({ page }) => {
  await setup(page, language, theme); await page.setViewportSize({ width, height: 900 }); await page.goto('/projects/compose/gateway-prod'); await page.getByRole('tab', { name: language === 'zh-CN' ? '日志' : 'Logs', exact: true }).click(); await expect(page.getByTestId('project-log-lines')).toContainText('request'); expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await page.screenshot({ path: `/tmp/suma-operations-${language}-${theme}-${width}.png`, fullPage: true })
 })
}

test('external project custom history, stream filter, clear and node switching', async ({ page }) => {
 await setup(page); await page.goto('/projects/compose/legacy-tools'); await page.getByRole('tab', { name: 'Logs', exact: true }).click()
 const lines = page.getByTestId('project-log-lines'); await expect(lines).toContainText('legacy-tools')
 await page.getByRole('combobox', { name: 'Log stream', exact: true }).click(); await page.getByRole('option', { name: 'stderr', exact: true }).click(); await expect(lines).toContainText('[stderr]'); await expect(lines).not.toContainText('[stdout]')
 await page.getByRole('combobox', { name: 'Log time range', exact: true }).click(); await page.getByRole('option', { name: 'Custom range', exact: true }).click(); await expect(lines).toContainText('Choose start and end times')
 const localTime = (date: Date) => new Date(date.getTime() - date.getTimezoneOffset() * 60000).toISOString().slice(0, 16)
 await page.getByLabel('Start time', { exact: true }).fill(localTime(new Date(Date.now() - 3600000))); await page.getByLabel('End time', { exact: true }).fill(localTime(new Date(Date.now() + 60000)))
 await page.getByRole('button', { name: 'Query range', exact: true }).click(); await expect(lines).toContainText('retrying'); await page.getByRole('button', { name: 'Clear view', exact: true }).click(); await expect(lines).not.toContainText('retrying')
 await page.getByRole('combobox', { name: 'Current Docker node', exact: true }).click(); await page.getByRole('option').filter({ hasText: 'edge-hk' }).click(); await expect(page.getByRole('combobox', { name: 'Current Docker node', exact: true })).toContainText('edge-hk')
})
