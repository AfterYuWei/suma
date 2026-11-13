import { expect, test } from '@playwright/test'
import type { DockerNode } from '../src/lib/nodes'

for (const language of ['en-US', 'zh-CN']) for (const theme of ['dark', 'light']) for (const width of [390, 1440]) {
  test(`replace Agent Engine retaining node ${language} ${theme} ${width}`, async ({ page }, testInfo) => {
    const zh = language === 'zh-CN'
    const past = new Date(Date.now() - 24 * 60 * 60_000).toISOString()
    const node: DockerNode = {
      id: 'cloud-node', name: 'HuaWeiCloud', connection_type: 'agent', endpoint: 'agent://cloud-node',
      tls_mode: 'disabled', enabled: true, engine_id: 'original-engine', status: 'offline',
      created_at: past, updated_at: past, group_ids: [7], agent_connected_at: past,
      last_error: 'Agent disconnected',
      agent_enrollment: { node_id: 'cloud-node', expires_at: past, consumed_at: past, last_error: 'Agent Docker Engine ID does not match the node' },
    }
    const writes: { path: string; body: unknown }[] = []
    const errors: string[] = []
    let rejectReplacement = true
    page.on('pageerror', error => errors.push(error.message))
    await page.setViewportSize({ width, height: 1000 })
    await page.addInitScript(({ language, theme }) => {
      localStorage.setItem('suma-language', language)
      localStorage.setItem('suma-theme', theme)
      localStorage.setItem('suma-node', 'cloud-node')
    }, { language, theme })
    await page.route('**/api/v1/**', async route => {
      const request = route.request()
      const path = new URL(request.url()).pathname.replace('/api/v1', '')
      if (request.method() !== 'GET') writes.push({ path, body: request.postDataJSON() })
      let data: unknown = []
      if (path === '/auth/status') data = { needs_setup: false }
      else if (path === '/auth/session') data = { id: 1, username: 'admin', has_avatar: false }
      else if (path === '/settings') data = { 'general.timezone': 'Asia/Shanghai' }
      else if (path === '/nodes') data = [node]
      else if (path === '/node-groups') data = [{ id: 7, name: 'Cloud', description: '', node_count: 1 }]
      else if (path === '/notifications/inbox') data = { items: [], unread: 0 }
      else if (path === '/nodes/cloud-node/agent/replace-engine') {
        if (rejectReplacement) {
          await route.fulfill({ status: 422, json: { code: 20514, message: 'Docker Engine is already registered' } })
          return
        }
        node.engine_id = 'replacement-engine'
        node.agent_connected_at = undefined
        node.last_error = ''
        node.agent_enrollment = { node_id: node.id, expires_at: new Date(Date.now() + 10 * 60_000).toISOString() }
        data = { ...node.agent_enrollment, token: 'c'.repeat(64), public_url: 'https://suma.example.com' }
      }
      await route.fulfill({ json: { code: 0, message: '', data } })
    })
    await page.goto('/nodes')
    const edit = page.getByRole('button', { name: zh ? '编辑' : 'Edit', exact: true })
    const migration = page.getByRole('button', { name: zh ? '迁移服务器 / 更换 Docker Engine' : 'Migrate server / replace Docker Engine', exact: true })
    await edit.click()
    await migration.click()
    const sheet = page.getByRole('dialog', { name: zh ? '更换 Docker Engine' : 'Replace Docker Engine', exact: true })
    await expect(sheet).toContainText(zh ? '项目关联、凭据授权和历史记录' : 'project links, credential grants and history')
    await expect(sheet.getByLabel(zh ? '保留的节点 ID' : 'Retained node ID')).toHaveValue('cloud-node')
    await expect(sheet.getByLabel(zh ? '当前 Engine ID' : 'Current Engine ID')).toHaveValue('original-engine')
    await sheet.getByRole('button', { name: zh ? '取消' : 'Cancel', exact: true }).click()
    expect(writes).toEqual([])
    await edit.click()
    await migration.click()
    const submit = sheet.getByRole('button', { name: zh ? '确认更换并生成令牌' : 'Replace and create token', exact: true })
    const engine = sheet.getByLabel(zh ? '新主机的 Engine ID' : 'New host Engine ID', { exact: true })
    const confirmation = sheet.getByLabel(zh ? '输入节点名 HuaWeiCloud 以确认' : 'Enter node name HuaWeiCloud to confirm', { exact: true })
    await expect(submit).toBeDisabled()
    await engine.fill('original-engine')
    await confirmation.fill('HuaWeiCloud')
    await expect(submit).toBeDisabled()
    await engine.fill('replacement-engine')
    await confirmation.fill('wrong-name')
    await expect(submit).toBeDisabled()
    await confirmation.fill('HuaWeiCloud')
    await expect(submit).toBeEnabled()
    await expect(page.locator('body')).toHaveJSProperty('scrollWidth', width)
    await sheet.screenshot({ path: testInfo.outputPath('replacement.png') })
    await submit.click()
    await expect(sheet).toContainText('Docker Engine is already registered')
    await expect(engine).toHaveValue('replacement-engine')
    expect(node.engine_id).toBe('original-engine')
    rejectReplacement = false
    await submit.click()
    const pairing = page.getByRole('dialog', { name: zh ? 'Agent 配对' : 'Agent pairing', exact: true })
    await expect(pairing).toBeVisible()
    await expect(pairing).toContainText('docker-compose.yml')
    await expect(pairing.getByRole('textbox').first()).toHaveValue('cloud-node')
    await expect(pairing).not.toContainText('Agent Docker Engine ID does not match the node')
    expect(node.group_ids).toEqual([7])
    expect(node.created_at).toBe(past)
    expect(writes).toEqual(Array.from({ length: 2 }, () => ({ path: '/nodes/cloud-node/agent/replace-engine', body: { previous_engine_id: 'original-engine', engine_id: 'replacement-engine', confirmation: 'HuaWeiCloud' } })))
    expect(errors).toEqual([])
  })
}
