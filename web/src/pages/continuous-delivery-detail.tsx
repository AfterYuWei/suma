import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useParams } from '@tanstack/react-router'
import { ChevronLeft, MoreHorizontal, RefreshCw, Trash2, X } from 'lucide-react'
import { useEffect, useState } from 'react'
import { Badge } from '../components/ui/badge'
import { Button } from '../components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '../components/ui/dropdown-menu'
import { Spinner } from '../components/ui/spinner'
import { InlineStatus } from '../components/ui/inline-status'
import { Tabs, TabsList, TabsTrigger } from '../components/ui/tabs'
import { ErrorState } from '../components/ui/error-state'
import { LoadingState } from '../components/ui/loading-state'
import { CDOverview, ReleasePanel, type ReleaseOperation } from '../features/delivery/releases'
import { DeliveryRecords } from '../features/delivery/records'
import { FailedNodesDialog, RollbackDialog } from '../features/delivery/action-dialogs'
import { CDSettings } from '../features/delivery/settings'
import { DeliveryAutomationSwitch } from '../features/delivery/automation-switch'
import type { CDConfiguration, CDDrift, DeliveryProject, DeliveryRecord, DeliveryRelease } from '../features/delivery/types'
import { hasActiveRelease, shortCommit } from '../features/delivery/types'
import { api, ApiError } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { cn } from '../lib/utils'
import { confirmDialog, promptDialog, promptWithCheckboxDialog } from '../stores/dialog'
import { ResourceFrame } from './images'

