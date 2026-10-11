import { useI18n } from '../../lib/i18n'
import type { Catalog, RuleConfig, Subscription } from './types'
export function useOpsText() { const { language } = useI18n(); return (zh: string, en: string) => language === 'zh-CN' ? zh : en }
export function toggle(values: string[], value: string) { return values.includes(value) ? values.filter(v => v !== value) : [...values, value] }

export function channelPlatforms(language: string): [string, string][] {
 return [['feishu_app', language === 'zh-CN' ? '飞书应用机器人' : 'Feishu application bot'], ['telegram', 'Telegram'], ['webhook', 'Webhook']]
}
export function defaultSubscription(timezone: string): Subscription {
 return { rule_ids: [], policy: { mode: 'immediate', timezone, digest_hour: 9, quiet_start: '', quiet_end: '', template: '', fallback_id: '', fallback_chat_id: '' } }
}
export function normalizeSubscription(value: Subscription | undefined, timezone: string): Subscription {
 const defaults = defaultSubscription(timezone)
 return { rule_ids: value?.rule_ids || [], policy: { ...defaults.policy, ...value?.policy } }
}
export function ruleSummary(config: RuleConfig, catalog: Catalog | undefined, language: string) {
 const events = config.events.map(type => { const entry = catalog?.events.find(event => event.type === type); return entry ? language === 'zh-CN' ? entry.title : entry.title_en : type }).join('、')
 const zh = language === 'zh-CN'
 const scope = [config.severities?.join(' / '), config.node_ids?.length || config.group_ids?.length ? zh ? `${config.node_ids?.length || 0} 个节点 / ${config.group_ids?.length || 0} 个节点组` : `${config.node_ids?.length || 0} nodes / ${config.group_ids?.length || 0} groups` : zh ? '不限节点范围' : 'Any node scope', config.projects?.join('、')].filter(Boolean).join(' · ')
 return { events, scope }
}
