// Cron presets and light client-side validation. The server re-validates and
// computes real occurrences; this only shapes the form.

export interface CronPreset { value: string; zh: string; en: string }

export const cronPresets: CronPreset[] = [
  { value: '0 * * * *', zh: '每小时', en: 'Hourly' },
  { value: '0 */6 * * *', zh: '每 6 小时', en: 'Every 6 hours' },
  { value: '0 4 * * *', zh: '每天 04:00', en: 'Daily at 04:00' },
  { value: '0 4 * * 1', zh: '每周一 04:00', en: 'Mondays at 04:00' },
]

export const presetFor = (value: string) => cronPresets.find((preset) => preset.value === value)

// validateCron accepts the five standard fields plus @descriptors; the server
// preview endpoint remains the source of truth.
export const validateCron = (value: string) => {
  const trimmed = value.trim()
  if (trimmed.startsWith('@')) return ['@yearly', '@annually', '@monthly', '@weekly', '@daily', '@midnight', '@hourly'].includes(trimmed)
  const fields = trimmed.split(/\s+/)
  if (fields.length !== 5) return false
  const ranges: [number, number, string[]][] = [
    [0, 59, ['*', '/', '-', ',']],
    [0, 23, ['*', '/', '-', ',']],
    [1, 31, ['*', '/', '-', ',', '?']],
    [1, 12, ['*', '/', '-', ',']],
    [0, 7, ['*', '/', '-', ',', '?']],
  ]
  return fields.every((field, index) => {
    const [min, max, symbols] = ranges[index]
    return field.split(',').every((part) => {
      const body = part.startsWith('*/') ? part.slice(2) : part
      if (body === '*' || body === '?') return true
      return body.split('/').every((step) => {
        if (step === '') return false
        if (/^\d+$/.test(step)) {
          const numeric = Number(step)
          return numeric >= min && numeric <= max
        }
        return [...step].some((char) => symbols.includes(char)) && step.length <= 32
      })
    })
  })
}

// describeCron renders a short human summary for lists; unknown expressions
// fall back to the raw value.
export const describeCron = (value: string, zh: boolean) => {
  const preset = presetFor(value.trim())
  if (preset) return zh ? preset.zh : preset.en
  return value
}
