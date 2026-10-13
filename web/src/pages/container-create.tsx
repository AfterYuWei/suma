import { useEffect, useRef, useState, type ReactNode } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useBlocker } from '@tanstack/react-router'
import { ArrowLeft, Check, Eye, EyeOff, Plus, Trash2 } from 'lucide-react'
import { Button } from '../components/ui/button'
import { Input } from '../components/ui/input'
import { Label } from '../components/ui/label'
import { Textarea } from '../components/ui/textarea'
import { Checkbox } from '../components/ui/checkbox'
import { Alert, AlertDescription } from '../components/ui/alert'
import { Progress } from '../components/ui/progress'
import { Spinner } from '../components/ui/spinner'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../components/ui/select'
import { Tabs, TabsList, TabsTrigger } from '../components/ui/tabs'
import { containerCreateRequest, emptyContainerDraft, mountsDockerSocket, type ContainerCreateDraft } from '../features/containers/create'
import { dockerSocketHeaders, confirmDockerSocketMount } from '../features/compose/docker-socket-confirmation'
import { api, demoMode } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { nodePath, type DockerNode } from '../lib/nodes'
import { registerProjectNavigationGuard } from '../lib/project-navigation-guard'
import { confirmDialog } from '../stores/dialog'
import { useContainerViewStore, defaultContainerView } from '../stores/container-view'
import { useUIStore } from '../stores/ui'
import { ResourceFrame } from './images'

interface CreationTask { id: string; status: string; progress: number; message: string }
interface TaskStep { id: string; status: string }
interface Image { id: string; tags: string[] }
interface Credential { id: number; name: string; server_address: string; authorized_node_ids?: string[] }

export function ContainerCreatePage() {
  const nodeID = useUIStore(state => state.currentNodeID)
  return <ContainerCreateForm key={nodeID} nodeID={nodeID} />
}

