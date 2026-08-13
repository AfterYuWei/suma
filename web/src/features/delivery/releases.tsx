import { CheckCircle2, GitCommitHorizontal, HeartPulse, History, RotateCcw, Rocket, TriangleAlert, XCircle } from 'lucide-react'
import type { CDConfiguration, CDDrift, DeliveryRelease } from './types'
import { parseStringList, repositoryName, shortCommit } from './types'

const statusTone: Record<string, string> = {
  succeeded: 'border-success/25 bg-success-subtle text-success',
  partial_failed: 'border-warning/25 bg-warning-subtle text-warning',
  rolled_back: 'border-warning/25 bg-warning-subtle text-warning',
  failed: 'border-danger/25 bg-danger-subtle text-danger',
  rollback_failed: 'border-danger/25 bg-danger-subtle text-danger',
  rejected: 'border-danger/25 bg-danger-subtle text-danger',
  awaiting_approval: 'border-warning/25 bg-warning-subtle text-warning',
  approved: 'border-success/25 bg-success-subtle text-success',
  validating: 'border-accent/25 bg-accent-subtle text-accent',
  pulling: 'border-accent/25 bg-accent-subtle text-accent',
  deploying: 'border-accent/25 bg-accent-subtle text-accent',
  verifying: 'border-accent/25 bg-accent-subtle text-accent',
  rolling_back: 'border-warning/25 bg-warning-subtle text-warning',
}

