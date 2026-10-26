import { expect, test } from '@playwright/test'

const created = '2026-10-26T06:00:00Z'
for (const language of ['zh-CN', 'en-US']) for (const theme of ['light', 'dark']) for (const width of [390, 1440]) {
  test(`CD project names ${language} ${theme} ${width}`, async ({ page }) => {
    const zh = language === 'zh-CN'
    await page.setViewportSize({ width, height: 1000 })
    await page.addInitScript(({ language, theme }) => {
      localStorage.setItem('suma-language', language)
      localStorage.setItem('suma-theme', theme)
      if (!localStorage.getItem('suma-node')) localStorage.setItem('suma-node', 'local')
    }, { language, theme })
    const writes: string[] = [], errors: string[] = []
    page.on('pageerror', error => errors.push(error.message))
    await page.route('**/api/v1/**', async route => {
      const request = route.request(), path = new URL(request.url()).pathname.replace('/api/v1', '')
      if (request.method() !== 'GET') writes.push(path)
      const other = path.startsWith('/nodes/other/')
      const project = { ref: { backend: 'compose', scope: { kind: 'engine', id: other ? 'other' : 'local' }, native_name: 'suma-cd-1' }, backend: 'compose', scope: { kind: 'engine', id: other ? 'other' : 'local' }, node_id: other ? 'other' : 'local', name: 'suma-cd-1', native_name: 'suma-cd-1', delivery_project_name: other ? undefined : 'eizhu_admin', managed: false, source: 'external', capabilities: ['view', 'services', 'logs'], service_count: 1, instance_count: 1, status: 'stopped', created_at: created, updated_at: created }
      let data: unknown = []
      if (path === '/auth/status') data = { needs_setup: false }
      else if (path === '/auth/session') data = { id: 1, username: 'admin', has_avatar: false }
      else if (path === '/settings') data = { 'general.timezone': 'Asia/Shanghai' }
      else if (path === '/nodes') data = ['local', 'other'].map(id => ({ id, name: id === 'local' ? 'Aliyun' : 'Other', enabled: true, connection_type: 'unix', status: 'online', group_ids: [] }))
      else if (path === '/notifications/inbox') data = { items: [], unread: 0 }
      else if (path.endsWith('/projects')) data = [project]
      else if (path.endsWith('/projects/compose/suma-cd-1')) data = { ...project, path: '', can_manage: false, config_files: [], services: 1, containers: 1, compose: '', environment: '' }
      else if (path.endsWith('/containers')) data = [{ id: 'a'.repeat(64), name: 'suma-cd-1-app-1', image: 'app:v1', command: '', state: 'exited', status: 'Exited', created, ports: [], labels: { 'com.docker.compose.project': 'suma-cd-1', 'com.docker.compose.service': 'app' }, cpu_percent: 0, memory_bytes: 0, uptime_seconds: 0 }]
      await route.fulfill({ json: { code: 0, message: '', data } })
    })
    await page.goto('/projects')
    const row = page.getByTestId('project-row')
    await expect(row.getByRole('link', { name: 'eizhu_admin', exact: true })).toBeVisible()
    await expect(row).toContainText('CD')
    await expect(row).toHaveAttribute('data-project', 'suma-cd-1')
    const search = page.getByRole('textbox', { name: zh ? '搜索项目、服务或镜像' : 'Search projects, services or images' })
    await search.fill('eizhu')
    await expect(row).toHaveCount(1)
    await search.fill('suma-cd-1')
    await expect(row).toHaveCount(1)
    await search.fill('')
    await page.screenshot({ path: `/private/tmp/suma-cd-names-${language}-${theme}-${width}.png` })
    await row.getByRole('link', { name: 'eizhu_admin', exact: true }).click()
    await expect(page).toHaveURL(/\/projects\/compose\/suma-cd-1$/)
    await expect(page.getByRole('heading', { name: 'eizhu_admin', exact: true })).toBeVisible()
    await page.evaluate(() => localStorage.setItem('suma-node', 'other'))
    await page.goto('/projects')
    await page.reload()
    await expect(page.getByTestId('project-row').getByRole('link', { name: 'suma-cd-1', exact: true })).toBeVisible()
    await expect(page.getByRole('link', { name: 'eizhu_admin', exact: true })).toHaveCount(0)
    await page.goto('/continuous-delivery/new/project')
    const name = page.getByLabel(zh ? '项目名称' : 'Project name', { exact: true })
    await name.fill('App.Prod')
    expect(await name.evaluate(element => (element as HTMLInputElement).checkValidity())).toBe(false)
    await name.fill('eizhu_admin')
    expect(await name.evaluate(element => (element as HTMLInputElement).checkValidity())).toBe(true)
    expect(writes).toEqual([])
    expect(errors).toEqual([])
  })
}
