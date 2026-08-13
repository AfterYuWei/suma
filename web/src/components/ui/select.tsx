import { Select as BaseSelect } from '@base-ui/react/select'
import { Check, ChevronDown } from 'lucide-react'
import { cn } from '../../lib/cn'

export interface SelectOption<T extends string> {
  value: T
  label: string
  disabled?: boolean
}

export function Select<T extends string>({ value, options, onValueChange, placeholder, disabled, required, name, className }: {
  value?: T | null
  options: readonly SelectOption<T>[]
  onValueChange: (value: T) => void
  placeholder?: string
  disabled?: boolean
  required?: boolean
  name?: string
  className?: string
}) {
  return <BaseSelect.Root items={options} value={value || null} onValueChange={(next) => { if (next) onValueChange(next) }} disabled={disabled} required={required} name={name}>
    <BaseSelect.Trigger className={cn('group flex h-9 w-full items-center gap-2 rounded-md border border-border bg-surface px-3 text-left text-xs text-text outline-none transition-[border-color,background-color,box-shadow] hover:border-[color-mix(in_srgb,var(--accent)_38%,var(--border))] hover:bg-surface-hover focus-visible:border-accent focus-visible:outline-none focus-visible:ring-3 focus-visible:ring-[color-mix(in_srgb,var(--accent)_12%,transparent)] disabled:cursor-not-allowed disabled:border-border-subtle disabled:bg-muted disabled:text-text-disabled', className)}>
      <BaseSelect.Value placeholder={placeholder} className="min-w-0 flex-1 truncate data-[placeholder]:text-text-subtle" />
      <BaseSelect.Icon className="ml-auto shrink-0 text-text-subtle"><ChevronDown className="size-3.5 transition-transform duration-150 group-data-[popup-open]:rotate-180" /></BaseSelect.Icon>
    </BaseSelect.Trigger>
    <BaseSelect.Portal>
      <BaseSelect.Positioner sideOffset={6} alignItemWithTrigger={false} className="z-[100] outline-none">
        <BaseSelect.Popup className="min-w-[var(--anchor-width)] origin-[var(--transform-origin)] overflow-hidden rounded-lg border border-border bg-elevated p-1 text-text shadow-[0_16px_48px_rgba(0,0,0,.32)] transition-[transform,opacity] duration-150 data-[ending-style]:scale-[.98] data-[ending-style]:opacity-0 data-[starting-style]:scale-[.98] data-[starting-style]:opacity-0">
          <BaseSelect.List className="max-h-72 overflow-y-auto outline-none">
            {options.map((option) => <BaseSelect.Item key={option.value} value={option.value} disabled={option.disabled} className="grid min-h-9 cursor-default grid-cols-[18px_minmax(0,1fr)] items-center gap-2 rounded-md px-2.5 text-xs text-text-muted outline-none transition-colors data-[disabled]:text-text-disabled data-[highlighted]:bg-surface-hover data-[highlighted]:text-text data-[selected]:text-text">
              <BaseSelect.ItemIndicator keepMounted className="grid size-[18px] place-items-center text-accent opacity-0 data-[selected]:opacity-100"><Check className="size-3.5" /></BaseSelect.ItemIndicator>
              <BaseSelect.ItemText className="min-w-0 truncate">{option.label}</BaseSelect.ItemText>
            </BaseSelect.Item>)}
          </BaseSelect.List>
        </BaseSelect.Popup>
      </BaseSelect.Positioner>
    </BaseSelect.Portal>
  </BaseSelect.Root>
}