function ContainerCreateForm({ nodeID }: { nodeID: string }) {
  const { language } = useI18n(), zh = language === 'zh-CN'
  const tr = (cn: string, en: string) => zh ? cn : en
  const client = useQueryClient()
  const [draft, setDraft] = useState(emptyContainerDraft)
  const [tab, setTab] = useState('basic'), [notice, setNotice] = useState(''), [taskID, setTaskID] = useState('')
  const [reviewing, setReviewing] = useState(false)
  const [revealed, setRevealed] = useState<Set<number>>(new Set())
  const update = <K extends keyof ContainerCreateDraft>(key: K, value: ContainerCreateDraft[K]) => { setDraft(old => ({ ...old, [key]: value })); setNotice('') }
  const dirty = !taskID && JSON.stringify(draft) !== JSON.stringify(emptyContainerDraft()), dirtyRef = useRef(dirty)
  dirtyRef.current = dirty
  const discard = () => confirmDialog({ title: tr('放弃未保存的容器配置？', 'Discard unsaved container configuration?'), danger: true })
  useBlocker({ disabled: !dirty, enableBeforeUnload: dirty, shouldBlockFn: async () => !await discard() })
  useEffect(() => registerProjectNavigationGuard(async () => !dirtyRef.current || await confirmDialog({ title: zh ? '放弃容器配置并切换节点？' : 'Discard container configuration and change node?', danger: true })), [zh])

  const nodes = useQuery({ queryKey: ['nodes'], queryFn: ({ signal }) => api<DockerNode[]>('/nodes', { signal }) })
  const node = nodes.data?.find(node => node.id === nodeID)
  const images = useQuery({ queryKey: ['images', nodeID], queryFn: ({ signal }) => api<Image[]>(nodePath(nodeID, '/images'), { signal }) })
  const networks = useQuery({ queryKey: ['networks', nodeID], queryFn: ({ signal }) => api<{ name: string }[]>(nodePath(nodeID, '/networks'), { signal }) })
  const volumes = useQuery({ queryKey: ['volumes', nodeID], queryFn: ({ signal }) => api<{ name: string }[]>(nodePath(nodeID, '/volumes'), { signal }) })
  const credentials = useQuery({ queryKey: ['registry-credentials'], queryFn: ({ signal }) => api<Credential[]>('/credentials/registries', { signal }) })
  const taskKey = ['container-create-task', nodeID, taskID]
  const create = useMutation({ mutationFn: (payload: { input: ReturnType<typeof containerCreateRequest>; socket: boolean }) => api<CreationTask>(nodePath(nodeID, '/containers'), { method: 'POST', headers: dockerSocketHeaders(payload.socket), body: JSON.stringify(payload.input) }), onSuccess: task => { client.setQueryData(['container-create-task', nodeID, task.id], task); setTaskID(task.id); void client.invalidateQueries({ queryKey: ['tasks'] }) }, onError: error => setNotice(error.message) })
  const task = useQuery({ queryKey: taskKey, queryFn: ({ signal }) => api<CreationTask>(nodePath(nodeID, `/tasks/${encodeURIComponent(taskID)}`), { signal }), enabled: !!taskID, refetchInterval: query => query.state.data && ['success', 'failed', 'canceled'].includes(query.state.data.status) ? false : 1000 })
  const running = !!taskID && (!task.data || ['pending', 'running'].includes(task.data.status))
  const taskStatus = task.data?.status
  const steps = useQuery({ queryKey: ['container-create-steps', nodeID, taskID], queryFn: ({ signal }) => api<TaskStep[]>(nodePath(nodeID, `/tasks/${encodeURIComponent(taskID)}/steps`), { signal }), enabled: !!taskID, refetchInterval: running ? 1000 : false })
  const containerID = steps.data?.find(step => /^container:[a-f0-9]{64}$/.test(step.id))?.id.slice('container:'.length)
  const cancel = useMutation({ mutationFn: () => api(nodePath(nodeID, `/tasks/${taskID}/cancel`), { method: 'POST' }), onSuccess: () => void task.refetch() })
  useEffect(() => {
    if (!running || demoMode) return
    const socket = new WebSocket(`${location.protocol === 'https:' ? 'wss:' : 'ws:'}//${location.host}/ws/nodes/${encodeURIComponent(nodeID)}/tasks/${encodeURIComponent(taskID)}`)
    socket.onmessage = () => { void client.invalidateQueries({ queryKey: ['container-create-task', nodeID, taskID] }); void client.invalidateQueries({ queryKey: ['container-create-steps', nodeID, taskID] }) }
    return () => { socket.onmessage = null; socket.close() }
  }, [running, nodeID, taskID, client])
  useEffect(() => {
    if (taskStatus && ['success', 'failed', 'canceled'].includes(taskStatus)) {
      for (const key of ['containers', 'container-runtime', 'container-metrics', 'images', 'networks', 'volumes', 'tasks']) void client.invalidateQueries({ queryKey: [key] })
      void client.invalidateQueries({ queryKey: ['container-create-steps', nodeID, taskID] })
      if (taskStatus === 'success') {
        setDraft(emptyContainerDraft()); setRevealed(new Set())
        const store = useContainerViewStore.getState(), view = store.views[nodeID] ?? defaultContainerView
        store.update(nodeID, { ...defaultContainerView, sort: view.sort, descending: view.descending })
      }
    }
  }, [taskStatus, client, nodeID, taskID])
  const submit = async () => {
    if (reviewing || create.isPending) return
    setReviewing(true)
    setNotice('')
    try {
      const input = containerCreateRequest(draft, zh)
      const socket = mountsDockerSocket(input.mounts) ? await confirmDockerSocketMount('/var/run/docker.sock', zh) : false
      if (socket === null) return
      create.mutate({ input, socket })
    } catch (error) { setNotice(error instanceof Error ? error.message : String(error)) } finally { setReviewing(false) }
  }
  const text = (key: 'name' | 'image' | 'working_directory' | 'user' | 'memory_mb' | 'cpus') => ({ value: draft[key], onChange: (event: React.ChangeEvent<HTMLInputElement>) => update(key, event.target.value) })
  const authorizedCredentials = (credentials.data ?? []).filter(credential => credential.authorized_node_ids?.includes(nodeID))
  const networkNames = [...new Set(['bridge', 'host', 'none', ...(networks.data ?? []).map(network => network.name)])]
  const back = <Button variant="ghost" size="sm" nativeButton={false} render={<Link to="/containers" />}><ArrowLeft />{tr('返回容器', 'Back to containers')}</Button>

  return <ResourceFrame title={tr('新增容器', 'New container')} detail={tr('创建到节点：', 'Create on node: ') + (node?.name ?? nodeID)} action={back}>
    <div className="max-w-4xl space-y-4">
      {taskID ? <section aria-label={tr('创建进度', 'Creation progress')} className="space-y-4 rounded-lg bg-surface p-4">
        <div className="flex items-center gap-2 text-sm font-medium">{running ? <Spinner /> : task.data?.status === 'success' ? <Check className="size-4 text-emerald-500" /> : null}{running ? tr('正在创建容器', 'Creating container') : task.data?.status === 'success' ? tr('容器创建完成', 'Container created') : task.data?.status === 'canceled' ? tr('创建已取消', 'Creation canceled') : tr('创建失败', 'Creation failed')}</div>
        <Progress value={task.data?.progress ?? 0} />
        <p role="status" className="break-words text-sm">{task.data?.message || tr('等待任务进度…', 'Waiting for task progress…')}</p>
        {(task.isError || steps.isError || cancel.isError) && <Alert variant="destructive"><AlertDescription>{task.error?.message || steps.error?.message || cancel.error?.message}</AlertDescription></Alert>}
        {containerID && task.data?.status !== 'success' && <p className="text-sm text-muted-foreground">{tr('容器已经创建并保留，请先检查容器；再次创建同名容器会发生冲突。', 'The created container was retained. Inspect it before creating another container with the same name.')}</p>}
        <div className="flex flex-wrap gap-2">
          {running && <Button variant="outline" disabled={cancel.isPending} onClick={() => cancel.mutate()}>{tr('取消任务', 'Cancel task')}</Button>}
          {containerID && <Button nativeButton={false} render={<Link to="/containers/$containerId" params={{ containerId: containerID }} />}>{tr('查看容器', 'View container')}</Button>}
          <Button variant="outline" nativeButton={false} render={<Link to="/tasks" hash={taskID} />}>{tr('查看任务', 'View task')}</Button>
          {!running && task.data?.status !== 'success' && !containerID && !steps.isPending && !steps.isError && <Button variant="outline" onClick={() => { setTaskID(''); create.reset() }}>{tr('修改配置并重试', 'Edit and retry')}</Button>}
        </div>
      </section> : <form onSubmit={event => { event.preventDefault(); if (!create.isPending) void submit() }} className="space-y-4">
        <Tabs value={tab} onValueChange={setTab}><TabsList><TabsTrigger value="basic">{tr('基础配置', 'Basics')}</TabsTrigger><TabsTrigger value="network">{tr('网络与存储', 'Network & storage')}</TabsTrigger><TabsTrigger value="runtime">{tr('运行配置', 'Runtime')}</TabsTrigger></TabsList></Tabs>
        <fieldset disabled={create.isPending || reviewing} className="min-w-0 space-y-4">
          {tab === 'basic' && <Section title={tr('容器与镜像', 'Container and image')}>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label={tr('容器名称', 'Container name')}><Input aria-label={tr('容器名称', 'Container name')} required maxLength={128} autoComplete="off" placeholder="my-app" {...text('name')} /></Field>
              <Field label={tr('镜像', 'Image')}><Input aria-label={tr('镜像', 'Image')} required list="container-image-options" placeholder="nginx:alpine" {...text('image')} /><datalist id="container-image-options">{[...new Set((images.data ?? []).flatMap(image => image.tags?.filter(tag => tag !== '<none>:<none>') ?? []))].map(tag => <option key={tag} value={tag} />)}</datalist></Field>
              <Choice label={tr('镜像拉取策略', 'Image pull policy')} value={draft.pull_policy} options={[['missing', tr('本地缺失时拉取', 'Pull if missing')], ['always', tr('始终拉取最新镜像', 'Always pull')], ['never', tr('仅使用本地镜像', 'Local image only')]]} onChange={value => update('pull_policy', value as ContainerCreateDraft['pull_policy'])} />
              <Choice label={tr('镜像仓库凭据', 'Registry credential')} value={String(draft.credential_id ?? '')} options={[[ '', tr('公开镜像，无凭据', 'Public image, no credential')], ...authorizedCredentials.map(credential => [String(credential.id), credential.name + ' · ' + credential.server_address] as [string, string])]} onChange={value => update('credential_id', value ? Number(value) : undefined)} />
            </div>
            {images.isError && <p className="text-xs text-muted-foreground">{tr('本地镜像列表不可用，仍可手动输入镜像引用。', 'Local images are unavailable; you can still enter an image reference.')}</p>}
            <Toggle label={tr('创建后立即启动', 'Start after creation')} checked={draft.start} onChange={value => update('start', value)} />
          </Section>}
          {tab === 'network' && <>
            <Section title={tr('网络与端口', 'Network and ports')}>
              <Choice label={tr('网络', 'Network')} value={draft.network} options={networkNames.map(name => [name, name])} onChange={value => update('network', value)} />
              {networks.isError && <p className="text-xs text-muted-foreground">{tr('无法加载自定义网络，请重试页面。', 'Custom networks could not load; retry the page.')}</p>}
              <p className="text-xs text-muted-foreground">{tr('默认仅绑定 127.0.0.1；需要局域网或公网访问时，可改为具体 IP 或 0.0.0.0。宿主机端口为 0 时自动分配。', 'Bindings default to 127.0.0.1. Use a specific IP or 0.0.0.0 for external access. Host port 0 assigns an available port.')}</p>
              {draft.ports.map((port, index) => <div key={index} className="flex flex-wrap items-end gap-2 rounded-lg bg-muted/30 p-3">
                <Field label={tr('宿主机 IP', 'Host IP')} className="w-36"><Input aria-label={`${tr('宿主机 IP', 'Host IP')} ${index + 1}`} value={port.host_ip} onChange={event => update('ports', draft.ports.map((old, i) => i === index ? { ...old, host_ip: event.target.value } : old))} /></Field>
                {(['host_port', 'container_port'] as const).map(key => <Field key={key} className="w-28" label={key === 'host_port' ? tr('宿主机端口', 'Host port') : tr('容器端口', 'Container port')}><Input type="number" required min={key === 'host_port' ? 0 : 1} max={65535} aria-label={`${key === 'host_port' ? tr('宿主机端口', 'Host port') : tr('容器端口', 'Container port')} ${index + 1}`} value={port[key]} onChange={event => update('ports', draft.ports.map((old, i) => i === index ? { ...old, [key]: Number(event.target.value) } : old))} /></Field>)}
                <Choice label={tr('协议', 'Protocol')} value={port.protocol} options={[[ 'tcp', 'TCP'], ['udp', 'UDP']]} onChange={value => update('ports', draft.ports.map((old, i) => i === index ? { ...old, protocol: value as 'tcp' | 'udp' } : old))} />
                <Remove label={`${tr('移除端口', 'Remove port')} ${index + 1}`} onClick={() => update('ports', draft.ports.filter((_, i) => i !== index))} />
              </div>)}
              <Button type="button" size="sm" variant="outline" disabled={['host', 'none'].includes(draft.network)} onClick={() => update('ports', [...draft.ports, { host_ip: '127.0.0.1', host_port: 0, container_port: 80, protocol: 'tcp' }])}><Plus />{tr('添加端口', 'Add port')}</Button>
            </Section>
            <Section title={tr('存储挂载', 'Storage mounts')}>
              <p className="text-xs text-muted-foreground">{tr('绑定挂载路径属于所选 Docker 节点，必须使用绝对路径；不存在的具名存储卷会由 Docker 创建。', 'Bind paths belong to the selected Docker node and must be absolute. Docker creates named volumes that do not exist.')}</p>
              <datalist id="container-volume-options">{volumes.data?.map(volume => <option key={volume.name} value={volume.name} />)}</datalist>
              {draft.mounts.map((mount, index) => <div key={index} className="space-y-3 rounded-lg bg-muted/30 p-3">
                <div className="flex flex-wrap items-end gap-2">
                  <Choice label={tr('挂载类型', 'Mount type')} value={mount.type} options={[[ 'volume', tr('具名存储卷', 'Named volume')], ['bind', tr('节点路径', 'Node path')]]} onChange={value => update('mounts', draft.mounts.map((old, i) => i === index ? { ...old, type: value as 'bind' | 'volume', source: '' } : old))} />
                  <Field className="min-w-40 flex-1" label={tr('来源', 'Source')}><Input required aria-label={`${tr('挂载来源', 'Mount source')} ${index + 1}`} list={mount.type === 'volume' ? 'container-volume-options' : undefined} placeholder={mount.type === 'volume' ? 'app-data' : '/srv/app/data'} value={mount.source} onChange={event => update('mounts', draft.mounts.map((old, i) => i === index ? { ...old, source: event.target.value } : old))} /></Field>
                  <Field className="min-w-40 flex-1" label={tr('容器路径', 'Container path')}><Input required aria-label={`${tr('挂载目标', 'Mount target')} ${index + 1}`} placeholder="/data" value={mount.target} onChange={event => update('mounts', draft.mounts.map((old, i) => i === index ? { ...old, target: event.target.value } : old))} /></Field>
                  <Remove label={`${tr('移除挂载', 'Remove mount')} ${index + 1}`} onClick={() => update('mounts', draft.mounts.filter((_, i) => i !== index))} />
                </div>
                <Toggle label={tr('只读挂载', 'Read-only mount')} checked={mount.read_only} onChange={value => update('mounts', draft.mounts.map((old, i) => i === index ? { ...old, read_only: value } : old))} />
              </div>)}
              <Button type="button" size="sm" variant="outline" onClick={() => update('mounts', [...draft.mounts, { type: 'volume', source: '', target: '', read_only: false }])}><Plus />{tr('添加挂载', 'Add mount')}</Button>
            </Section>
          </>}
          {tab === 'runtime' && <>
            <Section title={tr('环境变量', 'Environment variables')}>
              {draft.environment.map((variable, index) => <div key={index} className="flex flex-wrap items-end gap-2">
                <Field className="min-w-36 flex-1" label={tr('变量名', 'Variable name')}><Input required aria-label={`${tr('变量名', 'Variable name')} ${index + 1}`} placeholder="APP_ENV" autoComplete="off" value={variable.key} onChange={event => update('environment', draft.environment.map((old, i) => i === index ? { ...old, key: event.target.value } : old))} /></Field>
                <Field className="min-w-36 flex-1" label={tr('变量值', 'Variable value')}><Input aria-label={`${tr('变量值', 'Variable value')} ${index + 1}`} type={revealed.has(index) ? 'text' : 'password'} autoComplete="new-password" value={variable.value} onChange={event => update('environment', draft.environment.map((old, i) => i === index ? { ...old, value: event.target.value } : old))} /></Field>
                <Button type="button" size="icon-sm" variant="ghost" aria-label={`${tr('切换变量值显示', 'Toggle variable visibility')} ${index + 1}`} onClick={() => setRevealed(old => { const next = new Set(old); if (next.has(index)) next.delete(index); else next.add(index); return next })}>{revealed.has(index) ? <EyeOff /> : <Eye />}</Button>
                <Remove label={`${tr('移除变量', 'Remove variable')} ${index + 1}`} onClick={() => { update('environment', draft.environment.filter((_, i) => i !== index)); setRevealed(new Set()) }} />
              </div>)}
              <Button type="button" size="sm" variant="outline" onClick={() => update('environment', [...draft.environment, { key: '', value: '' }])}><Plus />{tr('添加环境变量', 'Add variable')}</Button>
            </Section>
            <Section title={tr('启动与资源', 'Startup and resources')}>
              <div className="grid gap-4 sm:grid-cols-2">
                <Choice label={tr('重启策略', 'Restart policy')} value={draft.restart_policy} options={[[ 'no', tr('不自动重启', 'Do not restart')], ['unless-stopped', tr('除非手动停止', 'Unless stopped')], ['always', tr('始终重启', 'Always')], ['on-failure', tr('失败时重启', 'On failure')]]} onChange={value => update('restart_policy', value)} />
                <Field label={tr('运行用户', 'Run as user')}><Input aria-label={tr('运行用户', 'Run as user')} placeholder="1000:1000" {...text('user')} /></Field>
                <Field label={tr('工作目录', 'Working directory')}><Input aria-label={tr('工作目录', 'Working directory')} placeholder="/app" {...text('working_directory')} /></Field>
                <Field label={tr('内存上限（MiB）', 'Memory limit (MiB)')}><Input type="number" min={6} aria-label={tr('内存上限（MiB）', 'Memory limit (MiB)')} placeholder={tr('不限制', 'Unlimited')} {...text('memory_mb')} /></Field>
                <Field label={tr('CPU 上限（核）', 'CPU limit (cores)')}><Input type="number" min={0} max={1024} step="0.01" aria-label={tr('CPU 上限（核）', 'CPU limit (cores)')} placeholder={tr('不限制', 'Unlimited')} {...text('cpus')} /></Field>
              </div>
              <details className="rounded-lg bg-muted/30 p-3"><summary className="cursor-pointer text-sm">{tr('命令与高级配置', 'Command and advanced options')}</summary><div className="mt-4 space-y-4">
                <p className="text-xs text-muted-foreground">{tr('每行一个参数；留空沿用镜像默认值。例如：第一行 sh，第二行 -c，第三行 sleep 3600。', 'One argument per line; leave blank for image defaults. For example: sh, -c, sleep 3600 on three lines.')}</p>
                <div className="grid gap-4 sm:grid-cols-2">{(['command', 'entrypoint'] as const).map(key => <Field key={key} label={key === 'command' ? tr('命令参数', 'Command arguments') : tr('入口参数', 'Entrypoint arguments')}><Textarea aria-label={key === 'command' ? tr('命令参数', 'Command arguments') : tr('入口参数', 'Entrypoint arguments')} className="font-mono text-sm" value={draft[key]} onChange={event => update(key, event.target.value)} /></Field>)}</div>
                <div className="flex flex-wrap gap-4"><Toggle label={tr('只读根文件系统', 'Read-only root filesystem')} checked={draft.read_only_rootfs} onChange={value => update('read_only_rootfs', value)} /><Toggle label="TTY" checked={draft.tty} onChange={value => update('tty', value)} /><Toggle label={tr('保持标准输入打开', 'Keep stdin open')} checked={draft.open_stdin} onChange={value => update('open_stdin', value)} /></div>
              </div></details>
            </Section>
          </>}
        </fieldset>
        {notice && <Alert variant="destructive"><AlertDescription>{notice}</AlertDescription></Alert>}
        <div className="flex flex-wrap items-center gap-3"><Button type="submit" disabled={create.isPending || reviewing || !draft.name.trim() || !draft.image.trim() || node?.enabled === false || node?.status === 'offline'}>{create.isPending ? <Spinner /> : <Plus />}{draft.start ? tr('创建并启动', 'Create and start') : tr('创建容器', 'Create container')}</Button><span className="text-xs text-muted-foreground">{tr('配置仅应用于当前节点。', 'Configuration applies to this node.')}</span></div>
      </form>}
    </div>
  </ResourceFrame>
}

