import { useDateTime } from '../../lib/time-zone'
import { Alert, AlertDescription, AlertTitle } from '../../components/ui/alert'
import { Badge } from '../../components/ui/badge'
import { Button } from '../../components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '../../components/ui/card'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '../../components/ui/collapsible'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '../../components/ui/dropdown-menu'
import { ListPagination } from '../../components/ui/list-pagination'
import { useListPagination } from '../../components/ui/use-list-pagination'
import { Progress } from '../../components/ui/progress'
import { Spinner } from '../../components/ui/spinner'
import { InlineStatus } from '../../components/ui/inline-status'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../components/ui/table'
import { Tooltip, TooltipContent, TooltipTrigger } from '../../components/ui/tooltip'
import { Check, CheckCircle2, ChevronDownIcon, GitCommitHorizontal, History, MoreHorizontal, RotateCcw, Rocket, TriangleAlert, X, XCircle } from 'lucide-react'
import { useId, type ReactNode } from 'react'
import type { CDConfiguration, CDDrift, DeliveryRelease } from './types'
import { hasActiveRelease, parseStringList, repositoryName, shortCommit } from './types'
import { deliveryOverviewNotice, driftReason } from './overview-notice'

const statusBadgeClass = (status: string) => ['succeeded', 'approved'].includes(status)
  ? 'border-transparent bg-emerald-500/15 text-emerald-600 dark:text-emerald-400'
  : ['partial_failed', 'rolled_back', 'awaiting_approval', 'rolling_back'].includes(status)
    ? 'border-transparent bg-amber-500/15 text-amber-600 dark:text-amber-400'
    : ['failed', 'rollback_failed', 'rejected'].includes(status)
      ? 'border-transparent bg-red-500/15 text-red-600 dark:text-red-400'
      : 'border-transparent bg-primary/10 text-primary'

function DetailList({ items }: { items: [string, string][] }) {
  return <dl className="grid gap-x-8 gap-y-2.5 text-sm sm:grid-cols-2 xl:grid-cols-4">
    {items.map(([key, value]) => (
      <div key={key} className="min-w-0">
        <dt className="text-xs text-muted-foreground">{key}</dt>
        <dd className="break-all">{value}</dd>
      </div>
    ))}
  </dl>
}

function EmptyHint({ icon, title, description }: { icon: ReactNode; title: string; description?: string }) {
  return <div className="flex w-full flex-col items-center gap-1.5 rounded-lg bg-surface py-12 text-center">
    <span className="text-muted-foreground">{icon}</span>
    <p className="text-sm font-medium">{title}</p>
    {description && <p className="max-w-md text-xs text-muted-foreground">{description}</p>}
  </div>
}

