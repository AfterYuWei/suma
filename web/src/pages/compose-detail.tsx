import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate, useParams } from '@tanstack/react-router'
import { ChevronLeft, FileCheck2, LoaderCircle, Play, Save, Trash2 } from 'lucide-react'
import { lazy, Suspense, useEffect, useState } from 'react'
import { LoadingState } from '../components/ui/loading-state'
import type { ComposeProject } from '../features/compose/types'
import type { ContainerSummary } from '../features/containers/types'
import { api } from '../lib/api'
import { nodePath } from '../lib/nodes'
import { useI18n } from '../lib/i18n'
import { confirmDialog, promptDialog } from '../stores/dialog'
import { useUIStore } from '../stores/ui'

const Monaco = lazy(() => import('@monaco-editor/react'))
type View = 'Files' | 'Services' | 'Logs'

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
  if (query.isError || !query.data) return <div className="border-y border-danger/25 bg-danger-subtle px-4 py-8 text-center"><p className="text-sm font-medium text-danger">{zh ? '无法加载 Compose 项目' : 'Unable to load Compose project'}</p><p className="mt-2 text-xs text-text-muted">{query.error?.message}</p></div>

  const project = query.data
  const dirty = compose !== project.compose || environment !== project.environment
  return <div>
    <Link to="/compose" className="mb-6 inline-flex items-center gap-1 text-xs text-text-muted hover:text-text"><ChevronLeft className="size-3.5" />Compose</Link>
    <header className="flex flex-wrap items-start gap-3 border-b border-border pb-6">
      <div className="min-w-0"><div className="flex items-center gap-2"><span className={`size-2 rounded-full ${project.status === 'running' ? 'bg-success' : project.status === 'degraded' ? 'bg-warning' : 'bg-text-subtle'}`} /><h1 className="truncate text-xl font-semibold">{projectName}</h1></div><p className="mt-1 text-xs text-text-muted">{dirty ? (zh ? '本地管理 · 有未保存更改' : 'Locally managed · Unsaved changes') : (zh ? '本地管理 · 已保存' : 'Locally managed · Saved')}</p>{notice && <p className={`mt-1.5 text-[10px] ${action.isError ? 'text-danger' : 'text-text-subtle'}`}>{notice}</p>}</div>
      <div className="ml-auto flex flex-wrap justify-end gap-1.5">{['start', 'stop', 'restart', 'pull', 'build', 'down'].map((name) => <button key={name} disabled={action.isPending} onClick={() => void run(name)} className={`h-8 rounded-md border px-3 text-xs capitalize disabled:opacity-50 ${name === 'down' ? 'border-danger/30 text-danger' : 'border-border bg-surface hover:bg-surface-hover'}`}>{name}</button>)}<button disabled={action.isPending} onClick={() => void run('up')} className="flex h-8 items-center gap-2 rounded-md bg-accent px-3 text-xs font-semibold text-accent-foreground disabled:opacity-50"><Play className="size-3.5" />Up</button></div>
    </header>
    <nav className="flex h-12 items-center gap-7 border-b border-border text-xs">{(['Files', 'Services', 'Logs'] as View[]).map((name) => <button key={name} onClick={() => setView(name)} className={`h-full ${view === name ? 'border-b border-accent font-medium text-text' : 'text-text-muted hover:text-text'}`}>{name === 'Files' ? (zh ? 'Compose 文件' : 'Compose files') : name === 'Services' ? (zh ? '服务' : 'Services') : (zh ? '日志' : 'Logs')}</button>)}</nav>
    {view === 'Files' && <ComposeFiles dark={dark} file={file} compose={compose} environment={environment} dirty={dirty} notice={notice} zh={zh} setFile={setFile} setCompose={setCompose} setEnvironment={setEnvironment} onRemove={() => void remove()} onValidate={() => validate.mutate()} onSave={() => save.mutate()} onDeploy={() => void deploy()} validating={validate.isPending} saving={save.isPending} />}
    {view === 'Services' && <Services rows={services.data} loading={services.isPending} zh={zh} />}
    {view === 'Logs' && <Logs value={logs.data?.logs} loading={logs.isPending} error={logs.isError} zh={zh} />}
  </div>
}

