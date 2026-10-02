import { useEffect, useRef, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useBlocker, useNavigate } from '@tanstack/react-router'
import {
  ChevronDown,
  ChevronLeft,
  FileCheck2,
  Rocket,
  Save
} from 'lucide-react'
import {
  ConfigurationDiff,
  ConfigurationOverview,
  ProjectConfiguration,
  ValueInput
} from '../components/docker/project-configuration'
import { Button } from '../components/ui/button'
import { Alert, AlertDescription } from '../components/ui/alert'
import { Spinner } from '../components/ui/spinner'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle
} from '../components/ui/dialog'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger
} from '../components/ui/dropdown-menu'
import type { ConfigPath } from '../features/compose/document'
import { configKeys } from '../features/compose/document'
import {
  confirmDockerSocketMount,
  dockerSocketHeaders
} from '../features/compose/docker-socket-confirmation'
import type { Project } from '../features/compose/types'
import { api } from '../lib/api'
import { nodePath } from '../lib/nodes'
import { useI18n } from '../lib/i18n'
import { registerProjectNavigationGuard } from '../lib/project-navigation-guard'
import { confirmDialog } from '../stores/dialog'
import { useUIStore } from '../stores/ui'
import { ResourceFrame } from './images'

const emptyCompose = 'services: {}\n'

