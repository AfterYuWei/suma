import { create } from 'zustand'

export type Theme = 'dark' | 'light' | 'system'
export type Language = 'zh-CN' | 'en-US'

interface UIState {
  theme: Theme
  language: Language
  commandOpen: boolean
  sidebarOpen: boolean
	currentNodeID: string
  setTheme: (theme: Theme) => void
  setLanguage: (language: Language) => void
  setCommandOpen: (open: boolean) => void
  toggleSidebar: () => void
	setCurrentNodeID: (nodeID: string) => void
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
applyTheme(storedTheme)
document.documentElement.lang = storedLanguage

export const useUIStore = create<UIState>((set) => ({
  theme: storedTheme,
  language: storedLanguage,
  commandOpen: false,
  sidebarOpen: matchMedia('(min-width: 1024px)').matches,
	currentNodeID: localStorage.getItem('suma-node') || 'local',
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
}))