function Section({ title, children }: { title: string; children: ReactNode }) { return <section className="space-y-4 rounded-lg bg-surface p-4"><h3 className="text-sm font-medium">{title}</h3>{children}</section> }
function Field({ label, className = '', children }: { label: string; className?: string; children: ReactNode }) { return <div className={`min-w-0 space-y-1.5 ${className}`}><Label>{label}</Label>{children}</div> }
function Toggle({ label, checked, onChange }: { label: string; checked: boolean; onChange: (checked: boolean) => void }) { return <label className="flex items-center gap-2 text-sm"><Checkbox checked={checked} onCheckedChange={value => onChange(value === true)} />{label}</label> }
function Remove({ label, onClick }: { label: string; onClick: () => void }) { return <Button type="button" size="icon-sm" variant="ghost" aria-label={label} onClick={onClick}><Trash2 /></Button> }
function Choice({ label, value, options, onChange }: { label: string; value: string; options: [string, string][]; onChange: (value: string) => void }) {
  const items = options.map(([value, label]) => ({ value, label }))
  return <Field label={label}><Select items={items} value={value} onValueChange={value => value != null && onChange(String(value))}><SelectTrigger className="w-full min-w-28" aria-label={label}><SelectValue /></SelectTrigger><SelectContent>{items.map(item => <SelectItem key={item.value} value={item.value}>{item.label}</SelectItem>)}</SelectContent></Select></Field>
}
