import { expect, test, type Page } from '@playwright/test'

async function setup(page: Page, language = 'en-US', theme = 'dark', runs: unknown[] = []) {
  await page.addInitScript(({ language, theme, runs }) => {
    sessionStorage.setItem('suma-demo-session', '1')
    localStorage.setItem('suma-language', language)
    localStorage.setItem('suma-theme', theme)
    localStorage.setItem('suma-node', 'edge-hk')
    if (!sessionStorage.getItem('suma-demo-operations')) sessionStorage.setItem('suma-demo-operations', JSON.stringify({
      serial: 200, settings: { version: 1, enabled: true, protocol: 'responses', endpoint: 'https://example.com/v1', model: 'default-model', models: ['default-model', 'alternate-model'], allow_private: false, allow_insecure: false, node_ids: ['local', 'edge-hk'], auto_events: [], max_concurrent: 2, daily_auto_limit: 20, max_tool_calls: 8, log_lines: 500, log_bytes: 65536, approval_minutes: 15, has_secret: false, tool_capable: true },
      channels: [], rules: [], deliveries: [], runs, operations: [], bindings: [], audits: [], inbox: { items: [], unread: 0 },
    }))
  }, { language, theme, runs })
}

async function state(page: Page) {
  return page.evaluate(() => JSON.parse(sessionStorage.getItem('suma-demo-operations')!))
}

test('global entry and per-turn model/scope selection preserve defaults and connected history', async ({ page }) => {
  await setup(page)
  await page.goto('/ai-operations')
  await expect(page.getByRole('combobox', { name: 'Current Docker node', exact: true })).toHaveCount(0)
  const navigation = page.getByRole('navigation', { name: 'Primary navigation', exact: true })
  const ai = navigation.getByRole('button', { name: 'AI workbench', exact: true })
  await expect(ai).toHaveAttribute('aria-current', 'page')
  expect((await ai.boundingBox())!.y).toBeLessThan((await navigation.getByRole('button', { name: 'Containers', exact: true }).boundingBox())!.y)
  const scope = page.getByRole('combobox', { name: 'Diagnosis scope', exact: true })
  const model = page.getByRole('combobox', { name: 'Conversation model', exact: true })
  await expect(scope).toContainText('All authorized nodes')
  await expect(model).toContainText('default-model')
  await model.click()
  await page.getByRole('option', { name: 'alternate-model', exact: true }).click()
  const input = page.getByRole('textbox', { name: 'Diagnosis request', exact: true })
  await input.fill('Compare the nodes')
  await page.getByRole('tab', { name: 'Audit history', exact: true }).click()
  await page.getByRole('tab', { name: 'Conversations', exact: true }).click()
  await expect(model).toContainText('alternate-model')
  await expect(input).toHaveValue('Compare the nodes')
  await input.press('Enter')
  const transcript = page.getByRole('region', { name: 'Diagnosis conversation', exact: true })
  await expect(transcript.locator('[data-slot="markdown"]')).toHaveCount(1)
  const first = (await state(page)).runs[0]
  expect(first).toMatchObject({ model: 'alternate-model', node_id: '' })
  expect((await state(page)).settings.model).toBe('default-model')
  await scope.click()
  await page.getByRole('option', { name: 'edge-hk', exact: true }).click()
  await model.click()
  await page.getByRole('option', { name: 'default-model', exact: true }).click()
  await input.fill('Now focus on the remote node')
  await input.press('Enter')
  await expect(transcript.locator('[data-slot="markdown"]')).toHaveCount(2)
  expect((await state(page)).runs[0]).toMatchObject({ parent_id: first.id, model: 'default-model', node_id: 'edge-hk' })
  await page.getByRole('button', { name: 'New diagnosis', exact: true }).click()
  await expect(transcript.locator('[data-slot="message"]')).toHaveCount(0)
  await expect(scope).toContainText('All authorized nodes')
  await expect(model).toContainText('default-model')
  await page.getByRole('complementary', { name: 'Conversation history', exact: true }).getByRole('button', { name: 'Compare the nodes', exact: false }).click()
  await expect(transcript.locator('[data-slot="message"]')).toHaveCount(4)
  await page.reload()
  await page.getByRole('complementary', { name: 'Conversation history', exact: true }).getByRole('button', { name: 'Compare the nodes', exact: false }).click()
  await expect(transcript.locator('[data-slot="message"]')).toHaveCount(4)
})

test('resource context keeps its own runtime while the conversation stays global', async ({ page }) => {
  await setup(page)
  await page.goto('/ai-operations#kind=container&resource=redis-main&node=local')
  await expect(page.getByRole('combobox', { name: 'Diagnosis scope', exact: true })).toContainText('All authorized nodes')
  await page.getByRole('textbox', { name: 'Diagnosis request', exact: true }).fill('Inspect this container')
  await page.getByRole('button', { name: 'Start diagnosis', exact: true }).click()
  await expect(page.getByRole('region', { name: 'Diagnosis conversation', exact: true }).locator('[data-slot="markdown"]')).toHaveCount(1)
  const saved = await state(page)
  expect(saved.runs[0].node_id).toBe('')
  expect(saved.runs[0].result.evidence[0].node_id).toBe('local')
  expect(saved.operations[0].node_id).toBe('local')
  expect(await page.evaluate(() => localStorage.getItem('suma-node'))).toBe('edge-hk')
})

