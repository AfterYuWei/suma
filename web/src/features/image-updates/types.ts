export interface ImageUsage { container_id: string; container_name: string; project?: string; service?: string; state: string; image_id: string; reference: string; delivery_project?: string }
export interface ImageUpdateResult { reference: string; registry?: string; platform: { os: string; architecture: string; variant?: string }; local_image_id: string; remote_manifest_digest?: string; remote_config_digest?: string; status: string; reason_code?: string; checked_at?: string; stale: boolean; pull_required: boolean; recreate_required: boolean; containers: ImageUsage[] }
export interface ImageUpdateView { results: ImageUpdateResult[]; running_task_id?: string }
export interface ImageUpdatePolicy { version: number; enabled: boolean; interval_hours: number; next_run_at?: string; registry_credentials: Record<string, number> }
export interface RegistryCredential { id: number; name: string; server_address: string; authorized_node_ids: string[] }
export interface CheckTarget { image_ids?: string[]; project_name?: string }
export const registryHost = (value: string) => ['index.docker.io', 'registry-1.docker.io'].includes(value.toLowerCase()) ? 'docker.io' : value.toLowerCase()
export function updateLabel(row: ImageUpdateResult, zh: boolean) {
 if (row.stale) return zh ? '检测已过期' : 'Check expired'
 if (row.recreate_required && !row.pull_required) return zh ? '已拉取，待重建' : 'Pulled · recreate pending'
 return ({ unchecked: zh ? '未检测' : 'Not checked', checking: zh ? '检测中' : 'Checking', current: zh ? '当前版本' : 'Current', update_available: zh ? '有更新' : 'Update available', pinned: zh ? 'Digest 固定' : 'Digest pinned', unavailable: zh ? '无法检测' : 'Unavailable' } as Record<string, string>)[row.status] || row.status
}
export function reasonLabel(reason: string | undefined, zh: boolean) {
 const labels: Record<string, string> = { untagged: '无镜像标签', image_id: '镜像 ID 无跟踪标签', invalid_reference: '镜像引用无效', digest_pinned: '固定摘要不跟踪标签更新', authentication_required: '需要仓库认证', credential_unavailable: '凭据不可用或节点未获授权', rate_limited: '仓库请求限流，请稍后重试', timeout: '查询超时', registry_unreachable: '控制端无法通过 HTTPS 访问仓库', reference_not_found: '仓库中不存在该引用', platform_unavailable: '仓库不支持当前镜像平台', unsupported_manifest: '不支持该镜像格式' }
 return zh ? labels[reason || ''] || reason || '' : (reason || '').replaceAll('_', ' ')
}
