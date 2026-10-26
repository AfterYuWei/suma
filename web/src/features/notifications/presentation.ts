import type { NotificationEvent } from './types'

type Event = Pick<NotificationEvent, 'type' | 'severity' | 'title' | 'message'> & Partial<NotificationEvent>
export interface NotificationPresentation { title: string; subject: string; summary: string; status: string }
const titles: Record<string, [string, string]> = {
 'auth.login':['登录成功','Login succeeded'], 'auth.new_ip':['新的登录 IP','New login IP'], 'auth.login_failed':['连续登录失败','Repeated login failures'],
 'account.changed':['账户安全变更','Account security changed'], 'credential.changed':['凭证变更','Credential changed'],
 'node.offline':['节点离线','Node offline'], 'node.recovered':['节点已恢复','Node recovered'], 'node.changed':['节点配置已更新','Node configuration updated'],
 'agent.error':['Agent 连接异常','Agent connection failed'], 'tls.expiring':['TLS 证书即将到期','TLS certificate expiring'], 'tls.recovered':['TLS 证书已更新','TLS certificate renewed'],
 'container.exited':['容器异常退出','Container exited unexpectedly'], 'container.oom':['容器内存不足','Container out of memory'], 'container.unhealthy':['容器健康检查失败','Container health check failed'],
 'container.restart_loop':['容器频繁重启','Container restarting repeatedly'], 'container.recovered':['容器已恢复','Container recovered'],
 'image.available':['镜像有更新','Image update available'], 'image.recreate_required':['容器待重建','Container recreation needed'], 'image.check_failed':['镜像检查失败','Image check failed'],
 'image.pull_failed':['镜像拉取失败','Image pull failed'], 'image.check_recovered':['镜像检查已恢复','Image check recovered'],
 'cd.awaiting_approval':['发布待审核','Release awaiting approval'], 'cd.completed':['发布完成','Deployment completed'], 'cd.drift':['发布状态不一致','Deployment drift detected'], 'cd.recovered':['发布状态已恢复','Deployment recovered'],
 'project.completed':['项目操作完成','Project operation completed'], 'cleanup.completed':['清理完成','Cleanup completed'], 'cleanup.skipped':['清理已跳过','Cleanup skipped'],
 'task.failed':['任务失败','Task failed'], 'task.canceled':['任务已取消','Task canceled'], 'notification.failed':['通知发送失败','Notification delivery failed'], 'notification.recovered':['通知发送已恢复','Notification delivery recovered'],
}
const short = (value: string, length: number) => { const text = Array.from(value.trim().replace(/\s+/g,' ')); return text.length > length ? text.slice(0,length-1).join('')+'…' : text.join('') }
export function compactResource(value: string) {
 if (value.startsWith('sha256:')) return short(value,19)
 return short(value,Array.from(value).length >= 32 && !['/', '.', ':', ' '].some(char=>value.includes(char)) ? 13 : 80)
}
function conciseReason(message: string, en: boolean) {
 const value = message.toLowerCase()
 const reasons: [string[],string,string][] = [
  [['authentication_required','unauthorized','authentication failed','http 401'],'认证失败','Authentication failed'],
  [['permission denied','forbidden','http 403'],'权限不足','Permission denied'],
  [['reference_not_found'],'镜像不存在','Image reference not found'], [['rate_limited','http 429'],'请求受限','Rate limited'],
  [['platform_unavailable'],'没有匹配的系统架构','No matching platform'], [['unsupported_manifest'],'镜像格式不支持','Unsupported image format'], [['invalid_reference'],'镜像地址无效','Invalid image reference'],
  [['timeout','timed out','deadline exceeded'],'连接超时','Connection timed out'], [['registry_unreachable','registry_unavailable'],'镜像仓库不可达','Registry unreachable'],
  [['connection refused','provider connection failed'],'连接失败','Connection failed'], [['desired_commit_mismatch'],'运行版本与期望不一致','Running revision differs from desired'],
  [['no_active_release'],'尚无运行中的发布','No active release'], [['runtime_unhealthy'],'服务未正常运行','Service is not healthy'],
 ]
 const match = reasons.find(([keys]) => keys.some(key => value.includes(key)))
 return match ? match[en ? 2 : 1] : ''
}
export function presentNotification(event: Event, language: string): NotificationPresentation {
 const en = language === 'en-US', t = (zh: string, english: string) => en ? english : zh
 const failed = event.severity === 'error' || event.severity === 'critical'
 const result = { title: titles[event.type]?.[en ? 1 : 0] || '',subject:'',summary:'',status:event.severity === 'critical' ? 'critical' : failed ? 'error' : event.severity === 'warning' ? 'warning' : 'info' }
 if (event.type.endsWith('.recovered') || event.type === 'image.check_recovered') result.status = 'recovered'
 if (['auth.login','cd.completed','project.completed','cleanup.completed'].includes(event.type) && !failed) result.status = 'success'
 if (event.type === 'agent.error' && event.title.toLowerCase().includes('incompatible')) result.title = t('Agent 版本不兼容','Agent version incompatible')
 if (event.type === 'tls.expiring' && /^.{4}-.{2}-/.test(event.message)) result.summary = t('到期：','Expires: ')+event.message.slice(0,10)
 if (event.type === 'container.exited') { const code=event.message.match(/(?:exit=|exit code )(-?\d+)/)?.[1]; if(code) result.summary=t('退出码 ','Exit code ')+code }
 if (event.type === 'container.recovered') {
  if(event.message.includes('health')) result.title=t('健康检查已恢复','Health check recovered')
  if(event.message.includes('stably')) result.title=t('容器运行已稳定','Container running stably')
 }
 if (event.type === 'cd.awaiting_approval') { result.status='pending';const commit=event.message.match(/\b[0-9a-f]{40,64}\b/)?.[0];if(commit)result.summary=t('版本 ','Commit ')+commit.slice(0,7) }
 if (event.type === 'cd.completed') result.title=event.title.includes('rollback') ? failed ? t('回滚失败','Rollback failed') : t('回滚完成','Rollback completed') : failed ? t('发布失败','Deployment failed') : result.title
 if (event.type === 'cd.completed' && event.title.startsWith('cd.sync')) result.title=failed?t('代码同步失败','Git sync failed'):t('代码同步完成','Git sync completed')
 if (event.type === 'cd.completed' && event.title.startsWith('cd.approve')) result.title=failed?t('发布审核失败','Release review failed'):t('发布已批准','Release approved')
 if (event.type === 'cd.completed' && event.title.startsWith('cd.reject')) {result.title=failed?t('发布审核失败','Release review failed'):t('发布已拒绝','Release rejected');if(!failed)result.status='canceled'}
 if (event.type === 'project.completed' && failed) result.title=t('项目操作失败','Project operation failed')
 if (event.type === 'project.completed' && event.severity === 'warning') {result.title=t('项目操作已取消','Project operation canceled');result.status='canceled'}
 if (event.type === 'cleanup.completed') { if(failed)result.title=t('清理失败','Cleanup failed');if(event.message.startsWith('partial_failed'))result.title=t('清理部分失败','Cleanup partially failed') }
 if (event.type === 'cleanup.skipped') result.status='skipped'
 if (event.type === 'task.canceled') result.status='canceled'
 if (['auth.login','auth.new_ip','auth.login_failed'].includes(event.type)) {
  const ip=event.message.split(/\s+/).find(word => /^(?:\d{1,3}\.){3}\d{1,3}$/.test(word) || /^[0-9a-f]*:[0-9a-f:.%]+$/i.test(word))
  if(ip)result.summary='IP '+ip
 }
 if (event.type.startsWith('container.') || event.type.startsWith('image.')) result.subject=event.title.replace(/^\//,'')
 else if (event.type.startsWith('cd.')) result.subject=event.project || (['project','delivery_project'].includes(event.resource_type || '') ? event.resource_id || '' : '') || (event.title.startsWith('cd.') ? '' : event.title)
 else if (event.type.startsWith('task.') || event.type === 'project.completed') result.subject=event.title
 else if (event.type.startsWith('notification.') && !event.title.includes('Channel delivery failed')) result.subject=event.title
 else if (event.type === 'credential.changed') result.subject=compactResource(event.resource_id || '')
 else if ((event.type.startsWith('auth.') || event.type === 'account.changed') && event.actor_id != null) result.subject=t(`用户 #${event.actor_id}`,`User #${event.actor_id}`)
 const aliases: Record<string,string[]> = { 'container.oom':['容器 OOM','Container OOM'],'container.unhealthy':['健康检查失败','Container unhealthy'],'container.restart_loop':['Container restart loop'],'image.available':['发现镜像更新'],'image.recreate_required':['镜像更新后需重建','Container recreation required'],'notification.failed':['渠道发送失败','Channel delivery failed'],'notification.recovered':['渠道发送恢复','Channel delivery recovered'],'cd.completed':['发布或回滚结果','Deployment or rollback completed'] }
 if(result.subject===result.title || titles[event.type]?.includes(result.subject) || aliases[event.type]?.includes(result.subject))result.subject=''
 if (!result.subject && /^(container|image|notification)\./.test(event.type)) result.subject=compactResource(event.resource_id || '')
 if (result.subject === event.node_name || result.subject === result.title) result.subject=''
 result.subject=short(result.subject,80)
 if(!result.title) {result.title=short(event.title,100);result.summary=short(event.message,100)}
 if(!result.title)result.title=t('系统通知','System notification')
 if(!result.summary && (failed || ['image.check_failed','agent.error','cd.drift'].includes(event.type)))result.summary=conciseReason(event.message,en)
 return result
}
export function defaultNotificationText(event: Event, language: string, timestamp: string) {
 const display=presentNotification(event,language), context=[event.node_name || compactResource(event.node_id || '')]
 if(display.subject && context[0] !== display.subject)context.push(display.subject)
 if(timestamp)context.push(timestamp)
 const title=(event.severity === 'critical' ? language === 'en-US' ? 'Urgent · ' : '紧急 · ' : '')+display.title
 return [title,context.filter(Boolean).join(' · '),display.summary].filter(Boolean).join('\n')
}
export function notificationTimestamp(time: string, timezone: string) {
 const parts=new Intl.DateTimeFormat('en-CA',{timeZone:timezone,year:'numeric',month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit',hourCycle:'h23'}).formatToParts(new Date(time))
 const value=Object.fromEntries(parts.map(part=>[part.type,part.value]))
 return `${value.year}-${value.month}-${value.day} ${value.hour}:${value.minute}`
}
export function templateExample(type: string, language: string): NotificationEvent {
 const recovered=type.includes('recover'), container=type.startsWith('container.'), cd=type.startsWith('cd.'), notification=type.startsWith('notification.'), node=/^(node|agent|tls)\./.test(type)
 const title=container?'redis':cd?'gateway':notification?'Operations':node?'Production':type.startsWith('image.')?'nginx:stable':type.startsWith('task.') || type==='project.completed'?'Pull image':titles[type]?.[language==='en-US'?1:0] || 'SUMA'
 const message=type==='container.recovered'?'Container has been running stably for at least two minutes':type==='auth.new_ip' || type==='auth.login'?'Login completed from 203.0.113.10':type==='auth.login_failed'?'Repeated authentication failures from 203.0.113.10':type==='container.exited'?'die exit=137':type==='image.check_failed'?'unavailable authentication_required':type==='tls.expiring'?'2026-11-01T00:00:00Z':type==='cd.awaiting_approval'?'Release aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa is ready for approval':type==='container.oom'?'oom exit=':''
 const info=recovered || type.endsWith('completed') || ['auth.login','account.changed','credential.changed','node.changed','image.available','image.recreate_required'].includes(type)
 const warning=['auth.new_ip','tls.expiring','container.exited','container.unhealthy','container.restart_loop','cd.awaiting_approval','cd.drift','cleanup.skipped','task.canceled'].includes(type)
 return {id:'example',type,severity:type==='auth.login_failed'?'critical':info?'info':warning?'warning':'error',time:'2026-10-11T04:00:00Z',node_id:'local',node_name:'Production',resource_id:container?'container-example':notification?'channel-example':cd?'gateway':'',title,project:cd?'gateway':'',message,task_id:'',read:false}
}
