import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Copy, FolderTree, Link2Off, Pencil, Plus, RefreshCw, Trash2 } from 'lucide-react'
import { cn } from '@/lib/utils'
import { LoadingState } from '../components/ui/loading-state'
import { Alert, AlertDescription } from '../components/ui/alert'
import { Badge } from '../components/ui/badge'
import { Button } from '../components/ui/button'
import { Checkbox } from '../components/ui/checkbox'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '../components/ui/dialog'
import { ListShell } from '../components/ui/list-shell'
import { ListPagination } from '../components/ui/list-pagination'
import { useListPagination } from '../components/ui/use-list-pagination'
import { Input } from '../components/ui/input'
import { Label } from '../components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../components/ui/select'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '../components/ui/sheet'
import { Spinner } from '../components/ui/spinner'
import { StatusBadge } from '../components/ui/status-badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'
import { Textarea } from '../components/ui/textarea'
import { TooltipHint } from '../components/ui/tooltip-hint'
import { api, ApiError } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { filterNodesByGroup, groupFilterID, type DockerNode, type NodeGroup } from '../lib/nodes'
import { confirmDialog, promptDialog } from '../stores/dialog'
import { useUIStore } from '../stores/ui'
import { ResourceFrame } from './images'

interface TLSCredential { id: number; name: string; fingerprint: string; authorized_node_ids: string[] }
interface NodeFormValues { name: string; connection_type: 'unix' | 'tcp' | 'agent'; endpoint: string; tls_mode: 'required' | 'disabled'; tls_credential_id?: number; enabled: boolean; group_ids: number[] }
interface NodeInput extends NodeFormValues { plaintext_confirmation?: string }
interface AgentEnrollment { node_id: string; token: string; expires_at: string; public_url: string }

const blank = (groupID?: number): NodeFormValues => ({ name: '', connection_type: 'unix', endpoint: 'unix:///var/run/docker.sock', tls_mode: 'disabled', enabled: true, group_ids: groupID ? [groupID] : [] })

const connectionLabels: Record<string, string> = { unix: 'Unix Socket', tcp: 'Docker TCP', agent: 'Agent (WSS)' }

const agentCompose = (publicURL: string) => `services:\n  suma-agent:\n    image: ghcr.io/afteryuwei/suma-agent:stable\n    restart: unless-stopped\n    environment:\n      SUMA_AGENT_SERVER_URL: ${publicURL}\n      SUMA_AGENT_TOKEN_FILE: /run/secrets/enrollment_token\n    volumes:\n      - /var/run/docker.sock:/var/run/docker.sock:ro\n      - /etc/suma-agent/enrollment-token:/run/secrets/enrollment_token:ro\n      - suma-agent-data:/var/lib/suma-agent\nvolumes:\n  suma-agent-data:\n`

