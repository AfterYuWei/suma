import { useEffect, useId, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Check as CheckIcon, ChevronDown, ChevronRight, Copy, Pencil, Send } from 'lucide-react'
import { Button } from '../../components/ui/button'
import { Input } from '../../components/ui/input'
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '../../components/ui/select'
import { Tabs, TabsList, TabsTrigger } from '../../components/ui/tabs'
import { RuleEditor } from './rule-editor'
import { SubscriptionEditor } from './subscription-editor'
import { Switch } from '../../components/ui/switch'
import { Tooltip, TooltipContent, TooltipTrigger } from '../../components/ui/tooltip'
import { api, ApiError } from '../../lib/api'
import { useI18n } from '../../lib/i18n'
import { useDateTime } from '../../lib/time-zone'
import { Check, Choice, ErrorText, Field } from './common'
import { channelPlatforms, defaultSubscription, normalizeSubscription, useOpsText } from './helpers'
import type { Channel, ChannelConfig, ChannelTarget, ConnectionStatus, Rule, Subscription } from './types'

const newConfig = (language: string, timezone: string): ChannelConfig => ({ endpoint: '', chat_id: '', app_id: '', targets: [], language, subscription: defaultSubscription(timezone), allow_private: false, auto_discover: false, public_url: '' })
type ChannelAction = { kind: 'save' | 'check' | 'test' | 'done'; chatID?: string }
type Chat = { chat_id: string; name: string; private: boolean }
class ChannelActionError extends Error {
 constructor(readonly stage: ChannelAction['kind'], message: string) { super(message) }
}

