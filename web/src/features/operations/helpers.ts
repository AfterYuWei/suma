import { useI18n } from '../../lib/i18n'
export function useOpsText() { const { language } = useI18n(); return (zh: string, en: string) => language === 'zh-CN' ? zh : en }
export function toggle(values: string[], value: string) { return values.includes(value) ? values.filter(v => v !== value) : [...values, value] }
