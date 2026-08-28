import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useParams } from '@tanstack/react-router'
import { AlertTriangle, Check, ChevronDown, ChevronLeft, ChevronRight, CirclePause, Eye, EyeOff, FileCheck2, FlaskConical, ShieldAlert, Trash2 } from 'lucide-react'
import { lazy, Suspense, useEffect, useMemo, useRef, useState } from 'react'
import { Alert, AlertDescription, AlertTitle } from '../components/ui/alert'
import { Badge } from '../components/ui/badge'
import { Button } from '../components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '../components/ui/card'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '../components/ui/collapsible'
import { ErrorState } from '../components/ui/error-state'
import { Input } from '../components/ui/input'
import { Label } from '../components/ui/label'
import { LoadingState } from '../components/ui/loading-state'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../components/ui/select'
import { Spinner } from '../components/ui/spinner'
import { StatusBadge } from '../components/ui/status-badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'
import { Tabs, TabsList, TabsTrigger } from '../components/ui/tabs'
import type { EnvironmentCandidate, Project, ProjectTakeoverDraft, ShadowAssessment, ShadowPreviewSession, ShadowPreviewStatus } from '../features/compose/types'
import { api } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { nodePath } from '../lib/nodes'
import { confirmDialog } from '../stores/dialog'
import { useUIStore } from '../stores/ui'
import { ResourceFrame } from './images'

const Monaco = lazy(() => import('@monaco-editor/react'))
const steps = ['analysis', 'environment', 'editor', 'confirm'] as const
type Destination = EnvironmentCandidate['destination']
interface TaskRow { id: string; status: string; progress: number; message: string }

function normalizeTakeoverDraft(draft: ProjectTakeoverDraft): ProjectTakeoverDraft {
  const observation = draft.observation ?? { name: draft.project_name, services: [], one_off_containers: [], orphan_containers: [], fingerprint: draft.fingerprint }
  return {
    ...draft,
    variables: draft.variables ?? [],
    warnings: draft.warnings ?? [],
    blockers: draft.blockers ?? [],
    capabilities: draft.capabilities ?? ['takeover'],
    observation: {
      ...observation,
      services: (observation.services ?? []).map((service) => ({ ...service, instances: service.instances ?? [], config_variants: service.config_variants ?? [] })),
      one_off_containers: observation.one_off_containers ?? [],
      orphan_containers: observation.orphan_containers ?? [],
    },
  }
}

const driftStatusLabels: Record<string, [string, string]> = {
  in_sync: ['配置一致', 'In sync'],
  runtime_drift: ['运行态偏移', 'Runtime drift'],
  orphan: ['孤立 Service', 'Orphan Service'],
  not_created: ['尚未创建', 'Not created'],
}

const driftReasonLabels: Record<string, [string, string]> = {
  runtime_drift: ['运行态配置偏移', 'Runtime drift'],
  stale_container: ['遗留容器', 'Stale container'],
  manual_modification: ['疑似手动修改', 'Possible manual modification'],
  partial_recreate: ['部分实例已重建', 'Partial recreate'],
  abnormal_state: ['运行状态异常', 'Abnormal state'],
}

const environmentSourceLabels: Record<string, [string, string]> = {
  compose_explicit: ['Compose 显式配置', 'Explicit Compose value'],
  explicit_inferred: ['运行态推断', 'Runtime inferred'],
  image_default: ['镜像默认值', 'Image default'],
  unknown: ['来源未知', 'Unknown source'],
}

const environmentReasonLabels: Record<string, [string, string]> = {
  compose_explicit: ['由安全解析后的 Compose Project 显式声明', 'Declared by the safely rendered Compose Project'],
  explicit_inferred: ['运行值与镜像默认 ENV 不同，推断为显式配置', 'Inferred as an explicit service environment value'],
  image_default: ['与镜像默认 ENV 完全一致', 'Matches the image default environment exactly'],
  unknown: ['无法读取镜像默认 ENV，不能确认原始来源', 'Image defaults were unavailable, so the original source cannot be proven'],
}

const taskStatusLabels: Record<string, [string, string]> = {
  pending: ['等待中', 'Pending'],
  running: ['进行中', 'Running'],
  success: ['已完成', 'Completed'],
  failed: ['失败', 'Failed'],
  canceled: ['已取消', 'Canceled'],
}

function localizedLabel(labels: Record<string, [string, string]>, value: string, zh: boolean) {
  const pair = labels[value]
  return pair ? pair[zh ? 0 : 1] : value
}

function confidenceLabel(value: ProjectTakeoverDraft['confidence'], zh: boolean) {
  if (!zh) return value
  return { high: '高', medium: '中', low: '低' }[value]
}

