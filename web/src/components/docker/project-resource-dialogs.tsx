import { useEffect, useId, useRef, useState } from 'react'
import { ArrowRight, Trash2 } from 'lucide-react'
import { useQuery } from '@tanstack/react-query'
import { api } from '../../lib/api'
import { nodePath } from '../../lib/nodes'
import {
  configKeys,
  configValue,
  configureServiceMount,
  connectServiceNetwork,
  editConfig,
  REMOVE,
  type ProjectResourceChoice
} from '../../features/compose/document'
import { Alert, AlertDescription } from '../ui/alert'
import { Button } from '../ui/button'
import { Checkbox } from '../ui/checkbox'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '../ui/dialog'
import { Input } from '../ui/input'
import { Label } from '../ui/label'
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Spinner } from '../ui/spinner'
import { Tabs, TabsList, TabsTrigger } from '../ui/tabs'

interface ResourceContext {
  compose: string
  nodeID: string
  service: string
  displayService?: string
  zh: boolean
  disabled?: boolean
  onCompose: (source: string) => void
  pending?: (key: string, pending: boolean) => void
}
interface NodeResource {
  name: string
  driver: string
  scope?: string
  containers?: number
  used_by?: string[]
}

function errorText(error: unknown, zh: boolean) {
  const message = error instanceof Error ? error.message : String(error)
  if (!zh) return message
  const messages: Record<string, string> = {
    'Select a declared project resource': '请选择已配置的网络或卷。',
    'Invalid or duplicate resource name': '名称无效、已存在或为保留名称，请使用其他名称。',
    'Select an existing node resource': '请选择当前节点已有的资源。',
    'Container path must be absolute': '容器路径必须是绝对路径。',
    'Container path already has a mount': '该容器路径已经配置挂载，请编辑原挂载。',
    'Mount no longer exists': '挂载配置已发生变化，请关闭表单后重试。'
  }
  return messages[message] ?? message
}

