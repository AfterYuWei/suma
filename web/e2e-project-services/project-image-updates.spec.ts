import { expect, test, type Page } from '@playwright/test'
const digest = `sha256:${'b'.repeat(64)}`
async function setup(page: Page, language: string, theme: string) {
 const state = { writes: [] as string[], mode: 'inherit', cron: '0 */6 * * *', timezone: 'Asia/Shanghai', version: 0, status: 'pending', drift: false }
 const project = { name: 'alpha', backend: 'compose', native_name: 'alpha', managed: true, source: 'managed', node_id: 'local', status: 'running', service_count: 1, instance_count: 1, declared_service_names: ['web'], capabilities: ['view', 'edit', 'deploy'], created_at: '2026-10-29T00:00:00Z', updated_at: '2026-10-29T00:00:00Z' }
 const candidate = () => ({ id: 'candidate-1', node_id: 'local', project: 'alpha', status: state.status, services: [{ name: 'web', reference: 'docker.io/team/app:latest', manifest_digest: digest, config_digest: digest, replicas: 2 }], requires_docker_socket: false })
 const errors: string[] = []; page.on('pageerror', error => errors.push(error.message))
 await page.addInitScript(({ language, theme }) => { localStorage.setItem('suma-language', language); localStorage.setItem('suma-theme', theme); localStorage.setItem('suma-node', 'local') }, { language, theme })
 await page.route('**/api/v1/**', async route => {
  const request = route.request(), path = new URL(request.url()).pathname.replace('/api/v1', '')
  if (request.method() !== 'GET') state.writes.push(path)
  let data: unknown = []
  if (path === '/auth/status') data = { needs_setup: false }
  else if (path === '/auth/session') data = { id: 1, username: 'admin' }
  else if (path === '/settings') data = { 'general.timezone': 'Asia/Shanghai' }
  else if (path === '/nodes') data = [{ id: 'local', name: 'Local', enabled: true, status: 'online', connection_type: 'unix', group_ids: [] }]
  else if (path === '/notifications/inbox') data = { items: [], unread: 0 }
  else if (path.endsWith('/image-updates/projects')) data = [{ project: 'alpha', updated_services: 1, blocked_services: 0, candidate: candidate() }]
  else if (path.endsWith('/image-updates/projects/alpha/policy')) {
   if (request.method() === 'PUT') { const body = request.postDataJSON(); if (Object.keys(body).some(key => !['expected_version', 'mode', 'cron', 'timezone'].includes(key))) return route.fulfill({ status: 400, json: { code: 1, message: 'Unknown policy field', data: null } }); Object.assign(state, { mode: body.mode, cron: body.cron, timezone: body.timezone, version: state.version + 1 }) }
   data = { project: 'alpha', version: state.version, mode: state.mode, cron: state.cron, timezone: state.timezone }
  } else if (path.endsWith('/image-updates/schedule/preview')) { if (request.postDataJSON().timezone === 'Asia/Invalid') return route.fulfill({ status: 422, json: { code: 1, message: 'Invalid schedule timezone', data: null } }); data = Array.from({ length: 5 }, (_, index) => `2026-10-30T01:30:0${index}Z`) }
  else if (path.endsWith('/publish')) { state.status = state.drift ? 'superseded' : 'publishing'; data = { id: 'publish-task', status: 'running' } }
  else if (path.endsWith('/image-updates')) data = { results: [], candidate: candidate() }
  else if (path.endsWith('/image-updates/policy')) data = { version: 0, enabled: false, interval_hours: 6, registry_credentials: {} }
  else if (path.endsWith('/projects')) data = [project]
  return route.fulfill({ json: { code: 0, message: '', data } })
 })
 await page.goto('/projects')
 return { state, errors }
}
for (const language of ['zh-CN', 'en-US']) for (const theme of ['dark', 'light']) for (const width of [390, 1440]) {
 test(`project image scheduling and publish ${language} ${theme} ${width}`, async ({ page }) => {
  await page.setViewportSize({ width, height: 1000 })
  const { state, errors } = await setup(page, language, theme), zh = language === 'zh-CN'
  const opener = page.getByRole('button', { name: `${zh ? '项目镜像更新' : 'Project image updates'} · alpha` })
  await expect(opener).toContainText(zh ? '有更新' : 'Updates'); await opener.click()
  const dialog = page.getByRole('dialog', { name: `alpha · ${zh ? '镜像更新' : 'Image updates'}`, exact: true })
  await dialog.locator('summary').filter({ hasText: zh ? '定时检测' : 'Scheduled checks' }).click()
  const trigger = dialog.getByRole('combobox', { name: zh ? '项目检测策略' : 'Project check policy' })
  await trigger.click()
  const menuBox = await page.getByRole('listbox').boundingBox(), triggerBox = await trigger.boundingBox()
  expect(menuBox!.x).toBeGreaterThanOrEqual(0); expect(menuBox!.x + menuBox!.width).toBeLessThanOrEqual(width)
  await expect.poll(async () => (await page.getByRole('listbox').boundingBox())!.y).toBeGreaterThanOrEqual(triggerBox!.y + triggerBox!.height - 1)
  await page.getByRole('option', { name: zh ? '自定义 Cron' : 'Custom Cron' }).click()
  await dialog.getByLabel('Cron ·', { exact: false }).fill('30 9 * * 1-5')
  await expect(dialog.getByText(zh ? '未来五次（所选调度时区）' : 'Next five (schedule timezone)')).toBeVisible()
  await dialog.getByLabel(zh ? 'IANA 时区' : 'IANA timezone', { exact: true }).fill('Asia/Invalid')
  await expect(dialog.getByRole('alert')).toContainText('Invalid schedule timezone')
  await dialog.getByLabel(zh ? 'IANA 时区' : 'IANA timezone', { exact: true }).fill('Asia/Shanghai')
  await expect(dialog.getByRole('alert')).toHaveCount(0)
  await dialog.getByRole('button', { name: zh ? '保存策略' : 'Save policy', exact: true }).click()
  await expect.poll(() => state.cron).toBe('30 9 * * 1-5')
  await dialog.getByRole('button', { name: zh ? '发布' : 'Publish', exact: true }).click()
  const confirm = page.getByRole('dialog', { name: zh ? '发布 alpha？' : 'Publish alpha?', exact: true })
  await expect(confirm).toContainText(digest)
  await confirm.getByRole('button', { name: zh ? '发布' : 'Publish', exact: true }).click()
  await expect(dialog).toContainText(zh ? '发布中' : 'Publishing')
  expect(state.writes.filter(path => path.endsWith('/publish'))).toHaveLength(1)
  expect(state.writes.some(path => path.includes('/notifications/'))).toBe(false)
  expect(await dialog.evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true)
  expect(errors).toEqual([])
  await dialog.screenshot({ path: `/tmp/suma-project-images-${language}-${theme}-${width}.png` })
 })
}
test('a superseded version cannot be published from the old candidate', async ({ page }) => {
 const { state } = await setup(page, 'zh-CN', 'dark'); state.drift = true
 await page.getByRole('button', { name: '项目镜像更新 · alpha' }).click()
 await page.getByRole('dialog').getByRole('button', { name: '发布', exact: true }).click()
 await page.getByRole('dialog', { name: '发布 alpha？', exact: true }).getByRole('button', { name: '发布', exact: true }).click()
 await expect(page.getByRole('dialog', { name: 'alpha · 镜像更新', exact: true })).toContainText('版本已变化，请重新检测')
 await expect(page.getByRole('dialog', { name: 'alpha · 镜像更新', exact: true }).getByRole('button', { name: '发布', exact: true })).toHaveCount(0)
 expect(state.writes.filter(path => path.endsWith('/publish'))).toHaveLength(1)
})
