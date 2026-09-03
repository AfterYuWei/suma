import { useMutation, useQuery } from '@tanstack/react-query'
import { Copy, Plus, Save, Trash2 } from 'lucide-react'
import { type FormEvent, type ReactNode, useEffect, useRef, useState } from 'react'
import { Alert, AlertDescription, AlertTitle } from '../../components/ui/alert'
import { Button } from '../../components/ui/button'
import { Card, CardContent } from '../../components/ui/card'
import { Checkbox } from '../../components/ui/checkbox'
import { Input } from '../../components/ui/input'
import { Label } from '../../components/ui/label'
import { ListPagination } from '../../components/ui/list-pagination'
import { RadioGroup, RadioGroupItem } from '../../components/ui/radio-group'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../../components/ui/select'
import { Spinner } from '../../components/ui/spinner'
import { Textarea } from '../../components/ui/textarea'
import { useListPagination } from '../../components/ui/use-list-pagination'
import { api } from '../../lib/api'
import { cn } from '../../lib/utils'
import { choiceDialog } from '../../stores/dialog'
import type { DockerNode } from '../../lib/nodes'
import {
  type CDConfiguration,
  type CDConfigureInput,
  type GitAuthType,
  type GitAuthentication,
  type GitCredential,
  type GitCredentialInput,
  normalizedCDConfiguration,
} from './types'

function toDraft(value: CDConfiguration): CDConfigureInput {
  const configuration = normalizedCDConfiguration(value)
  return {
    repository: { ...configuration.repository },
    reconcile_mode: configuration.reconcile_mode,
    sync_interval_seconds: configuration.sync_interval_seconds,
    auto_rollback: configuration.auto_rollback,
    deployment_timeout: configuration.deployment_timeout,
    webhook_enabled: configuration.webhook_enabled,
    webhook_secret: configuration.webhook_secret || '',
    node_ids: configuration.node_ids,
    registry_credential_ids: configuration.registry_credential_ids,
  }
}

