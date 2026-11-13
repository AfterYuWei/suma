import { expect, test } from '@playwright/test'

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => {
    localStorage.setItem('suma-language', 'en-US')
    localStorage.setItem('suma-theme', 'dark')
    localStorage.setItem('suma-node', 'local')
    sessionStorage.setItem('suma-demo-session', '1')
  })
})

test('demo check and publication retain original business IDs and node children', async ({ page }) => {
  await page.goto('/tasks?tab=schedules')
  const ids = await page.evaluate(async () => {
    // @ts-expect-error Vite serves the real Demo API module in the browser.
    const { api } = await import('/src/lib/api.ts')
    const checked = await api('/delivery-projects/gateway-prod/sync', { method: 'POST', body: '{}' })
    let task = await api(`/tasks/${checked.id}`)
    for (let i = 0; ['pending', 'running'].includes(task.status) && i < 10; i++) {
      await new Promise(resolve => setTimeout(resolve, 100)); task = await api(`/tasks/${checked.id}`)
    }
    const before = await api(`/delivery-records/by-task/${checked.id}`)
    const released = await api(`/delivery-projects/gateway-prod/releases/${before.releases[0].id}/deploy`, { method: 'POST', body: '{}' })
    const retained = await api(`/delivery-records/by-task/${checked.id}`)
    return { checked: checked.id, published: released.task.id, recordID: released.record_id, releaseID: before.releases[0].id, retainedID: retained.releases[0].id }
  })
  expect(ids.retainedID).toBe(ids.releaseID)
  // The Demo is in memory. Navigate through the app so the accepted operation
  // remains in the same session rather than reinitializing its fixture module.
  await page.getByRole('tab', { name: 'Control plane', exact: true }).click()
  await page.getByRole('tab', { name: 'History', exact: true }).click()
  await page.getByText('Deliver gateway-prod', { exact: true }).click()
  const task = page.getByRole('dialog')
  await expect(task.getByTestId('task-delivery-evidence')).toContainText(`#${ids.recordID}`)
  await expect(task.getByRole('button', { name: /Deliver gateway-prod to local/ })).toBeVisible()
  await task.getByRole('button', { name: /Deliver gateway-prod to edge-hk/ }).click()
  await expect(task.getByRole('button', { name: 'View parent task', exact: true })).toBeVisible()
  await expect(task.getByTestId('task-delivery-evidence')).toContainText(`#${ids.recordID}`)
})

test('real demo module supports image check, task result and retained trigger history', async ({ page }) => {
  const errors: string[] = []; page.on('pageerror', error => errors.push(error.message))
  await page.goto('/tasks?tab=schedules')
  const rule = page.getByRole('row').filter({ hasText: 'media-stack image check' })
  await rule.getByRole('button', { name: 'Rule actions' }).click()
  await page.getByRole('menuitem', { name: 'Edit', exact: true }).click()
  const editor = page.getByRole('dialog')
  await editor.getByRole('button', { name: 'Check now', exact: true }).click()
  await expect(editor.getByText('server', { exact: true })).toBeVisible({ timeout: 10000 })
  await editor.getByRole('button', { name: 'Close', exact: true }).click()
  await rule.getByRole('button', { name: 'Rule actions' }).click()
  await page.getByRole('menuitem', { name: 'Trigger history', exact: true }).click()
  await page.getByRole('button', { name: 'View task', exact: true }).click()
  const task = page.getByRole('dialog').filter({ has: page.getByRole('heading', { name: /image.update media-stack/ }) })
  await expect(task.getByText('server', { exact: true })).toBeVisible()
  await task.getByText('Show full result JSON', { exact: true }).click()
  await expect(task.locator('details').filter({ has: page.getByText('Show full result JSON', { exact: true }) }).locator('pre')).toContainText('media-stack')
  expect(errors).toEqual([])
})

test('real demo cleanup editor creates a paused business-owned rule', async ({ page }) => {
  await page.goto('/tasks?tab=schedules')
  await page.getByRole('button', { name: 'New automation', exact: true }).click()
  await page.getByRole('dialog').getByRole('combobox').first().click()
  await page.getByRole('option', { name: 'Storage cleanup', exact: true }).click()
  await page.getByRole('button', { name: 'Configure automation', exact: true }).click()
  const editor = page.getByRole('dialog')
  await editor.getByRole('button', { name: 'Save policy', exact: true }).click()
  await expect(editor.getByRole('status').filter({ hasText: 'Policy saved.' })).toBeVisible()
  await editor.getByRole('button', { name: 'Close', exact: true }).click()
  const rule = page.getByRole('row').filter({ hasText: 'Storage cleanup local' })
  await expect(rule).toBeVisible()
  await expect(rule.getByText('Paused', { exact: true })).toBeVisible()
})
