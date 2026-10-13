import { expect, test, type Locator, type Page } from '@playwright/test'

async function setup(page: Page, language = 'en-US', theme = 'dark') {
  await page.addInitScript(
    ({ language, theme }) => {
      sessionStorage.setItem('suma-demo-session', '1')
      localStorage.setItem('suma-language', language)
      localStorage.setItem('suma-theme', theme)
      localStorage.setItem('suma-node', 'local')
    },
    { language, theme }
  )
}
async function prompt(page: Page, title: string, value: string) {
  const dialog = page.getByRole('dialog', { name: title, exact: true })
  await dialog.getByRole('textbox').fill(value)
  await dialog.getByRole('button', { name: /Confirm|确认/ }).click()
}
async function addService(page: Page, name = 'api', image = 'nginx:alpine') {
  await page.getByRole('button', { name: 'Add service', exact: true }).click()
  const form = page.getByTestId('service-configuration-form')
  await form.getByLabel('Service name', { exact: true }).fill(name)
  await form.getByLabel('Service name', { exact: true }).press('Enter')
  await expect(page.getByRole('heading', { name, exact: true })).toBeVisible()
  await form.getByLabel('Image', { exact: true }).fill(image)
  await form.getByLabel('Image', { exact: true }).blur()
}
async function removeService(page: Page, name: string, zh = false) {
  await page.getByTestId('service-configuration-form').getByRole('button', { name: zh ? '删除配置' : 'Remove configuration', exact: true }).first().click()
  await page.getByRole('dialog', { name: zh ? `删除服务配置 ${name}？` : `Remove service configuration ${name}?`, exact: true }).getByRole('button', { name: zh ? '确认' : 'Confirm', exact: true }).click()
}
async function expectAlignedSelect(page: Page, trigger: Locator, screenshot?: string) {
  await trigger.scrollIntoViewIfNeeded()
  const anchor = await trigger.boundingBox()
  await trigger.click()
  const popup = page.locator('[data-slot="select-content"]:visible')
  await expect(popup).toBeVisible()
  await popup.evaluate(async element => { await Promise.all(element.getAnimations({ subtree: true }).map(animation => animation.finished.catch(() => {}))) })
  const bounds = await popup.boundingBox()
  expect(Math.abs(bounds!.x - anchor!.x)).toBeLessThanOrEqual(1)
  expect(Math.abs(bounds!.width - anchor!.width)).toBeLessThanOrEqual(1)
  expect(bounds!.y >= anchor!.y + anchor!.height - 1 || bounds!.y + bounds!.height <= anchor!.y + 1).toBe(true)
  if (screenshot) { await page.mouse.move(0, 0); await page.screenshot({ path: screenshot, fullPage: true }) }
  await page.keyboard.press('Escape')
  await expect(popup).not.toBeVisible()
}
async function editSource(page: Page, source: string) {
  const workspace = page.getByTestId('project-configuration')
  await workspace.getByRole('tab', { name: /^(Compose|源码)$/, exact: true }).click()
  const reveal = workspace.getByRole('button', {
    name: 'Reveal sensitive source',
    exact: true
  })
  if (await reveal.count()) await reveal.click()
  const input = workspace.getByRole('textbox', {
    name: 'Editor content',
    exact: true
  })
  await input.focus()
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  await page.evaluate((text) => navigator.clipboard.writeText(text), source)
  await page.keyboard.press('Control+A')
  await page.keyboard.press('Control+V')
}

test('create, configure, save independently and review deployment', async ({
  page
}) => {
  await setup(page)
  const errors: string[] = []
  page.on('pageerror', (error) => errors.push(error.stack ?? error.message))
  await page.goto('/projects/new')
  await page
    .getByLabel('Project name · lowercase letters, numbers, -, _')
    .fill('visual-browser')
  await page
    .getByLabel('Project name · lowercase letters, numbers, -, _')
    .blur()
  await page.getByRole('button', { name: 'Add service', exact: true }).click()
  const serviceForm = page.getByTestId('service-configuration-form')
  await expect(page.getByRole('heading', { name: 'Unnamed 1', exact: true })).toBeVisible()
  await expect(serviceForm.getByLabel('Service name', { exact: true })).toHaveValue('')
  await expect(serviceForm.getByLabel('Service name', { exact: true })).toBeFocused()
  await expect(serviceForm.getByLabel('Image', { exact: true })).toHaveValue('')
  await expect(serviceForm.getByRole('combobox', { name: 'Restart policy', exact: true })).toBeVisible()
  await expect(serviceForm.getByRole('tab', { name: 'Storage', exact: true })).toBeVisible()
  await expect(page.getByTestId('new-service-form')).toHaveCount(0)
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await page.getByRole('button', { name: 'Add service', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Unnamed 2', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Unnamed 1', exact: true })).toBeVisible()
  await removeService(page, 'Unnamed 2')
  await serviceForm.getByLabel('Service name', { exact: true }).fill('invalid name')
  await serviceForm.getByLabel('Service name', { exact: true }).blur()
  await expect(serviceForm.getByLabel('Service name', { exact: true })).toHaveAttribute('aria-invalid', 'true')
  await serviceForm.getByLabel('Service name', { exact: true }).fill('api')
  await serviceForm.getByLabel('Service name', { exact: true }).press('Enter')
  await expect(page.getByRole('heading', { name: 'api', exact: true })).toBeVisible()
  await serviceForm.getByLabel('Image', { exact: true }).fill('nginx:alpine')
  await serviceForm.getByLabel('Image', { exact: true }).blur()
  await page.getByRole('button', { name: 'Add service', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Unnamed 3', exact: true })).toBeVisible()
  await serviceForm.getByLabel('Service name', { exact: true }).fill('api')
  await serviceForm.getByLabel('Service name', { exact: true }).blur()
  await expect(serviceForm.getByText('This service name already exists. Choose another name.', { exact: true })).toBeVisible()
  await removeService(page, 'Unnamed 3')
  await expect(page.getByLabel('Image', { exact: true })).toHaveValue('nginx:alpine')
  await page.getByRole('tab', { name: 'Ports', exact: true }).click()
  const port = page.getByTestId('new-port-mapping')
  await port.getByLabel('Host port', { exact: true }).fill('8088')
  await port.getByLabel('Container port', { exact: true }).fill('80')
  await port.getByRole('button', { name: 'Add mapping', exact: true }).click()
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page).toHaveURL(/\/projects\/compose\/visual-browser/)
  await expect(page.getByTestId('project-configuration')).toBeVisible()
  await page.getByRole('button', { name: 'api', exact: true }).click()
  await page.getByRole('tab', { name: 'Ports', exact: true }).click()
  await expect(page.getByLabel('Host port', { exact: true })).toHaveValue(
    '8088'
  )
  await page
    .getByRole('button', { name: 'Pull & recreate', exact: true })
    .click()
  const review = page.getByRole('dialog', {
    name: 'Review deployment',
    exact: true
  })
  await expect(review).toBeVisible()
  await expect(review).toContainText('local')
  await expect(review).toContainText('Pull & recreate')
  await review
    .getByRole('button', { name: 'Confirm deployment', exact: true })
    .click()
  await expect(page.getByRole('dialog', { name: /progress/ })).toBeVisible()
  expect(errors).toEqual([])
})

