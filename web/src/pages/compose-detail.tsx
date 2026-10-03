import { AIAnalyzeButton } from '../features/operations/workbench'
import { useDateTime } from '../lib/time-zone'
import { useImageUpdates } from '../features/image-updates/hooks'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Link,
  useBlocker,
  useNavigate,
  useParams,
  useSearch
} from '@tanstack/react-router'
import {
  CheckCircle2,
  ChevronDown,
  ChevronLeft,
  CircleAlert,
  CircleStop,
  Download,
  FileCheck2,
  ListTodo,
  PanelTopClose,
  Rocket,
  Save,
  Trash2
} from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { Button } from '../components/ui/button'
import {
  Alert,
  AlertAction,
  AlertDescription,
  AlertTitle
} from '../components/ui/alert'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle
} from '../components/ui/dialog'
import { ErrorState } from '../components/ui/error-state'
import { LoadingState } from '../components/ui/loading-state'
import { ListPagination } from '../components/ui/list-pagination'
import { useListPagination } from '../components/ui/use-list-pagination'
import { Progress } from '../components/ui/progress'
import { Spinner } from '../components/ui/spinner'
import { StatusBadge } from '../components/ui/status-badge'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow
} from '../components/ui/table'
import { Tabs, TabsList, TabsTrigger } from '../components/ui/tabs'
import { TooltipHint } from '../components/ui/tooltip-hint'
import { confirmExternalProjectCleanup } from '../features/compose/external-project-cleanup'
import {
  confirmDockerSocketMount,
  dockerSocketHeaders
} from '../features/compose/docker-socket-confirmation'
import { confirmManagedProjectRemoval } from '../features/compose/managed-project-removal'
import { TakeoverWarningDialog } from '../features/compose/takeover-warning-dialog'
import type { Project } from '../features/compose/types'
import { ProjectLogs } from '../features/project-logs/project-logs'
import { ImageUpdateCheck, ImageUpdateDetails, UpdateStatus } from '../features/image-updates/image-updates'
import { useLogAutoScroll } from '../features/containers/use-log-auto-scroll'
import type {
  ContainerMetrics,
  ContainerSummary
} from '../features/containers/types'
import { compactTaskLogs } from '../features/tasks/compact-task-logs'
import { api } from '../lib/api'
import { nodePath } from '../lib/nodes'
import { useI18n } from '../lib/i18n'
import { confirmDialog } from '../stores/dialog'
import { useUIStore } from '../stores/ui'
import { ResourceFrame } from './images'

import {
  ConfigurationDiff,
  ConfigurationOverview,
  ProjectConfiguration
} from '../components/docker/project-configuration'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger
} from '../components/ui/dropdown-menu'
import { registerProjectNavigationGuard } from '../lib/project-navigation-guard'
import { ApiError } from '../lib/api'
import { configKeys, type ConfigPath } from '../features/compose/document'
import type { ImageUpdateResult } from '../features/image-updates/types'
type View = 'Files' | 'Services' | 'Logs'
interface ComposeTask {
  id: string
  type: string
  name: string
  status: string
  progress: number
  message: string
}
interface TaskLog {
  id: number
  level: string
  message: string
  created_at: string
}
interface ComposeOperation {
  action: string
  taskID: string
}
const statusTone = (status: string) =>
  status === 'running'
    ? 'success'
    : status === 'degraded'
      ? 'warning'
      : 'neutral'
const stateTone = (state: string) =>
  state === 'running' ? 'success' : 'neutral'

