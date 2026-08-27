import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useParams } from '@tanstack/react-router'
import { ChevronLeft, FileCheck2, Play, Save, Trash2 } from 'lucide-react'
import { lazy, Suspense, useEffect, useState } from 'react'
import { Badge } from '../components/ui/badge'
import { Button } from '../components/ui/button'
import { Card, CardContent } from '../components/ui/card'
import { ErrorState } from '../components/ui/error-state'
import { LoadingState } from '../components/ui/loading-state'
import { Spinner } from '../components/ui/spinner'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'
import { Tabs, TabsList, TabsTrigger } from '../components/ui/tabs'
import type { ComposeProject } from '../features/compose/types'
import type { ContainerSummary } from '../features/containers/types'
import { api } from '../lib/api'
import { nodePath } from '../lib/nodes'
import { useI18n } from '../lib/i18n'
import { confirmDialog, promptDialog } from '../stores/dialog'
import { useUIStore } from '../stores/ui'
import { ResourceFrame } from './images'

const Monaco = lazy(() => import('@monaco-editor/react'))
type View = 'Files' | 'Services' | 'Logs'
const activeClass = 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-400'
const warnClass = 'border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-400'

export function ComposeDetailPage() {
  const { projectName } = useParams({ from: '/compose/$projectName' })
  const encodedName = encodeURIComponent(projectName)
  const navigate = useNavigate()
  const client = useQueryClient()
  const { t, language } = useI18n()
  const zh = language === 'zh-CN'
  const theme = useUIStore((state) => state.theme)
  const nodeID = useUIStore((state) => state.currentNodeID)
  const dark = theme === 'dark' || (theme === 'system' && matchMedia('(prefers-color-scheme: dark)').matches)
  const query = useQuery({ queryKey: ['compose', nodeID, projectName], queryFn: () => api<ComposeProject>(nodePath(nodeID, `/compose/${encodedName}`)) })
  const [view, setView] = useState<View>('Files')
  const [file, setFile] = useState('compose')
  const [compose, setCompose] = useState('')
  const [environment, setEnvironment] = useState('')
  const [notice, setNotice] = useState('')

  useEffect(() => {
    if (!query.data) return
    setCompose(query.data.compose)
    setEnvironment(query.data.environment)
  }, [query.data])

  const services = useQuery({ queryKey: ['compose-services', nodeID, projectName], queryFn: () => api<ContainerSummary[]>(nodePath(nodeID, `/compose/${encodedName}/services`)), enabled: view === 'Services', refetchInterval: 5_000 })
  const logs = useQuery({ queryKey: ['compose-logs', nodeID, projectName], queryFn: () => api<{ logs: string }>(nodePath(nodeID, `/compose/${encodedName}/logs`)), enabled: view === 'Logs', refetchInterval: 3_000, retry: false })
  const save = useMutation({
    mutationFn: () => api<ComposeProject>(nodePath(nodeID, `/compose/${encodedName}`), { method: 'PUT', body: JSON.stringify({ compose, environment }) }),
    onSuccess: (row) => {
      client.setQueryData(['compose', nodeID, projectName], row)
      setNotice(zh ? '已保存。' : 'Saved.')
    },
    onError: (error) => setNotice(error.message),
  })
  const validate = useMutation({
    mutationFn: () => api(nodePath(nodeID, `/compose/${encodedName}/validate`), { method: 'POST', body: JSON.stringify({ compose, environment }) }),
    onSuccess: () => setNotice(zh ? 'Compose 配置有效。' : 'Compose configuration is valid.'),
    onError: (error) => setNotice(error.message),
  })
  const action = useMutation({
    mutationFn: (name: string) => api(nodePath(nodeID, `/compose/${encodedName}/${name}`), { method: 'POST' }),
    onSuccess: () => {
      setNotice(zh ? '任务已启动。' : 'Task started.')
      void client.invalidateQueries({ queryKey: ['tasks', nodeID] })
    },
    onError: (error) => setNotice(error.message),
  })

  const deploy = async () => {
    const changes = [compose !== query.data?.compose && 'compose.yml', environment !== query.data?.environment && '.env'].filter(Boolean)
    if (changes.length && !await confirmDialog({ title: t('deployChanges'), description: t('deployChangesDescription', { files: changes.join(' / ') }), confirmLabel: zh ? '保存并部署' : 'Save & deploy' })) return
    if (changes.length) await save.mutateAsync()
    await action.mutateAsync('update')
  }
  const remove = async () => {
    if (await promptDialog({ title: t('removeProject'), description: t('removeProjectDescription'), confirmLabel: t('remove'), danger: true, input: { label: t('typeToConfirm', { value: projectName }), requiredValue: projectName } }) !== projectName) return
    await api(nodePath(nodeID, `/compose/${encodedName}?confirm=${encodedName}`), { method: 'DELETE' })
    void navigate({ to: '/compose' })
  }
  const run = async (name: string) => {
    if (name === 'down' && !await confirmDialog({ title: t('composeDown'), description: t('composeDownDescription', { name: projectName }), confirmLabel: 'Down', danger: true })) return
    action.mutate(name)
  }

  if (query.isPending) return <LoadingState label={zh ? '正在加载 Compose 项目' : 'Loading Compose project'} rows={6} />
  if (query.isError || !query.data) return <ErrorState title={zh ? '无法加载 Compose 项目' : 'Unable to load Compose project'} description={query.error?.message || (zh ? '服务端没有返回项目数据。' : 'The server did not return project data.')} />

  const project = query.data
  const dirty = compose !== project.compose || environment !== project.environment
  const statusClass = project.status === 'running' ? activeClass : project.status === 'degraded' ? warnClass : ''
  const headerActions = <div className="flex flex-wrap items-center gap-2">
    <Badge variant="outline" className={statusClass}>{project.status}</Badge>
    {['start', 'stop', 'restart', 'pull', 'build', 'down'].map((name) => <Button key={name} variant={name === 'down' ? 'destructive' : 'outline'} disabled={action.isPending} onClick={() => void run(name)}>{name}</Button>)}
    <Button disabled={action.isPending} onClick={() => void run('up')}>{action.isPending ? <Spinner className="size-4" /> : <Play size={16} />}Up</Button>
  </div>
  return <div className="flex w-full flex-col items-start gap-4">
    <Button variant="ghost" size="sm" className="-ml-2 text-muted-foreground" onClick={() => void navigate({ to: '/compose' })}><ChevronLeft />Compose</Button>
    <ResourceFrame title={projectName} detail={dirty ? (zh ? '本地管理 · 有未保存更改' : 'Locally managed · Unsaved changes') : (zh ? '本地管理 · 已保存' : 'Locally managed · Saved')} action={headerActions}>
      <div className="flex w-full flex-col items-start gap-3">
        {notice && <p className={`text-sm ${action.isError ? 'text-red-600 dark:text-red-400' : 'text-muted-foreground'}`}>{notice}</p>}
        <Tabs value={view} onValueChange={(value) => setView(value as View)}>
          <TabsList variant="line">
            {(['Files', 'Services', 'Logs'] as View[]).map((name) => <TabsTrigger key={name} value={name}>{name === 'Files' ? (zh ? 'Compose 文件' : 'Compose files') : name === 'Services' ? (zh ? '服务' : 'Services') : (zh ? '日志' : 'Logs')}</TabsTrigger>)}
          </TabsList>
        </Tabs>
        {view === 'Files' && <ComposeFiles dark={dark} file={file} compose={compose} environment={environment} dirty={dirty} notice={notice} zh={zh} setFile={setFile} setCompose={setCompose} setEnvironment={setEnvironment} onRemove={() => void remove()} onValidate={() => validate.mutate()} onSave={() => save.mutate()} onDeploy={() => void deploy()} validating={validate.isPending} saving={save.isPending} />}
        {view === 'Services' && <Services rows={services.data} loading={services.isPending} zh={zh} />}
        {view === 'Logs' && <Logs value={logs.data?.logs} loading={logs.isPending} error={logs.isError} zh={zh} />}
      </div>
    </ResourceFrame>
  </div>
}

