import { createContext, useContext, useMemo } from 'react'
import { useI18n } from './i18n'
import { formatInstant, systemTimeZone, type Instant } from './date-time'

export const TimeZoneContext = createContext<string | undefined>(undefined)

export function useDateTime() {
  const zone = useContext(TimeZoneContext)
  const { language } = useI18n()
  const timeZone = zone || systemTimeZone()
  return useMemo(() => ({
    timeZone,
    formatDateTime: (value: Instant, options?: Intl.DateTimeFormatOptions) => formatInstant(value, timeZone, language, 'datetime', options),
    formatTime: (value: Instant, options?: Intl.DateTimeFormatOptions) => formatInstant(value, timeZone, language, 'time', options),
    formatDate: (value: Instant, options?: Intl.DateTimeFormatOptions) => formatInstant(value, timeZone, language, 'date', options),
  }), [timeZone, language])
}
