import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Pencil, Plus, RefreshCw, Trash2 } from 'lucide-react'
import { cn } from '@/lib/utils'
import { LoadingState } from '../components/ui/loading-state'
import { Alert, AlertDescription } from '../components/ui/alert'
import { Badge } from '../components/ui/badge'
import { Button } from '../components/ui/button'
import { Checkbox } from '../components/ui/checkbox'
import { ListShell } from '../components/ui/list-shell'
import { Input } from '../components/ui/input'
import { Label } from '../components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../components/ui/select'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '../components/ui/sheet'
import { Spinner } from '../components/ui/spinner'
import { StatusBadge } from '../components/ui/status-badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'
import { TooltipHint } from '../components/ui/tooltip-hint'
import { api } from '../lib/api'
import { useI18n } from '../lib/i18n'
import type { DockerNode } from '../lib/nodes'
import { confirmDialog } from '../stores/dialog'
import { ResourceFrame } from './images'

interface TLSCredential { id: number; name: string; fingerprint: string; authorized_node_ids: string[] }
interface NodeInput { name: string; connection_type: 'unix' | 'tcp'; endpoint: string; tls_mode: 'required' | 'disabled'; tls_credential_id?: number; enabled: boolean }
type NodeFormValues = NodeInput

const blank = (): NodeFormValues => ({ name: '', connection_type: 'unix', endpoint: 'unix:///var/run/docker.sock', tls_mode: 'disabled', enabled: true })

const connectionLabels: Record<string, string> = { unix: 'Unix Socket', tcp: 'Docker TCP' }

