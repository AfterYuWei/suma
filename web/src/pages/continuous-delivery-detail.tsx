import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useParams } from '@tanstack/react-router'
import { ChevronLeft, RefreshCw, Trash2 } from 'lucide-react'
import { useEffect, useState } from 'react'
import { Badge } from '../components/ui/badge'
import { Button } from '../components/ui/button'
import { Spinner } from '../components/ui/spinner'
import { Tabs, TabsList, TabsTrigger } from '../components/ui/tabs'
import { TooltipHint } from '../components/ui/tooltip-hint'
import { ErrorState } from '../components/ui/error-state'
import { LoadingState } from '../components/ui/loading-state'
import { CDOverview, ReleasePanel, type ReleaseOperation } from '../features/delivery/releases'
import { CDSettings } from '../features/delivery/settings'
import type { CDConfiguration, CDDrift, DeliveryProject, DeliveryRelease } from '../features/delivery/types'
import { hasActiveRelease, shortCommit } from '../features/delivery/types'
import { api, ApiError } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { cn } from '../lib/utils'
import { confirmDialog, promptDialog, promptWithCheckboxDialog } from '../stores/dialog'
import { ResourceFrame } from './images'

type View = 'Overview' | 'Releases' | 'Settings'

export function ContinuousDeliveryDetailPage() {
  const { projectName } = useParams({ from: '/continuous-delivery/$projectName' })
  const encodedName = encodeURIComponent(projectName)
  const client = useQueryClient()
  const navigate = useNavigate()
  const { language } = useI18n()
  const zh = language === 'zh-CN'
  const projectQuery = useQuery({ queryKey: ['delivery-project', projectName], queryFn: () => api<DeliveryProject>(`/delivery-projects/${encodedName}`) })
  const cdQuery = useQuery({ queryKey: ['delivery-configuration', projectName], queryFn: () => api<CDConfiguration>(`/delivery-projects/${encodedName}/configuration`), refetchInterval: 5_000 })
  const isGit = cdQuery.data?.configured === true
  const driftQuery = useQuery({ queryKey: ['delivery-drift', projectName], queryFn: () => api<CDDrift>(`/delivery-projects/${encodedName}/drift`), enabled: isGit, refetchInterval: 5_000 })
  const releasesQuery = useQuery({ queryKey: ['delivery-releases', projectName], queryFn: () => api<DeliveryRelease[]>(`/delivery-projects/${encodedName}/releases`), enabled: isGit, refetchInterval: 5_000 })
  const [view, setView] = useState<View>('Overview')
  const [notice, setNotice] = useState('')

  useEffect(() => {
    if (cdQuery.data && !cdQuery.data.configured) setView('Settings')
  }, [cdQuery.data])

  const sync = useMutation({
    mutationFn: () => api(`/delivery-projects/${encodedName}/sync`, { method: 'POST' }),
    onSuccess: () => {
      setNotice(zh ? '同步任务已启动。' : 'Synchronization task started.')
      void client.invalidateQueries({ queryKey: ['tasks'] })
      void client.invalidateQueries({ queryKey: ['delivery-drift', projectName] })
      void client.invalidateQueries({ queryKey: ['delivery-releases', projectName] })
    },
    onError: (error) => setNotice(error.message),
  })
  const releaseAction = useMutation({
    mutationFn: ({ release, operation }: { release: DeliveryRelease; operation: ReleaseOperation }) => api(`/delivery-projects/${encodedName}/releases/${release.id}/${operation}`, { method: 'POST' }),
    onSuccess: (_, variables) => {
      const queued = variables.operation === 'deploy' || variables.operation === 'rollback'
      setNotice(queued ? (zh ? '交付任务已启动。' : 'Delivery task started.') : variables.operation === 'approve' ? (zh ? 'Release 已批准。' : 'Release approved.') : (zh ? 'Release 已拒绝。' : 'Release rejected.'))
      if (queued) void client.invalidateQueries({ queryKey: ['tasks'] })
      void client.invalidateQueries({ queryKey: ['delivery-releases', projectName] })
      void client.invalidateQueries({ queryKey: ['delivery-drift', projectName] })
    },
    onError: (error) => setNotice(error.message),
  })

  const runReleaseAction = async (release: DeliveryRelease, operation: ReleaseOperation) => {
    let confirmed = false
    if (operation === 'approve') confirmed = await confirmDialog({ title: zh ? `批准 Release #${release.id}？` : `Approve release #${release.id}?`, description: zh ? `批准 Commit ${shortCommit(release.commit_sha)} 进入可发布状态。此操作本身不会部署。` : `Mark commit ${shortCommit(release.commit_sha)} as ready to deploy. Approval does not deploy it.`, confirmLabel: zh ? '批准' : 'Approve' })
    if (operation === 'reject') confirmed = await confirmDialog({ title: zh ? `拒绝 Release #${release.id}？` : `Reject release #${release.id}?`, description: zh ? '该候选 Release 将不能再发布。' : 'This candidate release can no longer be deployed.', confirmLabel: zh ? '拒绝' : 'Reject', danger: true })
    if (operation === 'deploy') confirmed = await confirmDialog({ title: zh ? `${release.status === 'failed' ? '重新发布' : '发布'} Release #${release.id}？` : `${release.status === 'failed' ? 'Redeploy' : 'Deploy'} release #${release.id}?`, description: zh ? `将 Commit ${shortCommit(release.commit_sha)} 并行应用到 Release 快照中的全部目标节点；失败节点独立回滚。` : `Apply commit ${shortCommit(release.commit_sha)} to every node in the release target snapshot in parallel; failed nodes roll back independently.`, confirmLabel: release.status === 'failed' ? (zh ? '重新发布' : 'Redeploy') : (zh ? '发布' : 'Deploy') })
    if (operation === 'rollback') confirmed = await confirmDialog({ title: zh ? `回滚到 Release #${release.id}？` : `Restore release #${release.id}?`, description: zh ? '这会重新应用该版本，但不会回滚存储卷中的数据；自动交付会切换为手动。' : 'This reapplies the release without rolling back volume data; automatic delivery switches to manual.', confirmLabel: zh ? '回滚' : 'Restore', danger: true })
    if (confirmed) releaseAction.mutate({ release, operation })
  }

  const removeProject = async (force: boolean) => {
    const request = {
      title: force ? (zh ? '强制删除持续交付项目？' : 'Force delete delivery project?') : (zh ? '删除持续交付项目？' : 'Delete delivery project?'),
      description: force
        ? (zh ? `将立即终止并移除“${projectName}”的 Compose 容器、网络和孤立容器，然后删除项目记录、Release、Git 工作区和项目文件。默认同时永久删除命名存储卷及其中的数据。` : `Immediately stop and remove the Compose containers, networks, and orphans for “${projectName}”, then delete its project record, releases, Git worktree, and files. Named volumes and their data are permanently deleted by default.`)
        : (zh ? `将删除“${projectName}”的项目记录、Git 工作区和项目文件，但不会停止或删除仍在运行的容器。` : `This removes “${projectName}”, its Git worktree, and project files. Running containers are not stopped or removed.`),
      confirmLabel: force ? (zh ? '强制删除' : 'Force delete') : (zh ? '删除项目' : 'Delete project'),
      danger: true,
      input: { label: zh ? `输入 ${projectName} 以确认` : `Type ${projectName} to confirm`, requiredValue: projectName },
      ...(force ? { checkbox: { label: zh ? '保留命名存储卷和其中的数据' : 'Preserve named volumes and their data', description: zh ? '勾选后仅删除容器和网络，不删除由此 Compose 项目声明的命名卷。' : 'When selected, containers and networks are removed, but named volumes declared by this Compose project are kept.' } } : {}),
    }
    const result = force ? await promptWithCheckboxDialog(request) : await promptDialog(request)
    const confirmed = typeof result === 'string' ? result : result?.value
    if (confirmed !== projectName) return
    const preserveVolumes = typeof result === 'object' && result?.checked === true
    try {
      await api(`/delivery-projects/${encodedName}?confirm=${encodedName}${force ? `&force=true&preserve_volumes=${preserveVolumes}` : ''}`, { method: 'DELETE' })
      client.removeQueries({ queryKey: ['delivery-project', projectName] })
      client.removeQueries({ queryKey: ['delivery-configuration', projectName] })
      client.removeQueries({ queryKey: ['delivery-drift', projectName] })
      client.removeQueries({ queryKey: ['delivery-releases', projectName] })
      await client.invalidateQueries({ queryKey: ['delivery-projects'] })
      void navigate({ to: '/continuous-delivery' })
    } catch (error) {
      setNotice(error instanceof Error ? error.message : String(error))
    }
  }

  if (projectQuery.isPending || cdQuery.isPending) return <LoadingState label={zh ? '正在加载持续交付项目' : 'Loading delivery project'} rows={6} />
  if (projectQuery.isError || cdQuery.isError || !projectQuery.data || !cdQuery.data) return <ErrorState title={zh ? '无法加载持续交付项目' : 'Unable to load delivery project'} description={loadErrorMessage(projectQuery.error || cdQuery.error, zh)} />
  const project = projectQuery.data
  const configuration = cdQuery.data
  const activeDelivery = hasActiveRelease(releasesQuery.data)
  const tabs: View[] = isGit ? ['Overview', 'Releases', 'Settings'] : ['Settings']
  const headerActions = <div className="flex flex-wrap items-center gap-2"><Badge variant="outline" className={cn(isGit ? 'border-transparent bg-primary/10 text-primary' : 'text-muted-foreground')}>{isGit ? modeLabel(configuration.reconcile_mode, zh) : (zh ? '待配置' : 'Setup')}</Badge><TooltipHint content={activeDelivery ? (zh ? '交付任务进行中，暂时无法删除' : 'Cannot delete while a delivery is active') : undefined}><Button disabled={activeDelivery} onClick={() => void removeProject(false)}><Trash2 />{zh ? '删除项目' : 'Delete project'}</Button></TooltipHint><TooltipHint content={activeDelivery ? (zh ? '交付任务进行中，暂时无法强制删除' : 'Cannot force delete while a delivery is active') : undefined}><Button disabled={activeDelivery} onClick={() => void removeProject(true)} className="bg-destructive text-white hover:bg-destructive/90"><Trash2 />{zh ? '强制删除' : 'Force delete'}</Button></TooltipHint>{isGit && <Button disabled={activeDelivery || sync.isPending} onClick={() => sync.mutate()}>{sync.isPending ? <Spinner /> : null}{activeDelivery ? (zh ? '交付中' : 'Delivering') : sync.isPending ? (zh ? '启动中' : 'Starting') : (zh ? '同步 Git' : 'Sync Git')}{!sync.isPending && !activeDelivery && <RefreshCw />}</Button>}</div>
  return <div className="flex w-full flex-col gap-4">
    <Button variant="ghost" size="sm" className="-ml-2 w-fit gap-1 text-muted-foreground" onClick={() => void navigate({ to: '/continuous-delivery' })}><ChevronLeft className="size-4" />{zh ? '持续交付' : 'Continuous Delivery'}</Button>
    <ResourceFrame title={project.name} detail={isGit ? `${configuration.repository.ref_type}:${configuration.repository.ref} · ${shortCommit(configuration.desired_commit)}` : (zh ? '尚未配置 Git 持续交付' : 'Git continuous delivery is not configured')} action={headerActions}>
      <div className="flex flex-col gap-4">
        {notice && <p className={cn('text-sm', sync.isError || releaseAction.isError ? 'text-destructive' : 'text-muted-foreground')}>{notice}</p>}
        <Tabs value={view} onValueChange={(value) => setView(value as View)}>
          <TabsList variant="line">
            {tabs.map((name) => (
              <TabsTrigger key={name} value={name}>{name === 'Overview' ? (zh ? '概览' : 'Overview') : name === 'Releases' ? (zh ? 'Release' : 'Releases') : (zh ? '设置' : 'Settings')}</TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
        {view === 'Overview' && isGit && <CDOverview configuration={configuration} drift={driftQuery.data} releases={releasesQuery.data} zh={zh} />}
        {view === 'Releases' && isGit && (releasesQuery.isPending ? <LoadingState label={zh ? '正在加载 Release' : 'Loading releases'} /> : <ReleasePanel configuration={configuration} releases={releasesQuery.data} pendingReleaseID={releaseAction.isPending ? releaseAction.variables?.release.id : undefined} zh={zh} onAction={(release, operation) => void runReleaseAction(release, operation)} />)}
        {view === 'Settings' && <CDSettings projectName={projectName} configuration={configuration} zh={zh} onSaved={(value) => { client.setQueryData(['delivery-configuration', projectName], value); void client.invalidateQueries({ queryKey: ['delivery-projects'] }); setNotice(zh ? '持续交付配置已保存。' : 'Continuous delivery configuration saved.'); if (value.configured) setView('Overview') }} />}
      </div>
    </ResourceFrame>
  </div>
}

function modeLabel(value: string, zh: boolean) {
  if (value === 'auto') return zh ? '自动交付' : 'Automatic CD'
  if (value === 'observe') return zh ? '仅观察' : 'Observe only'
  return zh ? '手动交付' : 'Manual CD'
}

function loadErrorMessage(error: Error | null, zh: boolean) {
  if (!error) return zh ? '服务端没有返回项目数据。' : 'The server did not return project data.'
  if (zh && error instanceof ApiError && error.code === -1) return `后端没有为当前 API 返回 JSON。请重新构建并重启 SUMA 后端，同时检查 /api 反向代理。详情：${error.message}`
  return error.message
}
