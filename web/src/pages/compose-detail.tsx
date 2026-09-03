import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useParams } from '@tanstack/react-router'
import { CheckCircle2, ChevronLeft, CircleAlert, CircleStop, Download, FileCheck2, Hammer, ListTodo, PanelTopClose, Play, PowerOff, RefreshCw, Rocket, Save, Square, Trash2 } from 'lucide-react'
import { lazy, type ReactNode, Suspense, useEffect, useState } from 'react'
import { Button } from '../components/ui/button'
import { Alert, AlertAction, AlertDescription, AlertTitle } from '../components/ui/alert'
import { Card, CardContent } from '../components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '../components/ui/dialog'
import { ErrorState } from '../components/ui/error-state'
import { LoadingState } from '../components/ui/loading-state'
import { Progress } from '../components/ui/progress'
import { Spinner } from '../components/ui/spinner'
import { StatusBadge } from '../components/ui/status-badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'
import { Tabs, TabsList, TabsTrigger } from '../components/ui/tabs'
import { TooltipHint } from '../components/ui/tooltip-hint'
import { TakeoverWarningDialog } from '../features/compose/takeover-warning-dialog'
import type { Project } from '../features/compose/types'
import { LogTailSelect } from '../features/containers/log-tail-select'
import { useLogAutoScroll } from '../features/containers/use-log-auto-scroll'
import type { ContainerMetrics, ContainerSummary } from '../features/containers/types'
import { api } from '../lib/api'
import { nodePath } from '../lib/nodes'
import { useI18n } from '../lib/i18n'
import { confirmDialog, promptDialog } from '../stores/dialog'
import { useUIStore } from '../stores/ui'
import { ResourceFrame } from './images'

const Monaco = lazy(() => import('@monaco-editor/react'))
type View = 'Files' | 'Services' | 'Logs'
interface ComposeTask { id: string; type: string; name: string; status: string; progress: number; message: string }
interface TaskLog { id: number; level: string; message: string; created_at: string }
interface ComposeOperation { action: string; taskID: string }
const statusTone = (status: string) => status === 'running' ? 'success' : status === 'degraded' ? 'warning' : 'neutral'
const stateTone = (state: string) => state === 'running' ? 'success' : 'neutral'