function takeoverMessage(message: string | undefined, zh: boolean): string {
  if (!message || !zh) return message ?? ''
  const exact: Record<string, string> = {
    'Unable to compare rendered service hashes with running containers': '无法比较规范化 Service 配置与运行容器的 config hash',
    'Mapped Compose source could not be rendered; the whole Project was rebuilt from runtime metadata': '原始 Compose 配置无法渲染，已将整个 Project 降级为运行态重建',
    'One or more services have runtime drift; the majority configuration was selected for the draft': '一个或多个 Service 存在运行态偏移，草稿已采用实例数量最多的配置变体',
    'Runtime reconstruction cannot recover comments, YAML anchors, source variable expressions, profiles, dependencies, build contexts, or removed services': '运行态重建无法恢复注释、YAML 锚点、原始变量表达式、profiles、依赖关系、build context 或已删除的 Service',
    'top-level volumes are not isolated for shadow preview': '顶层 volumes 无法在隔离预演中安全隔离',
    'top-level configs are not supported by shadow preview': '隔离预演暂不支持顶层 configs',
    'top-level secrets are not supported by shadow preview': '隔离预演暂不支持顶层 secrets',
    'Compose Project must define at least one service': 'Compose Project 至少需要定义一个 Service',
    'shadow preview runtime is unavailable': '隔离预演运行环境不可用',
    'Project changed while preparing shadow preview; analyze it again': '准备隔离预演期间 Project 已发生变化，请重新分析',
    'Project changed while preparing takeover': '准备接管期间 Project 已发生变化，请重新分析',
    'Project changed while preparing takeover; analyze it again': '准备接管期间 Project 已发生变化，请重新分析',
    'Project is already managed by SUMA': '该 Project 已由 SUMA 托管',
    'Docker runtime does not support Compose Project inspection': '当前 Docker 运行环境不支持检查 Compose Project',
    'Compose Project has no observable containers': '该 Compose Project 没有可观测的容器',
    'invalid environment destination': '环境变量写入位置无效',
    'type the Compose Project name to confirm takeover': '请输入完整的 Compose Project 名称以确认接管',
    'takeover fingerprint and Compose YAML are required': '接管需要 Project 指纹和 Compose YAML',
    'Compose runner is unavailable': 'ComposeRunner 不可用',
    'invalid shadow preview session': '隔离预演会话无效',
    'shadow preview metadata is invalid': '隔离预演元数据无效',
    'Task service is unavailable': '任务服务不可用',
    'Validating isolated Compose Project': '正在校验隔离的 Compose Project',
    'Starting isolated Compose Project': '正在启动隔离的 Compose Project',
    'Shadow preview is ready': '隔离预演已就绪',
    'Stopping isolated Compose Project': '正在停止隔离的 Compose Project',
    'Shadow preview removed': '隔离预演已清理',
    'Compose diagnostic omitted because it may contain a short sensitive environment value': 'Compose 返回的信息可能包含较短的敏感环境变量值，详细内容已隐藏',
  }
  if (exact[message]) return exact[message]

  let match = message.match(/^Mapped Compose source was not safe or complete; the whole Project was rebuilt from runtime metadata: (.+)$/)
  if (match) return `原始 Compose 配置不安全或不完整，已将整个 Project 降级为运行态重建：${takeoverMessage(match[1], zh)}`
  match = message.match(/^Project is not eligible for shadow preview: (.+)$/)
  if (match) return `该 Project 不满足隔离预演条件：${match[1].split('; ').map((reason) => takeoverMessage(reason, zh)).join('；')}`
  match = message.match(/^Service (.+) build configuration was removed; takeover uses its resolved image$/)
  if (match) return `Service ${match[1]} 的 build 配置无法恢复，接管草稿将使用当前解析后的镜像`
  match = message.match(/^(configs|secrets) "([^"]+)" is file-backed and must be converted to an external resource or supplied inside the managed Project$/)
  if (match) return `${match[1]} “${match[2]}” 依赖本地文件；请改为 external 资源，或在托管 Project 内安全提供该文件`
  match = message.match(/^Service (.+) has more running instances than the normalized Compose configuration; a CLI scale override may be active$/)
  if (match) return `Service ${match[1]} 的运行实例多于规范化 Compose 配置，可能使用了 CLI scale 覆盖`
  match = message.match(/^network "([^"]+)" is external$/)
  if (match) return `网络“${match[1]}”是 external 网络，无法隔离`
  match = message.match(/^network "([^"]+)" has an explicit name that will be replaced by an isolated preview name$/)
  if (match) return `网络“${match[1]}”设置了显式名称，预演时将替换为隔离名称`
  match = message.match(/^service "([^"]+)" (.+)$/)
  if (match) {
    const actions: Record<string, string> = {
      'publishes ports': '发布了固定端口',
      'mounts production data': '挂载了生产数据',
      'sets container_name': '设置了 container_name',
      'uses privileged mode or devices': '使用 privileged 模式或 devices',
      'requires a build context': '依赖 build context',
      'requests more than three preview replicas': '请求了超过 3 个预演副本',
      'has no healthcheck; preview can only verify that its containers remain running': '没有 healthcheck，预演只能确认容器保持运行',
    }
    const action = actions[match[2]]
      ?? match[2].replace(/^shares an explicit (.+)$/, '显式共享 $1')
        .replace(/^uses (.+)$/, '使用 $1')
    return `Service “${match[1]}”${action}`
  }
  match = message.match(/^Compose working directory and config files must be absolute$/)
  if (match) return 'Compose working directory 和 config files 必须使用绝对路径'
  match = message.match(/^Compose working directory is not mapped into SUMA$/)
  if (match) return 'Compose working directory 未映射到 SUMA'
  match = message.match(/^Compose working directory is unavailable$/)
  if (match) return 'Compose working directory 不可访问'
  match = message.match(/^file "([^"]+)" is not mapped into SUMA$/)
  if (match) return `文件“${match[1]}”未映射到 SUMA`
  match = message.match(/^file "([^"]+)" is outside the Compose working directory$/)
  if (match) return `文件“${match[1]}”超出 Compose working directory 边界`
  match = message.match(/^file "([^"]+)" is not a regular file$/)
  if (match) return `文件“${match[1]}”不是普通文件`
  match = message.match(/^file "([^"]+)" exceeds 2 MiB$/)
  if (match) return `文件“${match[1]}”超过 2 MiB 限制`
  match = message.match(/^interpolated file path "([^"]+)" cannot be proven safe$/)
  if (match) return `无法证明插值文件路径“${match[1]}”是安全的`
  match = message.match(/^parse Compose Project: (.+)$/)
  if (match) return `无法解析 Compose Project：${match[1]}`
  match = message.match(/^docker compose config --quiet: exit status \d+(?:: ([\s\S]+))?$/)
  if (match) return match[1] ? `Compose 配置校验失败：${takeoverMessage(match[1], zh)}` : 'Compose 配置校验失败，但命令没有返回详细原因'
  match = message.match(/^validate takeover Compose Project: docker compose config --quiet: exit status \d+(?:: ([\s\S]+))?$/)
  if (match) return match[1] ? `接管前的 Compose 配置校验失败：${takeoverMessage(match[1], zh)}` : '接管前的 Compose 配置校验失败，但命令没有返回详细原因'
  return message
}