export function CDOverview({ configuration, drift, releases, zh }: { configuration: CDConfiguration; drift?: CDDrift; releases?: DeliveryRelease[]; zh: boolean }) {
  const latest = releases?.[0]
  const repository = configuration.repository
  const checking = !drift
  return <div className="py-6">
    <div className={`mb-6 flex items-start gap-3 border-y px-4 py-3 ${checking ? 'border-border bg-surface/35' : drift.drifted ? 'border-warning/25 bg-warning-subtle' : 'border-success/20 bg-success-subtle'}`}>
      <span className={`mt-1 size-2 shrink-0 rounded-full ${checking ? 'animate-pulse bg-text-subtle' : drift.drifted ? 'bg-warning' : 'bg-success'}`} />
      <div className="min-w-0"><p className={`text-sm font-medium ${checking ? 'text-text-muted' : drift.drifted ? 'text-warning' : 'text-success'}`}>{checking ? (zh ? '正在检查交付漂移' : 'Checking delivery drift') : drift.drifted ? (zh ? '运行状态与 Git 期望不一致' : 'Runtime differs from the Git desired state') : (zh ? '运行状态与 Git 已对齐' : 'Runtime is aligned with Git')}</p><p className="mt-1 text-xs leading-5 text-text-muted">{checking ? (zh ? '正在读取期望 Commit 与当前活动 Release。' : 'Reading the desired commit and active release.') : driftReason(drift.reason, zh) || (zh ? '当前活动 Release 使用期望 Commit。' : 'The active release uses the desired commit.')}</p></div>
    </div>

    <section className="grid gap-6 border-t border-border py-6 lg:grid-cols-[190px_minmax(0,1fr)]"><div><h2 className="text-sm font-semibold">{zh ? '仓库来源' : 'Repository source'}</h2><p className="mt-1 text-[11px] leading-5 text-text-subtle">Git · {configuration.reconcile_mode}</p></div><dl className="divide-y divide-border border-y border-border text-xs">
      <InfoRow label={zh ? '仓库' : 'Repository'}><span className="font-medium">{repositoryName(repository.clone_url)}</span><span className="ml-2 font-mono text-[10px] text-text-subtle">{repository.clone_url}</span></InfoRow>
      <InfoRow label={zh ? '跟踪引用' : 'Tracked ref'}><span className="rounded border border-border bg-surface px-1.5 py-0.5 font-mono text-[10px]">{repository.ref_type}:{repository.ref}</span></InfoRow>
      <InfoRow label={zh ? 'Compose 文件' : 'Compose files'}><span className="font-mono text-[10px]">{repository.compose_files.join(' · ')}</span></InfoRow>
      <InfoRow label={zh ? '交付模式' : 'Delivery mode'}><span className="capitalize">{modeLabel(configuration.reconcile_mode, zh)}</span><span className="ml-2 text-text-subtle">{zh ? `每 ${configuration.sync_interval_seconds} 秒轮询` : `poll every ${configuration.sync_interval_seconds}s`}</span></InfoRow>
    </dl></section>

    <section className="grid gap-6 border-t border-border py-6 lg:grid-cols-[190px_minmax(0,1fr)]"><div><h2 className="text-sm font-semibold">{zh ? '版本与运行状态' : 'Revision and runtime state'}</h2><p className="mt-1 text-[11px] leading-5 text-text-subtle">{zh ? '同时核对 Git Commit 和活动 Release 的容器健康。' : 'Checks both Git commits and the active release container health.'}</p></div><div className="grid divide-y divide-border border-y border-border sm:grid-cols-4 sm:divide-x sm:divide-y-0">
      <CommitState label={zh ? 'Git 期望' : 'Git desired'} commit={configuration.desired_commit} detail={zh ? '远端引用解析结果' : 'Resolved remote ref'} />
      <CommitState label={zh ? '已检查' : 'Observed'} commit={configuration.observed_commit} detail={zh ? '最近通过 Compose 校验' : 'Last Compose validation'} />
      <CommitState label={zh ? '当前运行' : 'Active'} commit={drift?.active_commit} detail={configuration.active_release_id ? `Release #${configuration.active_release_id}` : (zh ? '尚未发布' : 'Not deployed')} />
      <RuntimeState drift={drift} zh={zh} />
    </div></section>

    <section className="grid gap-6 border-t border-border py-6 lg:grid-cols-[190px_minmax(0,1fr)]"><div><h2 className="text-sm font-semibold">{zh ? '最近一次交付' : 'Latest delivery'}</h2><p className="mt-1 text-[11px] leading-5 text-text-subtle">{zh ? '同步、验证与部署形成不可变 Release。' : 'Sync, validation, and deployment create an immutable release.'}</p></div>{latest ? <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-2 border-y border-border px-2 py-3"><ReleaseStatus status={latest.status} zh={zh} /><div className="min-w-0 flex-1"><p className="truncate text-xs font-medium">{latest.commit_message || (zh ? '无提交说明' : 'No commit message')}</p><p className="mt-1 font-mono text-[9px] text-text-subtle">#{latest.id} · {shortCommit(latest.commit_sha)} · {new Date(latest.created_at).toLocaleString(zh ? 'zh-CN' : 'en-US')}</p></div></div> : <div className="border-y border-border py-8 text-center text-xs text-text-subtle">{zh ? '同步仓库后将在这里生成首个 Release。' : 'Synchronize the repository to create the first release.'}</div>}</section>
  </div>
}

export type ReleaseOperation = 'approve' | 'reject' | 'deploy' | 'rollback'

export function ReleasePanel({ configuration, releases, pendingReleaseID, zh, onAction }: { configuration: CDConfiguration; releases?: DeliveryRelease[]; pendingReleaseID?: number; zh: boolean; onAction: (release: DeliveryRelease, action: ReleaseOperation) => void }) {
  if (!releases?.length) return <div className="border-y border-border py-16 text-center"><History className="mx-auto size-5 text-text-subtle" /><p className="mt-3 text-sm font-medium">{zh ? '还没有 Release' : 'No releases yet'}</p><p className="mt-1 text-xs text-text-subtle">{zh ? '点击同步以读取 Git 并验证 Compose 配置。' : 'Synchronize to read Git and validate the Compose configuration.'}</p></div>

  return <div className="divide-y divide-border border-y border-border">{releases.map((release) => {
    const images = parseStringList(release.image_references)
    const files = parseStringList(release.compose_files)
    const active = configuration.active_release_id === release.id
    const canApprove = configuration.reconcile_mode !== 'observe' && release.status === 'awaiting_approval'
    const canReject = release.status === 'awaiting_approval' || release.status === 'approved'
    const canDeploy = configuration.reconcile_mode !== 'observe' && (release.status === 'approved' || release.status === 'failed')
    const canRollback = configuration.reconcile_mode !== 'observe' && (release.status === 'succeeded' || release.status === 'rolled_back') && !active
    const pending = pendingReleaseID === release.id
    return <article key={release.id} className="group px-2 py-4 hover:bg-surface/35">
      <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_160px_auto] lg:items-start">
        <div className="flex min-w-0 gap-3"><span className={`mt-0.5 grid size-8 shrink-0 place-items-center rounded-md border ${active ? 'border-success/25 bg-success-subtle text-success' : 'border-border bg-surface text-text-subtle'}`}><GitCommitHorizontal className="size-4" /></span><div className="min-w-0"><div className="flex flex-wrap items-center gap-2"><p className="truncate text-sm font-medium">{release.commit_message || (zh ? '无提交说明' : 'No commit message')}</p>{active && <span className="rounded border border-success/25 bg-success-subtle px-1.5 py-0.5 text-[9px] font-semibold uppercase tracking-wider text-success">{zh ? '当前运行' : 'Active'}</span>}</div><p className="mt-1 flex flex-wrap items-center gap-x-2 font-mono text-[9px] text-text-subtle"><span>Release #{release.id}</span><span>·</span><span>{shortCommit(release.commit_sha)}</span><span>·</span><span>{release.commit_author || '—'}</span></p>{release.failure_reason && <p className="mt-2 flex items-start gap-1.5 text-[10px] leading-4 text-danger"><TriangleAlert className="mt-0.5 size-3 shrink-0" />{release.failure_reason}</p>}</div></div>
        <div><ReleaseStatus status={release.status} zh={zh} /><p className="mt-1.5 text-[9px] text-text-subtle">{triggerLabel(release.trigger_type, zh)} · {new Date(release.created_at).toLocaleString(zh ? 'zh-CN' : 'en-US')}</p></div>
        <div className="flex flex-wrap gap-2 lg:justify-end">{canApprove && <button disabled={pending} onClick={() => onAction(release, 'approve')} className="flex h-8 items-center gap-1.5 rounded-md border border-success/30 bg-success-subtle px-3 text-xs font-medium text-success disabled:opacity-50"><CheckCircle2 className="size-3.5" />{zh ? '批准' : 'Approve'}</button>}{canReject && <button disabled={pending} onClick={() => onAction(release, 'reject')} className="flex h-8 items-center gap-1.5 rounded-md border border-danger/25 bg-surface px-3 text-xs text-danger hover:bg-danger-subtle disabled:opacity-50"><XCircle className="size-3.5" />{zh ? '拒绝' : 'Reject'}</button>}{canDeploy && <button disabled={pending} onClick={() => onAction(release, 'deploy')} className="flex h-8 items-center gap-1.5 rounded-md bg-accent px-3 text-xs font-semibold text-accent-foreground disabled:opacity-50"><Rocket className="size-3.5" />{pending ? (zh ? '处理中…' : 'Working…') : release.status === 'failed' ? (zh ? '重新发布' : 'Redeploy') : (zh ? '发布' : 'Deploy')}</button>}{canRollback && <button disabled={pending} onClick={() => onAction(release, 'rollback')} className="flex h-8 items-center gap-1.5 rounded-md border border-border bg-surface px-3 text-xs hover:bg-surface-hover disabled:opacity-50"><RotateCcw className="size-3.5" />{zh ? '回滚到此版本' : 'Restore'}</button>}</div>
      </div>
      <details className="ml-11 mt-3 text-[10px] text-text-muted"><summary className="w-fit cursor-pointer select-none hover:text-text">{zh ? '查看发布详情' : 'Release details'}</summary><dl className="mt-3 grid gap-x-6 gap-y-2 rounded-md border border-border bg-surface/40 p-3 sm:grid-cols-2"><Detail label="Commit SHA" value={release.commit_sha} /><Detail label={zh ? '配置 Hash' : 'Config hash'} value={release.config_hash} /><Detail label={zh ? '触发者' : 'Triggered by'} value={release.trigger_actor || release.trigger_type} /><Detail label={zh ? '任务 ID' : 'Task ID'} value={release.task_id || '—'} /><Detail label={zh ? 'Compose 文件' : 'Compose files'} value={files.join(', ') || '—'} /><Detail label={zh ? '镜像' : 'Images'} value={images.join(', ') || '—'} />{release.approved_at && <Detail label={zh ? '批准记录' : 'Approval'} value={`${release.approved_by ? `User #${release.approved_by} · ` : ''}${new Date(release.approved_at).toLocaleString(zh ? 'zh-CN' : 'en-US')}`} />}{release.deployments?.length ? <div className="sm:col-span-2"><dt className="mb-2 text-text-subtle">{zh ? '逐节点发布' : 'Per-node deployments'}</dt><dd className="divide-y divide-border border-y border-border">{release.deployments.map((deployment) => <div key={deployment.id} className="grid grid-cols-[minmax(0,1fr)_110px] gap-3 py-2"><div className="min-w-0"><p className="truncate text-text">{deployment.node_name}</p>{deployment.failure_reason && <p className="mt-1 truncate text-danger" title={deployment.failure_reason}>{deployment.failure_reason}</p>}</div><div className="text-right"><ReleaseStatus status={deployment.status} zh={zh} />{deployment.task_id && <p className="mt-1 truncate font-mono text-[8px] text-text-subtle">{deployment.task_id}</p>}</div></div>)}</dd></div> : null}{release.health_summary && <div className="sm:col-span-2"><dt className="mb-1 text-text-subtle">{zh ? '健康状态' : 'Health summary'}</dt><dd><pre className="max-h-36 overflow-auto whitespace-pre-wrap rounded bg-[var(--code-background)] p-2 font-mono text-[9px] leading-4">{release.health_summary}</pre></dd></div>}</dl></details>
    </article>
  })}</div>
}

function InfoRow({ label, children }: { label: string; children: React.ReactNode }) {
  return <div className="grid min-h-11 grid-cols-[130px_minmax(0,1fr)] items-center gap-4 px-2"><dt className="text-text-muted">{label}</dt><dd className="min-w-0 truncate">{children}</dd></div>
}

function CommitState({ label, commit, detail }: { label: string; commit?: string; detail: string }) {
  return <div className="px-4 py-4"><p className="text-[10px] uppercase tracking-wider text-text-subtle">{label}</p><p className="mt-2 font-mono text-sm font-semibold">{shortCommit(commit)}</p><p className="mt-1 text-[9px] text-text-subtle">{detail}</p></div>
}

function RuntimeState({ drift, zh }: { drift?: CDDrift; zh: boolean }) {
  if (!drift) return <div className="px-4 py-4"><p className="text-[10px] uppercase tracking-wider text-text-subtle">{zh ? '运行时健康' : 'Runtime health'}</p><p className="mt-2 flex items-center gap-1.5 text-sm font-semibold text-text-muted"><HeartPulse className="size-3.5 animate-pulse" />{zh ? '检查中' : 'Checking'}</p><p className="mt-1 text-[9px] text-text-subtle">{zh ? '正在读取活动 Release 状态' : 'Reading the active release state'}</p></div>
  const active = !!drift?.active_release_id
  const healthy = drift.runtime_healthy
  return <div className="px-4 py-4"><p className="text-[10px] uppercase tracking-wider text-text-subtle">{zh ? '运行时健康' : 'Runtime health'}</p><p className={`mt-2 flex items-center gap-1.5 text-sm font-semibold ${!active ? 'text-text-muted' : healthy ? 'text-success' : 'text-danger'}`}><HeartPulse className="size-3.5" />{!active ? (zh ? '无活动版本' : 'No active release') : healthy ? (zh ? '健康' : 'Healthy') : (zh ? '异常' : 'Unhealthy')}</p><p className="mt-1 text-[9px] text-text-subtle">{!active ? (zh ? '部署后开始检查' : 'Checked after deployment') : healthy ? (zh ? '服务均已运行并通过健康检查' : 'Services are running and healthy') : (zh ? '容器缺失、停止或未通过健康检查' : 'Containers are missing, stopped, or unhealthy')}</p></div>
}

function ReleaseStatus({ status, zh }: { status: string; zh: boolean }) {
  return <span className={`inline-flex rounded border px-1.5 py-0.5 text-[9px] font-semibold uppercase tracking-wider ${statusTone[status] || 'border-border bg-surface text-text-muted'}`}>{statusLabel(status, zh)}</span>
}

function Detail({ label, value }: { label: string; value: string }) {
  return <div className="min-w-0"><dt className="text-text-subtle">{label}</dt><dd className="mt-0.5 truncate font-mono text-[9px]" title={value}>{value}</dd></div>
}


function modeLabel(value: string, zh: boolean) {
  if (value === 'auto') return zh ? '自动发布' : 'Automatic'
  if (value === 'observe') return zh ? '仅观察' : 'Observe only'
  return zh ? '手动发布' : 'Manual'
}

function statusLabel(value: string, zh: boolean) {
  if (!zh) return value.replaceAll('_', ' ')
  const labels: Record<string, string> = { validating: '校验中', awaiting_approval: '等待批准', approved: '已批准', rejected: '已拒绝', pulling: '拉取镜像', deploying: '部署中', verifying: '健康检查', succeeded: '成功', partial_failed: '部分失败', failed: '失败', rolling_back: '回滚中', rolled_back: '已回滚', rollback_failed: '回滚失败' }
  return labels[value] || value
}

function triggerLabel(value: string, zh: boolean) {
  if (!zh) return value.replaceAll('_', ' ')
  const labels: Record<string, string> = { webhook: 'Webhook', poll: '定时轮询', manual: '手动同步', rollback: '回滚', startup_reconcile: '启动对账' }
  return labels[value] || value
}

function driftReason(value: string | undefined, zh: boolean) {
  if (!value || !zh) return value || ''
  const reasons: Record<string, string> = {
    'repository has not been synchronized': '仓库尚未同步。',
    'no release is active': '当前没有活动 Release。',
    'active release differs from desired Git commit': '当前活动 Release 与 Git 期望 Commit 不一致。',
    'unable to read active release runtime state': '无法读取活动 Release 的运行状态。',
    'active release has missing or unhealthy containers': '活动 Release 存在缺失、停止或不健康的容器。',
  }
  return reasons[value] || value
}
