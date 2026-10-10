import { useI18n } from '../../lib/i18n'
import type { Channel, RuleConfig } from './types'
export function useOpsText() { const { language } = useI18n(); return (zh: string, en: string) => language === 'zh-CN' ? zh : en }
export function toggle(values: string[], value: string) { return values.includes(value) ? values.filter(v => v !== value) : [...values, value] }

export function channelPlatforms(language: string): [string, string][] {
 return [['feishu_app', language === 'zh-CN' ? '飞书应用机器人' : 'Feishu application bot'], ['telegram', 'Telegram'], ['webhook', 'Webhook']]
}
export function hasMissingRecipients(config: RuleConfig, channels: Channel[]) {
 const primaryMissing = config.channel_ids.some(id => {
  const channel = channels.find(item => item.id === id)
  return !channel || channel.provider === 'feishu_app' && (!config.channel_targets?.[id]?.length || config.channel_targets[id].some(chatID => !channel.config.targets?.some(target => target.chat_id === chatID)))
 })
 const fallback = channels.find(channel => channel.id === config.fallback_id)
 return primaryMissing || !!config.fallback_id && (!fallback || fallback.provider === 'feishu_app' && !fallback.config.targets?.some(target => target.chat_id === config.fallback_chat_id))
}
