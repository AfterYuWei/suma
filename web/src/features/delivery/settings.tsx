import { useMutation, useQuery } from '@tanstack/react-query'
import { Copy, GitBranch, KeyRound, Plus, Save, Trash2, Webhook } from 'lucide-react'
import { type FormEvent, type ReactNode, useEffect, useRef, useState } from 'react'
import { api } from '../../lib/api'
import { choiceDialog } from '../../stores/dialog'
import { Select } from '../../components/ui/select'
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

const inputClass = 'h-9 w-full rounded-md border border-border bg-surface px-3 text-xs outline-none transition-colors focus:border-accent'
const textAreaClass = 'min-h-24 w-full resize-y rounded-md border border-border bg-surface px-3 py-2 font-mono text-[11px] leading-5 outline-none transition-colors focus:border-accent'

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

  const submit = async (event: FormEvent) => {
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

  return <form onSubmit={submit} className="max-w-5xl">
    {!configuration.configured && <div className="mb-7 flex gap-3 border-y border-border bg-surface/35 px-4 py-3">
      <GitBranch className="mt-0.5 size-4 shrink-0 text-accent" />
      <div><p className="text-sm font-medium">{zh ? '连接 Git 持续交付' : 'Connect Git continuous delivery'}</p><p className="mt-1 text-xs leading-5 text-text-muted">{zh ? '保存后 Git 将成为部署期望状态来源，DockPort 中的 Compose 文件会切换为只读。' : 'After saving, Git becomes the deployment source of truth and Compose files become read-only in DockPort.'}</p></div>
    </div>}

    <SettingsSection title={zh ? '目标节点' : 'Target nodes'} description={zh ? '一次审批后并行发布到全部选中节点；修改只影响后续 Release。' : 'One approval deploys to every selected node in parallel. Changes affect only future releases.'}>
      <TargetSelector nodes={nodes.data || []} value={draft.node_ids} onChange={(node_ids) => setDraft((current) => ({ ...current, node_ids }))} />
    </SettingsSection>

    <SettingsSection title={zh ? 'Git 仓库' : 'Git repository'} description={zh ? '支持任意标准 HTTPS 或 SSH Git 仓库，不区分代码托管平台。' : 'Works with any standard HTTPS or SSH Git repository without a hosting-provider setting.'}>
      <Field label="Git Clone URL" hint={zh ? '支持 HTTPS、ssh:// 或 git@host:path；认证信息请使用下方凭据，不要写入 URL。' : 'Supports HTTPS, ssh://, or git@host:path. Use a credential below instead of embedding secrets in the URL.'}>
        <input required value={draft.repository.clone_url} onChange={(event) => updateRepository('clone_url', event.target.value)} className={`${inputClass} font-mono`} placeholder="https://git.example.com/team/deploy.git" />
      </Field>
    </SettingsSection>

    <SettingsSection title={zh ? '版本与文件' : 'Revision and files'} description={zh ? 'DockPort 会检出精确 Commit，并按仓库根目录相对路径依次合并 Compose 文件。' : 'DockPort checks out an exact commit and merges repository-root-relative Compose files in order.'}>
      <div className="grid gap-4 sm:grid-cols-[150px_1fr]">
        <Field label={zh ? '引用类型' : 'Reference type'}><Select value={draft.repository.ref_type} onValueChange={(value) => updateRepository('ref_type', value)} options={[{ value: 'branch', label: 'Branch' }, { value: 'tag', label: 'Tag' }, { value: 'commit', label: 'Commit' }]} /></Field>
        <Field label={draft.repository.ref_type === 'commit' ? 'Commit SHA' : (zh ? '引用名称' : 'Reference')}><input required value={draft.repository.ref} onChange={(event) => updateRepository('ref', event.target.value)} className={`${inputClass} font-mono`} placeholder={draft.repository.ref_type === 'branch' ? 'main' : draft.repository.ref_type === 'tag' ? 'v1.0.0' : '40-character commit SHA'} /></Field>
      </div>
      <Field label={zh ? 'Compose 文件（仓库根目录相对路径）' : 'Compose files (relative to repository root)'} hint={zh ? '可位于不同目录；按从上到下的顺序合并，后面的文件覆盖前面的配置。' : 'Files may live in different directories and are merged top to bottom, with later files overriding earlier configuration.'}>
        <div className="space-y-2">{composeFiles.map((file, index) => <div key={index} className="flex gap-2"><input required value={file} onChange={(event) => setComposeFiles((current) => current.map((value, fileIndex) => fileIndex === index ? event.target.value : value))} className={`${inputClass} font-mono`} placeholder={index === 0 ? 'compose.yml' : 'environments/production.yml'} />{composeFiles.length > 1 && <button type="button" onClick={() => setComposeFiles((current) => current.filter((_, fileIndex) => fileIndex !== index))} className="grid size-9 shrink-0 place-items-center rounded-md border border-border text-text-subtle hover:border-danger/30 hover:bg-danger-subtle hover:text-danger" aria-label={zh ? '删除 Compose 文件' : 'Remove Compose file'}><Trash2 className="size-3.5" /></button>}</div>)}</div>
        <button type="button" onClick={() => setComposeFiles((current) => [...current, ''])} className="mt-2 inline-flex h-8 items-center gap-1.5 rounded-md border border-border bg-surface px-3 text-xs hover:bg-surface-hover"><Plus className="size-3.5" />{zh ? '添加 Compose 文件' : 'Add Compose file'}</button>
      </Field>
      <Field label={zh ? '环境变量文件（仓库根目录相对路径）' : 'Environment file (relative to repository root)'} hint={zh ? '可选，用于 Compose 中的 ${VARIABLE} 插值。' : 'Optional. Used for ${VARIABLE} interpolation in Compose files.'}><input value={draft.repository.environment_file} onChange={(event) => updateRepository('environment_file', event.target.value)} className={`${inputClass} font-mono`} placeholder="env/production.env" /></Field>
    </SettingsSection>

    <SettingsSection title={zh ? 'Git 认证' : 'Git authentication'} description={zh ? '凭据独立加密保存，可用于任何兼容的 HTTPS 或 SSH 仓库；公开仓库可以不选择。' : 'Credentials are encrypted separately and work with any compatible HTTPS or SSH repository. Public repositories need none.'}>
      <CredentialSelector zh={zh} nodeIDs={draft.node_ids} value={draft.repository.authentication} onChange={(value) => updateRepository('authentication', value)} />
    </SettingsSection>

    <SettingsSection title={zh ? '镜像仓库凭据' : 'Registry credentials'} description={zh ? '为发布显式选择凭据；只显示已授权给全部目标节点的凭据。' : 'Explicitly select credentials for deployment. Only credentials granted to every target are available.'}>
      <div className="space-y-2">{(registries.data || []).filter((row) => draft.node_ids.every((id) => row.authorized_node_ids.includes(id))).map((row) => <label key={row.id} className="flex items-center gap-3 rounded-md border border-border bg-surface px-3 py-2 text-xs"><input type="checkbox" checked={draft.registry_credential_ids.includes(row.id)} onChange={() => setDraft((current) => ({ ...current, registry_credential_ids: current.registry_credential_ids.includes(row.id) ? current.registry_credential_ids.filter((id) => id !== row.id) : [...current.registry_credential_ids, row.id] }))} /><span className="min-w-0 flex-1 truncate">{row.name}</span><span className="font-mono text-[9px] text-text-subtle">{row.server_address}</span></label>)}{registries.data?.length === 0 && <p className="text-xs text-text-subtle">{zh ? '认证中心暂无镜像仓库凭据。' : 'No registry credentials in the Authentication Center.'}</p>}</div>
    </SettingsSection>

    <SettingsSection title={zh ? '交付策略' : 'Delivery policy'} description={zh ? 'Observe 只生成待发布版本，Manual 需要人工发布，Auto 验证后自动发布。' : 'Observe records a candidate, Manual waits for an operator, and Auto deploys after validation.'}>
      <div className="grid gap-2 sm:grid-cols-3">
        {([
          ['observe', zh ? '观察' : 'Observe', zh ? '检测新提交但不自动应用' : 'Detect changes without applying'],
          ['manual', zh ? '手动' : 'Manual', zh ? '同步后由用户确认发布' : 'Require an explicit deploy action'],
          ['auto', zh ? '自动' : 'Auto', zh ? '验证通过后立即发布' : 'Deploy immediately after validation'],
        ] as [CDConfigureInput['reconcile_mode'], string, string][]).map(([value, label, detail]) => <button key={value} type="button" onClick={() => setDraft((current) => ({ ...current, reconcile_mode: value }))} className={`rounded-md border px-3 py-2 text-left ${draft.reconcile_mode === value ? 'border-accent bg-accent-subtle' : 'border-border bg-surface'}`}><span className="text-xs font-semibold">{label}</span><span className="mt-1 block text-[10px] text-text-subtle">{detail}</span></button>)}
      </div>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label={zh ? '轮询间隔（秒）' : 'Polling interval (seconds)'} hint="30–86400"><input required min={30} max={86400} type="number" value={draft.sync_interval_seconds} onChange={(event) => setDraft((current) => ({ ...current, sync_interval_seconds: Number(event.target.value) }))} className={inputClass} /></Field>
        <Field label={zh ? '部署超时（秒）' : 'Deployment timeout (seconds)'} hint="10–3600"><input required min={10} max={3600} type="number" value={draft.deployment_timeout} onChange={(event) => setDraft((current) => ({ ...current, deployment_timeout: Number(event.target.value) }))} className={inputClass} /></Field>
      </div>
      <label className="flex items-start gap-3 rounded-md border border-border bg-surface px-3 py-3"><input type="checkbox" checked={draft.auto_rollback} onChange={(event) => setDraft((current) => ({ ...current, auto_rollback: event.target.checked }))} className="mt-0.5 size-4 accent-[var(--accent)]" /><span><span className="block text-xs font-medium">{zh ? '部署失败时自动回滚' : 'Automatically roll back failed deployments'}</span><span className="mt-1 block text-[10px] leading-4 text-text-subtle">{zh ? '仅回到上一个成功的 Compose Release，不会回滚存储卷中的数据。' : 'Restores only the previous successful Compose release; volume data is never rolled back.'}</span></span></label>
    </SettingsSection>

    <SettingsSection title="Webhook" description={zh ? 'Webhook 只触发安全的 Git 拉取；Commit 和配置始终由 DockPort 从仓库读取。轮询仍作为兜底。' : 'Webhooks only trigger a safe Git fetch. DockPort always reads commits and configuration from the repository; polling remains the fallback.'}>
      <label className="flex items-center gap-3 text-xs"><input type="checkbox" checked={draft.webhook_enabled} onChange={(event) => setDraft((current) => ({ ...current, webhook_enabled: event.target.checked }))} className="size-4 accent-[var(--accent)]" /><Webhook className="size-3.5 text-text-subtle" />{zh ? '启用仓库 Webhook' : 'Enable repository webhook'}</label>
      {draft.webhook_enabled && <div className="space-y-4 rounded-md border border-border bg-surface/45 p-4">
        {webhookURL && <Field label="Webhook URL"><input readOnly value={webhookURL} className={`${inputClass} font-mono text-[10px] text-text-muted`} /></Field>}
        <Field label={zh ? 'Webhook Secret' : 'Webhook secret'} hint={configuration.webhook_id && !configuration.webhook_secret ? (zh ? '留空以保留已保存的 Secret；输入新值可轮换。' : 'Leave empty to keep the stored secret, or enter a new value to rotate it.') : (zh ? '留空时 DockPort 会生成随机 Secret，并只显示一次。' : 'Leave empty and DockPort generates a random secret that is shown once.')}><input type="password" autoComplete="new-password" value={draft.webhook_secret} onChange={(event) => setDraft((current) => ({ ...current, webhook_secret: event.target.value }))} className={`${inputClass} font-mono`} placeholder={configuration.webhook_id ? '••••••••••••••••' : (zh ? '自动生成' : 'Generate automatically')} /></Field>
        {revealedWebhookSecret && <div className="rounded-md border border-warning/25 bg-warning-subtle p-3"><p className="mb-2 text-[10px] leading-4 text-warning">{zh ? '这是新生成的 Secret。请立即保存到仓库的 Webhook 配置中；离开此页后不会再次显示。' : 'This secret was just generated. Store it in the repository webhook configuration now; it will not be shown again after leaving this page.'}</p><div className="flex gap-2"><input readOnly value={revealedWebhookSecret} className={`${inputClass} flex-1 bg-background font-mono text-[10px]`} /><button type="button" onClick={() => { void navigator.clipboard.writeText(revealedWebhookSecret); setSecretCopied(true) }} className="flex h-9 shrink-0 items-center gap-1.5 rounded-md border border-warning/30 bg-surface px-3 text-[10px] text-warning"><Copy className="size-3" />{secretCopied ? (zh ? '已复制' : 'Copied') : (zh ? '复制' : 'Copy')}</button></div></div>}
        <p className="text-[10px] leading-5 text-text-subtle">{zh ? '支持 GitHub、GitLab 原生 Push Webhook，也支持 Authorization: Bearer <secret> 的通用 JSON Webhook。' : 'Accepts native GitHub and GitLab push webhooks plus a generic JSON webhook using Authorization: Bearer <secret>.'}</p>
      </div>}
    </SettingsSection>

    <div className="flex flex-wrap items-center justify-end gap-3 border-t border-border pt-5">
      <span className={`mr-auto text-xs ${save.isError ? 'text-danger' : 'text-text-subtle'}`}>{notice}</span>
      <button disabled={save.isPending} className="flex h-9 items-center gap-2 rounded-md bg-accent px-4 text-xs font-semibold text-accent-foreground disabled:opacity-60"><Save className="size-3.5" />{save.isPending ? (zh ? '保存中…' : 'Saving…') : !configuration.configured ? (zh ? '连接并保存' : 'Connect and save') : (zh ? '保存 CD 配置' : 'Save CD settings')}</button>
    </div>
  </form>
}

