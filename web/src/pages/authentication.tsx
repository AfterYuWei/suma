import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { GitBranch, KeyRound, Pencil, Plus, Search, Server, ShieldCheck, Trash2, X } from 'lucide-react'
import { useMemo, useState, type FormEvent, type ReactNode } from 'react'
import { Alert, AlertDescription } from '../components/ui/alert'
import { Button } from '../components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '../components/ui/card'
import { Checkbox } from '../components/ui/checkbox'
import { Input } from '../components/ui/input'
import { Label } from '../components/ui/label'
import { ListShell } from '../components/ui/list-shell'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../components/ui/select'
import { Sheet, SheetContent, SheetFooter, SheetHeader, SheetTitle } from '../components/ui/sheet'
import { Spinner } from '../components/ui/spinner'
import { Tabs, TabsList, TabsTrigger } from '../components/ui/tabs'
import { Textarea } from '../components/ui/textarea'
import type { GitAuthType, GitCredential, GitCredentialInput } from '../features/delivery/types'
import { api } from '../lib/api'
import { useI18n } from '../lib/i18n'
import type { DockerNode } from '../lib/nodes'
import { confirmDialog } from '../stores/dialog'
import { ResourceFrame } from './images'

type Tab = 'git' | 'registries' | 'docker-tls'
type RegistryAuthType = 'basic' | 'token'
interface RegistryCredential { id: number; name: string; server_address: string; auth_type: RegistryAuthType; username?: string; fingerprint?: string; created_at: string; updated_at: string; last_used_at?: string; authorized_node_ids: string[] }
interface RegistryInput { name: string; server_address: string; auth_type: RegistryAuthType; username: string; secret: string; authorized_node_ids: string[] }
interface TLSCredential { id: number; name: string; fingerprint: string; authorized_node_ids: string[]; created_at: string; updated_at: string }
interface TLSInput { name: string; ca: string; certificate: string; private_key: string; authorized_node_ids: string[] }

const emptyGit = (): GitCredentialInput => ({ name: '', auth_type: 'http_token', username: '', secret: '', private_key: '', passphrase: '', known_hosts: '', custom_ca: '', authorized_node_ids: [] })
const emptyRegistry = (): RegistryInput => ({ name: '', server_address: '', auth_type: 'basic', username: '', secret: '', authorized_node_ids: [] })
const emptyTLS = (): TLSInput => ({ name: '', ca: '', certificate: '', private_key: '', authorized_node_ids: [] })

