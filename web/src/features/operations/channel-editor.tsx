import { useState } from 'react'
import { Link } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Copy, Trash2 } from 'lucide-react'
import { Button } from '../../components/ui/button'
import { Input } from '../../components/ui/input'
import { api } from '../../lib/api'
import { useI18n } from '../../lib/i18n'
import { useDateTime } from '../../lib/time-zone'
import { Check, Choice, ErrorText, Field } from './common'
import { channelPlatforms, useOpsText } from './helpers'
import type { Channel, ChannelConfig, ChannelTarget, ConnectionStatus } from './types'

const newConfig = (language: string, timezone: string): ChannelConfig => ({ endpoint: '', chat_id: '', app_id: '', targets: [], language, timezone, allow_private: false, interactive: false, public_url: '' })
type ChannelAction = { kind: 'save' | 'check' | 'test'; chatID?: string }
type Chat = { chat_id: string; name: string; private: boolean }

export function ChannelEditor({ row, onDone, onSubscribe }: { row?: Channel; onDone: () => void; onSubscribe: (channel: Channel) => void }) {
 const t = useOpsText(), { language } = useI18n(), { timeZone } = useDateTime(), client = useQueryClient()
 const normalize = (config?: ChannelConfig) => ({ ...newConfig(language, timeZone), ...config, targets: config?.targets || [] })
 const [provider, setProvider] = useState(row?.provider || 'feishu_app'), [name, setName] = useState(row?.name || t('飞书机器人', 'Feishu bot'))
 const [enabled, setEnabled] = useState(row?.enabled ?? true), [cfg, setCfg] = useState<ChannelConfig>(normalize(row?.config)), [saved, setSaved] = useState(row)
 const [secret, setSecret] = useState(''), [signing, setSigning] = useState(''), [authorization, setAuthorization] = useState('')
 const [manualID, setManualID] = useState(''), [manualName, setManualName] = useState(''), [testRecipient, setTestRecipient] = useState('')
 const [saveMessage, setSaveMessage] = useState(''), [credentialMessage, setCredentialMessage] = useState(''), [testResults, setTestResults] = useState<Record<string, { success: boolean; message: string }>>({})
 const isFeishu = provider === 'feishu_app', isBot = isFeishu || provider === 'telegram', targets = cfg.targets || []
 const identityChanged = !!saved && saved.config.app_id !== cfg.app_id
 const dirty = !saved || !!(secret || signing || authorization) || name !== saved.name || enabled !== saved.enabled || JSON.stringify(cfg) !== JSON.stringify(normalize(saved.config))
 const selectedTest = targets.some(target => target.chat_id === testRecipient) ? testRecipient : targets[0]?.chat_id || ''
 const action = useMutation({
  mutationFn: async ({ kind, chatID }: ChannelAction) => {
   let current = saved
   if (dirty || !current) {
    current = await api<Channel>(`/notifications/channels${current ? `/${current.id}` : ''}`, { method: current ? 'PUT' : 'POST', body: JSON.stringify({ name, provider, enabled, version: current?.version || 0, config: cfg, secrets: { token: secret, signing_key: signing, authorization } }) })
    setSaved(current); setCfg(normalize(current.config)); setSecret(''); setSigning(''); setAuthorization(''); setSaveMessage(t('渠道已保存', 'Channel saved'))
    void client.invalidateQueries({ queryKey: ['notification-channels'] }); void client.invalidateQueries({ queryKey: ['notification-chats', current.id] }); void client.invalidateQueries({ queryKey: ['notification-connection', current.id] })
   }
   if (kind === 'check') {
    const result = await api<{ credentials_valid: boolean }>(`/notifications/channels/${current.id}/check`, { method: 'POST' })
    if (!result.credentials_valid) throw new Error(t('凭证校验未通过', 'Credentials were not verified'))
   }
   if (kind === 'test') {
    if (isFeishu && !current.config.targets?.some(target => target.chat_id === chatID)) throw new Error(t('请选择已配置的测试目标', 'Select a configured test recipient'))
    await api(`/notifications/channels/${current.id}/test`, { method: 'POST', body: JSON.stringify(isFeishu ? { chat_id: chatID } : {}) })
   }
  },
  onSuccess: (_, { kind, chatID }) => {
   if (kind === 'check') setCredentialMessage(t('凭证有效；连接和消息发送需分别验证。', 'Credentials verified. Connection and delivery are verified separately.'))
   if (kind === 'test') setTestResults(previous => ({ ...previous, [chatID || 'default']: { success: true, message: t('测试消息已发送', 'Test message sent') } }))
  },
  onError: (error, { kind, chatID }) => {
   if (kind === 'check') setCredentialMessage('')
   if (kind === 'test') setTestResults(previous => ({ ...previous, [chatID || 'default']: { success: false, message: error.message } }))
  },
 })
 const update = <K extends keyof ChannelConfig>(key: K, value: ChannelConfig[K]) => {
  action.reset(); setSaveMessage(''); setCfg(previous => ({ ...previous, [key]: value, ...(key === 'app_id' ? { targets: [] } : {}) }))
  if (key === 'app_id') { setCredentialMessage(''); setTestResults({}); setTestRecipient('') }
 }
 const chats = useQuery({ queryKey: ['notification-chats', saved?.id], queryFn: () => api<Chat[]>(`/notifications/channels/${saved?.id}/chats`), enabled: !!saved && !identityChanged && (isFeishu || cfg.interactive), refetchInterval: 2000 })
 const connection = useQuery({ queryKey: ['notification-connection', saved?.id], queryFn: () => api<ConnectionStatus>(`/notifications/channels/${saved?.id}/connection`), enabled: !!saved && isFeishu && !identityChanged, refetchInterval: 2000 })
 const labels: Record<ConnectionStatus['state'], string> = { stopped: t('未连接', 'Stopped'), connecting: t('连接中', 'Connecting'), connected: t('已连接', 'Connected'), reconnecting: t('重新连接中', 'Reconnecting'), error: t('连接失败', 'Connection failed') }
 const toggleTarget = (target: ChannelTarget) => {
  update('targets', targets.some(item => item.chat_id === target.chat_id) ? targets.filter(item => item.chat_id !== target.chat_id) : [...targets, target])
  setTestResults(previous => { const next = { ...previous }; delete next[target.chat_id]; return next })
 }
 const discovered = (chats.data || []).filter(chat => !targets.some(target => target.chat_id === chat.chat_id))
 const canSave = !!name.trim() && (!isBot || (isFeishu ? !!cfg.app_id.trim() : true) && !!(secret.trim() || saved?.has_secrets))
 const testResult = testResults[isFeishu ? selectedTest : 'default']
 return <div className="space-y-4 overflow-y-auto p-5">
  <fieldset disabled={action.isPending} className="min-w-0 space-y-4">
   {!saved && <Choice label={t('平台', 'Platform')} value={provider} options={channelPlatforms(language)} onChange={value => { setProvider(value); setCfg(newConfig(language, timeZone)); setName(value === 'feishu_app' ? t('飞书机器人', 'Feishu bot') : value === 'telegram' ? 'Telegram' : 'Webhook'); setSecret(''); setSigning(''); setAuthorization(''); setCredentialMessage(''); setTestResults({}); action.reset() }} />}
   <Field label={t('渠道名称', 'Channel name')}><Input value={name} onChange={event => { setName(event.target.value); setSaveMessage(''); action.reset() }} maxLength={128} /></Field>
   {isFeishu && <Field label="App ID"><Input value={cfg.app_id} onChange={event => update('app_id', event.target.value)} placeholder="cli_…" autoComplete="off" /></Field>}
   {isBot && <Field label={isFeishu ? 'App Secret' : 'Bot Token'} hint={saved?.has_secrets ? t('密钥已配置；留空保留。', 'Secret configured; leave blank to retain.') : undefined}><Input type="password" autoComplete="new-password" value={secret} onChange={event => { setSecret(event.target.value); setCredentialMessage(''); setTestResults({}); setSaveMessage(''); action.reset() }} /></Field>}
   {provider === 'webhook' && <><Field label="Webhook URL" hint={saved?.has_secrets ? t('地址已加密保存，留空保留。', 'URL is encrypted; leave blank to retain.') : undefined}><Input type="password" autoComplete="off" value={cfg.endpoint} onChange={event => update('endpoint', event.target.value)} placeholder="https://…" /></Field><Field label={t('签名密钥（可选）', 'Signing key (optional)')}><Input type="password" autoComplete="new-password" value={signing} onChange={event => setSigning(event.target.value)} /></Field><Field label={t('Authorization（可选）', 'Authorization (optional)')}><Input type="password" autoComplete="new-password" value={authorization} onChange={event => setAuthorization(event.target.value)} /></Field><Check label={t('允许内部网络地址', 'Allow internal network destination')} checked={cfg.allow_private} onChange={value => update('allow_private', value)} /></>}
   {isFeishu && <>
    <div role="status" aria-label={t('连接状态', 'Connection status')} className="space-y-1 text-sm"><span>{t('长连接', 'Persistent connection')} · {identityChanged ? t('保存新的应用凭证后连接', 'Save the new application credentials to connect') : connection.data ? labels[connection.data.state] : t('保存凭证后连接', 'Save credentials to connect')}</span><ErrorText error={connection.data?.error || connection.error} /></div>
    <FeishuSetup appID={cfg.app_id} interactive={cfg.interactive} />
    <Field label={t('接收目标', 'Recipients')} hint={t('先保存凭证，再私聊机器人或在群内 @ 机器人。勾选要接收通知的会话；通知规则可分别选择目标。', 'Save credentials, then message the bot privately or mention it in a group. Select recipients; rules can route to different chats.')}>
     <div className="divide-y rounded-md border px-3">
      {targets.map(target => <div key={target.chat_id} className="flex min-w-0 items-center gap-2 py-2"><input type="checkbox" className="shrink-0 accent-foreground" aria-label={target.name || target.chat_id} checked onChange={() => toggleTarget(target)} /><Input className="min-w-0 flex-1" aria-label={t(`备注 ${target.chat_id}`, `Recipient name ${target.chat_id}`)} value={target.name} maxLength={128} onChange={event => update('targets', targets.map(item => item.chat_id === target.chat_id ? { ...item, name: event.target.value } : item))} /><Button variant="ghost" size="icon-sm" className="shrink-0" aria-label={t(`移除 ${target.name}`, `Remove ${target.name}`)} onClick={() => toggleTarget(target)}><Trash2 /></Button></div>)}
      {discovered.map(chat => { const label = `${chat.private ? t('私聊', 'Private chat') : t('群聊', 'Group chat')} · ${chat.name || chat.chat_id}`; return <div key={chat.chat_id} className="break-all py-2"><Check label={label} checked={false} onChange={() => toggleTarget({ chat_id: chat.chat_id, name: label })} /></div> })}
      {!targets.length && !discovered.length && <p className="py-4 text-sm text-muted-foreground">{t('尚未识别会话。完成下方平台配置后向机器人发消息。', 'No chats detected. Complete the platform setup, then send the bot a message.')}</p>}
     </div>
    </Field><ErrorText error={chats.error} />
   </>}
   {provider === 'telegram' && <><Field label={t('通知会话 ID', 'Notification chat ID')} hint={t('开启聊天能力后私聊或在群内 @ 机器人，也可手动填写会话 ID。', 'Enable chat to discover private chats or group mentions, or enter a chat ID.')}><Input value={cfg.chat_id} onChange={event => update('chat_id', event.target.value)} /></Field>{chats.data?.map(chat => <Button key={chat.chat_id} variant="ghost" onClick={() => update('chat_id', chat.chat_id)}>{chat.name} · {chat.chat_id}</Button>)}</>}
   {isBot && <details className="space-y-3"><summary className="cursor-pointer text-sm">{t('AI 聊天与审批（可选）', 'AI chat and approvals (optional)')}</summary><Check label={t('启用聊天诊断和审批', 'Enable chat diagnosis and approvals')} checked={cfg.interactive} onChange={value => update('interactive', value)} />{cfg.interactive && <><p className="text-xs text-muted-foreground">{t('非白名单用户只能查询安全状态和数量摘要。操作需要站内身份绑定，每项变更仍需审核。', 'Other users can query safe status and counts only. Operations require a site-confirmed identity and individual approval.')}</p>{isFeishu && <p className="break-words text-xs">{t('飞书回调配置中使用长连接接收，并订阅', 'Use a persistent connection for callbacks and subscribe to')} <code>card.action.trigger</code></p>}<Link to="/settings" hash="ai" className="text-sm underline">{t('配置 AI 与身份绑定', 'Configure AI and identity binding')}</Link></>}</details>}
   <details className="space-y-3"><summary className="cursor-pointer text-sm">{t('高级选项', 'Advanced options')}</summary>
    {isFeishu && <><Field label={t('手填会话 ID', 'Manual chat ID')}><Input value={manualID} onChange={event => setManualID(event.target.value)} placeholder="oc_…" maxLength={128} /></Field><Field label={t('会话备注（可选）', 'Recipient name (optional)')}><Input value={manualName} onChange={event => setManualName(event.target.value)} maxLength={128} /></Field><Button variant="outline" size="sm" disabled={!manualID.trim() || targets.some(target => target.chat_id === manualID.trim())} onClick={() => { update('targets', [...targets, { chat_id: manualID.trim(), name: manualName.trim() || manualID.trim() }]); setManualID(''); setManualName('') }}>{t('添加接收目标', 'Add recipient')}</Button></>}
    <Choice label={t('消息语言', 'Message language')} value={cfg.language} options={[[ 'zh-CN', '中文' ], [ 'en-US', 'English' ]]} onChange={value => update('language', value)} /><Field label={t('IANA 时区', 'IANA timezone')}><Input value={cfg.timezone} onChange={event => update('timezone', event.target.value)} /></Field><Field label={t('SUMA 公网地址（可选）', 'SUMA public URL (optional)')}><Input value={cfg.public_url} onChange={event => update('public_url', event.target.value)} placeholder="https://suma.example.com" /></Field>
   </details>
   <Check label={t('启用此渠道（也可停用保存）', 'Enable channel (or save paused)')} checked={enabled} onChange={value => { setEnabled(value); setSaveMessage('') }} />
   <div className="flex flex-wrap gap-2"><Button disabled={!canSave} onClick={() => action.mutate({ kind: 'save' })}>{action.isPending ? t('处理中…', 'Working…') : t('保存渠道', 'Save channel')}</Button>{isBot && <Button variant="outline" disabled={!canSave} onClick={() => { setCredentialMessage(''); action.mutate({ kind: 'check' }) }}>{t('检查凭证', 'Check credentials')}</Button>}</div>
   {saveMessage && <p role="status" className="text-sm text-muted-foreground">{saveMessage}</p>}
   {credentialMessage && <p role="status" className="text-sm text-emerald-600 dark:text-emerald-400">{credentialMessage}</p>}
   <div className="space-y-2 border-t pt-3">
    {isFeishu && targets.length > 0 && <Choice label={t('测试接收目标', 'Test recipient')} value={selectedTest} options={targets.map(target => [target.chat_id, target.name || target.chat_id])} onChange={setTestRecipient} />}
    <Button variant="outline" disabled={!canSave || isFeishu && !selectedTest || provider === 'telegram' && !cfg.chat_id.trim()} onClick={() => action.mutate({ kind: 'test', chatID: isFeishu ? selectedTest : undefined })}>{t('发送测试消息', 'Send test message')}</Button>
    {testResult && <p role={testResult.success ? 'status' : 'alert'} className={`break-words text-sm ${testResult.success ? 'text-emerald-600 dark:text-emerald-400' : 'text-destructive'}`}>{isFeishu ? `${targets.find(target => target.chat_id === selectedTest)?.name} · ` : ''}{testResult.message}</p>}
   </div>
   <ErrorText error={action.error} />
   {saved && <div className="flex flex-wrap gap-2 border-t pt-3"><Button variant="outline" disabled={dirty || isFeishu && !targets.length} onClick={() => onSubscribe(saved)}>{t('选择通知订阅', 'Choose subscriptions')}</Button><Button variant="ghost" onClick={onDone}>{t('完成', 'Done')}</Button></div>}
  </fieldset>
 </div>
}