export function ComposeDetailPage() {
  const { backend, projectName } = useParams({
    from: '/projects/$backend/$projectName'
  })
  const initialOperation = useSearch({
    from: '/projects/$backend/$projectName'
  })
  const encodedName = encodeURIComponent(projectName)
  const navigate = useNavigate()
  const client = useQueryClient()
  const { t, language } = useI18n()
  const zh = language === 'zh-CN'
  const nodeID = useUIStore((state) => state.currentNodeID)
  const imageUpdates = useImageUpdates(nodeID, projectName)
  const query = useQuery({
    queryKey: ['project', nodeID, backend, projectName],
    queryFn: () =>
      api<Project>(
        nodePath(
          nodeID,
          `/projects/${encodeURIComponent(backend)}/${encodedName}`
        )
      ),
    enabled: backend === 'compose'
  })
  const [view, setView] = useState<View>('Files')
  const [compose, setCompose] = useState('')
  const [environment, setEnvironment] = useState('')
  const [notice, setNotice] = useState('')
  const [baseline, setBaseline] = useState<Project | null>(null)
  const [validated, setValidated] = useState('')
  const [pendingConfiguration, setPendingConfiguration] = useState(false)
  const [conflict, setConflict] = useState(false)
  const [review, setReview] = useState<{
    compose: string
    environment: string
    revision?: string
    action: string
    socket: boolean
  } | null>(null)
  const [conflictOpen, setConflictOpen] = useState(false)
  const [location, setLocation] = useState<ConfigPath>()
  const [reviewSubmitting, setReviewSubmitting] = useState(false)
  const [takeoverOpen, setTakeoverOpen] = useState(false)
  const [operation, setOperation] = useState<ComposeOperation | null>(
    initialOperation.task
      ? {
          action: initialOperation.action || 'update',
          taskID: initialOperation.task
        }
      : null
  )
  const [operationOpen, setOperationOpen] = useState(!!initialOperation.task)
  const bypassNavigationPrompt = useRef(false)
  const dirty =
    !!baseline &&
    (pendingConfiguration || compose !== baseline.compose || environment !== baseline.environment)
  const baselineRef = useRef(baseline)
  baselineRef.current = baseline
  const signature = JSON.stringify([compose, environment])
  const draftRef = useRef({ dirty, compose, environment })
  draftRef.current = { dirty, compose, environment }
  useBlocker({
    disabled: !dirty,
    enableBeforeUnload: dirty,
    shouldBlockFn: async () =>
      bypassNavigationPrompt.current
        ? false
        : !(await confirmDialog({
            title: zh
              ? '放弃未保存的项目配置？'
              : 'Discard unsaved project configuration?',
            confirmLabel: zh ? '放弃' : 'Discard',
            danger: true
          }))
  })

  useEffect(() => {
    if (!query.data) return
    if (
      draftRef.current.dirty &&
      baselineRef.current?.name === projectName &&
      baselineRef.current.node_id === nodeID
    ) {
      if (query.data.revision !== baselineRef.current.revision)
        setConflict(true)
      return
    }
    setBaseline(query.data)
    setCompose(query.data.compose)
    setEnvironment(query.data.environment)
    setConflict(false)
    if (!query.data.managed && (baselineRef.current?.name !== projectName || baselineRef.current?.node_id !== nodeID)) setView('Services')
  }, [query.data, nodeID, projectName])
  useEffect(
    () =>
      registerProjectNavigationGuard(
        async () =>
          !draftRef.current.dirty ||
          (await confirmDialog({
            title: zh
              ? '放弃未保存的项目配置并切换节点？'
              : 'Discard unsaved project configuration and change node?',
            danger: true
          }))
      ),
    [zh]
  )

  const services = useQuery({
    queryKey: ['project-services', nodeID, projectName],
    queryFn: async () => {
      const [rows, metrics] = await Promise.all([
        api<ContainerSummary[]>(
          nodePath(nodeID, `/projects/compose/${encodedName}/services`)
        ),
        api<ContainerMetrics[]>(nodePath(nodeID, '/containers/metrics')).catch(
          () => []
        )
      ])
      const metricsByID = new Map(metrics.map((row) => [row.id, row]))
      return rows.map((row) => ({ ...row, ...metricsByID.get(row.id) }))
    },
    enabled: view === 'Services',
    refetchInterval: 5_000
  })
  const save = useMutation({
    mutationFn: (allowDockerSocket: boolean) =>
      api<Project>(nodePath(nodeID, `/projects/compose/${encodedName}`), {
        method: 'PUT',
        headers: dockerSocketHeaders(allowDockerSocket),
        body: JSON.stringify({
          compose,
          environment,
          expected_revision: baseline?.revision
        })
      }),
    onSuccess: (row) => {
      client.setQueryData(['project', nodeID, backend, projectName], row)
      setBaseline(row)
      setConflict(false)
      setNotice(zh ? '已保存。' : 'Saved.')
    },
    onError: (error) => {
      setNotice(error.message)
      if (error instanceof ApiError && error.status === 409) {
        setConflict(true)
        void query.refetch()
      }
    }
  })
  const validate = useMutation({
    mutationFn: (allowDockerSocket: boolean) =>
      api(nodePath(nodeID, `/projects/compose/${encodedName}/validate`), {
        method: 'POST',
        headers: dockerSocketHeaders(allowDockerSocket),
        body: JSON.stringify({ compose, environment })
      }),
    onSuccess: () => {
      setValidated(signature)
      setNotice(zh ? 'Compose 配置有效。' : 'Compose configuration is valid.')
    },
    onError: (error) => setNotice(error.message)
  })
  const action = useMutation({
    mutationFn: (name: string) =>
      api<ComposeTask>(
        nodePath(nodeID, `/projects/compose/${encodedName}/actions/${name}`),
        {
          method: 'POST',
          body: JSON.stringify({ expected_revision: baseline?.revision })
        }
      ),
    onMutate: (name) => {
      setNotice('')
      setOperation({ action: name, taskID: '' })
      setOperationOpen(true)
    },
    onSuccess: (task, name) => {
      setOperation({ action: name, taskID: task.id })
      client.setQueryData(['compose-action-task', nodeID, task.id], task)
      setNotice(
        zh
          ? `${composeActionLabel(name, zh)}任务已启动。`
          : `${composeActionLabel(name, zh)} task started.`
      )
      void client.invalidateQueries({ queryKey: ['tasks', 'current', nodeID] })
    },
    onError: (error) => setNotice(error.message)
  })
  const trackedTask = useQuery({
    queryKey: ['compose-action-task', nodeID, operation?.taskID],
    queryFn: () =>
      api<ComposeTask>(
        nodePath(
          nodeID,
          `/tasks/${encodeURIComponent(operation?.taskID || '')}`
        )
      ),
    enabled: !!operation?.taskID,
    refetchInterval: (result) => {
      const status = result.state.data?.status
      return !status || status === 'pending' || status === 'running'
        ? 1_000
        : false
    }
  })
  const taskRunning =
    trackedTask.data?.status === 'pending' ||
    trackedTask.data?.status === 'running'
  const taskLogs = useQuery({
    queryKey: ['compose-action-logs', nodeID, operation?.taskID],
    queryFn: () =>
      api<TaskLog[]>(
        nodePath(
          nodeID,
          `/tasks/${encodeURIComponent(operation?.taskID || '')}/logs`
        )
      ),
    enabled: !!operation?.taskID,
    refetchInterval: taskRunning ? 1_000 : false
  })
  const cancelAction = useMutation({
    mutationFn: (taskID: string) =>
      api(nodePath(nodeID, `/tasks/${encodeURIComponent(taskID)}/cancel`), {
        method: 'POST'
      }),
    onSuccess: () => {
      void client.invalidateQueries({
        queryKey: ['compose-action-task', nodeID, operation?.taskID]
      })
      void client.invalidateQueries({ queryKey: ['tasks', 'current', nodeID] })
    }
  })
  const cleanup = useMutation({
    mutationFn: (removeVolumes: boolean) =>
      api<ComposeTask>(
        nodePath(nodeID, `/projects/compose/${encodedName}/cleanup`),
        {
          method: 'POST',
          body: JSON.stringify({
            confirmation_name: projectName,
            remove_volumes: removeVolumes
          })
        }
      ),
    onSuccess: (task) => {
      client.setQueryData(['compose-action-task', nodeID, task.id], task)
      void client.invalidateQueries({ queryKey: ['projects', nodeID] })
      void client.invalidateQueries({ queryKey: ['tasks', 'current', nodeID] })
      bypassNavigationPrompt.current = true
      void navigate({ to: '/tasks' })
    },
    onError: (error) => setNotice(error.message)
  })
  const authorizeDockerSocket = () => confirmDockerSocketMount(compose, zh)
  const saveFiles = async () => {
    const allowed = await authorizeDockerSocket()
    if (allowed === null) return false
    try {
      await save.mutateAsync(allowed)
      return true
    } catch {
      return false
    }
  }
  const validateFiles = async () => {
    const allowed = await authorizeDockerSocket()
    if (allowed !== null) validate.mutate(allowed)
  }
  useEffect(() => {
    const task = trackedTask.data
    if (!task || task.status === 'pending' || task.status === 'running') return
    setNotice(
      task.status === 'success'
        ? zh
          ? `${composeActionLabel(operation?.action || '', zh)}完成。`
          : `${composeActionLabel(operation?.action || '', zh)} completed.`
        : zh
          ? `${composeActionLabel(operation?.action || '', zh)}${task.status === 'canceled' ? '已取消' : '失败'}：${task.message}`
          : `${composeActionLabel(operation?.action || '', zh)} ${task.status}: ${task.message}`
    )
    void Promise.all([
      client.invalidateQueries({
        queryKey: ['project', nodeID, backend, projectName]
      }),
      client.invalidateQueries({
        queryKey: ['project-services', nodeID, projectName]
      }),
      client.invalidateQueries({
        queryKey: ['project-logs', nodeID, projectName]
      }),
      client.invalidateQueries({ queryKey: ['projects', nodeID] }),
      client.invalidateQueries({ queryKey: ['tasks', 'current', nodeID] }),
      client.invalidateQueries({
        queryKey: ['compose-action-logs', nodeID, operation?.taskID]
      })
    ])
  }, [
    backend,
    client,
    nodeID,
    operation?.action,
    operation?.taskID,
    projectName,
    trackedTask.data,
    zh
  ])
  const deploy = async (deployAction = 'update') => {
    if (action.isPending || taskRunning) {
      setOperationOpen(true)
      return
    }
    const allowed = await authorizeDockerSocket()
    if (allowed === null) return
    try {
      await validate.mutateAsync(allowed)
      setReview({
        compose,
        environment,
        revision: baseline?.revision,
        action: deployAction,
        socket: allowed
      })
    } catch {
      /* mutation displays the diagnostic */
    }
  }
  const confirmDeployment = async () => {
    if (!review || reviewSubmitting) return
    setReviewSubmitting(true)
    try {
      let revision = review.revision
      if (dirty) {
        const row = await api<Project>(
          nodePath(nodeID, `/projects/compose/${encodedName}`),
          {
            method: 'PUT',
            headers: dockerSocketHeaders(review.socket),
            body: JSON.stringify({
              compose: review.compose,
              environment: review.environment,
              expected_revision: review.revision
            })
          }
        )
        setBaseline(row)
        client.setQueryData(['project', nodeID, backend, projectName], row)
        revision = row.revision
      }
      const task = await api<ComposeTask>(
        nodePath(
          nodeID,
          `/projects/compose/${encodedName}/actions/${review.action}`
        ),
        {
          method: 'POST',
          headers: dockerSocketHeaders(review.socket),
          body: JSON.stringify({ expected_revision: revision })
        }
      )
      setOperation({ action: review.action, taskID: task.id })
      setOperationOpen(true)
      setReview(null)
      client.setQueryData(['compose-action-task', nodeID, task.id], task)
    } catch (error) {
      setReview(null)
      setNotice(error instanceof Error ? error.message : String(error))
      if (error instanceof ApiError && error.status === 409) {
        setConflict(true)
        void query.refetch()
      }
    } finally {
      setReviewSubmitting(false)
    }
  }

  const remove = async () => {
    const result = await confirmManagedProjectRemoval(projectName, zh)
    if (!result) return
    await api(
      nodePath(
        nodeID,
        `/projects/compose/${encodedName}?confirm=${encodedName}&force=true&preserve_volumes=${!result.checked}`
      ),
      { method: 'DELETE' }
    )
    bypassNavigationPrompt.current = true
    void navigate({ to: '/projects' })
  }
  const cleanupExternal = async () => {
    if (cleanup.isPending) return
    const result = await confirmExternalProjectCleanup(projectName, zh)
    if (result) cleanup.mutate(result.checked)
  }
  const run = async (name: string) => {
    if (action.isPending || taskRunning) {
      setOperationOpen(true)
      return
    }
    if (
      name === 'down' &&
      !(await confirmDialog({
        title: t('composeDown'),
        description: t('composeDownDescription', { name: projectName }),
        confirmLabel: 'Down',
        danger: true
      }))
    )
      return
    action.reset()
    cancelAction.reset()
    action.mutate(name)
  }

  if (backend !== 'compose')
    return (
      <ErrorState
        title={zh ? '后端尚不可用' : 'Backend unavailable'}
        description={
          zh
            ? '当前版本只实现 Docker Compose Project；Swarm Stack 仅预留领域模型。'
            : 'This version implements Docker Compose Projects only; Swarm Stack remains a model extension point.'
        }
      />
    )
  if (query.isPending)
    return (
      <LoadingState
        label={zh ? '正在加载 Project' : 'Loading Project'}
        rows={6}
      />
    )
  if (query.isError || !query.data)
    return (
      <ErrorState
        title={zh ? '无法加载 Compose 项目' : 'Unable to load Compose project'}
        description={
          query.error?.message ||
          (zh
            ? '服务端没有返回项目数据。'
            : 'The server did not return project data.')
        }
      />
    )

  const project = query.data
  const operationActive = action.isPending || taskRunning || reviewSubmitting

  const headerActions = (
    <div className="flex flex-wrap items-center gap-2">
      <StatusBadge tone={statusTone(project.status)}>
        {project.status}
      </StatusBadge>
      {project.managed && (
        <>
          <Button
            variant="outline"
            disabled={pendingConfiguration || operationActive || validate.isPending || !!review}
            onClick={() => void validateFiles()}
          >
            {validate.isPending ? <Spinner /> : <FileCheck2 />}
            {zh ? '校验' : 'Validate'}
          </Button>
          <Button
            variant="outline"
            disabled={pendingConfiguration || operationActive || save.isPending || !dirty || !!review}
            onClick={() => void saveFiles()}
          >
            {save.isPending ? <Spinner /> : <Save />}
            {zh ? '保存' : 'Save'}
          </Button>
          <Button
            disabled={pendingConfiguration || operationActive || validate.isPending || !!review}
            onClick={() => void deploy()}
          >
            <Rocket />
            {zh ? '拉取并重建' : 'Pull & recreate'}
          </Button>
          <DropdownMenu>
            <DropdownMenuTrigger
              render={
                <Button
                  variant="outline"
                  size="icon"
                  aria-label={zh ? '项目操作' : 'Project actions'}
                />
              }
            >
              <ChevronDown />
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem
                disabled={pendingConfiguration || operationActive || !!review}
                onClick={() => void deploy('up')}
              >
                {zh ? '应用当前配置' : 'Apply current configuration'}
              </DropdownMenuItem>
              {['stop', 'restart', 'pull', 'build', 'down'].map((name) => (
                <DropdownMenuItem
                  key={name}
                  disabled={operationActive}
                  onClick={() => void run(name)}
                >
                  {composeActionLabel(name, zh)}
                </DropdownMenuItem>
              ))}
              <DropdownMenuItem
                disabled={operationActive}
                onClick={() => void remove()}
              >
                {zh ? '删除项目' : 'Remove Project'}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </>
      )}
      {!project.managed && project.capabilities.includes('cleanup') && (
        <Button
          variant="destructive"
          disabled={cleanup.isPending}
          onClick={() => void cleanupExternal()}
        >
          <Trash2 />
          {zh ? '删除项目' : 'Delete Project'}
        </Button>
      )}
      {!project.managed && (
        <Button onClick={() => setTakeoverOpen(true)}>
          <Download />
          {zh ? '接管' : 'Take over'}
        </Button>
      )}
    </div>
  )

  return (
    <div className="flex w-full flex-col items-start gap-4">
      <Button
        variant="ghost"
        size="sm"
        className="-ml-2 text-muted-foreground"
        onClick={() => void navigate({ to: '/projects' })}
      >
        <ChevronLeft />
        {zh ? '项目' : 'Projects'}
      </Button>
      <ResourceFrame
        title={projectName}
        detail={
          project.managed
            ? dirty
              ? zh
                ? 'SUMA 托管 · 有未保存更改'
                : 'SUMA managed · Unsaved changes'
              : zh
                ? 'SUMA 托管 · 已保存'
                : 'SUMA managed · Saved'
            : zh
              ? '从 Docker Compose 标签发现 · 外部'
              : 'Discovered from Docker Compose labels · External'
        }
        action={<div className="flex flex-wrap gap-2"><AIAnalyzeButton kind="project" id={projectName} nodeID={nodeID} />{headerActions}</div>}
      >
        <div className="flex w-full flex-col items-start gap-3">
          {!project.managed && (
            <Alert className="w-full pr-28">
              <AlertTitle>
                {zh ? '外部 Compose Project' : 'External Compose Project'}
              </AlertTitle>
              <AlertDescription>
                {zh
                  ? 'SUMA 已按 Compose Project 聚合全部 Service 和容器实例。接管会分析完整 Project 并生成可复核的配置草稿，不会立即部署。'
                  : 'SUMA aggregated every Service and Container Instance. Takeover analyzes the complete Project and creates a reviewable draft without deploying it.'}
              </AlertDescription>
              <AlertAction>
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => setTakeoverOpen(true)}
                >
                  <Download />
                  {zh ? '接管' : 'Take over'}
                </Button>
              </AlertAction>
            </Alert>
          )}
          {project.managed &&
            project.metadata?.origin === 'takeover' &&
            !project.metadata.last_deployed_at && (
              <Alert className="w-full">
                <AlertTitle>
                  {zh ? '尚未由 SUMA 部署' : 'Not deployed by SUMA yet'}
                </AlertTitle>
                <AlertDescription>
                  {project.metadata.takeover_source === 'manual'
                    ? zh
                      ? '接管保存了你直接填写的配置，没有改变现有容器。首次部署时 Compose 会按该配置重建或调整容器、网络与孤立容器，请在执行前复核。'
                      : 'Takeover saved the configuration you wrote directly and did not change existing containers. The first deployment will recreate or adjust containers, networks, and orphans to match it; review before continuing.'
                    : zh
                      ? '接管没有改变现有容器。首次部署可能重建容器、改变资源或处理孤立容器，请在执行前复核。'
                      : 'Takeover did not change existing containers. The first deployment may recreate containers, change resources, or handle orphans; review before continuing.'}
                </AlertDescription>
              </Alert>
            )}
          {notice && (
            <p
              className={`text-sm ${action.isError ? 'text-red-600 dark:text-red-400' : 'text-muted-foreground'}`}
            >
              {notice}
            </p>
          )}
          <Tabs value={view} onValueChange={(value) => setView(value as View)}>
            <TabsList variant="line">
              {(project.managed
                ? ['Files', 'Services', 'Logs']
                : ['Services', 'Logs']
              ).map((name) => (
                <TabsTrigger key={name} value={name}>
                  {name === 'Files'
                    ? zh
                      ? '配置'
                      : 'Configuration'
                    : name === 'Services'
                      ? zh
                        ? '服务'
                        : 'Services'
                      : zh
                        ? '日志'
                        : 'Logs'}
                </TabsTrigger>
              ))}
            </TabsList>
          </Tabs>
          {conflict && (
            <Alert className="w-full">
              <AlertTitle>
                {zh ? '配置版本已变化' : 'Configuration changed'}
              </AlertTitle>
              <AlertDescription>
                {zh
                  ? '本地草稿已保留，请比较服务端配置后重新加载。'
                  : 'Your local draft is preserved. Compare the saved configuration before reloading.'}
              </AlertDescription>
              <div className="mt-2 flex gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => setConflictOpen(true)}
                >
                  {zh ? '比较差异' : 'Compare'}
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={async () => {
                    if (
                      await confirmDialog({
                        title: zh
                          ? '放弃本地草稿并重新加载？'
                          : 'Discard local draft and reload?',
                        danger: true
                      })
                    ) {
                      const latest = await query.refetch()
                      if (latest.data) {
                        setBaseline(latest.data)
                        setCompose(latest.data.compose)
                        setEnvironment(latest.data.environment)
                        setValidated('')
                        setConflict(false)
                      }
                    }
                  }}
                >
                  {zh ? '重新加载' : 'Reload'}
                </Button>
              </div>
            </Alert>
          )}
          {view === 'Files' && project.managed && (
            <>
              <ProjectConfiguration
                key={`${nodeID}:${projectName}`}
                nodeID={nodeID}
                onPendingChange={setPendingConfiguration}
                compose={compose}
                environment={environment}
                onCompose={(value) => {
                  setCompose(value)
                  setValidated('')
                }}
                onEnvironment={(value) => {
                  setEnvironment(value)
                  setValidated('')
                }}
                zh={zh}
                location={location}
                disabled={
                  operationActive ||
                  save.isPending ||
                  validate.isPending ||
                  !!review
                }
              />
              <p className="text-xs text-muted-foreground">
                {dirty ? (zh ? '未保存' : 'Unsaved') : zh ? '已保存' : 'Saved'}{' '}
                ·{' '}
                {validated === signature
                  ? zh
                    ? '校验通过'
                    : 'Validated'
                  : zh
                    ? '尚未校验当前配置'
                    : 'Current configuration not validated'}
              </p>
            </>
          )}

          {view === 'Services' && (
            <div className="flex flex-col gap-4"><div className="flex justify-end"><ImageUpdateCheck nodeID={nodeID} target={{ project_name: projectName }} /></div><ImageUpdateDetails rows={imageUpdates.data?.results || []} zh={zh} />{project.managed && imageUpdates.data?.results.some(row => row.pull_required || row.recreate_required) && <Button className="self-start" variant="outline" disabled={pendingConfiguration || operationActive || validate.isPending || !!review} onClick={() => void deploy()}>{zh ? '拉取并重建' : 'Pull & recreate'}</Button>}<Services
              rows={services.data}
              updates={imageUpdates.data?.results || []}
              declared={project.managed ? configKeys(project.compose, ['services']) : []}
              loading={services.isPending}
              error={services.error?.message}
              zh={zh}
            /></div>
          )}
          {view === 'Logs' && <ProjectLogs key={`${nodeID}/${projectName}`} nodeID={nodeID} projectName={projectName} />}
        </div>
      </ResourceFrame>
      <Dialog
        open={!!review}
        onOpenChange={(open) => {
          if (!open && !reviewSubmitting) setReview(null)
        }}
      >
        <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-4xl">
          <DialogHeader>
            <DialogTitle>
              {zh ? '审阅部署配置' : 'Review deployment'}
            </DialogTitle>
            <DialogDescription>
              {projectName} · {nodeID} ·{' '}
              {review?.action === 'up'
                ? zh
                  ? '应用当前配置'
                  : 'Apply current configuration'
                : zh
                  ? '拉取并重建'
                  : 'Pull & recreate'}
            </DialogDescription>
          </DialogHeader>
          {review && (
            <>
              <p className="text-xs text-muted-foreground">
                {zh
                  ? '差异对比当前草稿与已保存文件；实际重建范围由 Compose 决定。'
                  : 'Compares the draft with saved files. Compose determines actual runtime changes.'}
              </p>
              {project.metadata?.origin === 'takeover' &&
                !project.metadata.last_deployed_at && (
                  <Alert>
                    <AlertDescription>
                      {zh
                        ? '这是首次由 SUMA 部署，可能重建容器、调整网络或处理孤立容器。'
                        : 'First SUMA deployment may recreate containers, change networks or handle orphans.'}
                    </AlertDescription>
                  </Alert>
                )}
              <ConfigurationOverview
                compose={review.compose}
                before={baseline?.compose}
                zh={zh}
                onLocate={(path) => {
                  setLocation(path)
                  setReview(null)
                }}
              />
              <ConfigurationDiff
                beforeCompose={baseline?.compose ?? ''}
                beforeEnvironment={baseline?.environment ?? ''}
                compose={review.compose}
                environment={review.environment}
                zh={zh}
              />
            </>
          )}
          <DialogFooter>
            <Button
              variant="outline"
              disabled={reviewSubmitting}
              onClick={() => setReview(null)}
            >
              {zh ? '返回编辑' : 'Back to editing'}
            </Button>
            <Button
              disabled={reviewSubmitting}
              onClick={() => void confirmDeployment()}
            >
              {zh ? '确认部署' : 'Confirm deployment'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <Dialog open={conflictOpen} onOpenChange={setConflictOpen}>
        <DialogContent className="sm:max-w-4xl">
          <DialogHeader>
            <DialogTitle>
              {zh
                ? '服务端配置与本地草稿'
                : 'Saved configuration and local draft'}
            </DialogTitle>
          </DialogHeader>
          <ConfigurationDiff
            beforeCompose={query.data?.compose ?? ''}
            beforeEnvironment={query.data?.environment ?? ''}
            compose={compose}
            environment={environment}
            zh={zh}
          />
        </DialogContent>
      </Dialog>
      <TakeoverWarningDialog
        open={takeoverOpen}
        projectName={projectName}
        zh={zh}
        onOpenChange={setTakeoverOpen}
        onContinue={() => {
          setTakeoverOpen(false)
          void navigate({
            to: '/projects/$backend/$projectName/takeover',
            params: { backend: 'compose', projectName }
          })
        }}
      />
      <ComposeActionDialog
        open={operationOpen}
        operation={operation}
        task={trackedTask.data}
        logs={taskLogs.data ?? []}
        submitting={action.isPending}
        loading={trackedTask.isPending && !!operation?.taskID}
        error={
          action.error?.message ||
          trackedTask.error?.message ||
          taskLogs.error?.message ||
          cancelAction.error?.message
        }
        canceling={cancelAction.isPending}
        zh={zh}
        projectName={projectName}
        onOpenChange={setOperationOpen}
        onCancel={() => {
          if (operation?.taskID) cancelAction.mutate(operation.taskID)
        }}
        onViewTasks={() => {
          setOperationOpen(false)
          void navigate({ to: '/tasks' })
        }}
      />
    </div>
  )
}