test('source round trip preserves advanced fields and masks secrets', async ({
  page
}) => {
  await setup(page)
  await page.goto('/projects/new')
  const source =
    '# preserved\nservices:\n  api:\n    image: api:1\n    environment:\n      PASSWORD: browser-private\n    x-extra: { hello: world }\n  db:\n    image: postgres:18\n'
  await editSource(page, source)
  const workspace = page.getByTestId('project-configuration')
  await workspace.getByRole('tab', { name: 'Visual', exact: true }).click()
  await workspace.getByRole('button', { name: 'api', exact: true }).click()
  await page.getByLabel('Image').fill('api:2')
  await page.getByLabel('Image').blur()
  await expect(
    workspace.getByRole('button', { name: 'x-extra', exact: true })
  ).toBeVisible()
  await workspace.getByRole('button', { name: 'x-extra', exact: true }).click()
  await expect(workspace.locator('.monaco-editor')).toBeVisible()
  await workspace.getByRole('tab', { name: 'Visual', exact: true }).click()
  await workspace.getByRole('tab', { name: /^Environment/ }).click()
  await expect(page.getByLabel('Value', { exact: true })).toHaveAttribute(
    'type',
    'password'
  )
  await workspace
    .getByRole('button', { name: 'Hide sensitive source', exact: true })
    .click()
  await workspace.getByRole('tab', { name: /^(Compose|源码)$/, exact: true }).click()
  await expect(workspace.locator('.monaco-editor')).not.toContainText(
    'browser-private'
  )
  await workspace
    .getByRole('button', { name: 'Reveal sensitive source', exact: true })
    .click()
  await expect(workspace.locator('.monaco-editor')).toContainText('api:2')
  await expect(workspace.locator('.monaco-editor')).toContainText('preserved')
  await expect(workspace.locator('.monaco-editor')).toContainText(
    'hello: world'
  )
  await expect(workspace.locator('.monaco-editor')).toContainText('postgres:18')
})

test('shared service configuration is locally read only', async ({ page }) => {
  await setup(page)
  await page.goto('/projects/new')
  await editSource(
    page,
    'x-base: &base\n  restart: always\nservices:\n  shared:\n    <<: *base\n    image: busybox\n  plain:\n    image: nginx\n'
  )
  const workspace = page.getByTestId('project-configuration')
  await workspace.getByRole('tab', { name: 'Visual', exact: true }).click()
  await workspace.getByRole('button', { name: 'shared', exact: true }).click()
  await expect(page.getByLabel('Image')).toBeDisabled()
  await workspace.getByRole('button', { name: 'plain', exact: true }).click()
  await expect(page.getByLabel('Image')).toBeEnabled()
})

test('unsaved node switching asks before discarding', async ({ page }) => {
  await setup(page)
  await page.goto('/projects/new')
  await page
    .getByLabel('Project name · lowercase letters, numbers, -, _')
    .fill('unsaved-app')
  await page
    .getByLabel('Project name · lowercase letters, numbers, -, _')
    .blur()
  await page
    .getByRole('combobox', { name: 'Current Docker node', exact: true })
    .click()
  await page.getByRole('option').filter({ hasText: 'edge-hk' }).click()
  const dialog = page.getByRole('dialog', {
    name: 'Discard unsaved configuration and change node?',
    exact: true
  })
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
  await expect(
    page.getByLabel('Project name · lowercase letters, numbers, -, _')
  ).toHaveValue('unsaved-app')
  await expect(
    page.getByRole('combobox', { name: 'Current Docker node', exact: true })
  ).toContainText('homelab-01')
})

async function changeSavedConfiguration(page: Page, project: string) {
  await page.evaluate(async (name) => {
    const { api } = await import('/src/lib/api.ts')
    const path = `/nodes/local/projects/compose/${name}`
    const current = await api(path)
    await api(path, {
      method: 'PUT',
      body: JSON.stringify({
        compose: current.compose.replace(/image: [^\n]+/, 'image: remote:2'),
        environment: current.environment,
        expected_revision: current.revision
      })
    })
  }, project)
}

test('save conflicts preserve the local draft through refetch and require explicit reload', async ({
  page
}) => {
  await setup(page)
  await page.goto('/projects/compose/gateway-prod')
  const workspace = page.getByTestId('project-configuration')
  await workspace.getByRole('button', { name: 'gateway', exact: true }).click()
  await page.getByLabel('Image').fill('local:2')
  await page.getByLabel('Image').blur()
  await changeSavedConfiguration(page, 'gateway-prod')
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(
    page.getByText(
      'Your local draft is preserved. Compare the saved configuration before reloading.',
      { exact: true }
    )
  ).toBeVisible()
  await expect(page.getByLabel('Image')).toHaveValue('local:2')
  await page.getByRole('button', { name: 'Compare', exact: true }).click()
  const comparison = page.getByRole('dialog', {
    name: 'Saved configuration and local draft',
    exact: true
  })
  await expect(comparison).toBeVisible()
  await expect(comparison).not.toContainText('demo-secret')
  await page.keyboard.press('Escape')
  await page.getByRole('button', { name: 'Reload', exact: true }).click()
  await page
    .getByRole('dialog', {
      name: 'Discard local draft and reload?',
      exact: true
    })
    .getByRole('button', { name: /Confirm/ })
    .click()
  await expect(page.getByLabel('Image')).toHaveValue('remote:2')
})

test('a revision change after deployment review blocks submission and restores editing', async ({
  page
}) => {
  await setup(page)
  await page.goto('/projects/compose/gateway-prod')
  await page
    .getByRole('button', { name: 'Pull & recreate', exact: true })
    .click()
  const review = page.getByRole('dialog', {
    name: 'Review deployment',
    exact: true
  })
  await expect(review).toBeVisible()
  await changeSavedConfiguration(page, 'gateway-prod')
  await review
    .getByRole('button', { name: 'Confirm deployment', exact: true })
    .click()
  await expect(review).not.toBeVisible()
  await expect(page.getByRole('dialog', { name: /progress/ })).not.toBeVisible()
  await page
    .getByTestId('project-configuration')
    .getByRole('button', { name: 'gateway', exact: true })
    .click()
  await expect(page.getByLabel('Image')).toBeEnabled()
  await expect(
    page.getByText('Project configuration changed', { exact: true })
  ).toBeVisible()
})

test('semantic drafts can be saved while failed validation blocks deployment', async ({
  page
}) => {
  await setup(page)
  await page.goto('/projects/new')
  await page
    .getByLabel('Project name · lowercase letters, numbers, -, _')
    .fill('incomplete-browser')
  await page
    .getByLabel('Project name · lowercase letters, numbers, -, _')
    .blur()
  await editSource(page, 'services:\n  app:\n    restart: invalid\n')
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page).toHaveURL(/\/projects\/compose\/incomplete-browser/)
  await page
    .getByRole('button', { name: 'Pull & recreate', exact: true })
    .click()
  await expect(
    page.getByText('Compose validation failed', { exact: true })
  ).toBeVisible()
  await expect(
    page.getByRole('dialog', { name: 'Review deployment', exact: true })
  ).not.toBeVisible()
  await page
    .getByTestId('project-configuration')
    .getByRole('button', { name: 'app', exact: true })
    .click()
  await expect(page.getByLabel('Image')).toBeEnabled()
})

