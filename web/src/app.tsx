import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { Outlet, RouterProvider, createRootRoute, createRoute, createRouter, redirect } from '@tanstack/react-router'
import { lazy, Suspense, type ComponentType } from 'react'
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
import { ContinuousDeliveryPage } from './pages/continuous-delivery'
import { SettingsPage } from './pages/settings'
import { AuthenticationPage } from './pages/authentication'
import { NodesPage } from './pages/nodes'
import { AppDialog } from './components/ui/app-dialog'
import { Spinner } from './components/ui/spinner'
import { TooltipProvider } from './components/ui/tooltip'

const ContainerDetailPage = lazy(() => import('./pages/container-detail').then((module) => ({ default: module.ContainerDetailPage })))
const ComposeDetailPage = lazy(() => import('./pages/compose-detail').then((module) => ({ default: module.ComposeDetailPage })))
const ProjectTakeoverPage = lazy(() => import('./pages/project-takeover').then((module) => ({ default: module.ProjectTakeoverPage })))
const ContinuousDeliveryDetailPage = lazy(() => import('./pages/continuous-delivery-detail').then((module) => ({ default: module.ContinuousDeliveryDetailPage })))
const deferred = (Component: ComponentType) => () => <Suspense fallback={<div role="status" className="grid min-h-72 place-items-center gap-2 text-muted-foreground"><Spinner /><span className="text-sm">Loading module</span></div>}><Component /></Suspense>

const rootRoute = createRootRoute({ component: () => <AuthGate><AppShell><Outlet /></AppShell></AuthGate> })
const overviewRoute = createRoute({ getParentRoute: () => rootRoute, path: '/', component: OverviewPage })
const containersRoute = createRoute({ getParentRoute: () => rootRoute, path: '/containers', component: ContainersPage })
const containerDetailRoute = createRoute({ getParentRoute: () => rootRoute, path: '/containers/$containerId', component: deferred(ContainerDetailPage) })
const imagesRoute = createRoute({ getParentRoute: () => rootRoute, path: '/images', component: ImagesPage })
const networksRoute = createRoute({ getParentRoute: () => rootRoute, path: '/networks', component: NetworksPage })
const volumesRoute = createRoute({ getParentRoute: () => rootRoute, path: '/volumes', component: VolumesPage })
const tasksRoute = createRoute({ getParentRoute: () => rootRoute, path: '/tasks', component: TasksPage })
const auditRoute = createRoute({ getParentRoute: () => rootRoute, path: '/audit-logs', component: AuditLogsPage })
const projectsRoute = createRoute({ getParentRoute: () => rootRoute, path: '/projects', component: ComposePage })
const projectDetailRoute = createRoute({ getParentRoute: () => rootRoute, path: '/projects/$backend/$projectName', component: deferred(ComposeDetailPage) })
const projectTakeoverRoute = createRoute({ getParentRoute: () => rootRoute, path: '/projects/$backend/$projectName/takeover', component: deferred(ProjectTakeoverPage) })
const composeRoute = createRoute({ getParentRoute: () => rootRoute, path: '/compose', beforeLoad: () => { throw redirect({ to: '/projects' }) } })
const composeDetailRoute = createRoute({ getParentRoute: () => rootRoute, path: '/compose/$projectName', beforeLoad: ({ params }) => { throw redirect({ to: '/projects/$backend/$projectName', params: { backend: 'compose', projectName: params.projectName } }) } })
const continuousDeliveryRoute = createRoute({ getParentRoute: () => rootRoute, path: '/continuous-delivery', component: ContinuousDeliveryPage })
const continuousDeliveryDetailRoute = createRoute({ getParentRoute: () => rootRoute, path: '/continuous-delivery/$projectName', component: deferred(ContinuousDeliveryDetailPage) })
const settingsRoute = createRoute({ getParentRoute: () => rootRoute, path: '/settings', component: SettingsPage })
const authenticationRoute = createRoute({ getParentRoute: () => rootRoute, path: '/authentication', component: AuthenticationPage })
const nodesRoute = createRoute({ getParentRoute: () => rootRoute, path: '/nodes', component: NodesPage })
const router = createRouter({ routeTree: rootRoute.addChildren([overviewRoute, containersRoute, containerDetailRoute, imagesRoute, networksRoute, volumesRoute, tasksRoute, auditRoute, projectsRoute, projectDetailRoute, projectTakeoverRoute, composeRoute, composeDetailRoute, continuousDeliveryRoute, continuousDeliveryDetailRoute, authenticationRoute, nodesRoute, settingsRoute]) })
const queryClient = new QueryClient({ defaultOptions: { queries: { staleTime: 5_000, retry: 1 } } })

declare module '@tanstack/react-router' { interface Register { router: typeof router } }

export function App() {
  return <QueryClientProvider client={queryClient}><TooltipProvider><RouterProvider router={router} /><AppDialog /></TooltipProvider></QueryClientProvider>
}