function composeActionLabel(action: string, zh: boolean) {
  const labels: Record<string, [string, string]> = {
    up: ['启动', 'Start'],
    down: ['Down', 'Down'],
    stop: ['停止', 'Stop'],
    restart: ['重启', 'Restart'],
    pull: ['拉取', 'Pull'],
    build: ['构建', 'Build'],
    update: ['更新', 'Update']
  }
  return labels[action]?.[zh ? 0 : 1] ?? action
}

function ComposeActionDialog({
  open,
  operation,
  task,
  logs,
  submitting,
  loading,
  error,
  canceling,
  zh,
  projectName,
  onOpenChange,
  onCancel,
  onViewTasks
}: {
  open: boolean
  operation: ComposeOperation | null
  task?: ComposeTask
  logs: TaskLog[]
  submitting: boolean
  loading: boolean
  error?: string
  canceling: boolean
  zh: boolean
  projectName: string
  onOpenChange: (open: boolean) => void
  onCancel: () => void
  onViewTasks: () => void
}) {
  const { formatTime } = useDateTime()

  const visibleLogs = compactTaskLogs(logs)
  const compactedCount = logs.length - visibleLogs.length
  const { viewportRef, onScroll } = useLogAutoScroll<HTMLDivElement>(
    visibleLogs.at(-1)?.id,
    operation?.taskID
  )
  const status =
    task?.status || (submitting ? 'submitting' : error ? 'failed' : 'pending')
  const running = submitting || status === 'pending' || status === 'running'
  const label = zh
    ? ({
        submitting: '正在提交',
        pending: '等待中',
        running: '执行中',
        success: '已完成',
        failed: '失败',
        canceled: '已取消'
      }[status] ?? status)
    : ({
        submitting: 'Submitting',
        pending: 'Pending',
        running: 'Running',
        success: 'Completed',
        failed: 'Failed',
        canceled: 'Canceled'
      }[status] ?? status)
  const tone =
    status === 'success'
      ? 'success'
      : status === 'failed' || status === 'canceled'
        ? 'critical'
        : status === 'running' || status === 'submitting'
          ? 'warning'
          : 'neutral'
  const actionName = composeActionLabel(operation?.action || '', zh)
  const progress = task?.progress ?? 0

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      {open && (
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <div className="flex items-center gap-2 pr-8">
              <DialogTitle>
                {zh
                  ? `Compose ${actionName}进度`
                  : `Compose ${actionName} progress`}
              </DialogTitle>
              <StatusBadge tone={tone}>{label}</StatusBadge>
            </div>
            <DialogDescription className="break-all">
              {projectName}
            </DialogDescription>
          </DialogHeader>
          <div className="flex flex-col gap-3">
            <div className="flex items-center justify-between gap-4 text-sm">
              <span className="text-muted-foreground">
                {zh ? '任务进度' : 'Task progress'}
              </span>
              <span className="font-medium tabular-nums">{progress}%</span>
            </div>
            <Progress value={progress} />
            <div className="flex min-h-6 items-start gap-2 text-sm">
              {running ? (
                <Spinner className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
              ) : status === 'success' ? (
                <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-emerald-600" />
              ) : (
                <CircleAlert className="mt-0.5 size-4 shrink-0 text-destructive" />
              )}
              <p className="break-all text-muted-foreground">
                {task?.message ||
                  (submitting
                    ? zh
                      ? '正在创建后台任务…'
                      : 'Creating background task…'
                    : loading
                      ? zh
                        ? '正在读取任务状态…'
                        : 'Loading task status…'
                      : zh
                        ? '等待 Docker Compose 输出…'
                        : 'Waiting for Docker Compose output…')}
              </p>
            </div>
            <div className="flex items-center justify-between gap-4 border-t pt-3">
              <span className="text-sm font-medium">
                {zh ? '实时输出' : 'Live output'}
              </span>
              <span className="text-right text-[11px] text-muted-foreground">
                {compactedCount > 0
                  ? zh
                    ? `${visibleLogs.length} 条状态 · 已合并 ${compactedCount} 条重复进度`
                    : `${visibleLogs.length} states · ${compactedCount} repeated updates merged`
                  : operation?.taskID}
              </span>
            </div>
            <div
              ref={viewportRef}
              onScroll={onScroll}
              className="max-h-64 min-h-24 overflow-y-auto overscroll-contain rounded-lg bg-muted/50 p-3"
            >
              {logs.length === 0 ? (
                <p className="text-center text-xs text-muted-foreground">
                  {zh ? '等待任务输出…' : 'Waiting for task output…'}
                </p>
              ) : (
                <div className="flex flex-col gap-1.5">
                  {visibleLogs.map((log) => (
                    <div key={log.id} className="flex items-baseline gap-3">
                      <span className="shrink-0 font-mono text-[11px] text-muted-foreground tabular-nums">
                        {formatTime(log.created_at)}
                      </span>
                      <span
                        className={`font-mono text-xs break-all ${log.level === 'error' ? 'text-destructive' : ''}`}
                      >
                        {log.message}
                      </span>
                    </div>
                  ))}
                </div>
              )}
            </div>
            {error && <ErrorState description={error} />}
            <p className="text-xs text-muted-foreground">
              {running
                ? zh
                  ? '关闭窗口不会停止操作，任务会继续在后台执行。'
                  : 'Closing this window does not stop the operation; it continues in the background.'
                : zh
                  ? '可在任务中心查看完整记录。'
                  : 'You can review the complete record in the Task Center.'}
            </p>
          </div>
          <DialogFooter className="mt-1">
            {task && running && (
              <Button
                type="button"
                variant="destructive"
                disabled={canceling}
                onClick={onCancel}
              >
                {canceling ? <Spinner /> : <CircleStop />}
                {zh ? '取消任务' : 'Cancel task'}
              </Button>
            )}
            {task && !running && (
              <Button type="button" variant="outline" onClick={onViewTasks}>
                <ListTodo />
                {zh ? '查看任务' : 'View task'}
              </Button>
            )}
            <Button
              type="button"
              variant="outline"
              onClick={() => onOpenChange(false)}
            >
              <PanelTopClose />
              {zh ? '关闭窗口' : 'Close window'}
            </Button>
          </DialogFooter>
        </DialogContent>
      )}
    </Dialog>
  )
}