test('opening a new conversation while a request is pending keeps the new draft', async ({ page }) => {
  await setup(page)
  await page.goto('/ai-operations')
  const input = page.getByRole('textbox', { name: 'Diagnosis request', exact: true })
  await input.fill('Background diagnosis')
  await expect(page.getByRole('button', { name: 'Start diagnosis', exact: true })).toBeEnabled()
  await input.press('Enter')
  await page.getByRole('button', { name: 'New diagnosis', exact: true }).click()
  await input.fill('Draft for another conversation')
  const history = page.getByRole('complementary', { name: 'Conversation history', exact: true })
  await expect(history.getByRole('button', { name: 'Background diagnosis', exact: false })).toHaveCount(1)
  await expect(input).toHaveValue('Draft for another conversation')
  await expect(page.getByRole('region', { name: 'Diagnosis conversation', exact: true }).locator('[data-slot="message"]')).toHaveCount(0)
})

test('default setting explains channel usage and persists the chosen model', async ({ page }) => {
  await setup(page)
  await page.goto('/settings#ai')
  await expect(page.getByText('New conversations, notification-channel chats and automatic diagnoses use the default model.', { exact: false })).toBeVisible()
  await page.getByRole('combobox', { name: 'Default model', exact: true }).click()
  await page.getByRole('option', { name: 'alternate-model', exact: true }).click()
  await page.getByRole('button', { name: 'Save AI settings', exact: true }).click()
  await expect(page.getByText('AI settings saved', { exact: true })).toBeVisible()
  await page.goto('/ai-operations')
  await expect(page.getByRole('combobox', { name: 'Conversation model', exact: true })).toContainText('alternate-model')
})

for (const language of ['en-US', 'zh-CN']) for (const theme of ['dark', 'light']) for (const width of [390, 1440]) {
  test(`Markdown chat ${language} ${theme} ${width}`, async ({ page }) => {
    const zh = language === 'zh-CN'
    const summary = `## ${zh ? '跨节点诊断' : 'Cross-node diagnosis'}\n\n**OOM** and *restarts* with \`redis-main\`.\n\n- Inspect memory\n- Review limits\n\n1. Collect evidence\n2. Review changes\n\n> Each change needs approval.\n\n| Node | Status | Evidence |\n| --- | --- | --- |\n| local | restarting | ${'very-long-evidence-'.repeat(20)} |\n| remote | running | healthy |\n\n\`\`\`bash\ndocker inspect redis-main\n${'very-long-command-'.repeat(35)}\n\`\`\`\n\n- [x] Evidence collected\n- [ ] Approval pending\n\n[Documentation](https://docs.example.com/guide)\n\n[Unsafe link](javascript:alert(1))\n\n<img src="https://leak.example.com/pixel" onerror="window.markdownExecuted=true">\n\n![Blocked image](https://leak.example.com/image.png)\n\n<script>window.markdownExecuted=true</script>`
    const first = { id: 'md-local', node_id: 'local', model: 'default-model', question: zh ? '检查本地节点' : 'Inspect the local node', parent_id: '', status: 'completed', error: '', tokens: 20, created_at: '2026-10-06T09:00:00Z', result: { summary: 'Local node inspected.', evidence: [], operation_ids: [], missing: [] } }
    const second = { ...first, id: 'md-global', node_id: '', model: 'alternate-model', parent_id: first.id, question: zh ? '比较所有节点' : 'Compare all nodes', result: { ...first.result, summary, evidence: [{ node_id: 'edge-hk', source: 'read_status', resource: 'redis-main', time: first.created_at, content: 'State: running', unavailable: false }] } }
    await setup(page, language, theme, [second, first])
    await page.setViewportSize({ width, height: 900 })
    const errors: string[] = [], leaked: string[] = []
    page.on('pageerror', error => errors.push(error.message))
    page.on('request', request => { if (request.url().includes('leak.example.com')) leaked.push(request.url()) })
    await page.goto('/ai-operations#run=md-global')
    const transcript = page.getByRole('region', { name: zh ? '诊断对话' : 'Diagnosis conversation', exact: true })
    await expect(transcript.locator('[data-slot="message"]')).toHaveCount(4)
    await expect(transcript.getByRole('heading', { name: zh ? '跨节点诊断' : 'Cross-node diagnosis', exact: true })).toBeVisible()
    await expect(transcript.locator('strong')).toHaveText('OOM')
    await expect(transcript.locator('table')).toHaveCount(1)
    await expect(transcript.locator('pre code')).toContainText('docker inspect redis-main')
    await expect(transcript.locator('blockquote')).toContainText('Each change needs approval.')
    await expect(transcript.getByRole('checkbox')).toHaveCount(2)
    const link = transcript.getByRole('link', { name: 'Documentation', exact: true })
    await expect(link).toHaveAttribute('target', '_blank')
    await expect(link).toHaveAttribute('rel', 'noopener noreferrer')
    await expect(transcript.getByRole('link', { name: 'Unsafe link', exact: true })).toHaveCount(0)
    await expect(transcript.locator('img,script')).toHaveCount(0)
    expect(await page.evaluate(() => (window as unknown as { markdownExecuted?: boolean }).markdownExecuted)).toBeUndefined()
    expect(leaked).toEqual([])
    await expect(page.getByRole('combobox', { name: zh ? '对话模型' : 'Conversation model', exact: true })).toContainText('alternate-model')
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await expect.poll(() => transcript.evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
    await transcript.evaluate(element => { element.scrollTop = 0 })
    await page.mouse.move(0, 0)
    await page.screenshot({ path: `/tmp/suma-ai-global-markdown-${language}-${theme}-${width}.png`, fullPage: true })
    expect(errors).toEqual([])
  })
}
