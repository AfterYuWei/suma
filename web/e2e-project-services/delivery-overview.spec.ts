import { expect, test } from '@playwright/test'
import { setupReleaseFlow } from './release-flow-fixture'

for (const language of ['zh-CN', 'en-US']) for (const theme of ['dark', 'light']) for (const width of [390, 1440]) {
  test(`runtime and publication outcomes remain distinct ${language} ${theme} ${width}`, async ({page}, testInfo) => {
    const zh = language === 'zh-CN'
    const reason = `invalid bind source /Data/Docker/${'configuration-directory-'.repeat(18)}application.yml`
    const state = await setupReleaseFlow(page, {language, theme, width, fail: true, failureReason: reason})
    await expect(page.getByRole('heading', {name: zh ? '当前运行' : 'Current runtime', exact: true})).toBeVisible()
    await expect(page.getByText(zh ? '容器运行 · 1 项健康检查' : 'Containers running · 1 health checks', {exact: true})).toHaveCount(2)
    await page.getByRole('button', {name: zh ? '发布…' : 'Publish…', exact: true}).click()
    const sheet = page.getByRole('dialog')
    await sheet.getByRole('button', {name: zh ? '发布到 2 个节点' : 'Publish to 2 nodes'}).click()
    await expect(sheet).toContainText(reason)
    await sheet.getByRole('button', {name: zh ? '关闭' : 'Close', exact: true}).first().click()
    await expect(page.getByText(reason, {exact: true}).first()).toBeVisible()
    await expect(page.getByRole('button', {name: zh ? '重试失败节点（1）…' : 'Retry failed nodes (1)…', exact: true})).toBeVisible()
    expect(state.records.find(record => record.id === 185)?.status).toBe('partial_failed')
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true)
    await page.screenshot({path: testInfo.outputPath('publication-failure-reason.png'), fullPage: true})
    expect(state.errors).toEqual([])
  })
}