export function ProjectTakeoverPage() {
  const { backend, projectName } = useParams({ from: '/projects/$backend/$projectName/takeover' })
  const nodeID = useUIStore((state) => state.currentNodeID)
  const theme = useUIStore((state) => state.theme)
  const { language } = useI18n()
  const zh = language === 'zh-CN'
  const navigate = useNavigate()
  const client = useQueryClient()
  const encoded = encodeURIComponent(projectName)
  const [step, setStep] = useState(0)
  const [choices, setChoices] = useState<Record<string, Destination>>({})
  const [compose, setCompose] = useState('')
  const [environment, setEnvironment] = useState('')
  const [file, setFile] = useState<'compose' | 'environment'>('compose')
  const [revealed, setRevealed] = useState<Set<string>>(() => new Set())
  const [validated, setValidated] = useState('')
  const [confirmation, setConfirmation] = useState('')
  const [shadowSession, setShadowSession] = useState<ShadowPreviewSession | null>(null)
  const shadowSessionRef = useRef<ShadowPreviewSession | null>(null)
  const dark = theme === 'dark' || (theme === 'system' && matchMedia('(prefers-color-scheme: dark)').matches)
  const preview = useQuery({ queryKey: ['project-takeover', nodeID, projectName], queryFn: () => api<ProjectTakeoverDraft>(nodePath(nodeID, `/projects/compose/${encoded}/takeover/preview`), { method: 'POST' }), select: normalizeTakeoverDraft, enabled: backend === 'compose', retry: false })
  const contentSignature = useMemo(() => `${compose}\u0000${environment}`, [compose, environment])
  const tasks = useQuery({ queryKey: ['tasks', nodeID], queryFn: () => api<TaskRow[]>(`/tasks?node_id=${encodeURIComponent(nodeID)}`), enabled: shadowSession !== null, refetchInterval: 1_000 })
  const shadowTask = tasks.data?.find((task) => task.id === shadowSession?.task.id)
  const shadowStatus = useQuery({ queryKey: ['project-shadow-status', nodeID, shadowSession?.session_id], queryFn: () => api<ShadowPreviewStatus>(nodePath(nodeID, `/projects/compose/${encoded}/takeover/shadow/${shadowSession?.session_id}`)), enabled: shadowSession !== null && shadowTask?.status === 'success', refetchInterval: 5_000, retry: false })

  useEffect(() => {
    if (!preview.data) return
    setChoices(Object.fromEntries(preview.data.variables.map((variable) => [variable.id, variable.source === 'image_default' ? 'exclude' : variable.destination])))
  }, [preview.data])
  useEffect(() => {
    if (step === 0) return
    const listener = (event: BeforeUnloadEvent) => { event.preventDefault(); event.returnValue = '' }
    window.addEventListener('beforeunload', listener)
    return () => window.removeEventListener('beforeunload', listener)
  }, [step])
  useEffect(() => { shadowSessionRef.current = shadowSession }, [shadowSession])
  useEffect(() => () => {
    const current = shadowSessionRef.current
    if (current) void fetch(`/api/v1${nodePath(nodeID, `/projects/compose/${encoded}/takeover/shadow/${current.session_id}`)}`, { method: 'DELETE', credentials: 'include', keepalive: true })
  }, [encoded, nodeID])

  const render = useMutation({
    mutationFn: () => api<ProjectTakeoverDraft>(nodePath(nodeID, `/projects/compose/${encoded}/takeover/render`), { method: 'POST', body: JSON.stringify({ fingerprint: preview.data?.fingerprint, choices: Object.entries(choices).map(([id, destination]) => ({ id, destination })) }) }),
    onSuccess: (draft) => { setCompose(draft.compose); setEnvironment(draft.environment); setValidated(''); setStep(2) },
  })
  const validate = useMutation({
    mutationFn: () => api(nodePath(nodeID, `/projects/compose/${encoded}/takeover/validate`), { method: 'POST', body: JSON.stringify({ compose, environment }) }),
    onSuccess: () => setValidated(contentSignature),
  })
  const assessShadow = useMutation({ mutationFn: () => api<ShadowAssessment>(nodePath(nodeID, `/projects/compose/${encoded}/takeover/shadow/assess`), { method: 'POST', body: JSON.stringify({ compose }) }) })
  const startShadow = useMutation({
    mutationFn: () => api<ShadowPreviewSession>(nodePath(nodeID, `/projects/compose/${encoded}/takeover/shadow`), { method: 'POST', body: JSON.stringify({ fingerprint: preview.data?.fingerprint, compose, environment }) }),
    onSuccess: (session) => { setShadowSession(session); void client.invalidateQueries({ queryKey: ['tasks', nodeID] }) },
  })
  const cleanupShadow = useMutation({
    mutationFn: (session: ShadowPreviewSession) => api(nodePath(nodeID, `/projects/compose/${encoded}/takeover/shadow/${session.session_id}`), { method: 'DELETE' }),
    onSuccess: () => { shadowSessionRef.current = null; setShadowSession(null); void client.invalidateQueries({ queryKey: ['tasks', nodeID] }) },
  })
  const takeover = useMutation({
    mutationFn: () => api<Project>(nodePath(nodeID, `/projects/compose/${encoded}/takeover`), { method: 'POST', body: JSON.stringify({ fingerprint: preview.data?.fingerprint, confirmation_name: confirmation, compose, environment }) }),
    onSuccess: async () => { if (shadowSession) await cleanupShadow.mutateAsync(shadowSession); await client.invalidateQueries({ queryKey: ['projects', nodeID] }); void navigate({ to: '/projects/$backend/$projectName', params: { backend: 'compose', projectName } }) },
  })
  const leave = async () => {
    if (step > 0 && !await confirmDialog({ title: zh ? '放弃接管草稿？' : 'Discard takeover draft?', description: zh ? '未保存的变量选择和配置编辑将丢失。' : 'Unsaved variable choices and configuration edits will be lost.', confirmLabel: zh ? '放弃' : 'Discard', danger: true })) return
    if (shadowSession) await cleanupShadow.mutateAsync(shadowSession)
    void navigate({ to: '/projects/$backend/$projectName', params: { backend: 'compose', projectName } })
  }
  const postpone = async () => {
    if (shadowSession && shadowTask?.status !== 'failed' && shadowTask?.status !== 'canceled') await cleanupShadow.mutateAsync(shadowSession)
    shadowSessionRef.current = null
    setShadowSession(null)
    void navigate({ to: '/projects/$backend/$projectName', params: { backend: 'compose', projectName } })
  }

  if (backend !== 'compose') return <ErrorState title={zh ? '后端尚不可用' : 'Backend unavailable'} description={zh ? '当前只支持 Compose Project 接管。' : 'Only Compose Project takeover is currently supported.'} />
  if (preview.isPending) return <LoadingState rows={8} label={zh ? '正在聚合并分析整个 Compose Project' : 'Aggregating and analyzing the complete Compose Project'} />
  if (preview.isError || !preview.data) return <ErrorState title={zh ? '无法分析 Project' : 'Unable to analyze Project'} description={takeoverMessage(preview.error?.message, zh)} />
  const draft = preview.data
  const hasBlockers = draft.blockers.length > 0
  const canShadowPreview = draft.capabilities.includes('shadow_preview')
  const selected = file === 'compose' ? { label: 'compose.yml', value: compose, language: 'yaml' } : { label: '.env', value: environment, language: 'plaintext' }
  const stepLabels = zh ? ['Project 分析', '环境变量', canShadowPreview ? '配置编辑 · 可预演' : '配置编辑', '接管确认'] : ['Project analysis', 'Environment', canShadowPreview ? 'Configuration · preview' : 'Configuration', 'Confirmation']

  return <div className="flex w-full flex-col gap-4">
    <Button variant="ghost" size="sm" className="self-start text-muted-foreground" onClick={() => void leave()}><ChevronLeft />{zh ? '返回 Project' : 'Back to Project'}</Button>
    <ResourceFrame title={zh ? `接管 ${projectName}` : `Take over ${projectName}`} detail={zh ? '将整个 Compose Project 纳入 SUMA 管理' : 'The complete Compose Project will become one SUMA Project'} action={<Badge variant="outline">Compose</Badge>}>
      <div className="flex w-full flex-col gap-5">
        <ol className="grid grid-cols-4 gap-2">{steps.map((name, index) => <li key={name} className={`flex items-center gap-2 border-b-2 px-1 pb-2 text-sm ${index === step ? 'border-primary font-medium' : index < step ? 'border-emerald-500 text-emerald-600 dark:text-emerald-400' : 'border-border text-muted-foreground'}`}><span className="grid size-5 shrink-0 place-items-center rounded-full bg-muted text-xs">{index < step ? <Check className="size-3" /> : index + 1}</span><span className="hidden sm:inline">{stepLabels[index]}</span></li>)}</ol>

        {step === 0 && <AnalysisStep draft={draft} zh={zh} />}
        {step === 0 && <DriftReport services={draft.observation.services} zh={zh} />}
        {step === 1 && <EnvironmentStep variables={draft.variables} choices={choices} revealed={revealed} zh={zh} onChoice={(id, destination) => setChoices((current) => ({ ...current, [id]: destination }))} onReveal={(id) => setRevealed((current) => { const next = new Set(current); if (next.has(id)) next.delete(id); else next.add(id); return next })} />}
        {step === 1 && render.isError && <Alert variant="destructive"><ShieldAlert /><AlertTitle>{zh ? '无法生成配置草稿' : 'Unable to render draft'}</AlertTitle><AlertDescription>{takeoverMessage(render.error.message, zh)}</AlertDescription></Alert>}
        {step === 2 && <Card><CardContent className="flex flex-col gap-3">
          <Tabs value={file} onValueChange={(value) => setFile(value as 'compose' | 'environment')}><TabsList><TabsTrigger value="compose">compose.yml</TabsTrigger><TabsTrigger value="environment">.env</TabsTrigger></TabsList></Tabs>
          <div className="h-[52vh] overflow-hidden rounded-lg ring-1 ring-foreground/10"><Suspense fallback={<div className="grid h-full place-items-center"><Spinner /></div>}><Monaco key={selected.label} language={selected.language} theme={dark ? 'vs-dark' : 'light'} value={selected.value} onChange={(value) => { setValidated(''); assessShadow.reset(); if (file === 'compose') setCompose(value ?? ''); else setEnvironment(value ?? '') }} options={{ minimap: { enabled: false }, automaticLayout: true, wordWrap: 'on', scrollBeyondLastLine: false, readOnly: shadowSession !== null }} /></Suspense></div>
          {validate.isError && <Alert variant="destructive"><ShieldAlert /><AlertDescription>{takeoverMessage(validate.error.message, zh)}</AlertDescription></Alert>}
          {validated === contentSignature && <Alert><Check /><AlertDescription>{zh ? 'Compose 与安全策略校验通过。可直接进入确认，也可以先进行隔离预演。' : 'Compose and security policy validation passed. Continue directly or run an isolated preview first.'}</AlertDescription></Alert>}
          <div className="flex justify-end"><Button variant="outline" disabled={validate.isPending || shadowSession !== null} onClick={() => validate.mutate()}>{validate.isPending ? <Spinner /> : <FileCheck2 />}{zh ? '校验草稿' : 'Validate draft'}</Button></div>
          {validated === contentSignature && <ShadowPreviewPanel zh={zh} assessment={assessShadow.data} assessError={assessShadow.error?.message} operationError={startShadow.error?.message || cleanupShadow.error?.message} assessing={assessShadow.isPending} starting={startShadow.isPending} session={shadowSession} task={shadowTask} status={shadowStatus.data} statusError={shadowStatus.error?.message} cleaning={cleanupShadow.isPending} onAssess={() => assessShadow.mutate()} onStart={() => startShadow.mutate()} onPostpone={() => void postpone()} onReject={() => { if (!shadowSession) return; if (shadowTask?.status === 'failed' || shadowTask?.status === 'canceled') { shadowSessionRef.current = null; setShadowSession(null) } else cleanupShadow.mutate(shadowSession) }} onAccept={async () => { if (shadowSession) await cleanupShadow.mutateAsync(shadowSession); setStep(3) }} />}
        </CardContent></Card>}
        {step === 3 && <Card><CardHeader><CardTitle>{zh ? '确认接管 Project' : 'Confirm Project takeover'}</CardTitle></CardHeader><CardContent className="flex flex-col gap-4"><Alert><AlertTriangle /><AlertTitle>{zh ? '接管不会触发部署' : 'Takeover will not deploy'}</AlertTitle><AlertDescription>{zh ? 'SUMA 将原子保存 compose.yml、.env 和 .suma/project.json。现有容器、网络和运行状态不会改变。' : 'SUMA atomically saves compose.yml, .env, and .suma/project.json. Existing containers, networks, and runtime state remain unchanged.'}</AlertDescription></Alert><div className="space-y-2"><Label htmlFor="project-confirm">{zh ? `输入 Project 名称 ${projectName} 以确认` : `Type ${projectName} to confirm`}</Label><Input id="project-confirm" autoComplete="off" value={confirmation} onChange={(event) => setConfirmation(event.target.value)} /></div>{takeover.isError && <ErrorState description={takeoverMessage(takeover.error.message, zh)} />}</CardContent></Card>}

        <div className="flex items-center justify-between"><Button variant="outline" disabled={step === 0 || render.isPending || takeover.isPending || shadowSession !== null} onClick={() => setStep((current) => Math.max(0, current - 1))}><ChevronLeft />{zh ? '上一步' : 'Back'}</Button>{step === 0 ? <Button disabled={hasBlockers} onClick={() => setStep(1)}>{zh ? '检查环境变量' : 'Review environment'}<ChevronRight /></Button> : step === 1 ? <Button disabled={render.isPending} onClick={() => render.mutate()}>{render.isPending ? <Spinner /> : null}{zh ? '生成配置草稿' : 'Render draft'}<ChevronRight /></Button> : step === 2 ? <Button disabled={validated !== contentSignature || shadowSession !== null} onClick={() => setStep(3)}>{zh ? '跳过预演，进入确认' : 'Skip preview and continue'}<ChevronRight /></Button> : <Button disabled={confirmation !== projectName || takeover.isPending} onClick={() => takeover.mutate()}>{takeover.isPending ? <Spinner /> : <Check />}{zh ? '完成接管' : 'Complete takeover'}</Button>}</div>
      </div>
    </ResourceFrame>
  </div>
}

