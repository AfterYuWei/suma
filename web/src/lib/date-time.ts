export type Instant = string | number | Date | null | undefined

// Reuse formatters across log rows; keep the cache bounded as users change zones.
const formatters = new Map<string, Intl.DateTimeFormat>()

export function systemTimeZone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
}

export function validTimeZone(value: string): boolean {
  if (!value || value === 'Local' || /^[+-]/.test(value)) return false
  try {
    new Intl.DateTimeFormat('en', { timeZone: value }).format(0)
    return true
  } catch {
    return false
  }
}

export function resolveTimeZone(value?: string): string {
  return value && value !== 'system' && validTimeZone(value) ? value : systemTimeZone()
}

export function formatInstant(value: Instant, timeZone: string, locale: string, kind: 'datetime' | 'time' | 'date', options?: Intl.DateTimeFormatOptions): string {
  if (value === null || value === undefined || value === '') return '—'
  const date = new Date(value)
  if (!Number.isFinite(date.getTime())) return '—'
  const zone = timeZone === 'system' ? systemTimeZone() : timeZone
  const key = JSON.stringify([locale, zone, kind, options])
  let formatter = formatters.get(key)
  if (!formatter) {
    const defaults: Intl.DateTimeFormatOptions = kind === 'time'
      ? { hour: 'numeric', minute: '2-digit', second: '2-digit' }
      : kind === 'date'
        ? { year: 'numeric', month: 'numeric', day: 'numeric' }
        : { year: 'numeric', month: 'numeric', day: 'numeric', hour: 'numeric', minute: '2-digit', second: '2-digit' }
    formatter = new Intl.DateTimeFormat(locale, { ...defaults, ...options, timeZone: resolveTimeZone(zone) })
    if (formatters.size >= 64) {
      const oldest = formatters.keys().next().value
      if (oldest !== undefined) formatters.delete(oldest)
    }
    formatters.set(key, formatter)
  }
  return formatter.format(date)
}

function wallParts(date: Date, timeZone: string): number[] {
  const parts = new Intl.DateTimeFormat('en-GB', { timeZone, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' }).formatToParts(date)
  return ['year', 'month', 'day', 'hour', 'minute', 'second'].map(type => Number(parts.find(part => part.type === type)?.value))
}

// datetime-local has no offset. Interpret its wall time in the chosen zone,
// reject nonexistent DST times, and choose the earlier match for a repeated hour.
export function wallTimeToISO(value: string, timeZone: string): string | null {
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})(?::(\d{2}))?$/.exec(value)
  if (!match || !validTimeZone(timeZone)) return null
  const desired = match.slice(1).map(Number)
  desired[5] = Number(match[6] || 0)
  const target = Date.UTC(desired[0], desired[1] - 1, desired[2], desired[3], desired[4], desired[5])
  const calendar = new Date(target)
  if ([calendar.getUTCFullYear(), calendar.getUTCMonth() + 1, calendar.getUTCDate(), calendar.getUTCHours(), calendar.getUTCMinutes(), calendar.getUTCSeconds()].some((value, index) => value !== desired[index])) return null
  let candidate = target
  for (let i = 0; i < 5; i++) {
    const p = wallParts(new Date(candidate), timeZone)
    const observed = Date.UTC(p[0], p[1] - 1, p[2], p[3], p[4], p[5])
    const correction = target - observed
    if (!correction) break
    candidate += correction
  }
  for (let shift = -120; shift <= 120; shift += 30) {
    const date = new Date(candidate + shift * 60000)
    if (wallParts(date, timeZone).every((value, index) => value === desired[index])) return date.toISOString()
  }
  return null
}