export function NodesPage() {
  const { language } = useI18n()
  const zh = language === 'zh-CN'
  const agentURLHelp = zh ? '请通过 HTTPS 打开 SUMA；若已设置 SUMA_AGENT_PUBLIC_URL，请检查它是否为有效的 HTTPS 来源。' : 'Open SUMA over HTTPS. If SUMA_AGENT_PUBLIC_URL is set, check that it is a valid HTTPS origin.'
  const client = useQueryClient()
  const currentGroupFilter = useUIStore((state) => state.currentGroupFilter)
  const setCurrentGroupFilter = useUIStore((state) => state.setCurrentGroupFilter)
  const query = useQuery({ queryKey: ['nodes'], queryFn: () => api<DockerNode[]>('/nodes'), refetchInterval: 15_000 })
  const groups = useQuery({ queryKey: ['node-groups'], queryFn: () => api<NodeGroup[]>('/node-groups') })
  const credentials = useQuery({ queryKey: ['docker-tls-credentials'], queryFn: () => api<TLSCredential[]>('/credentials/docker-tls') })
  const [editing, setEditing] = useState<DockerNode | null>(null)
  const [values, setValues] = useState<NodeFormValues>(() => blank())
  const [open, setOpen] = useState(false)
  const [groupOpen, setGroupOpen] = useState(false)
  const [editingGroup, setEditingGroup] = useState<NodeGroup | null>(null)
  const [groupValues, setGroupValues] = useState({ name: '', description: '' })
  const [pairing, setPairing] = useState<AgentEnrollment | null>(null)
  const [agentActionError, setAgentActionError] = useState('')
  const save = useMutation({ mutationFn: (input: NodeInput) => api<DockerNode>(editing ? `/nodes/${editing.id}` : '/nodes', { method: editing ? 'PUT' : 'POST', body: JSON.stringify(input) }), onSuccess: async () => { setOpen(false); await Promise.all([client.invalidateQueries({ queryKey: ['nodes'] }), client.invalidateQueries({ queryKey: ['node-groups'] })]) } })
  const enroll = useMutation({ mutationFn: (input: { name: string; group_ids?: number[]; node_id?: string }) => api<AgentEnrollment>('/agent-enrollments', { method: 'POST', body: JSON.stringify(input) }), onSuccess: async (value) => { setOpen(false); setPairing(value); await client.invalidateQueries({ queryKey: ['nodes'] }) } })
  const saveGroup = useMutation({ mutationFn: (input: { name: string; description: string }) => api<NodeGroup>(editingGroup ? `/node-groups/${editingGroup.id}` : '/node-groups', { method: editingGroup ? 'PUT' : 'POST', body: JSON.stringify(input) }), onSuccess: async () => { setGroupOpen(false); await Promise.all([client.invalidateQueries({ queryKey: ['node-groups'] }), client.invalidateQueries({ queryKey: ['nodes'] })]) } })
  const test = useMutation({ mutationFn: (id: string) => api(`/nodes/${id}/test`, { method: 'POST' }), onSuccess: () => client.invalidateQueries({ queryKey: ['nodes'] }) })
  const edit = (node: DockerNode) => { setEditing(node); setValues({ name: node.name, connection_type: node.connection_type, endpoint: node.endpoint, tls_mode: node.tls_mode, tls_credential_id: node.tls_credential_id, enabled: node.enabled, group_ids: node.group_ids }); setOpen(true) }
  const remove = async (node: DockerNode) => { if (!await confirmDialog({ title: zh ? `删除节点 ${node.name}？` : `Delete node ${node.name}?`, description: zh ? '必须先解绑 Compose、CD 和全部凭据授权。历史任务和审计记录会保留。' : 'Compose, CD, and credential grants must be detached first. Historical tasks and audits remain.', confirmLabel: zh ? '删除节点' : 'Delete node', danger: true })) return; await api(`/nodes/${node.id}`, { method: 'DELETE' }); await client.invalidateQueries({ queryKey: ['nodes'] }) }
  const reissue = async (node: DockerNode) => { try { setAgentActionError(''); const value = await api<AgentEnrollment>(`/agent-enrollments/${node.id}/reissue`, { method: 'POST' }); setPairing(value) } catch (error) { setAgentActionError(error instanceof ApiError && error.code === 20501 ? agentURLHelp : error instanceof Error ? error.message : String(error)) } }
  const revoke = async (node: DockerNode) => { if (!await confirmDialog({ title: zh ? `撤销 ${node.name} 的 Agent 凭据？` : `Revoke Agent for ${node.name}?`, description: zh ? 'Agent 会立即断开。重新接入需要生成新的配对令牌。' : 'The Agent disconnects immediately. A new token is required to pair again.', confirmLabel: zh ? '撤销凭据' : 'Revoke credential', danger: true })) return; try { setAgentActionError(''); await api(`/nodes/${node.id}/agent/revoke`, { method: 'POST' }); await client.invalidateQueries({ queryKey: ['nodes'] }) } catch (error) { setAgentActionError(error instanceof Error ? error.message : String(error)) } }
  const submitNode = async () => {
    if (values.connection_type === 'agent' && editing?.connection_type !== 'agent') {
      enroll.mutate(editing ? { name: editing.name, node_id: editing.id } : { name: values.name, group_ids: values.group_ids })
      return
    }
    if (values.connection_type !== 'tcp' || values.tls_mode !== 'disabled') {
      save.mutate(values)
      return
    }
    let host = ''
    try { host = new URL(values.endpoint).hostname.replace(/^\[|\]$/g, '') } catch { /* The server returns the canonical endpoint validation error. */ }
    if (!host) {
      save.mutate(values)
      return
    }
    const confirmation = await promptDialog({
      title: zh ? '确认使用无 TLS Docker TCP？' : 'Confirm plaintext Docker TCP?',
      description: zh
        ? `任何能够访问 ${host} Docker API 端口的设备都可能取得宿主机的完整控制权。仅应在可信内网或 Tailscale 网络中使用。`
        : `Any device that can reach the Docker API port on ${host} may gain full control of the host. Use this only on a trusted private or Tailscale network.`,
      confirmLabel: zh ? '确认并保存' : 'Confirm and save',
      danger: true,
      input: { label: zh ? `再次输入 IP ${host} 以确认` : `Enter IP ${host} again to confirm`, requiredValue: host },
    })
    if (confirmation === host) save.mutate({ ...values, plaintext_confirmation: host })
  }
  const createNode = () => { setEditing(null); setValues(blank(groupFilterID(currentGroupFilter) ?? undefined)); setOpen(true) }
  const createGroup = () => { saveGroup.reset(); setEditingGroup(null); setGroupValues({ name: '', description: '' }); setGroupOpen(true) }
  const editGroup = (group: NodeGroup) => { saveGroup.reset(); setEditingGroup(group); setGroupValues({ name: group.name, description: group.description }); setGroupOpen(true) }
  const removeGroup = async (group: NodeGroup) => {
    if (!await confirmDialog({ title: zh ? `删除 Group ${group.name}？` : `Delete group ${group.name}?`, description: zh ? `只会解除 ${group.node_count} 个节点的归属，不会删除节点或改变当前 Docker 上下文。` : `This only detaches ${group.node_count} nodes. It does not delete nodes or change the current Docker context.`, confirmLabel: zh ? '删除 Group' : 'Delete group', danger: true })) return
    await api(`/node-groups/${group.id}`, { method: 'DELETE' })
    if (currentGroupFilter === `group:${group.id}`) setCurrentGroupFilter('all')
    await Promise.all([client.invalidateQueries({ queryKey: ['node-groups'] }), client.invalidateQueries({ queryKey: ['nodes'] })])
  }
  const update = (patch: Partial<NodeFormValues>) => setValues((previous) => ({ ...previous, ...patch }))
  const tcp = values.connection_type === 'tcp'
  const filteredNodes = filterNodesByGroup(query.data ?? [], currentGroupFilter)
  const pagination = useListPagination(filteredNodes)

  return <ResourceFrame title={zh ? 'Docker 节点' : 'Docker nodes'} detail={zh ? '通过 Group 组织节点；Docker 操作仍始终绑定明确的单个节点。' : 'Organize nodes with groups while every Docker operation remains bound to one explicit node.'} action={<Button onClick={createNode}><Plus />{zh ? '添加节点' : 'Add node'}</Button>}>
    <section className="mb-4 flex flex-col gap-3 rounded-xl border p-3" aria-labelledby="node-groups-heading">
      <div className="flex flex-wrap items-center gap-2">
        <div className="mr-auto flex min-w-0 items-center gap-2">
          <FolderTree className="size-4 text-muted-foreground" />
          <div><h2 id="node-groups-heading" className="text-sm font-medium">{zh ? '节点 Group' : 'Node groups'}</h2><p className="text-xs text-muted-foreground">{zh ? 'Group 只筛选节点，不会切换当前 Docker 上下文。' : 'Groups filter nodes without switching the current Docker context.'}</p></div>
        </div>
        <Button variant="outline" size="sm" onClick={createGroup}><Plus />{zh ? '新建 Group' : 'New group'}</Button>
      </div>
      <div className="flex flex-wrap gap-1.5">
        <Button variant={currentGroupFilter === 'all' ? 'secondary' : 'ghost'} size="sm" onClick={() => setCurrentGroupFilter('all')}>{zh ? '全部' : 'All'} · {(query.data ?? []).length}</Button>
        {(groups.data ?? []).map((group) => <div key={group.id} className="flex items-center rounded-lg border bg-muted/20">
          <Button variant={currentGroupFilter === `group:${group.id}` ? 'secondary' : 'ghost'} size="sm" className="rounded-r-none" onClick={() => setCurrentGroupFilter(`group:${group.id}`)}>{group.name} · {group.node_count}</Button>
          <TooltipHint content={zh ? '编辑 Group' : 'Edit group'}><Button variant="ghost" size="icon-sm" className="rounded-none" aria-label={zh ? '编辑 Group' : 'Edit group'} onClick={() => editGroup(group)}><Pencil /></Button></TooltipHint>
          <TooltipHint content={zh ? '删除 Group' : 'Delete group'}><Button variant="ghost" size="icon-sm" className="rounded-l-none text-destructive" aria-label={zh ? '删除 Group' : 'Delete group'} onClick={() => void removeGroup(group)}><Trash2 /></Button></TooltipHint>
        </div>)}
      </div>
    </section>
    {agentActionError && <Alert variant="destructive" className="mb-4"><AlertDescription>{agentActionError}</AlertDescription></Alert>}
    {query.isPending
      ? <LoadingState compact rows={4} label={zh ? '正在加载节点' : 'Loading nodes'} />
      : (
          <><ListShell><Table>
            <TableHeader>
              <TableRow>
                <TableHead>{zh ? '节点' : 'Node'}</TableHead>
                <TableHead className="w-44">{zh ? '连接' : 'Connection'}</TableHead>
                <TableHead className="min-w-40">Group</TableHead>
                <TableHead className="w-28">{zh ? '状态' : 'Status'}</TableHead>
                <TableHead className="w-28 text-right">{zh ? '操作' : 'Actions'}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {filteredNodes.length === 0 && (
                <TableRow><TableCell colSpan={5} className="h-24 text-center text-muted-foreground">{zh ? '当前 Group 筛选下暂无节点' : 'No nodes match the current group filter'}</TableCell></TableRow>
              )}
              {pagination.items.map((node) => (
                <TableRow key={node.id}>
                  <TableCell className="max-w-80 whitespace-normal">
                    <div className="font-medium">{node.name}</div>
                    <TooltipHint content={node.endpoint}><span className="block truncate text-xs text-muted-foreground">{node.connection_type === 'agent' ? (zh ? '主动连接到 SUMA' : 'Outbound connection to SUMA') : node.endpoint}{node.agent_version ? ` · ${node.agent_version}` : ''}</span></TooltipHint>
                    {node.last_error && <div className="mt-0.5 text-xs break-all text-destructive">{node.last_error}</div>}
                    {node.agent_enrollment && !node.agent_enrollment.consumed_at && <div className="mt-0.5 text-xs text-muted-foreground">{zh ? '等待 Agent 配对' : 'Waiting for Agent pairing'}</div>}
                    {node.agent_enrollment?.last_error && <div className="mt-0.5 text-xs break-all text-destructive">{node.agent_enrollment.last_error}</div>}
                  </TableCell>
                  <TableCell>
                    <div className="flex items-center gap-1.5">
                      <Badge variant="outline" className="font-mono text-xs">{node.connection_type.toUpperCase()}</Badge>
                      <Badge variant="outline" className="text-xs">{node.connection_type === 'agent' ? 'WSS' : node.tls_mode === 'required' ? 'mTLS' : 'PLAIN'}</Badge>
                    </div>
                  </TableCell>
                  <TableCell><div className="flex flex-wrap gap-1">{node.group_ids.length === 0 ? <span className="text-xs text-muted-foreground">{zh ? '无 Group' : 'No group'}</span> : node.group_ids.map((id) => { const group = groups.data?.find((item) => item.id === id); return <Badge key={id} variant="secondary" className="text-xs">{group?.name ?? `#${id}`}</Badge> })}</div></TableCell>
                  <TableCell><StatusBadge tone={node.status === 'online' ? 'success' : node.status === 'incompatible' || node.agent_enrollment?.last_error?.includes('protocol version is incompatible') ? 'critical' : node.status === 'pairing' ? 'warning' : 'neutral'}>{node.status === 'incompatible' || node.agent_enrollment?.last_error?.includes('protocol version is incompatible') ? (zh ? '版本不兼容' : 'Incompatible') : node.status === 'pairing' ? (zh ? '配对中' : 'Pairing') : node.status === 'online' ? (zh ? '在线' : 'Online') : node.status === 'offline' ? (zh ? '离线' : 'Offline') : node.status}</StatusBadge></TableCell>
                  <TableCell>
                    <div className="flex items-center justify-end gap-0.5">
                      <TooltipHint content={zh ? '测试连接' : 'Test connection'}><Button
                        variant="ghost"
                        size="icon-sm"
                        aria-label={zh ? '测试连接' : 'Test connection'}
                        disabled={test.isPending && test.variables === node.id}
                        onClick={() => test.mutate(node.id)}
                      ><RefreshCw className={cn(test.isPending && test.variables === node.id && 'animate-spin')} /></Button></TooltipHint>
                      {(node.connection_type === 'agent' || node.agent_enrollment) && <><TooltipHint content={zh ? '重新配对' : 'Pair again'}><Button variant="ghost" size="icon-sm" aria-label={zh ? '重新配对' : 'Pair again'} onClick={() => void reissue(node)}><Copy /></Button></TooltipHint>{node.connection_type === 'agent' && <TooltipHint content={zh ? '撤销 Agent' : 'Revoke Agent'}><Button variant="ghost" size="icon-sm" aria-label={zh ? '撤销 Agent' : 'Revoke Agent'} onClick={() => void revoke(node)}><Link2Off /></Button></TooltipHint>}</>}
                      <TooltipHint content={zh ? '编辑' : 'Edit'}><Button variant="ghost" size="icon-sm" aria-label={zh ? '编辑' : 'Edit'} onClick={() => edit(node)}><Pencil /></Button></TooltipHint>
                      <TooltipHint content={zh ? '删除' : 'Delete'}><Button
                        variant="destructive"
                        size="icon-sm"
                        disabled={node.id === 'local'}
                        aria-label={zh ? '删除' : 'Delete'}
                        onClick={() => void remove(node)}
                      ><Trash2 /></Button></TooltipHint>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table></ListShell><ListPagination {...pagination} zh={zh} /></>
        )}

    <Dialog open={groupOpen} onOpenChange={setGroupOpen}>
      {groupOpen && <DialogContent className="sm:max-w-md">
        <DialogHeader><DialogTitle>{editingGroup ? (zh ? '编辑 Group' : 'Edit group') : (zh ? '新建 Group' : 'New group')}</DialogTitle></DialogHeader>
        <form onSubmit={(event) => { event.preventDefault(); saveGroup.mutate(groupValues) }} className="flex flex-col gap-4">
          <div className="grid gap-1.5"><Label htmlFor="group-name">{zh ? '名称' : 'Name'}</Label><Input id="group-name" required maxLength={128} value={groupValues.name} onChange={(event) => setGroupValues((current) => ({ ...current, name: event.target.value }))} /></div>
          <div className="grid gap-1.5"><Label htmlFor="group-description">{zh ? '描述（可选）' : 'Description (optional)'}</Label><Textarea id="group-description" maxLength={512} rows={4} value={groupValues.description} onChange={(event) => setGroupValues((current) => ({ ...current, description: event.target.value }))} /></div>
          {saveGroup.isError && <Alert variant="destructive"><AlertDescription>{saveGroup.error.message}</AlertDescription></Alert>}
          <DialogFooter><Button type="button" variant="outline" onClick={() => setGroupOpen(false)}>{zh ? '取消' : 'Cancel'}</Button><Button type="submit" disabled={saveGroup.isPending}>{saveGroup.isPending && <Spinner />}{zh ? '保存 Group' : 'Save group'}</Button></DialogFooter>
        </form>
      </DialogContent>}
    </Dialog>

    <Sheet open={open} onOpenChange={(next) => setOpen(next)} disablePointerDismissal>
      <SheetContent side="right" className="w-full sm:max-w-[520px]">
        <SheetHeader>
          <SheetTitle>{editing ? (zh ? '编辑节点' : 'Edit node') : (zh ? '添加节点' : 'Add node')}</SheetTitle>
          <SheetDescription>{values.connection_type === 'agent' ? (zh ? '配对成功后校验 Engine 身份。' : 'Engine identity is verified when the Agent pairs.') : (zh ? '保存前会连接 Engine 并校验身份。' : 'The Engine identity is verified before saving.')}</SheetDescription>
        </SheetHeader>
        <form onSubmit={(event) => { event.preventDefault(); void submitNode() }} className="flex flex-1 flex-col gap-5 overflow-y-auto px-4 pb-4">
          <div className="grid gap-1.5">
            <Label htmlFor="node-name">{zh ? '节点名称' : 'Node name'}</Label>
            <Input id="node-name" required value={values.name} onChange={(event) => update({ name: event.target.value })} />
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="node-connection">{zh ? '连接方式' : 'Connection type'}</Label>
            <Select<'unix' | 'tcp' | 'agent'> value={values.connection_type} onValueChange={(next) => { if (next === null) return; const isTCP = next === 'tcp'; update({ connection_type: next, endpoint: next === 'agent' ? (editing?.connection_type === 'agent' ? editing.endpoint : '') : isTCP ? 'tcp://docker.example.com:2376' : 'unix:///var/run/docker.sock', tls_mode: isTCP ? 'required' : 'disabled', tls_credential_id: undefined }) }}>
              <SelectTrigger id="node-connection" aria-label={zh ? '连接方式' : 'Connection type'} className="w-full"><SelectValue>{connectionLabels[values.connection_type]}</SelectValue></SelectTrigger>
              <SelectContent>
                <SelectItem value="unix">Unix Socket</SelectItem>
                <SelectItem value="tcp">Docker TCP</SelectItem>
                <SelectItem value="agent">Agent (WSS)</SelectItem>
              </SelectContent>
            </Select>
          </div>
          {values.connection_type !== 'agent' && <div className="grid gap-1.5">
            <Label htmlFor="node-endpoint">Endpoint</Label>
            <Input id="node-endpoint" required value={values.endpoint} onChange={(event) => update({ endpoint: event.target.value })} />
          </div>}
          {values.connection_type === 'agent' && <p className="text-sm text-muted-foreground">{zh ? 'Agent 主动通过 HTTPS/WSS 连接；保存后生成一次性配对令牌。' : 'The Agent connects outbound over HTTPS/WSS. Saving generates a one-time pairing token.'}</p>}
          {tcp && <>
            <div className="grid gap-1.5">
              <Label>TLS</Label>
              <Select<'required' | 'disabled'> value={values.tls_mode} onValueChange={(next) => { if (next !== null) update({ tls_mode: next }) }}>
                <SelectTrigger aria-label="TLS" className="w-full"><SelectValue>{values.tls_mode === 'required' ? `mTLS (${zh ? '推荐' : 'recommended'})` : (zh ? '无 TLS（内网或 Tailscale）' : 'No TLS (private network or Tailscale)')}</SelectValue></SelectTrigger>
                <SelectContent>
                  <SelectItem value="required">{`mTLS (${zh ? '推荐' : 'recommended'})`}</SelectItem>
                  <SelectItem value="disabled">{zh ? '无 TLS（内网或 Tailscale）' : 'No TLS (private network or Tailscale)'}</SelectItem>
                </SelectContent>
              </Select>
            </div>
            {values.tls_mode === 'required' && (
              <div className="grid gap-1.5">
                <Label htmlFor="node-tls-credential">{zh ? 'Docker TLS 凭据' : 'Docker TLS credential'}</Label>
                <Select<number> value={values.tls_credential_id ?? null} onValueChange={(next) => update({ tls_credential_id: next === null ? undefined : next })}>
                  <SelectTrigger id="node-tls-credential" className="w-full"><SelectValue>{(selected: number | null) => selected == null ? (zh ? '选择凭据' : 'Choose credential') : (() => { const credential = credentials.data?.find((item) => item.id === selected); return credential ? `${credential.name} · ${credential.fingerprint}` : String(selected) })()}</SelectValue></SelectTrigger>
                  <SelectContent>
                    {(credentials.data ?? []).map((credential) => (
                      <SelectItem key={credential.id} value={credential.id}>{`${credential.name} · ${credential.fingerprint}`}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            )}
          </>}
          <div className="grid gap-2">
            <Label>{zh ? '所属 Group（可多选）' : 'Groups (multiple allowed)'}</Label>
            <div className="flex max-h-40 flex-col gap-2 overflow-y-auto rounded-lg border p-3">
              {(groups.data ?? []).length === 0 ? <p className="text-xs text-muted-foreground">{zh ? '暂无 Group；节点将不属于任何 Group。' : 'No groups exist; the node will not belong to a Group.'}</p> : (groups.data ?? []).map((group) => <label key={group.id} className="flex cursor-pointer items-start gap-2">
                <Checkbox checked={values.group_ids.includes(group.id)} onCheckedChange={(checked) => update({ group_ids: checked ? [...values.group_ids, group.id] : values.group_ids.filter((id) => id !== group.id) })} className="mt-0.5" />
                <span className="min-w-0"><span className="block truncate text-sm">{group.name}</span>{group.description && <span className="block truncate text-xs text-muted-foreground">{group.description}</span>}</span>
              </label>)}
            </div>
          </div>
          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={values.enabled} onCheckedChange={(checked) => update({ enabled: Boolean(checked) })} />
            {zh ? '启用节点' : 'Enable node'}
          </label>
          {(save.isError || enroll.isError) && <Alert variant="destructive"><AlertDescription>{enroll.error instanceof ApiError && enroll.error.code === 20501 ? agentURLHelp : save.error?.message ?? enroll.error?.message}</AlertDescription></Alert>}
          <div className="mt-auto flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={() => setOpen(false)}>{zh ? '取消' : 'Cancel'}</Button>
            <Button type="submit" disabled={save.isPending || enroll.isPending}>{(save.isPending || enroll.isPending) && <Spinner className="size-4" />}{values.connection_type === 'agent' && editing?.connection_type !== 'agent' ? (zh ? '生成配对令牌' : 'Create pairing token') : (zh ? '保存节点' : 'Save node')}</Button>
          </div>
        </form>
      </SheetContent>
    </Sheet>
    <Sheet open={pairing !== null} onOpenChange={(next) => { if (!next) setPairing(null) }}>
      <SheetContent side="right" className="w-full sm:max-w-[620px]">
        <SheetHeader><SheetTitle>{zh ? 'Agent 配对' : 'Agent pairing'}</SheetTitle><SheetDescription>{zh ? '令牌只显示一次，有效期 10 分钟。先在 Agent 主机创建权限为 0600 的 /etc/suma-agent/enrollment-token，写入令牌，再运行 Compose。配对成功后清空文件内容，保留文件供容器重启挂载。' : 'The token is shown once and expires in 10 minutes. Put it in /etc/suma-agent/enrollment-token with mode 0600 on the Agent host, then run Compose. After pairing, empty the file but keep it for container restarts.'}</SheetDescription></SheetHeader>
        {pairing && <div className="flex flex-col gap-4 overflow-y-auto px-4 pb-4">
          <p className="text-xs text-muted-foreground">{zh ? `Agent 将连接 ${pairing.public_url}。请确认远端主机能访问此地址且信任其 HTTPS 证书。` : `The Agent will connect to ${pairing.public_url}. Check that the remote host can reach it and trusts its HTTPS certificate.`}</p>
          <div className="grid gap-1.5"><Label>{zh ? '节点 ID' : 'Node ID'}</Label><Input readOnly value={pairing.node_id} /></div>
          <div className="grid gap-1.5"><Label>{zh ? '一次性令牌' : 'One-time token'}</Label><div className="flex gap-2"><Input readOnly value={pairing.token} className="font-mono" /><Button type="button" variant="outline" onClick={() => void navigator.clipboard.writeText(pairing.token)}><Copy />{zh ? '复制' : 'Copy'}</Button></div></div>
          <div className="grid gap-1.5"><Label>docker-compose.yml</Label><pre className="overflow-x-auto rounded-lg border bg-muted/30 p-3 text-xs">{agentCompose(pairing.public_url)}</pre><Button type="button" variant="outline" onClick={() => void navigator.clipboard.writeText(agentCompose(pairing.public_url))}><Copy />{zh ? '复制 Compose' : 'Copy Compose'}</Button></div>
          <p className="text-xs text-muted-foreground">{zh ? 'Docker socket 即使只读挂载，Agent 仍拥有 Docker 管理权限。生产环境请固定镜像版本，并确保 SUMA 的 HTTPS 证书受信任。' : 'A read-only Docker socket mount still grants Docker control. Pin the image version and use a trusted HTTPS certificate in production.'}</p>
        </div>}
      </SheetContent>
    </Sheet>
  </ResourceFrame>
}
