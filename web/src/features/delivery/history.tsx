import {useQuery} from '@tanstack/react-query'
import {Link, useNavigate} from '@tanstack/react-router'
import {useState} from 'react'
import {Button} from '../../components/ui/button'
import {Input} from '../../components/ui/input'
import {Select, SelectContent, SelectItem, SelectTrigger, SelectValue} from '../../components/ui/select'
import {ListPagination} from '../../components/ui/list-pagination'
import {LoadingState} from '../../components/ui/loading-state'
import {ErrorState} from '../../components/ui/error-state'
import {api} from '../../lib/api'
import {useDateTime} from '../../lib/time-zone'
import {useUIStore, type ListPageSize} from '../../stores/ui'
import {ReleaseStatus} from './releases'
import {recordOperationLabel, shortCommit, type DeliveryHistoryPage, type DeliveryHistoryRecord} from './types'

const operations = ['check', 'deploy', 'retry', 'manual_rollback', 'auto_rollback', 'rollback_failed', 'start', 'stop', 'restart', 'detach']
const statuses = ['succeeded', 'failed', 'partial_failed', 'running', 'pending', 'validating', 'pulling', 'deploying', 'verifying', 'interrupted', 'rolling_back', 'rolled_back', 'rollback_failed', 'canceled']

export function DeliveryHistory({zh}: {zh: boolean}) {
  const [source, setSource] = useState('all'), [operation, setOperation] = useState('all'), [status, setStatus] = useState('all'), [search, setSearch] = useState('')
  const [page, setPage] = useState(0), [pageSize, setPageSize] = useState<ListPageSize>(20)
  const {formatDateTime} = useDateTime()
  const navigate = useNavigate()
  const setCurrentNode = useUIStore(state => state.setCurrentNodeID)
  const sources = [{value: 'all', label: zh ? '全部来源' : 'All sources'}, {value: 'continuous_delivery', label: zh ? '批量交付' : 'Batch delivery'}, {value: 'project_cd', label: zh ? '节点项目 CD' : 'Node project CD'}]
  const operationItems = [{value: 'all', label: zh ? '全部操作' : 'All operations'}, ...operations.map(value => ({value, label: recordOperationLabel(value, zh)}))]
  const statusItems = [{value: 'all', label: zh ? '全部状态' : 'All statuses'}, ...statuses.map(value => ({value, label: <ReleaseStatus status={value} zh={zh}/>}))]
  const query = useQuery({
    queryKey: ['delivery-history', source, operation, status, search, page, pageSize],
    queryFn: () => {
      const params = new URLSearchParams({page: String(page + 1), page_size: String(pageSize)})
      if (source !== 'all') params.set('source', source)
      if (operation !== 'all') params.set('operation', operation)
      if (status !== 'all') params.set('status', status)
      if (search.trim()) params.set('search', search.trim())
      return api<DeliveryHistoryPage>(`/delivery-records?${params}`)
    },
    refetchInterval: 5_000,
  })
  const openProject = async (record: DeliveryHistoryRecord) => {
    if (record.source === 'project_cd' && record.node_id) {
      if (await setCurrentNode(record.node_id)) await navigate({to: '/projects/$backend/$projectName', params: {backend: 'compose', projectName: record.project_name}, search: {view: 'cd'}})
    } else {
      await navigate({to: '/continuous-delivery/$projectName', params: {projectName: record.project_name}, search: {view: 'records'}})
    }
  }
  return <div className="flex min-w-0 flex-col gap-3" data-testid="delivery-history">
    <div className="flex min-w-0 flex-wrap gap-2">
      <Input className="min-w-0 flex-1 sm:min-w-52" maxLength={256} aria-label={zh ? '搜索项目或节点' : 'Search projects or nodes'} placeholder={zh ? '搜索项目或节点' : 'Search projects or nodes'} value={search} onChange={event => {setSearch(event.target.value); setPage(0)}}/>
      <Select items={sources} value={source} onValueChange={value => {if (value) {setSource(value); setPage(0)}}}><SelectTrigger className="w-44" aria-label={zh ? '记录来源' : 'Record source'}><SelectValue/></SelectTrigger><SelectContent>{sources.map(item => <SelectItem key={item.value} value={item.value}>{item.label}</SelectItem>)}</SelectContent></Select>
      <Select items={operationItems} value={operation} onValueChange={value => {if (value) {setOperation(value); setPage(0)}}}><SelectTrigger className="w-40" aria-label={zh ? '操作类型' : 'Operation type'}><SelectValue/></SelectTrigger><SelectContent>{operationItems.map(item => <SelectItem key={item.value} value={item.value}>{item.label}</SelectItem>)}</SelectContent></Select>
      <Select items={statusItems} value={status} onValueChange={value => {if (value) {setStatus(value); setPage(0)}}}><SelectTrigger className="w-36" aria-label={zh ? '记录状态' : 'Record status'}><SelectValue/></SelectTrigger><SelectContent>{statusItems.map(item => <SelectItem key={item.value} value={item.value}>{item.label}</SelectItem>)}</SelectContent></Select>
    </div>
    {query.isPending ? <LoadingState label={zh ? '正在加载交付记录' : 'Loading delivery records'}/> : query.isError ? <ErrorState description={query.error.message}/> : <>
      {!query.data.items.length && <p className="rounded-lg bg-muted/40 p-8 text-center text-sm text-muted-foreground">{zh ? '暂无匹配的交付记录' : 'No matching delivery records'}</p>}
      {query.data.items.map(record => <details key={record.id} className="min-w-0 rounded-lg bg-surface p-3" data-testid="delivery-history-row" data-record-id={record.id}>
        <summary className="cursor-pointer list-none"><div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-sm">
          <span className="font-medium break-all">{record.project_name || `${zh ? '已删除项目' : 'Deleted project'} #${record.project_id}`}</span>
          <span className="rounded-md bg-muted/60 px-1.5 py-0.5 text-xs text-muted-foreground">{record.source === 'project_cd' ? (zh ? '节点项目 CD' : 'Node project CD') : (zh ? '批量交付' : 'Batch delivery')}</span>
          <span>#{record.record_id} · {recordOperationLabel(record.operation, zh)}</span><ReleaseStatus status={record.status} zh={zh}/>
          <span className="min-w-0 truncate text-xs text-muted-foreground">{record.source === 'project_cd' ? record.node_name || record.node_id : `${record.node_ids.length} ${zh ? '个节点' : 'nodes'}`}</span>
          <span className="font-mono text-xs text-muted-foreground">{shortCommit(record.commit_sha)}</span>
          <time className="ml-auto text-xs text-muted-foreground">{formatDateTime(record.created_at)}</time>
        </div></summary>
        <div className="mt-3 flex min-w-0 flex-col gap-3 text-xs">
          {record.failure_reason && <p className="break-words text-destructive">{record.failure_reason}</p>}
          <dl className="grid min-w-0 gap-2 rounded-lg bg-muted/40 p-3 sm:grid-cols-2 [&_dd]:break-all [&_dt]:text-muted-foreground">
            <div><dt>Commit</dt><dd>{record.commit_sha || '—'}</dd></div><div><dt>{zh ? '配置 Hash' : 'Configuration hash'}</dt><dd>{record.config_hash || '—'}</dd></div>
            <div><dt>{zh ? '节点' : 'Nodes'}</dt><dd>{record.node_ids.join(', ') || '—'}</dd></div><div><dt>{zh ? '执行人' : 'Actor'}</dt><dd>{record.actor_name || '—'}</dd></div>
            {record.release_id && <div><dt>Release</dt><dd>#{record.release_id}</dd></div>}
            {record.source_record_id && <div><dt>{zh ? '来源记录' : 'Source record'}</dt><dd>#{record.source_record_id}</dd></div>}
          </dl>
          <div className="flex flex-wrap items-center gap-3">
            {record.task_id && <Link to="/tasks" hash={record.task_id} className="underline underline-offset-2">{zh ? '任务日志' : 'Task logs'}</Link>}
            {!!record.project_name && <Button variant="outline" size="sm" disabled={record.source === 'project_cd' && !record.node_exists} onClick={() => void openProject(record)}>{record.source === 'project_cd' ? (zh ? '查看项目 CD' : 'Open project CD') : (zh ? '查看交付项目' : 'Open delivery project')}</Button>}
          </div>
        </div>
      </details>)}
      <ListPagination total={query.data.total} page={page} pageCount={Math.max(1, Math.ceil(query.data.total / pageSize))} pageSize={pageSize} setPage={setPage} setPageSize={size => {setPageSize(size); setPage(0)}} zh={zh}/>
    </>}
  </div>
}
