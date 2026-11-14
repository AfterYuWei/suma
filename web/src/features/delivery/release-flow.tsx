import { useEffect, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { create } from 'zustand'
import { RefreshCw, Rocket, RotateCcw } from 'lucide-react'
import { Button } from '../../components/ui/button'
import { Checkbox } from '../../components/ui/checkbox'
import { InlineStatus } from '../../components/ui/inline-status'
import { LoadingState } from '../../components/ui/loading-state'
import { Sheet, SheetContent, SheetTitle } from '../../components/ui/sheet'
import { api } from '../../lib/api'
import { confirmDialog } from '../../stores/dialog'
import { compactTaskLogs } from '../tasks/compact-task-logs'

export interface ReleaseRequest { operation?: 'deploy' | 'retry' | 'restore' | 'restore-failed' | 'reapply'; source_record_id?: number; release_id?: number }
interface ReleaseImage { service: string; declared: string; previous: string; reference: string; platform: string; changed: boolean }
interface ReleaseNode { node_id: string; node_name: string; previous_record_id?: number; previous_commit: string; target_commit?: string; changed: boolean; runtime_healthy: boolean; health_checked: boolean; can_restore: boolean; compose_before: string; compose_after: string; images: ReleaseImage[]; services: {name: string; action: string}[]; environment: {name: string; action: string}[] }
export interface ReleasePlan { id: string; expires_at: string; operation: string; source_record_id?: number; release_id?: number; commit_sha: string; commit_message: string; compose_files: string[]; nodes: ReleaseNode[]; images_pinned: boolean; auto_rollback: boolean; deployment_timeout: number; warnings?: string[] }
interface ReleaseTask { id: string; type?: string; status: string; message: string; node_name?: string; result?: {record_id?: number; release_id?: number}; result_error?: string }
interface ReleaseUI { open: boolean; autoPrepare: boolean; phase: 'preparing' | 'review' | 'running' | 'result'; taskID: string; planID: string; selected: string[]; force: boolean; request: ReleaseRequest }
const initialUI: ReleaseUI = {open: false, autoPrepare: false, phase: 'preparing', taskID: '', planID: '', selected: [], force: false, request: {operation: 'deploy'}}
// Only navigation/progress pointers live here. Plans, tasks and logs remain in Query.
const useReleaseUI = create<{jobs: Record<string, ReleaseUI>; update: (base: string, patch: Partial<ReleaseUI>) => void}>(set => ({jobs: {}, update: (base, patch) => set(state => ({jobs: {...state.jobs, [base]: {...(state.jobs[base] ?? initialUI), ...patch}}}))}))
const terminal = (status?: string) => status === 'success' || status === 'failed' || status === 'canceled'

export function requestInitialRelease(base: string) { useReleaseUI.getState().update(base, {...initialUI, autoPrepare: true}) }

export function useReleaseFlow({base, zh, singleNode = false}: {base: string; zh: boolean; singleNode?: boolean}) {
  const client = useQueryClient()
  const ui = useReleaseUI(state => state.jobs[base]) ?? initialUI
  const update = useReleaseUI(state => state.update)
  const [showLogs, setShowLogs] = useState(false)
  const [clock, setClock] = useState(Date.now)
  const refreshed = useRef('')
  const prepare = useMutation({
    mutationFn: (request: ReleaseRequest) => api<ReleaseTask>(`${base}/release-plans`, {method: 'POST', body: JSON.stringify(request)}),
    onMutate: request => { update(base, {...initialUI, open: true, request}); setShowLogs(false) },
    onSuccess: task => update(base, {taskID: task.id}),
  })
  const task = useQuery({queryKey: ['release-task', ui.taskID], queryFn: () => api<ReleaseTask>(`/tasks/${encodeURIComponent(ui.taskID)}`), enabled: !!ui.taskID, refetchInterval: query => terminal(query.state.data?.status) ? false : 1000})
  const plan = useQuery({queryKey: ['release-plan', base, ui.taskID, ui.planID], queryFn: () => api<ReleasePlan>(`${base}/release-plans${ui.planID ? `/${ui.planID}` : `?task_id=${encodeURIComponent(ui.taskID)}`}`), enabled: !!ui.taskID && (ui.phase === 'review' || ui.phase === 'preparing' && task.data?.status === 'success'), retry: false})
  useEffect(() => {
    if (ui.phase === 'preparing' && plan.data && plan.data.id !== ui.planID) update(base, {phase: 'review', planID: plan.data.id, selected: plan.data.nodes.map(node => node.node_id)})
  }, [base, plan.data, ui.phase, ui.planID, update])
  useEffect(() => {
    if (!ui.taskID || !terminal(task.data?.status)) return
    if (ui.phase === 'running') update(base, {phase: 'result'})
    if (refreshed.current === ui.taskID) return
    refreshed.current = ui.taskID
    void client.invalidateQueries({predicate: query => typeof query.queryKey[0] === 'string' && /^(delivery-|project-gitops|project-cd|projects$|project$|tasks$|task-schedules)/.test(query.queryKey[0])})
  }, [base, client, task.data?.status, ui.phase, ui.taskID, update])
  useEffect(() => {
    if (!plan.data) return
    const delay = Math.max(0, new Date(plan.data.expires_at).getTime() - Date.now())
    const timer = window.setTimeout(() => setClock(Date.now()), delay)
    return () => window.clearTimeout(timer)
  }, [plan.data])
  const confirm = useMutation({
    mutationFn: async () => {
      const response = await api<ReleaseTask | {task: ReleaseTask}>(`${base}/release-plans/${ui.planID}/confirm`, {method: 'POST', body: JSON.stringify({node_ids: ui.selected, force_recreate: ui.force})})
      return 'task' in response ? response.task : response
    },
    onSuccess: task => { update(base, {phase: 'running', taskID: task.id}); setShowLogs(false) },
  })
  const cancel = useMutation({mutationFn: () => api(`/tasks/${encodeURIComponent(ui.taskID)}/cancel`, {method: 'POST'}), onSuccess: () => client.invalidateQueries({queryKey: ['release-task', ui.taskID]})})
  const children = useQuery({queryKey: ['release-task-children', ui.taskID], queryFn: () => api<ReleaseTask[]>(`/tasks/${encodeURIComponent(ui.taskID)}/children`), enabled: ui.open && !!ui.taskID && ['running', 'result'].includes(ui.phase), refetchInterval: terminal(task.data?.status) ? false : 1500})
  const logs = useQuery({queryKey: ['release-task-logs', ui.taskID], queryFn: () => api<{id: number; message: string}[]>(`/tasks/${encodeURIComponent(ui.taskID)}/logs`), enabled: ui.open && showLogs && !!ui.taskID, refetchInterval: terminal(task.data?.status) ? false : 1500})
  const start = (request: ReleaseRequest = {operation: 'deploy'}) => { confirm.reset(); prepare.mutate(request) }
  const pending = prepare.isPending || !!ui.taskID && !terminal(task.data?.status) && ui.phase !== 'review'
  const selectedNodes = plan.data?.nodes.filter(node => ui.selected.includes(node.node_id)) ?? []
  const noChanges = selectedNodes.length > 0 && selectedNodes.every(node => !node.changed) && plan.data?.operation === 'deploy'
  const expired = plan.data ? new Date(plan.data.expires_at).getTime() <= Math.max(clock, Date.now()) : false
  const title = ui.phase === 'preparing' ? (zh ? '准备发布' : 'Preparing release') : ui.phase === 'review' ? (zh ? '审阅本次发布' : 'Review this release') : ui.phase === 'running' ? (zh ? '发布进行中' : 'Release in progress') : (zh ? '本次发布结果' : 'Release result')
  const operation = plan.data?.operation ?? ui.request.operation
  const recovering = operation === 'restore' || operation === 'restore-failed' || operation === 'reapply'
  const errors = [prepare.error, task.error, plan.error, confirm.error, cancel.error].filter(Boolean)
  const panel = <Sheet open={ui.open} onOpenChange={open => update(base, {open})}><SheetContent className="gap-0 overflow-y-auto data-[side=right]:w-full data-[side=right]:sm:w-[760px] data-[side=right]:sm:max-w-[calc(100vw-2rem)]">
    <div className="flex min-w-0 flex-col gap-5 p-4 sm:p-6">
      <div className="pr-8"><SheetTitle className="text-base font-medium">{title}</SheetTitle><p className="mt-1 text-xs text-muted-foreground">{zh ? '确认前不改变容器；本次固定版本、配置、镜像和目标。' : 'Preparation leaves containers unchanged. Version, configuration, images and targets are frozen.'}</p></div>
      {errors.map((error, index) => <InlineStatus key={index} tone="error">{error?.message}</InlineStatus>)}
      {ui.phase === 'preparing' && (prepare.isPending || !terminal(task.data?.status)) && <LoadingState compact label={task.data?.message || (zh ? '检查 Git、配置、镜像与节点授权' : 'Checking Git, configuration, images and node access')} />}
      {ui.phase === 'preparing' && task.data?.status === 'failed' && <InlineStatus tone="error">{task.data.message}</InlineStatus>}
      {ui.phase === 'preparing' && ui.taskID && !terminal(task.data?.status) && <Button variant="ghost" className="self-start" disabled={cancel.isPending} onClick={() => cancel.mutate()}>{zh ? '停止准备' : 'Stop preparation'}</Button>}
      {task.isError && <Button variant="outline" className="self-start" onClick={() => void task.refetch()}>{zh ? '重新读取任务' : 'Reload task'}</Button>}
      {ui.phase === 'review' && plan.isPending && <LoadingState compact label={zh ? '读取固定发布方案' : 'Loading the frozen release plan'} />}
      {ui.phase === 'review' && plan.data && <>
        <section className="section-surface space-y-3"><p className="text-xs text-muted-foreground">{zh ? '当前 → 本次' : 'Current → this release'}</p><h3 className="break-words text-sm font-medium">{plan.data.commit_message || (zh ? '固定 Git 版本' : 'Fixed Git version')}</h3>{plan.data.commit_sha && <p className="break-all font-mono text-xs">{plan.data.commit_sha}</p>}<p className="break-words text-xs text-muted-foreground">{plan.data.compose_files.join(' → ')}</p></section>
        {(plan.data.warnings ?? []).map(warning => <InlineStatus key={warning} tone="pending">{warning}</InlineStatus>)}
        {operation === 'retry' && <p className="text-sm text-muted-foreground">{zh ? `重试发布记录 #${plan.data.source_record_id} 的失败节点，沿用原配置和固定镜像；不会应用新设置。` : `Retry failed nodes from publication #${plan.data.source_record_id} using its original configuration and images. New settings are excluded.`}</p>}
        {recovering && <InlineStatus tone={operation === 'reapply' ? 'error' : 'pending'}>{operation === 'reapply' ? (zh ? '历史镜像未固定：本次重新应用旧配置，使用刚刚解析的镜像内容，不能保证恢复原版本。' : 'Historical images were not pinned. This reapplies old configuration with images resolved now, and cannot restore the original image version.') : (zh ? '使用历史成功发布的配置和固定镜像。' : 'Use configuration and pinned images from the successful publication.')} {zh ? '确认后切换为手动确认；保留数据卷，数据库内容不会回退。' : 'Confirmation switches to manual delivery. Volumes remain; database contents are not restored.'}</InlineStatus>}
        <section className="space-y-3"><h3 className="text-sm font-medium">{singleNode ? (zh ? '目标：当前节点' : 'Target: current node') : (zh ? '本次目标节点' : 'Release targets')}</h3>{plan.data.nodes.map(node => <div key={node.node_id} className="rounded-md bg-muted/30 p-3">
          <div className="flex flex-wrap items-start justify-between gap-3"><label className="flex min-w-0 items-start gap-2 text-sm">{!singleNode && <Checkbox checked={ui.selected.includes(node.node_id)} onCheckedChange={checked => update(base, {selected: checked ? [...ui.selected, node.node_id] : ui.selected.filter(id => id !== node.node_id)})} />}<span className="break-all">{node.node_name || node.node_id}<span className="mt-1 block text-xs text-muted-foreground">{node.previous_record_id ? `#${node.previous_record_id} · ${node.previous_commit.slice(0, 8)} → ${node.target_commit?.slice(0, 8) || plan.data?.commit_sha.slice(0, 8)}` : (zh ? '尚无成功发布基线' : 'No successful publication baseline')}</span></span></label><span className="text-xs text-muted-foreground">{node.changed ? (zh ? '有变更' : 'Changes') : (zh ? '无变更' : 'Unchanged')}</span></div>
          <div className="mt-3 space-y-2">{node.images.map(image => <div key={image.service} className="min-w-0 text-xs"><span className="font-medium">{image.service}</span><span className="ml-2 break-all text-muted-foreground">{image.declared}</span><p className="mt-1 break-all font-mono">{image.previous ? image.previous.split('@').at(-1)?.slice(0, 19) : (zh ? '旧镜像未记录' : 'Previous image not recorded')} → {image.reference.split('@').at(-1)?.slice(0, 19)}</p><span className="text-muted-foreground">{image.platform} · {image.changed ? (zh ? '镜像变更' : 'Image changed') : (zh ? '镜像不变' : 'Image unchanged')}</span></div>)}</div>
          {!!node.images.length && <details className="mt-3 text-xs"><summary className="cursor-pointer text-muted-foreground">{zh ? '完整镜像标识' : 'Full image references'}</summary><div className="mt-2 space-y-3">{node.images.map(image => <div key={image.service} className="min-w-0"><p className="font-medium">{image.service} · {image.platform}</p><p className="mt-1 break-all font-mono">{image.previous || (zh ? '旧镜像未记录' : 'Previous image not recorded')} → {image.reference}</p></div>)}</div></details>}
          <details className="mt-3 text-xs"><summary className="cursor-pointer text-muted-foreground">{zh ? '比较配置（敏感值隐藏）' : 'Compare configuration (sensitive values hidden)'}</summary><div className="mt-3 space-y-3">{node.compose_before && <div><p className="mb-1 text-muted-foreground">{zh ? '最近成功配置' : 'Last successful configuration'}</p><pre className="max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-md bg-muted/40 p-3">{node.compose_before}</pre></div>}<div><p className="mb-1 text-muted-foreground">{zh ? '本次配置' : 'This configuration'}</p><pre className="max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-md bg-muted/40 p-3">{node.compose_after}</pre></div></div></details>
          {!!node.environment.length && <p className="mt-3 break-words text-xs text-muted-foreground">{zh ? '变量变更（值隐藏）：' : 'Variable changes (values hidden): '}{node.environment.map(variable => `${variable.name} · ${actionLabel(variable.action, zh)}`).join('，')}</p>}
          <details className="mt-3 text-xs"><summary className="cursor-pointer text-muted-foreground">{zh ? '服务影响与恢复基线' : 'Service impact and recovery baseline'}</summary><div className="mt-2 space-y-1">{node.services.map(service => <p key={service.name}>{service.name} · {actionLabel(service.action, zh)}</p>)}<p className="text-muted-foreground">{node.can_restore ? (zh ? '已记录上次成功发布的固定镜像与配置。' : 'Pinned images and configuration from the previous successful publication are recorded.') : (zh ? '此节点尚无可靠恢复基线，失败后不能保证自动恢复。' : 'This node has no reliable recovery baseline.')}</p><p className="text-muted-foreground">{zh ? '服务更新可能中断访问；数据卷保留。' : 'Service replacement may interrupt availability. Data volumes remain.'}</p></div></details>
        </div>)}</section>
        <details className="text-xs"><summary className="cursor-pointer text-muted-foreground">{zh ? '执行策略' : 'Execution policy'}</summary><p className="mt-2 text-muted-foreground">{plan.data.deployment_timeout}s · {zh ? '失败自动恢复' : 'Automatic recovery'} {plan.data.auto_rollback ? (zh ? '开启' : 'on') : (zh ? '关闭' : 'off')} · {zh ? '方案有效至' : 'Valid until'} {new Date(plan.data.expires_at).toLocaleTimeString()}</p></details>
        {noChanges && <div className="space-y-3"><p className="text-sm text-muted-foreground">{zh ? '没有待发布变更。' : 'There are no pending changes.'}</p><label className="flex items-start gap-2 text-sm"><Checkbox checked={ui.force} onCheckedChange={force => update(base, {force: !!force})} /><span>{zh ? '即使无变化也重建所选节点' : 'Recreate selected nodes even without changes'}</span></label></div>}
        {expired && <InlineStatus tone="error">{zh ? '方案已过期，请重新准备并审阅。' : 'The plan has expired. Prepare and review it again.'}</InlineStatus>}
        <div className="flex flex-wrap items-center justify-between gap-3"><Button variant="ghost" onClick={() => start(ui.request)} disabled={pending || confirm.isPending}><RefreshCw />{zh ? '重新准备' : 'Prepare again'}</Button><Button disabled={confirm.isPending || !ui.selected.length || noChanges && !ui.force || expired} onClick={() => confirm.mutate()}><Rocket />{confirm.isPending ? (zh ? '启动中' : 'Starting') : recovering ? (operation === 'reapply' ? (zh ? '重新应用历史配置' : 'Reapply historical configuration') : (zh ? '确认恢复' : 'Restore version')) : operation === 'retry' ? (zh ? `重试 ${ui.selected.length} 个失败节点` : `Retry ${ui.selected.length} failed ${ui.selected.length === 1 ? 'node' : 'nodes'}`) : singleNode ? (zh ? '发布到当前节点' : 'Publish to current node') : (zh ? `发布到 ${ui.selected.length} 个节点` : `Publish to ${ui.selected.length} ${ui.selected.length === 1 ? 'node' : 'nodes'}`)}</Button></div>
      </>}
      {['running', 'result'].includes(ui.phase) && task.data && <>
        <InlineStatus tone={task.data.status === 'success' ? 'success' : terminal(task.data.status) ? 'error' : 'pending'}>{task.data.message || title}</InlineStatus>
        {task.data.result_error && <InlineStatus tone="error">{zh ? '执行证据未完整保存，请核对当前运行状态后决定后续操作。' : 'Execution evidence is incomplete. Check the actual runtime before continuing.'}</InlineStatus>}
        {(children.data ?? []).map(child => <div key={child.id} className="flex flex-wrap items-start justify-between gap-3 rounded-md bg-muted/30 p-3 text-xs"><span>{child.node_name || child.id}</span><span className={child.status === 'failed' ? 'text-destructive' : 'text-muted-foreground'}>{child.message}</span></div>)}
        <p className="text-xs text-muted-foreground">{zh ? '本次执行结果与当前运行状态分别记录。没有健康检查时，仅能确认容器已运行。' : 'Execution outcomes and current runtime are tracked separately. Without health checks, only container running state is confirmed.'}</p>
        <div className="flex flex-wrap gap-2">{task.data.status === 'failed' && !['project.cd.check', 'project.cd.prepare'].includes(task.data.type ?? '') && task.data.result?.record_id && <Button onClick={() => start({operation: 'retry', source_record_id: task.data?.result?.record_id})}><RotateCcw />{zh ? '重试失败节点…' : 'Retry failed nodes…'}</Button>}{terminal(task.data.status) && <Button variant="outline" onClick={() => start()}>{zh ? '使用新设置发布…' : 'Publish with new settings…'}</Button>}{!terminal(task.data.status) && <Button variant="ghost" disabled={cancel.isPending} onClick={async () => { if (await confirmDialog({title: zh ? '停止后续执行？' : 'Stop remaining execution?', description: zh ? '已完成的容器变更不会自动撤销，请在结束后检查每个节点的实际状态。' : 'Completed container changes are not undone. Inspect each node after the operation stops.', danger: true})) cancel.mutate() }}>{zh ? '停止后续执行' : 'Stop remaining execution'}</Button>}</div>
      </>}
      {!!ui.taskID && <details onToggle={event => setShowLogs(event.currentTarget.open)}><summary className="cursor-pointer text-xs text-muted-foreground">{zh ? '任务日志' : 'Task logs'}</summary>{logs.isError ? <InlineStatus tone="error">{logs.error.message}</InlineStatus> : <pre className="mt-3 max-h-72 overflow-auto whitespace-pre-wrap break-all rounded-md bg-muted/40 p-3 text-xs">{compactTaskLogs(logs.data ?? []).map(log => log.message).join('\n')}</pre>}</details>}
      {(prepare.isError || plan.isError || ui.phase === 'preparing' && task.data && terminal(task.data.status) && task.data.status !== 'success') && <Button className="self-start" onClick={() => start(ui.request)}>{zh ? '重新准备发布' : 'Prepare again'}</Button>}
      <Button variant="ghost" className="self-end" onClick={() => update(base, {open: false})}>{pending ? (zh ? '收起到后台' : 'Continue in background') : (zh ? '关闭' : 'Close')}</Button>
    </div>
  </SheetContent></Sheet>
  return {start, panel, pending, initialRequested: ui.autoPrepare, hasTask: !!ui.taskID, reopen: () => update(base, {open: true}), resume: (taskID: string) => { prepare.reset(); confirm.reset(); setShowLogs(false); update(base, {open: true, taskID, phase: 'running', planID: ''}) }}
}

function actionLabel(action: string, zh: boolean) {
  if (!zh) return action
  const labels: Record<string, string> = {create: '新增', recreate: '重建', remove: '移除', unchanged: '不变', add: '新增', modify: '修改'}
  return labels[action] ?? action
}
