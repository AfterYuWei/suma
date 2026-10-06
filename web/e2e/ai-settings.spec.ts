import { expect, test, type Locator, type Page } from '@playwright/test'

async function setup(page: Page, language = 'en-US', theme = 'dark') {
  await page.addInitScript(({ language, theme }) => {
    sessionStorage.setItem('suma-demo-session', '1')
    localStorage.setItem('suma-language', language)
    localStorage.setItem('suma-theme', theme)
    localStorage.setItem('suma-node', 'local')
  }, { language, theme })
}

async function savedSettings(page: Page) {
  return page.evaluate(async () => {
    // @ts-expect-error Vite serves the API module to the browser.
    const { api } = await import('/src/lib/api.ts')
    return api('/ai/settings')
  })
}

async function alignedPopup(page: Page, trigger: Locator, screenshot: string) {
  await trigger.scrollIntoViewIfNeeded()
  await trigger.click()
  const popup = page.locator('[data-slot="select-content"]:visible')
  await expect(popup).toBeVisible()
  await popup.evaluate(async element => { await Promise.all(element.getAnimations({ subtree: true }).map(animation => animation.finished.catch(() => {}))) })
  const anchor = (await trigger.boundingBox())!
  const bounds = (await popup.boundingBox())!
  expect(Math.abs(bounds.x - anchor.x)).toBeLessThanOrEqual(1)
  expect(Math.abs(bounds.width - anchor.width)).toBeLessThanOrEqual(1)
  expect(bounds.y >= anchor.y + anchor.height + 2 || bounds.y + bounds.height <= anchor.y - 2).toBe(true)
  expect(bounds.x).toBeGreaterThanOrEqual(0)
  expect(bounds.x + bounds.width).toBeLessThanOrEqual(page.viewportSize()!.width)
  await page.mouse.move(0, 0)
  const top = Math.max(0, Math.min(anchor.y, bounds.y) - 24)
  const bottom = Math.min(page.viewportSize()!.height, Math.max(anchor.y + anchor.height, bounds.y + bounds.height) + 4)
  await page.screenshot({ path: screenshot, clip: { x: Math.max(0, anchor.x - 2), y: top, width: Math.min(anchor.width + 4, page.viewportSize()!.width - Math.max(0, anchor.x - 2)), height: bottom - top } })
  return popup
}

for (const language of ['en-US', 'zh-CN']) for (const theme of ['dark', 'light']) for (const width of [390, 1440]) {
  test(`AI model settings ${language} ${theme} ${width}`, async ({ page }) => {
    await setup(page, language, theme)
    await page.setViewportSize({ width, height: 900 })
    const zh = language === 'zh-CN'
    const errors: string[] = []
    page.on('pageerror', error => errors.push(error.message))
    await page.goto('/settings#ai')
    const enabled = page.getByRole('switch', { name: zh ? '启用 AI 运维' : 'Enable AI operations', exact: true })
    await expect(enabled).not.toBeChecked()
    await enabled.check()
    await expect(page.getByRole('button', { name: zh ? '保存 AI 设置' : 'Save AI settings', exact: true })).toBeDisabled()
    await page.getByLabel(zh ? '服务地址' : 'Service URL', { exact: true }).fill('https://gateway.example.com/custom/v1/')
    await expect(page.getByRole('combobox', { name: zh ? '模型协议' : 'Model protocol', exact: true })).toHaveCount(0)
    await expect(page.getByText('Responses', { exact: true })).toBeVisible()
    await page.getByText(zh ? '什么时候需要 /v1？查看实际请求地址' : 'When is /v1 needed? View request URLs', { exact: true }).click()
    await expect(page.getByText('POST https://gateway.example.com/custom/v1/responses', { exact: true })).toBeVisible()
    await expect(page.getByText('GET https://gateway.example.com/custom/v1/models', { exact: true })).toBeVisible()
    await page.getByRole('button', { name: zh ? '自动探测模型' : 'Auto-detect models', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: zh ? '配置模型' : 'Configure models', exact: true })
    await expect(dialog.getByRole('checkbox', { name: 'deepseek-v4-pro', exact: true })).toBeVisible()
    await dialog.getByRole('checkbox', { name: zh ? '选择所有搜索结果' : 'Select all search results', exact: true }).check()
    await dialog.getByLabel(zh ? '搜索模型' : 'Search models', { exact: true }).fill('pro')
    await expect(dialog.getByRole('checkbox', { name: 'deepseek-v4-flash', exact: true })).toHaveCount(0)
    await dialog.getByRole('checkbox', { name: zh ? '选择所有搜索结果' : 'Select all search results', exact: true }).uncheck()
    await dialog.getByLabel(zh ? '搜索模型' : 'Search models', { exact: true }).fill('')
    await expect(dialog.getByRole('checkbox', { name: 'deepseek-v4-flash', exact: true })).toBeChecked()
    await dialog.getByRole('checkbox', { name: 'deepseek-v4-pro', exact: true }).check()
    await dialog.getByLabel(zh ? '手动添加模型' : 'Add model IDs manually', { exact: true }).fill('custom-model, deepseek-v4-pro')
    await dialog.getByRole('button', { name: zh ? '添加' : 'Add', exact: true }).click()
    await expect(dialog.getByText(zh ? '已选 4 个模型' : '4 models selected', { exact: true })).toBeVisible()
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await page.screenshot({ path: `/tmp/suma-ai-model-dialog-${language}-${theme}-${width}.png` })
    await dialog.getByRole('button', { name: zh ? '应用' : 'Apply', exact: true }).click()
    await expect(dialog).not.toBeVisible()
    await alignedPopup(page, page.getByRole('combobox', { name: zh ? '默认模型' : 'Default model', exact: true }), `/tmp/suma-ai-model-select-${language}-${theme}-${width}.png`)
    await page.getByRole('option', { name: 'deepseek-v4-pro', exact: true }).click()
    await page.getByRole('checkbox', { name: 'homelab-01', exact: true }).check()
    await page.getByRole('button', { name: zh ? '保存 AI 设置' : 'Save AI settings', exact: true }).click()
    await expect(page.getByText(zh ? 'AI 设置已保存' : 'AI settings saved', { exact: true })).toBeVisible()
    const saved = await savedSettings(page)
    expect(saved.models).toEqual(['deepseek-v4-flash', 'deepseek-v4-flash-vision-exp', 'deepseek-v4-pro', 'custom-model'])
    expect(saved.model).toBe('deepseek-v4-pro')
    expect(saved.protocol).toBe('responses')
    await page.getByRole('button', { name: zh ? '测试连接' : 'Test connection', exact: true }).click()
    await expect(page.getByText(zh ? '文本和工具调用通过' : 'Text and tool calling verified', { exact: true })).toBeVisible()
    await page.reload()
    await expect(page.getByRole('combobox', { name: zh ? '默认模型' : 'Default model', exact: true })).toContainText('deepseek-v4-pro')
    await expect(page.getByRole('button', { name: zh ? '移除模型 custom-model' : 'Remove model custom-model', exact: true })).toBeVisible()
    const channel = page.getByRole('combobox', { name: zh ? '聊天渠道' : 'Chat channel', exact: true })
    const popup = await alignedPopup(page, channel, `/tmp/suma-ai-channel-select-${language}-${theme}-${width}.png`)
    await expect(popup.getByRole('option', { name: zh ? '请选择' : 'Select', exact: true })).toHaveAttribute('aria-selected', 'true')
    await page.keyboard.press('Escape')
    await expect(popup).not.toBeVisible()
    await expect(channel).toBeFocused()
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await page.locator('main > div').first().evaluate(element => { element.scrollTop = 0 })
    await page.screenshot({ path: `/tmp/suma-ai-settings-${language}-${theme}-${width}.png` })
    expect(errors).toEqual([])
  })
}

