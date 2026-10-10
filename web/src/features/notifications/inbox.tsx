import { Link } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Button } from '../../components/ui/button'
import { Badge } from '../../components/ui/badge'
import { ResourceFrame } from '../../pages/images'
import { api } from '../../lib/api'
import { useDateTime } from '../../lib/time-zone'
import { ErrorText } from './common'
import { useOpsText } from './helpers'
import type { Inbox } from './types'
export function NotificationInbox() {
 const t = useOpsText(), client = useQueryClient(), { formatDateTime } = useDateTime()
 const query = useQuery({ queryKey: ['notification-inbox'], queryFn: () => api<Inbox>('/notifications/inbox'), refetchInterval: 5000 })
 const read = useMutation({ mutationFn: (id: string) => api(`/notifications/inbox/${id}/read`, { method: 'POST' }), onSuccess: () => { void client.invalidateQueries({ queryKey: ['notification-inbox'] }) } })
 return <ResourceFrame title={t('站内消息', 'Inbox')} detail={`${query.data?.unread || 0} ${t('条未读', 'unread')}`} action={<Button variant="outline" nativeButton={false} render={<Link to="/settings" hash="notifications" />}>{t('通知设置', 'Notification settings')}</Button>}><div className="divide-y border-y"><ErrorText error={query.error || read.error} />{query.data?.items.map(e => <article key={e.id} className="space-y-2 py-4"><div className="flex flex-wrap items-start gap-3"><div className="min-w-0 flex-1"><h3 className={`text-sm ${e.read ? '' : 'font-semibold'}`}>{e.title}</h3><p className="text-xs text-muted-foreground">{formatDateTime(e.time)} · {e.node_name || e.node_id || t('全局', 'Global')} · {e.resource_id}</p></div><Badge variant="outline">{e.severity}</Badge>{!e.read && <Button size="sm" variant="ghost" disabled={read.isPending} onClick={() => read.mutate(e.id)}>{t('标为已读', 'Mark read')}</Button>}</div><p className="whitespace-pre-wrap break-words text-sm text-muted-foreground">{e.message}</p>{e.task_id && <Link to="/tasks" hash={e.task_id} className="ml-3 text-xs underline">{t('查看任务', 'View task')}</Link>}</article>)}{query.data?.items.length === 0 && <p className="py-10 text-center text-sm text-muted-foreground">{t('暂无消息', 'Your inbox is empty')}</p>}</div></ResourceFrame>
}
