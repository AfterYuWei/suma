import { ListFilter, MoreHorizontal } from 'lucide-react'
import { Button } from '../../components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from '../../components/ui/dropdown-menu'
import { ListShell } from '../../components/ui/list-shell'
import { ListPagination } from '../../components/ui/list-pagination'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../components/ui/table'
import { useListPagination } from '../../components/ui/use-list-pagination'
import { useI18n } from '../../lib/i18n'
import { EmptyState } from '../../pages/images'
import { useOpsText } from './helpers'
import type { Catalog, Channel, Rule } from './types'

export function RuleList({ rules, channels, catalog, search, pending, onEdit, onSubscription, onRemove }: {
 rules: Rule[]; channels: Channel[]; catalog?: Catalog; search: string; pending: boolean
 onEdit: (rule: Rule) => void; onSubscription: (channel: Channel) => void; onRemove: (rule: Rule) => void
}) {
 const t = useOpsText(), { language } = useI18n(), zh = language === 'zh-CN'
 const destinations = (rule: Rule) => channels.flatMap(channel => {
  const locations: {channel:Channel;label:string}[] = []
  if (channel.config.subscription?.rule_ids.includes(rule.id)) locations.push({channel,label:`${channel.name} · ${t('全部对话', 'All conversations')}`})
  for (const target of channel.config.targets || []) { if (target.subscription_override?.rule_ids.includes(rule.id)) locations.push({channel,label:`${channel.name} · ${target.name}`}) }
  return locations
 })
 const eventNames = (rule: Rule) => rule.config.events.map(type => { const entry = catalog?.events.find(event => event.type === type); return entry ? t(entry.title, entry.title_en) : type }).join('、')
 const scope = (rule: Rule) => [
  rule.config.node_ids?.length || rule.config.group_ids?.length ? [rule.config.node_ids?.length ? t(`${rule.config.node_ids.length} 个节点`, `${rule.config.node_ids.length} nodes`) : '', rule.config.group_ids?.length ? t(`${rule.config.group_ids.length} 个节点组`, `${rule.config.group_ids.length} groups`) : ''].filter(Boolean).join(' / ') : t('不限节点范围', 'Any node scope'),
  rule.config.projects?.length ? t(`${rule.config.projects.length} 个项目`, `${rule.config.projects.length} projects`) : '',
 ].filter(Boolean).join(' · ')
 const query = search.trim().toLowerCase()
 const filtered = rules.filter(rule => `${rule.name} ${rule.config.events.join(' ')} ${eventNames(rule)} ${destinations(rule).map(value => value.label).join(' ')} ${(rule.config.projects || []).join(' ')}`.toLowerCase().includes(query))
 const pagination = useListPagination(filtered, query)
 if (!filtered.length) return <EmptyState icon={<ListFilter />} title={rules.length ? t('暂无匹配的通知规则', 'No matching notification rules') : t('暂无通知规则', 'No notification rules')} detail={rules.length ? t('尝试其他规则名称、事件或渠道。', 'Try another rule name, event or channel.') : t('使用预设创建规则，也可以自选事件。', 'Start with a preset or choose your own events.')} />
 return <>
  <ListShell><Table aria-label={t('通知规则列表', 'Notification rules')}>
   <TableHeader><TableRow><TableHead className="min-w-48 pl-3">{t('规则', 'Rule')}</TableHead><TableHead className="min-w-44">{t('事件与范围', 'Events and scope')}</TableHead><TableHead className="min-w-44">{t('订阅位置', 'Subscriptions')}</TableHead><TableHead className="w-16 pr-3 text-right">{t('操作', 'Actions')}</TableHead></TableRow></TableHeader>
   <TableBody>{pagination.items.map(rule => <TableRow key={rule.id}>
    <TableCell className="max-w-72 pl-3 whitespace-normal"><button type="button" className="-ml-2 flex max-w-full items-center gap-2.5 rounded-md px-2 py-1 text-left outline-none focus-visible:ring-3 focus-visible:ring-ring/50" onClick={() => onEdit(rule)}><ListFilter className="size-[18px] shrink-0 text-muted-foreground" /><span className="truncate font-medium" title={rule.name}>{rule.name}</span></button></TableCell>
    <TableCell className="max-w-72 whitespace-normal"><span title={eventNames(rule)}>{t(`${rule.config.events.length} 种事件`, `${rule.config.events.length} events`)}</span><p className="mt-0.5 text-xs text-muted-foreground">{scope(rule)}</p></TableCell>
    <TableCell className="max-w-72 whitespace-normal">{destinations(rule).length ? <details><summary className="cursor-pointer text-sm text-muted-foreground select-none">{t(`${destinations(rule).length} 个订阅位置`, `${destinations(rule).length} subscriptions`)}</summary><div className="space-y-1 pt-1">{destinations(rule).map((location,index) => <button key={index} type="button" className="block w-full truncate text-left text-xs underline-offset-2 hover:underline" title={location.label} onClick={() => onSubscription(location.channel)}>{location.label}</button>)}</div></details> : <span className="text-xs text-muted-foreground">{t('尚未被订阅', 'No subscriptions')}</span>}</TableCell>
    <TableCell className="pr-3 text-right"><DropdownMenu><DropdownMenuTrigger render={<Button variant="ghost" size="icon-sm" disabled={pending} aria-label={t(`规则操作 ${rule.name}`, `Rule actions ${rule.name}`)}><MoreHorizontal /></Button>} /><DropdownMenuContent align="end"><DropdownMenuItem onClick={() => onEdit(rule)}>{t('编辑', 'Edit')}</DropdownMenuItem><DropdownMenuSeparator /><DropdownMenuItem variant="destructive" onClick={() => onRemove(rule)}>{t('删除', 'Delete')}</DropdownMenuItem></DropdownMenuContent></DropdownMenu></TableCell>
   </TableRow>)}</TableBody>
  </Table></ListShell>
  <ListPagination {...pagination} zh={zh} />
 </>
}