function ShadowPreviewPanel({ zh, assessment, assessError, operationError, assessing, starting, session, task, status, statusError, cleaning, onAssess, onStart, onPostpone, onReject, onAccept }: { zh: boolean; assessment?: ShadowAssessment; assessError?: string; operationError?: string; assessing: boolean; starting: boolean; session: ShadowPreviewSession | null; task?: TaskRow; status?: ShadowPreviewStatus; statusError?: string; cleaning: boolean; onAssess: () => void; onStart: () => void; onPostpone: () => void; onReject: () => void; onAccept: () => Promise<void> }) {
  const taskStatus = task?.status ?? session?.task.status ?? ''
  const taskMessage = task?.message ?? session?.task.message
  return <div className="rounded-lg border border-border p-4">
    <div className="flex flex-wrap items-start justify-between gap-3">
      <div>
        <h3 className="flex items-center gap-2 text-sm font-medium"><FlaskConical className="size-4" />{zh ? '可选：隔离预演' : 'Optional: isolated preview'}</h3>
        <p className="mt-1 text-xs text-muted-foreground">{zh ? '仅对可安全隔离的无状态草稿启用。预演使用临时 Compose Project，不切换生产流量。' : 'Available only for safely isolated stateless drafts. It uses a temporary Compose Project and never switches production traffic.'}</p>
      </div>
      {!assessment && <Button variant="outline" size="sm" disabled={assessing} onClick={onAssess}>{assessing ? <Spinner /> : <ShieldAlert />}{zh ? '检查预演条件' : 'Check eligibility'}</Button>}
    </div>
    {(assessError || operationError) && <Alert variant="destructive" className="mt-3"><ShieldAlert /><AlertDescription>{takeoverMessage(assessError || operationError, zh)}</AlertDescription></Alert>}
    {assessment && !assessment.eligible && <div className="mt-3 space-y-2">
      <Alert variant="destructive"><ShieldAlert /><AlertTitle>{zh ? '不满足隔离条件' : 'Not eligible'}</AlertTitle><AlertDescription>{zh ? '该草稿仍可直接接管，但不能创建临时预演环境。' : 'The draft can still be taken over directly, but a temporary preview cannot be created.'}</AlertDescription></Alert>
      <ul className="list-disc space-y-1 pl-5 text-xs text-muted-foreground">{assessment.reasons.map((reason) => <li key={reason}>{takeoverMessage(reason, zh)}</li>)}</ul>
    </div>}
    {assessment?.eligible && !session && <div className="mt-3 flex flex-col gap-2">
      <Alert><Check /><AlertTitle>{zh ? '满足严格隔离条件' : 'Strict isolation checks passed'}</AlertTitle><AlertDescription>{zh ? 'SUMA 将创建无固定端口、无生产数据挂载的临时 Project，并等待 healthcheck。' : 'SUMA will create a temporary Project without fixed ports or production data mounts and wait for healthchecks.'}</AlertDescription></Alert>
      {assessment.warnings.map((warning) => <p key={warning} className="text-xs text-amber-600 dark:text-amber-400">{takeoverMessage(warning, zh)}</p>)}
      <Button className="self-end" disabled={starting} onClick={onStart}>{starting ? <Spinner /> : <FlaskConical />}{zh ? '启动隔离预演' : 'Start isolated preview'}</Button>
    </div>}
    {session && <div className="mt-3 flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant="outline">{session.preview_project}</Badge>
        <StatusBadge tone={taskStatus === 'success' ? 'success' : taskStatus === 'failed' || taskStatus === 'canceled' ? 'critical' : 'warning'}>{localizedLabel(taskStatusLabels, taskStatus, zh)}</StatusBadge>
        <span className="text-xs text-muted-foreground">{takeoverMessage(taskMessage, zh)}</span>
      </div>
      {task?.status === 'failed' || task?.status === 'canceled' ? <Alert variant="destructive"><AlertTriangle /><AlertDescription>{takeoverMessage(task.message, zh)}</AlertDescription></Alert> : null}
      {statusError && <Alert variant="destructive"><AlertDescription>{takeoverMessage(statusError, zh)}</AlertDescription></Alert>}
      {status && <>
        <div className="grid gap-3 lg:grid-cols-2">
          <div><p className="mb-1 text-xs font-medium">{zh ? '容器状态' : 'Container status'}</p><pre className="max-h-56 overflow-auto rounded-lg bg-muted p-3 font-mono text-xs whitespace-pre-wrap">{status.containers}</pre></div>
          <div><p className="mb-1 text-xs font-medium">{zh ? '预演日志' : 'Preview logs'}</p><pre className="max-h-56 overflow-auto rounded-lg bg-muted p-3 font-mono text-xs whitespace-pre-wrap">{status.logs}</pre></div>
        </div>
        <div className="flex flex-wrap justify-end gap-2">
          <Button variant="outline" disabled={cleaning} onClick={onPostpone}><CirclePause />{zh ? '稍后决定并清理' : 'Decide later and clean up'}</Button>
          <Button variant="outline" disabled={cleaning} onClick={onReject}>{cleaning ? <Spinner /> : <Trash2 />}{zh ? '放弃预演并清理' : 'Reject and clean up'}</Button>
          <Button disabled={cleaning} onClick={() => void onAccept()}>{cleaning ? <Spinner /> : <Check />}{zh ? '采用草稿，继续接管' : 'Accept draft and continue'}</Button>
        </div>
      </>}
    </div>}
  </div>
}