export function ComposeDetailPage() {
  const { backend, projectName } = useParams({ from: '/projects/$backend/$projectName' })
  const encodedName = encodeURIComponent(projectName)
  const navigate = useNavigate()
  const client = useQueryClient()
  const { t, language } = useI18n()
  const zh = language === 'zh-CN'
  const theme = useUIStore((state) => state.theme)
  const nodeID = useUIStore((state) => state.currentNodeID)
  const logTail = useUIStore((state) => state.logTail)
  const dark = theme === 'dark' || (theme === 'system' && matchMedia('(prefers-color-scheme: dark)').matches)
  const query = useQuery({ queryKey: ['project', nodeID, backend, projectName], queryFn: () => api<Project>(nodePath(nodeID, `/projects/${encodeURIComponent(backend)}/${encodedName}`)), enabled: backend === 'compose' })
  const [view, setView] = useState<View>('Files')
  const [file, setFile] = useState('compose')
  const [compose, setCompose] = useState('')
  const [environment, setEnvironment] = useState('')
  const [notice, setNotice] = useState('')
  const [takeoverOpen, setTakeoverOpen] = useState(false)
  const [operation, setOperation] = useState<ComposeOperation | null>(null)
  const [operationOpen, setOperationOpen] = useState(false)

  useEffect(() => {
    if (!query.data) return
    setCompose(query.data.compose)
    setEnvironment(query.data.environment)
    if (!query.data.managed) setView('Services')
  }, [query.data])

  const services = useQuery({
    queryKey: ['project-services', nodeID, projectName],
    queryFn: async () => {
      const [rows, metrics] = await Promise.all([
        api<ContainerSummary[]>(nodePath(nodeID, `/projects/compose/${encodedName}/services`)),
        api<ContainerMetrics[]>(nodePath(nodeID, '/containers/metrics')).catch(() => []),
      ])
      const metricsByID = new Map(metrics.map((row) => [row.id, row]))
      return rows.map((row) => ({ ...row, ...metricsByID.get(row.id) }))
    },
    enabled: view === 'Services',
    refetchInterval: 5_000,
  })
  const logs = useQuery({ queryKey: ['project-logs', nodeID, projectName, logTail], queryFn: () => api<{ logs: string }>(nodePath(nodeID, `/projects/compose/${encodedName}/logs?tail=${logTail}`)), enabled: view === 'Logs', refetchInterval: 3_000, retry: false })
  const save = useMutation({
    mutationFn: () => api<Project>(nodePath(nodeID, `/projects/compose/${encodedName}`), { method: 'PUT', body: JSON.stringify({ compose, environment }) }),
    onSuccess: (row) => {
      client.setQueryData(['project', nodeID, backend, projectName], row)
      setNotice(zh ? '已保存。' : 'Saved.')
    },
    onError: (error) => setNotice(error.message),
  })
  const validate = useMutation({
    mutationFn: () => api(nodePath(nodeID, `/projects/compose/${encodedName}/validate`), { method: 'POST', body: JSON.stringify({ compose, environment }) }),
    onSuccess: () => setNotice(zh ? 'Compose 配置有效。' : 'Compose configuration is valid.'),
    onError: (error) => setNotice(error.message),
  })
  const action = useMutation({
    mutationFn: (name: string) => api<ComposeTask>(nodePath(nodeID, `/projects/compose/${encodedName}/actions/${name}`), { method: 'POST' }),
    onMutate: (name) => {
      setNotice('')
      setOperation({ action: name, taskID: '' })
      setOperationOpen(true)
    },
    onSuccess: (task, name) => {
      setOperation({ action: name, taskID: task.id })
      client.setQueryData(['compose-action-task', nodeID, task.id], task)
      setNotice(zh ? `${composeActionLabel(name, zh)}任务已启动。` : `${composeActionLabel(name, zh)} task started.`)
      void client.invalidateQueries({ queryKey: ['tasks', 'current', nodeID] })
    },
    onError: (error) => setNotice(error.message),
  })
  const trackedTask = useQuery({
    queryKey: ['compose-action-task', nodeID, operation?.taskID],
    queryFn: () => api<ComposeTask>(nodePath(nodeID, `/tasks/${encodeURIComponent(operation?.taskID || '')}`)),
    enabled: !!operation?.taskID,
    refetchInterval: (result) => {
      const status = result.state.data?.status
      return !status || status === 'pending' || status === 'running' ? 1_000 : false
    },
  })
  const taskRunning = trackedTask.data?.status === 'pending' || trackedTask.data?.status === 'running'
  const taskLogs = useQuery({
    queryKey: ['compose-action-logs', nodeID, operation?.taskID],
    queryFn: () => api<TaskLog[]>(nodePath(nodeID, `/tasks/${encodeURIComponent(operation?.taskID || '')}/logs`)),
    enabled: !!operation?.taskID,
    refetchInterval: taskRunning ? 1_000 : false,
  })
  const cancelAction = useMutation({
    mutationFn: (taskID: string) => api(nodePath(nodeID, `/tasks/${encodeURIComponent(taskID)}/cancel`), { method: 'POST' }),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['compose-action-task', nodeID, operation?.taskID] })
      void client.invalidateQueries({ queryKey: ['tasks', 'current', nodeID] })
    },
  })
  useEffect(() => {
    const task = trackedTask.data
    if (!task || task.status === 'pending' || task.status === 'running') return
    setNotice(task.status === 'success'
      ? (zh ? `${composeActionLabel(operation?.action || '', zh)}完成。` : `${composeActionLabel(operation?.action || '', zh)} completed.`)
      : (zh ? `${composeActionLabel(operation?.action || '', zh)}${task.status === 'canceled' ? '已取消' : '失败'}：${task.message}` : `${composeActionLabel(operation?.action || '', zh)} ${task.status}: ${task.message}`))
    void Promise.all([
      client.invalidateQueries({ queryKey: ['project', nodeID, backend, projectName] }),
      client.invalidateQueries({ queryKey: ['project-services', nodeID, projectName] }),
      client.invalidateQueries({ queryKey: ['project-logs', nodeID, projectName] }),
      client.invalidateQueries({ queryKey: ['projects', nodeID] }),
      client.invalidateQueries({ queryKey: ['tasks', 'current', nodeID] }),
      client.invalidateQueries({ queryKey: ['compose-action-logs', nodeID, operation?.taskID] }),
    ])
  }, [backend, client, nodeID, operation?.action, operation?.taskID, projectName, trackedTask.data, zh])
  const deploy = async () => {
    if (action.isPending || taskRunning) {
      setOperationOpen(true)
      return
    }
    const changes = [compose !== query.data?.compose && 'compose.yml', environment !== query.data?.environment && '.env'].filter(Boolean)
    const firstTakeoverDeploy = query.data?.metadata?.origin === 'takeover' && !query.data.metadata.last_deployed_at
    if ((changes.length || firstTakeoverDeploy) && !await confirmDialog({ title: firstTakeoverDeploy ? (zh ? '首次由 SUMA 部署？' : 'First SUMA deployment?') : t('deployChanges'), description: firstTakeoverDeploy ? (zh ? '现有运行态可能与接管草稿不同，Compose 可能重建容器、改变网络或处理孤立容器。' : 'Runtime state may differ from the takeover draft; Compose may recreate containers, change networks, or handle orphans.') : t('deployChangesDescription', { files: changes.join(' / ') }), confirmLabel: zh ? '确认部署' : 'Deploy' })) return
    if (changes.length) await save.mutateAsync()
    action.reset()
    cancelAction.reset()
    await action.mutateAsync('update')
  }
  const remove = async () => {
    if (await promptDialog({ title: t('removeProject'), description: t('removeProjectDescription'), confirmLabel: t('remove'), danger: true, input: { label: t('typeToConfirm', { value: projectName }), requiredValue: projectName } }) !== projectName) return
    await api(nodePath(nodeID, `/projects/compose/${encodedName}?confirm=${encodedName}`), { method: 'DELETE' })
    void navigate({ to: '/projects' })
  }
  const run = async (name: string) => {
    if (action.isPending || taskRunning) {
      setOperationOpen(true)
      return
    }
    if (name === 'down' && !await confirmDialog({ title: t('composeDown'), description: t('composeDownDescription', { name: projectName }), confirmLabel: 'Down', danger: true })) return
    action.reset()
    cancelAction.reset()
    action.mutate(name)
  }

  if (backend !== 'compose') return <ErrorState title={zh ? '后端尚不可用' : 'Backend unavailable'} description={zh ? '当前版本只实现 Docker Compose Project；Swarm Stack 仅预留领域模型。' : 'This version implements Docker Compose Projects only; Swarm Stack remains a model extension point.'} />
  if (query.isPending) return <LoadingState label={zh ? '正在加载 Project' : 'Loading Project'} rows={6} />
  if (query.isError || !query.data) return <ErrorState title={zh ? '无法加载 Compose 项目' : 'Unable to load Compose project'} description={query.error?.message || (zh ? '服务端没有返回项目数据。' : 'The server did not return project data.')} />

  const project = query.data
  const dirty = compose !== project.compose || environment !== project.environment
  const operationActive = action.isPending || taskRunning
  const actionButton = (name: string, icon: ReactNode) => <Button key={name} variant={name === 'down' ? 'destructive' : 'outline'} disabled={operationActive} onClick={() => void run(name)}>{operationActive && operation?.action === name ? <Spinner className="size-4" /> : icon}{composeActionLabel(name, zh)}</Button>
  const headerActions = <div className="flex flex-wrap items-center gap-2">
    <StatusBadge tone={statusTone(project.status)}>{project.status}</StatusBadge>
    {project.managed && actionButton('stop', <Square size={16} />)}
    {project.managed && actionButton('restart', <RefreshCw size={16} />)}
    {project.managed && actionButton('pull', <Download size={16} />)}
    {project.managed && actionButton('build', <Hammer size={16} />)}
    {project.managed && actionButton('down', <PowerOff size={16} />)}
    {project.managed && <Button disabled={operationActive} onClick={() => void run('up')}>{operationActive && operation?.action === 'up' ? <Spinner className="size-4" /> : <Play size={16} />}{composeActionLabel('up', zh)}</Button>}
    {!project.managed && <Button onClick={() => setTakeoverOpen(true)}><Download />{zh ? '接管' : 'Take over'}</Button>}
  </div>
  return <div className="flex w-full flex-col items-start gap-4">
    <Button variant="ghost" size="sm" className="-ml-2 text-muted-foreground" onClick={() => void navigate({ to: '/projects' })}><ChevronLeft />{zh ? '项目' : 'Projects'}</Button>
    <ResourceFrame title={projectName} detail={project.managed ? (dirty ? (zh ? 'SUMA 托管 · 有未保存更改' : 'SUMA managed · Unsaved changes') : (zh ? 'SUMA 托管 · 已保存' : 'SUMA managed · Saved')) : (zh ? '从 Docker Compose 标签发现 · 外部' : 'Discovered from Docker Compose labels · External')} action={headerActions}>
      <div className="flex w-full flex-col items-start gap-3">
        {!project.managed && <Alert className="w-full pr-28"><AlertTitle>{zh ? '外部 Compose Project' : 'External Compose Project'}</AlertTitle><AlertDescription>{zh ? 'SUMA 已按 Compose Project 聚合全部 Service 和容器实例。接管会分析完整 Project 并生成可复核的配置草稿，不会立即部署。' : 'SUMA aggregated every Service and Container Instance. Takeover analyzes the complete Project and creates a reviewable draft without deploying it.'}</AlertDescription><AlertAction><Button size="sm" variant="outline" onClick={() => setTakeoverOpen(true)}><Download />{zh ? '接管' : 'Take over'}</Button></AlertAction></Alert>}
        {project.managed && project.metadata?.origin === 'takeover' && !project.metadata.last_deployed_at && <Alert className="w-full"><AlertTitle>{zh ? '尚未由 SUMA 部署' : 'Not deployed by SUMA yet'}</AlertTitle><AlertDescription>{zh ? '接管没有改变现有容器。首次部署可能重建容器、改变资源或处理孤立容器，请在执行前复核。' : 'Takeover did not change existing containers. The first deployment may recreate containers, change resources, or handle orphans; review before continuing.'}</AlertDescription></Alert>}
        {notice && <p className={`text-sm ${action.isError ? 'text-red-600 dark:text-red-400' : 'text-muted-foreground'}`}>{notice}</p>}
        <Tabs value={view} onValueChange={(value) => setView(value as View)}>
          <TabsList variant="line">
            {(project.managed ? ['Files', 'Services', 'Logs'] : ['Services']).map((name) => <TabsTrigger key={name} value={name}>{name === 'Files' ? (zh ? 'Compose 文件' : 'Compose files') : name === 'Services' ? (zh ? '服务' : 'Services') : (zh ? '日志' : 'Logs')}</TabsTrigger>)}
          </TabsList>
        </Tabs>
        {view === 'Files' && project.managed && <ComposeFiles dark={dark} file={file} compose={compose} environment={environment} dirty={dirty} notice={notice} zh={zh} setFile={setFile} setCompose={setCompose} setEnvironment={setEnvironment} onRemove={() => void remove()} onValidate={() => validate.mutate()} onSave={() => save.mutate()} onDeploy={() => void deploy()} validating={validate.isPending} saving={save.isPending} actionBusy={operationActive} deploying={operationActive && operation?.action === 'update'} />}
        {view === 'Services' && <Services rows={services.data} loading={services.isPending} error={services.error?.message} zh={zh} />}
        {view === 'Logs' && project.managed && <Logs value={logs.data?.logs} loading={logs.isPending} error={logs.isError} zh={zh} sourceKey={`${nodeID}\n${projectName}\n${logTail}`} />}
      </div>
    </ResourceFrame>
    <TakeoverWarningDialog open={takeoverOpen} projectName={projectName} zh={zh} onOpenChange={setTakeoverOpen} onContinue={() => { setTakeoverOpen(false); void navigate({ to: '/projects/$backend/$projectName/takeover', params: { backend: 'compose', projectName } }) }} />
    <ComposeActionDialog
      open={operationOpen}
      operation={operation}
      task={trackedTask.data}
      logs={taskLogs.data ?? []}
      submitting={action.isPending}
      loading={trackedTask.isPending && !!operation?.taskID}
      error={action.error?.message || trackedTask.error?.message || taskLogs.error?.message || cancelAction.error?.message}
      canceling={cancelAction.isPending}
      zh={zh}
      projectName={projectName}
      onOpenChange={setOperationOpen}
      onCancel={() => { if (operation?.taskID) cancelAction.mutate(operation.taskID) }}
      onViewTasks={() => { setOperationOpen(false); void navigate({ to: '/tasks' }) }}
    />
  </div>
}

