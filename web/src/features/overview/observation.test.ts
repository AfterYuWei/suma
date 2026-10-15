import { describe, expect, it } from 'vitest'
import { appendNodeHistory, memoryTopFive, nodeIdentity, nodeLoads, nodeMetricSummary, visibleNodeHistory } from './observation'
import type { FleetContainer } from './types'
import type { DockerNode } from '../../lib/nodes'

describe('node observations', () => {
  it('summarizes actual observations without treating gaps as zeros or capping CPU', () => {
    const points = [{ time: 1, cpu: 0, memory: 20 }, { time: 2, cpu: null, memory: null }, { time: 3, cpu: 180, memory: 40 }]
    expect(nodeMetricSummary(points, 'cpu')).toEqual({ count: 2, average: 90, peak: 180 })
    expect(nodeMetricSummary(points, 'memory')).toEqual({ count: 2, average: 30, peak: 40 })
    expect(nodeMetricSummary([], 'cpu')).toEqual({ count: 0, average: null, peak: null })
  })
  it('normalizes aggregate CPU and memory, retains zero and over-capacity values', () => {
    expect(nodeLoads({ metrics_available: true, container_cpu_percent: 480, container_memory_bytes: 512 }, 4, 1024)).toEqual({ cpu: 120, memory: 50 })
    expect(nodeLoads({ metrics_available: true, container_cpu_percent: 0, container_memory_bytes: 0 }, 4, 1024)).toEqual({ cpu: 0, memory: 0 })
    expect(nodeLoads({ metrics_available: false, container_cpu_percent: 80, container_memory_bytes: 512 }, 4, 1024)).toEqual({ cpu: null, memory: null })
    expect(nodeLoads({ metrics_available: true, container_cpu_percent: NaN, container_memory_bytes: -1 }, 0, 0)).toEqual({ cpu: null, memory: null })
  })
  it('keeps five minutes, rejects duplicate times, and inserts gaps without zeros', () => {
    const first = { time: 10_000, cpu: 0, memory: 12 }
    expect(appendNodeHistory([first], first)).toEqual([first])
    const point = { time: 45_001, cpu: 120, memory: null }
    expect(appendNodeHistory([first], point)).toEqual([first, { time: 10_001, cpu: null, memory: null }, point])
    expect(appendNodeHistory([first], { ...point, time: 310_001 })).toEqual([{ ...point, time: 310_001 }])
    expect(appendNodeHistory([first], { ...point, time: NaN })).toEqual([first])
  })
  it('expires stale guides and old history even without a new response', () => {
    const first = { time: 10_000, cpu: 0, memory: 12 }
    expect(visibleNodeHistory([first], 40_000)).toEqual([first])
    expect(visibleNodeHistory([first], 40_001)).toEqual([first, { time: 40_000, cpu: null, memory: null }])
    expect(visibleNodeHistory([first], 310_001)).toEqual([])
    expect(first.cpu).toBe(0)
  })
  it('isolates connection changes independently of card size', () => {
    const node = { id: 'edge', updated_at: 'config-v1', agent_connected_at: 'session-1' } as DockerNode
    expect(nodeIdentity(node)).not.toEqual(nodeIdentity({ ...node, agent_connected_at: 'session-2' }))
    expect(nodeIdentity(node)).not.toEqual(nodeIdentity({ ...node, updated_at: 'config-v2' }))
    expect(nodeIdentity(node)).not.toEqual(nodeIdentity({ ...node, id: 'other' }))
  })
  it('ranks valid memory samples, with stable name and ID ties, and caps at five', () => {
    const rows = [
      { id: 'bad', name: 'bad', available: false, memory_bytes: 999 },
      { id: 'unknown', name: 'unknown', available: true, memory_bytes: NaN },
      ...['b', 'a', 'd', 'c', 'f', 'e'].map((id) => ({ id, name: 'same', available: true, memory_bytes: 100 })),
      { id: 'large', name: 'largest', available: true, memory_bytes: 200 },
    ] as FleetContainer[]
    expect(memoryTopFive(rows).map(row => row.id)).toEqual(['large', 'a', 'b', 'c', 'd'])
    expect(rows[0].id).toBe('bad')
  })
})