function AnalysisStep({ draft, zh }: { draft: ProjectTakeoverDraft; zh: boolean }) {
  const instanceCount = draft.observation.services.reduce((total, service) => total + service.instances.length, 0)
  return <div className="flex flex-col gap-4">
    <div className="flex flex-wrap gap-2">
      <StatusBadge tone={draft.source === 'mapped' ? 'success' : 'warning'}>{draft.source === 'mapped' ? (zh ? '安全源配置' : 'Mapped source') : (zh ? '运行态重建' : 'Runtime reconstruction')}</StatusBadge>
      <StatusBadge tone={draft.confidence === 'high' ? 'success' : draft.confidence === 'medium' ? 'warning' : 'critical'}>{zh ? '置信度' : 'Confidence'} · {confidenceLabel(draft.confidence, zh)}</StatusBadge>
      <Badge variant="outline">{zh ? `${draft.observation.services.length} 个 Service` : `${draft.observation.services.length} Services`}</Badge>
      <Badge variant="outline">{zh ? `${instanceCount} 个容器实例` : `${instanceCount} Instances`}</Badge>
    </div>
    {draft.blockers.map((message) => <Alert key={message} variant="destructive"><ShieldAlert /><AlertTitle>{zh ? '阻断项' : 'Blocker'}</AlertTitle><AlertDescription>{takeoverMessage(message, zh)}</AlertDescription></Alert>)}
    {draft.warnings.map((message) => <Alert key={message}><AlertTriangle /><AlertDescription>{takeoverMessage(message, zh)}</AlertDescription></Alert>)}
    <Card><CardContent><Table>
      <TableHeader><TableRow><TableHead>Service</TableHead><TableHead>{zh ? '副本' : 'Replicas'}</TableHead><TableHead>{zh ? '配置变体' : 'Variants'}</TableHead><TableHead>{zh ? '运行态偏移' : 'Drift'}</TableHead><TableHead>{zh ? '容器实例' : 'Container instances'}</TableHead></TableRow></TableHeader>
      <TableBody>{draft.observation.services.map((service) => <TableRow key={service.name}>
        <TableCell className="font-medium">{service.name}</TableCell>
        <TableCell>{service.desired_replicas}</TableCell>
        <TableCell>{service.config_variants.length}</TableCell>
        <TableCell><StatusBadge tone={service.drift_status === 'in_sync' ? 'success' : 'warning'}>{localizedLabel(driftStatusLabels, service.drift_status, zh)}</StatusBadge></TableCell>
        <TableCell className="text-muted-foreground">{service.instances.length ? service.instances.map((instance) => instance.container_name).join(', ') : (zh ? '无运行实例' : 'No running instances')}</TableCell>
      </TableRow>)}</TableBody>
    </Table></CardContent></Card>
    {(draft.observation.one_off_containers.length > 0 || draft.observation.orphan_containers.length > 0) && <Alert><AlertTriangle /><AlertTitle>{zh ? '不会写入 Service 的运行实例' : 'Runtime instances excluded from Services'}</AlertTitle><AlertDescription>{zh ? `一次性容器（one-off）${draft.observation.one_off_containers.length} 个，孤立容器 ${draft.observation.orphan_containers.length} 个；接管不会删除这些容器。` : `${draft.observation.one_off_containers.length} one-off and ${draft.observation.orphan_containers.length} orphan instances; takeover will not delete them.`}</AlertDescription></Alert>}
  </div>
}

