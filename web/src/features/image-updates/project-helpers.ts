import type { ProjectImageCandidate } from './types'
export function candidateStatus(status: ProjectImageCandidate['status'], zh: boolean) {
 return (zh ? { pending: '等待发布', publishing: '发布中', succeeded: '发布成功', failed: '发布失败', canceled: '已取消', superseded: '版本已变化，请重新检测' } : { pending: 'Ready to publish', publishing: 'Publishing', succeeded: 'Published', failed: 'Publication failed', canceled: 'Canceled', superseded: 'Version changed; check again' })[status]
}
export function generateCron(kind: string, step: string, clock: string, weekday: string) {
 const [hour, minute] = clock.split(':').map(Number)
 if (kind === 'minutes') return `*/${step} * * * *`
 if (kind === 'hours') return `0 */${step} * * *`
 if (kind === 'daily') return `${minute} ${hour} * * *`
 return `${minute} ${hour} * * ${weekday}`
}
