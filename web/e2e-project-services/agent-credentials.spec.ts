import { expect, test } from '@playwright/test'
import type { DockerNode } from '../src/lib/nodes'

for (const language of ['en-US', 'zh-CN']) {
  test(`persistent Agent pairing and revocation ${language}`, async ({ page }) => {
    const zh = language === 'zh-CN'
    const now = Date.now()
    const past = new Date(now - 24 * 60 * 60_000).toISOString()
    const node: DockerNode = {
      id: 'local', name: 'paired-edge', connection_type: 'agent', endpoint: 'agent://local',
      tls_mode: 'disabled', enabled: true, engine_id: 'engine-edge', status: 'online',
      created_at: past, updated_at: past, group_ids: [], agent_connected_at: past,
      agent_enrollment: { node_id: 'local', expires_at: past, consumed_at: past }
    }
    const writes: string[] = []
    const errors: string[] = []
    page.on('pageerror', error => errors.push(error.message))
    await page.addInitScript(language => {
      localStorage.setItem('suma-language', language)
      localStorage.setItem('suma-theme', 'dark')
      localStorage.setItem('suma-node', 'local')
    }, language)
    await page.route('**/api/v1/**', async route => {
      const request = route.request()
      const path = new URL(request.url()).pathname.replace('/api/v1', '')
      if (request.method() !== 'GET') writes.push(path)
      let data: unknown = []
      if (path === '/auth/status') data = { needs_setup: false }
      else if (path === '/auth/session') data = { id: 1, username: 'admin', has_avatar: false }
      else if (path === '/settings') data = { 'general.timezone': 'Asia/Shanghai' }
      else if (path === '/nodes') data = [node]
      else if (path === '/notifications/inbox') data = { items: [], unread: 0 }
      else if (path.endsWith('/reissue')) {
        node.status = 'offline'
        node.agent_enrollment = { node_id: node.id, expires_at: new Date(now + 10 * 60_000).toISOString() }
        data = { ...node.agent_enrollment, token: 'a'.repeat(64), public_url: 'https://suma.example.com' }
      } else if (path.endsWith('/agent/revoke')) {
        node.status = 'offline'
        node.last_error = 'Agent credential revoked'
        data = { node_id: node.id }
      }
      await route.fulfill({ json: { code: 0, message: '', data } })
    })
    await page.goto('/nodes')
    await expect(page.getByRole('cell', { name: /paired-edge/ })).toBeVisible()
    await expect(page.getByText(/配对令牌已过期|Pairing token expired/)).toHaveCount(0)
    const refresh = page.getByRole('button', { name: zh ? '手动刷新配对令牌' : 'Refresh pairing token manually', exact: true })
    await refresh.click()
    const confirmation = page.getByRole('dialog', { name: zh ? '刷新配对令牌并重新配对？' : 'Refresh the token and pair again?' })
    await expect(confirmation).toContainText(zh ? '旧 Agent 凭据会立即作废' : 'old Agent credential is revoked immediately')
    await confirmation.getByRole('button', { name: zh ? '取消' : 'Cancel', exact: true }).click()
    expect(writes).toEqual([])
    await refresh.click()
    await confirmation.getByRole('button', { name: zh ? '刷新并重新配对' : 'Refresh and pair again', exact: true }).click()
    const sheet = page.getByRole('dialog', { name: zh ? 'Agent 配对' : 'Agent pairing', exact: true })
    await expect(sheet.getByText('docker-compose.yml', { exact: true })).toBeVisible()
    await expect(sheet).toContainText('restart: unless-stopped')
    await expect(sheet).toContainText(zh ? '凭据长期有效' : 'persistent credential')
    node.agent_enrollment!.consumed_at = new Date(now).toISOString()
    await page.clock.setSystemTime(now + 11 * 60_000)
    await expect(sheet).toContainText(zh ? '配对令牌已使用，凭据长期有效' : 'pairing token has been used; the credential remains valid')
    await expect(sheet.getByText('docker-compose.yml', { exact: true })).toHaveCount(0)
    await expect(sheet.getByText(/配对令牌已过期|pairing token has expired/)).toHaveCount(0)
    node.status = 'online'
    node.agent_connected_at = new Date(now + 11 * 60_000).toISOString()
    await expect(sheet).toContainText(zh ? 'Agent 已连接成功' : 'Agent connected successfully')
    await expect(sheet).toContainText(zh ? '现在可从 Compose 移除 SUMA_AGENT_TOKEN' : 'You can now remove SUMA_AGENT_TOKEN')
    await expect(sheet.getByRole('button', { name: zh ? '手动刷新令牌' : 'Refresh token manually', exact: true })).toHaveCount(0)
    await page.keyboard.press('Escape')
    await page.getByRole('button', { name: zh ? '撤销 Agent' : 'Revoke Agent', exact: true }).click()
    const revoke = page.getByRole('dialog', { name: zh ? '撤销 paired-edge 的 Agent 凭据？' : 'Revoke Agent for paired-edge?' })
    await revoke.getByRole('button', { name: zh ? '撤销凭据' : 'Revoke credential', exact: true }).click()
    await expect(page.getByText('Agent credential revoked', { exact: true })).toBeVisible()
    expect(writes).toEqual(['/agent-enrollments/local/reissue', '/nodes/local/agent/revoke'])
    expect(errors).toEqual([])
  })
}
