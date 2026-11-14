import { Badge } from '../../components/ui/badge'

const statusBadgeClass = (status: string) => status === 'succeeded'
  ? 'border-transparent bg-emerald-500/15 text-emerald-600 dark:text-emerald-400'
  : ['ready', 'approved', 'partial_failed', 'rolled_back', 'awaiting_approval', 'rolling_back'].includes(status)
    ? 'border-transparent bg-amber-500/15 text-amber-600 dark:text-amber-400'
    : ['failed', 'rollback_failed', 'rejected'].includes(status)
      ? 'border-transparent bg-red-500/15 text-red-600 dark:text-red-400'
      : 'border-transparent bg-primary/10 text-primary'


export function ReleaseStatus({ status, zh }: { status: string; zh: boolean }) { return <Badge className={statusBadgeClass(status)}>{statusLabel(status, zh)}</Badge> }


function statusLabel(value: string, zh: boolean) { if (!zh) return value.replaceAll('_', ' '); const labels: Record<string, string> = { pending: '排队中', running: '执行中', canceled: '已取消', interrupted: '已中断', validating: '校验中', ready: '待发布', awaiting_approval: '待发布', approved: '待发布', rejected: '已拒绝', pulling: '拉取镜像', deploying: '部署中', verifying: '健康检查', succeeded: '成功', partial_failed: '部分失败', failed: '失败', rolling_back: '回滚中', rolled_back: '已回滚', rollback_failed: '回滚失败' }; return labels[value] || value }