function DriftReport({ services, zh }: { services: ProjectTakeoverDraft['observation']['services']; zh: boolean }) {
  const affected = services.filter((service) => service.drift_status !== 'in_sync')
  if (!affected.length) return null
  return <Card>
    <CardHeader><CardTitle>{zh ? '运行态偏移报告' : 'Runtime difference report'}</CardTitle></CardHeader>
    <CardContent className="flex flex-col gap-3">{affected.map((service) => <Alert key={service.name} variant={service.drift_status === 'orphan' ? 'destructive' : 'default'}>
      <AlertTriangle />
      <AlertTitle>{service.name} · {localizedLabel(driftStatusLabels, service.drift_status, zh)}</AlertTitle>
      <AlertDescription><div className="flex flex-col gap-2">
        <span>{service.instances.length ? (zh ? `涉及 ${service.instances.length} 个容器实例` : `${service.instances.length} container instances affected`) : (zh ? '源配置已声明，但当前没有容器实例' : 'Declared by source configuration, but no container instance currently exists')}</span>
        {Boolean(service.drift_reasons?.length) && <div className="flex flex-wrap gap-1">{service.drift_reasons?.map((reason) => <Badge key={reason} variant="outline">{localizedLabel(driftReasonLabels, reason, zh)}</Badge>)}</div>}
        {Boolean(service.drift_fields?.length) && <span className="font-mono text-xs">{zh ? '差异字段' : 'Different fields'}：{service.drift_fields?.join(', ')}</span>}
      </div></AlertDescription>
    </Alert>)}</CardContent>
  </Card>
}

