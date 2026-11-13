import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../../lib/api'
import type { KindInfo, ImageUpdateProjectStatus, Schedule, ScheduleInput, ScheduleRun, TaskRecord } from './types'

export const useTaskKinds = () => useQuery({ queryKey: ['task-kinds'], queryFn: () => api<KindInfo[]>('/task-center/kinds'), staleTime: 60_000 })

export const useSchedules = (kind?: string, filters?: { scope: 'all' | 'node' | 'control_plane'; nodeID?: string; includeArchived?: boolean }) => useQuery({
  queryKey: ['task-schedules', kind ?? 'all', filters],
  queryFn: () => {
    const search = new URLSearchParams()
    if (kind) search.set('kind', kind)
    if (filters) {
      search.set('scope', filters.scope)
      if (filters.nodeID) search.set('node_id', filters.nodeID)
      if (filters.includeArchived) search.set('include_archived', 'true')
    }
    return api<Schedule[]>(`/task-center/schedules?${search}`)
  },
  refetchInterval: 10_000,
})

export const useSchedule = (id: number | undefined) => useQuery({
  queryKey: ['task-schedule', id],
  queryFn: () => api<Schedule>(`/task-center/schedules/${id}`),
  enabled: id != null,
})

export interface RunFilters { scope: 'all' | 'control_plane' | 'node'; nodeID?: string; scheduleID?: number; source?: string; status?: string; limit: number; offset: number }
export const useTriggerRecords = (filters: RunFilters) => useQuery({
  queryKey: ['task-schedule-runs', filters],
  queryFn: () => {
    const search = new URLSearchParams({ scope: filters.scope, limit: String(filters.limit), offset: String(filters.offset), view: 'summary' })
    if (filters.nodeID) search.set('node_id', filters.nodeID)
    if (filters.scheduleID) search.set('schedule_id', String(filters.scheduleID))
    if (filters.source) search.set('source', filters.source)
    if (filters.status) search.set('status', filters.status)
    return api<{ items: ScheduleRun[]; total: number }>(`/task-center/runs?${search}`)
  },
  refetchInterval: 5_000,
})

export interface RecordFilters { scope: 'all' | 'control_plane' | 'node'; status: string; type: string; nodeID?: string; search?: string; source?: string; scheduleID?: number; limit?: number; offset?: number }

export const useTaskRecords = (filters: RecordFilters) => useQuery({
  queryKey: ['task-records', filters],
  queryFn: async () => {
    const search = new URLSearchParams({ scope: filters.scope, limit: String(filters.limit ?? 20), offset: String(filters.offset ?? 0), view: 'summary' })
    if (filters.status) search.set('status', filters.status)
    if (filters.type) search.set('type', filters.type)
    if (filters.nodeID) search.set('node_id', filters.nodeID)
    if (filters.search) search.set('search', filters.search)
    if (filters.source) search.set('source', filters.source)
    if (filters.scheduleID) search.set('schedule_id', String(filters.scheduleID))
    return api<{ items: TaskRecord[]; total: number }>(`/task-center/records?${search}`)
  },
  refetchInterval: (query) => (query.state.data?.items ?? []).some((row) => row.status === 'pending' || row.status === 'running') ? 3_000 : 15_000,
})

// Single record lookup reuses the plain task endpoint so /tasks#hash deep
// links and record details share one source.
export const useTaskRecord = (id: string | undefined) => useQuery({
  queryKey: ['task-record', id],
  queryFn: () => api<TaskRecord>(`/tasks/${encodeURIComponent(id ?? '')}`),
  enabled: !!id,
  refetchInterval: (query) => ['pending', 'running'].includes(query.state.data?.status ?? '') ? 2_000 : false,
})

export const useImageUpdates = (nodeID: string | undefined, project?: string) => useQuery({
  queryKey: ['image-updates', nodeID, project ?? 'all'],
  queryFn: () => api<ImageUpdateProjectStatus[]>(`/task-center/image-updates?node_id=${encodeURIComponent(nodeID ?? '')}${project ? `&project=${encodeURIComponent(project)}` : ''}`),
  enabled: !!nodeID,
  refetchInterval: (query) => (query.state.data ?? []).some((row) => row.running_task_id) ? 2_000 : 30_000,
})

export const useCronPreview = (cron: string, timezone: string, enabled: boolean, kind?: string) => useQuery({
  queryKey: ['cron-preview', cron, timezone, kind],
  queryFn: () => api<string[]>(`/task-center/schedules/preview?cron=${encodeURIComponent(cron)}&timezone=${encodeURIComponent(timezone)}${kind ? `&kind=${encodeURIComponent(kind)}` : ''}`),
  enabled: enabled && cron.trim().length > 0 && !!timezone,
})

export const useCreateSchedule = () => {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (input: ScheduleInput) => api<Schedule>('/task-center/schedules', { method: 'POST', body: JSON.stringify(input) }),
    onSuccess: () => {
      for (const key of ['task-schedules', 'task-schedule', 'cleanup-policy', 'cleanup-policies', 'delivery-configuration', 'project-gitops']) void client.invalidateQueries({ queryKey: [key] })
    },
  })
}

export const useUpdateSchedule = () => {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ id, version, ...input }: Partial<ScheduleInput> & { id: number; version: number; confirmation_name?: string; authorize?: boolean }) =>
      api<Schedule>(`/task-center/schedules/${id}`, { method: 'PUT', body: JSON.stringify({ ...input, expected_version: version }) }),
    onSuccess: () => {
      for (const key of ['task-schedules', 'task-schedule', 'cleanup-policy', 'cleanup-policies', 'delivery-configuration', 'project-gitops']) void client.invalidateQueries({ queryKey: [key] })
    },
  })
}

export const useDeleteSchedule = () => {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ id, version }: { id: number; version: number }) => api(`/task-center/schedules/${id}?expected_version=${version}`, { method: 'DELETE' }),
    onSuccess: () => {
      for (const key of ['task-schedules', 'task-schedule', 'cleanup-policy', 'cleanup-policies', 'delivery-configuration', 'project-gitops']) void client.invalidateQueries({ queryKey: [key] })
    },
  })
}

export const useRunSchedule = () => {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (input: number | { id: number; action?: string }) => {
      const id = typeof input === 'number' ? input : input.id
      return api<ScheduleRun>(`/task-center/schedules/${id}/run`, { method: 'POST', body: JSON.stringify(typeof input === 'number' ? {} : { action: input.action }) })
    },
    onSuccess: () => {
      for (const key of ['task-schedules', 'task-schedule', 'cleanup-policy', 'cleanup-policies', 'delivery-configuration', 'project-gitops']) void client.invalidateQueries({ queryKey: [key] })
    },
  })
}

// startTask launches one manual run of a registered kind; the generic endpoint
// keeps future task types free of new API surface.
export const useStartTask = () => {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ kind, nodeID, params }: { kind: string; nodeID?: string; params: Record<string, unknown> }) =>
      api<{ task_id: string }>('/task-center/tasks', { method: 'POST', body: JSON.stringify({ kind, node_id: nodeID, params }) }),
    onSuccess: () => {
      client.invalidateQueries({ queryKey: ['image-updates'] })
      client.invalidateQueries({ queryKey: ['tasks'] })
    },
  })
}