function SettingsSection({ title, description, children }: { title: string; description: string; children: ReactNode }) {
  return <section className="grid gap-5 border-t border-border py-7 md:grid-cols-[190px_minmax(0,1fr)]"><div><h2 className="text-sm font-semibold">{title}</h2><p className="mt-1.5 text-[11px] leading-5 text-text-subtle">{description}</p></div><div className="space-y-4">{children}</div></section>
}

function Field({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return <label className="block"><span className="mb-1.5 flex items-end justify-between gap-3 text-xs text-text-muted"><span>{label}</span>{hint && <span className="text-right text-[9px] leading-4 text-text-subtle">{hint}</span>}</span>{children}</label>
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
  return <div className="space-y-4">
    <Field label={zh ? '认证来源' : 'Authentication source'} hint={value.source === 'none' ? (zh ? '公开仓库无需提供凭据' : 'Public repositories need no credential') : undefined}><Select value={value.source} onValueChange={selectSource} options={[{ value: 'none', label: zh ? '无需认证（公开仓库）' : 'No authentication (public repository)' }, { value: 'center', label: zh ? '从认证中心选择' : 'Choose from Authentication Center' }, { value: 'project', label: zh ? '使用当前项目凭据' : 'Use a project credential' }]} /></Field>
    {value.source === 'center' && <Field label={zh ? 'Git 凭据' : 'Git credential'}><div className="flex items-center gap-2"><KeyRound className="size-4 shrink-0 text-text-subtle" /><Select value={value.credential_id ? String(value.credential_id) : null} onValueChange={(credentialID) => onChange({ source: 'center', credential_id: Number(credentialID) })} placeholder={query.isPending ? (zh ? '正在加载…' : 'Loading…') : (zh ? '选择已授权的 Git 凭据' : 'Choose an authorized Git credential')} disabled={query.isPending} options={available.map((row) => ({ value: String(row.id), label: `${row.name} · ${authLabel(row.auth_type, zh)}` }))} /></div></Field>}
    {value.source === 'project' && <div className="space-y-4 rounded-md border border-border bg-surface/45 p-4">
      <div className="grid gap-4 sm:grid-cols-2"><Field label={zh ? '名称' : 'Name'}><input value={input.name} onChange={(event) => set('name', event.target.value)} className={inputClass} placeholder={zh ? '当前项目 Git 凭据' : 'Project Git credential'} /></Field><Field label={zh ? '认证类型' : 'Authentication type'}><Select value={input.auth_type} onValueChange={(authType) => set('auth_type', authType)} options={[{ value: 'http_token', label: 'HTTPS token' }, { value: 'http_basic', label: 'HTTPS basic' }, { value: 'ssh_key', label: 'SSH key' }]} /></Field></div>
      {(input.auth_type === 'http_token' || input.auth_type === 'http_basic') && <div className="grid gap-4 sm:grid-cols-2"><Field label={input.auth_type === 'http_token' ? (zh ? '用户名（可选）' : 'Username (optional)') : (zh ? '用户名' : 'Username')}><input value={input.username} onChange={(event) => set('username', event.target.value)} className={inputClass} /></Field><Field label={input.auth_type === 'http_token' ? 'Token' : (zh ? '密码' : 'Password')} hint={zh ? '已配置时留空可保留原值' : 'Leave empty to keep an existing value'}><input type="password" autoComplete="new-password" value={input.secret} onChange={(event) => set('secret', event.target.value)} className={inputClass} /></Field></div>}
      {input.auth_type === 'ssh_key' && <><Field label={zh ? 'SSH 私钥' : 'SSH private key'}><textarea value={input.private_key} onChange={(event) => set('private_key', event.target.value)} className={textAreaClass} placeholder="-----BEGIN OPENSSH PRIVATE KEY-----" /></Field><Field label="known_hosts"><textarea value={input.known_hosts} onChange={(event) => set('known_hosts', event.target.value)} className={textAreaClass} placeholder="git.example.com ssh-ed25519 AAAA…" /></Field><Field label={zh ? '私钥密码（可选）' : 'Key passphrase (optional)'}><input type="password" value={input.passphrase} onChange={(event) => set('passphrase', event.target.value)} className={inputClass} /></Field></>}
      <Field label={zh ? '自定义 CA（可选）' : 'Custom CA (optional)'}><textarea value={input.custom_ca} onChange={(event) => set('custom_ca', event.target.value)} className={textAreaClass} placeholder="-----BEGIN CERTIFICATE-----" /></Field>
      {input.fingerprint && <p className="font-mono text-[10px] text-text-subtle">{zh ? '已配置' : 'Configured'} · {input.fingerprint}</p>}
    </div>}
  </div>
}

function TargetSelector({ nodes, value, onChange }: { nodes: DockerNode[]; value: string[]; onChange: (value: string[]) => void }) {
  return <div className="space-y-2">{nodes.filter((node) => node.enabled).map((node) => <label key={node.id} className="flex items-center gap-3 rounded-md border border-border bg-surface px-3 py-2 text-xs"><input type="checkbox" checked={value.includes(node.id)} onChange={() => onChange(value.includes(node.id) ? value.filter((id) => id !== node.id) : [...value, node.id])} /><span className="min-w-0 flex-1 truncate">{node.name}</span><span className="font-mono text-[9px] text-text-subtle">{node.connection_type} · {node.status || 'unknown'}</span></label>)}</div>
}

function authLabel(value: GitAuthType, zh: boolean) {
  if (value === 'http_token') return 'HTTPS token'
  if (value === 'http_basic') return zh ? 'HTTPS 用户名/密码' : 'HTTPS basic'
  if (value === 'ssh_key') return 'SSH key'
  return zh ? '无需认证' : 'No authentication'
}
