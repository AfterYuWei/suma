import type { ReactNode } from 'react'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '../ui/sheet'
import { Spinner } from '../ui/spinner'
import { useNodeOverviewDetails } from '../../features/overview/use-node-overview'
import { overviewBytes } from '../../features/overview/observation'
import type { FleetNode } from '../../features/overview/types'
import type { DockerNode } from '../../lib/nodes'
import { useDateTime } from '../../lib/time-zone'

function InfoSection({ title, entries }: { title: string; entries: [string, ReactNode][] }) {
  return <section className="rounded-lg bg-surface p-3">
    <h4 className="mb-3 text-xs font-medium">{title}</h4>
    <dl className="grid grid-cols-[minmax(0,1fr)_minmax(0,1.7fr)] gap-x-3 gap-y-2 text-xs">
      {entries.map(([label, value]) => <div key={label} className="contents"><dt className="text-muted-foreground">{label}</dt><dd className="min-w-0 break-words text-right [overflow-wrap:anywhere]">{value == null || value === '' ? '—' : value}</dd></div>)}
    </dl>
  </section>
}

export function NodeInformationSheet({ metadata, node, zh, onClose }: { metadata: DockerNode; node: FleetNode; zh: boolean; onClose: () => void }) {
  const available = node.enabled && node.status === 'online'
  const { resources, disk } = useNodeOverviewDetails(metadata, available)
  const { formatDateTime } = useDateTime()
  const engineValue = (value: ReactNode) => available ? value : '—'
  const queryValue = (value: ReactNode, pending: boolean, failed: boolean, retry: () => void, label: string) => {
    if (!available) return '—'
    if (pending) return <span role="status" aria-label={label} className="inline-flex"><Spinner className="size-3.5" /></span>
    if (failed) return <button type="button" className="text-destructive underline underline-offset-4" aria-label={`${zh ? '重试：' : 'Retry: '}${label}`} onClick={retry}>{zh ? '加载失败，重试' : 'Failed, retry'}</button>
    return value ?? '—'
  }
  return <Sheet open onOpenChange={open => { if (!open) onClose() }}>
    <SheetContent side="right" className="data-[side=right]:w-full data-[side=right]:sm:max-w-[480px]">
      <SheetHeader className="pr-12"><SheetTitle className="break-words [overflow-wrap:anywhere]">{node.name} · {zh ? '节点信息' : 'Node information'}</SheetTitle><SheetDescription>{zh ? '主机、连接与 Docker 资源' : 'Host, connection, and Docker resources'}</SheetDescription></SheetHeader>
      <div className="min-h-0 space-y-3 overflow-y-auto px-4 pb-4">
        <InfoSection title={zh ? '主机与 Engine' : 'Host and Engine'} entries={[
          [zh ? '主机名' : 'Hostname', engineValue(node.hostname)], [zh ? '系统' : 'OS', engineValue(node.os)],
          [zh ? '系统版本' : 'OS version', engineValue(node.os_version)], [zh ? '内核' : 'Kernel', engineValue(node.kernel_version)],
          [zh ? '架构' : 'Architecture', engineValue(node.architecture)], ['Engine', engineValue(node.engine_version)],
          ['CPU', engineValue(node.cpus > 0 ? `${node.cpus} vCPU` : '—')], [zh ? '内存容量' : 'Memory capacity', engineValue(node.memory_total_bytes > 0 ? overviewBytes(node.memory_total_bytes) : '—')],
          [zh ? '存储驱动' : 'Storage driver', engineValue(node.storage_driver)], [zh ? '日志驱动' : 'Logging driver', engineValue(node.logging_driver)],
          ['Cgroup', engineValue([node.cgroup_driver, node.cgroup_version].filter(Boolean).join(' · '))], ['Runtime', engineValue(node.default_runtime)],
          ['Live restore', engineValue(node.live_restore ? (zh ? '已启用' : 'Enabled') : (zh ? '未启用' : 'Disabled'))],
          [zh ? '安全选项' : 'Security options', engineValue(node.security_options?.join(' · '))],
        ]} />
        <InfoSection title={zh ? '连接信息' : 'Connection'} entries={[
          [zh ? '类型' : 'Type', node.connection_type.toUpperCase()], [zh ? '端点' : 'Endpoint', metadata.endpoint],
          [zh ? '传输安全' : 'Transport security', node.connection_type === 'unix' ? (zh ? '本地套接字' : 'Local socket') : node.connection_type === 'agent' ? 'HTTPS / WSS' : node.tls_mode === 'required' ? 'mTLS' : (zh ? '明文 TCP' : 'Plain TCP')],
          [zh ? '延迟' : 'Latency', available && node.last_latency_ms != null ? `${node.last_latency_ms} ms` : '—'],
          [zh ? '最后检查' : 'Last checked', node.last_checked_at ? formatDateTime(node.last_checked_at) : '—'],
        ]} />
        <InfoSection title={zh ? '资源数量与 Docker 占用' : 'Resources and Docker disk'} entries={[
          [zh ? '镜像' : 'Images', engineValue(node.images)],
          [zh ? '网络' : 'Networks', queryValue(resources.data?.networks, resources.isPending, resources.isError, () => void resources.refetch(), zh ? '资源数量' : 'Resource counts')],
          [zh ? '存储卷' : 'Volumes', queryValue(resources.data?.volumes, resources.isPending, resources.isError, () => void resources.refetch(), zh ? '资源数量' : 'Resource counts')],
          ['Docker disk', queryValue(disk.data ? overviewBytes(disk.data.docker_disk_usage_bytes) : null, disk.isPending, disk.isError, () => void disk.refetch(), zh ? '磁盘占用' : 'Disk usage')],
        ]} />
        <p className="text-xs text-muted-foreground">{zh ? '资源数量每 10 秒刷新，Docker 占用每 60 秒刷新。' : 'Resource counts refresh every 10 seconds; Docker disk every 60 seconds.'}</p>
      </div>
    </SheetContent>
  </Sheet>
}