export function ChannelEditor({ row, onDone, initialView = 'connection' }: { row?: Channel; onDone: () => void; initialView?: 'connection' | 'subscriptions' }) {
 const t = useOpsText(), { language } = useI18n(), { timeZone, formatDateTime } = useDateTime(), client = useQueryClient()
 const enabledID = useId()
 const [view, setView] = useState<string>(initialView), [subscriptionTarget, setSubscriptionTarget] = useState<string | null>(null), [creatingRule, setCreatingRule] = useState(false)
 const channels = useQuery({ queryKey: ['notification-channels'], queryFn: () => api<Channel[]>('/notifications/channels') })
 const normalize = (config?: ChannelConfig) => ({ ...newConfig(language, timeZone), ...config, subscription: normalizeSubscription(config?.subscription,timeZone), targets: (config?.targets || []).map(target => ({ ...target, ...(target.subscription_override ? { subscription_override:normalizeSubscription(target.subscription_override,timeZone) } : {}) })) })
 const [provider, setProvider] = useState(row?.provider || 'feishu_app'), [name, setName] = useState(row?.name || t('飞书机器人', 'Feishu bot'))
 const [enabled, setEnabled] = useState(row?.enabled ?? true), [cfg, setCfg] = useState<ChannelConfig>(normalize(row?.config)), [saved, setSaved] = useState(row)
 const [secret, setSecret] = useState(''), [signing, setSigning] = useState(''), [authorization, setAuthorization] = useState('')
 const [manualID, setManualID] = useState(''), [manualName, setManualName] = useState(''), [testRecipient, setTestRecipient] = useState('')
 const [credentialMessage, setCredentialMessage] = useState(''), [testResults, setTestResults] = useState<Record<string, { success: boolean; message: string }>>({})
 const isFeishu = provider === 'feishu_app', isBot = isFeishu || provider === 'telegram', targets = cfg.targets || []
 const identityChanged = !!saved && saved.config.app_id !== cfg.app_id
 const dirty = !saved || !!(secret || signing || authorization) || name !== saved.name || enabled !== saved.enabled || JSON.stringify(cfg) !== JSON.stringify(normalize(saved.config))
 const selectedTest = targets.some(target => target.chat_id === testRecipient) ? testRecipient : targets[0]?.chat_id || ''
 const action = useMutation({
  mutationFn: async ({ kind, chatID }: ChannelAction) => {
   let stage: ChannelAction['kind'] = 'save'
   try {
    let current = saved
    if (dirty || !current) {
     current = await api<Channel>(`/notifications/channels${current ? `/${current.id}` : ''}`, { method: current ? 'PUT' : 'POST', body: JSON.stringify({ name, provider, enabled, version: current?.version || 0, config: cfg, secrets: { token: secret, signing_key: signing, authorization } }) })
     setSaved(current); setCfg(normalize(current.config)); setSecret(''); setSigning(''); setAuthorization('')
     void client.invalidateQueries({ queryKey: ['notification-channels'] }); void client.invalidateQueries({ queryKey: ['notification-chats', current.id] }); void client.invalidateQueries({ queryKey: ['notification-connection', current.id] })
    }
    stage = kind
    if (kind === 'check') {
     const result = await api<{ credentials_valid: boolean }>(`/notifications/channels/${current.id}/check`, { method: 'POST' })
     if (!result.credentials_valid) throw new Error(t('凭证校验未通过', 'Credentials were not verified'))
    }
    if (kind === 'test') {
     if (isFeishu && !current.config.targets?.some(target => target.chat_id === chatID)) throw new Error(t('请选择已配置的测试目标', 'Select a configured test recipient'))
     await api(`/notifications/channels/${current.id}/test`, { method: 'POST', body: JSON.stringify(isFeishu ? { chat_id: chatID } : {}) })
    }
    return current
   } catch (error) {
    const message = error instanceof ApiError && error.code === 20802 ? t('数据库结构与当前 SUMA 版本不兼容，请使用此版本的全新 PostgreSQL 数据库。', 'The database schema is incompatible with this SUMA version. Use a fresh PostgreSQL database initialized by this version.') : error instanceof Error ? error.message : String(error)
    throw new ChannelActionError(stage, message)
   }
  },
  onMutate: ({ kind, chatID }) => {
   if (kind === 'test') setTestResults(previous => { const next = { ...previous }; delete next[chatID || 'default']; return next })
  },
  onSuccess: (_channel, { kind, chatID }) => {
   if (kind === 'check') setCredentialMessage(t('凭证有效；连接和消息发送需分别验证。', 'Credentials verified. Connection and delivery are verified separately.'))
   if (kind === 'test') setTestResults(previous => ({ ...previous, [chatID || 'default']: { success: true, message: t('测试消息已发送', 'Test message sent') } }))
   if (kind === 'done') onDone()
  },
  onError: (error, { kind, chatID }) => {
   if (kind === 'check') setCredentialMessage('')
   if (kind === 'test') {
    if (error instanceof ChannelActionError && error.stage === 'save') setTestResults(previous => { const next = { ...previous }; delete next[chatID || 'default']; return next })
    else setTestResults(previous => ({ ...previous, [chatID || 'default']: { success: false, message: error.message } }))
   }
  },
 })
 const update = <K extends keyof ChannelConfig>(key: K, value: ChannelConfig[K]) => {
  action.reset(); setCfg(previous => ({ ...previous, [key]: value, ...(key === 'app_id' ? { targets: [] } : {}) }))
  if (key === 'app_id') { setCredentialMessage(''); setTestResults({}); setTestRecipient('') }
 }
 const chats = useQuery({ queryKey: ['notification-chats', saved?.id], queryFn: () => api<Chat[]>(`/notifications/channels/${saved?.id}/chats`), enabled: !!saved && !identityChanged && (isFeishu || cfg.auto_discover), refetchInterval: 2000 })
 const connection = useQuery({ queryKey: ['notification-connection', saved?.id], queryFn: () => api<ConnectionStatus>(`/notifications/channels/${saved?.id}/connection`), enabled: !!saved && isFeishu && !identityChanged, refetchInterval: 2000 })
 const labels: Record<ConnectionStatus['state'], string> = { stopped: t('未连接', 'Stopped'), connecting: t('连接中', 'Connecting'), connected: t('已连接', 'Connected'), reconnecting: t('重新连接中', 'Reconnecting'), error: t('连接失败', 'Connection failed') }
 const toggleTarget = (target: ChannelTarget) => {
  update('targets', targets.some(item => item.chat_id === target.chat_id) ? targets.filter(item => item.chat_id !== target.chat_id) : [...targets, target])
  setTestResults(previous => { const next = { ...previous }; delete next[target.chat_id]; return next })
 }
 const discovered = (chats.data || []).filter(chat => !targets.some(target => target.chat_id === chat.chat_id))
 const canSave = !!name.trim() && (!isBot || (isFeishu ? !!cfg.app_id.trim() : true) && !!(secret.trim() || saved?.has_secrets))
 const testResult = testResults[isFeishu ? selectedTest : 'default']
 const recipientName = (target: ChannelTarget) => {
  const value = target.name.trim()
  if (!value || value === target.chat_id) return t('未命名会话', 'Unnamed conversation')
  return ['私聊', '群聊', 'Private chat', 'Group chat'].find(prefix => value === `${prefix} · ${target.chat_id}`) || value
 }
 const testRecipientName = (target: ChannelTarget) => {
  const label = recipientName(target)
  return targets.some(other => other.chat_id !== target.chat_id && recipientName(other) === label) ? `${label} · ${target.chat_id}` : label
 }
 const selectedTarget = targets.find(target => target.chat_id === selectedTest)
 const recipientRows: { target: ChannelTarget; name: string; checked: boolean }[] = [
  ...targets.map(target => ({ target, name: recipientName(target), checked: true })),
  ...discovered.map(chat => {
   const kind = chat.private ? t('私聊', 'Private chat') : t('群聊', 'Group chat'), name = chat.name && chat.name !== chat.chat_id ? `${kind} · ${chat.name}` : kind
   return { target: { chat_id: chat.chat_id, name }, name, checked: false }
  }),
 ]
 const testControls = <div role="group" aria-label={t('测试通知', 'Test notification')} className="min-w-0 space-y-2">
  <div className={isFeishu && targets.length > 1 ? 'grid min-w-0 items-end gap-2 sm:grid-cols-[minmax(0,1fr)_auto]' : 'flex min-w-0 items-end gap-2'}>
   {isFeishu && <div className="min-w-0 flex-1 space-y-1">
    <span className="text-xs text-muted-foreground">{t('测试接收目标', 'Test recipient')}</span>
    {targets.length === 1 ? <p className="truncate text-sm" title={recipientName(targets[0])}>{recipientName(targets[0])}</p> : <Select items={targets.map(target => ({ value: target.chat_id, label: testRecipientName(target) }))} value={selectedTest} onValueChange={value => { if (value) { setTestRecipient(value); action.reset() } }}>
     <SelectTrigger aria-label={t('测试接收目标', 'Test recipient')} className="w-full min-w-0"><SelectValue className="min-w-0"><span className="truncate" title={selectedTarget ? testRecipientName(selectedTarget) : undefined}>{selectedTarget ? testRecipientName(selectedTarget) : ''}</span></SelectValue></SelectTrigger>
     <SelectContent align="start" alignItemWithTrigger={false} className="min-w-0"><SelectGroup>{targets.map(target => <SelectItem key={target.chat_id} value={target.chat_id}><span className="min-w-0 truncate" title={testRecipientName(target)}>{testRecipientName(target)}</span></SelectItem>)}</SelectGroup></SelectContent>
    </Select>}
   </div>}
   <Button variant="outline" size="sm" className="shrink-0 justify-self-end" disabled={!canSave || isFeishu && !selectedTest || provider === 'telegram' && !cfg.chat_id.trim()} onClick={() => action.mutate({ kind: 'test', chatID: isFeishu ? selectedTest : undefined })}><Send className="size-3.5" />{action.isPending && action.variables.kind === 'test' ? t('发送中…', 'Sending…') : t('发送测试消息', 'Send test message')}</Button>
  </div>
  {dirty && <p className="text-xs text-muted-foreground">{t('发送测试前会保存当前修改。', 'Current changes are saved before sending a test.')}</p>}
  {testResult && <p role={testResult.success ? 'status' : 'alert'} aria-label={t('测试结果', 'Test result')} className={`flex items-start gap-1.5 text-xs ${testResult.success ? 'text-emerald-600 dark:text-emerald-400' : 'text-destructive'}`}>{testResult.success && <CheckIcon className="mt-0.5 size-3.5 shrink-0" />}<span className="min-w-0 break-words">{testResult.message}</span></p>}
 </div>
 const subscriptionTargetRow = targets.find(target => target.chat_id === subscriptionTarget)
 const inherited = !!subscriptionTargetRow && !subscriptionTargetRow.subscription_override
 const effectiveSubscription = subscriptionTargetRow?.subscription_override || cfg.subscription
 const setSubscription = (value: Subscription) => subscriptionTargetRow ? update('targets',targets.map(target => target.chat_id === subscriptionTargetRow.chat_id ? { ...target,subscription_override:value } : target)) : update('subscription',value)
 if (creatingRule) return <div className="flex min-h-0 flex-1 flex-col"><h3 className="border-b px-5 py-3 text-sm font-medium">{t('新建通知规则', 'New notification rule')}</h3><RuleEditor onCancel={() => setCreatingRule(false)} onDone={(rule: Rule) => {
  client.setQueryData<Rule[]>(['notification-rules'],previous => [...(previous || []).filter(value => value.id !== rule.id),rule])
  setSubscription({ ...effectiveSubscription,rule_ids:[...effectiveSubscription.rule_ids,rule.id] }); setCreatingRule(false)
 }} /></div>
 return <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden">
  <Tabs value={view} onValueChange={setView} className="shrink-0 border-b px-5"><TabsList variant="line"><TabsTrigger value="connection" disabled={action.isPending}>{t('连接配置', 'Connection')}</TabsTrigger><TabsTrigger value="subscriptions" disabled={action.isPending}>{t('通知订阅', 'Subscriptions')}</TabsTrigger></TabsList></Tabs>
  <fieldset disabled={action.isPending} className="min-h-0 min-w-0 flex-1 space-y-4 overflow-y-auto p-5">
   {view === 'connection' && <>
   {!saved && <Choice label={t('平台', 'Platform')} value={provider} options={channelPlatforms(language)} onChange={value => { setProvider(value); setCfg(newConfig(language, timeZone)); setName(value === 'feishu_app' ? t('飞书机器人', 'Feishu bot') : value === 'telegram' ? 'Telegram' : 'Webhook'); setSecret(''); setSigning(''); setAuthorization(''); setCredentialMessage(''); setTestResults({}); action.reset() }} />}
   <Field label={t('渠道名称', 'Channel name')}><Input value={name} onChange={event => { setName(event.target.value); action.reset() }} maxLength={128} /></Field>
   {isFeishu && <Field label="App ID"><Input value={cfg.app_id} onChange={event => update('app_id', event.target.value)} placeholder="cli_…" autoComplete="off" /></Field>}
   {isBot && <div className="space-y-2"><Field label={isFeishu ? 'App Secret' : 'Bot Token'} hint={saved?.has_secrets ? t('密钥已配置；留空保留。', 'Secret configured; leave blank to retain.') : undefined}><Input type="password" autoComplete="new-password" value={secret} onChange={event => { setSecret(event.target.value); setCredentialMessage(''); setTestResults({}); action.reset() }} /></Field>
    <div className="flex flex-wrap items-center gap-x-2 gap-y-1"><Button variant="ghost" size="sm" className="-ml-2" disabled={!canSave} onClick={() => { setCredentialMessage(''); action.mutate({ kind: 'check' }) }}>{action.isPending && action.variables.kind === 'check' ? t('检查中…', 'Checking…') : t('检查凭证', 'Check credentials')}</Button>{dirty && <span className="text-xs text-muted-foreground">{t('检查前会先保存。', 'Saves changes before checking.')}</span>}</div>
    {credentialMessage && <p role="status" className="text-xs text-emerald-600 dark:text-emerald-400">{credentialMessage}</p>}
    <ErrorText error={action.error instanceof ChannelActionError && action.error.stage === 'check' ? action.error : undefined} />
   </div>}
   {provider === 'webhook' && <><Field label="Webhook URL" hint={saved?.has_secrets ? t('地址已加密保存，留空保留。', 'URL is encrypted; leave blank to retain.') : undefined}><Input type="password" autoComplete="off" value={cfg.endpoint} onChange={event => update('endpoint', event.target.value)} placeholder="https://…" /></Field><Field label={t('签名密钥（可选）', 'Signing key (optional)')}><Input type="password" autoComplete="new-password" value={signing} onChange={event => setSigning(event.target.value)} /></Field><Field label={t('Authorization（可选）', 'Authorization (optional)')}><Input type="password" autoComplete="new-password" value={authorization} onChange={event => setAuthorization(event.target.value)} /></Field><Check label={t('允许内部网络地址', 'Allow internal network destination')} checked={cfg.allow_private} onChange={value => update('allow_private', value)} /></>}
   {isFeishu && <>
    <div role="status" aria-label={t('连接状态', 'Connection status')} className="space-y-1 text-sm"><span>{t('长连接', 'Persistent connection')} · {identityChanged ? t('保存新的应用凭证后连接', 'Save the new application credentials to connect') : connection.data ? labels[connection.data.state] : t('保存凭证后连接', 'Save credentials to connect')}</span><ErrorText error={connection.data?.error || connection.error} /></div>
    {connection.data?.state === 'connected' && connection.data.message_count === 0 && <p role="status" className="text-xs text-muted-foreground">{t('连接已建立，但尚未收到消息事件。请在飞书发布包含权限和“接收消息”订阅的新版本，确认生效后重新发消息。', 'Connected, but no message event has arrived. Publish a Feishu version containing the permissions and receive-message subscription, then send a new message.')}</p>}
    {!!connection.data?.message_count && <div role="status" className="space-y-1 text-xs text-muted-foreground"><p>{t(`已收到 ${connection.data.message_count} 次消息事件`, `${connection.data.message_count} message events received`)}{connection.data.last_message_at ? ` · ${formatDateTime(connection.data.last_message_at)}` : ''}</p>{connection.data.last_message_result === 'discovered' && <p>{t('会话已识别。', 'Conversation discovered.')}</p>}{connection.data.last_message_result === 'bot_identity_pending' && <p>{t('群消息已收到，正在获取机器人身份；稍后再次 @ 机器人。', 'Group message received; bot identity is pending. Mention the bot again shortly.')}</p>}{connection.data.last_message_result === 'ignored_group' && <p>{t('此群消息没有 @ 当前机器人，因此未识别。', 'This group message did not mention this bot.')}</p>}{['invalid_event', 'missing_identity', 'ignored_bot'].includes(connection.data.last_message_result || '') && <p>{t('收到的事件缺少有效用户或会话信息，未添加接收目标。', 'The event did not identify a valid user conversation.')}</p>}{connection.data.last_message_result === 'queue_full' && <p>{t('消息接收队列繁忙，请稍后重试。', 'The message queue is busy; try again shortly.')}</p>}<ErrorText error={connection.data.discovery_error ? t('消息事件已收到，但保存会话失败。请检查后端服务与数据库连接。', connection.data.discovery_error) : undefined} /></div>}
    <FeishuSetup appID={cfg.app_id} />
    <section aria-label={t('接收目标', 'Recipients')} className="min-w-0 space-y-2">
     <div className="flex min-w-0 flex-wrap items-center justify-between gap-1"><h3 className="text-sm font-medium">{t('接收目标', 'Recipients')}<span className="ml-2 text-xs font-normal text-muted-foreground">{t(`已选 ${targets.length} 个`, `${targets.length} selected`)}</span></h3><Button variant="ghost" size="sm" className="-mr-2" disabled={!canSave || !targets.length} onClick={() => setView('subscriptions')}>{t('订阅设置', 'Subscription settings')}<ChevronRight className="size-3.5" /></Button></div>
     <div className="min-w-0 rounded-md border">
      <div role="list" aria-label={t('接收会话', 'Recipient conversations')} className="min-w-0 divide-y px-3">
       {recipientRows.map(({ target, name, checked }) => <RecipientRow key={target.chat_id} target={target} name={name} checked={checked} subscriptionLabel={checked ? target.subscription_override ? t('独立配置', 'Custom configuration') : t('使用全部对话配置', 'Uses all conversations settings') : undefined} onToggle={() => toggleTarget(target)} onRename={checked ? value => update('targets', targets.map(item => item.chat_id === target.chat_id ? { ...item, name: value } : item)) : undefined} />)}
       {!targets.length && !discovered.length && <p className="py-3 text-xs leading-5 text-muted-foreground">{!saved ? t('保存应用凭证后，私聊机器人或在群内 @ 机器人，接收会话会显示在这里。', 'Save the app credentials, then message the bot privately or mention it in a group. Conversations will appear here.') : chats.isPending && !identityChanged ? t('正在读取接收会话…', 'Loading conversations…') : t('尚未识别会话。发布飞书应用的新版本后，再向机器人发送消息。', 'No chats detected. Publish the Feishu application version, then send the bot a new message.')}</p>}
      </div>
      {targets.length > 0 && <div className="min-w-0 border-t p-3">{testControls}</div>}
     </div>
     <p className="text-xs leading-5 text-muted-foreground">{t('勾选接收会话，点击名称修改备注。对话默认使用“全部对话”配置，切换页签保留草稿。', 'Select recipients; click names to rename. Conversations use All conversations settings by default. Switching tabs retains the draft.')}</p>
     <ErrorText error={chats.error} />
    </section>
   </>}
   {provider === 'telegram' && <><Check label={t('自动发现接收会话', 'Automatically discover recipient chats')} checked={cfg.auto_discover} onChange={value => update('auto_discover', value)} /><p className="text-xs text-muted-foreground">{t('只识别接收会话，不执行聊天指令；不会重置机器人已有的 Webhook。', 'Only recipient chats are discovered. Messages do not run commands, and existing bot webhooks are preserved.')}</p><Field label={t('通知会话 ID', 'Notification chat ID')} hint={t('开启自动发现并保存后向机器人发送消息，也可手动填写会话 ID。', 'Enable and save automatic discovery, then message the bot, or enter a chat ID.')}><Input value={cfg.chat_id} onChange={event => update('chat_id', event.target.value)} /></Field>{chats.data?.map(chat => <Button key={chat.chat_id} variant="ghost" onClick={() => update('chat_id', chat.chat_id)}>{chat.name} · {chat.chat_id}</Button>)}</>}
   <details className="group"><summary className="flex cursor-pointer list-none items-center justify-between gap-2 py-1 text-sm select-none outline-none focus-visible:ring-2 focus-visible:ring-ring [&::-webkit-details-marker]:hidden"><span>{t('高级选项', 'Advanced options')}</span><ChevronDown aria-hidden="true" className="size-3.5 text-muted-foreground group-open:rotate-180" /></summary><div className="space-y-3 pt-3">
    {isFeishu && <><Field label={t('手填会话 ID', 'Manual chat ID')}><Input value={manualID} onChange={event => setManualID(event.target.value)} placeholder="oc_…" maxLength={128} /></Field><Field label={t('会话备注（可选）', 'Recipient name (optional)')}><Input value={manualName} onChange={event => setManualName(event.target.value)} maxLength={128} /></Field><Button variant="outline" size="sm" disabled={!manualID.trim() || targets.some(target => target.chat_id === manualID.trim())} onClick={() => { update('targets', [...targets, { chat_id: manualID.trim(), name: manualName.trim() || manualID.trim() }]); setManualID(''); setManualName('') }}>{t('添加接收目标', 'Add recipient')}</Button></>}
    <Choice label={t('消息语言', 'Message language')} value={cfg.language} options={[[ 'zh-CN', '中文' ], [ 'en-US', 'English' ]]} onChange={value => update('language', value)} /><Field label={t('SUMA 公网地址（可选）', 'SUMA public URL (optional)')}><Input value={cfg.public_url} onChange={event => update('public_url', event.target.value)} placeholder="https://suma.example.com" /></Field>
   </div></details>
   <div className="space-y-1"><div className="flex items-center justify-between gap-3 py-1 text-sm"><label htmlFor={enabledID} className="cursor-pointer">{t('启用渠道', 'Enable channel')}</label><Switch id={enabledID} disabled={action.isPending} checked={enabled} onCheckedChange={value => { setEnabled(value); action.reset() }} /></div>{!enabled && <p className="text-xs text-muted-foreground">{t('停止通知投递，保留渠道配置。', 'Pauses notification delivery and keeps the configuration.')}</p>}</div>
   {!isFeishu && <div className="space-y-3 border-t pt-3">{testControls}<Button variant="ghost" size="sm" disabled={!canSave} onClick={() => setView('subscriptions')}>{t('订阅设置', 'Subscription settings')}<ChevronRight className="size-3.5" /></Button></div>}
   </>}
   {view === 'subscriptions' && <div className="min-w-0 space-y-4">
    <p className="text-xs leading-5 text-muted-foreground">{isFeishu ? t('已选对话默认使用“全部对话”的规则和发送策略，也可以为单个对话独立配置。', 'Selected conversations use All conversations settings by default. Individual conversations can have their own rules and delivery policy.') : t('选择此渠道订阅的规则和发送策略。', 'Choose this channel’s rules and delivery policy.')}</p>
    {isFeishu && targets.length > 0 && <Choice label={t('配置对象', 'Configure subscription for')} value={subscriptionTargetRow ? `chat:${subscriptionTargetRow.chat_id}` : 'default'} options={[[ 'default',t('全部对话', 'All conversations') ], ...targets.map(target => [`chat:${target.chat_id}`,`${recipientName(target)} · ${target.chat_id}`] as [string,string])]} onChange={value => setSubscriptionTarget(value === 'default' ? null : value.slice(5))} />}
    {subscriptionTargetRow && <Choice label={t('订阅方式', 'Subscription mode')} value={inherited ? 'inherit' : 'custom'} options={[[ 'inherit',t('使用全部对话配置', 'Use all conversations settings') ], [ 'custom',t('独立配置', 'Custom configuration') ]]} onChange={value => update('targets',targets.map(target => target.chat_id === subscriptionTargetRow.chat_id ? { ...target,subscription_override:value === 'inherit' ? null : structuredClone(cfg.subscription) } : target))} />}
    {inherited && <div className="flex flex-wrap items-center justify-between gap-2"><p className="text-xs text-muted-foreground">{t('此对话使用“全部对话”的规则和发送策略。', 'This conversation uses the rules and delivery policy from All conversations.')}</p><Button variant="ghost" size="sm" onClick={() => setSubscriptionTarget(null)}>{t('编辑全部对话', 'Edit all conversations')}</Button></div>}
    <fieldset disabled={inherited || action.isPending} className={inherited ? 'min-w-0 opacity-65' : 'min-w-0'}><SubscriptionEditor value={effectiveSubscription} channels={channels.data || []} channelID={saved?.id} language={cfg.language} publicURL={cfg.public_url} onChange={setSubscription} onCreate={() => setCreatingRule(true)} /></fieldset>
    <ErrorText error={channels.error} />
   </div>}
   <ErrorText error={action.error instanceof ChannelActionError && ['test', 'check'].includes(action.error.stage) ? undefined : action.error} />
  </fieldset>
  <fieldset aria-label={t('渠道操作', 'Channel actions')} disabled={action.isPending} className="flex min-w-0 shrink-0 flex-wrap items-center gap-x-3 gap-y-2 border-t bg-popover px-5 py-3">
   {view === 'subscriptions' && <p className="basis-full text-xs leading-5 text-muted-foreground">{t('订阅和策略修改只影响新事件，已排队消息按原配置继续发送；暂停渠道或移除目标可取消待发送消息。', 'Subscription and policy changes affect new events only. Queued messages keep their original configuration; pause the channel or remove the recipient to cancel pending messages.')}</p>}
   <p role="status" aria-label={t('保存状态', 'Save status')} className="min-w-0 flex-1 basis-20 text-xs text-muted-foreground">{action.isPending ? t('处理中…', 'Working…') : !saved ? t('尚未保存', 'Not saved') : dirty ? t('有未保存的修改', 'Unsaved changes') : t('已保存', 'Saved')}</p>
   <div className="ml-auto flex shrink-0 items-center gap-2">{(!saved || dirty) && <Button variant={saved ? 'outline' : 'default'} disabled={!canSave} onClick={() => action.mutate({ kind: 'save' })}>{t('保存渠道', 'Save channel')}</Button>}{saved && <Button disabled={dirty && !canSave} onClick={() => dirty ? action.mutate({ kind: 'done' }) : onDone()}>{dirty ? t('保存并完成', 'Save and finish') : t('完成', 'Done')}</Button>}</div>
  </fieldset>
 </div>
}

