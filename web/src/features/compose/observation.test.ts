import { describe, expect, it } from 'vitest'
import { aggregate, defaultProjectView, groupServices, observationMaps, selectProjects, type ProjectEntry } from './observation'
import type { ContainerMetrics, ContainerRuntime, ContainerSummary } from '../containers/types'
import type { ProjectSummary } from './types'
const now = Date.parse('2026-10-13T00:00:00Z')
const row = (id: string, patch: Partial<ContainerSummary> = {}): ContainerSummary => ({ id, name: id, image: 'app:v1', state: 'running', status: '', command: '', created: '', cpu_percent: 0, memory_bytes: 0, uptime_seconds: 0, ports: [], labels: { 'com.docker.compose.service': 'app' }, ...patch })
const meta = (id: string, patch: Partial<ContainerRuntime> = {}): ContainerRuntime => ({ id, state: 'running', health_status: 'healthy', restart_count: 0, memory_limit_bytes: 0, ...patch })
const sample = (id: string, patch: Partial<ContainerMetrics> = {}): ContainerMetrics => ({ id, available: true, cpu_percent: 120, memory_bytes: 100, uptime_seconds: 1, network_rx_bytes: 0, network_tx_bytes: 0, sampled_at: new Date(now).toISOString(), ...patch })
describe('project observations', () => {
 it('searches and sorts by the CD display name while retaining native identities', () => {
  const entry = (name: string, deliveryName?: string): ProjectEntry => ({ project: { name, delivery_project_name: deliveryName, source: 'external', status: 'stopped' } as ProjectSummary, containers: [], available: true, summary: aggregate([], { runtime: new Map(), metrics: new Map() }) })
  const entries = [entry('suma-cd-1', 'eizhu_admin'), entry('another-project')]
  expect(selectProjects(entries, { ...defaultProjectView, search: 'eizhu' }).map(e => e.project.name)).toEqual(['suma-cd-1'])
  expect(selectProjects(entries, { ...defaultProjectView, search: 'suma-cd-1' }).map(e => e.project.name)).toEqual(['suma-cd-1'])
  expect(selectProjects(entries, { ...defaultProjectView, sort: 'name' }).map(e => e.project.name)).toEqual(['another-project', 'suma-cd-1'])
 })
 it('groups normal replicas and undeployed declarations without merging one-off containers or image variants', () => {
  const groups = groupServices([row('a'), row('b', { image: 'app:v2' }), row('once', { labels: { 'com.docker.compose.service': 'worker', 'com.docker.compose.oneoff': 'True' } })], ['app', 'worker'])
  expect(groups.map(g => [g.name, g.containers.length, g.images])).toEqual([['app', 2, ['app:v1', 'app:v2']], ['worker', 0, []]])
 })
 it('sums valid per-field samples, preserves real zero and permits CPU above 100 percent', () => {
  const rows = [row('a'), row('b'), row('stopped', { state: 'exited' })]
  const obs = observationMaps(rows, [meta('a'), meta('b')], [sample('a'), sample('b', { cpu_percent: 0, memory_available: false }), sample('stopped')], now)
  expect(aggregate(rows, obs)).toMatchObject({ cpu: 120, memory: 100, cpuCoverage: 2, memoryCoverage: 1, running: 2, total: 3 })
  expect(aggregate([row('missing')], obs)).toMatchObject({ cpu: null, memory: null, abnormal: 0, unknown: 1 })
 })
 it('distinguishes actual image identities even when replicas use the same tag', () => {
  const rows = [row('a', { image_id: 'sha256:first' }), row('b', { image_id: 'sha256:second' }), row('c')]
  const groups = groupServices(rows, [])
  expect(groups[0].images).toEqual(['app:v1 · sha256:first', 'app:v1 · sha256:second'])
  expect(groupServices([rows[0], { ...rows[1], image_id: 'sha256:first' }], [])[0].images).toEqual(['app:v1'])
 })
 it('rejects expired, mismatched and pre-restart observations without inventing normal health', () => {
  const rows = [row('a'), row('b'), row('c')]
  const obs = observationMaps(rows, [meta('a', { state: 'exited', health_status: 'unhealthy' }), meta('b', { observed_at: new Date(now - 31_000).toISOString() }), meta('c', { started_at: 'new' })], [sample('a', { sampled_at: new Date(now - 16_000).toISOString() }), sample('b', { available: false }), sample('c', { started_at: 'old' })], now)
  expect(aggregate(rows, obs)).toMatchObject({ cpu: null, memory: null, abnormal: 0, unknown: 2 })
 })
 it('recognizes unhealthy and abnormal exits but does not treat normal exits or restart history as incidents', () => {
  const rows = [row('bad'), row('clean', { state: 'exited' }), row('failed', { state: 'exited' })]
  const obs = observationMaps(rows, [meta('bad', { health_status: 'unhealthy', restart_count: 3 }), meta('clean', { state: 'exited', exit_code: 0 }), meta('failed', { state: 'exited', exit_code: 137, oom_killed: false })], [], now)
  expect(aggregate(rows, obs).abnormal).toBe(2)
 })
 it('invalidates health from before a restart when a newer metric sample proves a new start', () => {
  const rows = [row('a')]
  const obs = observationMaps(rows, [meta('a', { health_status: 'unhealthy', started_at: new Date(now - 60_000).toISOString() })], [sample('a', { started_at: new Date(now - 1000).toISOString() })], now)
  expect(aggregate(rows, obs)).toMatchObject({ abnormal: 0, unknown: 1, cpu: 120 })
 })
 it('searches the full result including declared services and sorts unavailable resources last in either direction', () => {
  const entry = (name: string, cpu: number | null, abnormal = 0): ProjectEntry => ({ project: { name, source: 'managed', status: 'running', declared_service_names: ['worker'] } as ProjectSummary, containers: [row(name)], available: true, summary: { ...aggregate([], { runtime: new Map(), metrics: new Map() }), cpu, abnormal } })
  const entries = [entry('alpha', null), entry('beta', 20), entry('gamma', 100, 1)]
  expect(selectProjects(entries, defaultProjectView).map(e => e.project.name)).toEqual(['gamma', 'alpha', 'beta'])
  expect(selectProjects(entries, { ...defaultProjectView, sort: 'cpu', descending: true }).map(e => e.project.name)).toEqual(['gamma', 'beta', 'alpha'])
  expect(selectProjects(entries, { ...defaultProjectView, sort: 'cpu' }).map(e => e.project.name)).toEqual(['beta', 'gamma', 'alpha'])
  expect(selectProjects(entries, { ...defaultProjectView, search: 'worker', abnormal: true }).map(e => e.project.name)).toEqual(['gamma'])
 })
})
