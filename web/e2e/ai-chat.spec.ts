import { expect, test, type Page } from '@playwright/test'

async function setup(page: Page, language = 'en-US', theme = 'dark', runs: unknown[] = []) {
  await page.addInitScript(({ language, theme, runs }) => {
    sessionStorage.setItem('suma-demo-session', '1')
    localStorage.setItem('suma-language', language)
    localStorage.setItem('suma-theme', theme)
    localStorage.setItem('suma-node', 'local')
    if (!sessionStorage.getItem('suma-demo-operations-v2')) sessionStorage.setItem('suma-demo-operations-v2', JSON.stringify({
      serial: 100, settings: { version: 1, enabled: true, protocol: 'responses', endpoint: 'https://example.com', model: 'demo-model', models: ['demo-model'], allow_private: false, node_ids: ['local'], auto_events: [], max_concurrent: 2, daily_auto_limit: 20, max_tool_calls: 8, max_iterations: 40, max_operations: 20, log_lines: 500, log_bytes: 65536, approval_minutes: 15, has_secret: false, tool_capable: true },
      conversations: runs.length ? [{ id: 'test-conversation', title: (runs[runs.length - 1] as { question: string }).question, source: 'site', current_run_id: (runs[0] as { id: string }).id, revision: 1, event_seq: 0, created_at: '2026-10-03T09:00:00Z', updated_at: '2026-10-03T09:01:00Z', context: { node_ids: ['local'] }, messages: [], runs: [] }] : [], channels: [], rules: [], deliveries: [], runs, operations: [], bindings: [], audits: [], inbox: { items: [], unread: 0 },
    }))
  }, { language, theme, runs })
}

test('shadcn chat supports keyboard input, connected follow-ups and saved history', async ({ page }) => {
  await setup(page)
  await page.goto('/ai-operations')
  const transcript = page.getByRole('region', { name: 'Diagnosis conversation', exact: true })
  const input = page.getByRole('textbox', { name: 'Diagnosis request', exact: true })
  await expect(transcript.locator('[data-slot="message"]')).toHaveCount(0)
  await input.fill('Why is redis restarting?')
  await expect(page.getByRole('button', { name: 'Start diagnosis', exact: true })).toBeEnabled()
  await input.dispatchEvent('keydown', { key: 'Enter', code: 'Enter', isComposing: true })
  await expect(transcript.locator('[data-slot="message"]')).toHaveCount(0)
  await input.press('Enter')
  await expect(transcript.locator('[data-slot="message"][data-align="end"]')).toHaveCount(1)
  await expect(transcript.locator('[data-slot="message"][data-align="start"]')).toHaveCount(1)
  const interaction = page.getByRole('form', { name: 'Provide task input', exact: true }); await interaction.getByRole('checkbox').check(); await interaction.getByRole('button', { name: 'Confirm and continue', exact: true }).click(); await expect(transcript.getByText('Evidence suggests', { exact: false })).toBeVisible()
  await transcript.getByRole('button', { name: 'Evidence', exact: false }).click()
  await expect(transcript.getByText('PASSWORD=[redacted]', { exact: false })).toBeVisible()
  await input.fill('What should I check next?')
  await input.press('Shift+Enter')
  await input.press('End')
  await input.type('Explain the evidence.')
  await expect(input).toHaveValue('What should I check next?\nExplain the evidence.')
  await page.getByRole('tab', { name: 'Audit history', exact: true }).click()
  await page.getByRole('tab', { name: 'Conversations', exact: true }).click()
  await expect(input).toHaveValue('What should I check next?\nExplain the evidence.')
  await expect(page.getByRole('button', { name: 'Start diagnosis', exact: true })).toBeEnabled()
  await input.press('Enter')
  await expect(transcript.locator('[data-slot="message"][data-align="end"]')).toHaveCount(2)
  await expect(transcript.locator('[data-slot="message"][data-align="start"]')).toHaveCount(2)
  const history = page.getByRole('complementary', { name: 'Conversation history', exact: true })
  await expect(history.getByRole('button', { name: 'Why is redis restarting?', exact: false })).toHaveCount(1)
  await page.getByRole('button', { name: 'New diagnosis', exact: true }).click()
  await expect(transcript.locator('[data-slot="message"]')).toHaveCount(0)
  await history.getByRole('button', { name: 'Why is redis restarting?', exact: false }).click()
  await expect(transcript.locator('[data-slot="message"]')).toHaveCount(4)
  await page.reload()
  await history.getByRole('button', { name: 'Why is redis restarting?', exact: false }).click()
  await expect(transcript.locator('[data-slot="message"]')).toHaveCount(4)
})

