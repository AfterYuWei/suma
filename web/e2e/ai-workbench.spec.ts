import { expect, test, type Page } from '@playwright/test'

async function setup(page: Page, language = 'en-US', theme = 'dark', runs: unknown[] = []) {
  await page.addInitScript(({ language, theme, runs }) => {
    sessionStorage.setItem('suma-demo-session', '1')
    localStorage.setItem('suma-language', language)
    localStorage.setItem('suma-theme', theme)
    localStorage.setItem('suma-node', 'edge-hk')
    if (!sessionStorage.getItem('suma-demo-operations-v2')) sessionStorage.setItem('suma-demo-operations-v2', JSON.stringify({
      serial: 200, settings: { version: 1, enabled: true, protocol: 'responses', endpoint: 'https://example.com/v1', model: 'default-model', models: ['default-model', 'alternate-model'], allow_private: false, allow_insecure: false, node_ids: ['local', 'edge-hk'], auto_events: [], max_concurrent: 2, daily_auto_limit: 20, max_tool_calls: 8, max_iterations: 40, max_operations: 20, log_lines: 500, log_bytes: 65536, approval_minutes: 15, has_secret: false, tool_capable: true },
      conversations: runs.length ? [{ id: 'test-conversation', title: (runs[runs.length - 1] as { question: string }).question, source: 'site', current_run_id: (runs[0] as { id: string }).id, revision: 1, event_seq: 0, created_at: '2026-10-03T09:00:00Z', updated_at: '2026-10-03T09:01:00Z', context: { node_ids: (runs[0] as { target_node_ids?: string[] }).target_node_ids || ['local'] }, messages: [], runs: [] }] : [], channels: [], rules: [], deliveries: [], runs, operations: [], bindings: [], audits: [], inbox: { items: [], unread: 0 },
    }))
  }, { language, theme, runs })
}

async function state(page: Page) {
  return page.evaluate(() => JSON.parse(sessionStorage.getItem('suma-demo-operations-v2')!))
}

test('missing target prompts for a node and task adjustment invalidates the old proposal', async ({ page }) => {
 await setup(page); await page.goto('/ai-operations')
 const navigation = page.getByRole('navigation', { name: 'Primary navigation', exact: true })
 await expect(navigation.getByRole('button', { name: 'AI workbench', exact: true })).toHaveAttribute('aria-current', 'page')
 const operations = navigation.locator('p', { hasText: /^Operations$/ }).locator('..')
 await expect(operations.getByRole('button', { name: 'AI workbench', exact: true })).toHaveCount(1)
 await expect(page.getByRole('combobox', { name: 'Diagnosis scope', exact: true })).toHaveCount(0)
 const model = page.getByRole('combobox', { name: 'Conversation model', exact: true })
 await model.click(); await page.getByRole('option', { name: 'alternate-model', exact: true }).click()
 const input = page.getByRole('textbox', { name: 'Diagnosis request', exact: true }); await input.fill('Inspect the container'); await input.press('Enter')
 const interaction = page.getByRole('form', { name: 'Provide task input', exact: true }); await expect(interaction).toBeVisible()
 expect((await state(page)).operations).toHaveLength(0)
 expect((await state(page)).runs[0]).toMatchObject({ model: 'alternate-model', target_node_ids: [], status: 'waiting_input' })
 await interaction.getByRole('checkbox', { name: 'edge-hk', exact: false }).check(); await interaction.getByRole('button', { name: 'Confirm and continue', exact: true }).click()
 await expect(page.getByRole('list', { name: 'Task plan', exact: true })).toBeVisible()
 const first = (await state(page)).runs[0]; expect(first.target_node_ids).toEqual(['edge-hk']); expect((await state(page)).settings.model).toBe('default-model')
 await input.fill('Change the task and inspect again'); await input.press('Enter')
 await expect.poll(async () => (await state(page)).runs.length).toBe(2)
 const saved = await state(page); expect(saved.runs[0].conversation_id).toBe(first.conversation_id); expect(saved.operations.find((op: { run_id: string }) => op.run_id === first.id).status).toBe('invalidated')
 await page.getByRole('button', { name: 'New diagnosis', exact: true }).click(); await input.fill('A fresh task'); await input.press('Enter'); await expect(interaction).toBeVisible()
 await page.reload(); const history = page.getByRole('complementary', { name: 'Conversation history', exact: true }); await history.getByRole('button', { name: 'Inspect the container', exact: false }).click()
 await expect(page.getByRole('region', { name: 'Diagnosis conversation', exact: true }).locator('[data-slot="message"]')).toHaveCount(4)
})