function ResourceFields({ context: c, section, value, onChange, anonymous = false, preservedName }: {
  context: ResourceContext
  section: 'networks' | 'volumes'
  value: ProjectResourceChoice
  onChange: (choice: ProjectResourceChoice) => void
  anonymous?: boolean
  preservedName?: string
}) {
  const id = useId()
  const query = useQuery({
    queryKey: [section, c.nodeID],
    queryFn: () => api<NodeResource[]>(nodePath(c.nodeID, `/${section}`)),
    enabled: value.kind === 'existing',
    staleTime: 15_000
  })
  const keys = configKeys(c.compose, [section]).filter(key => section !== 'networks' || key !== 'default')
  if (preservedName && !keys.includes(preservedName)) keys.push(preservedName)
  const eligible = (query.data ?? []).filter(row => section !== 'networks' ||
    (!['bridge', 'host', 'none'].includes(row.name) && !['host', 'null'].includes(row.driver) && row.scope !== 'swarm'))
  // Compose resolves external resources by name. Ambiguous Docker network names
  // cannot be selected safely through a name-only Compose declaration.
  const available = eligible.filter(row => eligible.filter(other => other.name === row.name).length === 1)
  const rows: [string, string][] = value.kind === 'existing'
    ? available.map(row => [row.name, `${row.name} · ${row.driver}`])
    : keys.map(key => {
        const definition = configValue(c.compose, [section, key]) as Record<string, unknown> | undefined
        return [key, definition?.name ? `${key} → ${definition.name}` : key]
      })
  if (anonymous && value.kind === 'project') rows.unshift(['__anonymous', c.zh ? '保留匿名卷' : 'Keep anonymous volume'])
  const selected = available.find(row => row.name === value.name)
  const changeKind = (kind: string) => onChange({ kind: kind as ProjectResourceChoice['kind'], name: '' })
  return <div className="space-y-3">
    <Tabs value={value.kind} onValueChange={changeKind}>
      <TabsList className="group-data-horizontal/tabs:h-auto w-full flex-wrap">
        {keys.length > 0 && <TabsTrigger value="project" className="h-8">{c.zh ? '已配置' : 'Configured'}</TabsTrigger>}
        <TabsTrigger value="existing" className="h-8">{c.zh ? '节点已有' : 'Existing on node'}</TabsTrigger>
        <TabsTrigger value="new" className="h-8">{c.zh ? '创建网络' : 'Create network'}</TabsTrigger>
      </TabsList>
    </Tabs>
    {value.kind === 'new' ? <div className="flex flex-col gap-1.5">
      <Label htmlFor={id}>{section === 'networks' ? (c.zh ? '网络名称' : 'Network name') : (c.zh ? '卷名称' : 'Volume name')}</Label>
      <Input id={id} value={value.name} onChange={event => onChange({ ...value, name: event.target.value })} placeholder={section === 'networks' ? 'backend' : 'app_data'} autoComplete="off" />
      <p className="text-xs text-muted-foreground">{c.zh ? '保存配置后，在部署时创建或复用网络。' : 'The network is created or reused when you deploy this configuration.'}</p>
    </div> : <div className="flex flex-col gap-1.5">
      <Label htmlFor={id}>{value.kind === 'existing' ? (c.zh ? `节点 ${c.nodeID} 上的${section === 'networks' ? '网络' : '卷'}` : `${section === 'networks' ? 'Network' : 'Volume'} on node ${c.nodeID}`) : (c.zh ? '已配置网络' : 'Configured network')}</Label>
      <Select items={[['__select', c.zh ? '请选择资源' : 'Select a resource'], ...rows].map(([value, label]) => ({ value, label }))} value={value.name || '__select'} onValueChange={name => onChange({ ...value, name: String(name) === '__select' ? '' : String(name) })}>
        <SelectTrigger id={id} className="w-full"><SelectValue /></SelectTrigger>
        <SelectContent align="start" alignItemWithTrigger={false} className="min-w-0"><SelectGroup><SelectItem value="__select">{c.zh ? '请选择资源' : 'Select a resource'}</SelectItem>{rows.map(([name, label]) => <SelectItem key={name} value={name}>{label}</SelectItem>)}</SelectGroup></SelectContent>
      </Select>
      {value.kind === 'existing' && query.isPending ? <p className="flex items-center gap-2 text-xs text-muted-foreground"><Spinner />{c.zh ? '正在读取当前节点资源' : 'Loading node resources'}</p> : !rows.length && <p className="text-xs text-muted-foreground">{c.zh ? '没有可选资源，可切换到创建网络。' : 'No resources available. You can create a network on deployment.'}</p>}
      {value.kind === 'existing' && query.isError && <Alert variant="destructive"><AlertDescription>{query.error.message}<Button type="button" size="sm" variant="link" onClick={() => void query.refetch()}>{c.zh ? '重试' : 'Retry'}</Button></AlertDescription></Alert>}
      {value.kind === 'existing' && selected && <p className="text-xs text-muted-foreground">{c.zh ? '使用目标节点已有的资源。' : 'Use the existing resource on the target node.'}{section === 'volumes' && selected.used_by?.length ? ` ${c.zh ? '使用容器' : 'Used by'}: ${selected.used_by.join(', ')}` : ''}</p>}
      {value.kind === 'existing' && eligible.length !== available.length && <p className="text-xs text-muted-foreground">{c.zh ? '同名网络无法可靠引用，已从列表中隐藏。' : 'Resources with ambiguous names are omitted.'}</p>}
    </div>}
  </div>
}

