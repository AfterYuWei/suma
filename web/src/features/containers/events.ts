import type { ContainerEvent } from './types'
export const eventLabels = (zh: boolean): Record<string, string> => ({ create: zh ? '创建' : 'Created', start: zh ? '启动' : 'Started', stop: zh ? '停止完成' : 'Stopped', die: zh ? '进程退出' : 'Process exited', destroy: zh ? '删除' : 'Removed', restart: zh ? '请求重启' : 'Restart requested', pause: zh ? '暂停' : 'Paused', unpause: zh ? '恢复' : 'Unpaused', kill: zh ? '发送终止信号' : 'Signal sent', health_status: zh ? '健康状态变化' : 'Health changed', oom: zh ? '内存不足终止' : 'OOM', rename: zh ? '重命名' : 'Renamed', update: zh ? '配置更新' : 'Configuration updated' })
export function mergeEvents(old: ContainerEvent[], incoming: ContainerEvent[]) {
  return [...new Map([...old, ...incoming].map(e => [e.id, e])).values()].sort((a, b) => BigInt(a.time_nano) < BigInt(b.time_nano) ? 1 : BigInt(a.time_nano) > BigInt(b.time_nano) ? -1 : a.id.localeCompare(b.id)).slice(0, 500)
}