function composeActionLabel(action: string, zh: boolean) {
  const labels: Record<string, [string, string]> = {
    up: ['启动', 'Start'],
    down: ['Down', 'Down'],
    stop: ['停止', 'Stop'],
    restart: ['重启', 'Restart'],
    pull: ['拉取', 'Pull'],
    build: ['构建', 'Build'],
    update: ['更新', 'Update'],
  }
  return labels[action]?.[zh ? 0 : 1] ?? action
}

function ComposeActionDialog({ open, operation, task, logs, submitting, loading, error, canceling, zh, projectName, onOpenChange, onCancel, onViewTasks }: { open: boolean; operation: ComposeOperation | null; task?: ComposeTask; logs: TaskLog[]; submitting: boolean; loading: boolean; error?: string; canceling: boolean; zh: boolean; projectName: string; onOpenChange: (open: boolean) => void; onCancel: () => void; onViewTasks: () => void }) {
  const status = task?.status || (submitting ? 'submitting' : error ? 'failed' : 'pending')
  const running = submitting || status === 'pending' || status === 'running'
  const label = zh
    ? ({ submitting: '正在提交', pending: '等待中', running: '执行中', success: '已完成', failed: '失败', canceled: '已取消' }[status] ?? status)
    : ({ submitting: 'Submitting', pending: 'Pending', running: 'Running', success: 'Completed', failed: 'Failed', canceled: 'Canceled' }[status] ?? status)
  const tone = status === 'success' ? 'success' : status === 'failed' || status === 'canceled' ? 'critical' : status === 'running' || status === 'submitting' ? 'warning' : 'neutral'
  const actionName = composeActionLabel(operation?.action || '', zh)
  const progress = task?.progress ?? 0

  return <Dialog open={open} onOpenChange={onOpenChange}>
    {open && <DialogContent className="sm:max-w-lg">
      <DialogHeader>
        <div className="flex items-center gap-2 pr-8">
          <DialogTitle>{zh ? `Compose ${actionName}进度` : `Compose ${actionName} progress`}</DialogTitle>
          <StatusBadge tone={tone}>{label}</StatusBadge>
        </div>
        <DialogDescription className="break-all">{projectName}</DialogDescription>
      </DialogHeader>
      <div className="flex flex-col gap-3">
        <div className="flex items-center justify-between gap-4 text-sm">
          <span className="text-muted-foreground">{zh ? '任务进度' : 'Task progress'}</span>
          <span className="font-medium tabular-nums">{progress}%</span>
        </div>
        <Progress value={progress} />
        <div className="flex min-h-6 items-start gap-2 text-sm">
          {running ? <Spinner className="mt-0.5 size-4 shrink-0 text-muted-foreground" /> : status === 'success' ? <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-emerald-600" /> : <CircleAlert className="mt-0.5 size-4 shrink-0 text-destructive" />}
          <p className="break-all text-muted-foreground">{task?.message || (submitting ? (zh ? '正在创建后台任务…' : 'Creating background task…') : loading ? (zh ? '正在读取任务状态…' : 'Loading task status…') : (zh ? '等待 Docker Compose 输出…' : 'Waiting for Docker Compose output…'))}</p>
        </div>
        <div className="flex items-center justify-between gap-4 border-t pt-3">
          <span className="text-sm font-medium">{zh ? '实时输出' : 'Live output'}</span>
          {operation?.taskID && <span className="font-mono text-[11px] text-muted-foreground">{operation.taskID}</span>}
        </div>
        <div className="max-h-64 min-h-24 overflow-y-auto rounded-lg bg-muted/50 p-3">
          {logs.length === 0 ? <p className="text-center text-xs text-muted-foreground">{zh ? '等待任务输出…' : 'Waiting for task output…'}</p> : <div className="flex flex-col gap-1.5">
            {logs.map((log) => <div key={log.id} className="flex items-baseline gap-3">
              <span className="shrink-0 font-mono text-[11px] text-muted-foreground tabular-nums">{new Date(log.created_at).toLocaleTimeString(zh ? 'zh-CN' : 'en-US')}</span>
              <span className={`font-mono text-xs break-all ${log.level === 'error' ? 'text-destructive' : ''}`}>{log.message}</span>
            </div>)}
          </div>}
        </div>
        {error && <ErrorState description={error} />}
        <p className="text-xs text-muted-foreground">{running ? (zh ? '关闭窗口不会停止操作，任务会继续在后台执行。' : 'Closing this window does not stop the operation; it continues in the background.') : (zh ? '可在任务中心查看完整记录。' : 'You can review the complete record in the Task Center.')}</p>
      </div>
      <DialogFooter className="mt-1">
        {task && running && <Button type="button" variant="destructive" disabled={canceling} onClick={onCancel}>{canceling ? <Spinner /> : <CircleStop />}{zh ? '取消任务' : 'Cancel task'}</Button>}
        {task && !running && <Button type="button" variant="outline" onClick={onViewTasks}><ListTodo />{zh ? '查看任务' : 'View task'}</Button>}
        <Button type="button" variant="outline" onClick={() => onOpenChange(false)}><PanelTopClose />{zh ? '关闭窗口' : 'Close window'}</Button>
      </DialogFooter>
    </DialogContent>}
  </Dialog>
}

function ComposeFiles({ dark, file, compose, environment, dirty, notice, zh, setFile, setCompose, setEnvironment, onRemove, onValidate, onSave, onDeploy, validating, saving, actionBusy, deploying }: { dark: boolean; file: string; compose: string; environment: string; dirty: boolean; notice: string; zh: boolean; setFile: (file: string) => void; setCompose: (value: string) => void; setEnvironment: (value: string) => void; onRemove: () => void; onValidate: () => void; onSave: () => void; onDeploy: () => void; validating: boolean; saving: boolean; actionBusy: boolean; deploying: boolean }) {
  const files = [{ path: 'compose', label: 'compose.yml', content: compose, language: 'yaml' as const }, { path: 'environment', label: '.env', content: environment, language: 'plaintext' as const }]
  const selected = files.find((entry) => entry.path === file) || files[0]
  return <Card className="w-full">
    <CardContent className="flex w-full flex-col gap-3">
      <Tabs value={selected.path} onValueChange={(value) => setFile(String(value))}>
        <TabsList>
          {files.map((entry) => <TabsTrigger key={entry.path} value={entry.path}>{entry.label}</TabsTrigger>)}
        </TabsList>
      </Tabs>
      <div className="h-[52vh] overflow-hidden rounded-lg ring-1 ring-foreground/10"><Suspense fallback={<div className="grid h-full place-items-center"><Spinner className="size-5 text-muted-foreground" /></div>}><Monaco key={selected.path} language={selected.language} theme={dark ? 'vs-dark' : 'light'} value={selected.content} onChange={(value) => selected.path === 'compose' ? setCompose(value ?? '') : setEnvironment(value ?? '')} options={{ minimap: { enabled: false }, scrollBeyondLastLine: false, automaticLayout: true, wordWrap: 'on' }} /></Suspense></div>
      <div className="flex flex-wrap items-center justify-between gap-2"><p className="text-sm text-muted-foreground">{notice || (dirty ? (zh ? '有未保存更改' : 'Unsaved changes') : (zh ? '没有更改' : 'No changes'))}</p><div className="flex items-center gap-2"><Button variant="destructive" disabled={actionBusy} onClick={onRemove}><Trash2 size={16} />{zh ? '删除' : 'Remove'}</Button><Button variant="outline" disabled={actionBusy || validating} onClick={onValidate}>{validating ? <Spinner className="size-4" /> : <FileCheck2 size={16} />}{zh ? '校验' : 'Validate'}</Button><Button variant="outline" disabled={actionBusy || !dirty || saving} onClick={onSave}>{saving ? <Spinner className="size-4" /> : <Save size={16} />}{zh ? '保存' : 'Save'}</Button><Button disabled={actionBusy} onClick={onDeploy}>{deploying ? <Spinner className="size-4" /> : <Rocket size={16} />}{zh ? '保存并部署' : 'Save & deploy'}</Button></div></div>
    </CardContent>
  </Card>
}

const servicePorts = (row: ContainerSummary) => {
  if (!row.ports.length) return '—'
  const values = row.ports.slice(0, 3).map((port) => port.public_port ? `${port.ip || '0.0.0.0'}:${port.public_port} → ${port.private_port}/${port.type}` : `${port.private_port}/${port.type}`)
  return `${values.join(', ')}${row.ports.length > 3 ? ` +${row.ports.length - 3}` : ''}`
}
const serviceMemory = (bytes: number) => !bytes ? '—' : bytes >= 1024 ** 3 ? `${(bytes / 1024 ** 3).toFixed(2)} GB` : `${(bytes / 1024 ** 2).toFixed(0)} MB`
const serviceUptime = (seconds: number, zh: boolean) => !seconds ? '—' : seconds >= 86400 ? `${Math.floor(seconds / 86400)} ${zh ? '天' : 'd'}` : seconds >= 3600 ? `${Math.floor(seconds / 3600)} ${zh ? '小时' : 'h'}` : `${Math.max(1, Math.floor(seconds / 60))} ${zh ? '分钟' : 'm'}`
const serviceState = (state: string, zh: boolean) => zh ? ({ running: '运行中', paused: '已暂停', restarting: '重启中', exited: '已停止', dead: '异常', created: '已创建' }[state] ?? state) : state

function Services({ rows, loading, error, zh }: { rows?: ContainerSummary[]; loading: boolean; error?: string; zh: boolean }) {
  if (loading) return <LoadingState compact label={zh ? '正在加载项目服务' : 'Loading project services'} />
  if (error) return <ErrorState description={error} />
  return <Table className="w-full">
    <TableHeader>
      <TableRow>
        <TableHead className="min-w-[190px]">{zh ? '服务 / 容器' : 'Service / container'}</TableHead>
        <TableHead>{zh ? '镜像' : 'Image'}</TableHead>
        <TableHead className="min-w-[140px]">{zh ? '状态 / 运行时间' : 'State / uptime'}</TableHead>
        <TableHead className="min-w-[120px]">{zh ? '资源' : 'Resources'}</TableHead>
        <TableHead className="min-w-[190px]">{zh ? '端口' : 'Ports'}</TableHead>
        <TableHead className="min-w-[150px]">{zh ? '创建时间' : 'Created'}</TableHead>
      </TableRow>
    </TableHeader>
    <TableBody>
      {(rows ?? []).length === 0 && <TableRow><TableCell colSpan={6} className="h-24 text-center text-muted-foreground">{zh ? '项目尚未创建容器，请先启动项目。' : 'No containers have been created. Start the project first.'}</TableCell></TableRow>}
      {(rows ?? []).map((row) => (
        <TableRow key={row.id}>
          <TableCell><div className="flex flex-col gap-0.5"><span className="font-medium">{row.labels['com.docker.compose.service'] || row.name}</span><a href={`/containers/${row.id}`} className="text-xs text-muted-foreground hover:text-foreground hover:underline">{row.name}</a><span className="font-mono text-[11px] text-muted-foreground">{row.id.slice(0, 12)}{row.labels['com.docker.compose.container-number'] ? ` · #${row.labels['com.docker.compose.container-number']}` : ''}</span></div></TableCell>
          <TableCell><TooltipHint content={row.image}><span className="block max-w-72 truncate text-muted-foreground">{row.image}</span></TooltipHint></TableCell>
          <TableCell><div className="flex flex-col items-start gap-1"><StatusBadge tone={stateTone(row.state)}>{serviceState(row.state, zh)}</StatusBadge><TooltipHint content={row.status}><span className="max-w-40 truncate text-xs text-muted-foreground">{serviceUptime(row.uptime_seconds, zh)}</span></TooltipHint></div></TableCell>
          <TableCell>{row.state === 'running' ? <div className="flex flex-col"><span className="tabular-nums">CPU {row.cpu_percent.toFixed(1)}%</span><span className="text-xs text-muted-foreground tabular-nums">{serviceMemory(row.memory_bytes)}</span></div> : '—'}</TableCell>
          <TableCell className="font-mono text-xs"><TooltipHint content={servicePorts(row)}><span className="block max-w-64 truncate">{servicePorts(row)}</span></TooltipHint></TableCell>
          <TableCell className="text-xs text-muted-foreground tabular-nums">{new Date(row.created).toLocaleString(zh ? 'zh-CN' : 'en-US')}</TableCell>
        </TableRow>
      ))}
    </TableBody>
  </Table>
}

function Logs({ value, loading, error, zh, sourceKey }: { value?: string; loading: boolean; error: boolean; zh: boolean; sourceKey: string }) {
  const { viewportRef, onScroll } = useLogAutoScroll<HTMLDivElement>(value || '', sourceKey)
  return <div className="flex w-full flex-col gap-3">
    <div className="flex justify-end"><LogTailSelect zh={zh} /></div>
    {loading ? <LoadingState rows={6} label={zh ? '正在加载 Compose 日志' : 'Loading Compose logs'} /> : <Card className="h-[55vh] w-full">
      <CardContent ref={viewportRef} onScroll={onScroll} className="min-h-0 flex-1 overflow-auto">
        <pre className="font-mono text-xs leading-relaxed whitespace-pre-wrap">{value || ''}</pre>
        {!value && <p className="text-sm text-muted-foreground">{error ? (zh ? '没有可用日志，请先启动项目。' : 'No Compose logs available. Start the project first.') : ''}</p>}
      </CardContent>
    </Card>}
  </div>
}
