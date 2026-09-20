import { create } from 'zustand'

export type Theme = 'dark' | 'light' | 'system'
export type Language = 'zh-CN' | 'en-US'
export const logTailOptions = [100, 200, 500, 1000, 2000, 5000] as const
export type LogTail = (typeof logTailOptions)[number]
export const listPageSizeOptions = [10, 20, 50, 100] as const
export type ListPageSize = (typeof listPageSizeOptions)[number]
export const nodeCardSizeOptions = ['small', 'medium', 'large'] as const
export type NodeCardSize = (typeof nodeCardSizeOptions)[number]

interface UIState {
  theme: Theme
  language: Language
  commandOpen: boolean
  sidebarOpen: boolean
  currentNodeID: string
  logTail: LogTail
  listPageSize: ListPageSize
  overviewNodeCardSizes: Record<string, NodeCardSize>
  overviewNodeCardOrder: string[]
  setTheme: (theme: Theme) => void
  setLanguage: (language: Language) => void
  setCommandOpen: (open: boolean) => void
  toggleSidebar: () => void
  setCurrentNodeID: (nodeID: string) => void
  setLogTail: (tail: LogTail) => void
  setListPageSize: (pageSize: ListPageSize) => void
  setOverviewNodeCardSize: (nodeID: string, size: NodeCardSize) => void
  setOverviewNodeCardOrder: (nodeIDs: string[]) => void
}

function applyTheme(theme: Theme) {
  const dark = theme === 'dark' || (theme === 'system' && matchMedia('(prefers-color-scheme: dark)').matches)
  document.documentElement.classList.toggle('dark', dark)
  document.documentElement.dataset.theme = theme
  const background = getComputedStyle(document.documentElement).getPropertyValue('--background').trim()
  if (background) document.querySelector<HTMLMetaElement>('meta[name="theme-color"]')?.setAttribute('content', background)
}

const colorSchemeQuery = matchMedia('(prefers-color-scheme: dark)')
const syncSystemTheme = () => {
  if (localStorage.getItem('suma-theme') === 'system') applyTheme('system')
}
colorSchemeQuery.addEventListener('change', syncSystemTheme)
if (import.meta.hot) {
  import.meta.hot.dispose(() => colorSchemeQuery.removeEventListener('change', syncSystemTheme))
}

const storedTheme = (localStorage.getItem('suma-theme') as Theme | null) ?? 'dark'
const storedLanguage = (localStorage.getItem('suma-language') as Language | null) ?? (navigator.language.toLowerCase().startsWith('zh') ? 'zh-CN' : 'en-US')
const storedLogTailValue = Number(localStorage.getItem('suma-log-tail'))
const storedLogTail: LogTail = logTailOptions.includes(storedLogTailValue as LogTail) ? storedLogTailValue as LogTail : 200
const storedListPageSizeValue = Number(localStorage.getItem('suma-list-page-size'))
const storedListPageSize: ListPageSize = listPageSizeOptions.includes(storedListPageSizeValue as ListPageSize) ? storedListPageSizeValue as ListPageSize : 20
const nodeCardSizesStorageKey = 'suma-overview-node-card-sizes'
const readNodeCardSizes = (): Record<string, NodeCardSize> => {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(nodeCardSizesStorageKey) || '{}')
    if (!value || typeof value !== 'object' || Array.isArray(value)) return {}
    return Object.fromEntries(Object.entries(value).filter((entry): entry is [string, NodeCardSize] => nodeCardSizeOptions.includes(entry[1] as NodeCardSize)))
  } catch {
    return {}
  }
}
const storedNodeCardSizes = readNodeCardSizes()
const nodeCardOrderStorageKey = 'suma-overview-node-card-order'
const readNodeCardOrder = (): string[] => {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(nodeCardOrderStorageKey) || '[]')
    if (!Array.isArray(value)) return []
    return [...new Set(value.filter((item): item is string => typeof item === 'string' && item.length > 0))]
  } catch {
    return []
  }
}
const storedNodeCardOrder = readNodeCardOrder()
applyTheme(storedTheme)
document.documentElement.lang = storedLanguage

export const useUIStore = create<UIState>((set) => ({
  theme: storedTheme,
  language: storedLanguage,
  commandOpen: false,
  sidebarOpen: matchMedia('(min-width: 1024px)').matches,
  currentNodeID: localStorage.getItem('suma-node') || 'local',
  logTail: storedLogTail,
  listPageSize: storedListPageSize,
  overviewNodeCardSizes: storedNodeCardSizes,
  overviewNodeCardOrder: storedNodeCardOrder,
  setTheme: (theme) => {
    localStorage.setItem('suma-theme', theme)
    applyTheme(theme)
    set({ theme })
  },
  setLanguage: (language) => {
    localStorage.setItem('suma-language', language)
    document.documentElement.lang = language
    set({ language })
  },
  setCommandOpen: (commandOpen) => set({ commandOpen }),
  toggleSidebar: () => set((state) => ({ sidebarOpen: !state.sidebarOpen })),
  setCurrentNodeID: (currentNodeID) => { localStorage.setItem('suma-node', currentNodeID); set({ currentNodeID }) },
  setLogTail: (logTail) => { localStorage.setItem('suma-log-tail', String(logTail)); set({ logTail }) },
  setListPageSize: (listPageSize) => { localStorage.setItem('suma-list-page-size', String(listPageSize)); set({ listPageSize }) },
  setOverviewNodeCardSize: (nodeID, size) => set((state) => {
    const overviewNodeCardSizes = { ...state.overviewNodeCardSizes, [nodeID]: size }
    localStorage.setItem(nodeCardSizesStorageKey, JSON.stringify(overviewNodeCardSizes))
    return { overviewNodeCardSizes }
  }),
  setOverviewNodeCardOrder: (overviewNodeCardOrder) => {
    localStorage.setItem(nodeCardOrderStorageKey, JSON.stringify(overviewNodeCardOrder))
    set({ overviewNodeCardOrder })
  },
}))