export function CDOverview({ configuration, drift, releases, error, zh }: { configuration: CDConfiguration; drift?: CDDrift; releases?: DeliveryRelease[]; error?: string; zh: boolean }) {
  const { formatDateTime } = useDateTime()

  const noticeDescriptionID = useId()
  const latest = releases?.[0]
  const repository = configuration.repository
  const notice = deliveryOverviewNotice({ drift, latest, desiredCommit: configuration.desired_commit, error, zh })
  const nodePagination = useListPagination(drift?.nodes ?? [])
  return <div className="flex w-full flex-col gap-4">
    <Card>
      <CardHeader className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
        <CardTitle>{zh ? '交付状态' : 'Delivery state'}</CardTitle>
        {notice.tone !== 'warning' && <Tooltip>
          <TooltipTrigger render={<InlineStatus aria-describedby={noticeDescriptionID} tone={notice.tone === 'success' ? 'success' : 'pending'} tabIndex={0} className="rounded-sm outline-none focus-visible:ring-1 focus-visible:ring-ring" />}>{notice.title}</TooltipTrigger>
          <TooltipContent id={noticeDescriptionID} role="tooltip">{notice.description}</TooltipContent>
        </Tooltip>}
      </CardHeader>
      <CardContent>
        {notice.tone === 'warning' && <Alert className="mb-4 border-amber-500/30 bg-amber-500/10 text-amber-700 dark:border-amber-500/20 dark:text-amber-300">
          <TriangleAlert />
          <AlertTitle className="min-w-0 [overflow-wrap:anywhere]">{notice.title}</AlertTitle>
          <AlertDescription className="min-w-0 whitespace-pre-wrap [overflow-wrap:anywhere]">{notice.description}</AlertDescription>
        </Alert>}
        <DetailList items={[
          [zh ? '仓库' : 'Repository', `${repositoryName(repository.clone_url)} · ${repository.clone_url}`],
          [zh ? '跟踪引用' : 'Tracked ref', `${repository.ref_type}:${repository.ref}`],
          [zh ? 'Compose 文件' : 'Compose files', repository.compose_files.join(' · ')],
          [zh ? '交付模式' : 'Delivery mode', `${modeLabel(configuration.reconcile_mode, zh)} · ${configuration.automation_enabled === false ? (zh ? '自动触发已暂停' : 'Automatic triggers paused') : (zh ? `每 ${configuration.sync_interval_seconds} 秒轮询` : `poll every ${configuration.sync_interval_seconds}s`)}`],
          [zh ? 'Git 期望' : 'Git desired', shortCommit(configuration.desired_commit)],
          [zh ? '已检查' : 'Observed', shortCommit(configuration.observed_commit)],
          [zh ? '当前运行' : 'Active', `${shortCommit(drift?.active_commit)}${drift?.active_release_id ? ` · Release #${drift.active_release_id}` : ''}`],
          [zh ? '运行时健康' : 'Runtime health', !drift ? (zh ? '检查中' : 'Checking') : !drift.active_release_id ? (zh ? '无活动版本' : 'No active release') : drift.runtime_healthy ? (zh ? '健康' : 'Healthy') : (zh ? '异常' : 'Unhealthy')],
        ]} />
      </CardContent>
    </Card>
    {!!drift?.nodes?.length && <Card>
      <CardHeader><CardTitle>{zh ? '逐节点实时状态' : 'Live node state'}</CardTitle></CardHeader>
      <CardContent>
        <Table>
          <TableHeader><TableRow><TableHead>{zh ? '节点' : 'Node'}</TableHead><TableHead>Release / Commit</TableHead><TableHead>{zh ? '状态' : 'Status'}</TableHead><TableHead>{zh ? '原因' : 'Reason'}</TableHead><TableHead>{zh ? '检查时间' : 'Checked'}</TableHead></TableRow></TableHeader>
          <TableBody>{nodePagination.items.map((node) => <TableRow key={node.node_id}>
            <TableCell><div className="font-medium">{node.node_name}</div><div className="text-xs text-muted-foreground">{node.node_id}</div></TableCell>
            <TableCell className="font-mono text-xs">{node.active_release_id ? `#${node.active_release_id} · ${shortCommit(node.active_commit)}` : '—'}</TableCell>
            <TableCell><Badge className={node.status === 'healthy' ? statusBadgeClass('succeeded') : node.status === 'degraded' ? statusBadgeClass('failed') : statusBadgeClass('rolled_back')}>{node.status}</Badge></TableCell>
            <TableCell className="max-w-80 whitespace-normal text-xs text-muted-foreground">{driftReason(node.reason, zh) || '—'}{node.health_summary && <details className="mt-1"><summary className="cursor-pointer">{zh ? '健康摘要' : 'Health summary'}</summary><pre className="mt-1 max-h-32 overflow-auto overscroll-contain whitespace-pre-wrap rounded bg-muted p-2">{node.health_summary}</pre></details>}</TableCell>
            <TableCell className="text-xs text-muted-foreground">{formatDateTime(node.checked_at)}</TableCell>
          </TableRow>)}</TableBody>
        </Table>
        <ListPagination {...nodePagination} zh={zh} />
      </CardContent>
    </Card>}
    <Card>
      <CardHeader><CardTitle>{zh ? '最近一次交付' : 'Latest delivery'}</CardTitle></CardHeader>
      <CardContent>
        {latest ? <div className="flex flex-wrap items-center gap-3">
          <ReleaseStatus status={latest.status} zh={zh} />
          <div className="min-w-0">
            <p className="font-medium">{latest.commit_message || (zh ? '无提交说明' : 'No commit message')}</p>
            <p className="mt-0.5 text-xs text-muted-foreground">#{latest.id} · {shortCommit(latest.commit_sha)} · {formatDateTime(latest.created_at)}</p>
          </div>
        </div> : <EmptyHint icon={<GitCommitHorizontal className="size-5" />} title={zh ? '同步仓库后将在这里生成首个 Release' : 'Synchronize the repository to create the first release'} />}
      </CardContent>
    </Card>
  </div>
}

export type ReleaseOperation = 'approve' | 'reject' | 'deploy' | 'rollback' | 'retry-failed' | 'rollback-failed'

export function ReleasePanel({ configuration, releases, pendingReleaseID, pendingOperation, zh, onAction }: { configuration: CDConfiguration; releases?: DeliveryRelease[]; pendingReleaseID?: number; pendingOperation?: ReleaseOperation; zh: boolean; onAction: (release: DeliveryRelease, action: ReleaseOperation) => void }) {
  const pagination = useListPagination(releases ?? [])
  const busy = pendingReleaseID !== undefined || hasActiveRelease(releases)
  if (!releases?.length) return <EmptyHint icon={<History className="size-6" />} title={zh ? '还没有 Release' : 'No releases yet'} description={zh ? '点击同步以读取 Git 并验证 Compose 配置。' : 'Synchronize to read Git and validate the Compose configuration.'} />
  return <><div className="flex max-h-[calc(100dvh-16rem)] w-full flex-col gap-2 overflow-y-auto overscroll-contain">{pagination.items.map((release) => {
    const active = configuration.active_release_id === release.id
    return <Collapsible key={release.id} className="group/collapsible w-full shrink-0 overflow-hidden rounded-lg bg-surface">
      <div className="flex flex-wrap items-start gap-x-3 px-2">
        <CollapsibleTrigger className="flex min-w-0 flex-[1_1_20rem] cursor-pointer items-start gap-2 py-2.5 pl-1 text-left hover:bg-muted/50 [&_svg]:shrink-0">
          <ChevronDownIcon className="size-4 shrink-0 text-muted-foreground transition-transform group-data-open/collapsible:rotate-180" />
          <GitCommitHorizontal className="size-[18px] text-muted-foreground" />
          <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
            <span className="min-w-0 font-medium [overflow-wrap:anywhere]">{release.commit_message || (zh ? '无提交说明' : 'No commit message')}</span>
            <ReleaseStatus status={release.status} zh={zh} />
            {active && <Badge className="border-transparent bg-emerald-500/15 text-emerald-600 dark:text-emerald-400">{zh ? '当前运行' : 'Active'}</Badge>}
            <span className="text-xs text-muted-foreground">#{release.id} · {shortCommit(release.commit_sha)}</span>
            <ReleaseApproval release={release} zh={zh} />
          </span>
        </CollapsibleTrigger>
        <ReleaseActions configuration={configuration} release={release} busy={busy} pendingOperation={pendingReleaseID === release.id ? pendingOperation : undefined} zh={zh} onAction={onAction} />
      </div>
      <CollapsibleContent className="px-3 pb-3 pt-3">
        <ReleaseDetails release={release} zh={zh} />
      </CollapsibleContent>
    </Collapsible>
  })}</div><ListPagination {...pagination} zh={zh} /></>
}

function ReleaseActions({ configuration, release, busy, pendingOperation, zh, onAction }: { configuration: CDConfiguration; release: DeliveryRelease; busy: boolean; pendingOperation?: ReleaseOperation; zh: boolean; onAction: (release: DeliveryRelease, action: ReleaseOperation) => void }) {
  const canDeliver = configuration.reconcile_mode !== 'observe'
  const retryCount = release.remediation?.retry_failed_node_ids?.length ?? 0
  const rollbackCount = release.remediation?.rollback_failed_node_ids?.length ?? 0
  type Action = { operation: ReleaseOperation; label: string; description: string; icon: ReactNode; danger?: boolean }
  const deploy: Action = { operation: 'deploy', label: release.status === 'failed' ? (zh ? '重新发布全部节点' : 'Redeploy all nodes') : (zh ? '发布' : 'Deploy'), description: zh ? '将此版本应用到快照中的全部目标节点' : 'Apply this release to every node in its target snapshot', icon: <Rocket /> }
  const primary: Action | undefined = canDeliver && release.status === 'awaiting_approval'
    ? { operation: 'approve', label: zh ? '批准' : 'Approve', description: zh ? '批准后仍需手动发布' : 'Approval still requires a separate deployment', icon: <CheckCircle2 /> }
    : retryCount > 0
      ? { operation: 'retry-failed', label: zh ? `重试失败节点（${retryCount}）` : `Retry failed (${retryCount})`, description: zh ? '仅重新尝试服务端确认可重试的失败节点' : 'Retry only the failed nodes eligible for remediation', icon: <RotateCcw /> }
      : canDeliver && ['approved', 'failed'].includes(release.status) ? deploy : undefined
  const secondary: Action[] = []
  if (primary?.operation === 'retry-failed' && canDeliver && ['approved', 'failed'].includes(release.status)) secondary.push(deploy)
  if (rollbackCount > 0) secondary.push({ operation: 'rollback-failed', label: zh ? `回滚失败节点（${rollbackCount}）` : `Roll back failed (${rollbackCount})`, description: zh ? '仅将失败节点恢复到各自部署前的版本' : 'Restore failed nodes to their own previous releases', icon: <RotateCcw />, danger: true })
  if (canDeliver && ['succeeded', 'rolled_back'].includes(release.status) && configuration.active_release_id !== release.id) secondary.push({ operation: 'rollback', label: zh ? '回滚到此版本' : 'Restore this release', description: zh ? '重新应用此版本，自动交付将切换为手动' : 'Reapply this release and switch automatic delivery to manual', icon: <RotateCcw />, danger: true })
  if (['awaiting_approval', 'approved'].includes(release.status)) secondary.push({ operation: 'reject', label: zh ? '拒绝此版本' : 'Reject this release', description: zh ? '拒绝后此候选版本不能再发布' : 'This candidate can no longer be deployed once rejected', icon: <XCircle />, danger: true })
  if (!primary && !secondary.length) return null
  return <div className="ml-auto flex shrink-0 items-center gap-1 py-1.5" role="group" aria-label={zh ? `Release #${release.id} 操作` : `Release #${release.id} actions`}>
    {primary && <Button size="sm" className="bg-black text-white hover:bg-neutral-800 dark:border-white/15" disabled={busy} title={busy ? (zh ? '交付操作进行中，请稍候' : 'A delivery operation is in progress') : primary.description} onClick={() => onAction(release, primary.operation)}>{pendingOperation === primary.operation ? <Spinner /> : primary.icon}{primary.label}</Button>}
    {!!secondary.length && <DropdownMenu>
      <DropdownMenuTrigger render={<Button variant="outline" size="sm" disabled={busy} aria-label={zh ? `Release #${release.id} 更多操作` : `More actions for release #${release.id}`}>{pendingOperation && pendingOperation !== primary?.operation ? <Spinner /> : <MoreHorizontal />}{zh ? '更多' : 'More'}</Button>} />
      <DropdownMenuContent align="end" className="w-72 max-w-[calc(100vw-2rem)]">
        {secondary.map(action => <DropdownMenuItem key={action.operation} disabled={busy} variant={action.danger ? 'destructive' : 'default'} className="items-start py-2" onClick={() => onAction(release, action.operation)}>
          <span className="mt-0.5">{action.icon}</span>
          <span className="min-w-0"><span className="block">{action.label}</span><span className="mt-0.5 block text-xs text-muted-foreground">{action.description}</span></span>
        </DropdownMenuItem>)}
      </DropdownMenuContent>
    </DropdownMenu>}
  </div>
}

function rejectionActor(release: DeliveryRelease) {
  return release.status === 'rejected' && release.failure_reason?.startsWith('Rejected by ')
    ? release.failure_reason.slice('Rejected by '.length).trim()
    : ''
}

function ReleaseApproval({ release, zh }: { release: DeliveryRelease; zh: boolean }) {
  const rejected = release.status === 'rejected'
  if (!rejected && !release.approved_by) return null
  const actor = rejected ? rejectionActor(release) : release.approved_by_name
  const label = rejected ? (zh ? '已拒绝' : 'Rejected') : (zh ? '已批准' : 'Approved')
  return <span role="status" className={`inline-flex min-w-0 max-w-full items-center gap-1 text-xs ${rejected ? 'text-red-600 dark:text-red-400' : 'text-emerald-600 dark:text-emerald-400'}`}>
    {rejected ? <X className="size-3.5 shrink-0" aria-hidden="true" /> : <Check className="size-3.5 shrink-0" aria-hidden="true" />}
    <span className="min-w-0 [overflow-wrap:anywhere]"><span className="sr-only">{label}: </span>{actor || (zh ? '未知用户' : 'Unknown user')}</span>
  </span>
}

function ReleaseDetails({ release, zh }: { release: DeliveryRelease; zh: boolean }) {
  const { formatDateTime } = useDateTime()

  const images = parseStringList(release.image_references)
  const files = parseStringList(release.compose_files)
  const deployments = release.deployments ?? []
  type Deployment = (typeof deployments)[number]
  const pagination = useListPagination(deployments)
  return <div className="flex w-full flex-col gap-4">
    {release.failure_reason && !rejectionActor(release) && <Alert variant="destructive"><TriangleAlert /><AlertDescription>{release.failure_reason}</AlertDescription></Alert>}
    <DetailList items={[
      ['Commit SHA', release.commit_sha],
      [zh ? '配置 Hash' : 'Config hash', release.config_hash],
      [zh ? '触发者' : 'Triggered by', release.trigger_actor || triggerLabel(release.trigger_type, zh)],
      [zh ? '任务 ID' : 'Task ID', release.task_id || '—'],
      [zh ? 'Compose 文件' : 'Compose files', files.join(', ') || '—'],
      [zh ? '镜像' : 'Images', images.join(', ') || '—'],
    ]} />
    {!!deployments.length && (
      <><Table className="min-w-192 table-fixed">
        <colgroup>
          <col className="w-36" />
          <col className="w-36" />
          <col className="w-[40%]" />
          <col />
        </colgroup>
        <TableHeader>
          <TableRow>
            <TableHead>{zh ? '节点' : 'Node'}</TableHead>
            <TableHead>{zh ? '状态' : 'Status'}</TableHead>
            <TableHead>{zh ? '任务 ID' : 'Task ID'}</TableHead>
            <TableHead>{zh ? '最新尝试 / 历史' : 'Latest attempt / history'}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {(pagination.items as Deployment[]).map((deployment) => (
            <TableRow key={deployment.id} className="[&>td]:align-top">
              <TableCell className="whitespace-normal break-words [overflow-wrap:anywhere]">{deployment.node_name}</TableCell>
              <TableCell><ReleaseStatus status={String(deployment.status)} zh={zh} /></TableCell>
              <TableCell>
                <div className="whitespace-normal break-all font-mono text-xs">{deployment.task_id || '—'}</div>
                {/* Keep the row height stable when live progress appears or finishes. */}
                <div className="mt-1 h-6">
                  {['pulling', 'deploying', 'verifying', 'rolling_back'].includes(deployment.status) && <>
                    <Progress value={deployment.progress ?? 0} aria-label={zh ? `${deployment.node_name} 交付进度` : `Delivery progress for ${deployment.node_name}`} />
                    {deployment.message && <Tooltip>
                      <TooltipTrigger render={<p tabIndex={0} className="mt-1 truncate text-xs leading-4 text-muted-foreground" />}>{deployment.message}</TooltipTrigger>
                      <TooltipContent>{deployment.message}</TooltipContent>
                    </Tooltip>}
                  </>}
                </div>
              </TableCell>
              <TableCell className="whitespace-normal break-all text-xs text-muted-foreground">
                {deployment.failure_reason && <p className="text-destructive">{deployment.failure_reason}</p>}
                {deployment.rollback_result && <p>{zh ? '回滚结果' : 'Rollback'}: {deployment.rollback_result}</p>}
                {!!deployment.attempts?.length && <details className="mt-1"><summary className="cursor-pointer">{zh ? `${deployment.attempts.length} 次尝试` : `${deployment.attempts.length} attempts`}</summary><div className="mt-2 flex flex-col gap-2 pl-3">{[...deployment.attempts].reverse().map((attempt) => <div key={attempt.id}><div className="flex flex-wrap items-center gap-2"><ReleaseStatus status={attempt.status} zh={zh} /><span>{attempt.operation.replaceAll('_', ' ')} → Release #{attempt.target_release_id}</span></div><p>{attempt.failure_reason || attempt.message || '—'}</p><p className="text-[11px]">{formatDateTime(attempt.started_at || attempt.created_at)}{attempt.finished_at ? ` → ${formatDateTime(attempt.finished_at)}` : ''}</p></div>)}</div></details>}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table><ListPagination {...pagination} zh={zh} /></>
    )}
    {release.health_summary && <div className="flex flex-col gap-1.5">
      <span className="text-sm font-medium">{zh ? '健康状态' : 'Health summary'}</span>
      <pre className="max-h-36 overflow-auto overscroll-contain rounded-lg bg-muted p-3 font-mono text-xs text-muted-foreground">{release.health_summary}</pre>
    </div>}
  </div>
}

function ReleaseStatus({ status, zh }: { status: string; zh: boolean }) { return <Badge className={statusBadgeClass(status)}>{statusLabel(status, zh)}</Badge> }

function modeLabel(value: string, zh: boolean) { if (value === 'auto') return zh ? '自动发布' : 'Automatic'; if (value === 'observe') return zh ? '仅观察' : 'Observe only'; return zh ? '手动发布' : 'Manual' }
function statusLabel(value: string, zh: boolean) { if (!zh) return value.replaceAll('_', ' '); const labels: Record<string, string> = { validating: '校验中', awaiting_approval: '等待批准', approved: '已批准', rejected: '已拒绝', pulling: '拉取镜像', deploying: '部署中', verifying: '健康检查', succeeded: '成功', partial_failed: '部分失败', failed: '失败', rolling_back: '回滚中', rolled_back: '已回滚', rollback_failed: '回滚失败' }; return labels[value] || value }
function triggerLabel(value: string, zh: boolean) { if (!zh) return value.replaceAll('_', ' '); const labels: Record<string, string> = { webhook: 'Webhook', poll: '定时轮询', manual: '手动同步', rollback: '回滚', startup_reconcile: '启动对账' }; return labels[value] || value }
