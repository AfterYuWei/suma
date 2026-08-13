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
  document.querySelector<HTMLMetaElement>('meta[name="theme-color"]')?.setAttribute('content', dark ? '#121417' : '#f2f3f5')
}

const storedTheme = (localStorage.getItem('dockport-theme') as Theme | null) ?? 'dark'
const storedLanguage = (localStorage.getItem('dockport-language') as Language | null) ?? (navigator.language.toLowerCase().startsWith('zh') ? 'zh-CN' : 'en-US')
applyTheme(storedTheme)
document.documentElement.lang = storedLanguage

export const useUIStore = create<UIState>((set) => ({
  theme: storedTheme,
  language: storedLanguage,
  commandOpen: false,
  sidebarOpen: matchMedia('(min-width: 1024px)').matches,
	currentNodeID: localStorage.getItem('dockport-node') || 'local',
  setTheme: (theme) => {
    localStorage.setItem('dockport-theme', theme)
    applyTheme(theme)
    set({ theme })
  },
  setLanguage: (language) => {
    localStorage.setItem('dockport-language', language)
    document.documentElement.lang = language
    set({ language })
  },
  setCommandOpen: (commandOpen) => set({ commandOpen }),
  toggleSidebar: () => set((state) => ({ sidebarOpen: !state.sidebarOpen })),
	setCurrentNodeID: (currentNodeID) => { localStorage.setItem('dockport-node', currentNodeID); set({ currentNodeID }) },
}))
