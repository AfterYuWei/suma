import { Button } from '../../components/ui/button'
import { Input } from '../../components/ui/input'
import { useI18n } from '../../lib/i18n'
import { resolveTimeZone, systemTimeZone, validTimeZone } from '../../lib/date-time'

const zones = [...new Set(['UTC', systemTimeZone(), ...(Intl.supportedValuesOf?.('timeZone') || [])])].sort()

export function TimeZoneField({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  const { language } = useI18n()
  const zh = language === 'zh-CN'
  const automatic = value === 'system'
  return <div className="flex flex-col gap-1.5">
    <div className="flex flex-wrap items-center gap-2">
      <Input id="settings-general.timezone" aria-label={zh ? '时区' : 'Timezone'} className="min-w-48 flex-1" value={automatic ? systemTimeZone() : value} onChange={event => onChange(event.target.value)} list="suma-time-zones" required aria-invalid={!automatic && !validTimeZone(value)} />
      <datalist id="suma-time-zones">{zones.map(zone => <option key={zone} value={zone} />)}</datalist>
      <Button type="button" size="sm" variant="outline" onClick={() => onChange('system')}>{zh ? '使用系统时区' : 'Use system timezone'}</Button>
    </div>
    {!automatic && !validTimeZone(value) && <p role="alert" className="text-xs text-destructive">{zh ? '请输入有效的 IANA 时区。' : 'Enter a valid IANA timezone.'}</p>}
    <p className="text-xs text-muted-foreground">{automatic ? (zh ? `跟随当前设备：${systemTimeZone()}` : `Following this device: ${systemTimeZone()}`) : (zh ? '输入或选择 IANA 时区，例如 Asia/Shanghai。' : 'Enter or select an IANA timezone, such as Asia/Shanghai.')}</p>
    <p className="text-xs text-muted-foreground">{zh ? '界面时间统一使用此时区；已保存清理策略的执行时区保持不变。' : 'Interface times use this zone; saved cleanup schedules keep their execution zone.'} {resolveTimeZone(value)}</p>
  </div>
}