function ComposeFiles({ dark, file, compose, environment, dirty, notice, zh, setFile, setCompose, setEnvironment, onRemove, onValidate, onSave, onDeploy, validating, saving }: { dark: boolean; file: string; compose: string; environment: string; dirty: boolean; notice: string; zh: boolean; setFile: (file: string) => void; setCompose: (value: string) => void; setEnvironment: (value: string) => void; onRemove: () => void; onValidate: () => void; onSave: () => void; onDeploy: () => void; validating: boolean; saving: boolean }) {
  const files = [{ path: 'compose', label: 'compose.yml', content: compose, language: 'yaml' as const }, { path: 'environment', label: '.env', content: environment, language: 'plaintext' as const }]
  const selected = files.find((entry) => entry.path === file) || files[0]
  return <div className="py-6">
    <div className="overflow-hidden rounded-2xl border border-border">
      <div className="flex items-center gap-0.5 overflow-x-auto border-b border-border bg-surface px-2 pt-2">{files.map((entry) => <button key={entry.path} onClick={() => setFile(entry.path)} className={`h-9 shrink-0 px-3 text-xs ${selected.path === entry.path ? 'border-b border-accent' : 'text-text-muted'}`}>{entry.label}</button>)}</div>
      <div className="h-[52vh] overflow-hidden border-b border-border"><Suspense fallback={<div className="grid h-full place-items-center"><LoaderCircle className="size-5 animate-spin" /></div>}><Monaco key={selected.path} language={selected.language} theme={dark ? 'vs-dark' : 'light'} value={selected.content} onChange={(value) => selected.path === 'compose' ? setCompose(value ?? '') : setEnvironment(value ?? '')} options={{ minimap: { enabled: false }, fontSize: 12, lineHeight: 20, padding: { top: 14 }, scrollBeyondLastLine: false, automaticLayout: true, wordWrap: 'on' }} /></Suspense></div>
      <footer className="flex min-h-14 flex-wrap items-center gap-2 px-3 py-2"><span className="text-xs text-text-subtle">{notice || (dirty ? (zh ? '有未保存更改' : 'Unsaved changes') : (zh ? '没有更改' : 'No changes'))}</span><div className="ml-auto flex gap-2"><button onClick={onRemove} className="flex h-8 items-center gap-2 rounded-md border border-danger/30 bg-surface px-3 text-xs text-danger hover:bg-danger-subtle"><Trash2 className="size-3.5" />{zh ? '删除' : 'Remove'}</button><button disabled={validating} onClick={onValidate} className="flex h-8 items-center gap-2 rounded-md border border-border bg-surface px-3 text-xs disabled:opacity-50">{validating ? <LoaderCircle className="size-3.5 animate-spin" /> : <FileCheck2 className="size-3.5" />}{validating ? (zh ? '校验中…' : 'Validating…') : (zh ? '校验' : 'Validate')}</button><button disabled={!dirty || saving} onClick={onSave} className="flex h-8 items-center gap-2 rounded-md border border-border bg-surface px-3 text-xs disabled:opacity-40"><Save className="size-3.5" />{zh ? '保存' : 'Save'}</button><button onClick={onDeploy} className="h-8 rounded-md bg-accent px-3 text-xs font-semibold text-accent-foreground">{zh ? '保存并部署' : 'Save & deploy'}</button></div></footer>
    </div>
  </div>
}

function Services({ rows, loading, zh }: { rows?: ContainerSummary[]; loading: boolean; zh: boolean }) {
  if (loading) return <div className="py-6"><LoadingState compact label={zh ? '正在加载项目服务' : 'Loading project services'} /></div>
  return <div className="my-6 divide-y divide-border border-y border-border">{rows?.map((row) => <div key={row.id} className="grid min-h-14 gap-2 px-2 py-2 sm:grid-cols-[minmax(0,1fr)_120px_160px] sm:items-center sm:gap-4 sm:py-0"><div className="min-w-0"><p className="truncate text-sm font-medium">{row.labels['com.docker.compose.service'] || row.name}</p><p className="truncate text-[10px] text-text-subtle">{row.image}</p></div><p className="text-xs capitalize text-text-muted">{row.state}</p><p className="truncate text-xs text-text-subtle">{row.status}</p></div>)}{rows?.length === 0 && <p className="py-12 text-center text-xs text-text-subtle">{zh ? '当前没有运行中的项目服务。' : 'No project services are running.'}</p>}</div>
}

function Logs({ value, loading, error, zh }: { value?: string; loading: boolean; error: boolean; zh: boolean }) {
  if (loading) return <div className="py-6"><LoadingState rows={6} label={zh ? '正在加载 Compose 日志' : 'Loading Compose logs'} /></div>
  return <pre className="my-6 h-[55vh] overflow-auto rounded-md border border-border bg-[var(--code-background)] p-4 font-mono text-[11px] leading-5 text-text-muted">{value || (error ? (zh ? '没有可用日志，请先启动项目。' : 'No Compose logs available. Start the project first.') : '')}</pre>
}
