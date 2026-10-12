import { describe, expect, it } from 'vitest'
import { appendHistory, effectiveHealth, isAbnormal, selectContainers } from './observation'
import { mergeEvents } from './events'
import { defaultContainerView } from '../../stores/container-view'
import type { ContainerEvent, ContainerRuntime, ContainerSummary } from './types'

const row = (id: string, state = 'running'): ContainerSummary => ({ id, name: id, state, image: 'app:latest', command: '', created: '2026-10-12T00:00:00Z', status: '', labels: {}, ports: [], cpu_percent: 0, memory_bytes: 0, uptime_seconds: 0 })
const runtime = (id: string, patch: Partial<ContainerRuntime> = {}): ContainerRuntime => ({ id, health_status: 'healthy', restart_count: 0, memory_limit_bytes: 0, ...patch })
describe('container observation semantics', () => {
  it('does not treat restart history or absent data as abnormal and does not invent OOM', () => {
    expect(isAbnormal(row('a'), runtime('a', { restart_count: 10 }))).toBe(false)
    expect(isAbnormal(row('a'))).toBe(false)
    expect(isAbnormal(row('a', 'exited'), runtime('a', { exit_code: 137, oom_killed: false }))).toBe(true)
    expect(effectiveHealth(row('a', 'exited'), runtime('a'))).toBe('inactive')
  })
  it('sorts unknown values last in either direction and excludes unlimited memory from ratios', () => {
    const rows = [row('a'), row('b'), row('c', 'exited')]
    const metas = new Map([['a', runtime('a')], ['b', runtime('b', { memory_limit_bytes: 100 })]])
    const samples = new Map(rows.map(r => [r.id, { available: true, cpu_percent: r.id === 'a' ? 2 : 10, memory_bytes: 50, uptime_seconds: 10, rate: { status: 'sampling' as const } }]))
    expect(selectContainers(rows, { ...defaultContainerView, sort: 'cpu' }, metas, samples, false).map(r => r.id)).toEqual(['b', 'a', 'c'])
    expect(selectContainers(rows, { ...defaultContainerView, sort: 'cpu', descending: false }, metas, samples, false).map(r => r.id)).toEqual(['a', 'b', 'c'])
    expect(selectContainers(rows, { ...defaultContainerView, sort: 'memory_ratio' }, metas, samples, false)[0].id).toBe('b')
  })
  it('combines filters before pagination and distinguishes unconfigured/unknown health', () => {
    const rows = [row('a'), row('b')], metas = new Map([['a', runtime('a', { health_status: 'none' })]])
    expect(selectContainers(rows, { ...defaultContainerView, health: ['unknown'] }, metas, new Map(), false).map(r => r.id)).toEqual(['b'])
    expect(selectContainers(rows, { ...defaultContainerView, states: ['running'], health: ['none'], search: 'a' }, metas, new Map(), false).map(r => r.id)).toEqual(['a'])
  })
  it('breaks gaps, discards old data, resets restart segments and ignores duplicate samples', () => {
    const first = { time: 1000, cpu: 1, memory: 10, startedAt: 'one' }
    expect(appendHistory([first], first)).toEqual([first])
    expect(appendHistory([first], { ...first, time: 20000 })[1].cpu).toBeNull()
    expect(appendHistory([first], { ...first, time: 6000, startedAt: 'two' })).toHaveLength(1)
    expect(appendHistory([first], { ...first, time: 400000 })).toHaveLength(1)
  })
  it('deduplicates replay and preserves event nanosecond ordering beyond number precision', () => {
    const e = (id: string, time: string): ContainerEvent => ({ id, time_nano: time, time: '2026-10-12T00:00:00Z', node_id: 'local', container_id: 'a', action: 'start', exit_code: null })
    const a = e('a', '1791763200000000001'), b = e('b', '1791763200000000002')
    expect(mergeEvents([a], [a, b]).map(e => e.id)).toEqual(['b', 'a'])
  })
})
