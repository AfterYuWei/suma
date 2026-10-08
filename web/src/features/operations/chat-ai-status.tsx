import { useOpsText } from './helpers'
import type { AISettings } from './types'

export function ChatAIStatus({ settings }: { settings?: AISettings }) {
 const t = useOpsText()
 if (!settings) return null
 return <div role="status" aria-label={t('AI 聊天就绪状态', 'AI chat readiness')} className="space-y-1 break-words text-xs text-muted-foreground">
  <p className={settings.enabled ? 'text-emerald-600 dark:text-emerald-400' : 'font-medium text-foreground'}>{settings.enabled ? t('AI 运维已启用', 'AI operations enabled') : t('AI 运维尚未启用', 'AI operations are off')}</p>
  <p>{settings.enabled ? t(`默认模型：${settings.model} · 已授权 ${settings.node_ids.length} 个节点。`, `Default model: ${settings.model} · ${settings.node_ids.length} authorized node${settings.node_ids.length === 1 ? '' : 's'}.`) : t('先配置默认模型并授权节点，开启“启用 AI 运维”并保存。启用渠道聊天或绑定账号不会自动启用 AI。', 'Configure the default model and authorize nodes, then enable and save AI operations. Channel chat and account binding do not enable AI automatically.')}</p>
 </div>
}