export function CDSettings({ projectName, configuration, zh, onSaved }: { projectName: string; configuration: CDConfiguration; zh: boolean; onSaved: (value: CDConfiguration) => void }) {
  const initial = normalizedCDConfiguration(configuration)
  const [draft, setDraft] = useState<CDConfigureInput>(() => toDraft(initial))
  const [composeFiles, setComposeFiles] = useState<string[]>(initial.repository.compose_files.length ? initial.repository.compose_files : ['compose.yml'])
  const [notice, setNotice] = useState('')
  const [revealedWebhookSecret, setRevealedWebhookSecret] = useState('')
  const [secretCopied, setSecretCopied] = useState(false)
  const configurationSignature = useRef(JSON.stringify(toDraft(initial)))
  const nodes = useQuery({ queryKey: ['nodes'], queryFn: () => api<DockerNode[]>('/nodes') })
  const registries = useQuery({ queryKey: ['registry-credentials'], queryFn: () => api<{ id: number; name: string; server_address: string; authorized_node_ids: string[] }[]>('/credentials/registries') })

  useEffect(() => {
    const value = normalizedCDConfiguration(configuration)
    const signature = JSON.stringify(toDraft(value))
    if (signature === configurationSignature.current) return
    configurationSignature.current = signature
    setDraft(toDraft(value))
    setComposeFiles(value.repository.compose_files.length ? value.repository.compose_files : ['compose.yml'])
  }, [configuration])

  const save = useMutation({
    mutationFn: (input: CDConfigureInput) => api<CDConfiguration>(`/delivery-projects/${encodeURIComponent(projectName)}/configuration`, { method: 'PUT', body: JSON.stringify(input) }),
    onSuccess: (value) => {
      if (value.webhook_secret) setRevealedWebhookSecret(value.webhook_secret)
      setNotice(zh ? '持续交付配置已保存。' : 'Continuous delivery configuration saved.')
      onSaved(value)
    },
    onError: (error) => setNotice(error.message),
  })

  const updateRepository = <K extends keyof CDConfigureInput['repository']>(key: K, value: CDConfigureInput['repository'][K]) => {
    setDraft((current) => ({ ...current, repository: { ...current.repository, [key]: value } }))
  }

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (draft.node_ids.length === 0) {
      setNotice(zh ? '请至少选择一个目标节点。' : 'Select at least one target node.')
      return
    }
    const files = composeFiles.map((file) => file.trim())
    if (!files.length || files.some((file) => !file)) {
      setNotice(zh ? '请填写所有 Compose 文件路径。' : 'Complete every Compose file path.')
      return
    }
    let authentication = draft.repository.authentication
    if (authentication.source === 'project' && authentication.credential) {
      const material = authentication.credential
      const summary = authentication.summary
      const changed = !!(material.secret || material.private_key || material.passphrase || material.known_hosts || material.custom_ca || !summary || material.name !== summary.name || material.auth_type !== summary.auth_type || material.username !== (summary.username || ''))
      if (changed) {
        const choice = await choiceDialog({ title: zh ? '保存项目认证凭据' : 'Save project credential', description: zh ? '该凭据可以仅由当前项目使用，也可以保存到认证中心供其他功能选择。' : 'Keep this credential private to the current project, or save it to the Authentication Center for reuse.', choices: [{ value: 'project', label: zh ? '仅保存到此项目' : 'Only this project' }, { value: 'center', label: zh ? '保存到认证中心并使用' : 'Save to center and use', primary: true }] })
        if (!choice) return
        authentication = { ...authentication, save_to_center: choice === 'center' }
      } else {
        authentication = { source: 'project' }
      }
    } else if (authentication.source === 'project') {
      authentication = { source: 'project' }
    }
    save.mutate({ ...draft, repository: { ...draft.repository, authentication, compose_files: files } })
  }

  const webhookURL = configuration.webhook_id
    ? `${window.location.origin}/api/v1/webhooks/git/${configuration.webhook_id}`
    : ''

  return <form onSubmit={submit} className="flex w-full max-w-[960px] flex-col gap-8">
    {!configuration.configured && <Alert>
      <AlertTitle>{zh ? '连接 Git 持续交付' : 'Connect Git continuous delivery'}</AlertTitle>
      <AlertDescription>{zh ? '保存后 Git 将成为部署期望状态来源，SUMA 中的 Compose 文件会切换为只读。' : 'After saving, Git becomes the deployment source of truth and Compose files become read-only in SUMA.'}</AlertDescription>
    </Alert>}

    <SettingsSection title={zh ? '目标节点' : 'Target nodes'} description={zh ? '一次审批后并行发布到全部选中节点；修改只影响后续 Release。' : 'One approval deploys to every selected node in parallel. Changes affect only future releases.'}>
      <TargetSelector nodes={nodes.data || []} value={draft.node_ids} zh={zh} onChange={(node_ids) => setDraft((current) => ({ ...current, node_ids }))} />
    </SettingsSection>

    <SettingsSection title={zh ? 'Git 仓库' : 'Git repository'} description={zh ? '支持任意标准 HTTPS 或 SSH Git 仓库，不区分代码托管平台。' : 'Works with any standard HTTPS or SSH Git repository without a hosting-provider setting.'}>
      <Field label="Git Clone URL" hint={zh ? '支持 HTTPS、ssh:// 或 git@host:path；认证信息请使用下方凭据，不要写入 URL。' : 'Supports HTTPS, ssh://, or git@host:path. Use a credential below instead of embedding secrets in the URL.'}>
        <Input required value={draft.repository.clone_url} onChange={(event) => updateRepository('clone_url', event.target.value)} placeholder="https://git.example.com/team/deploy.git" />
      </Field>
    </SettingsSection>

    <SettingsSection title={zh ? '版本与文件' : 'Revision and files'} description={zh ? 'SUMA 会检出精确 Commit，并按仓库根目录相对路径依次合并 Compose 文件。' : 'SUMA checks out an exact commit and merges repository-root-relative Compose files in order.'}>
      <div className="grid gap-4 sm:grid-cols-[150px_1fr]">
        <Field label={zh ? '引用类型' : 'Reference type'}>
          <Select value={draft.repository.ref_type} onValueChange={(value) => updateRepository('ref_type', value as CDConfigureInput['repository']['ref_type'])}>
            <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="branch">Branch</SelectItem>
              <SelectItem value="tag">Tag</SelectItem>
              <SelectItem value="commit">Commit</SelectItem>
            </SelectContent>
          </Select>
        </Field>
        <Field label={draft.repository.ref_type === 'commit' ? 'Commit SHA' : (zh ? '引用名称' : 'Reference')}>
          <Input required value={draft.repository.ref} onChange={(event) => updateRepository('ref', event.target.value)} placeholder={draft.repository.ref_type === 'branch' ? 'main' : draft.repository.ref_type === 'tag' ? 'v1.0.0' : '40-character commit SHA'} />
        </Field>
      </div>
      <Field label={zh ? 'Compose 文件（仓库根目录相对路径）' : 'Compose files (relative to repository root)'} hint={zh ? '可位于不同目录；按从上到下的顺序合并，后面的文件覆盖前面的配置。' : 'Files may live in different directories and are merged top to bottom, with later files overriding earlier configuration.'}>
        <div className="flex flex-col gap-2">
          {composeFiles.map((file, index) => (
            <div key={index} className="flex items-center gap-2">
              <Input required value={file} onChange={(event) => setComposeFiles((current) => current.map((currentFile, fileIndex) => fileIndex === index ? event.target.value : currentFile))} placeholder={index === 0 ? 'compose.yml' : 'environments/production.yml'} />
              {composeFiles.length > 1 && <Button variant="destructive" size="icon" onClick={() => setComposeFiles((current) => current.filter((_, fileIndex) => fileIndex !== index))} aria-label={zh ? '删除 Compose 文件' : 'Remove Compose file'}><Trash2 /></Button>}
            </div>
          ))}
        </div>
        <Button variant="outline" size="sm" onClick={() => setComposeFiles((current) => [...current, ''])}><Plus data-icon="inline-start" />{zh ? '添加 Compose 文件' : 'Add Compose file'}</Button>
      </Field>
      <Field label={zh ? '环境变量文件（仓库根目录相对路径）' : 'Environment file (relative to repository root)'} hint={zh ? '可选，用于 Compose 中的 ${VARIABLE} 插值。' : 'Optional. Used for ${VARIABLE} interpolation in Compose files.'}><Input value={draft.repository.environment_file} onChange={(event) => updateRepository('environment_file', event.target.value)} placeholder="env/production.env" /></Field>
    </SettingsSection>

    <SettingsSection title={zh ? 'Git 认证' : 'Git authentication'} description={zh ? '凭据独立加密保存，可用于任何兼容的 HTTPS 或 SSH 仓库；公开仓库可以不选择。' : 'Credentials are encrypted separately and work with any compatible HTTPS or SSH repository. Public repositories need none.'}>
      <CredentialSelector zh={zh} nodeIDs={draft.node_ids} value={draft.repository.authentication} onChange={(value) => updateRepository('authentication', value)} />
    </SettingsSection>

    <SettingsSection title={zh ? '镜像仓库凭据' : 'Registry credentials'} description={zh ? '为发布显式选择凭据；只显示已授权给全部目标节点的凭据。' : 'Explicitly select credentials for deployment. Only credentials granted to every target are available.'}>
      <RegistryCredentialSelector rows={(registries.data || []).filter((row) => draft.node_ids.every((id) => row.authorized_node_ids.includes(id)))} value={draft.registry_credential_ids} zh={zh} onChange={(registry_credential_ids) => setDraft((current) => ({ ...current, registry_credential_ids }))} />
    </SettingsSection>

    <SettingsSection title={zh ? '交付策略' : 'Delivery policy'} description={zh ? 'Observe 只生成待发布版本，Manual 需要人工发布，Auto 验证后自动发布。' : 'Observe records a candidate, Manual waits for an operator, and Auto deploys after validation.'}>
      <RadioGroup value={draft.reconcile_mode} onValueChange={(value) => setDraft((current) => ({ ...current, reconcile_mode: value as CDConfigureInput['reconcile_mode'] }))} className="grid gap-2 sm:grid-cols-3">
        {([
          ['observe', zh ? '观察' : 'Observe', zh ? '检测新提交但不自动应用' : 'Detect changes without applying'],
          ['manual', zh ? '手动' : 'Manual', zh ? '同步后由用户确认发布' : 'Require an explicit deploy action'],
          ['auto', zh ? '自动' : 'Auto', zh ? '验证通过后立即发布' : 'Deploy immediately after validation'],
        ] as [CDConfigureInput['reconcile_mode'], string, string][]).map(([value, label, detail]) => (
          <label key={value} className={cn('flex cursor-pointer items-start gap-2.5 rounded-lg border p-3 transition-colors', draft.reconcile_mode === value ? 'border-ring bg-muted/50 ring-ring/50 ring-2' : 'hover:bg-muted/40')}>
            <RadioGroupItem value={value} className="mt-0.5" />
            <span className="min-w-0">
              <span className="block text-sm font-medium">{label}</span>
              <span className="mt-0.5 block text-xs text-muted-foreground">{detail}</span>
            </span>
          </label>
        ))}
      </RadioGroup>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label={zh ? '轮询间隔（秒）' : 'Polling interval (seconds)'} hint="30–86400"><Input required min={30} max={86400} type="number" value={draft.sync_interval_seconds} onChange={(event) => setDraft((current) => ({ ...current, sync_interval_seconds: Number(event.target.value) }))} /></Field>
        <Field label={zh ? '部署超时（秒）' : 'Deployment timeout (seconds)'} hint="10–3600"><Input required min={10} max={3600} type="number" value={draft.deployment_timeout} onChange={(event) => setDraft((current) => ({ ...current, deployment_timeout: Number(event.target.value) }))} /></Field>
      </div>
      <label className="flex max-w-xl cursor-pointer items-start gap-2">
        <Checkbox checked={draft.auto_rollback} onCheckedChange={(checked) => setDraft((current) => ({ ...current, auto_rollback: !!checked }))} className="mt-0.5" />
        <span className="text-sm">{zh ? '部署失败时自动回滚' : 'Automatically roll back failed deployments'}<span className="mt-0.5 block text-xs text-muted-foreground">{zh ? '仅回到上一个成功的 Compose Release，不会回滚存储卷中的数据。' : 'Restores only the previous successful Compose release; volume data is never rolled back.'}</span></span>
      </label>
    </SettingsSection>

    <SettingsSection title="Webhook" description={zh ? 'Webhook 只触发安全的 Git 拉取；Commit 和配置始终由 SUMA 从仓库读取。轮询仍作为兜底。' : 'Webhooks only trigger a safe Git fetch. SUMA always reads commits and configuration from the repository; polling remains the fallback.'}>
      <label className="flex w-fit cursor-pointer items-center gap-2">
        <Checkbox checked={draft.webhook_enabled} onCheckedChange={(checked) => setDraft((current) => ({ ...current, webhook_enabled: !!checked }))} />
        <span className="text-sm">{zh ? '启用仓库 Webhook' : 'Enable repository webhook'}</span>
      </label>
      {draft.webhook_enabled && <Card size="sm" className="w-full"><CardContent className="flex flex-col gap-4">
        {webhookURL && <Field label="Webhook URL"><Input readOnly value={webhookURL} /></Field>}
        <Field label={zh ? 'Webhook Secret' : 'Webhook secret'} hint={configuration.webhook_id && !configuration.webhook_secret ? (zh ? '留空以保留已保存的 Secret；输入新值可轮换。' : 'Leave empty to keep the stored secret, or enter a new value to rotate it.') : (zh ? '留空时 SUMA 会生成随机 Secret，并只显示一次。' : 'Leave empty and SUMA generates a random secret that is shown once.')}>
          <Input type="password" autoComplete="new-password" value={draft.webhook_secret} onChange={(event) => setDraft((current) => ({ ...current, webhook_secret: event.target.value }))} placeholder={configuration.webhook_id ? '••••••••••••••••' : (zh ? '自动生成' : 'Generate automatically')} />
        </Field>
        {revealedWebhookSecret && <Alert variant="destructive">
          <AlertTitle>{zh ? '请立即保存新 Secret' : 'Save the new secret now'}</AlertTitle>
          <AlertDescription>{zh ? '离开此页后不会再次显示。' : 'It will not be shown again after leaving this page.'}
            <div className="mt-2 flex items-center gap-2">
              <Input readOnly value={revealedWebhookSecret} />
              <Button variant="outline" size="sm" className="shrink-0" onClick={() => { void navigator.clipboard.writeText(revealedWebhookSecret); setSecretCopied(true) }}><Copy />{secretCopied ? (zh ? '已复制' : 'Copied') : (zh ? '复制' : 'Copy')}</Button>
            </div>
          </AlertDescription>
        </Alert>}
        <p className="text-xs text-muted-foreground">{zh ? '支持 GitHub、GitLab 原生 Push Webhook，也支持 Authorization: Bearer <secret> 的通用 JSON Webhook。' : 'Accepts native GitHub and GitLab push webhooks plus a generic JSON webhook using Authorization: Bearer <secret>.'}</p>
      </CardContent></Card>}
    </SettingsSection>

    <div className="flex flex-wrap items-center gap-3">
      {notice && <p className={cn('text-sm', save.isError ? 'text-destructive' : 'text-muted-foreground')}>{notice}</p>}
      <Button type="submit" disabled={save.isPending} className={save.isError ? '' : !notice ? 'ml-auto' : ''}>{save.isPending && <Spinner />}<Save data-icon="inline-end" />{!configuration.configured ? (zh ? '连接并保存' : 'Connect and save') : (zh ? '保存 CD 配置' : 'Save CD settings')}</Button>
    </div>
  </form>
}

