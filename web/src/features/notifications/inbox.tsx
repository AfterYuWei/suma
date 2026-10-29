import { CheckCheck } from 'lucide-react'
import { Link } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Button } from '../../components/ui/button'
import { Badge } from '../../components/ui/badge'
import { Spinner } from '../../components/ui/spinner'
import { ResourceFrame } from '../../pages/images'
import { api } from '../../lib/api'
import { useDateTime } from '../../lib/time-zone'
import { useI18n } from '../../lib/i18n'
import { ErrorText } from './common'
import { useOpsText } from './helpers'
import type { Inbox } from './types'
import { compactResource, presentNotification } from './presentation'
export function NotificationInbox() {
 const t = useOpsText(), client = useQueryClient(), { formatDateTime } = useDateTime(), { language } = useI18n()
 const query = useQuery({ queryKey: ['notification-inbox'], queryFn: () => api<Inbox>('/notifications/inbox'), refetchInterval: 5000 })
 const read = useMutation({ mutationFn: (id: string | undefined) => api(id === undefined ? '/notifications/inbox/read-all' : `/notifications/inbox/${encodeURIComponent(id)}/read`, { method: 'POST' }), onSuccess: () => client.invalidateQueries({ queryKey: ['notification-inbox'] }) })
 return <ResourceFrame title={t('站内消息', 'Inbox')} detail={`${query.data?.unread || 0} ${t('条未读', 'unread')}`} action={<div className="flex flex-wrap items-center gap-2">
  <Button variant="outline" aria-busy={read.isPending && read.variables === undefined} disabled={!query.data?.unread || read.isPending} onClick={() => read.mutate(undefined)}>{read.isPending && read.variables === undefined ? <Spinner aria-hidden="true" /> : <CheckCheck />}{t('全部已读', 'Mark all read')}</Button>
  <Button variant="outline" nativeButton={false} render={<Link to="/settings" hash="notifications" />}>{t('通知设置', 'Notification settings')}</Button>
 </div>}>
  <div className="space-y-2"><ErrorText error={query.error || read.error} />
   {query.data?.items.map(e => { const display = presentNotification(e,language), context = [e.node_name || compactResource(e.node_id || ''),display.subject,formatDateTime(e.time)].filter(Boolean); return <article id={e.id} data-event-id={e.id} key={e.id} className="scroll-mt-4 space-y-2 rounded-md bg-surface p-4">
    <div className="flex flex-wrap items-start gap-3">
     <div className="min-w-0 flex-1"><h3 className={`break-words text-sm ${e.read ? '' : 'font-semibold'}`}>{display.title}</h3><p className="break-words text-xs text-muted-foreground">{context.join(' · ')}</p></div>
     {!!e.occurrences && <Badge variant="secondary">{e.resolved_at ? t('已恢复', 'Recovered') : t('持续异常', 'Ongoing fault')}</Badge>}
     {e.severity === 'critical' && <Badge variant="destructive">{t('紧急', 'Urgent')}</Badge>}
     {!e.read && <Button size="sm" variant="ghost" disabled={read.isPending} onClick={() => read.mutate(e.id)}>{t('标为已读', 'Mark read')}</Button>}
    </div>
    {display.summary && <p className="break-words text-sm text-muted-foreground">{display.summary}</p>}
    {!!e.occurrences && <p className="break-words text-xs text-muted-foreground">{t(`累计 ${e.occurrences} 次`, `${e.occurrences} observations`)}{e.resolved_at ? ` · ${t('恢复于', 'Recovered at')} ${formatDateTime(e.resolved_at)}` : e.last_occurred_at ? ` · ${t('最近发生', 'Last observed')} ${formatDateTime(e.last_occurred_at)}` : ''}</p>}
    {e.task_id && <Link to="/tasks" hash={e.task_id} className="text-xs underline">{t('查看任务', 'View task')}</Link>}
    <details className="text-xs text-muted-foreground"><summary className="w-fit cursor-pointer select-none">{t('原始详情', 'Original details')}</summary><div className="mt-2 space-y-2 rounded-md bg-muted p-3"><p className="break-words">{e.title}</p><p className="whitespace-pre-wrap break-words">{e.message}</p>{e.resource_id && <p className="break-all">{t('资源：', 'Resource: ')}{e.resource_id}</p>}</div></details>
   </article> })}
   {query.data?.items.length === 0 && <p className="py-10 text-center text-sm text-muted-foreground">{t('暂无消息', 'Your inbox is empty')}</p>}
  </div>
 </ResourceFrame>
}
