import { useQuery } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { api } from '../../lib/api'
import { resolveTimeZone } from '../../lib/date-time'
import { TimeZoneContext } from '../../lib/time-zone'

export function TimeZoneProvider({ children }: { children: ReactNode }) {
  const settings = useQuery({ queryKey: ['settings'], queryFn: () => api<Record<string, string>>('/settings'), staleTime: 60_000 })
  return <TimeZoneContext value={resolveTimeZone(settings.data?.['general.timezone'])}>{children}</TimeZoneContext>
}
