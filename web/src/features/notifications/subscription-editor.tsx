import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Plus } from 'lucide-react'
import { Button } from '../../components/ui/button'
import { Input } from '../../components/ui/input'
import { LoadingState } from '../../components/ui/loading-state'
import { api } from '../../lib/api'
import { useI18n } from '../../lib/i18n'
import { Check, Choice, ErrorText, Field } from './common'
import { ruleSummary, toggle, useOpsText } from './helpers'
import type { Catalog, Channel, DeliveryPolicy, Rule, Subscription } from './types'

export function SubscriptionEditor({ value, channels, channelID, language, publicURL, onChange, onCreate }: {
 value: Subscription; channels: Channel[]; channelID?: string; language: string; publicURL: string; onChange: (value: Subscription) => void; onCreate: () => void
}) {
 const t = useOpsText(), { language: uiLanguage } = useI18n(), [search, setSearch] = useState('')
 const rules = useQuery({ queryKey: ['notification-rules'], queryFn: () => api<Rule[]>('/notifications/rules') })
 const catalog = useQuery({ queryKey: ['notification-catalog'], queryFn: () => api<Catalog>('/notifications/catalog') })
 const query = search.trim().toLowerCase(), policy = value.policy
 const filtered = (rules.data || []).filter(rule => { const summary = ruleSummary(rule.config,catalog.data,uiLanguage); return `${rule.name} ${summary.events} ${summary.scope}`.toLowerCase().includes(query) })
 const update = <K extends keyof DeliveryPolicy>(key: K, next: DeliveryPolicy[K]) => onChange({ ...value,policy:{ ...policy,[key]:next } })
 const fallback = channels.find(channel => channel.id === policy.fallback_id)
 const missing = value.rule_ids.filter(id => rules.data && !rules.data.some(rule => rule.id === id))
 const sampleType = rules.data?.find(rule => value.rule_ids.includes(rule.id))?.config.events[0] || 'container.oom'
 const entry = catalog.data?.events.find(event => event.type === sampleType)
 const title = entry ? language === 'zh-CN' ? entry.title : entry.title_en : 'Container OOM'
 let time = ''
 try { time = new Intl.DateTimeFormat(language,{ dateStyle:'short',timeStyle:'medium',timeZone:policy.timezone }).format(new Date('2026-10-11T04:00:00Z')) } catch { time = t('请输入有效的 IANA 时区', 'Enter a valid IANA timezone') }
 const variables: Record<string,string> = { title,message:language === 'zh-CN' ? '这是用于预览的事件描述。' : 'This is a sample event description.',node:'Production',resource:'container-example',time,url:`${publicURL.replace(/\/$/,'')}/notifications#example` }
 const template = policy.template || '[error] {{title}}\n{{time}} · {{node}} · {{resource}}\n{{message}}\n{{url}}'
 const clean = template.replace(/{{(title|message|node|resource|time|url)}}/g,'')
 const unsupported = clean.includes('{{') || clean.includes('}}')
 const preview = template.replace(/{{(title|message|node|resource|time|url)}}/g,(_,field: string) => variables[field])
 return <div className="min-w-0 space-y-5">
  <section aria-label={t('订阅规则', 'Subscribed rules')} className="space-y-2">
   <div className="flex items-center justify-between gap-2"><h3 className="text-sm font-medium">{t('订阅规则', 'Subscribed rules')} <span className="font-normal text-muted-foreground">{value.rule_ids.length}</span></h3><Button variant="ghost" size="sm" onClick={onCreate}><Plus className="size-3.5" />{t('新建规则', 'New rule')}</Button></div>
   <p className="text-xs text-muted-foreground">{t('任意一条规则命中即可通知；多条命中同一事件时，此目标只投递一次。不选规则则不自动发送。', 'Any selected rule can trigger a notification. Overlapping rules deliver each event once to this recipient. No selected rules means no automatic notifications.')}</p>
   <Input aria-label={t('搜索可订阅规则', 'Search available rules')} placeholder={t('搜索规则、事件或范围', 'Search rules, events or scope')} value={search} onChange={event => setSearch(event.target.value)} />
   <ErrorText error={rules.error || catalog.error} />
   {rules.isPending ? <LoadingState compact rows={3} label={t('正在加载规则', 'Loading rules')} /> : <div className="max-h-64 divide-y overflow-y-auto rounded-md border px-3">
    {filtered.map(rule => { const summary = ruleSummary(rule.config,catalog.data,uiLanguage); return <div key={rule.id} className="space-y-1 py-2.5"><Check label={rule.name} checked={value.rule_ids.includes(rule.id)} onChange={() => onChange({ ...value,rule_ids:toggle(value.rule_ids,rule.id) })} /><p className="pl-5 text-xs break-words text-muted-foreground">{summary.events}</p><p className="pl-5 text-xs break-words text-muted-foreground">{summary.scope}</p></div> })}
    {!filtered.length && <p className="py-3 text-xs text-muted-foreground">{rules.data?.length ? t('没有匹配的规则。', 'No matching rules.') : t('还没有规则，可以先新建一条。', 'No rules yet. Create one to begin.')}</p>}
    {missing.map(id => <div key={id} className="py-2.5 text-destructive"><Check label={t(`规则已删除：${id}`, `Deleted rule: ${id}`)} checked onChange={() => onChange({ ...value,rule_ids:value.rule_ids.filter(value => value !== id) })} /></div>)}
   </div>}
  </section>
  <section aria-label={t('发送策略', 'Delivery policy')} className="space-y-3 border-t pt-4">
   <h3 className="text-sm font-medium">{t('发送策略', 'Delivery policy')}</h3>
   <Choice label={t('发送频率', 'Frequency')} value={policy.mode} options={[[ 'immediate',t('即时', 'Immediate') ],['merge',t('五分钟合并', 'Merge over 5 minutes') ],['daily',t('每日摘要', 'Daily digest') ]]} onChange={next => update('mode',next as DeliveryPolicy['mode'])} />
   <Field label={t('订阅时区', 'Subscription timezone')} hint={t('同时用于静默、每日摘要和消息中的时间。', 'Used for quiet hours, daily digests and timestamps in messages.')}><Input value={policy.timezone} onChange={event => update('timezone',event.target.value)} /></Field>
   {policy.mode === 'daily' && <Field label={t('摘要小时（0–23）', 'Digest hour (0–23)')}><Input type="number" min={0} max={23} value={policy.digest_hour} onChange={event => update('digest_hour',Number(event.target.value))} /></Field>}
   <details><summary className="cursor-pointer py-1 text-sm select-none">{t('静默与备用投递', 'Quiet hours and fallback')}</summary><div className="space-y-3 pt-3">
    <p className="text-xs text-muted-foreground">{t('固定静默延后普通消息，临时静默跳过新事件的投递。已匹配的 critical 和 CD 待审核事件绕过静默及摘要等待。', 'Quiet hours defer ordinary messages; temporary mutes skip new deliveries. Matching critical events and CD approvals bypass quiet hours and digest delays.')}</p>
    <div className="grid grid-cols-2 gap-3"><Field label={t('静默开始', 'Quiet start')}><Input type="time" value={policy.quiet_start} onChange={event => update('quiet_start',event.target.value)} /></Field><Field label={t('静默结束', 'Quiet end')}><Input type="time" value={policy.quiet_end} onChange={event => update('quiet_end',event.target.value)} /></Field></div>
    <Field label={t('临时静默至', 'Mute until')}><Input type="datetime-local" value={policy.muted_until ? new Date(new Date(policy.muted_until).getTime()-new Date(policy.muted_until).getTimezoneOffset()*60000).toISOString().slice(0,16) : ''} onChange={event => update('muted_until',event.target.value ? new Date(event.target.value).toISOString() : null)} /></Field>
    <Choice label={t('备用渠道', 'Fallback destination')} value={policy.fallback_id || 'none'} options={[[ 'none',t('无', 'None') ],...channels.filter(channel => channel.id !== channelID).map(channel => [channel.id,channel.name] as [string,string])]} onChange={next => onChange({ ...value,policy:{ ...policy,fallback_id:next === 'none' ? '' : next,fallback_chat_id:'' } })} />
    {fallback?.provider === 'feishu_app' && <Choice label={t('备用接收目标', 'Fallback recipient')} value={policy.fallback_chat_id || 'none'} options={[[ 'none',t('选择目标', 'Select recipient') ],...(fallback.config.targets || []).map(target => [target.chat_id,`${target.name} · ${target.chat_id}`] as [string,string])]} onChange={next => update('fallback_chat_id',next === 'none' ? '' : next)} />}
    {policy.fallback_id && <p className="text-xs text-muted-foreground">{t('主目标重试失败后，备用目标即时接收原消息；同一事件自动去重，不再次匹配规则或递归备用。', 'After retries fail, the fallback receives the original message immediately. Events are deduplicated without rematching rules or recursive fallback.')}</p>}
   </div></details>
  </section>
  <details><summary className="cursor-pointer py-1 text-sm select-none">{t('消息模板与预览', 'Message template and preview')}</summary><div className="space-y-3 pt-3">
   <p className="text-xs text-muted-foreground">{t('留空使用默认格式。可修改固定文字、换行和字段顺序；模板只改变正文，不改变触发条件。', 'Leave blank for the default format. Customize fixed text, line breaks and field order. Templates change message text, not triggers.')}</p>
   <Field label={t('消息模板（可选）', 'Message template (optional)')}><textarea className="min-h-32 w-full rounded-md border bg-transparent p-2 font-mono text-xs" value={policy.template} maxLength={4000} onChange={event => update('template',event.target.value)} placeholder="[SUMA] {{title}}" /></Field>
   <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-xs">{[['title',t('事件标题', 'Event title')],['message',t('事件描述', 'Event description')],['node',t('节点名称，可能为空', 'Node name; may be empty')],['resource',t('资源 ID，可能为空', 'Resource ID; may be empty')],['time',t('事件时间，使用订阅时区', 'Event time in the subscription timezone')],['url',t('事件详情链接', 'Event detail link')]].map(([field,label]) => <div key={field} className="contents"><dt className="font-mono text-muted-foreground">{`{{${field}}}`}</dt><dd className="min-w-0 break-words">{label}</dd></div>)}</dl>
   {unsupported && <ErrorText error={t('模板包含不支持的占位符，仅支持上面六个字段。', 'The template contains unsupported placeholders. Only the six fields above are supported.')} />}
   <p className="text-xs text-muted-foreground">{t('示例预览，不会发送通知', 'Sample preview; no notification is sent')}</p><pre aria-label={t('消息预览', 'Message preview')} className="overflow-x-auto rounded-md bg-muted p-3 text-xs whitespace-pre-wrap break-words">{preview}</pre>
  </div></details>
 </div>
}