test('command forms, health checks and .env variable references round trip', async ({
  page
}) => {
  await setup(page)
  await page.goto('/projects/new')
  await editSource(
    page,
    'services:\n  app:\n    image: nginx\n    command: [sh, -c, "echo $$HOST"]\n    healthcheck:\n      test: [CMD-SHELL, "exit 0"]\n      interval: 30s\n    environment:\n      APP_HOST: localhost\n'
  )
  const workspace = page.getByTestId('project-configuration')
  await workspace.getByRole('tab', { name: 'Visual', exact: true }).click()
  await workspace.getByRole('button', { name: 'app', exact: true }).click()
  await expect(
    page.getByLabel('One argument per line', { exact: true })
  ).toHaveValue('sh\n-c\necho $$HOST')
  await workspace.getByRole('tab', { name: /^Health checks/ }).click()
  await expect(
    page.getByRole('combobox', { name: 'Check command', exact: true })
  ).toContainText('Run in shell')
  const healthCommand = workspace.getByRole('textbox', {
    name: 'Check command',
    exact: true
  })
  await healthCommand.fill('test -f /tmp/ready')
  await healthCommand.blur()
  await workspace.getByRole('tab', { name: /^Environment/ }).click()
  await page
    .getByRole('button', {
      name: 'Create and reference .env variable',
      exact: true
    })
    .click()
  await prompt(page, 'Create .env variable', 'HOST')
  await expect(
    page.getByRole('combobox', { name: '.env variable', exact: true })
  ).toContainText('HOST')
  await page.getByRole('combobox', { name: 'Value source', exact: true }).click()
  await page.getByRole('option', { name: 'Direct value / expression', exact: true }).click()
  await page.getByLabel('Value', { exact: true }).fill('direct-override')
  await page.getByLabel('Value', { exact: true }).blur()
  await page.getByRole('combobox', { name: 'Value source', exact: true }).click()
  await page.getByRole('option', { name: '.env variable', exact: true }).click()
  const reference = page.getByRole('dialog', { name: 'Select .env variable', exact: true })
  await expect(reference.getByRole('combobox', { name: '.env variable', exact: true })).toContainText('Select a variable')
  await expect(reference.getByRole('button', { name: 'Reference variable', exact: true })).toBeDisabled()
  await reference.getByRole('button', { name: 'Cancel', exact: true }).click()
  await expect(page.getByLabel('Value', { exact: true })).toHaveValue('direct-override')
  await page.getByRole('combobox', { name: 'Value source', exact: true }).click()
  await page.getByRole('option', { name: '.env variable', exact: true }).click()
  await reference.getByRole('combobox', { name: '.env variable', exact: true }).click()
  await page.getByRole('option', { name: 'HOST', exact: true }).click()
  await reference.getByRole('button', { name: 'Reference variable', exact: true }).click()
  await workspace.getByTestId('service-interpolation-variables').locator('summary').click()
  await expect(page.getByLabel('HOST', { exact: true })).toHaveValue(
    'localhost'
  )
  await expect(workspace).toContainText('app')
  await workspace.getByRole('tab', { name: /^(Compose|源码)$/, exact: true }).click()
  await expect(workspace.locator('.monaco-editor')).toContainText('${HOST}')
  await expect(workspace.locator('.monaco-editor')).toContainText('CMD-SHELL')
  await expect(workspace.locator('.monaco-editor')).toContainText(
    'test -f /tmp/ready'
  )
})

test('service resource forms preserve attachments and Compose declarations without a project settings page', async ({
  page
}) => {
  await setup(page)
  await page.goto('/projects/new')
  await addService(page)
  const workspace = page.getByTestId('project-configuration')
  await workspace.getByRole('tab', { name: 'Storage', exact: true }).click()
  const mount = page.getByTestId('new-mount-mapping')
  await mount.getByRole('combobox', { name: 'Type', exact: true }).click()
  await page.getByRole('option', { name: 'Named volume', exact: true }).click()
  await mount.getByLabel('New named volume name', { exact: true }).fill('data')
  await mount.getByLabel('Container path', { exact: true }).fill('/var/lib/app')
  await mount.getByRole('button', { name: 'Add mapping', exact: true }).click()
  await expect(page.getByTestId('mount-mapping-0').getByLabel('Container path', { exact: true })).toHaveValue('/var/lib/app')
  await workspace.getByRole('tab', { name: 'Networks & dependencies', exact: true }).click()
  await page.getByRole('button', { name: 'Add network', exact: true }).click()
  const network = page.getByRole('dialog', { name: 'Add service network', exact: true })
  await network.getByRole('tab', { name: 'Create network', exact: true }).click()
  await network.getByLabel('Network name', { exact: true }).fill('private')
  await network.getByRole('button', { name: 'Connect to service', exact: true }).click()
  await page.getByLabel('Service aliases private · one per line', { exact: true }).fill('internal-api')
  await page.getByLabel('Service aliases private · one per line', { exact: true }).blur()
  await expect(workspace.getByRole('button', { name: 'Project settings', exact: true })).toHaveCount(0)
  await workspace.getByRole('tab', { name: /^(Compose|源码)$/, exact: true }).click()
  await expect(workspace.locator('.monaco-editor')).toContainText(
    'internal-api'
  )
  await expect(workspace.locator('.monaco-editor')).toContainText(
    '/var/lib/app'
  )
})

test('services have independent environment, network and mount forms with existing node resources', async ({ page }) => {
  await setup(page)
  await page.goto('/projects/new')
  const workspace = page.getByTestId('project-configuration')
  const navigation = page.getByTestId('project-service-navigation')
  await expect(navigation.getByText('.env variables · .env', { exact: true })).toHaveCount(0)
  await addService(page, 'api', 'app:1')
  const addVariable = async (value: string) => {
    await workspace.getByRole('tab', { name: 'Environment', exact: true }).click()
    await workspace.getByRole('button', { name: 'Add variable', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: 'Add service environment variable', exact: true })
    await dialog.getByLabel('Variable name', { exact: true }).fill('MODE')
    await dialog.getByLabel('Value', { exact: true }).fill(value)
    await dialog.getByRole('button', { name: 'Add to service', exact: true }).click()
  }
  await addVariable('api-mode')
  await addService(page, 'worker', 'app:1')
  await addVariable('worker-mode')
  await navigation.getByRole('button', { name: 'api', exact: true }).click()
  await workspace.getByRole('tab', { name: 'Environment', exact: true }).click()
  await expect(workspace.getByLabel('Value', { exact: true })).toHaveValue('api-mode')
  await workspace.getByRole('tab', { name: 'Networks & dependencies', exact: true }).click()
  await workspace.getByRole('button', { name: 'Add network', exact: true }).click()
  const network = page.getByRole('dialog', { name: 'Add service network', exact: true })
  await network.getByRole('tab', { name: 'Existing on node', exact: true }).click()
  await network.getByRole('combobox', { name: 'Network on node local', exact: true }).click()
  await page.getByRole('option', { name: 'gateway-prod_default · bridge', exact: true }).click()
  await network.getByRole('checkbox', { name: 'Also keep the default network', exact: true }).uncheck()
  await network.getByRole('button', { name: 'Connect to service', exact: true }).click()
  await expect(workspace.getByRole('button', { name: 'Disconnect network gateway-prod_default', exact: true })).toBeVisible()
  await expect(workspace.getByText('Default network', { exact: true })).toHaveCount(0)
  await workspace.getByRole('tab', { name: 'Storage', exact: true }).click()
  const mount = page.getByTestId('new-mount-mapping')
  // Cancel keeps the declaration out of the Compose document.
  await mount.getByRole('combobox', { name: 'Type', exact: true }).click()
  await page.getByRole('option', { name: 'Named volume', exact: true }).click()
  await mount.getByLabel('New named volume name', { exact: true }).fill('cancelled-volume')
  await mount.getByRole('button', { name: 'Cancel', exact: true }).click()
  await workspace.getByRole('button', { name: 'Add storage mapping', exact: true }).click()
  await expect(mount.getByRole('combobox', { name: 'Type', exact: true })).toContainText('Host path')
  await mount.getByRole('combobox', { name: 'Type', exact: true }).click()
  await page.getByRole('option', { name: 'Named volume', exact: true }).click()
  await expect(mount.getByLabel('New named volume name', { exact: true })).toHaveValue('')
  await mount.getByRole('combobox', { name: 'Source', exact: true }).click()
  await page.getByRole('option', { name: 'On node · old-cache', exact: true }).click()
  await mount.getByLabel('Container path', { exact: true }).fill('/data')
  await mount.getByRole('checkbox', { name: 'Read-only', exact: true }).check()
  await mount.getByRole('button', { name: 'Add mapping', exact: true }).click()
  await expect(page.getByTestId('mount-mapping-0').getByRole('combobox', { name: 'Source', exact: true })).toContainText('old-cache')
  await navigation.getByRole('button', { name: 'worker', exact: true }).click()
  await workspace.getByRole('tab', { name: 'Networks & dependencies', exact: true }).click()
  await expect(workspace.getByText('Default network', { exact: true })).toBeVisible()
  await expect(workspace.getByRole('button', { name: /Disconnect network/ })).toHaveCount(0)
  await workspace.getByRole('button', { name: 'Add network', exact: true }).click()
  await network.getByRole('tab', { name: 'Create network', exact: true }).click()
  await network.getByLabel('Network name', { exact: true }).fill('worker_net')
  await network.getByRole('checkbox', { name: 'Also keep the default network', exact: true }).uncheck()
  await network.getByRole('button', { name: 'Connect to service', exact: true }).click()
  await workspace.getByRole('tab', { name: 'Storage', exact: true }).click()
  await expect(page.getByTestId('mount-mapping-0')).toHaveCount(0)
  await mount.getByRole('combobox', { name: 'Type', exact: true }).click()
  await page.getByRole('option', { name: 'Host path', exact: true }).click()
  await mount.getByLabel('Source', { exact: true }).fill('relative-path')
  await mount.getByLabel('Container path', { exact: true }).fill('/work')
  await mount.getByRole('button', { name: 'Add mapping', exact: true }).click()
  await expect(mount).toContainText('Use an absolute path on the target node without interpolation.')
  await mount.getByLabel('Source', { exact: true }).fill('/data/worker')
  await mount.getByRole('button', { name: 'Add mapping', exact: true }).click()
  await expect(page.getByTestId('mount-mapping-0').getByLabel('Source', { exact: true })).toHaveValue('/data/worker')
  await expect(workspace).not.toContainText('old-cache')
  await navigation.getByRole('button', { name: 'api', exact: true }).click()
  await workspace.getByRole('tab', { name: 'Storage', exact: true }).click()
  const edit = page.getByTestId('mount-mapping-0')
  await expect(edit.getByLabel('Container path', { exact: true })).toHaveValue('/data')
  await edit.getByLabel('Container path', { exact: true }).fill('/api-data')
  await edit.getByRole('button', { name: 'Save mapping', exact: true }).click()
  await expect(workspace.getByRole('button', { name: 'Project settings', exact: true })).toHaveCount(0)
  await expect(workspace).not.toContainText('cancelled-volume')
  await workspace.getByRole('button', { name: 'Remove mount /api-data', exact: true }).click()
  await expect(page.getByTestId('mount-mapping-0')).toHaveCount(0)
  await workspace.getByRole('tab', { name: 'Networks & dependencies', exact: true }).click()
  await workspace.getByRole('button', { name: 'Disconnect network gateway-prod_default', exact: true }).click()
  await expect(workspace.getByText('Default network', { exact: true })).toBeVisible()
  await navigation.getByRole('button', { name: 'worker', exact: true }).click()
  await workspace.getByRole('tab', { name: 'Storage', exact: true }).click()
  await expect(page.getByTestId('mount-mapping-0').getByLabel('Container path', { exact: true })).toBeVisible()
  await workspace.getByRole('tab', { name: 'Environment', exact: true }).click()
  await expect(workspace.getByLabel('Value', { exact: true })).toHaveValue('worker-mode')
  await workspace.getByRole('tab', { name: /^(Compose|源码)$/, exact: true }).click()
  await expect(workspace.locator('.monaco-editor')).toContainText('external: true')
  await expect(workspace.locator('.monaco-editor')).toContainText('old-cache')
  await expect(workspace.locator('.monaco-editor')).toContainText('worker_net')
  await expect(workspace.locator('.monaco-editor')).not.toContainText('cancelled-volume')
})

