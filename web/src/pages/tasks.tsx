import { useState } from 'react'
import { useLocation, useNavigate, useSearch } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { LoadingState } from '../components/ui/loading-state'
import { ErrorState } from '../components/ui/error-state'
import { Button } from '../components/ui/button'
import { Tabs, TabsList, TabsTrigger } from '../components/ui/tabs'
import { CleanupNodeSheet } from '../features/cleanup/storage-cleanup'
import { RecordsTab } from '../features/task-center/records-tab'
import { SchedulesTab } from '../features/task-center/schedules-tab'
import { TaskRecordDetail } from '../features/task-center/task-record-detail'
import { api } from '../lib/api'
import { useI18n } from '../lib/i18n'
import type { DockerNode } from '../lib/nodes'
import { useUIStore } from '../stores/ui'
import { ResourceFrame } from './images'

type CenterTab = 'tasks' | 'schedules' | 'records'
export type CenterScope = 'current' | 'control_plane' | 'all'

export function TasksPage() {
  const { t, language } = useI18n()
  const zh = language === 'zh-CN'
  const navigate = useNavigate()
  const search = useSearch({ from: '/tasks' })
  const tab: CenterTab = search.tab === 'schedules' || search.tab === 'records' ? search.tab : 'tasks'
  const nodeID = useUIStore((state) => state.currentNodeID)
  const [scope, setScope] = useState<CenterScope>('current')
  const [cleanupOpen, setCleanupOpen] = useState(false)
  const nodes = useQuery({ queryKey: ['nodes'], queryFn: () => api<DockerNode[]>('/nodes') })
  const currentNode = nodes.data?.find(node => node.id === nodeID)
  const linkedTaskID = useLocation({ select: location => location.hash })
  return <ResourceFrame title={t('tasks')} detail={zh ? '查看执行进度、管理自动化与追溯历史' : 'Track progress, manage automation and inspect history'} action={
    <div className="flex flex-wrap items-center gap-2">
      <Tabs value={tab} onValueChange={value => void navigate({ to: '/tasks', search: value === 'tasks' ? {} : { tab: value }, replace: true })}><TabsList>
        <TabsTrigger value="tasks">{zh ? '执行中' : 'Active'}</TabsTrigger>
        <TabsTrigger value="schedules">{zh ? '自动化' : 'Automation'}</TabsTrigger>
        <TabsTrigger value="records">{zh ? '执行历史' : 'History'}</TabsTrigger>
      </TabsList></Tabs>
      <Tabs value={scope} onValueChange={value => { if (value === 'current' || value === 'control_plane' || value === 'all') setScope(value) }}><TabsList>
        <TabsTrigger value="current">{zh ? '当前节点' : 'Current node'}</TabsTrigger>
        <TabsTrigger value="control_plane">{zh ? '控制平面' : 'Control plane'}</TabsTrigger>
        <TabsTrigger value="all">{zh ? '全部' : 'All'}</TabsTrigger>
      </TabsList></Tabs>
      <Button variant="outline" disabled={!currentNode} onClick={() => setCleanupOpen(true)}>{zh ? '存储清理' : 'Storage cleanup'}</Button>
    </div>
  }>
    {cleanupOpen && currentNode && <CleanupNodeSheet key={nodeID} node={currentNode} open initialTab="preview" onOpenChange={setCleanupOpen} />}
    {nodes.isError ? <ErrorState description={nodes.error.message} /> : nodes.isPending ? <LoadingState label={zh ? '正在加载节点' : 'Loading nodes'} /> : scope === 'current' && !currentNode ? <p className="py-8 text-center text-sm text-muted-foreground">{zh ? '请选择目标节点' : 'Select a target node'}</p> : <>
      {tab === 'schedules' ? <SchedulesTab key={`${scope}:${nodeID}`} scope={scope} nodeID={nodeID} /> : <RecordsTab key={`${tab}:${scope}:${nodeID}`} scope={scope} nodeID={nodeID} active={tab === 'tasks'} />}
    </>}
    <TaskRecordDetail id={linkedTaskID || undefined} open={!!linkedTaskID} onOpenChange={open => { if (!open) void navigate({ to: '/tasks', search, hash: '', replace: true }) }} />
  </ResourceFrame>
}
