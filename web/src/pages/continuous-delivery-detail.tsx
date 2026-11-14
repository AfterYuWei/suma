import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useParams, useSearch } from '@tanstack/react-router'
import { ChevronLeft, MoreHorizontal, RefreshCw, Rocket, Trash2 } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { Button } from '../components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '../components/ui/dropdown-menu'
import { InlineStatus } from '../components/ui/inline-status'
import { Tabs, TabsList, TabsTrigger } from '../components/ui/tabs'
import { ErrorState } from '../components/ui/error-state'
import { LoadingState } from '../components/ui/loading-state'
import { DeliveryRecords } from '../features/delivery/records'
import { CDSettings } from '../features/delivery/settings'
import { DeliveryAutomationSwitch } from '../features/delivery/automation-switch'
import { useReleaseFlow } from '../features/delivery/release-flow'
import type { CDConfiguration, CDDrift, DeliveryProject, DeliveryRecordPage, DeliveryRelease } from '../features/delivery/types'
import { hasActiveRelease, shortCommit } from '../features/delivery/types'
import { api } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { promptDialog, promptWithCheckboxDialog } from '../stores/dialog'
import { ResourceFrame } from './images'

type View = 'Publish' | 'Records' | 'Settings'

export function ContinuousDeliveryDetailPage() {
  const { projectName } = useParams({ from: '/continuous-delivery/$projectName' })
  const encodedName = encodeURIComponent(projectName)
  const base = `/delivery-projects/${encodedName}`
  const client = useQueryClient()
  const navigate = useNavigate()
  const { language } = useI18n()
  const zh = language === 'zh-CN'
  const projectQuery = useQuery({ queryKey: ['delivery-project', projectName], queryFn: () => api<DeliveryProject>(base) })
  const cdQuery = useQuery({ queryKey: ['delivery-configuration', projectName], queryFn: () => api<CDConfiguration>(`${base}/configuration`), refetchInterval: 5000 })
  const isGit = cdQuery.data?.configured === true
  const driftQuery = useQuery({ queryKey: ['delivery-drift', projectName], queryFn: () => api<CDDrift>(`${base}/drift`), enabled: isGit, refetchInterval: 5000 })
  const releasesQuery = useQuery({ queryKey: ['delivery-releases', projectName], queryFn: () => api<DeliveryRelease[]>(`${base}/releases`), enabled: isGit, refetchInterval: 5000 })
  const records = useQuery({ queryKey: ['delivery-records-summary', projectName], queryFn: () => api<DeliveryRecordPage>(`${base}/records?page=1&page_size=10`), enabled: isGit, refetchInterval: 3000 })
  const initialView = useSearch({from: '/continuous-delivery/$projectName'})
  const [view, setView] = useState<View>(initialView.view === 'records' ? 'Records' : 'Publish')
  const [notice, setNotice] = useState<{message: string; tone: 'success' | 'error'} | null>(null)
  const flow = useReleaseFlow({base, zh})
  const firstPrepare = useRef('')
  const activeDelivery = hasActiveRelease(releasesQuery.data)
  const actionPending = flow.pending || activeDelivery
  useEffect(() => { if (cdQuery.data && !cdQuery.data.configured) setView('Settings') }, [cdQuery.data])
  useEffect(() => {
    if (isGit && flow.initialRequested && firstPrepare.current !== projectName) { firstPrepare.current = projectName; setView('Publish'); flow.start() }
  }, [isGit, flow, projectName])
  const sync = useMutation({
    mutationFn: () => api(`${base}/sync`, {method: 'POST'}),
    onSuccess: () => { setNotice({message: zh ? '更新检查已启动，不会改变运行容器。' : 'Update check started. Running containers are unchanged.', tone: 'success'}); void client.invalidateQueries({queryKey: ['delivery-releases', projectName]}) },
    onError: error => setNotice({message: error.message, tone: 'error'}),
  })
  const openRelease = () => {
    if (flow.pending) { flow.reopen(); return }
    const active = releasesQuery.data?.find(release => ['validating', 'pulling', 'deploying', 'verifying', 'rolling_back'].includes(release.status))
    if (active?.task_id) { flow.resume(active.task_id); return }
    flow.start()
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

  if (projectQuery.isPending || cdQuery.isPending) return <LoadingState label={zh ? '正在加载发布项目' : 'Loading release project'} rows={6} />
  if (projectQuery.isError || cdQuery.isError || !projectQuery.data || !cdQuery.data) return <ErrorState description={(projectQuery.error || cdQuery.error)?.message || (zh ? '无法加载发布项目' : 'Unable to load release project')} />
  const project = projectQuery.data
  const configuration = cdQuery.data
  const latest = records.data?.items.find(record => record.operation !== 'auto_rollback')
  const failed = latest?.attempts.filter(attempt => ['failed', 'interrupted'].includes(attempt.status)) ?? []
  const canPublish = configuration.reconcile_mode !== 'observe'
  return <div className="flex w-full min-w-0 flex-col gap-4">
    <Button variant="ghost" size="sm" className="-ml-2 w-fit text-muted-foreground" onClick={() => void navigate({to: '/continuous-delivery'})}><ChevronLeft />{zh ? '持续交付' : 'Continuous Delivery'}</Button>
    <ResourceFrame title={project.name} detail={isGit ? `${configuration.repository.ref} · ${configuration.repository.compose_files.join(' → ')}` : (zh ? '连接 Git 来源后准备首次发布' : 'Connect Git to prepare the first release')} action={<div className="flex flex-wrap items-center gap-2">
      {isGit && <Button onClick={openRelease} disabled={!canPublish || sync.isPending}><Rocket />{actionPending ? (zh ? '查看发布进度' : 'View release progress') : (zh ? '发布…' : 'Publish…')}</Button>}
      <DropdownMenu><DropdownMenuTrigger render={<Button variant="outline" aria-label={zh ? '项目更多操作' : 'More project actions'}><MoreHorizontal /></Button>} /><DropdownMenuContent align="end" className="w-72 max-w-[calc(100vw-2rem)]">
        <DropdownMenuItem disabled={actionPending} variant="destructive" onClick={() => void removeProject(false)}><Trash2 />{zh ? '删除项目，保留运行容器' : 'Delete project; retain containers'}</DropdownMenuItem>
        <DropdownMenuItem disabled={actionPending} variant="destructive" onClick={() => void removeProject(true)}><Trash2 />{zh ? '强制删除…' : 'Force delete…'}</DropdownMenuItem>
      </DropdownMenuContent></DropdownMenu>
    </div>}>
      <Tabs value={view} onValueChange={value => {if (value === 'Publish' || value === 'Records' || value === 'Settings') setView(value)}}><TabsList>{(isGit ? ['Publish', 'Records', 'Settings'] : ['Settings']).map(name => <TabsTrigger key={name} value={name}>{name === 'Publish' ? (zh ? '发布' : 'Publish') : name === 'Records' ? (zh ? '发布记录' : 'Publications') : (zh ? '设置' : 'Settings')}</TabsTrigger>)}</TabsList></Tabs>
      {notice && <InlineStatus tone={notice.tone}>{notice.message}</InlineStatus>}
      {!canPublish && <p className="text-sm text-muted-foreground">{zh ? '当前仅检查更新。切换为手动确认或自动发布后，才能发布和恢复。' : 'This project only checks for updates. Select manual or automatic delivery to publish or restore.'}</p>}
      {view === 'Publish' && isGit && <div className="space-y-5">
        <section className="section-surface space-y-3"><div className="flex flex-wrap items-start justify-between gap-3"><div><h3 className="text-sm font-medium">{zh ? '当前运行' : 'Current runtime'}</h3><p className="mt-1 text-xs text-muted-foreground">{zh ? '逐节点读取 Docker；成功发布记录与实时运行状态分别展示。' : 'Read from each Docker node. Successful publications and runtime observations are shown separately.'}</p></div><Button size="sm" variant="ghost" disabled={actionPending || sync.isPending} onClick={() => sync.mutate()}><RefreshCw />{zh ? '检查更新' : 'Check for updates'}</Button></div>
          {driftQuery.isError ? <InlineStatus tone="error">{driftQuery.error.message}</InlineStatus> : !driftQuery.data ? <LoadingState compact label={zh ? '读取节点状态' : 'Reading node state'} /> : driftQuery.data.nodes.map(node => <div key={node.node_id} className="grid min-w-0 gap-1 rounded-md bg-muted/30 p-3 text-xs sm:grid-cols-[minmax(8rem,1fr)_minmax(9rem,1fr)_minmax(12rem,2fr)]"><span className="break-words font-medium">{node.node_name}</span><span className="break-all text-muted-foreground">{zh ? '最近成功版本' : 'Last successful version'} {shortCommit(node.active_commit)}</span><span className={node.status === 'unknown' || node.reason_code === 'runtime_unhealthy' ? 'text-destructive' : 'text-muted-foreground'}>{node.runtime_available === false || node.status === 'unknown' ? (zh ? '运行未知' : 'Runtime unknown') : node.runtime_healthy === true ? (node.health_checks ? (zh ? `容器运行 · ${node.health_checks} 项健康检查` : `Containers running · ${node.health_checks} health checks`) : (zh ? '容器运行 · 未配置健康检查' : 'Containers running · no health checks')) : node.reason || (zh ? '运行状态待确认' : 'Runtime unconfirmed')}</span></div>)}
        </section>
        {latest && <section className="section-surface space-y-3"><h3 className="text-sm font-medium">{zh ? `最近发布 #${latest.id}` : `Latest publication #${latest.id}`}</h3><p className="text-xs text-muted-foreground">{shortCommit(latest.commit_sha)} · {zh ? `成功 ${latest.attempts.filter(attempt => attempt.status === 'succeeded').length}，失败 ${failed.length}` : `${latest.attempts.filter(attempt => attempt.status === 'succeeded').length} succeeded, ${failed.length} failed`}</p>{latest.failure_reason && <InlineStatus tone="error">{latest.failure_reason}</InlineStatus>}{failed.map(attempt => <p key={attempt.id} className="break-words text-xs text-destructive">{attempt.node_name || attempt.node_id} · {attempt.failure_reason || (zh ? '查看任务日志确认失败阶段' : 'Inspect task logs for the failing stage')}</p>)}<div className="flex flex-wrap gap-2">{failed.length > 0 && <Button size="sm" disabled={!canPublish || actionPending} onClick={() => flow.start({operation: 'retry', source_record_id: latest.id})}>{zh ? `重试失败节点（${failed.length}）…` : `Retry failed nodes (${failed.length})…`}</Button>}{failed.length > 0 && <Button variant="ghost" size="sm" disabled={!canPublish || actionPending} onClick={() => flow.start({operation: 'restore-failed', source_record_id: latest.id})}>{zh ? '恢复失败节点…' : 'Restore failed nodes…'}</Button>}{latest.task_id && <Button variant="ghost" size="sm" onClick={() => { if (latest.task_id) flow.resume(latest.task_id) }}>{zh ? '查看本次结果' : 'Inspect this publication'}</Button>}</div></section>}
        <section className="section-surface flex flex-wrap items-center justify-between gap-3"><div><h3 className="text-sm font-medium">{zh ? '自动化' : 'Automation'}</h3><p className="mt-1 text-xs text-muted-foreground">{configuration.automation_enabled === false ? (zh ? '全部自动触发已暂停；手动检查和发布仍可使用。' : 'Automatic triggers are paused. Manual checks and publishing remain available.') : `${zh ? '按周期及 Webhook 检查，' : 'Scheduled and webhook checks; '}${configuration.initial_application_required ? (zh ? '首次发布待人工确认' : 'first release awaits confirmation') : configuration.reconcile_mode === 'auto' ? (zh ? '验证后自动发布' : 'publish after validation') : configuration.reconcile_mode === 'observe' ? (zh ? '仅检查，禁止发布' : 'checks only; publishing disabled') : (zh ? '有更新等待确认' : 'updates await confirmation')}`}</p></div><DeliveryAutomationSwitch projectName={projectName} enabled={configuration.automation_enabled !== false} version={configuration.automation_version} zh={zh} /></section>
        <details><summary className="cursor-pointer text-xs text-muted-foreground">{zh ? '选择其他 Git 版本发布' : 'Publish another Git version'}</summary><div className="mt-3 space-y-2">{releasesQuery.data?.map(release => <div key={release.id} className="flex flex-wrap items-center justify-between gap-2 rounded-md bg-muted/30 p-3"><span className="min-w-0 break-words text-xs">{shortCommit(release.commit_sha)} · {release.commit_message}</span><Button size="sm" variant="ghost" disabled={!canPublish || actionPending || release.status === 'rejected'} onClick={() => flow.start({operation: 'deploy', release_id: release.id})}>{zh ? '准备发布…' : 'Prepare release…'}</Button></div>)}</div></details>
      </div>}
      {view === 'Records' && isGit && <DeliveryRecords projectName={projectName} releases={releasesQuery.data ?? []} busy={actionPending} canDeliver={canPublish} zh={zh} onRollback={record => flow.start({operation: record.images_pinned ? 'restore' : 'reapply', source_record_id: record.id})} onRetry={record => flow.start({operation: 'retry', source_record_id: record.id})} onRestoreFailed={record => flow.start({operation: 'restore-failed', source_record_id: record.id})} />}
      {view === 'Settings' && <CDSettings projectName={projectName} configuration={configuration} zh={zh} onSaved={value => { client.setQueryData(['delivery-configuration', projectName], value); void client.invalidateQueries({queryKey: ['delivery-projects']}); if (value.configured && !value.webhook_secret) { setView('Publish'); setNotice({message: zh ? '配置已保存，尚未发布。' : 'Settings saved. Nothing has been deployed.', tone: 'success'}) } }} />}
    </ResourceFrame>
    {flow.panel}
  </div>
}
