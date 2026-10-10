import { cloneElement, isValidElement, useId, type ReactNode } from 'react'
import { Input } from '../../components/ui/input'
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '../../components/ui/select'
export function Field({ label, children, hint }: { label: string; children: ReactNode; hint?: string }) { const id = useId(); const control = isValidElement<Record<string, unknown>>(children) && (children.type === Input || children.type === 'textarea'); return <div className="grid gap-1.5">{control ? <label htmlFor={id} className="text-sm font-medium">{label}</label> : <span className="text-sm font-medium">{label}</span>}{control ? cloneElement(children, { id, 'aria-describedby': hint ? `${id}-hint` : undefined }) : children}{hint && <span id={`${id}-hint`} className="text-xs text-muted-foreground">{hint}</span>}</div> }
export function Choice({ label, value, options, onChange }: { label: string; value: string; options: [string, string][]; onChange: (value: string) => void }) {
 const items = options.map(([value, label]) => ({ value, label }))
 return <Field label={label}>
  <Select items={items} value={value} onValueChange={v => { if (v) onChange(v) }}>
   <SelectTrigger aria-label={label} className="w-full"><SelectValue /></SelectTrigger>
   <SelectContent align="start" alignItemWithTrigger={false} className="min-w-0">
    <SelectGroup>{items.map(item => <SelectItem key={item.value} value={item.value}>{item.label}</SelectItem>)}</SelectGroup>
   </SelectContent>
  </Select>
 </Field>
}
export function Check({ label, checked, onChange }: { label: string; checked: boolean; onChange: (value: boolean) => void }) { return <label className="flex cursor-pointer items-start gap-2 text-sm"><input type="checkbox" className="mt-1 accent-foreground" checked={checked} onChange={e => onChange(e.target.checked)} /><span>{label}</span></label> }
export function ErrorText({ error }: { error: unknown }) { return error ? <p role="alert" className="break-words text-sm text-destructive">{error instanceof Error ? error.message : String(error)}</p> : null }
