import { MoreHorizontal, Send } from 'lucide-react'
import { Button } from '../../components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from '../../components/ui/dropdown-menu'
import { ListShell } from '../../components/ui/list-shell'
import { ListPagination } from '../../components/ui/list-pagination'
import { StatusBadge } from '../../components/ui/status-badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../components/ui/table'
import { useListPagination } from '../../components/ui/use-list-pagination'
import { useI18n } from '../../lib/i18n'
import { useDateTime } from '../../lib/time-zone'
import { EmptyState } from '../../pages/images'
import { channelPlatforms, useOpsText } from './helpers'
import type { Channel } from './types'

export function ChannelList({ channels, search, pending, onEdit, onTest, onToggle, onRemove }: {
  channels: Channel[]
  search: string
  pending: boolean
  onEdit: (channel: Channel) => void
  onTest: (channel: Channel) => void
  onToggle: (channel: Channel) => void
  onRemove: (channel: Channel) => void
}) {
  const t = useOpsText(), { language } = useI18n(), { formatDateTime } = useDateTime()
  const zh = language === 'zh-CN', platforms = channelPlatforms(language)
  const query = search.trim().toLowerCase()
  const platformName = (channel: Channel) => platforms.find(([id]) => id === channel.provider)?.[1] || channel.provider
  const filtered = channels.filter(channel => `${channel.name} ${channel.provider} ${platformName(channel)}`.toLowerCase().includes(query))
  const pagination = useListPagination(filtered, query)

  if (filtered.length === 0) return <EmptyState
    icon={<Send />}
    title={channels.length === 0 ? t('暂无通知渠道', 'No notification channels') : t('暂无匹配的通知渠道', 'No matching notification channels')}
    detail={channels.length === 0 ? t('添加第一个渠道，接收重要事件。', 'Add your first channel to receive important events.') : t('尝试其他渠道名称或平台。', 'Try another channel name or platform.')}
  />

  return <>
    <ListShell>
      <Table aria-label={t('通知渠道列表', 'Notification channels')}>
        <TableHeader>
          <TableRow>
            <TableHead className="min-w-48 pl-3">{t('渠道', 'Channel')}</TableHead>
            <TableHead className="w-44">{t('平台', 'Platform')}</TableHead>
            <TableHead className="w-28">{t('状态', 'Status')}</TableHead>
            <TableHead className="min-w-44">{t('最近投递', 'Last delivery')}</TableHead>
            <TableHead className="w-16 pr-3 text-right">{t('操作', 'Actions')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {pagination.items.map(channel => <TableRow key={channel.id}>
            <TableCell className="max-w-72 pl-3 whitespace-normal">
              <button type="button" onClick={() => onEdit(channel)} className="-ml-2 flex max-w-full items-center gap-2.5 rounded-md px-2 py-1 text-left outline-none transition-colors focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50">
                <Send className="size-[18px] shrink-0 text-muted-foreground" />
                <span className="truncate font-medium">{channel.name}</span>
              </button>
            </TableCell>
            <TableCell className="text-muted-foreground">{platformName(channel)}</TableCell>
            <TableCell><StatusBadge tone={channel.enabled ? 'success' : 'neutral'}>{channel.enabled ? t('启用', 'Enabled') : t('停用', 'Paused')}</StatusBadge></TableCell>
            <TableCell className="max-w-xs whitespace-normal">
              <span className="text-sm text-muted-foreground">{channel.last_sent_at ? formatDateTime(channel.last_sent_at) : t('尚未发送', 'No deliveries yet')}</span>
              {channel.last_error && <p className="mt-0.5 text-xs break-words text-destructive">{channel.last_error}</p>}
            </TableCell>
            <TableCell className="pr-3 text-right">
              <DropdownMenu>
                <DropdownMenuTrigger render={<Button variant="ghost" size="icon-sm" disabled={pending} aria-label={t(`渠道操作 ${channel.name}`, `Channel actions ${channel.name}`)}><MoreHorizontal /></Button>} />
                <DropdownMenuContent align="end">
                  <DropdownMenuItem onClick={() => onEdit(channel)}>{t('编辑', 'Edit')}</DropdownMenuItem>
                  <DropdownMenuItem onClick={() => onTest(channel)}>{t('发送测试', 'Send test')}</DropdownMenuItem>
                  <DropdownMenuItem onClick={() => onToggle(channel)}>{channel.enabled ? t('暂停', 'Pause') : t('启用', 'Enable')}</DropdownMenuItem>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem variant="destructive" onClick={() => onRemove(channel)}>{t('删除', 'Delete')}</DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            </TableCell>
          </TableRow>)}
        </TableBody>
      </Table>
    </ListShell>
    <ListPagination {...pagination} zh={zh} />
  </>
}
