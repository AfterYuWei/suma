import { useQuery } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { useMemo } from 'react'
import type { ContainerSummary } from '../../features/containers/types'
import { api } from '../../lib/api'
import { useI18n } from '../../lib/i18n'
import { nodePath } from '../../lib/nodes'
import { confirmDialog } from '../../stores/dialog'
import { useUIStore } from '../../stores/ui'
import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from '../ui/command'

interface Project { name: string; backend: string }
interface Image { id: string; tags: string[] }
interface Result { id: string; label: string; detail: string; type: string; run: () => void }

export function CommandPalette({ open, close }: { open: boolean; close: () => void }) {
  const navigate = useNavigate()
  const { language } = useI18n()
  const zh = language === 'zh-CN'
  const nodeID = useUIStore((state) => state.currentNodeID)
  const containers = useQuery({ queryKey: ['containers', nodeID], queryFn: () => api<ContainerSummary[]>(nodePath(nodeID, '/containers')), enabled: open && !!nodeID })
  const projects = useQuery({ queryKey: ['projects', nodeID], queryFn: () => api<Project[]>(nodePath(nodeID, '/projects')), enabled: open && !!nodeID })
  const deliveries = useQuery({ queryKey: ['delivery-projects'], queryFn: () => api<Project[]>('/delivery-projects'), enabled: open })
  const images = useQuery({ queryKey: ['images', nodeID], queryFn: () => api<Image[]>(nodePath(nodeID, '/images')), enabled: open && !!nodeID })

  const groups = useMemo(() => {
    const byType = new Map<string, Result[]>()
    const push = (result: Result) => {
      const bucket = byType.get(result.type) ?? []
      bucket.push(result)
      byType.set(result.type, bucket)
    }

    ;[
      { id: 'containers', label: zh ? '打开容器' : 'Open containers', detail: zh ? '导航' : 'Navigation', type: zh ? '操作' : 'Actions', run: () => { close(); void navigate({ to: '/containers' }) } },
      { id: 'projects', label: zh ? '创建项目' : 'Create project', detail: zh ? '当前使用 Compose 后端' : 'Compose backend', type: zh ? '操作' : 'Actions', run: () => { close(); void navigate({ to: '/projects' }) } },
      { id: 'continuous-delivery', label: zh ? '打开持续交付' : 'Open continuous delivery', detail: zh ? 'Git 发布与回滚' : 'Git releases and rollback', type: zh ? '操作' : 'Actions', run: () => { close(); void navigate({ to: '/continuous-delivery' }) } },
      { id: 'pull', label: zh ? '拉取镜像' : 'Pull an image', detail: zh ? 'Docker 镜像' : 'Docker image', type: zh ? '操作' : 'Actions', run: () => { close(); void navigate({ to: '/images' }) } },
      { id: 'authentication', label: zh ? '打开认证中心' : 'Open Authentication Center', detail: zh ? 'Git 与镜像仓库凭据' : 'Git and registry credentials', type: zh ? '操作' : 'Actions', run: () => { close(); void navigate({ to: '/authentication' }) } },
    ].forEach(push)

    containers.data?.forEach((row) => {
      push({ id: `container-${row.id}`, label: row.name, detail: `${row.state} · ${row.image}`, type: zh ? '容器' : 'Containers', run: () => { close(); void navigate({ to: '/containers/$containerId', params: { containerId: row.id } }) } })
      push({ id: `logs-${row.id}`, label: zh ? `打开 ${row.name} 日志` : `Open ${row.name} logs`, detail: row.image, type: zh ? '容器操作' : 'Container actions', run: () => { close(); location.assign(`/containers/${row.id}#logs`) } })
      push({ id: `terminal-${row.id}`, label: zh ? `打开 ${row.name} 终端` : `Open ${row.name} terminal`, detail: row.image, type: zh ? '容器操作' : 'Container actions', run: () => { close(); location.assign(`/containers/${row.id}#terminal`) } })
      push({ id: `restart-${row.id}`, label: zh ? `重启 ${row.name}` : `Restart ${row.name}`, detail: zh ? '容器操作' : 'Container action', type: zh ? '容器操作' : 'Container actions', run: () => { close(); void api(nodePath(nodeID, `/containers/${row.id}/restart`), { method: 'POST' }) } })
      if (row.state === 'running') {
        push({
          id: `stop-${row.id}`,
          label: `${zh ? '停止' : 'Stop'} ${row.name}`,
          detail: zh ? '容器操作' : 'Container action',
          type: zh ? '容器操作' : 'Container actions',
          run: () => {
            void confirmDialog({ title: zh ? `停止 ${row.name}？` : `Stop ${row.name}?`, description: zh ? '容器将收到正常停止信号。' : 'The container will receive a graceful stop signal.', confirmLabel: zh ? '停止' : 'Stop' }).then((confirmed) => {
              if (confirmed) { close(); void api(nodePath(nodeID, `/containers/${row.id}/stop`), { method: 'POST' }) }
            })
          },
        })
      }
    })
    projects.data?.forEach((row) => push({ id: `project-${row.backend}-${row.name}`, label: row.name, detail: row.backend === 'compose' ? 'Compose Project' : 'Swarm Stack', type: zh ? '项目' : 'Projects', run: () => { close(); void navigate({ to: '/projects/$backend/$projectName', params: { backend: row.backend, projectName: row.name } }) } }))
    deliveries.data?.forEach((row) => push({ id: `delivery-${row.name}`, label: row.name, detail: zh ? '持续交付项目' : 'Continuous delivery project', type: zh ? '持续交付' : 'Continuous Delivery', run: () => { close(); void navigate({ to: '/continuous-delivery/$projectName', params: { projectName: row.name } }) } }))
    images.data?.forEach((row) => push({ id: `image-${row.id}`, label: row.tags?.[0] || row.id.slice(0, 19), detail: zh ? '本地镜像' : 'Local image', type: zh ? '镜像' : 'Images', run: () => { close(); void navigate({ to: '/images' }) } }))
    return [...byType.entries()]
  }, [containers.data, projects.data, deliveries.data, images.data, close, navigate, zh, nodeID])

  return <CommandDialog
    open={open}
    onOpenChange={(next) => { if (!next) close() }}
    title={zh ? '命令中心' : 'Command center'}
    description={zh ? '搜索容器、项目、镜像或操作' : 'Search containers, projects, images, or actions'}
  >
    <CommandInput placeholder={zh ? '搜索容器、项目、镜像或操作' : 'Search containers, projects, images, or actions'} />
    <CommandList>
      <CommandEmpty>{zh ? '没有匹配的命令或资源' : 'No matching command or resource'}</CommandEmpty>
      {groups.map(([type, results], index) => (
        <div key={type}>
          {index > 0 && <CommandSeparator />}
          <CommandGroup heading={`${type} · ${results.length}`}>
            {results.map((row) => (
              <CommandItem key={row.id} value={`${row.label} ${row.detail}`} onSelect={row.run} className="flex-col items-start gap-0">
                <span className="text-sm font-medium">{row.label}</span>
                <span className="text-xs text-muted-foreground">{row.detail}</span>
              </CommandItem>
            ))}
          </CommandGroup>
        </div>
      ))}
    </CommandList>
  </CommandDialog>
}