function RecipientRow({ target, name, checked, subscriptionLabel, onToggle, onRename }: { target: ChannelTarget; name: string; checked: boolean; subscriptionLabel?: string; onToggle: () => void; onRename?: (value: string) => void }) {
 const t = useOpsText(), id = useId(), [editing, setEditing] = useState(false), [draft, setDraft] = useState(target.name)
 const commit = () => { onRename?.(draft.trim() || name); setEditing(false) }
 return <div role="listitem" className="flex min-w-0 items-start gap-2.5 py-2.5">
  <input id={id} type="checkbox" aria-label={name} aria-describedby={`${id}-identity`} checked={checked} onChange={onToggle} className="mt-1 shrink-0 accent-foreground" />
  <div className="min-w-0 flex-1 space-y-0.5">
   {editing ? <Input autoFocus className="h-7 min-w-0" aria-label={t(`备注 ${target.chat_id}`, `Recipient name ${target.chat_id}`)} value={draft} maxLength={128} onFocus={event => event.target.select()} onChange={event => setDraft(event.target.value)} onBlur={commit} onKeyDown={event => { if (event.key === 'Enter') { event.preventDefault(); commit() } else if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); setEditing(false) } }} /> : onRename ? <button type="button" className="flex w-full min-w-0 items-center gap-2 text-left text-sm outline-none hover:text-foreground/75 focus-visible:ring-2 focus-visible:ring-ring" aria-label={t(`编辑备注 ${name}`, `Edit recipient name ${name}`)} onClick={() => { setDraft(target.name); setEditing(true) }}><span className="truncate">{name}</span><Pencil aria-hidden="true" className="size-3 shrink-0 text-muted-foreground" /></button> : <label htmlFor={id} className="block cursor-pointer truncate text-sm">{name}</label>}
   {subscriptionLabel && <p className="text-[11px] text-muted-foreground">{subscriptionLabel}</p>}
   <p id={`${id}-identity`} className="truncate font-mono text-[11px] text-muted-foreground" title={target.chat_id}>{target.chat_id}</p>
  </div>
 </div>
}

