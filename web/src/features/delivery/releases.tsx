import { Alert, AlertDescription, AlertTitle } from '../../components/ui/alert'
import { Badge } from '../../components/ui/badge'
import { Button } from '../../components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '../../components/ui/card'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '../../components/ui/collapsible'
import { Spinner } from '../../components/ui/spinner'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../components/ui/table'
import { CheckCircle2, ChevronDownIcon, GitCommitHorizontal, History, Loader2, RotateCcw, Rocket, TriangleAlert, XCircle } from 'lucide-react'
import type { ReactNode } from 'react'
import type { CDConfiguration, CDDrift, DeliveryRelease } from './types'
import { parseStringList, repositoryName, shortCommit } from './types'

const statusBadgeClass = (status: string) => ['succeeded', 'approved'].includes(status)
  ? 'border-transparent bg-emerald-500/15 text-emerald-600 dark:text-emerald-400'
  : ['partial_failed', 'rolled_back', 'awaiting_approval', 'rolling_back'].includes(status)
    ? 'border-transparent bg-amber-500/15 text-amber-600 dark:text-amber-400'
    : ['failed', 'rollback_failed', 'rejected'].includes(status)
      ? 'border-transparent bg-red-500/15 text-red-600 dark:text-red-400'
      : 'border-transparent bg-primary/10 text-primary'

function Notice({ tone, title, children }: { tone: 'checking' | 'success' | 'warning'; title: string; children: ReactNode }) {
  return <Alert className={tone === 'success' ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:border-emerald-500/20 dark:bg-emerald-500/10 dark:text-emerald-300' : tone === 'warning' ? 'border-amber-500/30 bg-amber-500/10 text-amber-700 dark:border-amber-500/20 dark:bg-amber-500/10 dark:text-amber-300' : ''}>
    {tone === 'checking' ? <Loader2 className="animate-spin" /> : tone === 'success' ? <CheckCircle2 /> : <TriangleAlert />}
    <AlertTitle>{title}</AlertTitle>
    <AlertDescription>{children}</AlertDescription>
  </Alert>
}

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
  return <div className="flex w-full flex-col items-center gap-1.5 rounded-xl border border-dashed py-12 text-center">
    <span className="text-muted-foreground">{icon}</span>
    <p className="text-sm font-medium">{title}</p>
    {description && <p className="max-w-md text-xs text-muted-foreground">{description}</p>}
  </div>
}

