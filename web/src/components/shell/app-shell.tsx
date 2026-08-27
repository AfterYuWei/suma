import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useRouterState } from '@tanstack/react-router'
import {
  Activity, Boxes, CircleGauge, Container, FileClock, GitPullRequest,
  HardDrive, KeyRound, Layers3, Network, PanelLeftClose,
  PanelLeftOpen, Search, Server, Settings,
} from 'lucide-react'
import { type ReactNode, useEffect, useState } from 'react'
import { api } from '../../lib/api'
import { useI18n, type TranslationKey } from '../../lib/i18n'
import type { DockerNode } from '../../lib/nodes'
import { confirmDialog } from '../../stores/dialog'
import { useUIStore } from '../../stores/ui'
import { LogoMark } from '../ui/logo-mark'
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from '../ui/select'
import { Separator } from '../ui/separator'
import { Sheet, SheetContent, SheetTitle } from '../ui/sheet'
import { Badge } from '../ui/badge'
import { Button } from '../ui/button'
import { ThemeToggle } from '../ui/theme-toggle'
import { cn } from '@/lib/utils'
import { CommandPalette } from './command-palette'

const navigationSections = [
  { key: 'docker', label: 'Docker', rawLabel: false, items: [{ label: 'containers', path: '/containers', icon: Container }, { label: 'compose', path: '/compose', icon: Layers3 }, { label: 'images', path: '/images', icon: Boxes }, { label: 'networks', path: '/networks', icon: Network }, { label: 'volumes', path: '/volumes', icon: HardDrive }] },
  { key: 'operations', label: 'operations', rawLabel: true, items: [{ label: 'continuousDelivery', path: '/continuous-delivery', icon: GitPullRequest }, { label: 'authenticationCenter', path: '/authentication', icon: KeyRound }, { label: 'tasks', path: '/tasks', icon: Activity }, { label: 'auditLogs', path: '/audit-logs', icon: FileClock }] },
  { key: 'system', label: 'system', rawLabel: true, items: [{ label: 'nodes', path: '/nodes', icon: Server }, { label: 'settings', path: '/settings', icon: Settings }] },
] as const

interface NavEntry { key: string; label: string; icon: typeof Container }
interface NavSection { key: string; label: string; items: NavEntry[] }

