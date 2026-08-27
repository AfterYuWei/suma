import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useParams } from '@tanstack/react-router'
import { ChevronLeft, MoreHorizontal, OctagonX, Pause, Pencil, Play, RefreshCw, Square, Trash2 } from 'lucide-react'
import { lazy, useState } from 'react'
import { Badge } from '../components/ui/badge'
import { Button } from '../components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '../components/ui/card'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from '../components/ui/dropdown-menu'
import { ErrorState } from '../components/ui/error-state'
import { LoadingState } from '../components/ui/loading-state'
import { Spinner } from '../components/ui/spinner'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'
import { Tabs, TabsList, TabsTrigger } from '../components/ui/tabs'
import type { ContainerDetail } from '../features/containers/types'
import { api } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { nodePath } from '../lib/nodes'
import { confirmDialog, promptDialog } from '../stores/dialog'
import { useUIStore } from '../stores/ui'
import { ResourceFrame } from './images'

const LogViewer = lazy(() => import('../features/containers/log-viewer').then((module) => ({ default: module.LogViewer })))
const StatsView = lazy(() => import('../features/containers/stats-view').then((module) => ({ default: module.StatsView })))
const TerminalView = lazy(() => import('../features/containers/terminal-view').then((module) => ({ default: module.TerminalView })))
const tabs = ['Overview', 'Logs', 'Terminal', 'Stats', 'Inspect'] as const