export function NodesPage() {
  const { language } = useI18n()
  const zh = language === 'zh-CN'
  const client = useQueryClient()
  const query = useQuery({ queryKey: ['nodes'], queryFn: () => api<DockerNode[]>('/nodes'), refetchInterval: 15_000 })
  const credentials = useQuery({ queryKey: ['docker-tls-credentials'], queryFn: () => api<TLSCredential[]>('/credentials/docker-tls') })
  const [editing, setEditing] = useState<DockerNode | null>(null)
  const [values, setValues] = useState<NodeFormValues>(blank)
  const [open, setOpen] = useState(false)
  const save = useMutation({ mutationFn: (input: NodeInput) => api<DockerNode>(editing ? `/nodes/${editing.id}` : '/nodes', { method: editing ? 'PUT' : 'POST', body: JSON.stringify(input) }), onSuccess: async () => { setOpen(false); await client.invalidateQueries({ queryKey: ['nodes'] }) } })
  const test = useMutation({ mutationFn: (id: string) => api(`/nodes/${id}/test`, { method: 'POST' }), onSuccess: () => client.invalidateQueries({ queryKey: ['nodes'] }) })
  const edit = (node: DockerNode) => { setEditing(node); setValues({ name: node.name, connection_type: node.connection_type, endpoint: node.endpoint, tls_mode: node.tls_mode, tls_credential_id: node.tls_credential_id, enabled: node.enabled }); setOpen(true) }
  const remove = async (node: DockerNode) => { if (!await confirmDialog({ title: zh ? `删除节点 ${node.name}？` : `Delete node ${node.name}?`, description: zh ? '必须先解绑 Compose、CD 和全部凭据授权。历史任务和审计记录会保留。' : 'Compose, CD, and credential grants must be detached first. Historical tasks and audits remain.', confirmLabel: zh ? '删除节点' : 'Delete node', danger: true })) return; await api(`/nodes/${node.id}`, { method: 'DELETE' }); await client.invalidateQueries({ queryKey: ['nodes'] }) }
  const createNode = () => { setEditing(null); setValues(blank()); setOpen(true) }
  const update = (patch: Partial<NodeFormValues>) => setValues((previous) => ({ ...previous, ...patch }))
  const tcp = values.connection_type === 'tcp'

  return <ResourceFrame title={zh ? 'Docker 节点' : 'Docker nodes'} detail={zh ? '通过本地 Unix Socket 或受保护的 Docker TCP API 管理多个 Engine。' : 'Manage Docker Engines through local Unix sockets or protected Docker TCP APIs.'} action={<Button onClick={createNode}><Plus />{zh ? '添加节点' : 'Add node'}</Button>}>
    {query.isPending
      ? <LoadingState compact rows={4} label={zh ? '正在加载节点' : 'Loading nodes'} />
      : (
          <ListShell><Table>
            <TableHeader>
              <TableRow>
                <TableHead>{zh ? '节点' : 'Node'}</TableHead>
                <TableHead className="w-44">{zh ? '连接' : 'Connection'}</TableHead>
                <TableHead className="w-28">{zh ? '状态' : 'Status'}</TableHead>
                <TableHead className="w-28 text-right">{zh ? '操作' : 'Actions'}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {(query.data ?? []).length === 0 && (
                <TableRow><TableCell colSpan={4} className="h-24 text-center text-muted-foreground">{zh ? '暂无节点' : 'No nodes'}</TableCell></TableRow>
              )}
              {(query.data ?? []).map((node) => (
                <TableRow key={node.id}>
                  <TableCell className="max-w-80 whitespace-normal">
                    <div className="font-medium">{node.name}</div>
                    <TooltipHint content={node.endpoint}><span className="block truncate text-xs text-muted-foreground">{node.endpoint}</span></TooltipHint>
                    {node.last_error && <div className="mt-0.5 text-xs break-all text-destructive">{node.last_error}</div>}
                  </TableCell>
                  <TableCell>
                    <div className="flex items-center gap-1.5">
                      <Badge variant="outline" className="font-mono text-xs">{node.connection_type.toUpperCase()}</Badge>
                      <Badge variant="outline" className="text-xs">{node.tls_mode === 'required' ? 'mTLS' : 'PLAIN'}</Badge>
                    </div>
                  </TableCell>
                  <TableCell><StatusBadge tone={node.status === 'online' ? 'success' : 'neutral'}>{node.status}</StatusBadge></TableCell>
                  <TableCell>
                    <div className="flex items-center justify-end gap-0.5">
                      <TooltipHint content={zh ? '测试连接' : 'Test connection'}><Button
                        variant="ghost"
                        size="icon-sm"
                        aria-label={zh ? '测试连接' : 'Test connection'}
                        disabled={test.isPending && test.variables === node.id}
                        onClick={() => test.mutate(node.id)}
                      ><RefreshCw className={cn(test.isPending && test.variables === node.id && 'animate-spin')} /></Button></TooltipHint>
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
          </Table></ListShell>
        )}

    <Sheet open={open} onOpenChange={(next) => setOpen(next)} disablePointerDismissal>
      <SheetContent side="right" className="w-full sm:max-w-[520px]">
        <SheetHeader>
          <SheetTitle>{editing ? (zh ? '编辑节点' : 'Edit node') : (zh ? '添加节点' : 'Add node')}</SheetTitle>
          <SheetDescription>{zh ? '保存前会连接 Engine 并校验身份。' : 'The Engine identity is verified before saving.'}</SheetDescription>
        </SheetHeader>
        <form onSubmit={(event) => { event.preventDefault(); save.mutate(values) }} className="flex flex-1 flex-col gap-5 overflow-y-auto px-4 pb-4">
          <div className="grid gap-1.5">
            <Label htmlFor="node-name">{zh ? '节点名称' : 'Node name'}</Label>
            <Input id="node-name" required value={values.name} onChange={(event) => update({ name: event.target.value })} />
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="node-connection">{zh ? '连接方式' : 'Connection type'}</Label>
            <Select<'unix' | 'tcp'> value={values.connection_type} onValueChange={(next) => { if (next === null) return; const isTCP = next === 'tcp'; update({ connection_type: next, endpoint: isTCP ? 'tcp://docker.example.com:2376' : 'unix:///var/run/docker.sock', tls_mode: isTCP ? 'required' : 'disabled', tls_credential_id: undefined }) }}>
              <SelectTrigger id="node-connection" aria-label={zh ? '连接方式' : 'Connection type'} className="w-full"><SelectValue>{connectionLabels[values.connection_type]}</SelectValue></SelectTrigger>
              <SelectContent>
                <SelectItem value="unix">Unix Socket</SelectItem>
                <SelectItem value="tcp">Docker TCP</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="node-endpoint">Endpoint</Label>
            <Input id="node-endpoint" required value={values.endpoint} onChange={(event) => update({ endpoint: event.target.value })} />
          </div>
          {tcp && <>
            <div className="grid gap-1.5">
              <Label>TLS</Label>
              <Select<'required' | 'disabled'> value={values.tls_mode} onValueChange={(next) => { if (next !== null) update({ tls_mode: next }) }}>
                <SelectTrigger aria-label="TLS" className="w-full"><SelectValue>{values.tls_mode === 'required' ? `mTLS (${zh ? '推荐' : 'recommended'})` : (zh ? '无 TLS（仅回环地址）' : 'No TLS (loopback only)')}</SelectValue></SelectTrigger>
                <SelectContent>
                  <SelectItem value="required">{`mTLS (${zh ? '推荐' : 'recommended'})`}</SelectItem>
                  <SelectItem value="disabled">{zh ? '无 TLS（仅回环地址）' : 'No TLS (loopback only)'}</SelectItem>
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
          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={values.enabled} onCheckedChange={(checked) => update({ enabled: Boolean(checked) })} />
            {zh ? '启用节点' : 'Enable node'}
          </label>
          {save.isError && <Alert variant="destructive"><AlertDescription>{save.error.message}</AlertDescription></Alert>}
          <div className="mt-auto flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={() => setOpen(false)}>{zh ? '取消' : 'Cancel'}</Button>
            <Button type="submit" disabled={save.isPending}>{save.isPending && <Spinner className="size-4" />}{zh ? '保存节点' : 'Save node'}</Button>
          </div>
        </form>
      </SheetContent>
    </Sheet>
  </ResourceFrame>
}