const servicePorts = (row: ContainerSummary) => {
  if (!row.ports.length) return '—'
  const values = row.ports
    .slice(0, 3)
    .map((port) =>
      port.public_port
        ? `${port.ip || '0.0.0.0'}:${port.public_port} → ${port.private_port}/${port.type}`
        : `${port.private_port}/${port.type}`
    )
  return `${values.join(', ')}${row.ports.length > 3 ? ` +${row.ports.length - 3}` : ''}`
}
const serviceMemory = (bytes: number) =>
  !bytes
    ? '—'
    : bytes >= 1024 ** 3
      ? `${(bytes / 1024 ** 3).toFixed(2)} GB`
      : `${(bytes / 1024 ** 2).toFixed(0)} MB`
const serviceUptime = (seconds: number, zh: boolean) =>
  !seconds
    ? '—'
    : seconds >= 86400
      ? `${Math.floor(seconds / 86400)} ${zh ? '天' : 'd'}`
      : seconds >= 3600
        ? `${Math.floor(seconds / 3600)} ${zh ? '小时' : 'h'}`
        : `${Math.max(1, Math.floor(seconds / 60))} ${zh ? '分钟' : 'm'}`
const serviceState = (state: string, zh: boolean) =>
  zh
    ? ({
        running: '运行中',
        paused: '已暂停',
        restarting: '重启中',
        exited: '已停止',
        dead: '异常',
        created: '已创建'
      }[state] ?? state)
    : state

