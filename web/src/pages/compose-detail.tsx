import { useContainerWorkspace } from '../features/containers/use-container-workspace'
import { useImageUpdates } from '../features/image-updates/hooks'
import { useProjectObservation } from '../features/compose/use-project-observation'
import { ContainerWorkspaceDialog } from '../components/docker/container-workspace-dialog'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  useBlocker,
  useNavigate,
  useParams,
  useSearch
} from '@tanstack/react-router'
import {
  ChevronDown,
  ChevronLeft,
  Download,
  FileCheck2,
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
import { Spinner } from '../components/ui/spinner'
import { InlineStatus } from '../components/ui/inline-status'
import { StatusBadge } from '../components/ui/status-badge'
import { Tabs, TabsList, TabsTrigger } from '../components/ui/tabs'
import { confirmExternalProjectCleanup } from '../features/compose/external-project-cleanup'
import {
  confirmDockerSocketMount,
  dockerSocketHeaders
} from '../features/compose/docker-socket-confirmation'
import { confirmManagedProjectRemoval } from '../features/compose/managed-project-removal'
import { TakeoverWarningDialog } from '../features/compose/takeover-warning-dialog'
import type { Project } from '../features/compose/types'
import { projectDisplayName } from '../features/compose/types'
import { ProjectLogs } from '../features/project-logs/project-logs'
import { ProjectServices } from '../components/docker/project-services'
import type {
  ContainerSummary
} from '../features/containers/types'
import { ComposeActionDialog } from '../features/compose/compose-action-dialog'
import { composeActionLabel, type ComposeTask, type ComposeTaskLog as TaskLog, type ComposeOperation } from '../features/compose/action-progress'
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
type View = 'Files' | 'Services' | 'Logs'
const statusTone = (status: string) =>
  status === 'running'
    ? 'success'
    : status === 'degraded'
      ? 'warning'
      : 'neutral'

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
  const [notice, setNotice] = useState<{ message: string; tone: 'success' | 'error' | 'pending' } | null>(null)
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
    queryFn: ({ signal }) => api<ContainerSummary[]>(nodePath(nodeID, `/projects/compose/${encodedName}/services`), { signal }),
    enabled: view === 'Services',
    refetchInterval: 5_000
  })
  const observation = useProjectObservation(nodeID, services.data ?? [], view === 'Services')
  const workspace = useContainerWorkspace(nodeID, zh, projectName)
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
      setNotice({ message: zh ? '已保存。' : 'Saved.', tone: 'success' })
    },
    onError: (error) => {
      setNotice({ message: error.message, tone: 'error' })
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
      setNotice({ message: zh ? 'Compose 配置有效。' : 'Compose configuration is valid.', tone: 'success' })
    },
    onError: (error) => setNotice({ message: error.message, tone: 'error' })
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
      setNotice(null)
      setOperation({ action: name, taskID: '' })
      setOperationOpen(true)
    },
    onSuccess: (task, name) => {
      setOperation({ action: name, taskID: task.id })
      client.setQueryData(['compose-action-task', nodeID, task.id], task)
      setNotice({
        message: zh ? `${composeActionLabel(name, zh)}任务已启动。` : `${composeActionLabel(name, zh)} task started.`,
        tone: 'pending',
      })
      void client.invalidateQueries({ queryKey: ['tasks', 'current', nodeID] })
    },
    onError: (error) => setNotice({ message: error.message, tone: 'error' })
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
    onError: (error) => setNotice({ message: error.message, tone: 'error' })
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
    setNotice({
      tone: task.status === 'success' ? 'success' : 'error',
      message: task.status === 'success'
        ? zh
          ? `${composeActionLabel(operation?.action || '', zh)}完成。`
          : `${composeActionLabel(operation?.action || '', zh)} completed.`
        : zh
          ? `${composeActionLabel(operation?.action || '', zh)}${task.status === 'canceled' ? '已取消' : '失败'}：${task.message}`
          : `${composeActionLabel(operation?.action || '', zh)} ${task.status}: ${task.message}`,
    })
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
      setNotice({ message: error instanceof Error ? error.message : String(error), tone: 'error' })
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
  const taskActive = action.isPending || taskRunning
  const operationActive = taskActive || reviewSubmitting

  const headerActions = (
    <div className="flex flex-wrap items-center gap-2">
      {project.managed && (
        <>
          {view === 'Files' && (
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
            </>
          )}
          <Button
            disabled={!taskActive && (pendingConfiguration || operationActive || validate.isPending || !!review)}
            onClick={() => void deploy()}
          >
            {taskActive ? <Spinner aria-hidden="true" className="motion-reduce:animate-none" /> : <Rocket />}
            {taskActive ? zh ? '查看任务进度' : 'View task progress' : zh ? '拉取并重建' : 'Pull & recreate'}
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
        title={projectDisplayName(project)}
        lead={<StatusBadge tone={statusTone(project.status)}>{zh ? ({ running: '运行中', stopped: '已停止', degraded: '部分异常' }[project.status] ?? project.status) : project.status}</StatusBadge>}
        detail={
          project.delivery_project_name
            ? `CD · ${project.native_name}`
            : project.managed
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
        action={<div className="flex flex-wrap gap-2">{headerActions}</div>}
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
          <div className="flex w-full min-w-0 flex-wrap items-center gap-x-4 gap-y-2">
            <Tabs value={view} onValueChange={(value) => setView(value as View)}>
              <TabsList>
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
            {notice && <InlineStatus tone={notice.tone} className="flex-1">{notice.message}</InlineStatus>}
          </div>
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
            <ProjectServices
              key={`${nodeID}/${projectName}`}
              nodeID={nodeID}
              projectName={projectName}
              rows={services.data}
              updates={imageUpdates.data?.results || []}
              declared={project.managed ? project.declared_service_names ?? configKeys(project.compose, ['services']) : []}
              observation={observation}
              onOpenContainer={(row, tab, opener) => void workspace.open(row, tab, opener)}
              loading={services.isPending}
              error={services.error?.message}
              updateError={imageUpdates.error?.message}
              zh={zh}
            />
          )}
          {view === 'Logs' && <ProjectLogs key={`${nodeID}/${projectName}`} nodeID={nodeID} projectName={projectName} />}
        </div>
      </ResourceFrame>
      <ContainerWorkspaceDialog workspace={workspace} rows={services.data ?? []} available={!!services.data && !services.isError} zh={zh} />
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