export function AuthenticationPage() {
  const { language } = useI18n(); const zh = language === 'zh-CN'
  const client = useQueryClient(); const [tab, setTab] = useState<Tab>('git'); const [search, setSearch] = useState('')
  const [editingGit, setEditingGit] = useState<GitCredential | null>(null); const [gitInput, setGitInput] = useState<GitCredentialInput>(emptyGit); const [gitOpen, setGitOpen] = useState(false)
  const [editingRegistry, setEditingRegistry] = useState<RegistryCredential | null>(null); const [registryInput, setRegistryInput] = useState<RegistryInput>(emptyRegistry); const [registryOpen, setRegistryOpen] = useState(false)
  const [editingTLS, setEditingTLS] = useState<TLSCredential | null>(null); const [tlsInput, setTLSInput] = useState<TLSInput>(emptyTLS); const [tlsOpen, setTLSOpen] = useState(false)
  const git = useQuery({ queryKey: ['git-credentials'], queryFn: () => api<GitCredential[]>('/credentials/git') })
  const registries = useQuery({ queryKey: ['registry-credentials'], queryFn: () => api<RegistryCredential[]>('/credentials/registries') })
  const tls = useQuery({ queryKey: ['docker-tls-credentials'], queryFn: () => api<TLSCredential[]>('/credentials/docker-tls') })
  const nodes = useQuery({ queryKey: ['nodes'], queryFn: () => api<DockerNode[]>('/nodes') })
  const saveGit = useMutation({ mutationFn: (value: GitCredentialInput) => api<GitCredential>(editingGit ? `/credentials/git/${editingGit.id}` : '/credentials/git', { method: editingGit ? 'PUT' : 'POST', body: JSON.stringify(value) }), onSuccess: () => { void client.invalidateQueries({ queryKey: ['git-credentials'] }); setGitOpen(false) } })
  const saveRegistry = useMutation({ mutationFn: (value: RegistryInput) => api<RegistryCredential>(editingRegistry ? `/credentials/registries/${editingRegistry.id}` : '/credentials/registries', { method: editingRegistry ? 'PUT' : 'POST', body: JSON.stringify(value) }), onSuccess: () => { void client.invalidateQueries({ queryKey: ['registry-credentials'] }); setRegistryOpen(false) } })
  const saveTLS = useMutation({ mutationFn: (value: TLSInput) => api<TLSCredential>(editingTLS ? `/credentials/docker-tls/${editingTLS.id}` : '/credentials/docker-tls', { method: editingTLS ? 'PUT' : 'POST', body: JSON.stringify(value) }), onSuccess: () => { void client.invalidateQueries({ queryKey: ['docker-tls-credentials'] }); setTLSOpen(false) } })
  const term = search.trim().toLowerCase()
  const filteredGit = useMemo(() => git.data?.filter((row) => `${row.name} ${row.auth_type} ${row.username || ''} ${row.fingerprint || ''}`.toLowerCase().includes(term)) || [], [git.data, term])
  const filteredRegistries = useMemo(() => registries.data?.filter((row) => `${row.name} ${row.server_address} ${row.auth_type} ${row.username || ''}`.toLowerCase().includes(term)) || [], [registries.data, term])
  const filteredTLS = useMemo(() => tls.data?.filter((row) => `${row.name} ${row.fingerprint}`.toLowerCase().includes(term)) || [], [tls.data, term])
  const create = () => tab === 'git' ? (setEditingGit(null), setGitInput(emptyGit()), setGitOpen(true)) : tab === 'registries' ? (setEditingRegistry(null), setRegistryInput(emptyRegistry()), setRegistryOpen(true)) : (setEditingTLS(null), setTLSInput(emptyTLS()), setTLSOpen(true))
  const removeGit = async (row: GitCredential) => { if (row.used_by) { await confirmDialog({ title: zh ? '凭据正在使用' : 'Credential in use', description: zh ? `“${row.name}”正被 ${row.used_by} 个项目使用，请先在项目中更换认证来源。` : `“${row.name}” is used by ${row.used_by} project(s). Change those projects first.`, confirmLabel: zh ? '知道了' : 'Got it' }); return } if (!await confirmDialog({ title: zh ? '删除 Git 凭据？' : 'Delete Git credential?', description: zh ? `将永久删除“${row.name}”。` : `Permanently delete “${row.name}”.`, confirmLabel: zh ? '删除' : 'Delete', danger: true })) return; await api(`/credentials/git/${row.id}`, { method: 'DELETE' }); await client.invalidateQueries({ queryKey: ['git-credentials'] }) }
  const removeRegistry = async (row: RegistryCredential) => { if (!await confirmDialog({ title: zh ? '删除镜像仓库凭据？' : 'Delete registry credential?', description: zh ? `将永久删除“${row.name}”。` : `Permanently delete “${row.name}”.`, confirmLabel: zh ? '删除' : 'Delete', danger: true })) return; await api(`/credentials/registries/${row.id}`, { method: 'DELETE' }); await client.invalidateQueries({ queryKey: ['registry-credentials'] }) }
  const removeTLS = async (row: TLSCredential) => { if (!await confirmDialog({ title: zh ? '删除 Docker TLS 凭据？' : 'Delete Docker TLS credential?', description: zh ? '请先取消节点引用和全部授权。' : 'Detach node references and grants first.', confirmLabel: zh ? '删除' : 'Delete', danger: true })) return; await api(`/credentials/docker-tls/${row.id}`, { method: 'DELETE' }); await client.invalidateQueries({ queryKey: ['docker-tls-credentials'] }) }

  return <ResourceFrame title={zh ? '认证中心' : 'Authentication Center'} detail={zh ? '集中保存 Git、镜像仓库与 Docker TLS 凭据，并按节点授权。' : 'Store Git, registry, and Docker TLS credentials with per-node grants.'} action={<Button onClick={create}><Plus data-icon="inline-start" />{zh ? '新增凭据' : 'New credential'}</Button>}>
    <div className="flex w-full flex-col gap-5">
      <div className="flex w-full flex-wrap items-center gap-3">
        <Tabs value={tab} onValueChange={(value) => setTab(value as Tab)}>
          <TabsList variant="line">
            <TabsTrigger value="git"><GitBranch className="size-4" />{zh ? 'Git 凭据' : 'Git credentials'}</TabsTrigger>
            <TabsTrigger value="registries"><Server className="size-4" />{zh ? '镜像仓库' : 'Registries'}</TabsTrigger>
            <TabsTrigger value="docker-tls"><ShieldCheck className="size-4" />Docker TLS</TabsTrigger>
          </TabsList>
        </Tabs>
        <div className="relative ml-auto w-full sm:w-64">
          <Search className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input value={search} onChange={(event) => setSearch(event.target.value)} placeholder={zh ? '搜索凭据' : 'Search credentials'} className="pr-8 pl-8" />
          {search && <Button variant="ghost" size="icon-xs" aria-label={zh ? '清除搜索' : 'Clear search'} className="absolute top-1/2 right-0.5 -translate-y-1/2 text-muted-foreground" onClick={() => setSearch('')}><X className="size-3.5" /></Button>}
        </div>
      </div>
      {tab === 'git' && <CredentialList empty={filteredGit.length === 0} zh={zh}>{filteredGit.map((row) => <CredentialRow key={row.id} icon={<KeyRound className="size-[18px]" />} title={row.name} detail={`${gitAuthLabel(row.auth_type, zh)}${row.username ? ` · ${row.username}` : ''}${row.fingerprint ? ` · ${row.fingerprint}` : ''}`} meta={zh ? `授权 ${row.authorized_node_ids?.length || 0} 个节点` : `${row.authorized_node_ids?.length || 0} node grants`} onEdit={() => { setEditingGit(row); setGitInput({ ...emptyGit(), name: row.name, auth_type: row.auth_type, username: row.username || '', authorized_node_ids: row.authorized_node_ids || [] }); setGitOpen(true) }} onRemove={() => void removeGit(row)} />)}</CredentialList>}
      {tab === 'registries' && <CredentialList empty={filteredRegistries.length === 0} zh={zh}>{filteredRegistries.map((row) => <CredentialRow key={row.id} icon={<Server className="size-[18px]" />} title={row.name} detail={`${row.server_address} · ${row.auth_type === 'basic' ? (zh ? '账号密码' : 'Username/password') : 'Token'}`} meta={zh ? `授权 ${row.authorized_node_ids?.length || 0} 个节点` : `${row.authorized_node_ids?.length || 0} node grants`} onEdit={() => { setEditingRegistry(row); setRegistryInput({ ...emptyRegistry(), name: row.name, server_address: row.server_address, auth_type: row.auth_type, username: row.username || '', authorized_node_ids: row.authorized_node_ids || [] }); setRegistryOpen(true) }} onRemove={() => void removeRegistry(row)} />)}</CredentialList>}
      {tab === 'docker-tls' && <CredentialList empty={filteredTLS.length === 0} zh={zh}>{filteredTLS.map((row) => <CredentialRow key={row.id} icon={<ShieldCheck className="size-[18px]" />} title={row.name} detail={row.fingerprint} meta={zh ? `授权 ${row.authorized_node_ids.length} 个节点` : `${row.authorized_node_ids.length} node grants`} onEdit={() => { setEditingTLS(row); setTLSInput({ ...emptyTLS(), name: row.name, authorized_node_ids: row.authorized_node_ids }); setTLSOpen(true) }} onRemove={() => void removeTLS(row)} />)}</CredentialList>}
      {gitOpen && <GitEditor zh={zh} nodes={nodes.data || []} editing={editingGit} input={gitInput} setInput={setGitInput} close={() => setGitOpen(false)} submit={(event) => { event.preventDefault(); saveGit.mutate(gitInput) }} error={saveGit.error?.message} pending={saveGit.isPending} />}
      {registryOpen && <RegistryEditor zh={zh} nodes={nodes.data || []} editing={editingRegistry} input={registryInput} setInput={setRegistryInput} close={() => setRegistryOpen(false)} submit={(event) => { event.preventDefault(); saveRegistry.mutate(registryInput) }} error={saveRegistry.error?.message} pending={saveRegistry.isPending} />}
      {tlsOpen && <TLSEditor zh={zh} nodes={nodes.data || []} editing={editingTLS} input={tlsInput} setInput={setTLSInput} close={() => setTLSOpen(false)} submit={(event) => { event.preventDefault(); saveTLS.mutate(tlsInput) }} error={saveTLS.error?.message} pending={saveTLS.isPending} />}
    </div>
  </ResourceFrame>
}

