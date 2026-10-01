export type Kind = 'container' | 'cache' | 'image' | 'network' | 'volume'
export interface Rule {
  enabled: boolean
  retention_days: number
}
export interface Config {
  enabled: boolean
  schedule: {
    frequency: 'daily' | 'weekly'
    weekday: number
    hour: number
    minute: number
    timezone: string
  }
  images: Rule & { include_tagged: boolean }
  cache: Rule & { reserved_bytes: number }
  containers: Rule
  networks: Rule
  scan_volumes: boolean
  protected: Partial<Record<Kind, string[]>>
}
export interface Policy extends Config {
  node_id: string
  version: number
  authorized_by?: number
  authorized_at?: string
  next_run_at?: string
}
export interface Resource {
  kind: Kind
  id: string
  name: string
  created_at: string
  last_used_at?: string
  size_bytes: number | null
  candidate: boolean
  manual: boolean
  reason: string
}
export interface Capabilities {
  available: boolean
  build_cache: boolean
  api_version?: string
  reason?: string
}
export interface Preview {
  id: string
  node_id: string
  policy_version: number
  generated_at: string
  expires_at: string
  resources: Resource[]
  capabilities: Capabilities
  image_layers_bytes: number | null
  cache_approximate: boolean
  policy: Policy
}
export interface Outcome {
  kind: Kind
  id: string
  name: string
  status: string
  reason?: string
  estimated_bytes?: number
}
export interface Stats {
  deleted: number
  skipped: number
  failed: number
  scanned: number
  reclaimed_bytes?: number
}
export interface Usage {
  image_layers_bytes: number | null
  container_bytes: number | null
  volume_bytes: number | null
}
export interface Run {
  id: string
  node_id: string
  node_name: string
  policy_version: number
  trigger: string
  task_id?: string
  status: string
  message: string
  scheduled_for?: string
  created_at: string
  started_at?: string
  finished_at?: string
  policy: Config
  result: {
    outcomes: Outcome[]
    stats: Partial<Record<Kind, Stats>>
    image_layers_before: number | null
    image_layers_after: number | null
    usage_before?: Usage
    usage_after?: Usage
  }
}
export interface View {
  policy: Policy
  capabilities: Capabilities
  next_runs: string[]
  latest_run: Run | null
  active_run: Run | null
}
export interface Summary {
  node_id: string
  node_name: string
  view: View
}
export interface RunPage {
  items: Run[]
  total: number
  page: number
}
export const kinds: Kind[] = [
  'container',
  'cache',
  'image',
  'network',
  'volume',
]
export const kindLabel = (kind: Kind, zh: boolean) =>
  ({
    container: zh ? '停止容器' : 'Stopped containers',
    cache: 'Engine BuildKit',
    image: zh ? '镜像' : 'Images',
    network: zh ? '网络' : 'Networks',
    volume: zh ? '卷（仅扫描）' : 'Volumes (scan only)',
  })[kind]
export const statusLabel = (status: string, zh: boolean) =>
  zh
    ? ((
        {
          pending: '等待执行',
          running: '执行中',
          success: '成功',
          failed: '失败',
          partial_failed: '部分失败',
          canceled: '已取消',
          interrupted: '重启中断',
          skipped: '已跳过',
          deleted: '已删除',
          scanned: '已扫描',
        } as Record<string, string>
      )[status] ?? status)
    : status.replaceAll('_', ' ')
export const reasonLabel = (reason: string, zh: boolean) =>
  (
    ({
      protection_label: zh ? '保护标签' : 'Protection label',
      system_resource: zh
        ? '系统资源／共享缓存'
        : 'System resource / shared cache',
      compose_project: zh ? 'Compose 项目资源' : 'Compose project resource',
      protected_reference: zh
        ? '保护名单或项目引用'
        : 'Protected identifier or project reference',
      in_use: zh ? '被容器引用或正在使用' : 'Referenced or in use',
      tagged_image: zh ? '保留带标签镜像' : 'Tagged image retained',
      not_exited: zh ? '不是已退出容器' : 'Container has not exited',
      unknown_age: zh ? '时间无法可靠判断' : 'Age unavailable',
      retention: zh ? '仍在保留期内' : 'Within retention',
      disabled: zh ? '未开启此项清理' : 'Cleanup disabled',
      unsupported: zh ? 'Engine 不支持' : 'Unsupported by Engine',
      manual_confirmation: zh
        ? '需要输入卷名确认'
        : 'Requires volume name confirmation',
      eligible: zh ? '符合清理规则' : 'Eligible',
      not_found: zh ? '资源已不存在' : 'Resource no longer exists',
    }) as Record<string, string>
  )[reason] ?? reason
export const bytes = (value: number | null | undefined, zh: boolean) => {
  if (value == null) return zh ? '不可获取' : 'Unavailable'
  if (value === 0) return '0 B'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  const index = Math.min(
    4,
    Math.floor(Math.log(Math.max(1, Math.abs(value))) / Math.log(1024)),
  )
  return `${(value / 1024 ** index).toFixed(index ? 1 : 0)} ${units[index]}`
}
export const configuration = (policy: Policy): Config => ({
  enabled: policy.enabled,
  schedule: { ...policy.schedule },
  images: { ...policy.images },
  cache: { ...policy.cache },
  containers: { ...policy.containers },
  networks: { ...policy.networks },
  scan_volumes: policy.scan_volumes,
  protected: structuredClone(policy.protected ?? {}),
})
export const expands = (old: Config, next: Config) => {
  for (const key of ['images', 'cache', 'containers', 'networks'] as const)
    if (
      next[key].enabled &&
      (!old[key].enabled || next[key].retention_days < old[key].retention_days)
    )
      return true
  if (
    next.images.enabled &&
    next.images.include_tagged &&
    !old.images.include_tagged
  )
    return true
  if (
    next.cache.enabled &&
    next.cache.reserved_bytes < old.cache.reserved_bytes
  )
    return true
  return Object.entries(old.protected ?? {}).some(([kind, values]) =>
    values?.some((value) => !next.protected[kind as Kind]?.includes(value)),
  )
}
export const scopeLabel = (config: Config, zh: boolean) =>
  [
    config.images.enabled &&
      (config.images.include_tagged
        ? zh
          ? '未引用镜像'
          : 'Unreferenced images'
        : zh
          ? '悬空镜像'
          : 'Dangling images'),
    config.cache.enabled && 'Engine BuildKit',
    config.containers.enabled && kindLabel('container', zh),
    config.networks.enabled && kindLabel('network', zh),
    config.scan_volumes && kindLabel('volume', zh),
  ]
    .filter(Boolean)
    .join(' · ') || (zh ? '未选择清理项' : 'No cleanup categories')
