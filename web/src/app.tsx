import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { Outlet, RouterProvider, createRootRoute, createRoute, createRouter } from '@tanstack/react-router'
import { lazy, Suspense, type ComponentType } from 'react'
import { LoaderCircle } from 'lucide-react'
import { AppShell } from './components/shell/app-shell'
import { AuthGate } from './features/auth/auth-gate'
import { OverviewPage } from './pages/overview'
import { ContainersPage } from './pages/containers'
import { ImagesPage } from './pages/images'
import { NetworksPage } from './pages/networks'
import { VolumesPage } from './pages/volumes'
import { TasksPage } from './pages/tasks'
import { AuditLogsPage } from './pages/audit-logs'
import { ComposePage } from './pages/compose'
import { SettingsPage } from './pages/settings'
import { AppDialog } from './components/ui/app-dialog'

const ContainerDetailPage = lazy(() => import('./pages/container-detail').then((module) => ({ default: module.ContainerDetailPage })))
const ComposeDetailPage = lazy(() => import('./pages/compose-detail').then((module) => ({ default: module.ComposeDetailPage })))
const deferred = (Component: ComponentType) => () => <Suspense fallback={<div className="grid min-h-72 place-items-center"><div className="text-center"><LoaderCircle className="mx-auto size-5 animate-spin text-accent" /><p className="mt-3 font-mono text-[9px] uppercase tracking-[.2em] text-text-subtle">Loading module</p></div></div>}><Component /></Suspense>

const rootRoute = createRootRoute({ component: () => <AuthGate><AppShell><Outlet /></AppShell></AuthGate> })
const overviewRoute = createRoute({ getParentRoute: () => rootRoute, path: '/', component: OverviewPage })
const containersRoute = createRoute({ getParentRoute: () => rootRoute, path: '/containers', component: ContainersPage })
const containerDetailRoute = createRoute({ getParentRoute: () => rootRoute, path: '/containers/$containerId', component: deferred(ContainerDetailPage) })
const imagesRoute = createRoute({ getParentRoute: () => rootRoute, path: '/images', component: ImagesPage })
const networksRoute = createRoute({ getParentRoute: () => rootRoute, path: '/networks', component: NetworksPage })
const volumesRoute = createRoute({ getParentRoute: () => rootRoute, path: '/volumes', component: VolumesPage })
const tasksRoute = createRoute({ getParentRoute: () => rootRoute, path: '/tasks', component: TasksPage })
const auditRoute = createRoute({ getParentRoute: () => rootRoute, path: '/audit-logs', component: AuditLogsPage })
const composeRoute = createRoute({ getParentRoute: () => rootRoute, path: '/compose', component: ComposePage })
const composeDetailRoute = createRoute({ getParentRoute: () => rootRoute, path: '/compose/$projectName', component: deferred(ComposeDetailPage) })
const settingsRoute = createRoute({ getParentRoute: () => rootRoute, path: '/settings', component: SettingsPage })
const router = createRouter({ routeTree: rootRoute.addChildren([overviewRoute, containersRoute, containerDetailRoute, imagesRoute, networksRoute, volumesRoute, tasksRoute, auditRoute, composeRoute, composeDetailRoute, settingsRoute]) })
const queryClient = new QueryClient({ defaultOptions: { queries: { staleTime: 5_000, retry: 1 } } })

declare module '@tanstack/react-router' { interface Register { router: typeof router } }

export function App() {
  return <QueryClientProvider client={queryClient}><RouterProvider router={router} /><AppDialog /></QueryClientProvider>
}