function SettingsSection({ title, description, children }: { title: string; description: string; children: ReactNode }) {
  return <section className="flex w-full flex-col gap-4">
    <header className="flex flex-col gap-0.5">
      <h3 className="text-sm font-semibold">{title}</h3>
      <p className="max-w-2xl text-xs leading-relaxed text-muted-foreground">{description}</p>
    </header>
    <div className="flex flex-col gap-4">{children}</div>
  </section>
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

const emptyCredential = (): GitCredentialInput => ({ name: '', auth_type: 'http_token', username: '', secret: '', private_key: '', passphrase: '', known_hosts: '', custom_ca: '' })

function CredentialSelector({ zh, nodeIDs, value, onChange }: { zh: boolean; nodeIDs: string[]; value: GitAuthentication; onChange: (value: GitAuthentication) => void }) {
  const query = useQuery({ queryKey: ['git-credentials'], queryFn: () => api<GitCredential[]>('/credentials/git') })
  const available = (query.data || []).filter((row) => nodeIDs.every((id) => row.authorized_node_ids?.includes(id)))
  const input = value.credential || { ...emptyCredential(), ...value.summary }
  const set = <K extends keyof GitCredentialInput>(key: K, next: GitCredentialInput[K]) => onChange({ source: 'project', credential: { ...input, [key]: next } })
  const selectSource = (source: GitAuthentication['source']) => onChange(
    source === 'center'
      ? { source, credential_id: available[0]?.id }
      : source === 'project'
        ? (value.source === 'project' ? value : { source, credential: emptyCredential() })
        : { source },
  )
  return <div className="flex w-full flex-col gap-4">
    <Field label={zh ? '认证来源' : 'Authentication source'} hint={value.source === 'none' ? (zh ? '公开仓库无需提供凭据' : 'Public repositories need no credential') : undefined}>
      <Select value={value.source} onValueChange={(next) => selectSource(next as GitAuthentication['source'])}>
        <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
        <SelectContent>
          <SelectItem value="none">{zh ? '无需认证（公开仓库）' : 'No authentication (public repository)'}</SelectItem>
          <SelectItem value="center">{zh ? '从认证中心选择' : 'Choose from Authentication Center'}</SelectItem>
          <SelectItem value="project">{zh ? '使用当前项目凭据' : 'Use a project credential'}</SelectItem>
        </SelectContent>
      </Select>
    </Field>
    {value.source === 'center' && <Field label={zh ? 'Git 凭据' : 'Git credential'}>
      <Select value={value.credential_id ? String(value.credential_id) : undefined} onValueChange={(credentialID) => onChange({ source: 'center', credential_id: Number(credentialID) })} disabled={query.isPending}>
        <SelectTrigger className="w-full"><SelectValue placeholder={query.isPending ? (zh ? '正在加载…' : 'Loading…') : (zh ? '选择已授权的 Git 凭据' : 'Choose an authorized Git credential')} /></SelectTrigger>
        <SelectContent>
          {available.map((row) => <SelectItem key={row.id} value={String(row.id)}>{`${row.name} · ${authLabel(row.auth_type, zh)}`}</SelectItem>)}
        </SelectContent>
      </Select>
    </Field>}
    {value.source === 'project' && <Card size="sm" className="w-full"><CardContent className="flex flex-col gap-4">
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label={zh ? '名称' : 'Name'}><Input value={input.name} onChange={(event) => set('name', event.target.value)} placeholder={zh ? '当前项目 Git 凭据' : 'Project Git credential'} /></Field>
        <Field label={zh ? '认证类型' : 'Authentication type'}>
          <Select value={input.auth_type} onValueChange={(next) => set('auth_type', next as GitAuthType)}>
            <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="http_token">HTTPS token</SelectItem>
              <SelectItem value="http_basic">HTTPS basic</SelectItem>
              <SelectItem value="ssh_key">SSH key</SelectItem>
            </SelectContent>
          </Select>
        </Field>
      </div>
      {(input.auth_type === 'http_token' || input.auth_type === 'http_basic') && <div className="grid gap-4 sm:grid-cols-2">
        <Field label={input.auth_type === 'http_token' ? (zh ? '用户名（可选）' : 'Username (optional)') : (zh ? '用户名' : 'Username')}><Input value={input.username} onChange={(event) => set('username', event.target.value)} /></Field>
        <Field label={input.auth_type === 'http_token' ? 'Token' : (zh ? '密码' : 'Password')} hint={zh ? '已配置时留空可保留原值' : 'Leave empty to keep an existing value'}><Input type="password" autoComplete="new-password" value={input.secret} onChange={(event) => set('secret', event.target.value)} /></Field>
      </div>}
      {input.auth_type === 'ssh_key' && <>
        <Field label={zh ? 'SSH 私钥' : 'SSH private key'}><Textarea rows={5} value={input.private_key} onChange={(event) => set('private_key', event.target.value)} placeholder="-----BEGIN OPENSSH PRIVATE KEY-----" /></Field>
        <Field label="known_hosts"><Textarea rows={4} value={input.known_hosts} onChange={(event) => set('known_hosts', event.target.value)} placeholder="git.example.com ssh-ed25519 AAAA…" /></Field>
        <Field label={zh ? '私钥密码（可选）' : 'Key passphrase (optional)'}><Input type="password" value={input.passphrase} onChange={(event) => set('passphrase', event.target.value)} /></Field>
      </>}
      <Field label={zh ? '自定义 CA（可选）' : 'Custom CA (optional)'}><Textarea rows={4} value={input.custom_ca} onChange={(event) => set('custom_ca', event.target.value)} placeholder="-----BEGIN CERTIFICATE-----" /></Field>
      {input.fingerprint && <p className="font-mono text-xs text-muted-foreground">{zh ? '已配置' : 'Configured'} · {input.fingerprint}</p>}
    </CardContent></Card>}
  </div>
}

function TargetSelector({ nodes, value, zh, onChange }: { nodes: DockerNode[]; value: string[]; zh: boolean; onChange: (value: string[]) => void }) {
  const enabled = nodes.filter((node) => node.enabled)
  const pagination = useListPagination(enabled)
  return <><div className="grid max-h-72 gap-2.5 overflow-y-auto overscroll-contain sm:grid-cols-2 xl:grid-cols-3">
    {pagination.items.map((node) => (
      <label key={node.id} className="flex cursor-pointer items-start gap-2">
        <Checkbox checked={value.includes(node.id)} onCheckedChange={() => onChange(value.includes(node.id) ? value.filter((id) => id !== node.id) : [...value, node.id])} className="mt-0.5" />
        <span className="flex min-w-0 flex-col">
          <span className="truncate text-sm">{node.name}</span>
          <span className="text-xs text-muted-foreground">{node.connection_type} · {node.status || 'unknown'}</span>
        </span>
      </label>
    ))}
  </div><ListPagination {...pagination} zh={zh} /></>
}

function RegistryCredentialSelector({ rows, value, zh, onChange }: { rows: { id: number; name: string; server_address: string }[]; value: number[]; zh: boolean; onChange: (value: number[]) => void }) {
  const pagination = useListPagination(rows)
  return <div className="flex flex-col gap-2">
    {pagination.items.map((row) => (
      <label key={row.id} className="flex cursor-pointer items-start gap-2">
        <Checkbox checked={value.includes(row.id)} onCheckedChange={() => onChange(value.includes(row.id) ? value.filter((id) => id !== row.id) : [...value, row.id])} className="mt-0.5" />
        <span className="flex min-w-0 flex-col">
          <span className="text-sm">{row.name}</span>
          <span className="text-xs text-muted-foreground">{row.server_address}</span>
        </span>
      </label>
    ))}
    {rows.length === 0 ? <p className="text-sm text-muted-foreground">{zh ? '没有可用于全部目标节点的镜像仓库凭据' : 'No registry credentials are available to every target node'}</p> : <ListPagination {...pagination} zh={zh} />}
  </div>
}

function authLabel(value: GitAuthType, zh: boolean) {
  if (value === 'http_token') return 'HTTPS token'
  if (value === 'http_basic') return zh ? 'HTTPS 用户名/密码' : 'HTTPS basic'
  if (value === 'ssh_key') return 'SSH key'
  return zh ? '无需认证' : 'No authentication'
}