export function ProjectCreatePage() {
  const nodeID = useUIStore((state) => state.currentNodeID),
    { language } = useI18n(),
    zh = language === 'zh-CN'
  const navigate = useNavigate(),
    client = useQueryClient()
  const [name, setName] = useState(''),
    [compose, setCompose] = useState(emptyCompose),
    [environment, setEnvironment] = useState(''),
    [notice, setNotice] = useState(''),
    [validated, setValidated] = useState(''),
    [pendingConfiguration, setPendingConfiguration] = useState(false)
  const [review, setReview] = useState<{
    name: string
    compose: string
    environment: string
    socket: boolean
    action: string
  } | null>(null)
  const [location, setLocation] = useState<ConfigPath>()
  const [deploying, setDeploying] = useState(false),
    bypass = useRef(false)
  const dirty = pendingConfiguration || name !== '' || compose !== emptyCompose || environment !== '',
    dirtyRef = useRef(dirty)
  dirtyRef.current = dirty
  const signature = JSON.stringify([name, compose, environment])
  useBlocker({
    disabled: !dirty,
    enableBeforeUnload: dirty,
    shouldBlockFn: async () =>
      bypass.current
        ? false
        : !(await confirmDialog({
            title: zh
              ? '放弃未保存的项目配置？'
              : 'Discard unsaved project configuration?',
            danger: true
          }))
  })
  useEffect(
    () =>
      registerProjectNavigationGuard(
        async () =>
          !dirtyRef.current ||
          (await confirmDialog({
            title: zh
              ? '放弃未保存配置并切换节点？'
              : 'Discard unsaved configuration and change node?',
            danger: true
          }))
      ),
    [zh]
  )
  useEffect(() => {
    setName('')
    setPendingConfiguration(false)
    setCompose(emptyCompose)
    setEnvironment('')
    setValidated('')
    setReview(null)
    setNotice('')
  }, [nodeID])
  const validName = /^[a-z0-9][a-z0-9_-]*$/.test(name),
    hasServices = configKeys(compose, ['services']).length > 0
  const create = useMutation({
    mutationFn: ({
      content,
      env,
      projectName,
      socket
    }: {
      content: string
      env: string
      projectName: string
      socket: boolean
    }) =>
      api<Project>(nodePath(nodeID, '/projects'), {
        method: 'POST',
        headers: dockerSocketHeaders(socket),
        body: JSON.stringify({
          backend: 'compose',
          name: projectName,
          compose: content,
          environment: env
        })
      }),
    onError: (error) => setNotice(error.message)
  })
  const validate = useMutation({
    mutationFn: (socket: boolean) =>
      api(nodePath(nodeID, '/projects/validate'), {
        method: 'POST',
        headers: dockerSocketHeaders(socket),
        body: JSON.stringify({ name, compose, environment })
      }),
    onSuccess: () => {
      setValidated(signature)
      setNotice(zh ? '校验通过' : 'Validation passed')
    },
    onError: (error) => setNotice(error.message)
  })
  const authorize = () => confirmDockerSocketMount(compose, zh)
  const save = async () => {
    const socket = await authorize()
    if (socket === null) return
    try {
      const row = await create.mutateAsync({
        content: compose,
        env: environment,
        projectName: name,
        socket
      })
      client.setQueryData(['project', nodeID, 'compose', name], row)
      void client.invalidateQueries({ queryKey: ['projects', nodeID] })
      bypass.current = true
      await navigate({
        to: '/projects/$backend/$projectName',
        params: { backend: 'compose', projectName: name }
      })
    } catch {
      /* mutation displays the error */
    }
  }
  const reviewDeployment = async (action = 'update') => {
    const socket = await authorize()
    if (socket === null) return
    try {
      await validate.mutateAsync(socket)
      setReview({ name, compose, environment, socket, action })
    } catch {
      /* mutation displays the error */
    }
  }
  const confirmDeployment = async () => {
    if (!review || deploying) return
    setDeploying(true)
    try {
      const row = await create.mutateAsync({
        projectName: review.name,
        content: review.compose,
        env: review.environment,
        socket: review.socket
      })
      client.setQueryData(['project', nodeID, 'compose', review.name], row)
      void client.invalidateQueries({ queryKey: ['projects', nodeID] })
      bypass.current = true
      try {
        const task = await api<{ id: string }>(
          nodePath(
            nodeID,
            `/projects/compose/${encodeURIComponent(review.name)}/actions/${review.action}`
          ),
          {
            method: 'POST',
            headers: dockerSocketHeaders(review.socket),
            body: JSON.stringify({ expected_revision: row.revision })
          }
        )
        await navigate({
          to: '/projects/$backend/$projectName',
          params: { backend: 'compose', projectName: review.name },
          search: { task: task.id, action: review.action }
        })
      } catch (error) {
        setNotice(error instanceof Error ? error.message : String(error))
        await navigate({
          to: '/projects/$backend/$projectName',
          params: { backend: 'compose', projectName: review.name }
        })
      }
    } catch (error) {
      setNotice(error instanceof Error ? error.message : String(error))
    } finally {
      setDeploying(false)
    }
  }
  const busy = create.isPending || validate.isPending || deploying || !!review
  const toolbar = (
    <div className="flex flex-wrap gap-2">
      <Button
        variant="outline"
        disabled={busy || pendingConfiguration || !validName || !hasServices}
        onClick={async () => {
          const socket = await authorize()
          if (socket !== null) validate.mutate(socket)
        }}
      >
        {validate.isPending ? <Spinner /> : <FileCheck2 />}
        {zh ? '校验' : 'Validate'}
      </Button>
      <Button
        variant="outline"
        disabled={busy || pendingConfiguration || !validName || !hasServices}
        onClick={() => void save()}
      >
        {create.isPending ? <Spinner /> : <Save />}
        {zh ? '保存' : 'Save'}
      </Button>
      <Button
        disabled={busy || pendingConfiguration || !validName || !hasServices}
        onClick={() => void reviewDeployment()}
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
              aria-label={zh ? '部署操作' : 'Deployment actions'}
              disabled={busy || pendingConfiguration || !validName || !hasServices}
            />
          }
        >
          <ChevronDown />
        </DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuItem onClick={() => void reviewDeployment('up')}>
            {zh ? '应用当前配置' : 'Apply current configuration'}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  )
  return (
    <div className="space-y-4">
      <Button
        variant="ghost"
        size="sm"
        onClick={() => void navigate({ to: '/projects' })}
      >
        <ChevronLeft />
        {zh ? '项目' : 'Projects'}
      </Button>
      <ResourceFrame
        title={zh ? '新建项目' : 'New Project'}
        detail={`${zh ? '目标节点' : 'Target node'} · ${nodeID}`}
        action={toolbar}
      >
        <div className="w-full min-w-0 space-y-4">
          <div className="max-w-md">
            <ValueInput
              label={
                zh
                  ? '项目名 · 小写字母、数字、-、_'
                  : 'Project name · lowercase letters, numbers, -, _'
              }
              value={name}
              disabled={busy}
              onChange={(value) => {
                setName(value.trim())
                setValidated('')
              }}
            />
          </div>
          {notice && (
            <Alert>
              <AlertDescription>{notice}</AlertDescription>
            </Alert>
          )}
          <ProjectConfiguration
            key={nodeID}
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
            disabled={busy}
          />
          <p className="text-xs text-muted-foreground">
            {validated === signature
              ? zh
                ? '校验通过'
                : 'Validated'
              : zh
                ? '尚未校验 · 保存不会部署'
                : 'Not validated · saving does not deploy'}
          </p>
        </div>
      </ResourceFrame>
      <Dialog
        open={!!review}
        onOpenChange={(open) => {
          if (!open && !deploying) setReview(null)
        }}
      >
        <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-4xl">
          <DialogHeader>
            <DialogTitle>
              {zh ? '审阅部署配置' : 'Review deployment'}
            </DialogTitle>
            <DialogDescription>
              {review?.name} · {nodeID} ·{' '}
              {review?.action === 'up' ? 'up -d' : 'pull → up -d'}
            </DialogDescription>
          </DialogHeader>
          {review && (
            <>
              <ConfigurationOverview
                compose={review.compose}
                zh={zh}
                onLocate={(path) => {
                  setLocation(path)
                  setReview(null)
                }}
              />
              <ConfigurationDiff
                beforeCompose=""
                beforeEnvironment=""
                compose={review.compose}
                environment={review.environment}
                zh={zh}
              />
            </>
          )}
          <DialogFooter>
            <Button
              variant="outline"
              disabled={deploying}
              onClick={() => setReview(null)}
            >
              {zh ? '返回编辑' : 'Back to editing'}
            </Button>
            <Button
              disabled={deploying}
              onClick={() => void confirmDeployment()}
            >
              {deploying && <Spinner />}
              {zh ? '确认部署' : 'Confirm deployment'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
