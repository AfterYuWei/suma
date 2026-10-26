import type { ContainerMetrics, ContainerRuntime, ContainerSummary } from '../containers/types'
import { effectiveHealth, isAbnormal } from '../containers/observation'
import type { ProjectSummary } from './types'
import { projectDisplayName } from './types'

export const serviceName = (row: ContainerSummary) => row.labels['com.docker.compose.service'] || row.name
export const isOneOff = (row: ContainerSummary) => row.labels['com.docker.compose.oneoff']?.toLowerCase() === 'true'
export interface ProjectObservation {
  runtime: Map<string, ContainerRuntime>
  metrics: Map<string, ContainerMetrics>
}
export function observationMaps(rows: ContainerSummary[], metadata: ContainerRuntime[], samples: ContainerMetrics[], now = Date.now()): ProjectObservation {
  const containers = new Map(rows.map(row => [row.id, row]))
  const recentSamples = new Map(samples.filter(sample => !sample.sampled_at || now - Date.parse(sample.sampled_at) <= 15_000).map(sample => [sample.id, sample]))
  const runtime = new Map(metadata.filter(meta => {
    const row = containers.get(meta.id)
    const sample = recentSamples.get(meta.id)
    const restarted = sample?.started_at && meta.started_at && Date.parse(sample.started_at) > Date.parse(meta.started_at)
    return row && !restarted && (!meta.state || meta.state === row.state) && (!meta.observed_at || now - Date.parse(meta.observed_at) <= 30_000)
  }).map(meta => [meta.id, meta]))
  const metrics = new Map(samples.filter(sample => {
    const row = containers.get(sample.id), meta = runtime.get(sample.id)
    return row?.state === 'running' && sample.available && (!sample.sampled_at || now - Date.parse(sample.sampled_at) <= 15_000) && (!meta?.started_at || !sample.started_at || meta.started_at === sample.started_at)
  }).map(sample => [sample.id, sample]))
  return { runtime, metrics }
}
export function aggregate(rows: ContainerSummary[], observation: ProjectObservation) {
  const running = rows.filter(row => row.state === 'running')
  const cpu = running.map(row => observation.metrics.get(row.id)).filter(sample => sample?.cpu_available !== false && Number.isFinite(sample?.cpu_percent)) as ContainerMetrics[]
  const memory = running.map(row => observation.metrics.get(row.id)).filter(sample => sample?.memory_available !== false && Number.isFinite(sample?.memory_bytes)) as ContainerMetrics[]
  return {
    running: running.length, total: rows.length,
    abnormal: rows.filter(row => isAbnormal(row, observation.runtime.get(row.id))).length,
    unknown: rows.filter(row => !observation.runtime.get(row.id) || (row.state === 'running' && effectiveHealth(row, observation.runtime.get(row.id)) === 'unknown')).length,
    cpu: cpu.length ? cpu.reduce((sum, sample) => sum + sample.cpu_percent, 0) : null,
    memory: memory.length ? memory.reduce((sum, sample) => sum + sample.memory_bytes, 0) : null,
    cpuCoverage: cpu.length, memoryCoverage: memory.length,
  }
}
export type Aggregate = ReturnType<typeof aggregate>
function runtimeImages(rows: ContainerSummary[]) {
  const refs = new Map<string, Set<string>>()
  for (const row of rows) {
    const ids = refs.get(row.image) ?? new Set<string>()
    if (row.image_id) ids.add(row.image_id)
    refs.set(row.image, ids)
  }
  return [...refs].flatMap(([reference, ids]) => ids.size > 1 ? [...ids].map(id => `${reference} · ${id}`) : [reference])
}
export function groupServices(rows: ContainerSummary[], declared: string[]) {
  const groups = new Map<string, ContainerSummary[]>(declared.map(name => [name, []]))
  for (const row of rows.filter(row => !isOneOff(row))) {
    const name = serviceName(row)
    groups.set(name, [...(groups.get(name) ?? []), row])
  }
  return [...groups].sort(([a], [b]) => a.localeCompare(b)).map(([name, containers]) => ({
    name, containers: containers.sort((a, b) => a.name.localeCompare(b.name) || a.id.localeCompare(b.id)),
    images: runtimeImages(containers),
  }))
}
export type ProjectSort = 'abnormal' | 'name' | 'cpu' | 'memory'
export interface ProjectView { search: string; status: string; source: string; abnormal: boolean; sort: ProjectSort; descending: boolean }
export const defaultProjectView: ProjectView = { search: '', status: '', source: '', abnormal: false, sort: 'abnormal', descending: false }
export interface ProjectEntry { project: ProjectSummary; containers: ContainerSummary[]; summary: Aggregate; available: boolean }
export function projectStatus(entry: ProjectEntry) {
  if (!entry.available) return entry.project.status
  if (!entry.summary.total) return 'not_created'
  if (entry.summary.running === entry.summary.total) return 'running'
  if (entry.summary.running) return 'degraded'
  if (entry.containers.every(row => row.state === 'paused')) return 'paused'
  return 'stopped'
}
export function selectProjects(entries: ProjectEntry[], view: ProjectView) {
  const search = view.search.trim().toLowerCase()
  return entries.filter(entry => {
    const text = [projectDisplayName(entry.project), entry.project.name, ...(entry.project.declared_service_names ?? []), ...entry.containers.flatMap(row => [serviceName(row), row.image])].join(' ').toLowerCase()
    return text.includes(search) && (!view.status || projectStatus(entry) === view.status) && (!view.source || entry.project.source === view.source) && (!view.abnormal || entry.summary.abnormal > 0)
  }).sort((a, b) => {
    if (view.sort === 'abnormal') return Number(b.summary.abnormal > 0) - Number(a.summary.abnormal > 0) || projectDisplayName(a.project).localeCompare(projectDisplayName(b.project))
    if (view.sort === 'name') return (view.descending ? -1 : 1) * projectDisplayName(a.project).localeCompare(projectDisplayName(b.project))
    const av = a.available ? a.summary[view.sort] : null, bv = b.available ? b.summary[view.sort] : null
    if (av === null || bv === null) { if (av !== bv) return av === null ? 1 : -1 }
    else if (av !== bv) return (view.descending ? -1 : 1) * (av - bv)
    return projectDisplayName(a.project).localeCompare(projectDisplayName(b.project))
  })
}