export function AddNetworkDialog({ context: c, onClose }: { context: ResourceContext; onClose: () => void }) {
  const raw = configValue(c.compose, ['services', c.service, 'networks'])
  const names = Array.isArray(raw) ? raw.map(String) : Object.keys((raw as Record<string, unknown>) ?? {})
  const [choice, setChoice] = useState<ProjectResourceChoice>({ kind: 'existing', name: '' })
  const [keepDefault, setKeepDefault] = useState(!names.length || names.includes('default'))
  const [error, setError] = useState('')
  return <Dialog open onOpenChange={open => { if (!open) onClose() }}>
    <DialogContent className="sm:max-w-lg">
      <DialogHeader><DialogTitle>{c.zh ? '添加服务网络' : 'Add service network'}</DialogTitle><DialogDescription>{c.displayService ?? c.service} · {c.zh ? '目标节点' : 'Target node'} {c.nodeID}</DialogDescription></DialogHeader>
      <form className="space-y-4" onSubmit={event => {
        event.preventDefault()
        if (c.disabled) return
        try {
          c.onCompose(connectServiceNetwork(c.compose, c.service, choice, keepDefault))
          onClose()
        } catch (failure) { setError(errorText(failure, c.zh)) }
      }}>
        <ResourceFields context={c} section="networks" value={choice} onChange={next => { setChoice(next); setError('') }} />
        <Label className="flex items-start gap-2"><Checkbox checked={keepDefault} onCheckedChange={checked => setKeepDefault(!!checked)} />{c.zh ? '同时保留默认网络连接' : 'Also keep the default network'}</Label>
        {error && <Alert variant="destructive"><AlertDescription>{error}</AlertDescription></Alert>}
        <DialogFooter><Button type="button" variant="outline" onClick={onClose}>{c.zh ? '取消' : 'Cancel'}</Button><Button type="submit" disabled={c.disabled}>{c.zh ? '连接到服务' : 'Connect to service'}</Button></DialogFooter>
      </form>
    </DialogContent>
  </Dialog>
}

function mountResourceChoice(value?: Record<string, unknown>): ProjectResourceChoice { return value?.type === 'volume'
    ? { kind: 'project', name: value.source ? String(value.source) : '__anonymous' }
    : { kind: 'new', name: '' } }
