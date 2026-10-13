const guards = new Set<() => Promise<boolean>>()

export function registerProjectNavigationGuard(next: () => Promise<boolean>) {
  guards.add(next)
  return () => { guards.delete(next) }
}

export async function allowProjectNodeChange() {
  for (const guard of guards) if (!await guard()) return false
  return true
}
