import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useParams } from '@tanstack/react-router'
import { AlertTriangle, Check, ChevronLeft, ChevronRight, CirclePause, Eye, EyeOff, FileCheck2, FlaskConical, ShieldAlert, Trash2 } from 'lucide-react'
import { lazy, Suspense, useEffect, useMemo, useRef, useState } from 'react'
import { Alert, AlertDescription, AlertTitle } from '../components/ui/alert'
import { Badge } from '../components/ui/badge'
import { Button } from '../components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '../components/ui/card'
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
    setChoices(Object.fromEntries(preview.data.variables.map((variable) => [variable.id, variable.destination])))
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
  if (preview.isError || !preview.data) return <ErrorState title={zh ? '无法分析 Project' : 'Unable to analyze Project'} description={preview.error?.message || ''} />
  const draft = preview.data
  const hasBlockers = draft.blockers.length > 0
  const canShadowPreview = draft.capabilities.includes('shadow_preview')
  const selected = file === 'compose' ? { label: 'compose.yml', value: compose, language: 'yaml' } : { label: '.env', value: environment, language: 'plaintext' }
  const stepLabels = zh ? ['Project 分析', '环境变量', canShadowPreview ? '配置编辑 · 可预演' : '配置编辑', '接管确认'] : ['Project analysis', 'Environment', canShadowPreview ? 'Configuration · preview' : 'Configuration', 'Confirmation']

  return <div className="flex w-full flex-col gap-4">
    <Button variant="ghost" size="sm" className="self-start text-muted-foreground" onClick={() => void leave()}><ChevronLeft />{zh ? '返回 Project' : 'Back to Project'}</Button>
    <ResourceFrame title={zh ? `接管 ${projectName}` : `Take over ${projectName}`} detail={zh ? '整个 Compose Project 将作为一个 SUMA Project 接管' : 'The complete Compose Project will become one SUMA Project'} action={<Badge variant="outline">Compose</Badge>}>
      <div className="flex w-full flex-col gap-5">
        <ol className="grid grid-cols-4 gap-2">{steps.map((name, index) => <li key={name} className={`flex items-center gap-2 border-b-2 px-1 pb-2 text-sm ${index === step ? 'border-primary font-medium' : index < step ? 'border-emerald-500 text-emerald-600 dark:text-emerald-400' : 'border-border text-muted-foreground'}`}><span className="grid size-5 shrink-0 place-items-center rounded-full bg-muted text-xs">{index < step ? <Check className="size-3" /> : index + 1}</span><span className="hidden sm:inline">{stepLabels[index]}</span></li>)}</ol>

        {step === 0 && <AnalysisStep draft={draft} zh={zh} />}
        {step === 0 && <DriftReport services={draft.observation.services} zh={zh} />}
        {step === 1 && <EnvironmentStep variables={draft.variables} choices={choices} revealed={revealed} zh={zh} onChoice={(id, destination) => setChoices((current) => ({ ...current, [id]: destination }))} onReveal={(id) => setRevealed((current) => { const next = new Set(current); if (next.has(id)) next.delete(id); else next.add(id); return next })} />}
        {step === 2 && <Card><CardContent className="flex flex-col gap-3">
          <Tabs value={file} onValueChange={(value) => setFile(value as 'compose' | 'environment')}><TabsList><TabsTrigger value="compose">compose.yml</TabsTrigger><TabsTrigger value="environment">.env</TabsTrigger></TabsList></Tabs>
          <div className="h-[52vh] overflow-hidden rounded-lg ring-1 ring-foreground/10"><Suspense fallback={<div className="grid h-full place-items-center"><Spinner /></div>}><Monaco key={selected.label} language={selected.language} theme={dark ? 'vs-dark' : 'light'} value={selected.value} onChange={(value) => { setValidated(''); assessShadow.reset(); if (file === 'compose') setCompose(value ?? ''); else setEnvironment(value ?? '') }} options={{ minimap: { enabled: false }, automaticLayout: true, wordWrap: 'on', scrollBeyondLastLine: false, readOnly: shadowSession !== null }} /></Suspense></div>
          {validate.isError && <Alert variant="destructive"><ShieldAlert /><AlertDescription>{validate.error.message}</AlertDescription></Alert>}
          {validated === contentSignature && <Alert><Check /><AlertDescription>{zh ? 'Compose 与安全策略校验通过。可直接进入确认，也可以先进行隔离预演。' : 'Compose and security policy validation passed. Continue directly or run an isolated preview first.'}</AlertDescription></Alert>}
          <div className="flex justify-end"><Button variant="outline" disabled={validate.isPending || shadowSession !== null} onClick={() => validate.mutate()}>{validate.isPending ? <Spinner /> : <FileCheck2 />}{zh ? '校验草稿' : 'Validate draft'}</Button></div>
          {validated === contentSignature && <ShadowPreviewPanel zh={zh} assessment={assessShadow.data} assessError={assessShadow.error?.message} operationError={startShadow.error?.message || cleanupShadow.error?.message} assessing={assessShadow.isPending} starting={startShadow.isPending} session={shadowSession} task={shadowTask} status={shadowStatus.data} statusError={shadowStatus.error?.message} cleaning={cleanupShadow.isPending} onAssess={() => assessShadow.mutate()} onStart={() => startShadow.mutate()} onPostpone={() => void postpone()} onReject={() => { if (!shadowSession) return; if (shadowTask?.status === 'failed' || shadowTask?.status === 'canceled') { shadowSessionRef.current = null; setShadowSession(null) } else cleanupShadow.mutate(shadowSession) }} onAccept={async () => { if (shadowSession) await cleanupShadow.mutateAsync(shadowSession); setStep(3) }} />}
        </CardContent></Card>}
        {step === 3 && <Card><CardHeader><CardTitle>{zh ? '确认 Project 接管' : 'Confirm Project takeover'}</CardTitle></CardHeader><CardContent className="flex flex-col gap-4"><Alert><AlertTriangle /><AlertTitle>{zh ? '接管不会部署' : 'Takeover will not deploy'}</AlertTitle><AlertDescription>{zh ? 'SUMA 将原子保存 compose.yml、.env 和 .suma/project.json。现有容器、网络和运行状态不会改变。' : 'SUMA atomically saves compose.yml, .env, and .suma/project.json. Existing containers, networks, and runtime state remain unchanged.'}</AlertDescription></Alert><div className="space-y-2"><Label htmlFor="project-confirm">{zh ? `输入 ${projectName} 确认` : `Type ${projectName} to confirm`}</Label><Input id="project-confirm" autoComplete="off" value={confirmation} onChange={(event) => setConfirmation(event.target.value)} /></div>{takeover.isError && <ErrorState description={takeover.error.message} />}</CardContent></Card>}

        <div className="flex items-center justify-between"><Button variant="outline" disabled={step === 0 || render.isPending || takeover.isPending || shadowSession !== null} onClick={() => setStep((current) => Math.max(0, current - 1))}><ChevronLeft />{zh ? '上一步' : 'Back'}</Button>{step === 0 ? <Button disabled={hasBlockers} onClick={() => setStep(1)}>{zh ? '处理环境变量' : 'Review environment'}<ChevronRight /></Button> : step === 1 ? <Button disabled={render.isPending} onClick={() => render.mutate()}>{render.isPending ? <Spinner /> : null}{zh ? '生成配置草稿' : 'Render draft'}<ChevronRight /></Button> : step === 2 ? <Button disabled={validated !== contentSignature || shadowSession !== null} onClick={() => setStep(3)}>{zh ? '跳过预演并确认' : 'Skip preview and continue'}<ChevronRight /></Button> : <Button disabled={confirmation !== projectName || takeover.isPending} onClick={() => takeover.mutate()}>{takeover.isPending ? <Spinner /> : <Check />}{zh ? '完成接管' : 'Complete takeover'}</Button>}</div>
      </div>
    </ResourceFrame>
  </div>
}