test('cancel preserves model selection; removing the default chooses another model', async ({ page }) => {
  await setup(page)
  await page.goto('/settings#ai')
  await page.getByRole('button', { name: 'Configure models', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'Configure models', exact: true })
  await dialog.getByLabel('Add model IDs manually', { exact: true }).fill('first, second, first')
  await dialog.getByLabel('Add model IDs manually', { exact: true }).press('Enter')
  await expect(dialog.getByText('2 models selected', { exact: true })).toBeVisible()
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
  await expect(page.getByRole('combobox', { name: 'Default model', exact: true })).toHaveCount(0)
  await page.getByRole('button', { name: 'Configure models', exact: true }).click()
  await dialog.getByLabel('Add model IDs manually', { exact: true }).fill('first, second')
  await dialog.getByRole('button', { name: 'Add', exact: true }).click()
  await dialog.getByRole('button', { name: 'Apply', exact: true }).click()
  await page.getByRole('button', { name: 'Remove model first', exact: true }).click()
  await expect(page.getByRole('combobox', { name: 'Default model', exact: true })).toContainText('second')
  await page.getByRole('button', { name: 'Save AI settings', exact: true }).click()
  expect((await savedSettings(page)).models).toEqual(['second'])
})

test('HTTP and private-network permission switches persist with origin troubleshooting', async ({ page }) => {
  await setup(page)
  await page.goto('/settings#ai')
  await page.getByRole('switch', { name: 'Allow insecure AI connections (HTTP)', exact: true }).check()
  await page.getByRole('switch', { name: 'Allow an internal model endpoint', exact: true }).check()
  await page.getByLabel('Service URL', { exact: true }).fill('http://127.0.0.1:11434/v1')
  await page.getByText('HTTP pages and Request origin is not allowed', { exact: true }).click()
  await expect(page.getByText('AI works from same-origin HTTP pages.', { exact: false })).toBeVisible()
  await page.getByRole('button', { name: 'Save AI settings', exact: true }).click()
  const saved = await savedSettings(page)
  expect(saved.allow_private).toBe(true)
  expect(saved.allow_insecure).toBe(true)
  await page.reload()
  await expect(page.getByRole('switch', { name: 'Allow insecure AI connections (HTTP)', exact: true })).toBeChecked()
})

