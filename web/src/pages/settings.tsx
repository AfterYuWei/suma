import { useEffect, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Save } from 'lucide-react'
import { cn } from '@/lib/utils'
import { LoadingState } from '../components/ui/loading-state'
import { Button } from '../components/ui/button'
import { Input } from '../components/ui/input'
import { Label } from '../components/ui/label'
import { RadioGroup, RadioGroupItem } from '../components/ui/radio-group'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../components/ui/select'
import { Spinner } from '../components/ui/spinner'
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
  const zh = language === 'zh-CN'
  const query = useQuery({ queryKey: ['settings'], queryFn: () => api<Record<string, string>>('/settings') })
  const save = useMutation({ mutationFn: (values: Record<string, string>) => api('/settings', { method: 'PUT', body: JSON.stringify(values) }), onSuccess: () => client.invalidateQueries({ queryKey: ['settings'] }) })
  const [values, setValues] = useState<Record<string, string> | null>(null)
  const initializedRef = useRef(false)
  useEffect(() => {
    // 只在首次拿到数据时初始化草稿，后续刷新不覆盖用户正在编辑的内容（与原 Semi Form initValues 行为一致）。
    if (query.data && !initializedRef.current) {
      initializedRef.current = true
      setValues(query.data)
    }
  }, [query.data])
  if (!values) return <LoadingState label={t('loading')} rows={6} />
  const update = (key: string, value: string) => setValues((previous) => previous ? { ...previous, [key]: value } : previous)

  return <ResourceFrame title={t('settings')} detail={t('localConfiguration')}>
    <div className="mx-auto flex w-full max-w-[1120px] flex-col gap-8">
      <section className="flex w-full flex-col gap-4">
        <h3 className="text-sm font-medium">{t('appearance')}</h3>
        <div className="flex flex-col gap-4">
          <div className="flex flex-col items-start gap-2">
            <span className="text-sm font-medium">{t('language')}</span>
            <Select<Language> value={language} onValueChange={(next) => { if (next !== null) setLanguage(next) }}>
              <SelectTrigger aria-label={t('language')} className="w-44"><SelectValue>{language === 'zh-CN' ? t('chinese') : t('english')}</SelectValue></SelectTrigger>
              <SelectContent>
                <SelectItem value="zh-CN">{t('chinese')}</SelectItem>
                <SelectItem value="en-US">{t('english')}</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col items-start gap-2">
            <span className="text-sm font-medium">{t('chooseTheme')}</span>
            <RadioGroup value={theme} onValueChange={(next) => setTheme(next as Theme)} className="inline-grid w-fit grid-flow-col gap-0.5 rounded-lg bg-muted p-0.5">
              {([['dark', t('dark')], ['light', t('light')], ['system', t('systemTheme')]] as [Theme, string][]).map(([value, label]) => (
                <label key={value} className="cursor-pointer">
                  <RadioGroupItem value={value} className="peer sr-only" />
                  <span className={cn(
                    'block cursor-pointer rounded-md px-3 py-1 text-sm transition-colors peer-focus-visible:ring-3 peer-focus-visible:ring-ring/50',
                    theme === value ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground',
                  )}>{label}</span>
                </label>
              ))}
            </RadioGroup>
          </div>
        </div>
      </section>

      <form onSubmit={(event) => { event.preventDefault(); if (values) save.mutate(values) }} className="flex w-full flex-col gap-8">
        {sections.map(([section, fields]) => (
          <section key={section} className="flex w-full flex-col gap-4">
            <h3 className="text-sm font-medium">{t(section)}</h3>
            <div className="grid gap-x-6 gap-y-4 md:grid-cols-2">
              {fields.map(([key, label]) => (
                <div key={key} className="grid gap-1.5">
                  <Label htmlFor={`settings-${key}`}>{t(label)}</Label>
                  <Input id={`settings-${key}`} value={values[key] ?? ''} onChange={(event) => update(key, event.target.value)} />
                </div>
              ))}
            </div>
          </section>
        ))}
        <div className="flex flex-wrap items-center gap-3">
          <Button type="submit" disabled={save.isPending}>{save.isPending ? <Spinner className="size-4" /> : <Save className="size-4" />}{t('saveChanges')}</Button>
          {save.isSuccess && <span className="text-sm text-emerald-600 dark:text-emerald-400">{t('settingsSaved')}</span>}
          {save.isError && <span className="text-sm text-destructive">{zh ? '保存失败' : 'Save failed'}</span>}
        </div>
      </form>
    </div>
  </ResourceFrame>
}