export function CDOverview({ configuration, drift, releases, zh }: { configuration: CDConfiguration; drift?: CDDrift; releases?: DeliveryRelease[]; zh: boolean }) {
  const latest = releases?.[0]
  const repository = configuration.repository
  const checking = !drift
  const aligned = !!drift && !drift.drifted
  return <div className="flex w-full flex-col gap-4">
    <Notice
      tone={checking ? 'checking' : aligned ? 'success' : 'warning'}
      title={checking ? (zh ? '正在检查交付漂移' : 'Checking delivery drift') : aligned ? (zh ? '运行状态与 Git 已对齐' : 'Runtime is aligned with Git') : (zh ? '运行状态与 Git 期望不一致' : 'Runtime differs from the Git desired state')}
    >
      {checking ? (zh ? '正在读取期望 Commit 与当前活动 Release。' : 'Reading the desired commit and active release.') : driftReason(drift.reason, zh) || (zh ? '当前活动 Release 使用期望 Commit。' : 'The active release uses the desired commit.')}
    </Notice>
    <Card>
      <CardHeader><CardTitle>{zh ? '交付状态' : 'Delivery state'}</CardTitle></CardHeader>
      <CardContent>
        <DetailList items={[
          [zh ? '仓库' : 'Repository', `${repositoryName(repository.clone_url)} · ${repository.clone_url}`],
          [zh ? '跟踪引用' : 'Tracked ref', `${repository.ref_type}:${repository.ref}`],
          [zh ? 'Compose 文件' : 'Compose files', repository.compose_files.join(' · ')],
          [zh ? '交付模式' : 'Delivery mode', `${modeLabel(configuration.reconcile_mode, zh)} · ${zh ? `每 ${configuration.sync_interval_seconds} 秒轮询` : `poll every ${configuration.sync_interval_seconds}s`}`],
          [zh ? 'Git 期望' : 'Git desired', shortCommit(configuration.desired_commit)],
          [zh ? '已检查' : 'Observed', shortCommit(configuration.observed_commit)],
          [zh ? '当前运行' : 'Active', `${shortCommit(drift?.active_commit)}${configuration.active_release_id ? ` · Release #${configuration.active_release_id}` : ''}`],
          [zh ? '运行时健康' : 'Runtime health', !drift ? (zh ? '检查中' : 'Checking') : !drift.active_release_id ? (zh ? '无活动版本' : 'No active release') : drift.runtime_healthy ? (zh ? '健康' : 'Healthy') : (zh ? '异常' : 'Unhealthy')],
        ]} />
      </CardContent>
    </Card>
    <Card>
      <CardHeader><CardTitle>{zh ? '最近一次交付' : 'Latest delivery'}</CardTitle></CardHeader>
      <CardContent>
        {latest ? <div className="flex flex-wrap items-center gap-3">
          <ReleaseStatus status={latest.status} zh={zh} />
          <div className="min-w-0">
            <p className="font-medium">{latest.commit_message || (zh ? '无提交说明' : 'No commit message')}</p>
            <p className="mt-0.5 text-xs text-muted-foreground">#{latest.id} · {shortCommit(latest.commit_sha)} · {new Date(latest.created_at).toLocaleString(zh ? 'zh-CN' : 'en-US')}</p>
          </div>
        </div> : <EmptyHint icon={<GitCommitHorizontal className="size-5" />} title={zh ? '同步仓库后将在这里生成首个 Release' : 'Synchronize the repository to create the first release'} />}
      </CardContent>
    </Card>
  </div>
}

export type ReleaseOperation = 'approve' | 'reject' | 'deploy' | 'rollback'

export function ReleasePanel({ configuration, releases, pendingReleaseID, zh, onAction }: { configuration: CDConfiguration; releases?: DeliveryRelease[]; pendingReleaseID?: number; zh: boolean; onAction: (release: DeliveryRelease, action: ReleaseOperation) => void }) {
  if (!releases?.length) return <EmptyHint icon={<History className="size-6" />} title={zh ? '还没有 Release' : 'No releases yet'} description={zh ? '点击同步以读取 Git 并验证 Compose 配置。' : 'Synchronize to read Git and validate the Compose configuration.'} />
  return <div className="flex w-full flex-col gap-2">{releases.map((release) => {
    const active = configuration.active_release_id === release.id
    const canApprove = configuration.reconcile_mode !== 'observe' && release.status === 'awaiting_approval'
    const canReject = release.status === 'awaiting_approval' || release.status === 'approved'
    const canDeploy = configuration.reconcile_mode !== 'observe' && (release.status === 'approved' || release.status === 'failed')
    const canRollback = configuration.reconcile_mode !== 'observe' && (release.status === 'succeeded' || release.status === 'rolled_back') && !active
    const pending = pendingReleaseID === release.id
    return <Collapsible key={release.id} className="group/collapsible w-full overflow-hidden rounded-xl border bg-card">
      <div className="flex items-start gap-2 pr-2">
        <CollapsibleTrigger className="flex min-w-0 flex-1 cursor-pointer flex-wrap items-center gap-x-2 gap-y-1 py-2.5 pl-3 text-left hover:bg-muted/50 [&_svg]:shrink-0">
          <ChevronDownIcon className="size-4 shrink-0 text-muted-foreground transition-transform group-data-open/collapsible:rotate-180" />
          <GitCommitHorizontal className="size-[18px] text-muted-foreground" />
          <span className="font-medium">{release.commit_message || (zh ? '无提交说明' : 'No commit message')}</span>
          <ReleaseStatus status={release.status} zh={zh} />
          {active && <Badge className="border-transparent bg-emerald-500/15 text-emerald-600 dark:text-emerald-400">{zh ? '当前运行' : 'Active'}</Badge>}
          <span className="text-xs text-muted-foreground">#{release.id} · {shortCommit(release.commit_sha)}</span>
        </CollapsibleTrigger>
        <div className="flex flex-wrap items-center justify-end gap-1.5 py-1.5">
          {canApprove && <Button size="sm" variant="secondary" disabled={pending} onClick={() => onAction(release, 'approve')}><CheckCircle2 />{zh ? '批准' : 'Approve'}</Button>}
          {canReject && <Button size="sm" variant="destructive" disabled={pending} onClick={() => onAction(release, 'reject')}><XCircle />{zh ? '拒绝' : 'Reject'}</Button>}
          {canDeploy && <Button size="sm" disabled={pending} onClick={() => onAction(release, 'deploy')}>{pending ? <Spinner /> : <Rocket />}{release.status === 'failed' ? (zh ? '重新发布' : 'Redeploy') : (zh ? '发布' : 'Deploy')}</Button>}
          {canRollback && <Button size="sm" variant="outline" disabled={pending} onClick={() => onAction(release, 'rollback')}><RotateCcw />{zh ? '回滚' : 'Restore'}</Button>}
        </div>
      </div>
      <CollapsibleContent className="border-t px-3 pb-3 pt-3">
        <ReleaseDetails release={release} zh={zh} />
      </CollapsibleContent>
    </Collapsible>
  })}</div>
}

function ReleaseDetails({ release, zh }: { release: DeliveryRelease; zh: boolean }) {
  const images = parseStringList(release.image_references)
  const files = parseStringList(release.compose_files)
  const deployments = release.deployments ?? []
  type Deployment = (typeof deployments)[number]
  return <div className="flex w-full flex-col gap-4">
    {release.failure_reason && <Alert variant="destructive"><TriangleAlert /><AlertDescription>{release.failure_reason}</AlertDescription></Alert>}
    <DetailList items={[
      ['Commit SHA', release.commit_sha],
      [zh ? '配置 Hash' : 'Config hash', release.config_hash],
      [zh ? '触发者' : 'Triggered by', release.trigger_actor || triggerLabel(release.trigger_type, zh)],
      [zh ? '任务 ID' : 'Task ID', release.task_id || '—'],
      [zh ? 'Compose 文件' : 'Compose files', files.join(', ') || '—'],
      [zh ? '镜像' : 'Images', images.join(', ') || '—'],
    ]} />
    {!!deployments.length && (
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{zh ? '节点' : 'Node'}</TableHead>
            <TableHead>{zh ? '状态' : 'Status'}</TableHead>
            <TableHead>{zh ? '任务 ID' : 'Task ID'}</TableHead>
            <TableHead>{zh ? '失败原因' : 'Failure'}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {(deployments as Deployment[]).map((deployment) => (
            <TableRow key={deployment.id}>
              <TableCell>{deployment.node_name}</TableCell>
              <TableCell><ReleaseStatus status={String(deployment.status)} zh={zh} /></TableCell>
              <TableCell className="font-mono text-xs">{deployment.task_id}</TableCell>
              <TableCell className="whitespace-normal break-all text-xs text-muted-foreground">{deployment.failure_reason}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    )}
    {release.health_summary && <div className="flex flex-col gap-1.5">
      <span className="text-sm font-medium">{zh ? '健康状态' : 'Health summary'}</span>
      <pre className="max-h-36 overflow-auto rounded-lg bg-muted p-3 font-mono text-xs text-muted-foreground">{release.health_summary}</pre>
    </div>}
  </div>
}

function ReleaseStatus({ status, zh }: { status: string; zh: boolean }) { return <Badge className={statusBadgeClass(status)}>{statusLabel(status, zh)}</Badge> }

function modeLabel(value: string, zh: boolean) { if (value === 'auto') return zh ? '自动发布' : 'Automatic'; if (value === 'observe') return zh ? '仅观察' : 'Observe only'; return zh ? '手动发布' : 'Manual' }
function statusLabel(value: string, zh: boolean) { if (!zh) return value.replaceAll('_', ' '); const labels: Record<string, string> = { validating: '校验中', awaiting_approval: '等待批准', approved: '已批准', rejected: '已拒绝', pulling: '拉取镜像', deploying: '部署中', verifying: '健康检查', succeeded: '成功', partial_failed: '部分失败', failed: '失败', rolling_back: '回滚中', rolled_back: '已回滚', rollback_failed: '回滚失败' }; return labels[value] || value }
function triggerLabel(value: string, zh: boolean) { if (!zh) return value.replaceAll('_', ' '); const labels: Record<string, string> = { webhook: 'Webhook', poll: '定时轮询', manual: '手动同步', rollback: '回滚', startup_reconcile: '启动对账' }; return labels[value] || value }
function driftReason(value: string | undefined, zh: boolean) { if (!value || !zh) return value || ''; const reasons: Record<string, string> = { 'repository has not been synchronized': '仓库尚未同步。', 'no release is active': '当前没有活动 Release。', 'active release differs from desired Git commit': '当前活动 Release 与 Git 期望 Commit 不一致。', 'unable to read active release runtime state': '无法读取活动 Release 的运行状态。', 'active release has missing or unhealthy containers': '活动 Release 存在缺失、停止或不健康的容器。' }; return reasons[value] || value }