test('running diagnosis shows progress and accepts an updated request', async ({ page }) => {
  const run = { id: 'chat-running', node_id: 'local', question: 'Investigate restarts', conversation_id: 'test-conversation', revision: 1, phase: 'report', target_node_ids: ['local'], target_source: 'selection', steps: [], status: 'running', error: '', tokens: 0, created_at: new Date().toISOString(), result: { summary: '', evidence: [], operation_ids: [], missing: [] } }
  await setup(page, 'en-US', 'dark', [run])
  await page.goto('/ai-operations#run=chat-running')
  await expect(page.getByRole('status').filter({ hasText: 'Analyzing the task' })).toBeVisible()
  await expect(page.getByRole('textbox', { name: 'Diagnosis request', exact: true })).toBeEnabled(); await page.getByRole('textbox', { name: 'Diagnosis request', exact: true }).fill('Adjust the task')
  await expect(page.getByRole('button', { name: 'Start diagnosis', exact: true })).toBeEnabled()
})

for (const width of [390, 1440]) for (const language of ['en-US', 'zh-CN']) for (const theme of ['dark', 'light']) {
  test(`filled chat ${language} ${theme} ${width}`, async ({ page }) => {
    const zh = language === 'zh-CN'
    const first = { id: 'chat-first', node_id: 'local', question: zh ? '这个容器为什么一直重启？' : 'Why does this container keep restarting?', conversation_id: 'test-conversation', revision: 1, phase: 'report', target_node_ids: ['local'], target_source: 'selection', steps: [], status: 'completed', error: '', tokens: 120, created_at: '2026-10-03T09:00:00Z', result: { summary: zh ? '容器超过了内存限制，系统记录了 OOM。请先核对应用的内存需求，再决定是否调整配置。' : 'The container exceeded its memory limit and Docker recorded an OOM. Check the application’s memory requirements before deciding whether to change its configuration.', evidence: [{ source: 'read_status', resource: 'redis-main', time: '2026-10-03T09:00:00Z', content: 'OOMKilled: true\nPASSWORD=[redacted]', unavailable: false }], operation_ids: [], missing: [] } }
    const second = { ...first, id: 'chat-second',  question: zh ? '这些证据说明了什么？' : 'What does the evidence tell us?', tokens: 180, created_at: '2026-10-03T09:01:00Z', result: { ...first.result, summary: zh ? 'OOM 与多次重启同时出现，可能是负载增加或内存限制过低。\n\n继续观察内存使用情况，并结合应用日志确认原因。任何变更都需要单独审核。' : 'The OOM and repeated restarts coincide, which may indicate increased load or an insufficient memory limit.\n\nObserve memory usage and correlate it with application logs. Each change requires its own review.' } }
    await setup(page, language, theme, [second, first])
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/ai-operations#run=chat-second')
    const transcript = page.getByRole('region', { name: zh ? '诊断对话' : 'Diagnosis conversation', exact: true })
    await expect(transcript.locator('[data-slot="message"]')).toHaveCount(4)
    const composer = page.getByRole('form', { name: zh ? '发送诊断请求' : 'Send diagnosis request', exact: true })
    await expect(composer.getByRole('textbox')).toBeVisible()
    const transcriptBottom = await transcript.evaluate(element => element.getBoundingClientRect().bottom)
    const composerTop = await composer.evaluate(element => element.getBoundingClientRect().top)
    expect(composerTop).toBeGreaterThanOrEqual(transcriptBottom - 1)
    await transcript.hover()
    await page.mouse.wheel(0, -2000)
    await expect.poll(() => transcript.evaluate(element => element.scrollTop)).toBe(0)
    await page.waitForTimeout(2300)
    expect(await transcript.evaluate(element => element.scrollTop)).toBe(0)
    await page.getByRole('button', { name: zh ? '回到最新回复' : 'Jump to latest reply', exact: true }).click()
    await expect.poll(() => transcript.evaluate(element => element.scrollTop)).toBeGreaterThan(0)
    if (width === 390) {
      await page.getByRole('button', { name: zh ? '会话历史' : 'Conversation history', exact: true }).click()
      const history = page.getByRole('dialog', { name: zh ? '会话历史' : 'Conversation history', exact: true })
      await expect(history.getByRole('button', { name: first.question, exact: false })).toHaveCount(1)
      await history.getByRole('button', { name: first.question, exact: false }).click()
      await expect(history).not.toBeVisible()
    }
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    await page.mouse.move(0, 0)
    await page.screenshot({ path: `/tmp/suma-ai-chat-${language}-${theme}-${width}.png`, fullPage: true })
  })
}