function CredentialList({ empty, zh, children }: { empty: boolean; zh: boolean; children: ReactNode }) { return empty ? <EmptyHint icon={<ShieldCheck className="size-6" />} title={zh ? '暂无匹配的凭据' : 'No matching credentials'} /> : <ListShell><ul className="flex w-full flex-col divide-y text-sm">{children}</ul></ListShell> }
function EmptyHint({ icon, title }: { icon: ReactNode; title: string }) { return <div className="flex w-full flex-col items-center gap-1.5 rounded-xl border border-dashed py-10 text-center"><span className="text-muted-foreground">{icon}</span><p className="text-sm font-medium">{title}</p></div> }
function CredentialRow({ icon, title, detail, meta, onEdit, onRemove }: { icon: ReactNode; title: string; detail: string; meta: string; onEdit: () => void; onRemove: () => void }) {
  return <li className="flex items-center gap-3 px-3 py-2.5">
    <span className="shrink-0 text-muted-foreground">{icon}</span>
    <div className="min-w-0 flex-1">
      <p className="truncate font-medium">{title}</p>
      <p className="truncate text-xs text-muted-foreground">{detail}</p>
    </div>
    <span className="hidden shrink-0 text-xs text-muted-foreground sm:inline">{meta}</span>
    <Button variant="ghost" size="icon-sm" aria-label="Edit" onClick={onEdit}><Pencil /></Button>
    <Button variant="ghost" size="icon-sm" aria-label="Remove" className="text-destructive hover:bg-destructive/10 hover:text-destructive" onClick={onRemove}><Trash2 /></Button>
  </li>
}
function Field({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return <div className="flex flex-col gap-1.5">
    <div className="flex flex-wrap items-baseline gap-x-2">
      <Label>{label}</Label>
      {hint && <span className="text-xs text-muted-foreground">{hint}</span>}
    </div>
    {children}
  </div>
}
function EditorShell({ title, close, submit, error, pending, children }: { title: string; close: () => void; submit: (event: FormEvent<HTMLFormElement>) => void; error?: string; pending: boolean; children: ReactNode }) {
  const saveLabel = /[\u4e00-\u9fff]/.test(title) ? '保存' : 'Save'
  return <Sheet open onOpenChange={(open) => { if (!open) close() }}>
    <SheetContent side="right" className="sm:max-w-[520px]">
      <SheetHeader className="border-b pr-12">
        <SheetTitle>{title}</SheetTitle>
      </SheetHeader>
      <form onSubmit={submit} className="flex min-h-0 flex-1 flex-col">
        <div className="flex flex-1 flex-col gap-4 overflow-y-auto p-4">
          {children}
          {error && <Alert variant="destructive"><AlertDescription>{error}</AlertDescription></Alert>}
        </div>
        <SheetFooter className="border-t bg-transparent">
          <Button type="submit" disabled={pending} className="w-full">{pending && <Spinner />}{saveLabel}</Button>
        </SheetFooter>
      </form>
    </SheetContent>
  </Sheet>
}
const gitTypeItems: { value: GitAuthType; label: string }[] = [
  { value: 'http_token', label: 'HTTPS token' },
  { value: 'http_basic', label: 'HTTPS basic' },
  { value: 'ssh_key', label: 'SSH key' },
  { value: 'none', label: 'None' },
]
function GitEditor({ zh, nodes, editing, input, setInput, close, submit, error, pending }: { zh: boolean; nodes: DockerNode[]; editing: GitCredential | null; input: GitCredentialInput; setInput: React.Dispatch<React.SetStateAction<GitCredentialInput>>; close: () => void; submit: (event: FormEvent<HTMLFormElement>) => void; error?: string; pending: boolean }) {
  const set = <K extends keyof GitCredentialInput>(key: K, value: GitCredentialInput[K]) => setInput((current) => ({ ...current, [key]: value }))
  return <EditorShell title={editing ? (zh ? '编辑 Git 凭据' : 'Edit Git credential') : (zh ? '新增 Git 凭据' : 'New Git credential')} close={close} submit={submit} error={error} pending={pending}>
    <Field label={zh ? '名称' : 'Name'}><Input required value={input.name} onChange={(event) => set('name', event.target.value)} /></Field>
    <Field label={zh ? '认证类型' : 'Authentication type'}>
      <Select value={input.auth_type} onValueChange={(value) => set('auth_type', value as GitAuthType)}>
        <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
        <SelectContent>{gitTypeItems.map((item) => <SelectItem key={item.value} value={item.value}>{item.label}</SelectItem>)}</SelectContent>
      </Select>
    </Field>
    {(input.auth_type === 'http_token' || input.auth_type === 'http_basic') && <>
      <Field label={input.auth_type === 'http_basic' ? (zh ? '用户名' : 'Username') : (zh ? '用户名（可选）' : 'Username (optional)')}><Input value={input.username} onChange={(event) => set('username', event.target.value)} /></Field>
      <Field label={input.auth_type === 'http_token' ? 'Token' : (zh ? '密码' : 'Password')} hint={editing ? (zh ? '留空保留原值' : 'Leave empty to preserve') : undefined}>
        <Input type="password" autoComplete="new-password" value={input.secret} onChange={(event) => set('secret', event.target.value)} />
      </Field>
    </>}
    {input.auth_type === 'ssh_key' && <>
      <Field label={zh ? 'SSH 私钥' : 'SSH private key'} hint={editing ? (zh ? '留空保留原值' : 'Leave empty to preserve') : undefined}><Textarea rows={5} value={input.private_key} onChange={(event) => set('private_key', event.target.value)} /></Field>
      <Field label="known_hosts"><Textarea rows={4} value={input.known_hosts} onChange={(event) => set('known_hosts', event.target.value)} /></Field>
      <Field label={zh ? '私钥密码（可选）' : 'Passphrase (optional)'}><Input type="password" value={input.passphrase} onChange={(event) => set('passphrase', event.target.value)} /></Field>
    </>}
    <Field label={zh ? '自定义 CA（可选）' : 'Custom CA (optional)'}><Textarea rows={4} value={input.custom_ca} onChange={(event) => set('custom_ca', event.target.value)} /></Field>
    <NodeGrantSelector zh={zh} nodes={nodes} value={input.authorized_node_ids || []} onChange={(value) => set('authorized_node_ids', value)} />
  </EditorShell>
}

function RegistryEditor({ zh, nodes, editing, input, setInput, close, submit, error, pending }: { zh: boolean; nodes: DockerNode[]; editing: RegistryCredential | null; input: RegistryInput; setInput: React.Dispatch<React.SetStateAction<RegistryInput>>; close: () => void; submit: (event: FormEvent<HTMLFormElement>) => void; error?: string; pending: boolean }) {
  const set = <K extends keyof RegistryInput>(key: K, value: RegistryInput[K]) => setInput((current) => ({ ...current, [key]: value }))
  return <EditorShell title={editing ? (zh ? '编辑镜像仓库凭据' : 'Edit registry credential') : (zh ? '新增镜像仓库凭据' : 'New registry credential')} close={close} submit={submit} error={error} pending={pending}>
    <Field label={zh ? '名称' : 'Name'}><Input required value={input.name} onChange={(event) => set('name', event.target.value)} /></Field>
    <Field label={zh ? '仓库地址' : 'Registry address'} hint="host[:port]"><Input required value={input.server_address} onChange={(event) => set('server_address', event.target.value)} placeholder="registry.example.com:5000" /></Field>
    <Field label={zh ? '认证类型' : 'Authentication type'}>
      <Select value={input.auth_type} onValueChange={(value) => set('auth_type', value as RegistryAuthType)}>
        <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
        <SelectContent>
          <SelectItem value="basic">{zh ? '账号密码' : 'Username/password'}</SelectItem>
          <SelectItem value="token">Token</SelectItem>
        </SelectContent>
      </Select>
    </Field>
    {input.auth_type === 'basic' && <Field label={zh ? '用户名' : 'Username'}><Input value={input.username} onChange={(event) => set('username', event.target.value)} /></Field>}
    <Field label={input.auth_type === 'basic' ? (zh ? '密码' : 'Password') : 'Token'} hint={editing ? (zh ? '留空保留原值' : 'Leave empty to preserve') : undefined}>
      <Input type="password" autoComplete="new-password" value={input.secret} onChange={(event) => set('secret', event.target.value)} />
    </Field>
    <NodeGrantSelector zh={zh} nodes={nodes} value={input.authorized_node_ids} onChange={(value) => set('authorized_node_ids', value)} />
  </EditorShell>
}

function TLSEditor({ zh, nodes, editing, input, setInput, close, submit, error, pending }: { zh: boolean; nodes: DockerNode[]; editing: TLSCredential | null; input: TLSInput; setInput: React.Dispatch<React.SetStateAction<TLSInput>>; close: () => void; submit: (event: FormEvent<HTMLFormElement>) => void; error?: string; pending: boolean }) {
  const set = <K extends keyof TLSInput>(key: K, value: TLSInput[K]) => setInput((current) => ({ ...current, [key]: value }))
  const preserve = editing ? (zh ? '留空保留原值' : 'Leave all empty to preserve') : undefined
  return <EditorShell title={editing ? (zh ? '编辑 Docker TLS 凭据' : 'Edit Docker TLS credential') : (zh ? '新增 Docker TLS 凭据' : 'New Docker TLS credential')} close={close} submit={submit} error={error} pending={pending}>
    <Field label={zh ? '名称' : 'Name'}><Input required value={input.name} onChange={(event) => set('name', event.target.value)} /></Field>
    <Field label="CA PEM" hint={preserve}><Textarea rows={5} value={input.ca} onChange={(event) => set('ca', event.target.value)} /></Field>
    <Field label={zh ? '客户端证书 PEM' : 'Client certificate PEM'} hint={preserve}><Textarea rows={5} value={input.certificate} onChange={(event) => set('certificate', event.target.value)} /></Field>
    <Field label={zh ? '客户端私钥 PEM' : 'Client private key PEM'} hint={preserve}><Textarea rows={5} value={input.private_key} onChange={(event) => set('private_key', event.target.value)} /></Field>
    <NodeGrantSelector zh={zh} nodes={nodes} value={input.authorized_node_ids} onChange={(value) => set('authorized_node_ids', value)} />
  </EditorShell>
}

function NodeGrantSelector({ zh, nodes, value, onChange }: { zh: boolean; nodes: DockerNode[]; value: string[]; onChange: (value: string[]) => void }) {
  const toggle = (id: string) => onChange(value.includes(id) ? value.filter((item) => item !== id) : [...value, id])
  return <Card className="w-full">
    <CardHeader><CardTitle className="text-sm">{zh ? '节点授权（默认不授权）' : 'Node grants (none by default)'}</CardTitle></CardHeader>
    <CardContent className="flex flex-col gap-2.5">
      {nodes.length === 0 ? <p className="text-sm text-muted-foreground">{zh ? '暂无节点' : 'No nodes'}</p> : nodes.map((node) => (
        <label key={node.id} className="flex cursor-pointer items-start gap-2">
          <Checkbox checked={value.includes(node.id)} onCheckedChange={() => toggle(node.id)} className="mt-0.5" />
          <span className="flex min-w-0 flex-col">
            <span className="truncate text-sm">{node.name}</span>
            <span className="text-xs text-muted-foreground">{node.connection_type}</span>
          </span>
        </label>
      ))}
    </CardContent>
  </Card>
}
function gitAuthLabel(value: GitAuthType, zh: boolean) { if (value === 'http_token') return 'HTTPS token'; if (value === 'http_basic') return zh ? 'HTTPS 用户名/密码' : 'HTTPS basic'; if (value === 'ssh_key') return 'SSH key'; return zh ? '无需认证' : 'No authentication' }
