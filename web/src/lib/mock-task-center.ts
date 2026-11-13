import { ApiError } from './api-error'
import type { DockerNode } from './nodes'
import type { KindInfo, Schedule, ScheduleInput, ScheduleRun, TaskRecord } from '../features/task-center/types'

export const mockTaskKinds: KindInfo[] = [
  { type: 'image.update', requires_node: true, schedule_types: ['cron', 'none'], actions: ['run', 'check', 'update'], webhook: false, business_owned: false },
  { type: 'system.cleanup', requires_node: true, schedule_types: ['cron'], actions: ['run'], webhook: false, business_owned: true },
  { type: 'cd.sync', requires_node: false, schedule_types: ['interval'], actions: ['check'], webhook: true, business_owned: true },
  { type: 'project.cd.check', requires_node: true, schedule_types: ['interval'], actions: ['check'], webhook: true, business_owned: true },
]

const invalid = () => new ApiError('Invalid automation configuration', 22001, 400)
const conflict = () => new ApiError('Schedule changed concurrently; reload and retry', 22003, 409)
const active = (task: TaskRecord) => ['pending', 'running'].includes(task.status)

// Demo previews use the same five-field contract. Work yields between days so
// sparse yearly expressions cannot block the demo UI; no timers execute Docker.
export async function mockNextRuns(input: Pick<Schedule, 'schedule_type' | 'cron' | 'timezone' | 'interval_seconds' | 'kind'>, after = new Date(), count = 5): Promise<string[]> {
  if (input.schedule_type === 'none') return []
  if (input.schedule_type === 'interval') {
    if (!Number.isInteger(input.interval_seconds) || input.interval_seconds < 30 || input.interval_seconds > 86400) throw invalid()
    return Array.from({ length: count }, (_, i) => new Date(+after + (i + 1) * input.interval_seconds * 1000).toISOString())
  }
  const descriptors: Record<string, string> = { '@hourly': '0 * * * *', '@daily': '0 0 * * *', '@midnight': '0 0 * * *', '@weekly': '0 0 * * 0', '@monthly': '0 0 1 * *', '@yearly': '0 0 1 1 *', '@annually': '0 0 1 1 *' }
  const fields = (descriptors[input.cron] ?? input.cron).trim().split(/\s+/)
  if (fields.length !== 5 || input.timezone === 'Local') throw invalid()
  if (input.kind === 'system.cleanup' && (!/^\d+$/.test(fields[0]) || !/^\d+$/.test(fields[1]) || fields[2] !== '*' || fields[3] !== '*' || fields[4] !== '*' && !/^[0-6]$/.test(fields[4]))) throw invalid()
  const ranges = [[0, 59], [0, 23], [1, 31], [1, 12], [0, 7]]
  const allowed = fields.map((field, index) => {
    const [min, max] = ranges[index], values = new Set<number>()
    for (const part of field.split(',')) {
      const match = /^(\*|\?|\d+(?:-\d+)?)(?:\/(\d+))?$/.exec(part)
      if (!match) throw invalid()
      const wildcard = match[1] === '*' || match[1] === '?'
      const [start, end] = wildcard ? [min, max] : match[1].includes('-') ? match[1].split('-').map(Number) : [Number(match[1]), match[2] ? max : Number(match[1])]
      const step = Number(match[2] ?? 1)
      if (start < min || end > max || start > end || step < 1 || step > max + 1) throw invalid()
      for (let value = start; value <= end; value += step) values.add(index === 4 && value === 7 ? 0 : value)
    }
    return values
  })
  let formatter: Intl.DateTimeFormat
  try { formatter = new Intl.DateTimeFormat('en-US', { timeZone: input.timezone, hourCycle: 'h23', year: 'numeric', month: 'numeric', day: 'numeric', weekday: 'short', hour: 'numeric', minute: 'numeric' }) } catch { throw invalid() }
  const result: string[] = [], cleanupDays = new Set<string>()
  const wildcardDay = fields[2] === '*' || fields[2] === '?', wildcardWeek = fields[4] === '*' || fields[4] === '?'
  const start = Math.floor(+after / 60000) * 60000 + 60000, end = start + 6 * 366 * 86400000
  for (let at = start, steps = 0; at < end && result.length < count; at += 60000, steps++) {
    const parts = Object.fromEntries(formatter.formatToParts(at).map(part => [part.type, part.value]))
    const day = allowed[2].has(Number(parts.day)), week = allowed[4].has(['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'].indexOf(parts.weekday))
    const dayMatches = wildcardDay && wildcardWeek || wildcardDay && week || wildcardWeek && day || !wildcardDay && !wildcardWeek && (day || week)
    const key = `${parts.year}-${parts.month}-${parts.day}`
    if (dayMatches && allowed[3].has(Number(parts.month)) && allowed[1].has(Number(parts.hour)) && allowed[0].has(Number(parts.minute)) && (input.kind !== 'system.cleanup' || !cleanupDays.has(key))) {
      result.push(new Date(at).toISOString()); cleanupDays.add(key)
    }
    if (steps && steps % 1440 === 0) await new Promise(resolve => setTimeout(resolve, 0))
  }
  if (!result.length) throw invalid()
  return result
}