function CopyValue({ label, value }: { label: string; value: string }) {
 const t = useOpsText(), [copied, setCopied] = useState(false), [error, setError] = useState('')
 return <div className="space-y-2"><div className="flex items-center justify-between gap-2"><span>{label}</span><Button variant="ghost" size="sm" onClick={async () => { try { await navigator.clipboard.writeText(value); setCopied(true); setError('') } catch { setError(t('复制失败，请选中下方文本复制。', 'Copy failed. Select and copy the text below.')) } }}><Copy />{copied ? t('已复制', 'Copied') : t('复制', 'Copy')}</Button></div><pre className="overflow-x-auto rounded-md bg-muted p-2 text-xs"><code>{value}</code></pre><ErrorText error={error} /></div>
}
function FeishuSetup({ appID, interactive }: { appID: string; interactive: boolean }) {
 const t = useOpsText()
 const permissions = JSON.stringify({ scopes: { tenant: ['im:message:send_as_bot', 'im:message.p2p_msg:readonly', 'im:message.group_at_msg:readonly'], user: [] } }, null, 2)
 return <details className="space-y-3 rounded-md border p-3 text-xs leading-5"><summary className="cursor-pointer text-sm font-medium">{t('飞书平台配置说明', 'Feishu platform setup')}</summary>
  <p>{t('创建企业自建应用，开启机器人能力。复制凭证到 SUMA 并保存，等待长连接显示“已连接”，再在飞书设置长连接接收。', 'Create an internal application and enable its bot. Save the credentials in SUMA and wait for Connected before configuring persistent connections in Feishu.')}</p>
  <a href={`https://open.feishu.cn/app${appID && /^cli_[\w-]+$/.test(appID) ? `/${encodeURIComponent(appID)}` : ''}`} target="_blank" rel="noreferrer" className="underline">{t('打开飞书应用后台', 'Open Feishu application console')}</a>
  <CopyValue label={t('权限管理 → 批量导入/导出权限（应用身份）', 'Permissions → Batch import/export (application identity)')} value={permissions} />
  <p>{t('事件与回调 → 事件配置：使用长连接接收，添加“接收消息”事件。无需公网回调地址。', 'Events & callbacks → Event configuration: receive through a persistent connection and add the receive-message event. No public callback URL is needed.')}</p>
  <CopyValue label={t('消息事件', 'Message event')} value="im.message.receive_v1" />
  {interactive && <CopyValue label={t('审批回调（长连接接收）', 'Approval callback (persistent connection)')} value="card.action.trigger" />}
  <p>{t('设置应用可用范围，创建并发布新版本，让权限和事件配置生效。将机器人加入目标群，或在可用范围内私聊机器人。', 'Set application visibility, create and publish a version to apply permissions and event subscriptions. Add the bot to target groups or message it privately within its visibility scope.')}</p>
  <a href="https://open.feishu.cn/document/server-docs/im-v1/message/create" target="_blank" rel="noreferrer" className="underline">{t('飞书发送消息与权限文档', 'Feishu message and permission documentation')}</a>
 </details>
}
