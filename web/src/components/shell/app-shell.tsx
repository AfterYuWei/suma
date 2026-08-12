import { useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import {
  Activity, Anchor, Boxes, ChevronRight, CircleGauge, Container, FileClock,
  HardDrive, Layers3, Network, PanelLeftClose,
  PanelLeftOpen, Search, Settings,
} from 'lucide-react'
import { type ReactNode, useEffect } from 'react'
import { api } from '../../lib/api'
import { useI18n, type TranslationKey } from '../../lib/i18n'
import { confirmDialog } from '../../stores/dialog'
import { useUIStore } from '../../stores/ui'
import { ThemeToggle } from '../ui/theme-toggle'
import { CommandPalette } from './command-palette'

const navigation = [
  { label: 'overview', icon: CircleGauge, path: '/' }, { heading: 'Docker' },
  { label: 'containers', icon: Container, path: '/containers' }, { label: 'compose', icon: Layers3, path: '/compose' },
  { label: 'images', icon: Boxes, path: '/images' }, { label: 'networks', icon: Network, path: '/networks' }, { label: 'volumes', icon: HardDrive, path: '/volumes' },
  { heading: 'operations' }, { label: 'tasks', icon: Activity, path: '/tasks' }, { label: 'auditLogs', icon: FileClock, path: '/audit-logs' },
  { heading: 'system' }, { label: 'settings', icon: Settings, path: '/settings' },
]

export function AppShell({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient()
  const { commandOpen, setCommandOpen, sidebarOpen, toggleSidebar, language } = useUIStore()
  const { t } = useI18n()

  useEffect(() => {
    const listener = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault()
        setCommandOpen(!commandOpen)
      }
      if (event.key === 'Escape') setCommandOpen(false)
    }
    window.addEventListener('keydown', listener)
    return () => window.removeEventListener('keydown', listener)
  }, [commandOpen, setCommandOpen])

  const logout = async () => {
    if (!await confirmDialog({ title: t('signOutTitle'), description: t('signOutDescription'), confirmLabel: t('signOut') })) return
    await api('/auth/logout', { method: 'POST' })
    queryClient.setQueryData(['session'], null)
  }

  return <div className="min-h-screen bg-background text-text">
    {sidebarOpen && <button className="fixed inset-0 z-30 bg-black/45 backdrop-blur-sm lg:hidden" onClick={toggleSidebar} aria-label="Close navigation" />}
    <aside className={`shell-sidebar fixed inset-y-0 left-0 z-40 flex w-[264px] flex-col bg-sidebar px-4 py-4 backdrop-blur-2xl transition-[transform,width,padding] duration-300 lg:z-30 lg:translate-x-0 ${sidebarOpen ? 'translate-x-0 lg:w-[264px]' : '-translate-x-full lg:w-20 lg:px-3'}`}>
      <div className={`flex h-11 items-center gap-3 ${sidebarOpen ? 'px-2' : 'justify-center'}`}>
        <span className="relative grid size-9 place-items-center overflow-hidden rounded-xl border border-accent/30 bg-accent/10 text-accent">
          <Anchor className="size-[18px]" strokeWidth={1.8} />
          <span className="absolute inset-x-1 bottom-0 h-px bg-accent/60" />
        </span>
        {sidebarOpen && <>
          <div>
            <p className="text-[15px] font-semibold tracking-[-.035em]">DockPort</p>
            <p className="font-mono text-[9px] uppercase tracking-[.2em] text-text-subtle">Local engine</p>
          </div>
        </>}
      </div>

      <button onClick={() => setCommandOpen(true)} title={sidebarOpen ? undefined : t('searchCommand')} className={`group mt-5 flex h-10 items-center overflow-hidden rounded-xl border border-border bg-surface/60 text-xs text-text-subtle transition-all hover:border-accent/30 hover:bg-surface-hover hover:text-text ${sidebarOpen ? 'px-3' : 'justify-center px-0'}`}>
        <Search className="size-3.5 shrink-0 transition-transform group-hover:scale-110" />
        <span className={`flex min-w-0 items-center overflow-hidden transition-[max-width,opacity,margin] ${sidebarOpen ? 'ml-auto max-w-16 opacity-100 delay-300 duration-150' : 'ml-0 max-w-0 opacity-0 duration-75'}`}>
          <kbd className="shrink-0 rounded-md border border-border bg-background/60 px-1.5 py-0.5 font-mono text-[9px] tracking-wide">CTRL K</kbd>
        </span>
      </button>

      <nav className={`mt-5 flex-1 overflow-y-auto ${sidebarOpen ? 'pr-1' : ''}`}>
        {navigation.map((item, index) => {
          if ('heading' in item) return sidebarOpen
            ? <p key={item.heading} className="mb-2 mt-6 px-3 font-mono text-[9px] font-medium uppercase tracking-[.22em] text-text-subtle">{item.heading === 'Docker' ? 'Docker' : t(item.heading as TranslationKey)}</p>
            : <div key={item.heading} className="mx-2 my-4 border-t border-border" />
          const Icon = item.icon
          return <Link key={`${item.label}-${index}`} to={item.path} title={sidebarOpen ? undefined : t(item.label as TranslationKey)} activeOptions={{ exact: item.path === '/' }} className={`group relative mb-1 flex h-10 items-center overflow-hidden rounded-xl text-[13px] text-text-muted transition-all hover:bg-surface-hover hover:text-text [&.active]:bg-surface-hover [&.active]:font-medium [&.active]:text-text ${sidebarOpen ? 'gap-3 px-3' : 'justify-center'}`}>
            <Icon className="size-[17px] text-text-subtle transition-colors group-hover:text-text group-[.active]:text-accent" strokeWidth={1.6} />
            {sidebarOpen && <><span>{t(item.label as TranslationKey)}</span><ChevronRight className="ml-auto size-3 translate-x-1 text-text-subtle opacity-0 transition-all group-hover:translate-x-0 group-hover:opacity-100 group-[.active]:translate-x-0 group-[.active]:opacity-100" /></>}
          </Link>
        })}
      </nav>

      <button onClick={toggleSidebar} className={`mt-4 flex h-10 items-center rounded-xl text-text-muted transition-colors hover:bg-surface-hover hover:text-text ${sidebarOpen ? 'gap-3 px-3' : 'justify-center'}`} aria-label={sidebarOpen ? 'Close sidebar' : 'Open sidebar'} title={sidebarOpen ? undefined : 'Open sidebar'}>
        {sidebarOpen ? <PanelLeftClose className="size-[17px]" strokeWidth={1.6} /> : <PanelLeftOpen className="size-[17px]" strokeWidth={1.6} />}
        {sidebarOpen && <span className="text-[13px]">{language === 'zh-CN' ? '收起菜单' : 'Collapse sidebar'}</span>}
      </button>
    </aside>

    <div className={`min-h-screen transition-[padding] duration-300 ${sidebarOpen ? 'lg:pl-[264px]' : 'lg:pl-20'}`}>
      <header className="shell-header sticky top-0 z-20 flex h-16 items-center bg-background/55 px-4 backdrop-blur-2xl sm:px-6 lg:px-8">
        <button onClick={toggleSidebar} className="grid size-9 place-items-center rounded-xl border border-border bg-surface/60 text-text-muted transition-colors hover:bg-surface-hover hover:text-text lg:hidden" aria-label="Toggle sidebar">
          <PanelLeftOpen className="size-4" strokeWidth={1.6} />
        </button>
        <div className="ml-4 hidden items-center gap-2 font-mono text-[10px] uppercase tracking-[.15em] text-text-subtle sm:flex">
          <span>DockPort</span><ChevronRight className="size-3" /><span className="text-text-muted">Control plane</span>
        </div>
        <div className="ml-auto flex items-center gap-2">
          <div className="mr-2 hidden items-center gap-2 text-[10px] text-text-subtle md:flex"><span className="signal-dot size-1.5 rounded-full bg-success" />Live telemetry</div>
          <ThemeToggle />
          <button onClick={logout} title={t('signOut')} className="grid size-9 place-items-center rounded-xl border border-border bg-surface/60 font-mono text-[10px] font-semibold text-text-muted transition-colors hover:border-accent/30 hover:text-text">DP</button>
        </div>
      </header>
      <main className="mx-auto max-w-[1540px] px-4 py-7 sm:px-6 lg:px-10 lg:py-10">{children}</main>
    </div>
    <CommandPalette open={commandOpen} close={() => setCommandOpen(false)} />
  </div>
}