test('inline service defaults, restart selection, bulk ports and continuous mount mapping', async ({ page }) => {
  await setup(page)
  await page.goto('/projects/new')
  const workspace = page.getByTestId('project-configuration')
  await addService(page, 'api', 'registry.example:5000/team/app')
  await expect(page.getByLabel('Image', { exact: true })).toHaveValue('registry.example:5000/team/app:latest')
  await page.getByLabel('Image', { exact: true }).fill('nginx')
  await page.getByLabel('Image', { exact: true }).blur()
  await expect(page.getByLabel('Image', { exact: true })).toHaveValue('nginx:latest')
  const policy = workspace.getByRole('combobox', { name: 'Restart policy', exact: true })
  await expect(policy).toContainText('Unset')
  await policy.click()
  await page.getByRole('option', { name: 'Restart on failure', exact: true }).click()
  await page.getByLabel('Maximum retries · optional', { exact: true }).fill('3')
  await page.getByLabel('Maximum retries · optional', { exact: true }).blur()
  await workspace.getByRole('tab', { name: 'Ports', exact: true }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  const port = page.getByTestId('new-port-mapping')
  await port.getByLabel('Host port', { exact: true }).fill('8080')
  await port.getByLabel('Container port', { exact: true }).fill('80')
  await port.getByRole('button', { name: 'Add & continue', exact: true }).click()
  await expect(port.getByLabel('Host port', { exact: true })).toHaveValue('')
  await expect(port.getByLabel('Host port', { exact: true })).toBeFocused()
  await port.getByLabel('Host port', { exact: true }).fill('8080')
  await port.getByLabel('Container port', { exact: true }).fill('80')
  await port.getByRole('button', { name: 'Add mapping', exact: true }).click()
  await expect(port).toContainText('Port mapping already exists')
  await port.getByRole('button', { name: 'Cancel', exact: true }).click()
  await workspace.getByRole('button', { name: 'Fill in bulk', exact: true }).click()
  const batch = page.getByTestId('bulk-port-mappings')
  await batch.getByRole('textbox').fill('8443:443\n5353:70000/udp')
  await batch.getByRole('button', { name: 'Add all', exact: true }).click()
  await expect(batch).toContainText('Line 2')
  await expect(page.getByTestId('port-mapping-1')).toHaveCount(0)
  await batch.getByRole('textbox').fill('8443:443\n5353:53/udp')
  await batch.getByRole('button', { name: 'Add all', exact: true }).click()
  await expect(page.getByTestId('port-mapping-1').getByLabel('Host port', { exact: true })).toHaveValue('8443')
  await expect(page.getByTestId('port-mapping-2').getByRole('combobox', { name: 'Protocol', exact: true })).toContainText('UDP')
  await workspace.getByRole('tab', { name: 'Storage', exact: true }).click()
  const mount = page.getByTestId('new-mount-mapping')
  await mount.getByRole('combobox', { name: 'Type', exact: true }).click()
  await page.getByRole('option', { name: 'Named volume', exact: true }).click()
  await mount.getByLabel('New named volume name', { exact: true }).fill('app_data')
  await mount.getByLabel('Container path', { exact: true }).fill('/data')
  await mount.getByRole('button', { name: 'Add & continue', exact: true }).click()
  await expect(mount.getByLabel('Container path', { exact: true })).toHaveValue('')
  await expect(mount.getByLabel('Container path', { exact: true })).toBeFocused()
  await expect(mount.getByRole('combobox', { name: 'Source', exact: true })).toContainText('Configured · app_data')
  await mount.getByLabel('Container path', { exact: true }).fill('/cache')
  await mount.getByRole('checkbox', { name: 'Read-only', exact: true }).check()
  await mount.getByRole('button', { name: 'Add mapping', exact: true }).click()
  await expect(page.getByTestId('mount-mapping-1').getByLabel('Container path', { exact: true })).toHaveValue('/cache')
  await expect(page.getByTestId('mount-mapping-1').getByRole('checkbox', { name: 'Read-only', exact: true })).toBeChecked()
  await workspace.getByRole('tab', { name: /^(Compose|源码)$/, exact: true }).click()
  await expect(workspace.locator('.monaco-editor')).toContainText('on-failure:3')
  await expect(workspace.locator('.monaco-editor')).toContainText('app_data')
})

test('unnamed services configure in the full basics page and retain mappings and dependencies after naming and saving', async ({ page }) => {
  await setup(page)
  await page.goto('/projects/new')
  const workspace = page.getByTestId('project-configuration')
  await page.getByLabel('Project name · lowercase letters, numbers, -, _', { exact: true }).fill('unnamed-services')
  await page.getByLabel('Project name · lowercase letters, numbers, -, _', { exact: true }).blur()
  await workspace.getByRole('button', { name: 'Add service', exact: true }).click()
  await expect(workspace.getByRole('heading', { name: 'Unnamed 1', exact: true })).toBeVisible()
  await page.getByLabel('Service name', { exact: true }).fill('unnamed-1')
  await page.getByLabel('Service name', { exact: true }).press('Enter')
  await expect(page.getByLabel('Service name', { exact: true })).toHaveValue('')
  await expect(page.getByTestId('pending-configuration-form')).toHaveCount(0)
  await page.getByLabel('Image', { exact: true }).fill('nginx')
  await page.getByLabel('Image', { exact: true }).blur()
  await page.getByRole('combobox', { name: 'Restart policy', exact: true }).click()
  await page.getByRole('option', { name: 'Unless manually stopped', exact: true }).click()
  await workspace.getByRole('tab', { name: 'Ports', exact: true }).click()
  const port = page.getByTestId('new-port-mapping')
  await port.getByLabel('Host port', { exact: true }).fill('8080')
  await port.getByLabel('Container port', { exact: true }).fill('80')
  await port.getByRole('button', { name: 'Add mapping', exact: true }).click()
  await addService(page, 'worker', 'busybox')
  await workspace.getByRole('tab', { name: 'Networks & dependencies', exact: true }).click()
  await workspace.getByRole('combobox', { name: 'Dependency service', exact: true }).click()
  await page.getByRole('option', { name: 'Unnamed 1', exact: true }).click()
  await workspace.getByRole('button', { name: 'Add dependency', exact: true }).click()
  await workspace.getByRole('button', { name: 'Unnamed 1', exact: true }).click()
  await expect(page.getByLabel('Service name', { exact: true })).toHaveValue('')
  await expect(page.getByRole('combobox', { name: 'Restart policy', exact: true })).toContainText('Unless manually stopped')
  await page.getByLabel('Service name', { exact: true }).fill('api')
  await page.getByLabel('Service name', { exact: true }).press('Enter')
  await expect(workspace.getByRole('heading', { name: 'api', exact: true })).toBeVisible()
  await workspace.getByRole('button', { name: 'worker', exact: true }).click()
  await workspace.getByRole('tab', { name: 'Networks & dependencies', exact: true }).click()
  await expect(workspace.getByRole('tabpanel', { name: 'Networks & dependencies', exact: true }).getByText('api', { exact: true })).toBeVisible()
  await workspace.getByRole('button', { name: 'api', exact: true }).click()
  await page.getByLabel('Service name', { exact: true }).fill('')
  await page.getByLabel('Service name', { exact: true }).blur()
  await expect(workspace.getByRole('heading', { name: 'Unnamed 3', exact: true })).toBeVisible()
  await workspace.getByRole('tab', { name: /^(Compose|源码)$/, exact: true }).click()
  await expect(workspace.locator('.monaco-editor')).toContainText('unnamed-3')
  await expect(workspace.locator('.monaco-editor')).toContainText('8080')
  await expect(workspace.locator('.monaco-editor')).toContainText('depends_on')
  await workspace.getByRole('tab', { name: 'Visual', exact: true }).click()
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page).toHaveURL(/\/projects\/compose\/unnamed-services/)
  await workspace.getByRole('button', { name: 'Unnamed 3', exact: true }).click()
  await expect(page.getByLabel('Service name', { exact: true })).toHaveValue('')
  await expect(page.getByLabel('Image', { exact: true })).toHaveValue('nginx:latest')
  await workspace.getByRole('tab', { name: 'Ports', exact: true }).click()
  await expect(page.getByLabel('Host port', { exact: true })).toHaveValue('8080')
})

