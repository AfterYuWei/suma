import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Save } from 'lucide-react'
import { type FormEvent } from 'react'
import { LoadingState } from '../components/ui/loading-state'
import { api } from '../lib/api'
import { useI18n, type TranslationKey } from '../lib/i18n'
import { type Language, type Theme, useUIStore } from '../stores/ui'
import { ResourceFrame } from './images'

const sections: [TranslationKey, [string, TranslationKey][]][] = [
  ['general', [['general.server_name', 'serverName'], ['general.timezone', 'timezone']]],
  ['compose', [['docker.compose_command', 'composeCommand']]],
  ['storage', [['storage.compose_root', 'composeRoot'], ['storage.data_root', 'dataRoot'], ['storage.backup_root', 'backupRoot']]],
  ['security', [['security.cookie_secure', 'secureCookies']]],
  ['registry', [['registry.default', 'defaultRegistry']]],
]

export function SettingsPage() {
  const client = useQueryClient()
  const { theme, setTheme, language, setLanguage } = useUIStore()
  const { t } = useI18n()
  const query = useQuery({ queryKey: ['settings'], queryFn: () => api<Record<string, string>>('/settings') })
  const save = useMutation({ mutationFn: (values: Record<string, string>) => api('/settings', { method: 'PUT', body: JSON.stringify(values) }), onSuccess: () => client.invalidateQueries({ queryKey: ['settings'] }) })
  const submit = (event: FormEvent<HTMLFormElement>) => { event.preventDefault(); const values: Record<string, string> = {}; new FormData(event.currentTarget).forEach((value, key) => { values[key] = String(value) }); save.mutate(values) }
  if (!query.data) return <LoadingState label={t('loading')} rows={6} />

  return <ResourceFrame eyebrow={t('system')} title={t('settings')} detail={t('localConfiguration')}><form onSubmit={submit} className="max-w-3xl">
    {sections.map(([section, fields]) => <section key={section} className="grid gap-6 border-t border-border py-7 md:grid-cols-[180px_1fr]"><div><h2 className="text-sm font-semibold">{t(section)}</h2></div><div className="space-y-4">{fields.map(([key, label]) => <label key={key} className="block"><span className="mb-1.5 block text-xs text-text-muted">{t(label)}</span><input name={key} defaultValue={query.data?.[key]} className="h-9 w-full rounded-md border border-border bg-surface px-3 font-mono text-xs outline-none focus:border-accent" /></label>)}</div></section>)}
    <PreferenceSection title={t('language')} description="中文 / English">{([['zh-CN', 'chinese'], ['en-US', 'english']] as [Language, TranslationKey][]).map(([value, label]) => <Choice key={value} selected={language === value} onClick={() => setLanguage(value)}>{t(label)}</Choice>)}</PreferenceSection>
    <PreferenceSection title={t('appearance')} description={t('chooseTheme')}>{([['dark', 'dark'], ['light', 'light'], ['system', 'systemTheme']] as [Theme, TranslationKey][]).map(([value, label]) => <Choice key={value} selected={theme === value} onClick={() => setTheme(value)}>{t(label)}</Choice>)}</PreferenceSection>
    <div className="flex items-center justify-end border-t border-border pt-5"><span className="mr-3 text-xs text-text-subtle">{save.isSuccess ? t('settingsSaved') : ''}</span><button className="flex h-8 items-center gap-2 rounded-md bg-accent px-3 text-xs font-semibold text-accent-foreground"><Save className="size-3.5" />{t('saveChanges')}</button></div>
  </form></ResourceFrame>
}

function PreferenceSection({ title, description, children }: { title: string; description: string; children: React.ReactNode }) { return <section className="grid gap-6 border-t border-border py-7 md:grid-cols-[180px_1fr]"><div><h2 className="text-sm font-semibold">{title}</h2><p className="mt-1 text-xs text-text-subtle">{description}</p></div><div className="flex gap-2">{children}</div></section> }
function Choice({ selected, onClick, children }: { selected: boolean; onClick: () => void; children: React.ReactNode }) { return <button type="button" onClick={onClick} className={`h-9 rounded-md border px-4 text-xs ${selected ? 'border-accent bg-surface-hover' : 'border-border bg-surface'}`}>{children}</button> }