function ShadowPreviewPanel({ zh, assessment, assessError, operationError, assessing, starting, session, task, status, statusError, cleaning, onAssess, onStart, onPostpone, onReject, onAccept }: { zh: boolean; assessment?: ShadowAssessment; assessError?: string; operationError?: string; assessing: boolean; starting: boolean; session: ShadowPreviewSession | null; task?: TaskRow; status?: ShadowPreviewStatus; statusError?: string; cleaning: boolean; onAssess: () => void; onStart: () => void; onPostpone: () => void; onReject: () => void; onAccept: () => Promise<void> }) {
  return <div className="rounded-lg border border-border p-4">
    <div className="flex flex-wrap items-start justify-between gap-3"><div><h3 className="flex items-center gap-2 text-sm font-medium"><FlaskConical className="size-4" />{zh ? '可选：隔离预演' : 'Optional: isolated preview'}</h3><p className="mt-1 text-xs text-muted-foreground">{zh ? '仅对可安全隔离的无状态草稿启用。预演使用临时 Compose Project，不切换生产流量。' : 'Available only for safely isolated stateless drafts. It uses a temporary Compose Project and never switches production traffic.'}</p></div>{!assessment && <Button variant="outline" size="sm" disabled={assessing} onClick={onAssess}>{assessing ? <Spinner /> : <ShieldAlert />}{zh ? '检查预演资格' : 'Check eligibility'}</Button>}</div>
    {(assessError || operationError) && <Alert variant="destructive" className="mt-3"><ShieldAlert /><AlertDescription>{assessError || operationError}</AlertDescription></Alert>}
    {assessment && !assessment.eligible && <div className="mt-3 space-y-2"><Alert variant="destructive"><ShieldAlert /><AlertTitle>{zh ? '不满足隔离条件' : 'Not eligible'}</AlertTitle><AlertDescription>{zh ? '该草稿仍可直接接管，但不能创建临时预演环境。' : 'The draft can still be taken over directly, but a temporary preview cannot be created.'}</AlertDescription></Alert><ul className="list-disc space-y-1 pl-5 text-xs text-muted-foreground">{assessment.reasons.map((reason) => <li key={reason}>{reason}</li>)}</ul></div>}
    {assessment?.eligible && !session && <div className="mt-3 flex flex-col gap-2"><Alert><Check /><AlertTitle>{zh ? '满足严格隔离条件' : 'Strict isolation checks passed'}</AlertTitle><AlertDescription>{zh ? 'SUMA 将创建无固定端口、无生产数据挂载的临时 Project，并等待 healthcheck。' : 'SUMA will create a temporary Project without fixed ports or production data mounts and wait for healthchecks.'}</AlertDescription></Alert>{assessment.warnings.map((warning) => <p key={warning} className="text-xs text-amber-600 dark:text-amber-400">{warning}</p>)}<Button className="self-end" disabled={starting} onClick={onStart}>{starting ? <Spinner /> : <FlaskConical />}{zh ? '启动隔离预演' : 'Start isolated preview'}</Button></div>}
    {session && <div className="mt-3 flex flex-col gap-3"><div className="flex flex-wrap items-center gap-2"><Badge variant="outline">{session.preview_project}</Badge><StatusBadge tone={task?.status === 'success' ? 'success' : task?.status === 'failed' || task?.status === 'canceled' ? 'critical' : 'warning'}>{task?.status ?? session.task.status}</StatusBadge><span className="text-xs text-muted-foreground">{task?.message}</span></div>{task?.status === 'failed' || task?.status === 'canceled' ? <Alert variant="destructive"><AlertTriangle /><AlertDescription>{task.message}</AlertDescription></Alert> : null}{statusError && <Alert variant="destructive"><AlertDescription>{statusError}</AlertDescription></Alert>}{status && <><div className="grid gap-3 lg:grid-cols-2"><div><p className="mb-1 text-xs font-medium">{zh ? '容器状态' : 'Container status'}</p><pre className="max-h-56 overflow-auto rounded-lg bg-muted p-3 font-mono text-xs whitespace-pre-wrap">{status.containers}</pre></div><div><p className="mb-1 text-xs font-medium">{zh ? '预演日志' : 'Preview logs'}</p><pre className="max-h-56 overflow-auto rounded-lg bg-muted p-3 font-mono text-xs whitespace-pre-wrap">{status.logs}</pre></div></div><div className="flex flex-wrap justify-end gap-2"><Button variant="outline" disabled={cleaning} onClick={onPostpone}><CirclePause />{zh ? '暂不决定并清理' : 'Decide later and clean up'}</Button><Button variant="outline" disabled={cleaning} onClick={onReject}>{cleaning ? <Spinner /> : <Trash2 />}{zh ? '拒绝并清理' : 'Reject and clean up'}</Button><Button disabled={cleaning} onClick={() => void onAccept()}>{cleaning ? <Spinner /> : <Check />}{zh ? '接受草稿并继续接管' : 'Accept draft and continue'}</Button></div></>}</div>}
  </div>
}