export function AppShell({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient()
  const { commandOpen, setCommandOpen, sidebarOpen, toggleSidebar, language, currentNodeID, setCurrentNodeID } = useUIStore()
  const [mobileNavOpen, setMobileNavOpen] = useState(false)
  const { t } = useI18n()
  const zh = language === 'zh-CN'
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  const nodes = useQuery({ queryKey: ['nodes'], queryFn: () => api<DockerNode[]>('/nodes'), refetchInterval: 30_000 })

  useEffect(() => {
    if (!nodes.data?.length) return
    if (!nodes.data.some((node) => node.id === currentNodeID && node.enabled)) {
      setCurrentNodeID(nodes.data.find((node) => node.enabled && node.connection_type === 'unix')?.id ?? nodes.data.find((node) => node.enabled)?.id ?? nodes.data[0].id)
    }
  }, [nodes.data, currentNodeID, setCurrentNodeID])

  useEffect(() => {
    const listener = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault()
        setCommandOpen(!commandOpen)
      }
    }
    window.addEventListener('keydown', listener)
    return () => window.removeEventListener('keydown', listener)
  }, [commandOpen, setCommandOpen])

  const selectedPath = pathname === '/'
    ? '/'
    : navigationSections.map((section) => section.items.find((item) => pathname.startsWith(item.path))?.path).find(Boolean) ?? ''

  const sections: NavSection[] = [
    { key: 'overview', label: '', items: [{ key: '/', label: t('overview'), icon: CircleGauge }] },
    ...navigationSections.map((section) => ({
      key: section.key,
      label: section.rawLabel ? t(section.label as TranslationKey) : 'Docker',
      items: section.items.map(({ label, path, icon }) => ({ key: path, label: t(label as TranslationKey), icon })),
    })),
  ]

  const logout = async () => {
    if (!await confirmDialog({ title: t('signOutTitle'), description: t('signOutDescription'), confirmLabel: t('signOut') })) return
    await api('/auth/logout', { method: 'POST' })
    queryClient.setQueryData(['session'], null)
  }

  const NavLink = ({ entry }: { entry: NavEntry }) => {
    const Icon = entry.icon
    const collapsed = !sidebarOpen
    const active = selectedPath === entry.key
    return (
      <Button
        variant="ghost"
        size="sm"
        className={cn('w-full font-normal', collapsed ? 'justify-center px-0' : 'justify-start gap-2')}
        data-active={active}
        aria-current={active ? 'page' : undefined}
        onClick={() => setMobileNavOpen(false)}
        render={<Link to={entry.key as never} />}
      >
        <Icon />
        <span className={collapsed ? 'sr-only' : undefined}>{entry.label}</span>
      </Button>
    )
  }

  const navigation = (mobile = false) => {
    const collapsed = !sidebarOpen && !mobile
    return (
      <div className="flex h-full flex-col">
        <div className={cn('flex h-14 items-center gap-2 px-3', collapsed && 'justify-center px-0')}>
          <LogoMark className="size-7" />
          {!collapsed && <span className="text-sm font-semibold">DockPort</span>}
        </div>
        <nav aria-label={zh ? '主导航' : 'Primary navigation'} className="flex-1 overflow-y-auto px-2 pb-3">
          {sections.map((section, index) => (
            <div key={section.key}>
              {index > 0 && <Separator className="my-2 opacity-60" />}
              {section.label ? (
                <p className={cn('px-2 py-1.5 text-xs font-medium tracking-wide text-muted-foreground uppercase', collapsed && 'text-center text-[10px] normal-case')}>
                  {collapsed ? section.label.slice(0, 1).toUpperCase() : section.label}
                </p>
              ) : (
                <div className="h-1" />
              )}
              <div className="flex flex-col gap-0.5">
                {section.items.map((entry) => <NavLink key={entry.key} entry={{ ...entry, icon: entry.icon as unknown as typeof Container }} />)}
              </div>
            </div>
          ))}
        </nav>
        {!mobile && (
          <div className="border-t p-2">
            <Button
              variant="ghost"
              size="sm"
              className={cn('w-full font-normal text-muted-foreground', collapsed ? 'justify-center px-0' : 'justify-start gap-2')}
              onClick={toggleSidebar}
            >
              {collapsed ? <PanelLeftOpen /> : <PanelLeftClose />}
              <span className={collapsed ? 'sr-only' : undefined}>{collapsed ? (zh ? '展开菜单' : 'Expand') : (zh ? '收起菜单' : 'Collapse')}</span>
            </Button>
          </div>
        )}
      </div>
    )
  }

  const currentNode = nodes.data?.find((node) => node.id === currentNodeID)

  return (
    <div className="flex min-h-screen">
      <aside className="sticky top-0 hidden h-screen shrink-0 overflow-hidden border-r bg-sidebar text-sidebar-foreground transition-all lg:block" style={{ width: sidebarOpen ? 240 : 72 }}>
        {navigation()}
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="sticky top-0 z-40 flex h-14 shrink-0 items-center gap-2 border-b bg-background/95 px-4 backdrop-blur md:px-6">
          <Button
            variant="ghost"
            size="icon"
            className="lg:hidden"
            aria-label={zh ? '打开导航' : 'Open navigation'}
            onClick={() => setMobileNavOpen(true)}
          >
            <PanelLeftOpen />
          </Button>

          <div className="flex items-center gap-2">
            <Badge variant={currentNode?.status === 'online' ? 'outline' : 'ghost'} className={cn('rounded-full', currentNode?.status === 'online' && 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400')} >
              <span className="size-1.5 rounded-full bg-current" aria-hidden="true" />
              {currentNode?.status === 'online' ? (zh ? '在线' : 'Online') : (zh ? '未知' : 'Offline')}
            </Badge>
            <Select value={currentNodeID} onValueChange={(value) => setCurrentNodeID(String(value))}>
              <SelectTrigger aria-label={zh ? '当前 Docker 节点' : 'Current Docker node'} className="w-28 sm:w-48">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {(nodes.data ?? []).map((node) => (
                  <SelectItem key={node.id} value={node.id} disabled={!node.enabled}>
                    {`${node.name} · ${node.connection_type.toUpperCase()}`}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <div className="ml-auto flex items-center gap-1.5">
            <span className="hidden text-sm text-muted-foreground md:inline">{zh ? '控制平面在线' : 'Control plane online'}</span>
            <Button variant="ghost" size="sm" className="gap-1.5 text-muted-foreground" onClick={() => setCommandOpen(true)}>
              <Search />
              <span className="hidden sm:inline">{t('searchCommand')}</span>
            </Button>
            <ThemeToggle />
            <Button variant="ghost" size="sm" className="hidden text-xs font-semibold sm:inline-flex" title={t('signOut')} onClick={() => void logout()}>
              DP
            </Button>
          </div>
        </header>

        <main className="min-w-0 flex-1 px-4 py-5 md:px-6 xl:px-8">{children}</main>
      </div>

      <Sheet open={mobileNavOpen} onOpenChange={setMobileNavOpen}>
        <SheetContent side="left" className="w-60 bg-sidebar p-0" showCloseButton={false}>
          <SheetTitle className="sr-only">{zh ? '主导航' : 'Primary navigation'}</SheetTitle>
          {navigation(true)}
        </SheetContent>
      </Sheet>
      <CommandPalette open={commandOpen} close={() => setCommandOpen(false)} />
    </div>
  )
}
