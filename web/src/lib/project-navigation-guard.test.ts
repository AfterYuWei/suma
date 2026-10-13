import { expect, it } from 'vitest'
import { allowProjectNodeChange, registerProjectNavigationGuard } from './project-navigation-guard'

it('composes project and container file draft guards and removes only the owning guard', async () => {
  const calls: string[] = []
  const project = registerProjectNavigationGuard(async () => { calls.push('project'); return true })
  const file = registerProjectNavigationGuard(async () => { calls.push('file'); return false })
  try {
    expect(await allowProjectNodeChange()).toBe(false)
    expect(calls).toEqual(['project', 'file'])
    project()
    calls.length = 0
    expect(await allowProjectNodeChange()).toBe(false)
    expect(calls).toEqual(['file'])
  } finally { project(); file() }
  expect(await allowProjectNodeChange()).toBe(true)
})
