import { useI18n } from '../../lib/i18n'
export function useOpsText() { const { language } = useI18n(); return (zh: string, en: string) => language === 'zh-CN' ? zh : en }
export function toggle(values: string[], value: string) { return values.includes(value) ? values.filter(v => v !== value) : [...values, value] }

// getRandomValues also works on local HTTP origins where randomUUID is absent.
export function newAIRequestID() {
 const bytes = new Uint8Array(16)
 crypto.getRandomValues(bytes)
 return Array.from(bytes, value => value.toString(16).padStart(2, '0')).join('')
}
