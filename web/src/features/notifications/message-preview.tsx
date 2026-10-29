import { Button } from '../../components/ui/button'
import type { NotificationCard } from './presentation'

export function MessagePreview({ provider, card, text, custom, language, url }: { provider: string; card: NotificationCard; text: string; custom: boolean; language: string; url: string }) {
 const en = language === 'en-US', feishu = provider === 'feishu_app', webhook = provider === 'webhook'
 const status = custom ? 'info' : card.status
 const color = ['error','critical'].includes(status) ? 'bg-red-500/15 text-red-700 dark:text-red-300' : ['success','recovered'].includes(status) ? 'bg-emerald-500/15 text-emerald-700 dark:text-emerald-300' : ['warning','pending'].includes(status) ? 'bg-amber-500/15 text-amber-800 dark:text-amber-200' : 'bg-sky-500/15 text-sky-700 dark:text-sky-300'
 const icon = ['error','critical'].includes(status) ? '🔴' : ['success','recovered'].includes(status) ? '🟢' : ['warning','pending'].includes(status) ? '🟠' : 'ℹ️'
 return <div className="min-w-0 space-y-2">
  <p className="text-xs text-muted-foreground">{feishu ? en ? 'Feishu · interactive card' : '飞书 · 交互卡片' : webhook ? 'Webhook · JSON' : 'Telegram · HTML'}</p>
  <div aria-label={en ? 'Message preview' : '消息预览'} className="min-w-0 overflow-hidden rounded-lg bg-card ring-1 ring-border/60">
   {webhook ? <pre className="p-4 text-xs whitespace-pre-wrap break-words [overflow-wrap:anywhere]">{text}{!custom && url ? `\n${url}` : ''}</pre> : <>
    {(!custom || feishu) && <div data-card-status={status} className={`px-4 py-3 text-sm font-semibold ${feishu ? color : ''}`}>{custom ? 'SUMA' : `${icon} ${card.title}`}</div>}
    <div className="space-y-4 px-4 pt-3 pb-4 text-sm leading-6">
     {custom ? <p className="whitespace-pre-wrap break-words [overflow-wrap:anywhere]">{text}</p> : <dl className="space-y-3.5">{card.fields.map(field => <div key={field.label} className="space-y-0.5"><dt className="text-xs font-medium text-muted-foreground">{field.label}</dt><dd className="break-words [overflow-wrap:anywhere]">{field.value}</dd></div>)}</dl>}
    </div>
   </>}
   {!webhook && url && <div className="px-4 pb-4"><Button variant="outline" size="sm" disabled>{en ? 'View details' : '查看详情'}</Button></div>}
   {!webhook && (!custom || feishu) && <p className="bg-muted/40 px-4 py-2 text-xs text-muted-foreground">SUMA · {en ? 'Docker operations notifications' : 'Docker 运维通知'}</p>}
  </div>
 </div>
}