function Services({
  rows,
  updates,
  declared,
  loading,
  error,
  zh
}: {
  rows?: ContainerSummary[]
  updates: ImageUpdateResult[]
  declared: string[]
  loading: boolean
  error?: string
  zh: boolean
}) {
  const { formatDateTime } = useDateTime()

  const pagination = useListPagination(rows ?? [])
  if (loading)
    return (
      <LoadingState
        compact
        label={zh ? '正在加载项目服务' : 'Loading project services'}
      />
    )
  if (error) return <ErrorState description={error} />
  return (
    <>
      <Table className="w-full">
        <TableHeader>
          <TableRow>
            <TableHead className="min-w-[190px]">
              {zh ? '服务 / 容器' : 'Service / container'}
            </TableHead>
            <TableHead>{zh ? '镜像' : 'Image'}</TableHead>
            <TableHead className="min-w-[140px]">
              {zh ? '状态 / 运行时间' : 'State / uptime'}
            </TableHead>
            <TableHead className="min-w-[120px]">
              {zh ? '资源' : 'Resources'}
            </TableHead>
            <TableHead className="min-w-[190px]">
              {zh ? '端口' : 'Ports'}
            </TableHead>
            <TableHead className="min-w-[150px]">
              {zh ? '创建时间' : 'Created'}
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {(rows ?? []).length === 0 && (
            <TableRow>
              <TableCell
                colSpan={6}
                className="h-24 text-center text-muted-foreground"
              >
                {zh
                  ? '项目尚未创建容器，请先启动项目。'
                  : 'No containers have been created. Start the project first.'}
              </TableCell>
            </TableRow>
          )}
          {declared.filter(name => !(rows || []).some(row => row.labels['com.docker.compose.service'] === name)).map(name => <TableRow key={`undeployed/${name}`}><TableCell>{name}</TableCell><TableCell colSpan={5} className="text-xs text-muted-foreground">{zh ? '尚未部署 · 没有本地运行版本可检测' : 'Not deployed · no local runtime version to check'}</TableCell></TableRow>)}
          {pagination.items.map((row) => (
            <TableRow key={row.id}>
              <TableCell>
                <div className="flex flex-col gap-0.5">
                  <span className="font-medium">
                    {row.labels['com.docker.compose.service'] || row.name}
                  </span>
                  <Link
                    to="/containers/$containerId"
                    params={{ containerId: row.id }}
                    className="text-xs text-muted-foreground hover:text-foreground hover:underline"
                  >
                    {row.name}
                  </Link>
                  <span className="font-mono text-[11px] text-muted-foreground">
                    {row.id.slice(0, 12)}
                    {row.labels['com.docker.compose.container-number']
                      ? ` · #${row.labels['com.docker.compose.container-number']}`
                      : ''}
                  </span>
                </div>
              </TableCell>
              <TableCell>
                <TooltipHint content={row.image}>
                  <span className="block max-w-72 truncate text-muted-foreground">
                    {row.image}
                  </span>
                </TooltipHint>
                <UpdateStatus rows={updates.filter(update => update.containers.some(container => container.container_id === row.id))} zh={zh} />
              </TableCell>
              <TableCell>
                <div className="flex flex-col items-start gap-1">
                  <StatusBadge tone={stateTone(row.state)}>
                    {serviceState(row.state, zh)}
                  </StatusBadge>
                  <TooltipHint content={row.status}>
                    <span className="max-w-40 truncate text-xs text-muted-foreground">
                      {serviceUptime(row.uptime_seconds, zh)}
                    </span>
                  </TooltipHint>
                </div>
              </TableCell>
              <TableCell>
                {row.state === 'running' ? (
                  <div className="flex flex-col">
                    <span className="tabular-nums">
                      CPU {row.cpu_percent.toFixed(1)}%
                    </span>
                    <span className="text-xs text-muted-foreground tabular-nums">
                      {serviceMemory(row.memory_bytes)}
                    </span>
                  </div>
                ) : (
                  '—'
                )}
              </TableCell>
              <TableCell className="font-mono text-xs">
                <TooltipHint content={servicePorts(row)}>
                  <span className="block max-w-64 truncate">
                    {servicePorts(row)}
                  </span>
                </TooltipHint>
              </TableCell>
              <TableCell className="text-xs text-muted-foreground tabular-nums">
                {formatDateTime(row.created)}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      <ListPagination {...pagination} zh={zh} />
    </>
  )
}