function ComposeFiles({ dark, file, compose, environment, dirty, notice, zh, setFile, setCompose, setEnvironment, onRemove, onValidate, onSave, onDeploy, validating, saving }: { dark: boolean; file: string; compose: string; environment: string; dirty: boolean; notice: string; zh: boolean; setFile: (file: string) => void; setCompose: (value: string) => void; setEnvironment: (value: string) => void; onRemove: () => void; onValidate: () => void; onSave: () => void; onDeploy: () => void; validating: boolean; saving: boolean }) {
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
      <div className="flex flex-wrap items-center justify-between gap-2"><p className="text-sm text-muted-foreground">{notice || (dirty ? (zh ? '有未保存更改' : 'Unsaved changes') : (zh ? '没有更改' : 'No changes'))}</p><div className="flex items-center gap-2"><Button variant="destructive" onClick={onRemove}><Trash2 size={16} />{zh ? '删除' : 'Remove'}</Button><Button variant="outline" disabled={validating} onClick={onValidate}>{validating ? <Spinner className="size-4" /> : <FileCheck2 size={16} />}{zh ? '校验' : 'Validate'}</Button><Button variant="outline" disabled={!dirty || saving} onClick={onSave}>{saving ? <Spinner className="size-4" /> : <Save size={16} />}{zh ? '保存' : 'Save'}</Button><Button onClick={onDeploy}>{zh ? '保存并部署' : 'Save & deploy'}</Button></div></div>
    </CardContent>
  </Card>
}

function Services({ rows, loading, zh }: { rows?: ContainerSummary[]; loading: boolean; zh: boolean }) {
  if (loading) return <LoadingState compact label={zh ? '正在加载项目服务' : 'Loading project services'} />
  return <Table className="w-full">
    <TableHeader>
      <TableRow>
        <TableHead>{zh ? '服务' : 'Service'}</TableHead>
        <TableHead>{zh ? '镜像' : 'Image'}</TableHead>
        <TableHead>{zh ? '状态' : 'State'}</TableHead>
        <TableHead>{zh ? '详情' : 'Detail'}</TableHead>
      </TableRow>
    </TableHeader>
    <TableBody>
      {(rows ?? []).map((row) => (
        <TableRow key={row.id}>
          <TableCell className="font-medium">{row.labels['com.docker.compose.service'] || row.name}</TableCell>
          <TableCell><span title={row.image} className="block max-w-72 truncate text-muted-foreground">{row.image}</span></TableCell>
          <TableCell><Badge variant="outline" className={row.state === 'running' ? activeClass : ''}>{row.state}</Badge></TableCell>
          <TableCell className="text-muted-foreground">{row.status}</TableCell>
        </TableRow>
      ))}
    </TableBody>
  </Table>
}

function Logs({ value, loading, error, zh }: { value?: string; loading: boolean; error: boolean; zh: boolean }) {
  if (loading) return <LoadingState rows={6} label={zh ? '正在加载 Compose 日志' : 'Loading Compose logs'} />
  return <Card className="h-[55vh] w-full overflow-auto">
    <CardContent>
      <pre className="font-mono text-xs leading-relaxed whitespace-pre-wrap">{value || ''}</pre>
      {!value && <p className="text-sm text-muted-foreground">{error ? (zh ? '没有可用日志，请先启动项目。' : 'No Compose logs available. Start the project first.') : ''}</p>}
    </CardContent>
  </Card>
}