function EnvironmentStep({ variables, choices, revealed, zh, onChoice, onReveal }: { variables: EnvironmentCandidate[]; choices: Record<string, Destination>; revealed: Set<string>; zh: boolean; onChoice: (id: string, value: Destination) => void; onReveal: (id: string) => void }) {
  const [showImageDefaults, setShowImageDefaults] = useState(false)
  const reviewVariables = variables.filter((variable) => variable.source === 'compose_explicit' || variable.source === 'explicit_inferred')
  const unknownVariables = variables.filter((variable) => variable.source === 'unknown')
  const imageDefaults = variables.filter((variable) => variable.source === 'image_default')

  return <div className="flex flex-col gap-4">
    <div className="flex flex-wrap items-center gap-2">
      <Badge variant={reviewVariables.length ? 'default' : 'outline'}>{zh ? `需要确认 ${reviewVariables.length}` : `Review ${reviewVariables.length}`}</Badge>
      <Badge variant={unknownVariables.length ? 'destructive' : 'outline'}>{zh ? `来源未知 ${unknownVariables.length}` : `Unknown ${unknownVariables.length}`}</Badge>
      <Badge variant="outline">{zh ? `镜像默认值 ${imageDefaults.length}` : `Image defaults ${imageDefaults.length}`}</Badge>
    </div>

    {reviewVariables.length ? <Card>
      <CardHeader><CardTitle>{zh ? '需要确认的环境变量' : 'Environment variables to review'}</CardTitle></CardHeader>
      <CardContent><EnvironmentVariableTable variables={reviewVariables} choices={choices} revealed={revealed} zh={zh} onChoice={onChoice} onReveal={onReveal} /></CardContent>
    </Card> : <Alert><Check /><AlertTitle>{zh ? '没有需要确认的环境变量' : 'No environment variables require review'}</AlertTitle><AlertDescription>{zh ? '未发现 Compose 显式配置或运行态推断变量。' : 'No explicit Compose or runtime-inferred values were found.'}</AlertDescription></Alert>}

    {unknownVariables.length > 0 && <Card className="border-destructive/40">
      <CardHeader><CardTitle className="text-destructive">{zh ? '来源未知，需要谨慎处理' : 'Unknown source — review carefully'}</CardTitle></CardHeader>
      <CardContent className="flex flex-col gap-3">
        <Alert variant="destructive"><ShieldAlert /><AlertDescription>{zh ? 'SUMA 无法读取镜像默认 ENV，因此不能判断这些变量来自镜像还是原 Compose 配置。默认不会写入；如需保留，请逐项选择写入位置。' : 'SUMA could not inspect image defaults, so it cannot determine whether these values came from the image or Compose. They are excluded by default; choose a destination only when needed.'}</AlertDescription></Alert>
        <EnvironmentVariableTable variables={unknownVariables} choices={choices} revealed={revealed} zh={zh} onChoice={onChoice} onReveal={onReveal} />
      </CardContent>
    </Card>}

    {imageDefaults.length > 0 && <Collapsible open={showImageDefaults} onOpenChange={setShowImageDefaults} className="group/image-defaults overflow-hidden rounded-xl border bg-card">
      <CollapsibleTrigger className="flex w-full cursor-pointer items-center gap-2 px-4 py-3 text-left hover:bg-muted/50">
        <ChevronDown className="size-4 text-muted-foreground transition-transform group-data-open/image-defaults:rotate-180" />
        <span className="font-medium">{zh ? `镜像默认 ENV（${imageDefaults.length}）` : `Image-default ENV (${imageDefaults.length})`}</span>
        <Badge variant="outline" className="ml-auto">{zh ? '已自动排除' : 'Automatically excluded'}</Badge>
      </CollapsibleTrigger>
      <CollapsibleContent className="border-t">
        <div className="px-4 py-3">
          <p className="mb-3 text-xs text-muted-foreground">{zh ? '这些变量与 Image Inspect 返回的默认 ENV 完全一致，不会写入 compose.yml 或 .env。' : 'These values exactly match Image Inspect defaults and will not be written to compose.yml or .env.'}</p>
          <EnvironmentVariableTable variables={imageDefaults} choices={choices} revealed={revealed} zh={zh} onChoice={onChoice} onReveal={onReveal} autoExcluded />
        </div>
      </CollapsibleContent>
    </Collapsible>}

    <p className="text-xs text-muted-foreground">{zh ? '.env 仍是明文文件，只是将变量值与 Compose YAML 分离，并不提供加密。敏感值不会写入浏览器存储、日志、任务记录或审计记录。' : '.env remains plaintext and only separates values from Compose YAML; it is not encrypted. Sensitive values are not written to browser storage, logs, Tasks, or Audit.'}</p>
  </div>
}

