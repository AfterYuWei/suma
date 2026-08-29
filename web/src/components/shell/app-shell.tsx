import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate, useRouterState } from '@tanstack/react-router'
import {
  Activity, Boxes, CircleGauge, Container, FileClock, GitPullRequest,
  HardDrive, KeyRound, Layers3, Network, PanelLeftClose,
  LogOut, PanelLeftOpen, Search, Server, Settings, UserRound,
} from 'lucide-react'
import { type ReactNode, useEffect, useState } from 'react'
import { api } from '../../lib/api'
import type { User } from '../../features/auth/types'
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
import { Button } from '../ui/button'
import { ThemeToggle } from '../ui/theme-toggle'
import { TooltipHint } from '../ui/tooltip-hint'
import { UserAvatar } from '../ui/user-avatar'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from '../ui/dropdown-menu'
import { cn } from '@/lib/utils'
import { CommandPalette } from './command-palette'

const navigationSections = [
  { key: 'docker', label: 'Docker', rawLabel: false, items: [{ label: 'containers', path: '/containers', icon: Container }, { label: 'projects', path: '/projects', icon: Layers3 }, { label: 'images', path: '/images', icon: Boxes }, { label: 'networks', path: '/networks', icon: Network }, { label: 'volumes', path: '/volumes', icon: HardDrive }] },
  { key: 'operations', label: 'operations', rawLabel: true, items: [{ label: 'continuousDelivery', path: '/continuous-delivery', icon: GitPullRequest }, { label: 'authenticationCenter', path: '/authentication', icon: KeyRound }, { label: 'tasks', path: '/tasks', icon: Activity }, { label: 'auditLogs', path: '/audit-logs', icon: FileClock }] },
  { key: 'system', label: 'system', rawLabel: true, items: [{ label: 'nodes', path: '/nodes', icon: Server }, { label: 'settings', path: '/settings', icon: Settings }] },
] as const

interface NavEntry { key: string; label: string; icon: typeof Container }
interface NavSection { key: string; label: string; items: NavEntry[] }

/** Latency tiers shared by trigger and items: dot + text color. */
function latencyTone(node: DockerNode) {
  if (node.status !== 'online' || node.last_latency_ms == null) {
    return { dot: 'bg-muted-foreground/60 text-muted-foreground/60', text: 'text-muted-foreground/60' }
  }
  if (node.last_latency_ms < 150) return { dot: 'bg-emerald-500 text-emerald-600 dark:text-emerald-400', text: 'text-emerald-600 dark:text-emerald-400' }
  if (node.last_latency_ms < 500) return { dot: 'bg-amber-500 text-amber-600 dark:text-amber-400', text: 'text-amber-600 dark:text-amber-400' }
  return { dot: 'bg-red-500 text-red-600 dark:text-red-400', text: 'text-red-600 dark:text-red-400' }
}

