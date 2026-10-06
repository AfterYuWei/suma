import { expect, test, type Page } from '@playwright/test'

async function setup(page: Page, outcome: 'partial' | 'retry' | 'not_called' | 'invalid_arguments', language = 'en-US', theme = 'dark') {
  await page.addInitScript(({ language, theme }) => {
    sessionStorage.setItem('suma-demo-session', '1')
    localStorage.setItem('suma-language', language)
    localStorage.setItem('suma-theme', theme)
    localStorage.setItem('suma-node', 'local')
  }, { language, theme })
  // Mock the API boundary while preserving the real settings form and mutation.
  await page.route(/\/src\/lib\/api\.ts(?:\?t=\d+)?$/, route => route.fulfill({ contentType: 'text/javascript', body: `
    export * from '/src/lib/api.ts?connection-result-original';
    import { api as originalApi, ApiError } from '/src/lib/api.ts?connection-result-original';
    let attempts = 0;
    export async function api(path, init) {
      if (path !== '/ai/settings/test') return originalApi(path, init);
      attempts++;
      await new Promise(resolve => setTimeout(resolve, 1200));
      const outcome = ${JSON.stringify(outcome)};
      if (outcome === 'retry' && attempts === 1) throw new ApiError('model service returned HTTP 401; check the API key', 20801, 422);
      const result = { text: true, tool_capable: outcome === 'retry', summary_only: outcome !== 'retry', model: 'probe-model', duration_ms: 1250, text_response: 'Connection confirmed.' };
      if (outcome !== 'retry') result.tool_failure = outcome === 'not_called' || outcome === 'invalid_arguments' ? outcome : 'request_failed';
      if (outcome === 'partial') result.tool_error = 'model service returned HTTP 400';
      return result;
    }
  ` }))
  await page.goto('/settings#ai')
  const zh = language === 'zh-CN'
  await page.getByRole('button', { name: zh ? '配置模型' : 'Configure models', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: zh ? '配置模型' : 'Configure models', exact: true })
  await dialog.getByLabel(zh ? '手动添加模型' : 'Add model IDs manually', { exact: true }).fill('probe-model')
  await dialog.getByRole('button', { name: zh ? '添加' : 'Add', exact: true }).click()
  await dialog.getByRole('button', { name: zh ? '应用' : 'Apply', exact: true }).click()
  await page.getByRole('button', { name: zh ? '保存 AI 设置' : 'Save AI settings', exact: true }).click()
  await expect(page.getByText(zh ? 'AI 设置已保存' : 'AI settings saved', { exact: true })).toBeVisible()
}

for (const language of ['en-US', 'zh-CN']) for (const theme of ['dark', 'light']) for (const width of [390, 1440]) {
  test(`partial connection result ${language} ${theme} ${width}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    await setup(page, 'partial', language, theme)
    const zh = language === 'zh-CN'
    await page.getByRole('button', { name: zh ? '测试连接' : 'Test connection', exact: true }).click()
    const result = page.getByRole('region', { name: zh ? '连接测试结果' : 'Connection test result', exact: true })
    await expect(result.getByText(zh ? '正在测试连接' : 'Testing connection', { exact: true })).toBeVisible()
    await expect(page.getByRole('button', { name: zh ? '测试中…' : 'Testing…', exact: true })).toBeDisabled()
    await expect(page.getByLabel(zh ? '服务地址' : 'Service URL', { exact: true })).toBeDisabled()
    await expect(page.getByText(zh ? 'AI 设置已保存' : 'AI settings saved', { exact: true })).toHaveCount(0)
    await expect(result.getByText(zh ? '连接正常，工具调用未通过' : 'Connection works; tool verification did not pass', { exact: true })).toBeVisible()
    await expect(result.getByText(zh ? '通过' : 'Passed', { exact: true })).toHaveCount(1)
    await expect(result.getByText(zh ? '未通过' : 'Did not pass', { exact: true })).toHaveCount(1)
    await expect(result.getByText('Connection confirmed.', { exact: true })).toBeVisible()
    await expect(result.getByText('model service returned HTTP 400', { exact: true })).toBeVisible()
    await expect(result.getByText(zh ? '测试模型：probe-model' : 'Tested model：probe-model', { exact: false })).toBeVisible()
    await expect(result.getByText(zh ? 'AI 当前仅生成摘要' : 'AI currently produces summaries only', { exact: false })).toBeVisible()
    await result.scrollIntoViewIfNeeded()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await result.screenshot({ path: `/tmp/suma-ai-connection-result-${language}-${theme}-${width}.png` })
  })
}

test('a failed connection displays its error and retry replaces it with a successful result', async ({ page }) => {
  await setup(page, 'retry')
  await page.getByRole('button', { name: 'Test connection', exact: true }).click()
  const result = page.getByRole('region', { name: 'Connection test result', exact: true })
  await expect(result.getByText('Connection test failed', { exact: true })).toBeVisible()
  await expect(result.getByText('model service returned HTTP 401; check the API key', { exact: true })).toBeVisible()
  await expect(result.getByText('Text and tool calling verified', { exact: true })).toHaveCount(0)
  await page.getByRole('button', { name: 'Test connection', exact: true }).click()
  await expect(result.getByText('Testing connection', { exact: true })).toBeVisible()
  await expect(result.getByText('Connection test failed', { exact: true })).toHaveCount(0)
  await expect(result.getByText('Text and tool calling verified', { exact: true })).toBeVisible()
  await expect(result.getByText('Passed', { exact: true })).toHaveCount(2)
  await expect(result.getByText('Connection confirmed.', { exact: true })).toBeVisible()
})

test('a model that does not call the probe has a specific reason; editing clears the old result', async ({ page }) => {
  await setup(page, 'not_called')
  await page.getByRole('button', { name: 'Test connection', exact: true }).click()
  const result = page.getByRole('region', { name: 'Connection test result', exact: true })
  await expect(result.getByText('The model responded without calling the test tool.', { exact: true })).toBeVisible()
  await page.getByLabel('Service URL', { exact: true }).fill('https://changed.example.com/v1')
  await expect(result).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Test connection', exact: true })).toBeDisabled()
})

for (const language of ['en-US', 'zh-CN']) test(`invalid tool arguments explain the required marker ${language}`, async ({ page }) => {
  await setup(page, 'invalid_arguments', language)
  const zh = language === 'zh-CN'
  await page.getByRole('button', { name: zh ? '测试连接' : 'Test connection', exact: true }).click()
  const result = page.getByRole('region', { name: zh ? '连接测试结果' : 'Connection test result', exact: true })
  await expect(result.getByText(zh ? '测试工具的参数无效，预期为 {"message":"suma_connection_test"}。' : 'The test tool arguments are invalid; {"message":"suma_connection_test"} was expected.', { exact: true })).toBeVisible()
  await expect(result.getByText(zh ? '通过' : 'Passed', { exact: true })).toHaveCount(1)
  await expect(result.getByText(zh ? '未通过' : 'Did not pass', { exact: true })).toHaveCount(1)
})
