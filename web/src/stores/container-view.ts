import { create } from 'zustand'

export const sortFields = ['name', 'restarts', 'cpu', 'memory', 'memory_ratio', 'receive', 'transmit', 'image', 'created', 'uptime'] as const
export type SortField = typeof sortFields[number]
export interface ContainerView { search: string; states: string[]; health: string[]; source: string; project: string; diagnostic: string; sort: SortField; descending: boolean }
export const defaultContainerView: ContainerView = { search: '', states: [], health: [], source: '', project: '', diagnostic: '', sort: 'created', descending: true }
const key = 'suma-container-views-v1'
function read(): Record<string, ContainerView> {
  try {
    const raw: unknown = JSON.parse(localStorage.getItem(key) ?? '{}')
    if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return {}
    return Object.fromEntries(Object.entries(raw).flatMap(([node, value]) => {
      if (!value || typeof value !== 'object') return []
      const v = value as Partial<ContainerView>
      if (!sortFields.includes(v.sort as SortField) || typeof v.search !== 'string' || typeof v.descending !== 'boolean') return []
      return [[node, { ...defaultContainerView, ...v, states: Array.isArray(v.states) ? v.states.filter(x => typeof x === 'string') : [], health: Array.isArray(v.health) ? v.health.filter(x => typeof x === 'string') : [], source: typeof v.source === 'string' ? v.source : '', project: typeof v.project === 'string' ? v.project : '', diagnostic: typeof v.diagnostic === 'string' ? v.diagnostic : '' }]]
    }))
  } catch { return {} }
}
export const useContainerViewStore = create<{ views: Record<string, ContainerView>; update: (node: string, patch: Partial<ContainerView>) => void }>((set) => ({
  views: read(),
  update: (node, patch) => set(state => {
    const views = { ...state.views, [node]: { ...(state.views[node] ?? defaultContainerView), ...patch } }
    localStorage.setItem(key, JSON.stringify(views))
    return { views }
  }),
}))
