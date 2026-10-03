import { describe, expect, it } from 'vitest'
import { formatInstant, resolveTimeZone, systemTimeZone, validTimeZone, wallTimeToISO } from './date-time'

describe('application timezone', () => {
  it('follows this device unless an explicit zone is saved', () => {
    expect(resolveTimeZone('system')).toBe(systemTimeZone())
    expect(resolveTimeZone()).toBe(systemTimeZone())
    expect(resolveTimeZone('Asia/Shanghai')).toBe('Asia/Shanghai')
    expect(validTimeZone('Not/AZone')).toBe(false)
    expect(validTimeZone('Local')).toBe(false)
    expect(validTimeZone('+08:00')).toBe(false)
  })
  it('formats the same instant across calendar boundaries and languages', () => {
    const instant = '2026-10-03T00:30:00Z'
    const options = { hourCycle: 'h23' as const, hour: '2-digit' as const }
    expect(formatInstant(instant, 'Asia/Shanghai', 'en-GB', 'datetime', options)).toBe('03/10/2026, 08:30:00')
    expect(formatInstant(instant, 'America/New_York', 'en-GB', 'datetime', options)).toBe('02/10/2026, 20:30:00')
    expect(formatInstant(instant, 'Asia/Shanghai', 'zh-CN', 'time', options)).toBe('08:30:00')
    expect(formatInstant(instant, 'America/New_York', 'en-GB', 'date')).toBe('02/10/2026')
  })
  it('handles missing and invalid instants without rendering Invalid Date', () => {
    for (const value of [null, undefined, '', 'bad']) expect(formatInstant(value, 'UTC', 'en', 'datetime')).toBe('—')
    expect(formatInstant(0, 'UTC', 'en-GB', 'date')).toBe('01/01/1970')
  })
  it('converts custom log ranges in the chosen zone, including fractional offsets', () => {
    expect(wallTimeToISO('2026-10-03T08:00', 'Asia/Shanghai')).toBe('2026-10-03T00:00:00.000Z')
    expect(wallTimeToISO('2026-10-03T08:00:15', 'Asia/Kolkata')).toBe('2026-10-03T02:30:15.000Z')
    expect(wallTimeToISO('2026-10-03T08:00', 'Pacific/Chatham')).toBe('2026-10-02T18:15:00.000Z')
  })
  it('rejects nonexistent DST hours and resolves overlaps to the earlier instant', () => {
    expect(wallTimeToISO('2026-03-08T02:30', 'America/New_York')).toBeNull()
    expect(wallTimeToISO('2026-11-01T01:30', 'America/New_York')).toBe('2026-11-01T05:30:00.000Z')
    expect(wallTimeToISO('2026-04-05T01:45', 'Australia/Lord_Howe')).toBe('2026-04-04T14:45:00.000Z')
  })
  it('rejects invalid dates, zones and range syntax', () => {
    for (const value of ['2026-02-30T08:00', '2026-10-03T24:00', '2026-10-03T08:60', 'bad', '']) expect(wallTimeToISO(value, 'UTC')).toBeNull()
    expect(wallTimeToISO('2026-10-03T08:00', 'Not/AZone')).toBeNull()
  })
})
