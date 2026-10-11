export interface DeliveryPolicy { mode: 'immediate' | 'merge' | 'daily'; timezone: string; digest_hour: number; quiet_start: string; quiet_end: string; muted_until?: string | null; template: string; fallback_id: string; fallback_chat_id: string }
export interface Subscription { rule_ids: string[]; policy: DeliveryPolicy }
export interface ChannelTarget { chat_id: string; name: string; subscription_override?: Subscription | null }
export interface ConnectionStatus { state: 'stopped' | 'connecting' | 'connected' | 'reconnecting' | 'error'; error?: string; message_count?: number; last_message_at?: string; last_message_result?: string; discovery_error?: string }
export interface ChannelConfig { endpoint: string; chat_id: string; app_id: string; targets?: ChannelTarget[]; language: string; subscription: Subscription; allow_private: boolean; auto_discover: boolean; public_url: string }
export interface Channel { id: string; name: string; provider: string; enabled: boolean; version: number; config: ChannelConfig; has_secrets: boolean; last_error?: string; last_sent_at?: string }
export interface RuleConfig { events: string[]; severities: string[]; node_ids: string[]; group_ids: number[]; projects: string[] }
export interface Rule { id: string; name: string; version: number; config: RuleConfig }
export interface Catalog { events: { type: string; category: string; title: string; title_en: string }[]; presets: Record<string, string[]> }
export interface MatchedRule { id: string; name: string; version: number }
export interface Delivery { id: string; channel_id: string; chat_id?: string; matched_rules: MatchedRule[]; event_count: number; source: 'event' | 'fallback' | 'manual' | 'test'; origin_delivery_id?: string; status: string; reason?: string; attempts: number; due_at: string; created_at: string }
export interface NotificationEvent { id: string; type: string; severity: string; time: string; node_id: string; node_name: string; resource_id: string; title: string; message: string; task_id: string; read: boolean }
export interface Inbox { items: NotificationEvent[]; unread: number }
