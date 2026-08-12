import { useQuery } from '@tanstack/react-query'
import { LoadingState } from '../components/ui/loading-state'
import { api } from '../lib/api'
import { displayDockerId } from '../lib/docker-id'
import { useI18n } from '../lib/i18n'
import { ResourceFrame } from './images'

interface Audit { id: number; user_id?: number; action: string; resource_type: string; resource_name: string; ip: string; result: string; created_at: string }
export function AuditLogsPage() { const { t, language } = useI18n(); const query = useQuery({ queryKey: ['audit-logs'], queryFn: () => api<Audit[]>('/audit-logs') }); return <ResourceFrame eyebrow={t('operations')} title={t('auditLogs')} detail={language === 'zh-CN' ? 'DockPort 中执行的重要操作' : 'Important actions performed in DockPort'}>{query.isPending ? <LoadingState compact label={language === 'zh-CN' ? '正在加载审计日志' : 'Loading audit logs'} /> : <div className="divide-y divide-border border-y border-border">{query.data?.map((row) => <div key={row.id} className="grid min-h-14 grid-cols-[150px_minmax(0,1fr)_120px_150px] items-center gap-4 px-2 text-xs"><time className="text-text-subtle">{new Date(row.created_at).toLocaleString(language)}</time><div><span className="font-medium">{row.action}</span><span className="ml-2 text-text-subtle">{row.resource_type} · {row.resource_type === 'container' ? displayDockerId(row.resource_name) : row.resource_name}</span></div><span className={row.result === 'success' ? 'text-success' : 'text-red-400'}>{row.result}</span><span className="text-right text-text-subtle">{row.ip}</span></div>)}</div>}</ResourceFrame> }