export function ContainerDetailPage() {
  const nodeID = useUIStore((state) => state.currentNodeID)
  const { containerId } = useParams({ from: '/containers/$containerId' })
  const navigate = useNavigate()
  const client = useQueryClient()
  const { t, language } = useI18n()
  const zh = language === 'zh-CN'
  const hash = location.hash.slice(1)
  const initial = tabs.find((name) => name.toLowerCase() === hash) ?? 'Overview'
  const [tab, setTab] = useState<(typeof tabs)[number]>(initial)
  const query = useQuery({ queryKey: ['container', nodeID, containerId], queryFn: () => api<ContainerDetail>(nodePath(nodeID, `/containers/${containerId}`)) })
  const action = useMutation({ mutationFn: (name: string) => api(nodePath(nodeID, `/containers/${containerId}/${name}`), { method: 'POST' }), onSuccess: () => { client.invalidateQueries({ queryKey: ['container', nodeID, containerId] }); client.invalidateQueries({ queryKey: ['containers', nodeID] }) } })
  const rename = async () => { const name = await promptDialog({ title: t('renameContainer'), confirmLabel: t('save'), input: { label: t('newContainerName'), initialValue: query.data?.name } }); if (!name) return; await api(nodePath(nodeID, `/containers/${containerId}`), { method: 'PATCH', body: JSON.stringify({ name }) }); await client.invalidateQueries({ queryKey: ['container', nodeID, containerId] }) }
  const remove = async () => { const name = query.data?.name ?? containerId; if (!await confirmDialog({ title: t('removeContainer'), description: t('removeContainerDescription', { name }), confirmLabel: t('remove'), danger: true })) return; await api(nodePath(nodeID, `/containers/${containerId}`), { method: 'DELETE' }); void navigate({ to: '/containers' }) }
  const kill = async () => { const name = query.data?.name ?? containerId; if (await confirmDialog({ title: t('killContainer'), description: t('killContainerDescription', { name }), confirmLabel: zh ? '强制终止' : 'Force kill', danger: true })) action.mutate('kill') }
  if (query.isPending) return <LoadingState label={zh ? '正在加载容器详情' : 'Loading container details'} rows={6} />
  if (!query.data) return <ErrorState description={zh ? '未找到容器。' : 'Container not found.'} />
  const row = query.data
  const label = (name: (typeof tabs)[number]) => zh ? ({ Overview: '概览', Logs: '日志', Terminal: '终端', Stats: '统计', Inspect: '检查' } as const)[name] : name

  const actions = <div className="flex flex-wrap items-center gap-2">
    <Badge variant="outline" className={row.state === 'running' ? 'border-transparent bg-emerald-500/15 text-emerald-600 dark:text-emerald-400' : 'text-muted-foreground'}>{row.state}</Badge>
    {row.state === 'running'
      ? <Button variant="outline" disabled={action.isPending} onClick={() => action.mutate('stop')}>{action.isPending ? <Spinner /> : <Square />}{zh ? '停止' : 'Stop'}</Button>
      : <Button disabled={action.isPending} onClick={() => action.mutate('start')}>{action.isPending ? <Spinner /> : <Play />}{zh ? '启动' : 'Start'}</Button>}
    <Button variant="outline" disabled={action.isPending} onClick={() => action.mutate('restart')}>{action.isPending ? <Spinner /> : <RefreshCw />}{zh ? '重启' : 'Restart'}</Button>
    <DropdownMenu>
      <DropdownMenuTrigger render={<Button variant="ghost" size="icon" aria-label={zh ? '更多操作' : 'More actions'}><MoreHorizontal /></Button>} />
      <DropdownMenuContent align="end" className="w-44">
        {row.state === 'paused' ? <MenuAction label={zh ? '恢复' : 'Unpause'} icon={Play} run={() => action.mutate('unpause')} /> : <MenuAction label={zh ? '暂停' : 'Pause'} icon={Pause} run={() => action.mutate('pause')} />}
        <MenuAction label={zh ? '强制终止' : 'Kill process'} icon={OctagonX} danger run={() => void kill()} />
        <MenuAction label={t('renameContainer')} icon={Pencil} run={() => void rename()} />
        <DropdownMenuSeparator />
        <MenuAction label={t('remove')} icon={Trash2} danger run={() => void remove()} />
      </DropdownMenuContent>
    </DropdownMenu>
  </div>

  return <div className="flex w-full flex-col items-start gap-5">
    <div className="-mb-1">
      <Button variant="ghost" size="sm" className="-ml-1 text-muted-foreground hover:text-foreground" onClick={() => void navigate({ to: '/containers' })}><ChevronLeft />{t('containers')}</Button>
    </div>
    <ResourceFrame title={row.name} detail={`${row.image} · ${row.status}`} action={actions}>
    <Tabs value={tab} onValueChange={(name) => { const next = String(name); setTab(next as (typeof tabs)[number]); location.hash = next.toLowerCase() }}>
      <TabsList variant="line" className="w-full justify-start gap-4 border-b pb-2">
        {tabs.map((name) => <TabsTrigger key={name} value={name} className="flex-none px-1">{label(name)}</TabsTrigger>)}
      </TabsList>
    </Tabs>
    <div className="mt-6 w-full">
      {tab === 'Overview' && <div className="grid gap-6 lg:grid-cols-2">
        <Card>
          <CardHeader><CardTitle>{zh ? '运行信息' : 'Runtime'}</CardTitle></CardHeader>
          <CardContent><div className="divide-y divide-border">
            {([[zh ? '容器 ID' : 'Container ID', row.id], [zh ? '状态' : 'Status', row.status], ['PID', String(row.pid)], [zh ? '工作目录' : 'Working directory', row.working_directory || '—'], [zh ? '重启策略' : 'Restart policy', row.restart_policy || '—']] as [string, string][]).map(([key, value]) => (
              <div key={key} className="flex items-start justify-between gap-6 py-2 first:pt-0 last:pb-0">
                <span className="shrink-0 text-xs text-muted-foreground">{key}</span>
                <span className="min-w-0 break-all text-right font-mono text-xs">{value}</span>
              </div>
            ))}
          </div></CardContent>
        </Card>
        <Card>
          <CardHeader><CardTitle>{zh ? '环境变量' : 'Environment'}</CardTitle></CardHeader>
          <CardContent>{row.environment.length === 0 ? (
            <p className="py-6 text-center text-sm text-muted-foreground">{zh ? '无环境变量。' : 'No environment variables.'}</p>
          ) : (
            <div className="overflow-hidden rounded-lg border border-border">
              <Table>
                <TableHeader>
                  <TableRow className="bg-muted/40 hover:bg-muted/40">
                    <TableHead className="h-8">{zh ? '变量' : 'Variable'}</TableHead>
                    <TableHead className="h-8">{zh ? '值' : 'Value'}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {row.environment.map((item) => (
                    <TableRow key={item.key}>
                      <TableCell className="max-w-[180px] break-all whitespace-normal font-mono text-xs">{item.key}</TableCell>
                      <TableCell className="break-all whitespace-normal">{item.sensitive ? '••••••••' : item.value}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
          </CardContent>
        </Card>
      </div>}
      {tab === 'Logs' && <LogViewer nodeID={nodeID} containerId={containerId} />}
      {tab === 'Terminal' && <TerminalView nodeID={nodeID} containerId={containerId} />}
      {tab === 'Stats' && <StatsView nodeID={nodeID} containerId={containerId} />}
      {tab === 'Inspect' && (
        <Card>
          <CardContent>
            <pre className="max-h-[60vh] overflow-auto rounded-lg bg-muted/50 p-3 text-xs leading-relaxed"><code>{JSON.stringify(row, null, 2)}</code></pre>
          </CardContent>
        </Card>
      )}
    </div>
    </ResourceFrame>
  </div>
}

function MenuAction({ label, icon: Icon, run, danger = false }: { label: string; icon: typeof Play; run: () => void; danger?: boolean }) { return <DropdownMenuItem onClick={run} variant={danger ? 'destructive' : 'default'}><Icon size={16} />{label}</DropdownMenuItem> }
