import { describe, expect, it } from 'vitest'
import { localInput, metricValue, monitoringCSV, parseInput } from './range'
import type { QueryResult } from './types'

describe('monitoring time and export', () => {
  it('round-trips the application timezone and rejects nonexistent DST times', () => {
    const time = Date.parse('2026-10-13T00:00:00Z')
    expect(localInput(time, 'Asia/Shanghai')).toBe('2026-10-13T08:00')
    expect(parseInput('2026-10-13T08:00', 'Asia/Shanghai')).toBe(time)
    expect(() => parseInput('2026-03-08T02:30', 'America/New_York')).toThrow()
    expect(() => parseInput('invalid', 'Asia/Shanghai')).toThrow()
  })
  it('preserves missing values, CPU above 100%, unlimited limits and rate units', () => {
    expect(metricValue('cpu', 200)).toBe('200.0%')
    expect(metricValue('memory', null)).toBe('—')
    expect(metricValue('memory_limit', 0)).toBe('∞')
    expect(metricValue('network_rx', 1024)).toBe('1.0 KiB/s')
  })
  it('exports nulls, extrema, identity and units while escaping CSV formulas', () => {
    const result = { series: [{ instance: { node_id: 'node', engine_id: 'engine', container_id: 'id', name: '=formula' }, step_seconds: 5, points: [{ time: 'time', started_at: 'start', segment: 'segment', values: { cpu: { avg: null, min: 200, max: 400, count: 2 } } }] }] } as unknown as QueryResult
    const csv = monitoringCSV(result)
    expect(csv).toContain('engine_id')
    expect(csv).toContain('"\'=formula"')
    expect(csv).toContain('"cpu","%","","200","400","2"')
  })
})