function AnalysisStep({ draft, zh }: { draft: ProjectTakeoverDraft; zh: boolean }) {
  return <div className="flex flex-col gap-4"><div className="flex flex-wrap gap-2"><StatusBadge tone={draft.source === 'mapped' ? 'success' : 'warning'}>{draft.source === 'mapped' ? (zh ? '安全源配置' : 'Mapped source') : (zh ? '运行态重建' : 'Runtime reconstruction')}</StatusBadge><StatusBadge tone={draft.confidence === 'high' ? 'success' : draft.confidence === 'medium' ? 'warning' : 'critical'}>{zh ? '置信度' : 'Confidence'} · {draft.confidence}</StatusBadge><Badge variant="outline">{draft.observation.services.length} Services</Badge><Badge variant="outline">{draft.observation.services.reduce((total, service) => total + service.instances.length, 0)} Instances</Badge></div>{draft.blockers.map((message) => <Alert key={message} variant="destructive"><ShieldAlert /><AlertTitle>{zh ? '阻断项' : 'Blocker'}</AlertTitle><AlertDescription>{message}</AlertDescription></Alert>)}{draft.warnings.map((message) => <Alert key={message}><AlertTriangle /><AlertDescription>{message}</AlertDescription></Alert>)}<Card><CardContent><Table><TableHeader><TableRow><TableHead>Service</TableHead><TableHead>{zh ? '副本' : 'Replicas'}</TableHead><TableHead>{zh ? '变体' : 'Variants'}</TableHead><TableHead>Drift</TableHead><TableHead>{zh ? '容器实例' : 'Container instances'}</TableHead></TableRow></TableHeader><TableBody>{draft.observation.services.map((service) => <TableRow key={service.name}><TableCell className="font-medium">{service.name}</TableCell><TableCell>{service.desired_replicas}</TableCell><TableCell>{service.config_variants.length}</TableCell><TableCell><StatusBadge tone={service.drift_status === 'in_sync' ? 'success' : 'warning'}>{service.drift_status}</StatusBadge></TableCell><TableCell className="text-muted-foreground">{service.instances.map((instance) => instance.container_name).join(', ')}</TableCell></TableRow>)}</TableBody></Table></CardContent></Card>{(draft.observation.one_off_containers.length > 0 || draft.observation.orphan_containers.length > 0) && <Alert><AlertTriangle /><AlertTitle>{zh ? '不会写入 Service 的运行实例' : 'Runtime instances excluded from Services'}</AlertTitle><AlertDescription>{zh ? `one-off ${draft.observation.one_off_containers.length} 个，orphan ${draft.observation.orphan_containers.length} 个；接管不会删除它们。` : `${draft.observation.one_off_containers.length} one-off and ${draft.observation.orphan_containers.length} orphan instances; takeover will not delete them.`}</AlertDescription></Alert>}</div>
}