function CopyValue({ label, value }: { label: string; value: string }) {
 const t = useOpsText(), [copied, setCopied] = useState(false), [error, setError] = useState('')
 useEffect(() => {
  if (!copied) return
  const timer = setTimeout(() => setCopied(false), 2000)
  return () => clearTimeout(timer)
 }, [copied])
 return <div className="space-y-2">
  <p>{label}</p>
  <div className="relative rounded-md bg-muted">
   <pre className="select-text overflow-x-auto p-3 pr-12 text-xs"><code>{value}</code></pre>
   <Tooltip>
    <TooltipTrigger render={<Button variant="ghost" size="icon-xs" className="absolute top-2 right-2 text-muted-foreground hover:bg-background hover:text-foreground" aria-label={t(`复制 ${label}`, `Copy ${label}`)} onClick={async () => { try { await navigator.clipboard.writeText(value); setCopied(true); setError('') } catch { setCopied(false); setError(t('复制失败，请选中下方文本复制。', 'Copy failed. Select and copy the text below.')) } }} />}>
     {copied ? <CheckIcon aria-hidden="true" className="size-3.5" /> : <Copy aria-hidden="true" className="size-3.5" />}
    </TooltipTrigger>
    <TooltipContent>{copied ? t('已复制', 'Copied') : t('复制', 'Copy')}</TooltipContent>
   </Tooltip>
   <span role="status" className="sr-only">{copied ? t('已复制', 'Copied') : ''}</span>
  </div>
  <ErrorText error={error} />
 </div>
}
function FeishuSetup({ appID }: { appID: string }) {
 const t = useOpsText()
 const permissions = JSON.stringify({ scopes: { tenant: ['im:message:send_as_bot', 'im:message.p2p_msg:readonly', 'im:message.group_at_msg:readonly'], user: [] } }, null, 2)
 return <details className="group overflow-hidden rounded-md border text-xs leading-5">
  <summary className="flex cursor-pointer list-none items-center justify-between gap-2 px-3 py-2 text-sm font-medium select-none outline-none hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring [&::-webkit-details-marker]:hidden"><span>{t('飞书平台配置说明', 'Feishu platform setup')}</span><ChevronDown aria-hidden="true" className="size-4 shrink-0 text-muted-foreground group-open:rotate-180" /></summary>
  <div className="space-y-3 border-t p-3">
   <p>{t('创建企业自建应用，开启机器人能力。复制凭证到 SUMA 并保存，等待长连接显示“已连接”，再在飞书设置长连接接收。', 'Create an internal application and enable its bot. Save the credentials in SUMA and wait for Connected before configuring persistent connections in Feishu.')}</p>
   <a href={`https://open.feishu.cn/app${appID && /^cli_[\w-]+$/.test(appID) ? `/${encodeURIComponent(appID)}` : ''}`} target="_blank" rel="noreferrer" className="underline">{t('打开飞书应用后台', 'Open Feishu application console')}</a>
   <CopyValue label={t('权限管理 → 批量导入/导出权限（应用身份）', 'Permissions → Batch import/export (application identity)')} value={permissions} />
   <p>{t('事件与回调 → 事件配置：使用长连接接收，添加“接收消息”事件。无需公网回调地址。', 'Events & callbacks → Event configuration: receive through a persistent connection and add the receive-message event. No public callback URL is needed.')}</p>
   <CopyValue label={t('消息事件', 'Message event')} value="im.message.receive_v1" />
   <p>{t('设置应用可用范围，创建并发布新版本，让权限和事件配置生效。将机器人加入目标群，或在可用范围内私聊机器人。', 'Set application visibility, create and publish a version to apply permissions and event subscriptions. Add the bot to target groups or message it privately within its visibility scope.')}</p>
   <a href="https://open.feishu.cn/document/server-docs/im-v1/message/create" target="_blank" rel="noreferrer" className="underline">{t('飞书发送消息与权限文档', 'Feishu message and permission documentation')}</a>
  </div>
 </details>
}
