import { useI18n } from '../../lib/i18n'
import { StatusBadge } from '../../components/ui/status-badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../components/ui/table'
import type { ImageUpdateServiceCheck, ImageUpdateSummary } from './types'

const statusTone = (status: string) => status === 'update_available' ? 'warning' : status === 'unavailable' ? 'critical' : status === 'blocked' ? 'neutral' : 'success'

const reasonLabel = (code: string, zh: boolean) => {
  const labels: Record<string, [string, string]> = {
    untagged: ['无标签镜像', 'Untagged image'],
    image_id: ['本地镜像 ID', 'Local image ID'],
    invalid_reference: ['引用无效', 'Invalid reference'],
    digest_pinned: ['digest 已钉定', 'Digest pinned'],
    authentication_required: ['需要认证', 'Authentication required'],
    credential_unavailable: ['缺少仓库凭据', 'Registry credential missing'],
    rate_limited: ['仓库限流', 'Rate limited'],
    timeout: ['超时', 'Timed out'],
    registry_unreachable: ['仓库不可达', 'Registry unreachable'],
    reference_not_found: ['引用不存在', 'Reference not found'],
    platform_unavailable: ['平台不可用', 'Platform unavailable'],
    unsupported_manifest: ['不支持的清单', 'Unsupported manifest'],
    not_running: ['服务未运行', 'Service not running'],
    managed_by_cd: ['由持续交付管理', 'Managed by CD'],
    image_mismatch: ['容器镜像不一致', 'Container images drifted'],
    one_off: ['一次性容器', 'One-off container'],
  }
  const label = labels[code]
  return label ? (zh ? label[0] : label[1]) : code
}

const shortDigest = (value?: string) => (value ? value.replace(/^sha256:/, '').slice(0, 12) : '')

export function ImageUpdateServiceTable({ services, zh }: { services: ImageUpdateServiceCheck[]; zh: boolean }) {
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>{zh ? '服务' : 'Service'}</TableHead>
          <TableHead>{zh ? '镜像引用' : 'Reference'}</TableHead>
          <TableHead className="w-36">{zh ? '状态' : 'Status'}</TableHead>
          <TableHead>{zh ? '远端 digest' : 'Remote digest'}</TableHead>
          <TableHead className="w-16 text-right">{zh ? '副本' : 'Replicas'}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {services.map((item) => (
          <TableRow key={item.service}>
            <TableCell className="font-medium">{item.service}</TableCell>
            <TableCell className="max-w-56 truncate font-mono text-xs text-muted-foreground" title={item.reference}>{item.reference}</TableCell>
            <TableCell>
              <StatusBadge tone={statusTone(item.status)}>{zh ? reasonLabel(item.status, true) : item.status}</StatusBadge>
              {item.reason_code && item.status !== 'update_available' && <div className="mt-1 text-xs text-muted-foreground">{reasonLabel(item.reason_code, zh)}</div>}
            </TableCell>
            <TableCell className="font-mono text-xs text-muted-foreground">{shortDigest(item.remote_manifest_digest) || '—'}</TableCell>
            <TableCell className="text-right tabular-nums">{item.replicas}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

export function ImageUpdateResultView({ summary }: { summary?: ImageUpdateSummary | null }) {
  const { language } = useI18n()
  const zh = language === 'zh-CN'
  if (!summary) return <p className="text-sm text-muted-foreground">{zh ? '暂无检测结果' : 'No check results yet'}</p>
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
        <span>{zh ? '模式' : 'Mode'}: {summary.mode === 'auto' ? (zh ? '检测并自动更新' : 'Detect and update') : (zh ? '仅检测' : 'Detect only')}</span>
        {!!summary.updated?.length && <span>· {zh ? `已更新 ${summary.updated.length} 个服务` : `${summary.updated.length} services updated`}</span>}
      </div>
      <ImageUpdateServiceTable services={summary.services} zh={zh} />
    </div>
  )
}