function EnvironmentVariableTable({ variables, choices, revealed, zh, onChoice, onReveal, autoExcluded = false }: { variables: EnvironmentCandidate[]; choices: Record<string, Destination>; revealed: Set<string>; zh: boolean; onChoice: (id: string, value: Destination) => void; onReveal: (id: string) => void; autoExcluded?: boolean }) {
  return <Table>
    <TableHeader><TableRow><TableHead>Service</TableHead><TableHead>{zh ? '变量名' : 'Key'}</TableHead><TableHead>{zh ? '值' : 'Value'}</TableHead><TableHead>{zh ? '识别来源' : 'Source'}</TableHead><TableHead className="w-44">{zh ? '写入位置' : 'Destination'}</TableHead></TableRow></TableHeader>
    <TableBody>{variables.map((variable) => <TableRow key={variable.id}>
      <TableCell>{variable.service}</TableCell>
      <TableCell className="font-mono text-xs">{variable.key}</TableCell>
      <TableCell><div className="flex max-w-72 items-center gap-1"><span className="truncate font-mono text-xs">{variable.sensitive && !revealed.has(variable.id) ? '••••••••' : variable.value}</span>{variable.sensitive && <Button variant="ghost" size="icon-xs" aria-label={revealed.has(variable.id) ? (zh ? '隐藏敏感值' : 'Hide') : (zh ? '显示敏感值' : 'Reveal')} onClick={() => onReveal(variable.id)}>{revealed.has(variable.id) ? <EyeOff /> : <Eye />}</Button>}</div></TableCell>
      <TableCell><Badge variant={variable.source === 'unknown' ? 'destructive' : 'outline'}>{localizedLabel(environmentSourceLabels, variable.source, zh)}</Badge><p className="mt-1 max-w-72 text-xs text-muted-foreground">{localizedLabel(environmentReasonLabels, variable.source, zh)}</p></TableCell>
      <TableCell>{autoExcluded ? <StatusBadge tone="neutral">{zh ? '自动排除' : 'Excluded'}</StatusBadge> : <Select value={choices[variable.id] ?? variable.destination} onValueChange={(value) => onChoice(variable.id, value as Destination)}><SelectTrigger className="w-40"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="compose">compose.yml</SelectItem><SelectItem value="env">.env</SelectItem><SelectItem value="exclude">{zh ? '不写入' : 'Exclude'}</SelectItem></SelectContent></Select>}</TableCell>
    </TableRow>)}</TableBody>
  </Table>
}
