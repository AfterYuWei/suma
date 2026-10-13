import { useEffect, useState } from 'react'
import { useLocation } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Plus, Search, X } from 'lucide-react'
import { Button } from '../../components/ui/button'
import { Input } from '../../components/ui/input'
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetDescription } from '../../components/ui/sheet'
import { Tabs, TabsList, TabsTrigger } from '../../components/ui/tabs'
import { ErrorState } from '../../components/ui/error-state'
import { LoadingState } from '../../components/ui/loading-state'
import { api } from '../../lib/api'
import { confirmDialog } from '../../stores/dialog'
import { ErrorText } from './common'
import { useOpsText } from './helpers'
import { RuleEditor } from './rule-editor'
import { ChannelEditor } from './channel-editor'
import { ChannelList } from './channel-list'
import { DeliveryList } from './delivery-list'
import { RuleList } from './rule-list'
import type { Catalog, Channel, Delivery, Rule } from './types'

export function NotificationSettings() {
 const t = useOpsText(), client = useQueryClient()
 const hash = useLocation({ select: location => location.hash }), requestedTab = new URLSearchParams(hash.split('?')[1] || '').get('tab'), initialTab = requestedTab === 'rules' || requestedTab === 'deliveries' ? requestedTab : 'channels'
 const [channelSearch, setChannelSearch] = useState(''), [ruleSearch, setRuleSearch] = useState(''), [deliverySearch, setDeliverySearch] = useState('')
 const [channelView, setChannelView] = useState<'connection' | 'subscriptions'>('connection')
 const [tab, setTab] = useState(initialTab), [editing, setEditing] = useState<Channel | 'new' | null>(null), [ruleEdit, setRuleEdit] = useState<Rule | 'new' | null>(null)
 useEffect(() => setTab(initialTab), [initialTab])
 const channels = useQuery({ queryKey: ['notification-channels'], queryFn: () => api<Channel[]>('/notifications/channels'), refetchInterval: 10000 })
 const rules = useQuery({ queryKey: ['notification-rules'], queryFn: () => api<Rule[]>('/notifications/rules') })
 const deliveries = useQuery({ queryKey: ['notification-deliveries'], queryFn: () => api<Delivery[]>('/notifications/deliveries'), enabled: tab === 'deliveries', refetchInterval: 5000 })
 const catalog = useQuery({ queryKey: ['notification-catalog'], queryFn: () => api<Catalog>('/notifications/catalog'), enabled: tab === 'rules' })
 const search = tab === 'channels' ? channelSearch : tab === 'rules' ? ruleSearch : deliverySearch
 const setSearch = tab === 'channels' ? setChannelSearch : tab === 'rules' ? setRuleSearch : setDeliverySearch
 const searchLabel = tab === 'channels' ? t('搜索通知渠道', 'Search notification channels') : tab === 'rules' ? t('搜索通知规则', 'Search notification rules') : t('搜索发送记录', 'Search notification deliveries')
 const placeholder = tab === 'channels' ? t('搜索渠道或平台', 'Search channels or platforms') : tab === 'rules' ? t('搜索规则、事件或渠道', 'Search rules, events or channels') : t('搜索渠道、目标、规则或状态', 'Search channels, recipients, rules or status')
 const listPending = channels.isPending || tab === 'rules' && (rules.isPending || catalog.isPending) || tab === 'deliveries' && deliveries.isPending
 const listError = channels.error || (tab === 'rules' ? rules.error || catalog.error : tab === 'deliveries' ? deliveries.error : undefined)
 const action = useMutation({ mutationFn: ({ path, method = 'POST', body }: { path: string; method?: string; body?: unknown }) => api(path, { method, body: body ? JSON.stringify(body) : undefined }), onSuccess: () => { void client.invalidateQueries({ queryKey: ['notification-channels'] }); void client.invalidateQueries({ queryKey: ['notification-rules'] }); void client.invalidateQueries({ queryKey: ['notification-deliveries'] }) } })
 const remove = async (path: string, name: string) => { if (await confirmDialog({ title: t(`删除 ${name}？`, `Delete ${name}?`), description: path.includes('/channels/') ? t('待发送消息将停止投递，已发现的接收会话将移除。', 'Pending deliveries stop and discovered recipient chats are removed.') : t('解除此规则的所有订阅。已排队消息保留原配置继续发送。', 'Remove all subscriptions to this rule. Queued messages keep their original configuration.'), danger: true, confirmLabel: t('删除', 'Delete') })) action.mutate({ path, method: 'DELETE' }) }
 return <section className="space-y-4">
  <div className="flex flex-wrap items-center gap-3">
   <Tabs value={tab} onValueChange={setTab}><TabsList><TabsTrigger value="channels">{t('渠道', 'Channels')}</TabsTrigger><TabsTrigger value="rules">{t('通知规则', 'Rules')}</TabsTrigger><TabsTrigger value="deliveries">{t('发送记录', 'Deliveries')}</TabsTrigger></TabsList></Tabs>
   <div className="ml-auto flex w-full items-center justify-end gap-2 sm:w-auto">
    <div className="relative min-w-0 flex-1 sm:w-64">
     <Search className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
     <Input value={search} onChange={event => setSearch(event.target.value)} aria-label={searchLabel} placeholder={placeholder} className="pr-8 pl-8" />
     {search && <Button variant="ghost" size="icon-xs" aria-label={t('清除搜索', 'Clear search')} className="absolute top-1/2 right-0.5 -translate-y-1/2 text-muted-foreground" onClick={() => setSearch('')}><X className="size-3.5" /></Button>}
    </div>
    {tab !== 'deliveries' && <Button size="sm" onClick={() => tab === 'channels' ? (setChannelView('connection'), setEditing('new')) : setRuleEdit('new')}><Plus data-icon="inline-start" />{tab === 'channels' ? t('添加通知渠道', 'Add channel') : t('添加规则', 'Add rule')}</Button>}
   </div>
  </div>
  <p className="text-sm text-muted-foreground">{t('站内事件始终保留。在渠道中订阅规则；同一事件在同一接收目标自动去重。', 'Events remain available in your inbox. Subscribe to rules in each channel. Overlapping rules deliver each event once per recipient.')}</p>
  <ErrorText error={action.error} />
  {listPending ? <LoadingState compact rows={4} label={t('正在加载通知列表', 'Loading notifications')} /> : listError ? <ErrorState description={listError.message} /> : <>
  {tab === 'channels' && <ChannelList channels={channels.data || []} search={search} pending={action.isPending} onEdit={row => { setChannelView('connection'); setEditing(row) }} onTest={row => row.provider === 'feishu_app' ? (setChannelView('connection'), setEditing(row)) : action.mutate({ path: `/notifications/channels/${row.id}/test` })} onToggle={row => action.mutate({ path: `/notifications/channels/${row.id}`, method: 'PUT', body: { name: row.name, provider: row.provider, enabled: !row.enabled, version: row.version, config: row.config } })} onRemove={row => { void remove(`/notifications/channels/${row.id}`, row.name) }} />}
  {tab === 'rules' && <RuleList rules={rules.data || []} channels={channels.data || []} catalog={catalog.data} search={search} pending={action.isPending} onEdit={setRuleEdit} onSubscription={channel => { setChannelView('subscriptions'); setEditing(channel) }} onRemove={row => { void remove(`/notifications/rules/${row.id}`, row.name) }} />}
  {tab === 'deliveries' && <DeliveryList deliveries={deliveries.data || []} channels={channels.data || []} search={search} pending={action.isPending} onResend={row => action.mutate({ path: `/notifications/deliveries/${row.id}/resend` })} />}
  </>}
  <Sheet open={editing !== null} onOpenChange={open => { if (!open) setEditing(null) }}><SheetContent className="w-full gap-0 data-[side=right]:w-full sm:max-w-xl"><SheetHeader><SheetTitle>{t('配置通知渠道', 'Configure channel')}</SheetTitle><SheetDescription>{t('配置凭证、接收目标和通知订阅。', 'Configure credentials, recipients and notification subscriptions.')}</SheetDescription></SheetHeader>{editing && <ChannelEditor key={editing === 'new' ? 'new' : editing.id} row={editing === 'new' ? undefined : editing} onDone={() => setEditing(null)} initialView={channelView} />}</SheetContent></Sheet>
  <Sheet open={ruleEdit !== null} onOpenChange={open => { if (!open) setRuleEdit(null) }}><SheetContent className="w-full gap-0 data-[side=right]:w-full sm:max-w-xl"><SheetHeader><SheetTitle>{t('通知规则', 'Notification rule')}</SheetTitle><SheetDescription>{t('选择触发事件和筛选范围，可由多个渠道复用', 'Choose events and scope. Multiple channels can subscribe to this rule.')}</SheetDescription></SheetHeader>{ruleEdit && <RuleEditor key={ruleEdit === 'new' ? 'new' : ruleEdit.id} row={ruleEdit === 'new' ? undefined : ruleEdit} onDone={() => setRuleEdit(null)} />}</SheetContent></Sheet>
 </section>
}
