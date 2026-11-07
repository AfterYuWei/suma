import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../../lib/api'
import type { KindInfo, ImageUpdateProjectStatus, Schedule, ScheduleInput, ScheduleRun, TaskRecord } from './types'

const list = <T,>(path: string) => async () => (await api<{ items: T[]; total: number }>(path)).items

export const useTaskKinds = () => useQuery({ queryKey: ['task-kinds'], queryFn: () => api<KindInfo[]>('/task-center/kinds'), staleTime: 60_000 })

export const useSchedules = (kind?: string) => useQuery({
  queryKey: ['task-schedules', kind ?? 'all'],
  queryFn: list<Schedule>(`/task-center/schedules${kind ? `?kind=${encodeURIComponent(kind)}` : ''}`),
  refetchInterval: 10_000,
})

export const useSchedule = (id: number | undefined) => useQuery({
  queryKey: ['task-schedule', id],
  queryFn: () => api<Schedule>(`/task-center/schedules/${id}`),
  enabled: id != null,
})

export const useScheduleRuns = (id: number | undefined) => useQuery({
  queryKey: ['task-schedule-runs', id],
  queryFn: () => api<ScheduleRun[]>(`/task-center/schedules/${id}/runs?limit=20`),
  enabled: id != null,
  refetchInterval: 5_000,
})

export interface RecordFilters { scope: 'all' | 'control_plane' | 'node'; status: string; type: string; nodeID?: string; limit?: number }

export const useTaskRecords = (filters: RecordFilters) => useQuery({
  queryKey: ['task-records', filters.scope, filters.status, filters.type, filters.nodeID ?? ''],
  queryFn: async () => {
    const search = new URLSearchParams({ scope: filters.scope, limit: String(filters.limit ?? 200) })
    if (filters.status) search.set('status', filters.status)
    if (filters.type) search.set('type', filters.type)
    if (filters.nodeID) search.set('node_id', filters.nodeID)
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

export const useCronPreview = (cron: string, timezone: string, enabled: boolean) => useQuery({
  queryKey: ['cron-preview', cron, timezone],
  queryFn: () => api<string[]>(`/task-center/schedules/preview?cron=${encodeURIComponent(cron)}&timezone=${encodeURIComponent(timezone)}`),
  enabled: enabled && cron.trim().length > 0 && !!timezone,
})

export const useCreateSchedule = () => {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (input: ScheduleInput) => api<Schedule>('/task-center/schedules', { method: 'POST', body: JSON.stringify(input) }),
    onSuccess: () => client.invalidateQueries({ queryKey: ['task-schedules'] }),
  })
}

export const useUpdateSchedule = () => {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ id, version, ...input }: ScheduleInput & { id: number; version: number }) =>
      api<Schedule>(`/task-center/schedules/${id}`, { method: 'PUT', body: JSON.stringify({ ...input, expected_version: version }) }),
    onSuccess: () => client.invalidateQueries({ queryKey: ['task-schedules'] }),
  })
}

export const useDeleteSchedule = () => {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => api(`/task-center/schedules/${id}`, { method: 'DELETE' }),
    onSuccess: () => client.invalidateQueries({ queryKey: ['task-schedules'] }),
  })
}

export const useRunSchedule = () => {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => api<ScheduleRun>(`/task-center/schedules/${id}/run`, { method: 'POST' }),
    onSuccess: () => client.invalidateQueries({ queryKey: ['task-schedules'] }),
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
