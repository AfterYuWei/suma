export function triggerSourceLabel(source: string | undefined, zh: boolean) {
  return ({ manual: zh ? '手动' : 'Manual', scheduled: zh ? '周期' : 'Scheduled', webhook: 'Webhook', system: zh ? '系统' : 'System' })[source ?? ''] ?? (zh ? '未记录' : 'Unrecorded')
}
