import { expect, test } from '@playwright/test'
import type { ReleaseRequest } from '../src/features/delivery/release-flow'
import { setupReleaseFlow as setup } from './release-flow-fixture'

for (const language of ['zh-CN', 'en-US']) for (const theme of ['dark', 'light']) for (const width of [320, 1440]) {
  test(`unified release preview ${language} ${theme} ${width}`, async ({page}, testInfo) => {
    const state = await setup(page, {language, theme, width, sameCommit: true})
    const zh = language === 'zh-CN'
    await page.getByRole('button', {name: zh ? '发布…' : 'Publish…', exact: true}).click()
    const sheet = page.getByRole('dialog')
    await expect(sheet.getByRole('heading', {name: zh ? '审阅本次发布' : 'Review this release'})).toBeVisible()
    await expect(sheet).toContainText('a'.repeat(40))
    await expect(sheet).toContainText('7'.repeat(64))
    const imageDetails = sheet.locator('details').filter({has: page.getByText(zh ? '完整镜像标识' : 'Full image references', {exact: true})}).first()
    await expect(imageDetails).not.toHaveAttribute('open', '')
    await imageDetails.locator('summary').click()
    await expect(imageDetails.getByText('example/app@sha256:' + '7'.repeat(64), {exact: false})).toBeVisible()
    await imageDetails.locator('summary').click()
    await expect(sheet).toContainText('API_TOKEN')
    expect(state.writes.some(write => write.path.endsWith('/sync'))).toBe(false)
    expect(state.confirmations).toBe(0)
    await expect(sheet.getByRole('button', {name: zh ? '发布到 2 个节点' : 'Publish to 2 nodes'})).toBeEnabled()
    expect(await sheet.evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
    await page.screenshot({path: testInfo.outputPath('release-preview.png'), fullPage: true})
    await sheet.getByRole('button', {name: zh ? '发布到 2 个节点' : 'Publish to 2 nodes'}).click()
    await expect(sheet.getByRole('heading', {name: zh ? '本次发布结果' : 'Release result'})).toBeVisible()
    expect(state.writes.find(write => write.path.endsWith('/confirm'))?.body).toEqual({node_ids: ['local', 'other'], force_recreate: false})
    expect(state.errors).toEqual([])
  })
}

test('failed publication retries only failed nodes and preserves source identity', async ({page}) => {
  const state = await setup(page, {fail: true})
  await page.getByRole('button', {name: '发布…', exact: true}).click()
  const sheet = page.getByRole('dialog')
  await sheet.getByRole('button', {name: '发布到 2 个节点', exact: true}).click()
  await sheet.getByRole('button', {name: '重试失败节点…', exact: true}).click()
  await expect(sheet).toContainText('原配置和固定镜像')
  await expect(sheet.getByRole('checkbox')).toHaveCount(1)
  await sheet.getByRole('button', {name: '重试 1 个失败节点', exact: true}).click()
  expect(state.writes.filter(write => write.path.endsWith('/release-plans'))[1].body).toEqual({operation: 'retry', source_record_id: 185} satisfies ReleaseRequest)
  expect(state.writes.filter(write => write.path.endsWith('/confirm'))[1].body.node_ids).toEqual(['other'])
  expect(state.errors).toEqual([])
})

test('background publishing reopens the same execution', async ({page}) => {
  const state = await setup(page, {running: true})
  await page.getByRole('button', {name: '发布…', exact: true}).click()
  await page.getByRole('dialog').getByRole('button', {name: '发布到 2 个节点'}).click()
  await page.getByRole('dialog').getByRole('button', {name: '收起到后台', exact: true}).click()
  await page.getByRole('button', {name: '查看发布进度', exact: true}).click()
  expect(state.confirmations).toBe(1)
  state.finish()
  await expect(page.getByRole('dialog').getByRole('heading', {name: '本次发布结果'})).toBeVisible()
})

test('unchanged release requires explicit recreation', async ({page}) => {
  const state = await setup(page, {changed: false})
  await page.getByRole('button', {name: '发布…', exact: true}).click()
  const sheet = page.getByRole('dialog'), confirm = sheet.getByRole('button', {name: '发布到 2 个节点'})
  await expect(confirm).toBeDisabled()
  await sheet.getByRole('checkbox', {name: '即使无变化也重建所选节点'}).check()
  await confirm.click()
  expect(state.writes.find(write => write.path.endsWith('/confirm'))?.body.force_recreate).toBe(true)
})

test('expired or changed plans stay reviewable and require preparation again', async ({page}) => {
  const state = await setup(page, {conflict: true})
  await page.getByRole('button', {name: '发布…', exact: true}).click()
  const sheet = page.getByRole('dialog')
  await sheet.getByRole('button', {name: '发布到 2 个节点'}).click()
  await expect(sheet).toContainText('Configuration changed; prepare again')
  await expect(sheet).toContainText('7'.repeat(64))
  await sheet.getByRole('button', {name: '重新准备', exact: true}).click()
  await expect.poll(() => state.preparations).toBe(2)
  await expect(sheet.getByRole('button', {name: '发布到 2 个节点'})).toBeEnabled()
})

test('expired preview cannot be confirmed', async ({page}) => {
  const state = await setup(page, {expired: true})
  await page.getByRole('button', {name: '发布…', exact: true}).click()
  const sheet = page.getByRole('dialog')
  await expect(sheet).toContainText('方案已过期')
  await expect(sheet.getByRole('button', {name: '发布到 2 个节点'})).toBeDisabled()
  expect(state.confirmations).toBe(0)
})

test('publishing a reviewed subset leaves other targets unselected', async ({page}) => {
  const state = await setup(page)
  await page.getByRole('button', {name: '发布…', exact: true}).click()
  const sheet = page.getByRole('dialog')
  await sheet.getByRole('checkbox', {name: /Local/}).uncheck()
  await sheet.getByRole('button', {name: '发布到 1 个节点'}).click()
  expect(state.writes.find(write => write.path.endsWith('/confirm'))?.body.node_ids).toEqual(['other'])
})
