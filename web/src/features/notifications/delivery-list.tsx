import { History, MoreHorizontal } from 'lucide-react'
import { Button } from '../../components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '../../components/ui/dropdown-menu'
import { ListShell } from '../../components/ui/list-shell'
import { ListPagination } from '../../components/ui/list-pagination'
import { StatusBadge, type StatusTone } from '../../components/ui/status-badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../components/ui/table'
import { useListPagination } from '../../components/ui/use-list-pagination'
import { useI18n } from '../../lib/i18n'
import { useDateTime } from '../../lib/time-zone'
import { EmptyState } from '../../pages/images'
import { useOpsText } from './helpers'
import type { Channel, Delivery } from './types'

export function DeliveryList({ deliveries, channels, search, pending, onResend }: {
 deliveries: Delivery[]; channels: Channel[]; search: string; pending: boolean; onResend: (delivery: Delivery) => void
}) {
 const t = useOpsText(), { language } = useI18n(), { formatDateTime } = useDateTime()
 const labels: Record<string, string> = { pending: t('待发送', 'Pending'), sending: t('发送中', 'Sending'), retry: t('等待重试', 'Retrying'), sent: t('已发送', 'Sent'), failed: t('发送失败', 'Failed'), suppressed: t('未发送', 'Suppressed') }
 const tones: Record<string, StatusTone> = { pending: 'neutral', sending: 'warning', retry: 'warning', sent: 'success', failed: 'critical', suppressed: 'neutral' }
 const reasons: Record<string, string> = { 'quiet hours': t('静默时段，延后发送', 'Deferred during quiet hours'), 'temporarily muted': t('临时静默，跳过投递', 'Skipped while temporarily muted'), 'manual resend': t('手动重发', 'Manual resend'), 'channel disabled or deleted': t('渠道已停用或删除', 'Channel paused or deleted'), 'channel deleted': t('渠道已删除', 'Channel deleted'), 'channel paused or destination changed':t('渠道已暂停或目标已更换', 'Channel paused or destination changed'), 'recipient removed or destination changed':t('目标已移除或更换', 'Recipient removed or changed'), 'fallback delivery':t('备用投递', 'Fallback delivery'), 'recipient removed or application changed': t('目标已移除或应用已更换', 'Recipient removed or application changed'), 'rule recipient removed': t('规则中的接收目标已移除', 'Recipient removed from rule') }
 const channelName = (row: Delivery) => channels.find(channel => channel.id === row.channel_id)?.name || t('渠道已删除', 'Deleted channel')
 const recipientName = (row: Delivery) => channels.find(channel => channel.id === row.channel_id)?.config.targets?.find(target => target.chat_id === row.chat_id)?.name || row.chat_id || ''
 const ruleName = (row: Delivery) => row.matched_rules?.length ? row.matched_rules.map(rule => rule.name).join('、') : t('未关联规则', 'No linked rule')
 const ruleCount = (row: Delivery) => new Set((row.matched_rules || []).map(rule => rule.id)).size
 const sourceName = (row: Delivery) => ({event:t('事件通知', 'Event notification'),fallback:t('备用投递', 'Fallback'),manual:t('手动重发', 'Manual resend'),test:t('测试消息', 'Test message')})[row.source] || ''
 const reason = (row: Delivery) => { const value = row.reason || ''; return reasons[value] || value }
 const query = search.trim().toLowerCase()
 const filtered = deliveries.filter(row => `${row.id} ${row.channel_id} ${row.chat_id || ''} ${row.matched_rules?.map(rule => rule.id).join(' ') || ''} ${channelName(row)} ${recipientName(row)} ${ruleName(row)} ${sourceName(row)} ${row.origin_delivery_id || ''} ${row.status} ${labels[row.status] || ''} ${reason(row)} ${row.reason || ''}`.toLowerCase().includes(query))
 const pagination = useListPagination(filtered, query)
 if (!filtered.length) return <EmptyState icon={<History />} title={deliveries.length ? t('暂无匹配的发送记录', 'No matching deliveries') : t('暂无发送记录', 'No deliveries')} detail={deliveries.length ? t('尝试其他渠道、接收目标、规则或状态。', 'Try another channel, recipient, rule or status.') : t('发送测试消息或事件通知后，投递结果会显示在这里。', 'Delivery results appear after sending a test or an event notification.')} />
 return <>
  <ListShell><Table aria-label={t('发送记录列表', 'Notification deliveries')}>
   <TableHeader><TableRow><TableHead className="min-w-48 pl-3">{t('投递目标', 'Destination')}</TableHead><TableHead className="min-w-36">{t('关联规则', 'Rule')}</TableHead><TableHead className="min-w-48">{t('投递状态', 'Delivery status')}</TableHead><TableHead className="min-w-48">{t('记录时间', 'Recorded')}</TableHead><TableHead className="w-16 pr-3 text-right">{t('操作', 'Actions')}</TableHead></TableRow></TableHeader>
   <TableBody>{pagination.items.map(row => <TableRow key={row.id}>
    <TableCell className="max-w-72 pl-3 whitespace-normal"><p className="truncate font-medium" title={channelName(row)}>{channelName(row)}</p>{row.chat_id && <p className="mt-0.5 truncate text-xs text-muted-foreground" title={`${recipientName(row)} · ${row.chat_id}`}>{recipientName(row)}</p>}</TableCell>
    <TableCell className="max-w-56 whitespace-normal">{row.matched_rules?.length > 1 ? <details><summary className="cursor-pointer text-sm text-muted-foreground select-none">{t(`命中 ${ruleCount(row)} 条规则`, `${ruleCount(row)} matching rules`)}</summary><ul className="space-y-1 pt-1 text-xs text-muted-foreground">{row.matched_rules.map(rule => <li key={`${rule.id}-${rule.version}`} title={`${rule.id} · v${rule.version}`} className="break-words">{rule.name} <span className="text-muted-foreground">v{rule.version}</span></li>)}</ul></details> : <span className="block truncate text-muted-foreground" title={ruleName(row)}>{ruleName(row)}</span>}<p className="mt-1 text-xs text-muted-foreground" title={row.origin_delivery_id}>{sourceName(row)}{row.event_count > 1 ? t(` · ${row.event_count} 个事件`, ` · ${row.event_count} events`) : ''}</p></TableCell>
    <TableCell className="max-w-80 whitespace-normal"><StatusBadge tone={tones[row.status] || 'neutral'}>{labels[row.status] || row.status}</StatusBadge><span className="ml-2 text-xs text-muted-foreground">{t(`${row.attempts} 次尝试`, `${row.attempts} attempts`)}</span>{row.reason && <p className={`mt-1 text-xs break-words ${row.status === 'failed' || row.status === 'retry' ? 'text-destructive' : 'text-muted-foreground'}`}>{reason(row)}</p>}</TableCell>
    <TableCell className="whitespace-normal text-xs text-muted-foreground"><span>{formatDateTime(row.created_at)}</span>{['pending', 'retry'].includes(row.status) && <p className="mt-1">{t('计划发送', 'Scheduled')} · {formatDateTime(row.due_at)}</p>}</TableCell>
    <TableCell className="pr-3 text-right"><DropdownMenu><DropdownMenuTrigger render={<Button variant="ghost" size="icon-sm" disabled={pending || !['failed', 'suppressed', 'sent'].includes(row.status)} aria-label={t(`投递操作 ${row.id}`, `Delivery actions ${row.id}`)}><MoreHorizontal /></Button>} /><DropdownMenuContent align="end"><DropdownMenuItem onClick={() => onResend(row)}>{t('重发', 'Resend')}</DropdownMenuItem></DropdownMenuContent></DropdownMenu></TableCell>
   </TableRow>)}</TableBody>
  </Table></ListShell>
  <ListPagination {...pagination} zh={language === 'zh-CN'} />
  {deliveries.length >= 200 && <p className="pt-2 text-xs text-muted-foreground">{t('搜索与分页覆盖最近 200 条发送记录。', 'Search and pagination cover the latest 200 deliveries.')}</p>}
 </>
}