function DriftReport({ services, zh }: { services: ProjectTakeoverDraft['observation']['services']; zh: boolean }) {
  const affected = services.filter((service) => service.drift_status !== 'in_sync')
  if (!affected.length) return null
  return <Card><CardHeader><CardTitle>{zh ? '运行态差异报告' : 'Runtime difference report'}</CardTitle></CardHeader><CardContent className="flex flex-col gap-3">{affected.map((service) => <Alert key={service.name} variant={service.drift_status === 'orphan' ? 'destructive' : 'default'}><AlertTriangle /><AlertTitle>{service.name} · {service.drift_status}</AlertTitle><AlertDescription><div className="flex flex-col gap-2"><span>{service.instances.length ? (zh ? `涉及 ${service.instances.length} 个容器实例` : `${service.instances.length} container instances affected`) : (zh ? '源配置已声明，但当前没有容器实例' : 'Declared by source configuration, but no container instance currently exists')}</span>{Boolean(service.drift_reasons?.length) && <div className="flex flex-wrap gap-1">{service.drift_reasons?.map((reason) => <Badge key={reason} variant="outline">{reason}</Badge>)}</div>}{Boolean(service.drift_fields?.length) && <span className="font-mono text-xs">{zh ? '差异字段' : 'Different fields'}: {service.drift_fields?.join(', ')}</span>}</div></AlertDescription></Alert>)}</CardContent></Card>
}