export function MountForm({ context: c, mount, index, onClose }: { context: ResourceContext; mount?: Record<string, unknown>; index?: number; onClose?: () => void }) {
  const { pending, service } = c
  const id = useId(), targetInput = useRef<HTMLInputElement>(null)
  const signature = JSON.stringify(mount)
  const [type, setType] = useState(String(mount?.type ?? 'bind'))
  const [choice, setChoice] = useState<ProjectResourceChoice>(mountResourceChoice(mount))
  const [source, setSource] = useState(String(mount?.source ?? ''))
  const [target, setTarget] = useState(String(mount?.target ?? ''))
  const [readOnly, setReadOnly] = useState(!!mount?.read_only), [error, setError] = useState('')
  const reset = (value = mount) => { setType(String(value?.type ?? 'bind')); setChoice(mountResourceChoice(value)); setSource(String(value?.source ?? '')); setTarget(String(value?.target ?? '')); setReadOnly(!!value?.read_only); setError('') }
  useEffect(() => {
    const value = signature ? JSON.parse(signature) as Record<string, unknown> : undefined
    setType(String(value?.type ?? 'bind')); setChoice(mountResourceChoice(value)); setSource(String(value?.source ?? '')); setTarget(String(value?.target ?? '')); setReadOnly(!!value?.read_only); setError('')
  }, [signature])
  const dirty = type !== String(mount?.type ?? 'bind') || target !== String(mount?.target ?? '') || readOnly !== !!mount?.read_only ||
    (type === 'bind' ? source !== String(mount?.source ?? '') : type === 'volume' ? JSON.stringify(choice) !== JSON.stringify(mountResourceChoice(mount)) : false)
  useEffect(() => {
    const key = `mounts:${service}:${index ?? 'new'}`
    pending?.(key, dirty)
    return () => pending?.(key, false)
  }, [pending, service, index, dirty])
  const query = useQuery({ queryKey: ['volumes', c.nodeID], queryFn: () => api<NodeResource[]>(nodePath(c.nodeID, '/volumes')), enabled: type === 'volume', staleTime: 15_000 })
  const keys = configKeys(c.compose, ['volumes'])
  const current = mount?.type === 'volume' && mount.source ? String(mount.source) : undefined
  const projectKeys = current && !keys.includes(current) ? [...keys, current] : keys
  const selected = choice.kind === 'new' ? '__new' : choice.name === '__anonymous' ? '__anonymous' : `${choice.kind}:${choice.name}`
  const sourceOptions: [string, string][] = [
    ['__new', c.zh ? '创建命名卷…' : 'New named volume…'],
    ...projectKeys.map((name): [string, string] => [`project:${name}`, `${c.zh ? '已配置' : 'Configured'} · ${name}`]),
    ...(query.data ?? []).map((volume): [string, string] => [`existing:${volume.name}`, `${c.zh ? '节点已有' : 'On node'} · ${volume.name}`]),
    ...(mount?.type === 'volume' && !mount.source ? [['__anonymous', c.zh ? '保留匿名卷' : 'Keep anonymous volume'] as [string, string]] : [])
  ]
  const apply = (keepOpen = false) => {
    if (c.disabled || (index !== undefined && !dirty)) return
    try {
      const next: Record<string, unknown> = { ...mount, type, target: target.trim() }
      if (mount?.type !== type) { delete next.volume; delete next.bind; delete next.tmpfs }
      if (readOnly) next.read_only = true
      else if (mount?.read_only !== undefined) next.read_only = false
      else delete next.read_only
      let resource: ProjectResourceChoice | undefined
      if (type === 'bind') {
        if (source !== mount?.source && (!source.trim().startsWith('/') || source.includes('$'))) throw new Error(c.zh ? '请填写目标节点上的绝对路径，不使用变量表达式。' : 'Use an absolute path on the target node without interpolation.')
        next.source = source.trim()
      } else if (type === 'tmpfs') delete next.source
      else if (choice.name === '__anonymous') delete next.source
      else if (mount?.type === 'volume' && choice.kind === 'project' && choice.name === mount.source) next.source = mount.source
      else resource = choice
      const changed = configureServiceMount(c.compose, c.service, next, resource, index)
      c.onCompose(changed); setError('')
      if (index === undefined && keepOpen) {
        if (type === 'volume') {
          const rows = configValue(changed, ['services', c.service, 'volumes']) as Record<string, unknown>[]
          setChoice({ kind: 'project', name: String(rows.at(-1)?.source ?? '') })
        }
        setTarget(''); targetInput.current?.focus()
      } else onClose?.()
    } catch (failure) { setError(errorText(failure, c.zh)) }
  }
  return <form className="space-y-3 border-b py-3" data-testid={index === undefined ? 'new-mount-mapping' : `mount-mapping-${index}`} onSubmit={event => { event.preventDefault(); apply() }}>
    <div className="grid grid-cols-2 items-start gap-2 lg:grid-cols-[132px_minmax(0,1fr)_24px_minmax(0,1fr)_auto_auto]">
      <div className="flex flex-col gap-1.5"><Label htmlFor={`${id}-type`}>{c.zh ? '类型 type' : 'Type type'}</Label><Select items={[{ value: 'bind', label: c.zh ? '主机路径' : 'Host path' }, { value: 'volume', label: c.zh ? '命名卷' : 'Named volume' }, { value: 'tmpfs', label: 'tmpfs' }]} value={type} onValueChange={next => { setType(String(next)); setError('') }}><SelectTrigger id={`${id}-type`} className="w-full"><SelectValue /></SelectTrigger><SelectContent align="start" alignItemWithTrigger={false} className="min-w-0"><SelectGroup><SelectItem value="bind">{c.zh ? '主机路径' : 'Host path'}</SelectItem><SelectItem value="volume">{c.zh ? '命名卷' : 'Named volume'}</SelectItem><SelectItem value="tmpfs">tmpfs</SelectItem></SelectGroup></SelectContent></Select></div>
      <div className="min-w-0 flex flex-col gap-1.5"><Label htmlFor={`${id}-source`}>{c.zh ? '来源 source' : 'Source source'}</Label>{type === 'bind' ? <Input id={`${id}-source`} value={source} placeholder="/data/app" autoComplete="off" onChange={event => { setSource(event.target.value); setError('') }} /> : type === 'tmpfs' ? <Input id={`${id}-source`} value={c.zh ? '内存临时存储' : 'Temporary memory storage'} readOnly /> : <><Select items={sourceOptions.map(([value, label]) => ({ value, label }))} value={selected} onValueChange={value => {
        const next = String(value)
        if (next === '__new') setChoice({ kind: 'new', name: '' })
        else if (next === '__anonymous') setChoice({ kind: 'project', name: '__anonymous' })
        else { const separator = next.indexOf(':'); setChoice({ kind: next.slice(0, separator) as ProjectResourceChoice['kind'], name: next.slice(separator + 1) }) }
        setError('')
      }}><SelectTrigger id={`${id}-source`} className="w-full"><SelectValue /></SelectTrigger><SelectContent align="start" alignItemWithTrigger={false} className="min-w-0"><SelectGroup>{sourceOptions.map(([value, label]) => <SelectItem key={value} value={value}>{label}</SelectItem>)}</SelectGroup></SelectContent></Select>{choice.kind === 'new' && <Input aria-label={c.zh ? '新建卷名称' : 'New named volume name'} value={choice.name} placeholder="app_data" autoComplete="off" onChange={event => { setChoice({ ...choice, name: event.target.value }); setError('') }} />}</> }</div>
      <ArrowRight className="mt-8 hidden size-4 text-muted-foreground lg:block" aria-hidden />
      <div className="col-span-2 min-w-0 space-y-1.5 lg:col-span-1"><Label htmlFor={`${id}-target`}>{c.zh ? '容器路径 target' : 'Container path target'}</Label><Input ref={targetInput} id={`${id}-target`} className="font-medium" value={target} placeholder="/app/data" autoComplete="off" onChange={event => { setTarget(event.target.value); setError('') }} /></div>
      <Label className="flex items-center gap-2 lg:mt-8"><Checkbox checked={readOnly} onCheckedChange={checked => setReadOnly(!!checked)} />{c.zh ? '只读' : 'Read-only'}</Label>
      {index !== undefined && <Button type="button" variant="ghost" size="icon-sm" className="justify-self-end lg:mt-7" aria-label={c.zh ? `移除挂载 ${mount?.target}` : `Remove mount ${mount?.target}`} onClick={() => c.onCompose(editConfig(c.compose, ['services', c.service, 'volumes', index], REMOVE))}><Trash2 /></Button>}
    </div>
    {type === 'volume' && query.isError && <p className="text-xs text-destructive">{c.zh ? '无法读取节点已有卷。' : 'Could not load existing node volumes.'}<Button type="button" variant="link" size="sm" onClick={() => void query.refetch()}>{c.zh ? '重试' : 'Retry'}</Button></p>}
    {mount && Object.keys(mount).some(key => !['type', 'source', 'target', 'read_only'].includes(key)) && <p className="text-xs text-muted-foreground">{c.zh ? '其他挂载选项保留在源码。' : 'Additional mount options are preserved in source.'}</p>}
    {error && <Alert variant="destructive"><AlertDescription>{error}</AlertDescription></Alert>}
    {(index === undefined || dirty) && <div className="flex flex-wrap gap-2"><Button type="submit" size="sm" disabled={c.disabled}>{index === undefined ? (c.zh ? '添加映射' : 'Add mapping') : (c.zh ? '保存映射' : 'Save mapping')}</Button>{index === undefined && <Button type="button" variant="outline" size="sm" disabled={c.disabled} onClick={() => apply(true)}>{c.zh ? '添加并继续' : 'Add & continue'}</Button>}<Button type="button" size="sm" variant="ghost" onClick={() => { if (index === undefined) onClose?.(); else reset() }}>{c.zh ? '取消' : 'Cancel'}</Button></div>}
  </form>
}
