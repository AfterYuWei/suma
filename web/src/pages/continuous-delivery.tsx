import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { GitPullRequest, Plus } from 'lucide-react'
import { useState } from 'react'
import { Button } from '../components/ui/button'
import { Checkbox } from '../components/ui/checkbox'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '../components/ui/dialog'
import { ErrorState } from '../components/ui/error-state'
import { Input } from '../components/ui/input'
import { Label } from '../components/ui/label'
import { ListShell } from '../components/ui/list-shell'
import { LoadingState } from '../components/ui/loading-state'
import { Spinner } from '../components/ui/spinner'
import { StatusBadge } from '../components/ui/status-badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'
import type { DeliveryProject } from '../features/delivery/types'
import { shortCommit } from '../features/delivery/types'
import { api } from '../lib/api'
import { useI18n } from '../lib/i18n'
import type { DockerNode } from '../lib/nodes'
import { useUIStore } from '../stores/ui'
import { ResourceFrame } from './images'

interface CreateValues { name: string; node_ids: string[] }

export function ContinuousDeliveryPage() {
  const client = useQueryClient()
  const navigate = useNavigate()
  const { language } = useI18n()
  const zh = language === 'zh-CN'
  const currentNodeID = useUIStore((state) => state.currentNodeID)
  const [createOpen, setCreateOpen] = useState(false)
  const [nameInput, setNameInput] = useState('')
  const [nodeIDsInput, setNodeIDsInput] = useState<string[]>([])
  const query = useQuery({ queryKey: ['delivery-projects'], queryFn: () => api<DeliveryProject[]>('/delivery-projects') })
  const nodes = useQuery({ queryKey: ['nodes'], queryFn: () => api<DockerNode[]>('/nodes') })
  const create = useMutation({ mutationFn: (input: CreateValues) => api<DeliveryProject>('/delivery-projects', { method: 'POST', body: JSON.stringify(input) }), onSuccess: async (project) => { setCreateOpen(false); await client.invalidateQueries({ queryKey: ['delivery-projects'] }); void navigate({ to: '/continuous-delivery/$projectName', params: { projectName: project.name } }) } })
  const rows = query.data ?? []
  const synchronized = rows.filter((project) => deliveryState(project) === 'synchronized').length
  const pending = rows.filter((project) => deliveryState(project) === 'pending').length
  const setup = rows.length - synchronized - pending
  const enabledNodes = (nodes.data || []).filter((node) => node.enabled)
  const initialNodeIDs = currentNodeID && enabledNodes.some((node) => node.id === currentNodeID) ? [currentNodeID] : enabledNodes[0] ? [enabledNodes[0].id] : []
  const canCreate = !!nameInput.trim() && nodeIDsInput.length > 0 && !create.isPending

  const openCreate = () => {
    setNameInput('')
    setNodeIDsInput(initialNodeIDs)
    setCreateOpen(true)
  }
  const toggleNode = (id: string) => setNodeIDsInput((current) => current.includes(id) ? current.filter((item) => item !== id) : [...current, id])
  const submitCreate = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!canCreate) return
    create.mutate({ name: nameInput.trim(), node_ids: nodeIDsInput })
  }

  return <ResourceFrame title={zh ? '持续交付' : 'Continuous Delivery'} detail={zh ? `${rows.length} 个交付项目` : `${rows.length} delivery projects`} action={<div className="flex flex-wrap items-center gap-2"><StatusBadge tone="success">{synchronized} {zh ? '已同步' : 'synced'}</StatusBadge><StatusBadge tone="warning">{pending} {zh ? '待对账' : 'pending'}</StatusBadge><StatusBadge tone="outline">{setup} {zh ? '待配置' : 'setup'}</StatusBadge><Button onClick={openCreate}><Plus data-icon="inline-start" />{zh ? '新建项目' : 'New project'}</Button></div>}>
    {query.isPending ? <LoadingState compact rows={7} label={zh ? '正在加载持续交付项目' : 'Loading delivery projects'} /> : query.isError ? <ErrorState description={query.error.message} /> : rows.length === 0 ? <div className="flex w-full flex-col items-center gap-1.5 rounded-xl border border-dashed py-12 text-center"><GitPullRequest className="size-6 text-muted-foreground" /><p className="text-sm font-medium">{zh ? '还没有持续交付项目' : 'No delivery projects yet'}</p><p className="max-w-md text-xs text-muted-foreground">{zh ? '直接在这里创建项目并连接 Git 仓库。' : 'Create a project here and connect its Git repository.'}</p></div> : (
      <ListShell><Table>
        <TableHeader>
          <TableRow>
            <TableHead className="min-w-[240px]">{zh ? '项目' : 'Project'}</TableHead>
            <TableHead className="w-[110px]">{zh ? '状态' : 'Status'}</TableHead>
            <TableHead className="w-[140px]">{zh ? '引用' : 'Reference'}</TableHead>
            <TableHead className="w-[100px]">{zh ? '期望版本' : 'Desired'}</TableHead>
            <TableHead className="w-[100px]">{zh ? '已观测' : 'Observed'}</TableHead>
            <TableHead className="w-[180px]">{zh ? '更新时间' : 'Updated'}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((project) => {
            const state = deliveryState(project)
            return <TableRow key={project.id}>
              <TableCell className="whitespace-normal">
                <a href={`/continuous-delivery/${encodeURIComponent(project.name)}`} className="font-medium text-primary underline-offset-4 hover:underline">{project.name}</a>
                <p className="mt-0.5 max-w-xs truncate text-xs text-muted-foreground">{project.configured ? project.repository_url || (zh ? '已连接 Git 仓库' : 'Git repository connected') : (zh ? '尚未连接 Git 仓库' : 'Git repository not configured')}</p>
              </TableCell>
              <TableCell><StateBadge state={state} zh={zh} /></TableCell>
              <TableCell className="text-muted-foreground">{project.git_ref || '—'}</TableCell>
              <TableCell className="font-mono text-xs">{shortCommit(project.desired_commit)}</TableCell>
              <TableCell className="font-mono text-xs">{shortCommit(project.observed_commit)}</TableCell>
              <TableCell className="text-sm text-muted-foreground">{new Date(String(project.updated_at)).toLocaleString(language)}</TableCell>
            </TableRow>
          })}
        </TableBody>
      </Table></ListShell>
    )}
    {create.isError && <ErrorState description={create.error.message} />}
    <Dialog open={createOpen} onOpenChange={(open) => setCreateOpen(open)}>
      {createOpen && <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{zh ? '新建交付项目' : 'New delivery project'}</DialogTitle>
        </DialogHeader>
        <form onSubmit={submitCreate}>
          <div className="flex flex-col gap-4">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="delivery-project-name">{zh ? '项目名称' : 'Project name'}</Label>
              <Input id="delivery-project-name" required value={nameInput} onChange={(event) => setNameInput(event.target.value)} placeholder={zh ? '例如 gateway-prod' : 'e.g. gateway-prod'} />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label>{zh ? '目标节点（可多选）' : 'Target nodes (multiple allowed)'}</Label>
              {enabledNodes.length === 0 ? <p className="text-xs text-muted-foreground">{zh ? '没有已启用的节点。' : 'No enabled nodes.'}</p> : (
                <div className="flex max-h-44 flex-col gap-2 overflow-y-auto rounded-lg border p-3">
                  {enabledNodes.map((node) => (
                    <label key={node.id} className="flex cursor-pointer items-center gap-2">
                      <Checkbox checked={nodeIDsInput.includes(node.id)} onCheckedChange={() => toggleNode(node.id)} />
                      <span className="text-sm">{`${node.name} · ${node.connection_type}`}</span>
                    </label>
                  ))}
                </div>
              )}
            </div>
          </div>
          <DialogFooter className="mt-4">
            <Button type="button" variant="outline" onClick={() => setCreateOpen(false)}>{zh ? '取消' : 'Cancel'}</Button>
            <Button type="submit" disabled={!canCreate}>{create.isPending && <Spinner />}{zh ? '创建' : 'Create'}</Button>
          </DialogFooter>
        </form>
      </DialogContent>}
    </Dialog>
  </ResourceFrame>
}

function StateBadge({ state, zh }: { state: string; zh: boolean }) {
  if (state === 'synchronized') return <StatusBadge tone="success">{zh ? '已同步' : 'Synchronized'}</StatusBadge>
  if (state === 'pending') return <StatusBadge tone="warning">{zh ? '待对账' : 'Pending'}</StatusBadge>
  return <StatusBadge tone="outline">{zh ? '待配置' : 'Setup'}</StatusBadge>
}

function deliveryState(project: DeliveryProject) { return !project.configured ? 'setup' : project.desired_commit && project.desired_commit === project.observed_commit ? 'synchronized' : 'pending' }
