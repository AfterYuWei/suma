import { describe, expect, it } from 'vitest'
import { networkRate, rateLabel } from './network-rate'
import type { ContainerMetrics } from './types'

const sample = (seconds: number, rx = 0, tx = 0): ContainerMetrics => ({
  id: 'app', available: true, cpu_percent: 0, memory_bytes: 0, uptime_seconds: seconds,
  started_at: '2026-10-12T00:00:00Z', sampled_at: new Date(Date.UTC(2026, 9, 12, 0, 1, seconds)).toISOString(),
  network_rx_bytes: rx, network_tx_bytes: tx,
})

describe('network rates', () => {
  it('uses actual sample time rather than the polling interval', () => {
    expect(networkRate(sample(7, 7000, 1400), sample(0))).toEqual({ status: 'ready', receive: 1000, transmit: 200 })
    expect(networkRate(sample(5), sample(0))).toEqual({ status: 'ready', receive: 0, transmit: 0 })
  })
  it('waits for two valid samples', () => {
    expect(networkRate(sample(0))).toEqual({ status: 'sampling' })
    expect(networkRate(sample(5), { ...sample(0), available: false })).toEqual({ status: 'sampling' })
  })
  it('resets on restart even when new counters exceed old counters', () => {
    expect(networkRate({ ...sample(5, 1000), started_at: '2026-10-12T00:01:03Z' }, sample(0, 10))).toEqual({ status: 'sampling' })
  })
  it('resets both directions when either counter rolls back', () => {
    expect(networkRate(sample(5, 10, 1000), sample(0, 100, 0))).toEqual({ status: 'sampling' })
    expect(networkRate(sample(5, 1000, 10), sample(0, 0, 100))).toEqual({ status: 'sampling' })
  })
  it('marks failed reads unavailable and recovers through a new baseline', () => {
    const failed = { ...sample(5), available: false }
    expect(networkRate(failed, sample(0))).toEqual({ status: 'unavailable' })
    expect(networkRate(sample(10), failed)).toEqual({ status: 'sampling' })
    expect(networkRate(sample(15, 100), sample(10))).toEqual({ status: 'ready', receive: 20, transmit: 0 })
  })
  it('rejects missing timestamps and resets on long or invalid intervals', () => {
    expect(networkRate({ ...sample(0), sampled_at: undefined })).toEqual({ status: 'unavailable' })
    expect(networkRate({ ...sample(0), started_at: undefined })).toEqual({ status: 'unavailable' })
    expect(networkRate(sample(16), sample(0))).toEqual({ status: 'sampling' })
    expect(networkRate(sample(0), sample(0))).toEqual({ status: 'sampling' })
    expect(networkRate(sample(0), sample(5))).toEqual({ status: 'sampling' })
    expect(networkRate(sample(15, 150), sample(0))).toEqual({ status: 'ready', receive: 10, transmit: 0 })
  })
  it('formats zero and byte units', () => {
    expect([0, 512, 1024, 1024 ** 2].map(rateLabel)).toEqual(['0 B/s', '512 B/s', '1.0 KB/s', '1.0 MB/s'])
  })
})