test('model discovery failure supports manual fallback without saving a key', async ({ page }) => {
  await setup(page)
  await page.goto('/settings#ai')
  await page.getByLabel('API key', { exact: true }).fill('draft-only-key')
  await page.evaluate(async () => {
    // @ts-expect-error Vite serves the API module to the browser.
    const { api } = await import('/src/lib/api.ts')
    const settings = await api('/ai/settings')
    await api('/ai/settings', { method: 'PUT', body: JSON.stringify(settings) })
  })
  await page.getByRole('button', { name: 'Configure models', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'Configure models', exact: true })
  await dialog.getByRole('button', { name: 'Discover', exact: true }).click()
  await expect(dialog.getByRole('alert')).toBeVisible()
  await expect(dialog.getByText('If /models is unsupported, add model IDs below.', { exact: false })).toBeVisible()
  await dialog.getByLabel('Add model IDs manually', { exact: true }).fill('fallback-model')
  await dialog.getByRole('button', { name: 'Add', exact: true }).click()
  await expect(dialog.getByRole('checkbox', { name: 'fallback-model', exact: true })).toBeChecked()
  expect((await savedSettings(page)).has_secret).toBe(false)
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
  await expect(page.getByLabel('API key', { exact: true })).toHaveValue('draft-only-key')
})

test('switching defaults clears previous connection verification and adding alternatives preserves it', async ({ page }) => {
  await setup(page)
  await page.goto('/settings#ai')
  await page.getByRole('button', { name: 'Configure models', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'Configure models', exact: true })
  await dialog.getByLabel('Add model IDs manually', { exact: true }).fill('first, second')
  await dialog.getByRole('button', { name: 'Add', exact: true }).click()
  await dialog.getByRole('button', { name: 'Apply', exact: true }).click()
  await page.getByRole('button', { name: 'Save AI settings', exact: true }).click()
  await page.getByRole('button', { name: 'Test connection', exact: true }).click()
  await expect(page.getByText('Text and tool calling verified', { exact: true })).toBeVisible()
  await page.getByRole('combobox', { name: 'Default model', exact: true }).click()
  await page.getByRole('option', { name: 'second', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Test connection', exact: true })).toBeDisabled()
  await page.getByRole('button', { name: 'Save AI settings', exact: true }).click()
  await expect(page.getByText('Tool calling not verified', { exact: true })).toBeVisible()
  expect((await savedSettings(page)).tool_capable).toBe(false)
  await page.getByRole('button', { name: 'Test connection', exact: true }).click()
  await expect(page.getByText('Text and tool calling verified', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Configure models', exact: true }).click()
  await dialog.getByLabel('Add model IDs manually', { exact: true }).fill('third')
  await dialog.getByRole('button', { name: 'Add', exact: true }).click()
  await dialog.getByRole('button', { name: 'Apply', exact: true }).click()
  await page.getByRole('button', { name: 'Save AI settings', exact: true }).click()
  await expect(page.getByText('Tool calling available', { exact: true })).toBeVisible()
  expect((await savedSettings(page)).tool_capable).toBe(true)
})

test('removed protocol is rejected by settings and legacy demo configuration starts disabled', async ({ page }) => {
  await setup(page)
  await page.addInitScript(() => {
    if (!sessionStorage.getItem('suma-demo-operations')) sessionStorage.setItem('suma-demo-operations', JSON.stringify({
      serial: 100,
      settings: { version: 1, enabled: true, protocol: 'chat_completions', endpoint: 'https://example.com/v1', model: 'legacy-model', models: ['legacy-model'], allow_private: false, allow_insecure: false, node_ids: ['local'], auto_events: [], max_concurrent: 2, daily_auto_limit: 20, max_tool_calls: 8, log_lines: 500, log_bytes: 65536, approval_minutes: 15, has_secret: true, tool_capable: true },
      channels: [], rules: [], deliveries: [], runs: [], operations: [], bindings: [], audits: [], inbox: { items: [], unread: 0 },
    }))
  })
  await page.goto('/settings#ai')
  await expect(page.getByRole('switch', { name: 'Enable AI operations', exact: true })).not.toBeChecked()
  await expect(page.getByText('Responses', { exact: true })).toBeVisible()
  await expect(page.getByRole('combobox', { name: 'Model protocol', exact: true })).toHaveCount(0)
  const migrated = await savedSettings(page)
  expect(migrated).toMatchObject({ version: 2, protocol: 'responses', enabled: false, tool_capable: false, endpoint: 'https://example.com/v1', model: 'legacy-model', has_secret: true })
  const rejected = await page.evaluate(async () => {
    // @ts-expect-error Vite serves the API module to the browser.
    const { api } = await import('/src/lib/api.ts')
    const settings = await api('/ai/settings')
    try { await api('/ai/settings', { method: 'PUT', body: JSON.stringify({ ...settings, protocol: 'chat_completions' }) }); return null }
    catch (error) { const failure = error as { status: number; message: string }; return { status: failure.status, message: failure.message } }
  })
  expect(rejected).toMatchObject({ status: 422, message: 'Only Responses protocol is supported' })
  await page.reload()
  expect((await savedSettings(page)).version).toBe(2)
})
