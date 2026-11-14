import { expect, test } from '@playwright/test'
import { setupReleaseFlow } from './release-flow-fixture'

// Publication-owned actions replace the obsolete Release-row menus.
for (const language of ['zh-CN', 'en-US']) for (const theme of ['dark', 'light']) for (const width of [390, 1440]) {
  test(`restore a successful publication ${language} ${theme} ${width}`, async ({page}, testInfo) => {
    const state = await setupReleaseFlow(page, {language, theme, width})
    const zh = language === 'zh-CN'
    await page.getByRole('tab', {name: zh ? '发布记录' : 'Publications', exact: true}).click()
    const record = page.locator('details').filter({has: page.locator('summary').filter({hasText: '#184'})}).first()
    await record.locator('summary').first().click()
    await record.getByRole('button', {name: zh ? '恢复到此版本…' : 'Restore this version…', exact: true}).click()
    const sheet = page.getByRole('dialog')
    await expect(sheet).toContainText(zh ? '保留数据卷，数据库内容不会回退' : 'Volumes remain; database contents are not restored')
    await expect(sheet).toContainText('6'.repeat(64))
    await expect(sheet.getByRole('button', {name: zh ? '确认恢复' : 'Restore version', exact: true})).toBeEnabled()
    expect(state.writes.find(write => write.path.endsWith('/release-plans'))?.body).toEqual({operation: 'restore', source_record_id: 184})
    await page.screenshot({path: testInfo.outputPath('restore-publication.png'), fullPage: true})
    await sheet.getByRole('button', {name: zh ? '确认恢复' : 'Restore version', exact: true}).click()
    await expect(sheet.getByRole('heading', {name: zh ? '本次发布结果' : 'Release result'})).toBeVisible()
    expect(state.errors).toEqual([])
  })
}

test('legacy configuration reapplication explains historical image limitations', async ({page}) => {
  const state = await setupReleaseFlow(page, {legacy: true})
  await page.getByRole('tab', {name: '发布记录', exact: true}).click()
  const record = page.locator('details').filter({has: page.locator('summary').filter({hasText: '#184'})}).first()
  await record.locator('summary').first().click()
  await expect(record.getByRole('button', {name: '恢复到此版本…', exact: true})).toHaveCount(0)
  await record.getByRole('button', {name: '重新应用历史配置…', exact: true}).click()
  await expect(page.getByRole('dialog')).toContainText('不能保证恢复原版本')
  expect(state.writes.find(write => write.path.endsWith('/release-plans'))?.body).toEqual({operation: 'reapply', source_record_id: 184})
})

test('failed-node recovery stays scoped to the failed publication', async ({page}) => {
  const state = await setupReleaseFlow(page, {fail: true})
  await page.getByRole('button', {name: '发布…', exact: true}).click()
  await page.getByRole('dialog').getByRole('button', {name: '发布到 2 个节点'}).click()
  await expect(page.getByRole('dialog').getByRole('heading', {name: '本次发布结果'})).toBeVisible()
  await page.getByRole('dialog').getByRole('button', {name: '关闭', exact: true}).click()
  await page.getByRole('button', {name: '恢复失败节点…', exact: true}).click()
  const sheet = page.getByRole('dialog')
  await expect(sheet.getByRole('checkbox')).toHaveCount(1)
  await expect(sheet).toContainText('Other')
  expect(state.writes.filter(write => write.path.endsWith('/release-plans'))[1].body).toEqual({operation: 'restore-failed', source_record_id: 185})
})