type View = 'Overview' | 'Releases' | 'Records' | 'Settings'

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
  const [rollback, setRollback] = useState<{ release: DeliveryRelease; record?: DeliveryRecord } | null>(null)
  const [failedAction, setFailedAction] = useState<{ release: DeliveryRelease; operation: 'retry-failed' | 'rollback-failed' } | null>(null)
  const [notice, setNotice] = useState<{ message: string; tone: 'success' | 'error' } | null>(null)

  useEffect(() => {
    if (cdQuery.data && !cdQuery.data.configured) setView('Settings')
  }, [cdQuery.data])

  const sync = useMutation({
    mutationFn: () => api(`/delivery-projects/${encodedName}/sync`, { method: 'POST' }),
    onSuccess: () => {
      setNotice({ message: zh ? '同步任务已启动。' : 'Synchronization task started.', tone: 'success' })
      void client.invalidateQueries({ queryKey: ['tasks'] })
      void client.invalidateQueries({ queryKey: ['delivery-drift', projectName] })
      void client.invalidateQueries({ queryKey: ['delivery-releases', projectName] })
    },
    onMutate: () => setNotice(null),
    onError: (error) => setNotice({ message: error.message, tone: 'error' }),
  })
  const releaseAction = useMutation({
    mutationFn: ({ release, operation, recordID, nodeIDs }: { release: DeliveryRelease; operation: ReleaseOperation; recordID?: number; nodeIDs?: string[] }) => api(`/delivery-projects/${encodedName}/releases/${release.id}/${operation.endsWith('-failed') ? `remediations/${operation}` : operation}`, { method: 'POST', ...(operation === 'rollback' ? { body: JSON.stringify({ record_id: recordID, ...(nodeIDs ? { node_ids: nodeIDs } : {}) }) } : operation.endsWith('-failed') ? { body: JSON.stringify({ source_record_id: release.remediation.source_record_id }) } : {}) }),
    onSuccess: (_, variables) => {
      const queued = ['deploy', 'rollback', 'retry-failed', 'rollback-failed'].includes(variables.operation)
      setNotice({ message: queued ? (zh ? '交付任务已启动。' : 'Delivery task started.') : (zh ? 'Release 已拒绝。' : 'Release rejected.'), tone: 'success' })
      void client.invalidateQueries({ queryKey: ['delivery-records', projectName] })
      void client.invalidateQueries({ queryKey: ['delivery-configuration', projectName] })
      if (queued) void client.invalidateQueries({ queryKey: ['tasks'] })
      void client.invalidateQueries({ queryKey: ['delivery-releases', projectName] })
      void client.invalidateQueries({ queryKey: ['delivery-drift', projectName] })
    },
    onMutate: () => setNotice(null),
    onError: (error) => setNotice({ message: error.message, tone: 'error' }),
  })

  const runReleaseAction = async (release: DeliveryRelease, operation: ReleaseOperation) => {
    let confirmed = false
    if (operation === 'reject') confirmed = await confirmDialog({ title: zh ? `拒绝 Release #${release.id}？` : `Reject release #${release.id}?`, description: zh ? '该候选 Release 将不能再发布。' : 'This candidate release can no longer be deployed.', confirmLabel: zh ? '拒绝' : 'Reject', danger: true })
    if (operation === 'deploy') confirmed = await confirmDialog({ title: zh ? `${release.status === 'failed' ? '重新发布全部节点' : '发布'} Release #${release.id}？` : `${release.status === 'failed' ? 'Redeploy all nodes for' : 'Deploy'} release #${release.id}?`, description: zh ? `批准并立即发布 Commit ${shortCommit(release.commit_sha)}，使用设置页当前已保存的配置和目标节点，包含此前已成功的节点。` : `Approve and deploy commit ${shortCommit(release.commit_sha)} using the current saved settings and target nodes, including previously successful nodes.`, confirmLabel: release.status === 'failed' ? (zh ? '重新发布全部节点' : 'Redeploy all nodes') : (zh ? '发布' : 'Deploy') })
    if (operation === 'rollback') { setRollback({ release }); return }
    if (operation === 'retry-failed' || operation === 'rollback-failed') { setFailedAction({ release, operation }); return }
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
      setNotice({ message: error instanceof Error ? error.message : String(error), tone: 'error' })
    }
  }

  if (projectQuery.isPending || cdQuery.isPending) return <LoadingState label={zh ? '正在加载持续交付项目' : 'Loading delivery project'} rows={6} />
  if (projectQuery.isError || cdQuery.isError || !projectQuery.data || !cdQuery.data) return <ErrorState title={zh ? '无法加载持续交付项目' : 'Unable to load delivery project'} description={loadErrorMessage(projectQuery.error || cdQuery.error, zh)} />
  const project = projectQuery.data
  const configuration = cdQuery.data
  const activeDelivery = hasActiveRelease(releasesQuery.data)
  const tabs: View[] = isGit ? ['Overview', 'Releases', 'Records', 'Settings'] : ['Settings']
  const actionPending = activeDelivery || sync.isPending || releaseAction.isPending
  const headerActions = <div className="flex flex-wrap items-center gap-2">
    <Badge variant="outline" className={cn(isGit ? 'border-transparent bg-primary/10 text-primary' : 'text-muted-foreground')}>{isGit ? modeLabel(configuration.reconcile_mode, zh) : (zh ? '待配置' : 'Setup')}</Badge>
    {isGit && <Button className="bg-black text-white hover:bg-neutral-800 dark:border-white/15" disabled={actionPending} onClick={() => sync.mutate()}>{sync.isPending ? <Spinner /> : <RefreshCw />}{activeDelivery ? (zh ? '交付中' : 'Delivering') : sync.isPending ? (zh ? '启动中' : 'Starting') : (zh ? '同步 Git' : 'Sync Git')}</Button>}
    <DropdownMenu>
      <DropdownMenuTrigger render={<Button variant="outline" aria-label={zh ? '项目更多操作' : 'More project actions'}><MoreHorizontal />{zh ? '更多' : 'More'}</Button>} />
      <DropdownMenuContent align="end" className="w-72 max-w-[calc(100vw-2rem)]">
        {actionPending && <p className="px-2 py-1.5 text-xs text-muted-foreground">{zh ? '交付操作进行中，暂时无法删除项目' : 'Project deletion is unavailable during a delivery operation'}</p>}
        <DropdownMenuItem disabled={actionPending} variant="destructive" className="items-start py-2" onClick={() => void removeProject(false)}>
          <Trash2 className="mt-0.5" /><span className="min-w-0"><span className="block">{zh ? '删除项目' : 'Delete project'}</span><span className="mt-0.5 block text-xs text-muted-foreground">{zh ? '移除项目记录与文件，保留运行中的容器' : 'Remove project records and files; keep running containers'}</span></span>
        </DropdownMenuItem>
        <DropdownMenuItem disabled={actionPending} variant="destructive" className="items-start py-2" onClick={() => void removeProject(true)}>
          <Trash2 className="mt-0.5" /><span className="min-w-0"><span className="block">{zh ? '强制删除' : 'Force delete'}</span><span className="mt-0.5 block text-xs text-muted-foreground">{zh ? '同时清理容器与网络，默认删除命名卷和数据' : 'Also remove containers and networks; named volumes and data are deleted by default'}</span></span>
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  </div>
  return <div className="flex w-full flex-col gap-4">
    <Button variant="ghost" size="sm" className="-ml-2 w-fit gap-1 text-muted-foreground" onClick={() => void navigate({ to: '/continuous-delivery' })}><ChevronLeft className="size-4" />{zh ? '持续交付' : 'Continuous Delivery'}</Button>
    <ResourceFrame title={project.name} detail={isGit ? `${configuration.repository.ref_type}:${configuration.repository.ref} · ${shortCommit(configuration.desired_commit)}` : (zh ? '尚未配置 Git 持续交付' : 'Git continuous delivery is not configured')} action={headerActions}>
      <div className="flex flex-col gap-4">
        {isGit && view !== 'Settings' && <div className="flex flex-col gap-3 rounded-lg bg-muted/40 px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="min-w-0 flex-1"><p className="text-sm font-medium">{zh ? '自动交付触发' : 'Automatic delivery triggers'}</p><p className="mt-1 text-xs text-muted-foreground">{configuration.automation_enabled === false ? (zh ? '轮询与 Webhook 已暂停。手动同步仅生成候选版本，仍可手动发布和回滚。已开始的任务会继续完成。' : 'Polling and webhooks are paused. Manual sync creates candidates; manual deploy and rollback remain available. Started tasks finish normally.') : (zh ? '轮询和 Webhook 按已保存的交付策略执行。' : 'Polling and webhooks follow the saved delivery policy.')}</p></div>
          <DeliveryAutomationSwitch projectName={projectName} enabled={configuration.automation_enabled !== false} zh={zh} />
        </div>}
        <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-2">
          <Tabs value={view} onValueChange={(value) => setView(value as View)}>
            <TabsList>
              {tabs.map((name) => (
                <TabsTrigger key={name} value={name}>{name === 'Overview' ? (zh ? '概览' : 'Overview') : name === 'Releases' ? (zh ? 'Release' : 'Releases') : name === 'Records' ? (zh ? '记录' : 'Records') : (zh ? '设置' : 'Settings')}</TabsTrigger>
              ))}
            </TabsList>
          </Tabs>
          {notice && <div className="flex min-w-0 flex-1 items-start gap-1">
            <InlineStatus tone={notice.tone}>{notice.message}</InlineStatus>
            <Button variant="ghost" size="icon-xs" className="shrink-0" aria-label={zh ? '关闭操作提示' : 'Dismiss action message'} onClick={() => setNotice(null)}><X /></Button>
          </div>}
        </div>
        {view === 'Overview' && isGit && <CDOverview configuration={configuration} drift={driftQuery.data} releases={releasesQuery.data} error={(driftQuery.error || releasesQuery.error)?.message} zh={zh} />}
        {view === 'Releases' && isGit && (releasesQuery.isPending ? <LoadingState label={zh ? '正在加载 Release' : 'Loading releases'} /> : <ReleasePanel configuration={configuration} releases={releasesQuery.data} pendingReleaseID={releaseAction.isPending ? releaseAction.variables?.release.id : undefined} pendingOperation={releaseAction.isPending ? releaseAction.variables?.operation : undefined} zh={zh} onAction={(release, operation) => void runReleaseAction(release, operation)} />)}
        {view === 'Records' && isGit && <DeliveryRecords key={projectName} projectName={projectName} releases={releasesQuery.data || []} busy={actionPending} canDeliver={configuration.reconcile_mode !== 'observe'} zh={zh} onRollback={record => { void (async () => { try { const release = releasesQuery.data?.find(item => item.id === record.release_id) || await api<DeliveryRelease>(`/delivery-projects/${encodedName}/releases/${record.release_id}`); setRollback({ release, record }) } catch (error) { setNotice({ tone: 'error', message: error instanceof Error ? error.message : String(error) }) } })() }} />}
        {view === 'Settings' && <CDSettings projectName={projectName} configuration={configuration} zh={zh} onSaved={(value) => { client.setQueryData(['delivery-configuration', projectName], value); void client.invalidateQueries({ queryKey: ['delivery-projects'] }); setNotice(null); if (value.configured && !value.webhook_secret) { setView('Overview'); setNotice({ message: zh ? '持续交付配置已保存。' : 'Continuous delivery configuration saved.', tone: 'success' }) } }} />}
      </div>
    {rollback && <RollbackDialog key={`${rollback.release.id}-${rollback.record?.id || 0}`} projectName={projectName} release={rollback.release} initialRecord={rollback.record} zh={zh} onClose={() => setRollback(null)} onConfirm={(recordID, nodeIDs) => { releaseAction.mutate({ release: rollback.release, operation: 'rollback', recordID, nodeIDs }); setRollback(null) }} />}
    {failedAction && <FailedNodesDialog release={failedAction.release} operation={failedAction.operation} zh={zh} onClose={() => setFailedAction(null)} onConfirm={() => { releaseAction.mutate(failedAction); setFailedAction(null) }} />}
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
