let guard: (() => Promise<boolean>) | undefined

export function registerProjectNavigationGuard(next: () => Promise<boolean>) {
  guard = next
  return () => {
    if (guard === next) guard = undefined
  }
}

export async function allowProjectNodeChange() {
  return guard ? guard() : true
}
