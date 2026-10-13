import { create } from 'zustand'
import { defaultProjectView, type ProjectView } from '../features/compose/observation'

const key = 'suma-project-views-v1'
function read(): Record<string, ProjectView> {
  try {
    const raw: unknown = JSON.parse(localStorage.getItem(key) ?? '{}')
    if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return {}
    return Object.fromEntries(Object.entries(raw).flatMap(([node, value]) => {
      if (!value || typeof value !== 'object') return []
      const v = value as ProjectView
      if (!['abnormal', 'name', 'cpu', 'memory'].includes(v.sort) || typeof v.search !== 'string' || typeof v.descending !== 'boolean') return []
      return [[node, { ...defaultProjectView, ...v, status: ['', 'running', 'degraded', 'paused', 'stopped', 'not_created'].includes(v.status) ? v.status : '', source: ['', 'managed', 'external'].includes(v.source) ? v.source : '', abnormal: v.abnormal === true }]]
    }))
  } catch { return {} }
}
export const useProjectViewStore = create<{ views: Record<string, ProjectView>; update: (node: string, patch: Partial<ProjectView>) => void }>(set => ({
  views: read(),
  update: (node, patch) => set(state => {
    const views = { ...state.views, [node]: { ...(state.views[node] ?? defaultProjectView), ...patch } }
    localStorage.setItem(key, JSON.stringify(views))
    return { views }
  }),
}))