export function AppShell({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const { commandOpen, setCommandOpen, sidebarOpen, toggleSidebar, language, currentNodeID, setCurrentNodeID } = useUIStore()
  const [mobileNavOpen, setMobileNavOpen] = useState(false)
  const { t } = useI18n()
  const zh = language === 'zh-CN'
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  const nodes = useQuery({ queryKey: ['nodes'], queryFn: () => api<DockerNode[]>('/nodes'), refetchInterval: 30_000 })
  const session = useQuery({ queryKey: ['session'], queryFn: () => api<User>('/auth/session') })

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
        className={cn(
          'w-full font-normal',
          collapsed ? 'justify-center px-0' : 'justify-start gap-2',
          active && 'bg-muted font-medium text-foreground aria-expanded:bg-muted aria-expanded:text-foreground'
        )}
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
          {!collapsed && <span className="text-sm font-semibold">SUMA</span>}
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
          <div className="p-2">
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
    <div className="flex h-screen overflow-hidden bg-background">
      <aside className="hidden shrink-0 overflow-hidden transition-all lg:block" style={{ width: sidebarOpen ? 240 : 72 }}>
        {navigation()}
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-14 shrink-0 items-center gap-2 px-4 md:px-6">
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
            <Select value={currentNodeID} onValueChange={(value) => setCurrentNodeID(String(value))}>
              <SelectTrigger aria-label={zh ? '当前 Docker 节点' : 'Current Docker node'} className="w-32 sm:w-52">
                <SelectValue>
                  {currentNode && (
                    <span className="flex min-w-0 items-center gap-2">
                      <span aria-hidden="true" className={cn('size-1.5 shrink-0 rounded-full bg-current', latencyTone(currentNode).dot)} />
                      <span className="truncate">{currentNode.name}</span>
                      <span className={cn('ml-auto shrink-0 text-xs leading-none tabular-nums', latencyTone(currentNode).text)}>
                        {currentNode.status === 'online' && currentNode.last_latency_ms != null ? `${currentNode.last_latency_ms}ms` : zh ? '离线' : 'offline'}
                      </span>
                    </span>
                  )}
                </SelectValue>
              </SelectTrigger>
              <SelectContent>
                {(nodes.data ?? []).map((node) => {
                  const tone = latencyTone(node)
                  return (
                    <SelectItem key={node.id} value={node.id} disabled={!node.enabled}>
                      <span className="flex w-full items-center gap-2">
                        <span aria-hidden="true" className={cn('size-1.5 shrink-0 rounded-full bg-current', tone.dot)} />
                        <span className="truncate">{`${node.name} · ${node.connection_type.toUpperCase()}`}</span>
                        <span className={cn('ml-auto shrink-0 text-xs leading-none tabular-nums', tone.text)}>
                          {node.status === 'online' && node.last_latency_ms != null ? `${node.last_latency_ms}ms` : zh ? '离线' : 'offline'}
                        </span>
                      </span>
                    </SelectItem>
                  )
                })}
              </SelectContent>
            </Select>
          </div>

          <div className="ml-auto flex items-center gap-1.5">
            <TooltipHint content={`${t('searchCommand')} (Ctrl+K)`}><Button
              variant="ghost"
              size="icon-sm"
              className="text-muted-foreground"
              aria-label={t('searchCommand')}
              onClick={() => setCommandOpen(true)}
            >
              <Search />
            </Button></TooltipHint>
            <ThemeToggle />
            {session.data && <DropdownMenu>
              <DropdownMenuTrigger render={<Button variant="ghost" size="sm" className="h-9 gap-2 px-1.5 sm:px-2" aria-label={zh ? '用户菜单' : 'User menu'} />}>
                <UserAvatar user={session.data} className="size-7" />
                <span className="hidden max-w-32 truncate text-sm font-medium sm:inline">{session.data.nickname || session.data.username}</span>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-56">
                <DropdownMenuLabel className="flex flex-col gap-0.5">
                  <span className="truncate text-sm text-foreground">{session.data.nickname || session.data.username}</span>
                  <span className="truncate font-normal">{session.data.email || session.data.username}</span>
                </DropdownMenuLabel>
                <DropdownMenuSeparator />
                <DropdownMenuItem onClick={() => void navigate({ to: '/account' })}><UserRound />{zh ? '账户设置' : 'Account settings'}</DropdownMenuItem>
                <DropdownMenuItem onClick={() => void logout()}><LogOut />{t('signOut')}</DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>}
          </div>
        </header>

        <main className="min-h-0 min-w-0 flex-1 px-4 pb-4 md:px-6 md:pb-6">
          <div className="h-full overflow-y-auto rounded-xl border border-border/60 bg-card shadow-sm ring-1 ring-black/[0.03] px-4 py-5 md:px-6 xl:px-8 dark:shadow-none dark:ring-white/[0.04]">{children}</div>
        </main>
      </div>

      <Sheet open={mobileNavOpen} onOpenChange={setMobileNavOpen}>
        <SheetContent side="left" className="w-60 p-0" showCloseButton={false}>
          <SheetTitle className="sr-only">{zh ? '主导航' : 'Primary navigation'}</SheetTitle>
          {navigation(true)}
        </SheetContent>
      </Sheet>
      <CommandPalette open={commandOpen} close={() => setCommandOpen(false)} />
    </div>
  )
}
