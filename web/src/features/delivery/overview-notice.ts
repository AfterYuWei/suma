import type { CDDrift, DeliveryRelease } from './types'

export interface DeliveryNotice {
  tone: 'checking' | 'success' | 'warning'
  title: string
  description: string
}

const failedStatuses = new Set(['failed', 'partial_failed', 'rollback_failed', 'interrupted'])

export function deliveryOverviewNotice({ drift, latest, desiredCommit, error, zh }: {
  drift?: CDDrift
  latest?: DeliveryRelease
  desiredCommit: string
  error?: string
  zh: boolean
}): DeliveryNotice {
  if (error) return { tone: 'warning', title: zh ? '无法读取交付状态' : 'Unable to read delivery state', description: error }

  // Use only the latest release for the current desired commit. Historical
  // failures must not override a newer successful deployment or a new Git ref.
  const currentFailure = latest && (!desiredCommit || latest.commit_sha === desiredCommit)
    && (!drift?.active_release_id || latest.id >= drift.active_release_id)
    && (failedStatuses.has(latest.status) || latest.status === 'rolled_back' && !!latest.failure_reason)
  if (currentFailure) {
    const nodeReasons = latest.deployments?.flatMap(node => {
      if (!failedStatuses.has(node.status) && node.status !== 'rolled_back') return []
      const attempt = node.attempts?.at(-1)
      const reason = (attempt && failedStatuses.has(attempt.status) ? attempt.failure_reason : '') || node.failure_reason
      return reason?.trim() ? [`${node.node_name || node.node_id}: ${reason}`] : []
    }) ?? []
    return {
      tone: 'warning',
      title: latest.status === 'partial_failed' ? (zh ? '部分节点交付失败' : 'Delivery failed on some nodes')
        : latest.status === 'rolled_back' ? (zh ? '交付失败，已回滚' : 'Delivery failed and was rolled back')
          : (zh ? '交付失败' : 'Delivery failed'),
      description: nodeReasons.join('\n') || latest.failure_reason?.trim() || driftReason(drift?.reason, zh)
        || (zh ? '未返回具体失败原因，请查看该 Release 的任务日志。' : 'No failure reason was returned. Check the task logs for this release.'),
    }
  }
  if (!drift) return { tone: 'checking', title: zh ? '正在检查交付状态' : 'Checking delivery state', description: zh ? '正在读取期望 Commit 与当前活动 Release。' : 'Reading the desired commit and active release.' }
  if (drift.status === 'healthy') return { tone: 'success', title: zh ? '运行状态与 Git 已对齐' : 'Runtime is aligned with Git', description: zh ? '当前活动 Release 使用期望 Commit。' : 'The active release uses the desired commit.' }

  const nodeReasons = drift.nodes.filter(node => node.status !== 'healthy' && node.reason?.trim())
    .map(node => `${node.node_name || node.node_id}: ${driftReason(node.reason, zh)}`)
  return {
    tone: 'warning',
    title: drift.status === 'unknown' ? (zh ? '无法确认节点运行状态' : 'Unable to determine node runtime state') : (zh ? '交付状态异常' : 'Delivery state needs attention'),
    description: nodeReasons.join('\n') || driftReason(drift.reason, zh)
      || (zh ? '未返回具体异常原因，请查看任务日志或检查节点连接。' : 'No reason was returned. Check the task logs or node connection.'),
  }
}

export function driftReason(value: string | undefined, zh: boolean): string {
  if (!value || !zh) return value || ''
  const reasons: Record<string, string> = {
    'repository has not been synchronized': '仓库尚未同步。',
    'no release is active': '当前没有活动 Release。',
    'active release differs from desired Git commit': '当前活动 Release 与 Git 期望 Commit 不一致。',
    'unable to read active release runtime state': '无法读取活动 Release 的运行状态。',
    'active release has missing or unhealthy containers': '活动 Release 存在缺失、停止或不健康的容器。',
    'active release state is unavailable': '无法读取活动 Release 记录。',
    'Compose runner does not support node targets': 'Compose 执行器不支持目标节点。',
    'target nodes do not share one active release': '目标节点当前运行的 Release 不一致。',
  }
  return reasons[value] || value
}