function EnvironmentStep({ variables, choices, revealed, zh, onChoice, onReveal }: { variables: EnvironmentCandidate[]; choices: Record<string, Destination>; revealed: Set<string>; zh: boolean; onChoice: (id: string, value: Destination) => void; onReveal: (id: string) => void }) {
  if (!variables.length) return <Alert><Check /><AlertTitle>{zh ? '没有需要处理的显式变量' : 'No explicit variables to review'}</AlertTitle><AlertDescription>{zh ? '镜像默认 ENV 已排除；草稿中没有推断出的显式环境变量。' : 'Image-default ENV values were excluded and no explicit variables were inferred.'}</AlertDescription></Alert>
  return <Card><CardContent><Table><TableHeader><TableRow><TableHead>Service</TableHead><TableHead>Key</TableHead><TableHead>{zh ? '值' : 'Value'}</TableHead><TableHead>{zh ? '来源' : 'Source'}</TableHead><TableHead className="w-44">{zh ? '写入位置' : 'Destination'}</TableHead></TableRow></TableHeader><TableBody>{variables.map((variable) => <TableRow key={variable.id}><TableCell>{variable.service}</TableCell><TableCell className="font-mono text-xs">{variable.key}</TableCell><TableCell><div className="flex max-w-72 items-center gap-1"><span className="truncate font-mono text-xs">{variable.sensitive && !revealed.has(variable.id) ? '••••••••' : variable.value}</span>{variable.sensitive && <Button variant="ghost" size="icon-xs" aria-label={revealed.has(variable.id) ? (zh ? '隐藏' : 'Hide') : (zh ? '揭示' : 'Reveal')} onClick={() => onReveal(variable.id)}>{revealed.has(variable.id) ? <EyeOff /> : <Eye />}</Button>}</div></TableCell><TableCell><Badge variant="outline">{variable.source}</Badge><p className="mt-1 max-w-72 text-xs text-muted-foreground">{variable.reason}</p></TableCell><TableCell><Select value={choices[variable.id] ?? variable.destination} onValueChange={(value) => onChoice(variable.id, value as Destination)}><SelectTrigger className="w-40"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="compose">compose.yml</SelectItem><SelectItem value="env">.env</SelectItem><SelectItem value="exclude">{zh ? '排除' : 'Exclude'}</SelectItem></SelectContent></Select></TableCell></TableRow>)}</TableBody></Table><p className="mt-3 text-xs text-muted-foreground">{zh ? '.env 仍是明文文件，只是将值与 Compose YAML 分离；不会提供加密。敏感值不会写入浏览器存储、日志、Task 或 Audit。' : '.env remains plaintext and only separates values from Compose YAML; it is not encrypted. Sensitive values are not written to browser storage, logs, Tasks, or Audit.'}</p></CardContent></Card>
}