export interface BusinessRuleInput extends ScheduleInput { webhook_enabled?: boolean; next_run_at?: string }
interface Dependencies {
  nodes: DockerNode[]
  tasks: TaskRecord[]
  change: (next: Schedule, input: Record<string, unknown>) => void
  run: (kind: string, nodeID: string | undefined, params: Record<string, unknown>, taskID: string) => unknown
  validateImage: (nodeID: string, project: string, params: Record<string, unknown>) => void
}

export function createMockTaskCenter(deps: Dependencies) {
  const rules: Schedule[] = [], runs: ScheduleRun[] = []
  let nextID = 1, nextRunID = 1
  const upsert = (input: BusinessRuleInput) => {
    const previous = rules.find(row => !row.archived_at && row.kind === input.kind && (row.node_id ?? '') === (input.node_id ?? '') && row.subject === (input.subject ?? ''))
    const now = new Date().toISOString()
    const row: Schedule = { id: previous?.id ?? nextID++, name: previous?.name ?? input.name, kind: input.kind, node_id: input.node_id, node_name: deps.nodes.find(node => node.id === input.node_id)?.name, subject: input.subject ?? '', params: input.params, cron: input.cron, timezone: input.timezone, enabled: input.enabled ?? true, timer_enabled: input.timer_enabled ?? previous?.timer_enabled ?? true, webhook_enabled: input.webhook_enabled ?? false, schedule_type: input.schedule_type ?? 'cron', interval_seconds: input.interval_seconds ?? 0, version: (previous?.version ?? 0) + 1, created_at: previous?.created_at ?? now, updated_at: now }
    row.next_run_at = row.enabled && row.timer_enabled ? input.next_run_at ?? (row.schedule_type === 'interval' ? new Date(Date.now() + row.interval_seconds * 1000).toISOString() : undefined) : undefined
    if (previous) Object.assign(previous, row)
    else rules.push(row)
    return previous ?? row
  }
  const archive = (kind: string | undefined, nodeID: string | undefined, subject?: string) => {
    for (const row of rules) if (!row.archived_at && (!kind || row.kind === kind) && (nodeID === undefined || row.node_id === nodeID) && (subject === undefined || row.subject === subject)) Object.assign(row, { archived_at: new Date().toISOString(), enabled: false, timer_enabled: false, next_run_at: undefined, version: row.version + 1 })
  }
  const view = (row: Schedule): Schedule => {
    const last = runs.slice().reverse().find(run => run.schedule_id === row.id), task = deps.tasks.find(task => task.id === last?.task_id)
    return { ...row, effective_status: row.archived_at ? 'archived' : !row.enabled ? 'paused' : !row.timer_enabled ? 'timer_paused' : row.schedule_type !== 'none' && !row.next_run_at ? 'parked' : 'enabled', last_run_id: last?.id, last_run_status: last?.status, last_run_reason: last?.reason_code, last_run_error: last?.error, last_task_id: task?.id, last_task_status: task?.status, last_task_finished_at: task?.finished_at }
  }
  const record = (kind: string, nodeID: string | undefined, subject: string, taskID: string) => {
    const rule = rules.find(row => !row.archived_at && row.kind === kind && (row.node_id ?? '') === (nodeID ?? '') && row.subject === subject)
    const run: ScheduleRun = { id: nextRunID++, schedule_id: rule?.id, task_id: taskID, triggered_by: 'manual', status: 'started', actor_id: 1, config_version: rule ? String(rule.version) : '', kind, node_id: nodeID, node_name: deps.nodes.find(node => node.id === nodeID)?.name, subject, rule_snapshot: { rule: rule ? structuredClone(rule) : undefined, kind, node_id: nodeID ?? '', params: { project: subject } }, created_at: new Date().toISOString() }
    runs.push(run)
    return run
  }
  const dispatch = (kind: string, nodeID: string | undefined, params: Record<string, unknown>, action?: string, rule?: Schedule) => {
    const spec = mockTaskKinds.find(entry => entry.type === kind)
    if (!spec || spec.requires_node !== !!nodeID || action && !spec.actions.includes(action)) throw invalid()
    if (spec.requires_node && !deps.nodes.some(node => node.id === nodeID && node.enabled)) throw new ApiError('Docker node is unavailable', 22008, 404)
    if (kind === 'system.cleanup') throw new ApiError('Use a real cleanup preview and node confirmation', 22007, 400)
    if (typeof params.project !== 'string' || !/^[a-z0-9][a-z0-9_-]{0,127}$/.test(params.project)) throw invalid()
    if (kind !== 'image.update' && Object.keys(params).some(key => key !== 'project')) throw invalid()
    if (new TextEncoder().encode(JSON.stringify(params)).length > 8192) throw invalid()
    const subject = params.project
    rule ??= rules.find(row => !row.archived_at && row.kind === kind && (row.node_id ?? '') === (nodeID ?? '') && row.subject === subject)
    const busy = deps.tasks.some(task => task.type === kind && task.node_id === nodeID && task.params && typeof task.params === 'object' && 'project' in task.params && task.params.project === subject && active(task))
    if (busy) {
      const run = record(kind, nodeID, subject, '')
      Object.assign(run, { status: 'skipped', reason_code: 'busy', error: 'A previous task is still active' })
      throw new ApiError('A previous task is still active', 22004, 409)
    }
    if (kind === 'image.update') {
      params = { ...params, auto_apply: action === 'check' ? false : action === 'update' ? true : params.auto_apply === true }
      deps.validateImage(nodeID!, subject, params)
    }
    const task: TaskRecord = { id: crypto.randomUUID(), scope: nodeID ? 'node' : 'control_plane', node_id: nodeID, node_name: deps.nodes.find(node => node.id === nodeID)?.name, type: kind, name: `${kind} ${subject}`, status: 'pending', progress: 0, message: 'Demo operation accepted', params: structuredClone(params), created_at: new Date().toISOString() }
    deps.tasks.unshift(task)
    const run = record(kind, nodeID, subject, task.id)
    setTimeout(() => {
      if (!active(task)) return
      if (rule && (rule.archived_at || String(rule.version) !== run.config_version)) {
        Object.assign(task, { status: 'canceled', message: 'Configuration changed before execution; no business work performed', finished_at: new Date().toISOString() })
        return
      }
      try { task.result = deps.run(kind, nodeID, params, task.id); task.status = 'success'; task.message = 'Demo operation completed' }
      catch (error) { task.status = 'failed'; task.message = error instanceof Error ? error.message : 'Demo operation failed' }
      task.progress = 100; task.finished_at = new Date().toISOString()
    }, 100)
    return run
  }
  const recordView = (task: TaskRecord): TaskRecord => {
    const recent = runs.slice().reverse()
    const trigger = recent.find(run => run.task_id === task.id) ?? (task.parent_task_id ? recent.find(run => run.task_id === task.parent_task_id) : undefined)
    const rule = rules.find(rule => rule.id === trigger?.schedule_id)
    return { ...task, run_id: trigger?.id, triggered_by: trigger?.triggered_by, schedule_id: trigger?.schedule_id, schedule_name: rule?.name }
  }
  const triggerPage = (url: URL, scheduleID?: number) => {
    const scope = url.searchParams.get('scope'), nodeID = url.searchParams.get('node_id'), source = url.searchParams.get('source'), status = url.searchParams.get('status')
    const selectedRule = scheduleID ?? Number(url.searchParams.get('schedule_id'))
    const all = runs.slice().reverse().filter(run => (!selectedRule || run.schedule_id === selectedRule) && (!source || run.triggered_by === source) && (!status || run.status === status) && (!nodeID || run.node_id === nodeID) && (scope !== 'node' || !!run.node_id) && (scope !== 'control_plane' || !run.node_id)).map(run => {
      const task = deps.tasks.find(task => task.id === run.task_id), rule = rules.find(rule => rule.id === run.schedule_id)
      const view = { ...run, rule_name: rule?.name, task_status: task?.status, task_finished_at: task?.finished_at }
      if (url.searchParams.get('view') !== 'summary') return view
      const { rule_snapshot: _snapshot, ...summary } = view
      return summary
    })
    const limit = Math.min(100, Math.max(1, Number(url.searchParams.get('limit')) || 20)), offset = Math.max(0, Number(url.searchParams.get('offset')) || 0)
    return { total: all.length, items: all.slice(offset, offset + limit) }
  }
  const handle = async (path: string, method: string, body: Record<string, unknown>, url: URL): Promise<unknown> => {
    if (path === '/task-center/kinds') return mockTaskKinds
    if (path === '/task-center/runs') return triggerPage(url)
    const triggerID = path.match(/^\/task-center\/runs\/(\d+)$/)
    if (triggerID) {
      const run = runs.find(run => run.id === Number(triggerID[1]))
      if (!run) throw new ApiError('Trigger record not found', 22005, 404)
      return { ...run, rule_name: rules.find(rule => rule.id === run.schedule_id)?.name }
    }
    const taskID = path.match(/^\/tasks\/([^/]+)$/)
    if (taskID && method === 'GET') {
      const task = deps.tasks.find(task => task.id === taskID[1])
      if (!task) throw new ApiError('Task not found', 16007, 404)
      return recordView(task)
    }
    const cancel = path.match(/^\/tasks\/([^/]+)\/cancel$/)
    if (cancel && method === 'POST') {
      const task = deps.tasks.find(task => task.id === cancel[1])
      if (!task || !active(task)) throw new ApiError('Task is not running', 16003, 409)
      Object.assign(task, { status: 'canceled', message: 'Demo operation canceled', finished_at: new Date().toISOString() })
      return { id: task.id }
    }
    if (path === '/task-center/image-updates') {
      const nodeID = url.searchParams.get('node_id'), selected = url.searchParams.get('project')
      if (!nodeID || !deps.nodes.some(node => node.id === nodeID)) throw new ApiError('Docker node not found', 22008, 404)
      const matching = deps.tasks.filter(task => task.type === 'image.update' && task.node_id === nodeID)
      const names = new Set(rules.filter(row => row.kind === 'image.update' && row.node_id === nodeID).map(row => row.subject))
      for (const task of matching) if (task.params && typeof task.params === 'object' && 'project' in task.params && typeof task.params.project === 'string') names.add(task.params.project)
      if (selected) names.add(selected)
      return [...names].filter(name => !selected || name === selected).map(project => {
        const records = matching.filter(task => task.params && typeof task.params === 'object' && 'project' in task.params && task.params.project === project)
        const last = records.find(task => !active(task)), running = records.find(active)
        return { project, last_task_id: last?.id, last_task_status: last?.status, last_task_finished_at: last?.finished_at, last_result: last?.result ?? null, running_task_id: running?.id }
      })
    }
    if (/^\/tasks\/[^/]+\/children$/.test(path)) return deps.tasks.filter(task => task.parent_task_id === path.split('/')[2]).map(({ params: _params, result: _result, ...task }) => task)
    if (path === '/task-center/records') {
      const status = url.searchParams.get('status'), scope = url.searchParams.get('scope'), node = url.searchParams.get('node_id'), type = url.searchParams.get('type')
      const source = url.searchParams.get('source'), scheduleID = Number(url.searchParams.get('schedule_id')), search = (url.searchParams.get('search') ?? '').trim().toLowerCase()
      const rows = deps.tasks.map(recordView).filter(task => (!scope || scope === 'all' || task.scope === scope) && (!node || task.node_id === node) && (!type || task.type.startsWith(type)) && (!status || status === 'active' && active(task) || status === 'finished' && !active(task) || task.status === status) && (!source || (source === 'unrecorded' ? !task.run_id : task.triggered_by === source)) && (!scheduleID || task.schedule_id === scheduleID) && (!search || [task.name, task.message, task.type, task.node_name].some(value => value?.toLowerCase().includes(search)))).sort((a, b) => b.created_at.localeCompare(a.created_at) || b.id.localeCompare(a.id))
      const limit = Math.min(200, Math.max(1, Number(url.searchParams.get('limit')) || 20)), offset = Math.max(0, Number(url.searchParams.get('offset')) || 0)
      return { total: rows.length, items: rows.slice(offset, offset + limit).map(task => { if (url.searchParams.get('view') !== 'summary') return task; const { params: _params, result: _result, ...summary } = task; return summary }) }
    }
    if (path === '/task-center/schedules/preview') return mockNextRuns({ kind: url.searchParams.get('kind') ?? '', schedule_type: url.searchParams.get('schedule_type') === 'interval' ? 'interval' : url.searchParams.get('schedule_type') === 'none' ? 'none' : 'cron', cron: url.searchParams.get('cron') ?? '', timezone: url.searchParams.get('timezone') ?? 'UTC', interval_seconds: Number(url.searchParams.get('interval_seconds')) })
    if (path === '/task-center/tasks' && method === 'POST') {
      if (typeof body.kind !== 'string' || body.node_id !== undefined && typeof body.node_id !== 'string' || !body.params || typeof body.params !== 'object' || Array.isArray(body.params)) throw invalid()
      return { task_id: dispatch(body.kind, body.node_id, { ...body.params }).task_id }
    }
    if (path === '/task-center/schedules' && method === 'GET') return rules.filter(row => (!row.archived_at || url.searchParams.get('include_archived') === 'true') && (!url.searchParams.get('kind') || row.kind === url.searchParams.get('kind')) && (!url.searchParams.get('node_id') || row.node_id === url.searchParams.get('node_id')) && (url.searchParams.get('scope') !== 'control_plane' || !row.node_id) && (url.searchParams.get('scope') !== 'node' || !!row.node_id)).map(view)
    const match = path.match(/^\/task-center\/schedules\/(\d+)(\/run|\/runs)?$/)
    const row = match ? rules.find(row => row.id === Number(match[1])) : undefined
    if (match && !row) throw new ApiError('Schedule not found', 22005, 404)
    if (row && match?.[2] === '/runs') {
      const page = triggerPage(url, row.id)
      return url.searchParams.has('offset') ? page : page.items
    }
    if (row && match?.[2] === '/run') {
      if (row.archived_at) throw new ApiError('Schedule not found', 22005, 404)
      return dispatch(row.kind, row.node_id, { ...row.params }, typeof body.action === 'string' ? body.action : undefined, row)
    }
    if (row && method === 'GET') return view(row)
    if (row?.archived_at) throw new ApiError('Schedule not found', 22005, 404)
    if (row && method === 'DELETE') {
      if (Number(url.searchParams.get('expected_version')) !== row.version) throw conflict()
      if (row.kind !== 'image.update' && (row.kind !== 'system.cleanup' || row.enabled)) throw new ApiError('Pause this rule through its business settings', 22007, 400)
      archive(row.kind, row.node_id ?? '', row.subject); return { id: row.id }
    }
    if (path !== '/task-center/schedules' && !row) return undefined
    if (method !== 'POST' && method !== 'PUT') return undefined
    if (row && (row.archived_at || body.expected_version !== row.version)) throw conflict()
    const kind = typeof body.kind === 'string' ? body.kind : row?.kind
    if (!kind || !mockTaskKinds.some(spec => spec.type === kind)) throw invalid()
    if (row && kind !== row.kind) throw invalid()
    if (!row && kind !== 'image.update') throw new ApiError('Configure this automation through its business settings', 22007, 400)
    const next: Schedule = row ? structuredClone(row) : { id: nextID, kind, name: '', subject: '', cron: '', timezone: 'UTC', enabled: true, timer_enabled: true, webhook_enabled: false, schedule_type: 'cron', interval_seconds: 0, version: 1, created_at: new Date().toISOString(), updated_at: new Date().toISOString() }
    for (const key of ['name', 'cron', 'timezone', 'subject', 'node_id'] as const) if (body[key] !== undefined) { if (typeof body[key] !== 'string') throw invalid(); next[key] = body[key] }
    for (const key of ['enabled', 'timer_enabled'] as const) if (body[key] !== undefined) { if (typeof body[key] !== 'boolean') throw invalid(); next[key] = body[key] }
    if (body.schedule_type !== undefined) { if (body.schedule_type !== 'cron' && body.schedule_type !== 'interval' && body.schedule_type !== 'none') throw invalid(); next.schedule_type = body.schedule_type }
    if (body.interval_seconds !== undefined) { if (typeof body.interval_seconds !== 'number') throw invalid(); next.interval_seconds = body.interval_seconds }
    if (body.params !== undefined) { if (!body.params || typeof body.params !== 'object' || Array.isArray(body.params)) throw invalid(); next.params = { ...body.params } }
    if (!next.name.trim() || next.name.length > 120 || next.subject.length > 200 || row && (next.kind !== row.kind || next.node_id !== row.node_id || next.subject !== row.subject)) throw invalid()
    const spec = mockTaskKinds.find(spec => spec.type === kind)!
    if (!spec.schedule_types.includes(next.schedule_type) || spec.requires_node !== !!next.node_id || new TextEncoder().encode(JSON.stringify(next.params)).length > 8192) throw invalid()
    if (next.schedule_type !== 'cron') { next.cron = ''; next.timezone = 'UTC' }
    if (next.kind === 'image.update') {
      if (next.params?.project !== next.subject || !next.node_id || !deps.nodes.some(node => node.id === next.node_id && node.enabled)) throw invalid()
      deps.validateImage(next.node_id, next.subject, next.params ?? {})
    }
    const previews = await mockNextRuns(next)
    if (row && body.expected_version !== row.version) throw conflict()
    if (!row && rules.some(row => !row.archived_at && row.kind === next.kind && row.node_id === next.node_id && row.subject === next.subject)) throw new ApiError('An identical schedule already exists', 22006, 409)
    next.version = row ? row.version + 1 : 1; next.updated_at = new Date().toISOString(); next.next_run_at = next.enabled && next.timer_enabled ? previews[0] : undefined
    if (next.kind !== 'image.update') deps.change(next, body)
    if (row) Object.assign(row, next)
    else { nextID++; rules.push(next) }
    return view(row ?? next)
  }
  return { handle, upsert, archive, dispatch, record, rules }
}
