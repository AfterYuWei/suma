import type { LogEvent, LogRecord, LogSource } from '../features/project-logs/types'
export function demoLogSources(project: string): LogSource[] {
 return ['web', 'api'].map(service => ({ container_id: `demo-${project}-${service}`, container_name: `${project}-${service}-1`, service, instance: 1, state: 'running', one_off: false, orphan: false }))
}
function records(project: string, count = 20): LogRecord[] { const sources = demoLogSources(project); return Array.from({ length: count }, (_, index) => { const source = sources[index % sources.length]; return { id: `${project}/history/${index}`, time: new Date(Date.now() - (count - index) * 1000).toISOString(), service: source.service, container_id: source.container_id, container_name: source.container_name, stream: index % 5 === 0 ? 'stderr' : 'stdout', text: index % 5 === 0 ? 'retrying upstream connection' : `request ${index} completed status=200` } }) }
export function operationsDemo(suffix: string, url: URL): { handled: boolean; value?: unknown } {
 const match = suffix.match(/^\/projects\/compose\/([^/]+)\/(log-sources|logs\/history)$/)
 if (match) {
  const project = match[1]; const sources = demoLogSources(project)
  if (match[2] === 'log-sources') return { handled: true, value: sources }
  const selectedServices = url.searchParams.get('services')?.split(','); const selectedContainers = url.searchParams.get('containers')?.split(',')
  const since = url.searchParams.get('since'); const until = url.searchParams.get('until')
  const entries = records(project).filter(row => (!since || row.time >= since) && (!until || row.time <= until) && (!selectedServices || selectedServices.includes(row.service)) && (!selectedContainers || selectedContainers.includes(row.container_id))).slice(-Number(url.searchParams.get('tail') || 200))
  return { handled: true, value: { entries, sources, errors: [], truncated: false } }
 }
 return { handled: false }
}
export function demoProjectLogStream(project: string, onEvent: (event: LogEvent) => void, tail: number): () => void {
 const sources = demoLogSources(project); let index = 0
 onEvent({ type: 'snapshot', entries: records(project).slice(-tail), sources, errors: [], truncated: false })
 const timer = setInterval(() => { const source = sources[index % sources.length]; index++; onEvent({ type: 'entries', entries: [{ id: `${project}/live/${Date.now()}/${index}`, time: new Date().toISOString(), service: source.service, container_id: source.container_id, container_name: source.container_name, stream: index % 4 ? 'stdout' : 'stderr', text: index % 4 ? `live request ${index} completed status=200` : 'retrying upstream connection' }] }) }, 700)
 return () => clearInterval(timer)
}