test('resource context keeps its own runtime while the conversation stays global', async ({ page }) => {
  await setup(page)
  await page.goto('/ai-operations#kind=container&resource=redis-main&node=local')
  await expect(page.getByRole('combobox', { name: 'Diagnosis scope', exact: true })).toHaveCount(0)
  await page.getByRole('textbox', { name: 'Diagnosis request', exact: true }).fill('Inspect this container')
  await page.getByRole('button', { name: 'Start diagnosis', exact: true }).click()
  await expect(page.getByRole('region', { name: 'Diagnosis conversation', exact: true }).locator('[data-slot="markdown"]')).toHaveCount(1)
  const saved = await state(page)
  expect(saved.runs[0].node_id).toBe('local')
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
  test(`confirm or reject a semantic node suggestion ${language} ${theme} ${width}`, async ({ page }) => {
    const zh = language === 'zh-CN', now = new Date().toISOString()
    const waiting = { id: 'semantic-node', conversation_id: 'test-conversation', revision: 2, phase: 'target', node_id: '', target_node_ids: [], target_source: '', steps: [], model: 'default-model', question: '阿里云节点有哪些容器？', status: 'waiting_input', error: '', tokens: 50, created_at: now, result: { summary: '', evidence: [], operation_ids: [], missing: [] }, interaction: { id: 'confirm-alias', run_id: 'semantic-node', revision: 2, kind: 'node', prompt: zh ? '你指的是 aliyun 节点吗？' : 'Do you mean the aliyun node?', multiple: false, status: 'pending', expires_at: new Date(Date.now() + 86400000).toISOString(), options: [{ id: 'local', name: 'aliyun', node_id: 'local', kind: 'node', suggested: true }, { id: 'edge-hk', name: 'edge-hk', node_id: 'edge-hk', kind: 'node' }] } }
    await setup(page, language, theme, [waiting])
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/ai-operations#run=semantic-node')
    const interaction = page.getByRole('form', { name: zh ? '补充任务信息' : 'Provide task input', exact: true })
    await expect(interaction).toContainText(zh ? '你指的是 aliyun 节点吗' : 'Do you mean the aliyun node')
    await expect(interaction.getByRole('button', { name: zh ? '是，继续' : 'Yes, continue', exact: true })).toBeVisible()
    expect((await state(page)).operations).toEqual([])
    expect((await state(page)).runs[0].target_node_ids).toEqual([])
    if (theme === 'dark') {
      if (zh && width === 390) await page.screenshot({ path: '/tmp/suma-ai-semantic-confirmation-mobile.png', fullPage: true })
      await interaction.getByRole('button', { name: zh ? '是，继续' : 'Yes, continue', exact: true }).click()
      await expect(interaction).toHaveCount(0)
      expect((await state(page)).runs[0]).toMatchObject({ node_id: 'local', target_source: 'selection', status: 'completed' })
    } else {
      await interaction.getByRole('button', { name: zh ? '选择其他节点' : 'Choose another node', exact: true }).click()
      const confirm = interaction.getByRole('button', { name: zh ? '确认并继续' : 'Confirm and continue', exact: true })
      await expect(confirm).toBeDisabled()
      await expect(interaction.getByRole('checkbox', { name: 'aliyun', exact: false })).not.toBeChecked()
      await interaction.getByRole('checkbox', { name: 'edge-hk', exact: false }).check()
      await confirm.click()
      await expect(interaction).toHaveCount(0)
      expect((await state(page)).runs[0]).toMatchObject({ node_id: 'edge-hk', target_source: 'selection', status: 'completed' })
    }
    await expect(page.getByRole('cell', { name: 'demo-nginx', exact: true })).toBeVisible()
    expect((await state(page)).operations).toEqual([])
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  })

  test(`recover an unverified node query ${language} ${theme} ${width}`, async ({ page }) => {
    const zh = language === 'zh-CN', question = zh ? '阿里云节点有哪些容器？' : 'Which containers are on the Alibaba Cloud node?'
    const failed = { id: 'legacy-scope-error', conversation_id: 'test-conversation', revision: 1, phase: 'paused', node_id: '', target_node_ids: [], target_source: '', steps: [], model: 'default-model', question, status: 'failed', error: '[NodeRunError] failed to stream tool call call_old: resource is outside the authorized AI scope -------- node path: [node_1, ToolNode]', tokens: 11755, created_at: '2026-10-10T09:00:00Z', result: { summary: '', evidence: [], operation_ids: [], missing: [] } }
    await setup(page, language, theme, [failed])
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/ai-operations#run=legacy-scope-error')
    const alert = page.getByRole('alert')
    await expect(alert).toContainText(zh ? '未能确定本次查询的目标节点' : 'The target node could not be verified')
    await expect(alert).not.toContainText('NodeRunError')
    await expect(alert.getByRole('link')).toHaveAttribute('href', '/settings#ai')
    const input = page.getByRole('textbox', { name: zh ? '分析请求' : 'Diagnosis request', exact: true })
    await input.fill(question); await input.press('Enter')
    const interaction = page.getByRole('form', { name: zh ? '补充任务信息' : 'Provide task input', exact: true })
    await expect(interaction).toBeVisible()
    const confirm = interaction.getByRole('button', { name: zh ? '确认并继续' : 'Confirm and continue', exact: true })
    await expect(confirm).toBeDisabled()
    expect((await state(page)).runs[0]).toMatchObject({ target_node_ids: [], status: 'waiting_input' })
    await interaction.getByRole('checkbox', { name: 'local', exact: false }).check()
    await confirm.click()
    await expect(interaction).toHaveCount(0)
    await expect(page.getByRole('cell', { name: 'demo-nginx', exact: true })).toBeVisible()
    const saved = await state(page)
    expect(saved.runs[0]).toMatchObject({ target_node_ids: ['local'], target_source: 'selection', status: 'completed', error: '' })
    expect(saved.runs[0].result.operation_ids).toEqual([])
    expect(saved.operations).toEqual([])
    expect(await page.evaluate(() => localStorage.getItem('suma-node'))).toBe('edge-hk')
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    if (language === 'zh-CN' && width === 390) await page.screenshot({ path: `/tmp/suma-ai-target-recovery-${theme}.png`, fullPage: true })
  })

  test(`Markdown chat ${language} ${theme} ${width}`, async ({ page }) => {
    const zh = language === 'zh-CN'
    const summary = `## ${zh ? '跨节点诊断' : 'Cross-node diagnosis'}\n\n**OOM** and *restarts* with \`redis-main\`.\n\n- Inspect memory\n- Review limits\n\n1. Collect evidence\n2. Review changes\n\n> Each change needs approval.\n\n| Node | Status | Evidence |\n| --- | --- | --- |\n| local | restarting | ${'very-long-evidence-'.repeat(20)} |\n| remote | running | healthy |\n\n\`\`\`bash\ndocker inspect redis-main\n${'very-long-command-'.repeat(35)}\n\`\`\`\n\n- [x] Evidence collected\n- [ ] Approval pending\n\n[Documentation](https://docs.example.com/guide)\n\n[Unsafe link](javascript:alert(1))\n\n<img src="https://leak.example.com/pixel" onerror="window.markdownExecuted=true">\n\n![Blocked image](https://leak.example.com/image.png)\n\n<script>window.markdownExecuted=true</script>`
    const first = { id: 'md-local', node_id: 'local', model: 'default-model', question: zh ? '检查本地节点' : 'Inspect the local node', conversation_id: 'test-conversation', revision: 1, phase: 'report', target_node_ids: ['local'], target_source: 'selection', steps: [], status: 'completed', error: '', tokens: 20, created_at: '2026-10-06T09:00:00Z', result: { summary: 'Local node inspected.', evidence: [], operation_ids: [], missing: [] } }
    const second = { ...first, id: 'md-global', node_id: '', model: 'alternate-model',  question: zh ? '比较所有节点' : 'Compare all nodes', result: { ...first.result, summary, evidence: [{ node_id: 'edge-hk', source: 'read_status', resource: 'redis-main', time: first.created_at, content: 'State: running', unavailable: false }] } }
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
    const model = page.getByRole('combobox', { name: zh ? '对话模型' : 'Conversation model', exact: true })
    await expect(model).toContainText('alternate-model')
    const modelBox = (await model.boundingBox())!, sendBox = (await page.getByRole('button', { name: zh ? '发起诊断' : 'Start diagnosis', exact: true }).boundingBox())!
    expect(modelBox.x + modelBox.width).toBeLessThan(sendBox.x)
    expect(Math.abs(modelBox.y + modelBox.height / 2 - sendBox.y - sendBox.height / 2)).toBeLessThan(1)
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await expect.poll(() => transcript.evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
    await transcript.evaluate(element => { element.scrollTop = 0 })
    await page.mouse.move(0, 0)
    await page.screenshot({ path: `/tmp/suma-ai-global-markdown-${language}-${theme}-${width}.png`, fullPage: true })
    expect(errors).toEqual([])
  })
}

test('operation review requires the human volume name and shows verified results', async ({ page }) => {
 await setup(page)
 await page.addInitScript(() => { Object.defineProperty(window.crypto, 'randomUUID', { value: undefined, configurable: true }) })
 await page.goto('/ai-operations#kind=container&resource=redis-main&node=local')
 await page.getByRole('textbox', { name: 'Diagnosis request', exact: true }).fill('Inspect this container')
 await page.getByRole('button', { name: 'Start diagnosis', exact: true }).click()
 await expect.poll(async () => (await state(page)).operations.length).toBe(1)
 const operationID = await page.evaluate(() => {
  const value = JSON.parse(sessionStorage.getItem('suma-demo-operations-v2')!)
  const op = value.operations[0]
  op.action = 'volume.remove'; op.resource_id = 'test-volume'; op.title = 'Delete test-volume'
  op.confirmations = [{ key: 'name', label: 'Type the complete volume name to confirm data loss', expected: 'test-volume', warning: 'All data will be permanently deleted' }]
  sessionStorage.setItem('suma-demo-operations-v2', JSON.stringify(value))
  return op.id as string
 })
 await page.goto(`/ai-operations#operation=${operationID}`)
 await page.reload()
 const sheet = page.getByRole('dialog', { name: 'Full operation preview', exact: true })
 await expect(sheet).toBeVisible()
 const approve = sheet.getByRole('button', { name: 'Approve this operation', exact: true })
 await expect(approve).toBeDisabled()
 await sheet.getByRole('checkbox', { name: 'I have read the full preview', exact: false }).check()
 const name = sheet.getByRole('textbox', { name: 'Type the complete volume name to confirm data loss', exact: true })
 await name.fill('wrong-volume'); await expect(approve).toBeDisabled()
 await name.fill('test-volume'); await expect(approve).toBeEnabled()
 await approve.click()
 await expect(sheet.getByText('Result verification', { exact: false })).toBeVisible()
 expect((await state(page)).operations[0].status).toBe('completed')
})