test('unfinished service names and mapping forms protect navigation and cannot save an older draft', async ({ page }) => {
  await setup(page)
  await page.goto('/projects/new')
  await page.getByRole('button', { name: 'Add service', exact: true }).click()
  const form = page.getByTestId('service-configuration-form')
  await form.getByLabel('Image', { exact: true }).fill('nginx')
  await form.getByLabel('Image', { exact: true }).blur()
  await form.getByLabel('Service name', { exact: true }).fill('pending service')
  await page.getByRole('combobox', { name: 'Current Docker node', exact: true }).click()
  await page.getByRole('option').filter({ hasText: 'edge-hk' }).click()
  await page.getByRole('dialog', { name: 'Discard unsaved configuration and change node?', exact: true }).getByRole('button', { name: 'Cancel', exact: true }).click()
  await expect(form.getByLabel('Image', { exact: true })).toHaveValue('nginx:latest')
  await page.getByRole('tab', { name: 'Ports', exact: true }).click()
  await page.getByRole('dialog', { name: 'Discard the unfinished form?', exact: true }).getByRole('button', { name: 'Cancel', exact: true }).click()
  await expect(form.getByLabel('Service name', { exact: true })).toHaveValue('pending service')
  await page.getByTestId('project-configuration').getByRole('tab', { name: 'Compose', exact: true }).click()
  await page.getByRole('dialog', { name: 'Discard the unfinished form?', exact: true }).getByRole('button', { name: 'Cancel', exact: true }).click()
  await expect(form.getByLabel('Service name', { exact: true })).toHaveValue('pending service')
  await form.getByLabel('Service name', { exact: true }).fill('pending-service')
  await form.getByLabel('Service name', { exact: true }).press('Enter')
  await expect(page.getByRole('heading', { name: 'pending-service', exact: true })).toBeVisible()
  await page.getByLabel('Project name · lowercase letters, numbers, -, _', { exact: true }).fill('pending-form-test')
  await page.getByLabel('Project name · lowercase letters, numbers, -, _', { exact: true }).blur()
  await page.getByRole('tab', { name: 'Ports', exact: true }).click()
  const port = page.getByTestId('new-port-mapping')
  await port.getByLabel('Host port', { exact: true }).fill('8080')
  await expect(page.getByRole('button', { name: 'Save', exact: true })).toBeDisabled()
  await expect(page.getByTestId('pending-configuration-form')).toBeVisible()
  await port.getByLabel('Container port', { exact: true }).fill('80')
  await port.getByRole('button', { name: 'Add mapping', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Save', exact: true })).toBeEnabled()
})

test('invalid source remains editable and visual editing resumes after repair', async ({
  page
}) => {
  await setup(page)
  await page.goto('/projects/new')
  const workspace = page.getByTestId('project-configuration')
  await workspace.getByRole('tab', { name: /^(Compose|源码)$/, exact: true }).click()
  const editor = workspace.getByRole('textbox', {
    name: 'Editor content',
    exact: true
  })
  await editor.focus()
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  await page.evaluate(() => navigator.clipboard.writeText('services: ['))
  await page.keyboard.press('Control+A')
  await page.keyboard.press('Control+V')
  await expect(workspace).toContainText('YAML:')
  await expect(workspace.locator('.monaco-editor')).toContainText('services: [')
  await workspace.getByRole('tab', { name: 'Visual', exact: true }).click()
  await expect(
    workspace.getByRole('button', { name: 'Add service', exact: true })
  ).toBeDisabled()
  await editSource(page, 'services: {app: {image: nginx}}\n')
  await workspace.getByRole('tab', { name: 'Visual', exact: true }).click()
  await workspace.getByRole('button', { name: 'app', exact: true }).click()
  await expect(page.getByLabel('Image')).toHaveValue('nginx')
  await expect(page.getByLabel('Image')).toBeEnabled()
})

test('explicit labels and logging forms preserve list syntax, reject duplicates and protect unfinished values', async ({ page }) => {
  await setup(page)
  await page.goto('/projects/new')
  await editSource(page, '# keep\nservices:\n  api:\n    image: nginx\n    labels: ["com.example.role=api"] # keep labels\n    logging:\n      driver: json-file\n      options: {max-size: "10m"}\n  worker: {image: busybox}\n')
  const workspace = page.getByTestId('project-configuration')
  await workspace.getByRole('tab', { name: 'Visual', exact: true }).click()
  await workspace.getByRole('button', { name: 'api', exact: true }).click()
  await workspace.getByRole('tab', { name: 'Advanced', exact: true }).click()
  const labels = page.getByTestId('service-labels')
  await expect(labels.getByRole('heading', { name: 'Labels', exact: true })).toBeVisible()
  await labels.getByRole('button', { name: 'Add label', exact: true }).click()
  const form = page.getByTestId('new-label-form')
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await form.getByLabel('Label key', { exact: true }).fill('com.example.role')
  await form.getByLabel('Label value', { exact: true }).fill('overwrite')
  await form.getByRole('button', { name: 'Add label', exact: true }).click()
  await expect(form).toContainText('This key already exists')
  await expect(labels.getByLabel('com.example.role', { exact: true })).toHaveValue('api')
  await form.getByLabel('Label key', { exact: true }).fill('com.example.team')
  await form.getByLabel('Label value', { exact: true }).fill('platform')
  await workspace.getByRole('tab', { name: 'Ports', exact: true }).click()
  await page.getByRole('dialog', { name: 'Discard the unfinished form?', exact: true }).getByRole('button', { name: 'Cancel', exact: true }).click()
  await form.getByRole('button', { name: 'Add label', exact: true }).click()
  await expect(labels.getByLabel('com.example.team', { exact: true })).toHaveValue('platform')
  await form.getByRole('button', { name: 'Cancel', exact: true }).click()
  const logs = page.getByTestId('service-log-options')
  await expect(logs.getByRole('heading', { name: 'Log options', exact: true })).toBeVisible()
  await logs.getByRole('button', { name: 'Add log option', exact: true }).click()
  const option = page.getByTestId('new-log-option-form')
  await option.getByLabel('Option key', { exact: true }).fill('max-file')
  await option.getByLabel('Option value', { exact: true }).fill('3')
  await option.getByRole('button', { name: 'Add log option', exact: true }).click()
  await option.getByRole('button', { name: 'Cancel', exact: true }).click()
  await workspace.getByRole('tab', { name: /^(Compose|源码)$/, exact: true }).click()
  await expect(workspace.locator('.monaco-editor')).toContainText('com.example.team=platform')
  await expect(workspace.locator('.monaco-editor')).toContainText('# keep labels')
  await expect(workspace.locator('.monaco-editor')).toContainText('max-file')
  await expect(workspace.locator('.monaco-editor')).toContainText('max-size')
})

test('labels, bulk environment input and dependency renaming retain their source forms', async ({
  page
}) => {
  await setup(page)
  await page.goto('/projects/new')
  await editSource(
    page,
    'services:\n  db:\n    image: postgres\n  api:\n    image: nginx\n    labels: ["com.example.role=api"]\n    depends_on:\n      db:\n        condition: service_started\n        restart: true\n'
  )
  const workspace = page.getByTestId('project-configuration')
  await workspace.getByRole('tab', { name: 'Visual', exact: true }).click()
  await workspace.getByRole('button', { name: 'api', exact: true }).click()
  await workspace.getByRole('tab', { name: /^Advanced/ }).click()
  await page.getByLabel('com.example.role', { exact: true }).fill('api-v2')
  await page.getByLabel('com.example.role', { exact: true }).blur()
  await workspace.getByRole('tab', { name: /^Environment/ }).click()
  await page
    .getByRole('button', { name: 'Paste variables', exact: true })
    .click()
  const paste = page.getByRole('dialog', {
    name: 'Paste environment variables',
    exact: true
  })
  await paste
    .getByLabel('KEY=value', { exact: true })
    .fill('PUBLIC=one\nPASSWORD=bulk-private\n')
  await paste.getByRole('button', { name: 'Add', exact: true }).click()
  await expect(workspace.locator('input[type="password"]')).toHaveValue(
    'bulk-private'
  )
  await workspace.getByRole('button', { name: 'db', exact: true }).click()
  await workspace.getByRole('tab', { name: 'Basics', exact: true }).click()
  await page.getByLabel('Service name', { exact: true }).fill('database')
  await page.getByLabel('Service name', { exact: true }).blur()
  await expect(
    workspace.getByRole('heading', { name: 'database', exact: true })
  ).toBeVisible()
  await workspace
    .getByRole('button', { name: 'Remove configuration', exact: true })
    .click()
  await expect(workspace).toContainText('Remove references first: api')
  await workspace.getByRole('tab', { name: /^(Compose|源码)$/, exact: true }).click()
  await expect(workspace.locator('.monaco-editor')).toContainText(
    'com.example.role=api-v2'
  )
  await expect(workspace.locator('.monaco-editor')).toContainText('database:')
  await expect(workspace.locator('.monaco-editor')).toContainText(
    'restart: true'
  )
})

for (const language of ['en-US', 'zh-CN']) {
  test(`replica count defaults, validation and persistence ${language}`, async ({ page }) => {
    await setup(page, language)
    await page.goto('/projects/new')
    const zh = language === 'zh-CN', workspace = page.getByTestId('project-configuration')
    await page.getByLabel(zh ? '项目名 · 小写字母、数字、-、_' : 'Project name · lowercase letters, numbers, -, _', { exact: true }).fill(`replicas-${zh ? 'zh' : 'en'}`)
    await page.getByRole('button', { name: zh ? '添加服务' : 'Add service', exact: true }).click()
    const form = page.getByTestId('service-configuration-form')
    await form.getByLabel(zh ? '服务名称' : 'Service name', { exact: true }).fill('api')
    await form.getByLabel(zh ? '服务名称' : 'Service name', { exact: true }).press('Enter')
    await form.getByLabel(zh ? '镜像' : 'Image', { exact: true }).fill('nginx')
    await form.getByLabel(zh ? '镜像' : 'Image', { exact: true }).blur()
    const count = form.getByLabel(zh ? '副本数量' : 'Replica count', { exact: true })
    await expect(count).toHaveValue('1')
    await workspace.getByRole('tab', { name: zh ? '源码' : 'Compose', exact: true }).click()
    await expect(workspace.locator('.monaco-editor')).not.toContainText('scale:')
    await workspace.getByRole('tab', { name: zh ? '可视化' : 'Visual', exact: true }).click()
    await count.fill('3'); await count.press('Enter')
    const save = page.getByRole('button', { name: zh ? '保存' : 'Save', exact: true })
    await expect(save).toBeEnabled()
    for (const invalid of ['-1', '1.5', 'wrong']) {
      await count.fill(invalid); await count.blur()
      await expect(count).toHaveAttribute('aria-invalid', 'true')
      await expect(save).toBeDisabled()
      await count.press('Escape'); await expect(count).toHaveValue('3')
    }
    await form.getByLabel(zh ? '容器名称' : 'Container name', { exact: true }).fill('fixed-api')
    await form.getByLabel(zh ? '容器名称' : 'Container name', { exact: true }).blur()
    await count.fill('2'); await count.blur()
    await expect(form.getByText(zh ? '请先清空容器名称，再配置多个副本。' : 'Clear the container name before configuring multiple replicas.', { exact: true })).toBeVisible()
    await count.press('Escape')
    await form.getByLabel(zh ? '容器名称' : 'Container name', { exact: true }).fill('')
    await form.getByLabel(zh ? '容器名称' : 'Container name', { exact: true }).blur()
    await count.fill(''); await count.blur(); await expect(count).toHaveValue('1')
    await count.fill('0'); await count.press('Enter'); await expect(count).toHaveValue('0')
    await count.fill('2'); await count.press('Enter')
    await save.click()
    await expect(page).toHaveURL(/\/projects\/compose\/replicas-(en|zh)$/)
    await page.getByRole('button', { name: 'api', exact: true }).click()
    await expect(page.getByLabel(zh ? '副本数量' : 'Replica count', { exact: true })).toHaveValue('2')
    await workspace.getByRole('tab', { name: zh ? '源码' : 'Compose', exact: true }).click()
    await expect(workspace.locator('.monaco-editor')).toContainText('scale: 2')
  })
}

test('replica editing retains legacy deployment settings and protects source-only values', async ({ page }) => {
  await setup(page)
  await page.goto('/projects/new')
  const workspace = page.getByTestId('project-configuration')
  const visual = async (source: string) => {
    await editSource(page, source)
    await workspace.getByRole('tab', { name: 'Visual', exact: true }).click()
    await workspace.getByRole('button', { name: 'api', exact: true }).click()
    return workspace.getByLabel('Replica count', { exact: true })
  }
  const count = await visual('# retained\nservices:\n  api:\n    image: nginx\n    deploy:\n      replicas: 4 # replicas comment\n      resources: {limits: {cpus: "0.5"}}\n')
  await expect(count).toHaveValue('4'); await count.fill('3'); await count.press('Enter')
  await workspace.getByRole('tab', { name: 'Compose', exact: true }).click()
  await expect(workspace.locator('.monaco-editor')).toContainText('replicas: 3 # replicas comment')
  await expect(workspace.locator('.monaco-editor')).not.toContainText('scale:')
  await expect(workspace.locator('.monaco-editor')).toContainText('cpus: "0.5"')
  await visual('services:\n  api: {image: nginx, scale: 2, deploy: {replicas: 3}}\n')
  await expect(count).toBeDisabled()
  await expect(workspace.getByText('The two replica counts conflict. Make them consistent in source first.', { exact: true })).toBeVisible()
  await workspace.getByRole('tab', { name: 'Compose', exact: true }).click()
  await expect(workspace.locator('.monaco-editor')).toContainText('scale: 2')
  await expect(workspace.locator('.monaco-editor')).toContainText('replicas: 3')
  for (const source of ['services:\n  api: {image: nginx, scale: "${COUNT}"}\n', 'x-count: &count 2\nservices:\n  api: {image: nginx, scale: *count}\n']) {
    await visual(source)
    await expect(count).toBeDisabled()
    await expect(count).toHaveValue('')
  }
})

for (const language of ['en-US', 'zh-CN'])
  for (const theme of ['dark', 'light'])
    test(`responsive workspace ${language} ${theme}`, async ({ page }) => {
      await setup(page, language, theme)
      const errors: string[] = []
      page.on('pageerror', (error) => errors.push(error.stack ?? error.message))
      await page.goto('/projects/new')
      const workspace = page.getByTestId('project-configuration')
      await expect(workspace).toBeVisible()
      await editSource(
        page,
        'services:\n  api:\n    image: nginx:alpine\n    scale: 2\n    x-preserved: keep\n    depends_on: {db: {condition: service_healthy}}\n    ports: [{target: 80, published: "8080"}]\n    environment: {APP_MODE: production}\n    networks:\n      backend: {aliases: [internal-api]}\n    volumes: ["app_data:/app/data"]\n  db:\n    image: postgres:18\n    networks: [database]\n    volumes: ["db_data:/var/lib/postgresql"]\nnetworks: {backend: {}, database: {}}\nvolumes: {app_data: {}, db_data: {}}\n'
      )
      await workspace
        .getByRole('tab', {
          name: language === 'zh-CN' ? '可视化' : 'Visual',
          exact: true
        })
        .click()
      await workspace.getByRole('button', { name: 'api', exact: true }).click()
      await expect(
        page.getByRole('textbox', { name: /^(Image|镜像)$/ })
      ).toHaveValue('nginx:alpine')
      expect(
        await page.evaluate(
          () => document.body.scrollWidth <= window.innerWidth
        )
      ).toBe(true)
      await expect(page.locator('html')).toHaveAttribute('data-theme', theme)
      const zh = language === 'zh-CN'
      await expect(workspace.getByTestId('service-configuration-tabs').getByRole('tab')).toHaveText(zh ? ['基础', '端口', '环境变量', '网络与依赖', '存储', '健康检查', '资源限制', '高级'] : ['Basics', 'Ports', 'Environment', 'Networks & dependencies', 'Storage', 'Health checks', 'Resources', 'Advanced'])
      for (const label of zh ? ['容器名称', '工作目录', '运行用户', '启动命令', '入口命令'] : ['Container name', 'Working directory', 'User', 'Command', 'Entrypoint']) await expect(workspace.getByLabel(label, { exact: true })).toBeVisible()
      await expect(workspace.getByLabel(zh ? '副本数量' : 'Replica count', { exact: true })).toHaveValue('2')
      const sourceConfiguration = workspace.getByRole('button', { name: 'x-preserved', exact: true }).locator('..')
      expect((await workspace.getByTestId('service-configuration-tabs').getByRole('tablist').boundingBox())!.height).toBe(32)
      expect((await sourceConfiguration.boundingBox())!.height).toBe(32)
      await page.mouse.move(0, 0)
      await page.screenshot({ path: `/tmp/suma-project-configuration-language-${language}-${theme}.png`, fullPage: true })
      await expect(workspace.getByRole('button', { name: zh ? '项目设置' : 'Project settings', exact: true })).toHaveCount(0)
      const restart = workspace.getByRole('combobox', { name: zh ? '重启策略' : 'Restart policy', exact: true })
      const restartBox = await restart.boundingBox(), inputBox = await workspace.getByLabel(zh ? '容器名称' : 'Container name', { exact: true }).boundingBox()
      expect(Math.abs(restartBox!.y - inputBox!.y)).toBeLessThanOrEqual(1)
      expect(Math.abs(restartBox!.height - inputBox!.height)).toBeLessThanOrEqual(1)
      await expectAlignedSelect(page, restart, zh ? `/tmp/suma-project-restart-menu-${theme}.png` : undefined)
      await restart.focus()
      await restart.press('ArrowDown')
      const failureOption = page.getByRole('option', { name: zh ? '失败时重启' : 'Restart on failure', exact: true })
      await expect(failureOption).toBeVisible()
      await page.keyboard.press('End')
      await expect(failureOption).toHaveAttribute('data-highlighted', '')
      await page.keyboard.press('Enter')
      await expect(restart).toContainText(zh ? '失败时重启' : 'Restart on failure')
      await workspace.getByRole('tab', { name: zh ? '健康检查' : 'Health checks', exact: true }).click()
      await expect(workspace.getByLabel(zh ? '检查间隔' : 'Check interval', { exact: true })).toBeVisible()
      await workspace.getByRole('combobox', { name: zh ? '禁用健康检查' : 'Disable health check', exact: true }).click()
      await expect(page.getByRole('option', { name: zh ? '是' : 'Yes', exact: true })).toBeVisible()
      await expect(page.getByRole('option', { name: zh ? '否' : 'No', exact: true })).toBeVisible()
      await page.keyboard.press('Escape')
      await expect(workspace.getByLabel(zh ? '内存上限' : 'Memory limit', { exact: true })).toHaveCount(0)
      if (zh) {
        await expect(workspace.getByTestId('service-configuration-tabs').getByRole('tablist')).not.toContainText(/Basics|Ports|Environment|Storage|Health checks|Resources|Advanced/)
        await page.mouse.move(0, 0)
        await page.screenshot({ path: `/tmp/suma-project-health-${theme}.png`, fullPage: true })
      }
      await workspace.getByRole('tab', { name: zh ? '资源限制' : 'Resources', exact: true }).click()
      await expect(workspace.getByLabel(zh ? '内存上限' : 'Memory limit', { exact: true })).toBeVisible()
      await expect(workspace.getByLabel(zh ? '检查间隔' : 'Check interval', { exact: true })).toHaveCount(0)
      await workspace.getByRole('tab', { name: zh ? '高级' : 'Advanced', exact: true }).click()
      await expect(workspace.getByTestId('service-labels').getByRole('heading', { name: zh ? '标签' : 'Labels', exact: true })).toBeVisible()
      if (zh) {
        await workspace.getByTestId('service-labels').getByRole('button', { name: '添加标签', exact: true }).click()
        await page.mouse.move(0, 0)
        await page.screenshot({ path: `/tmp/suma-project-advanced-labels-${theme}.png`, fullPage: true })
        await page.getByTestId('new-label-form').getByRole('button', { name: '取消', exact: true }).click()
      }
      await page.getByLabel(zh ? '项目名 · 小写字母、数字、-、_' : 'Project name · lowercase letters, numbers, -, _', { exact: true }).fill('service-workspace')
      await page.getByLabel(zh ? '项目名 · 小写字母、数字、-、_' : 'Project name · lowercase letters, numbers, -, _', { exact: true }).blur()
      await workspace.getByRole('tab', { name: zh ? '网络与依赖' : 'Networks & dependencies', exact: true }).click()
      await expect(workspace.getByLabel(zh ? '服务别名 backend · 每行一个' : 'Service aliases backend · one per line', { exact: true })).toHaveValue('internal-api')
      await expect(workspace.getByRole('combobox', { name: zh ? '启动条件' : 'Startup condition', exact: true })).toContainText(zh ? '服务健康' : 'Service healthy')
      await expect(workspace.getByText('database', { exact: true })).toHaveCount(0)
      await workspace.evaluate(async element => { await Promise.all(element.getAnimations({ subtree: true }).map(animation => animation.finished.catch(() => {}))) })
      if (zh) await page.screenshot({ path: `/tmp/suma-project-service-workspace-${theme}.png`, fullPage: true })
      await workspace.getByRole('button', { name: zh ? '添加网络' : 'Add network', exact: true }).click()
      const network = page.getByRole('dialog', { name: zh ? '添加服务网络' : 'Add service network', exact: true })
      await network.getByRole('tab', { name: zh ? '节点已有' : 'Existing on node', exact: true }).click()
      await expectAlignedSelect(page, network.getByRole('combobox', { name: zh ? '节点 local 上的网络' : 'Network on node local', exact: true }))
      await network.getByRole('combobox', { name: zh ? '节点 local 上的网络' : 'Network on node local', exact: true }).click()
      await expect(page.getByRole('option', { name: 'gateway-prod_default · bridge', exact: true })).toBeVisible()
      await page.keyboard.press('Escape')
      await network.getByRole('button', { name: zh ? '取消' : 'Cancel', exact: true }).click()
      await workspace.getByRole('tab', { name: zh ? '端口' : 'Ports', exact: true }).click()
      await workspace.evaluate(async element => { await Promise.all(element.getAnimations({ subtree: true }).map(animation => animation.finished.catch(() => {}))) })
      const protocol = workspace.getByRole('combobox', { name: zh ? '协议' : 'Protocol', exact: true })
      const protocolBox = await protocol.boundingBox(), portBox = await workspace.getByLabel(zh ? '容器端口' : 'Container port', { exact: true }).boundingBox()
      expect(Math.abs(protocolBox!.y - portBox!.y)).toBeLessThanOrEqual(1)
      await expectAlignedSelect(page, protocol)
      if (zh) await page.screenshot({ path: `/tmp/suma-project-port-mappings-${theme}.png`, fullPage: true })
      await workspace.getByRole('tab', { name: zh ? '存储' : 'Storage', exact: true }).click()
      await workspace.evaluate(async element => { await Promise.all(element.getAnimations({ subtree: true }).map(animation => animation.finished.catch(() => {}))) })
      await expectAlignedSelect(page, workspace.getByRole('combobox', { name: zh ? '类型' : 'Type', exact: true }))
      await expectAlignedSelect(page, workspace.getByRole('combobox', { name: zh ? '来源' : 'Source', exact: true }))
      if (zh) await page.screenshot({ path: `/tmp/suma-project-storage-mappings-${theme}.png`, fullPage: true })
      await workspace.getByRole('button', { name: zh ? '添加服务' : 'Add service', exact: true }).click()
      const serviceForm = page.getByTestId('service-configuration-form')
      await expect(serviceForm.getByLabel(zh ? '服务名称' : 'Service name', { exact: true })).toBeFocused()
      await expect(serviceForm.getByLabel(zh ? '镜像' : 'Image', { exact: true })).toBeVisible()
      await serviceForm.evaluate(async (element) => {
        await Promise.all(element.getAnimations({ subtree: true }).map(animation => animation.finished.catch(() => {})))
      })
      if (language === 'zh-CN' && theme === 'dark')
        await page.screenshot({
          path: '/tmp/suma-project-basics-unnamed.png',
          fullPage: true
        })
      await removeService(page, zh ? '未命名1' : 'Unnamed 1', zh)
      await workspace.getByRole('tab', { name: zh ? '存储' : 'Storage', exact: true }).click()
      await page.setViewportSize({ width: 390, height: 844 })
      await expect(
        page.getByRole('combobox', {
          name: language === 'zh-CN' ? '当前服务' : 'Current service',
          exact: true
        })
      ).toBeVisible()
      const tabList = workspace.getByTestId('service-configuration-tabs').getByRole('tablist')
      const storagePanel = workspace.getByRole('tabpanel', { name: zh ? '存储' : 'Storage', exact: true })
      const tabBounds = await tabList.boundingBox(), panelBounds = await storagePanel.boundingBox()
      expect(panelBounds!.y).toBeGreaterThanOrEqual(tabBounds!.y + tabBounds!.height)
      expect(tabBounds!.height).toBeGreaterThanOrEqual(32)
      for (const tab of await tabList.getByRole('tab').all()) {
        const bounds = (await tab.boundingBox())!
        expect(bounds.x).toBeGreaterThanOrEqual(tabBounds!.x)
        expect(bounds.x + bounds.width).toBeLessThanOrEqual(tabBounds!.x + tabBounds!.width)
        expect(bounds.y + bounds.height).toBeLessThanOrEqual(tabBounds!.y + tabBounds!.height)
      }
      expect((await sourceConfiguration.boundingBox())!.height).toBeGreaterThanOrEqual(32)
      expect(
        await page.evaluate(
          () => document.body.scrollWidth <= window.innerWidth
        )
      ).toBe(true)
      if (zh) await page.screenshot({ path: `/tmp/suma-project-service-mobile-${theme}.png`, fullPage: true })
      await workspace.getByRole('button', { name: zh ? '添加存储映射' : 'Add storage mapping', exact: true }).click()
      const mountForm = page.getByTestId('new-mount-mapping')
      await expect(mountForm.getByLabel(zh ? '容器路径' : 'Container path', { exact: true })).toBeVisible()
      await expect(mountForm.getByRole('combobox', { name: zh ? '类型' : 'Type', exact: true })).toContainText(zh ? '主机路径' : 'Host path')
      await expectAlignedSelect(page, mountForm.getByRole('combobox', { name: zh ? '类型' : 'Type', exact: true }))
      await mountForm.getByRole('combobox', { name: zh ? '类型' : 'Type', exact: true }).click()
      await page.getByRole('option', { name: zh ? '主机路径' : 'Host path', exact: true }).click()
      await expect(mountForm.getByLabel(zh ? '来源' : 'Source', { exact: true })).toBeVisible()
      const mountBounds = await mountForm.boundingBox()
      expect(mountBounds!.x).toBeGreaterThanOrEqual(0)
      expect(mountBounds!.x + mountBounds!.width).toBeLessThanOrEqual(390)
      expect(mountBounds!.y).toBeGreaterThanOrEqual(0)
      await mountForm.scrollIntoViewIfNeeded()
      await mountForm.getByRole('button', { name: zh ? '取消' : 'Cancel', exact: true }).click()
      await workspace.getByRole('button', { name: zh ? '添加服务' : 'Add service', exact: true }).click()
      await expect(serviceForm.getByRole('combobox', { name: zh ? '重启策略' : 'Restart policy', exact: true })).toBeVisible()
      await serviceForm.getByLabel(zh ? '服务名称' : 'Service name', { exact: true }).fill('worker')
      await serviceForm.getByLabel(zh ? '服务名称' : 'Service name', { exact: true }).press('Enter')
      await serviceForm.getByLabel(zh ? '镜像' : 'Image', { exact: true }).fill(' busybox:latest ')
      const bounds = await serviceForm.boundingBox()
      expect(bounds).not.toBeNull()
      expect(bounds!.x).toBeGreaterThanOrEqual(0)
      expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(390)
      expect(bounds!.y).toBeGreaterThanOrEqual(0)
      await serviceForm.scrollIntoViewIfNeeded()
      if (zh && theme === 'dark') await page.screenshot({ path: '/tmp/suma-project-basics-mobile.png', fullPage: true })
      await serviceForm.getByLabel(zh ? '镜像' : 'Image', { exact: true }).blur()
      await expect(serviceForm).toBeVisible()
      await expect(workspace.getByRole('heading', { name: 'worker', exact: true })).toBeVisible()
      await expect(page.getByRole('textbox', { name: /^(Image|镜像)$/ })).toHaveValue('busybox:latest')
      expect(errors).toEqual([])
    })
