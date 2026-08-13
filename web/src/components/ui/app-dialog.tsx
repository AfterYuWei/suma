import { Dialog } from '@base-ui/react/dialog'
import { AlertTriangle, Check, X } from 'lucide-react'
import { useEffect, useState, type FormEvent } from 'react'
import { useI18n } from '../../lib/i18n'
import { finishDialog, useDialogStore } from '../../stores/dialog'

export function AppDialog() {
  const request = useDialogStore((state) => state.request)
  const { t } = useI18n()
  const [value, setValue] = useState('')
  const [checked, setChecked] = useState(false)
  useEffect(() => {
    setValue(request?.input?.initialValue ?? '')
    setChecked(request?.checkbox?.initialChecked ?? false)
  }, [request])
  const valid = !request?.input?.requiredValue || value === request.input.requiredValue
  const submit = (event: FormEvent) => { event.preventDefault(); if (!request?.choices && valid) finishDialog(request?.checkbox ? { value: request.input ? value : 'confirmed', checked } : request?.input ? value : 'confirmed') }

  return <Dialog.Root open={!!request} onOpenChange={(open) => { if (!open) finishDialog(null) }}>
    <Dialog.Portal>
      <Dialog.Backdrop className="fixed inset-0 z-[80] bg-black/65 backdrop-blur-[2px] transition-opacity duration-150 data-[ending-style]:opacity-0 data-[starting-style]:opacity-0" />
      <Dialog.Viewport className="fixed inset-0 z-[81] grid place-items-center overflow-y-auto px-4 py-10">
        <Dialog.Popup className="relative w-full max-w-md origin-center overflow-hidden rounded-2xl border border-border bg-elevated shadow-[0_24px_80px_rgba(0,0,0,.45)] transition duration-150 data-[ending-style]:scale-[.98] data-[ending-style]:opacity-0 data-[starting-style]:scale-[.98] data-[starting-style]:opacity-0">
          <form onSubmit={submit}>
            <div className="flex gap-4 px-6 pb-5 pt-6"><div className={`grid size-9 shrink-0 place-items-center rounded-xl border ${request?.danger ? 'border-danger/30 bg-danger-subtle text-danger' : 'border-border bg-surface text-text-muted'}`}>{request?.danger ? <AlertTriangle className="size-4" /> : <span className="size-2 rounded-full bg-accent" />}</div><div className="min-w-0 flex-1"><Dialog.Title className="pr-8 text-base font-semibold tracking-tight">{request?.title}</Dialog.Title>{request?.description && <Dialog.Description className="mt-2 text-sm leading-6 text-text-muted">{request.description}</Dialog.Description>}</div><Dialog.Close className="absolute right-4 top-4 grid size-8 place-items-center rounded-lg text-text-subtle hover:bg-surface-hover hover:text-text" aria-label={t('cancel')}><X className="size-4" /></Dialog.Close></div>
            {request?.input && <div className="border-y border-border bg-surface/40 px-6 py-4"><label className="block text-xs text-text-muted">{request.input.label}<input autoFocus value={value} onChange={(event) => setValue(event.target.value)} placeholder={request.input.placeholder} className="mt-2 h-10 w-full rounded-xl border border-border bg-background px-3 font-mono text-sm outline-none focus:border-accent" /></label>{request.input.requiredValue && <p className={`mt-2 text-[11px] ${valid ? 'text-success' : 'text-text-subtle'}`}>{t('typeToConfirm', { value: request.input.requiredValue })}</p>}</div>}
            {request?.checkbox && <label className={`flex cursor-pointer items-start gap-3 bg-surface/40 px-6 py-4 ${request.input ? 'border-b border-border' : 'border-y border-border'}`}><input type="checkbox" checked={checked} onChange={(event) => setChecked(event.target.checked)} className="peer sr-only" /><span className="mt-0.5 grid size-4 shrink-0 place-items-center rounded border border-border bg-background text-transparent transition peer-focus-visible:ring-2 peer-focus-visible:ring-accent/40 peer-checked:border-accent peer-checked:bg-accent peer-checked:text-accent-foreground"><Check className="size-3" strokeWidth={3} /></span><span><span className="block text-xs font-medium text-text">{request.checkbox.label}</span>{request.checkbox.description && <span className="mt-1 block text-[11px] leading-4 text-text-subtle">{request.checkbox.description}</span>}</span></label>}
            <div className="flex flex-wrap justify-end gap-2 px-6 py-4"><button type="button" onClick={() => finishDialog(null)} className="h-9 rounded-xl border border-border bg-surface px-4 text-xs font-medium hover:bg-surface-hover">{request?.cancelLabel || t('cancel')}</button>{request?.choices?.map((choice) => <button key={choice.value} type="button" onClick={() => finishDialog(choice.value)} className={`h-9 rounded-xl px-4 text-xs font-semibold ${choice.primary ? 'bg-accent text-accent-foreground hover:bg-accent-hover' : 'border border-border bg-surface hover:bg-surface-hover'}`}>{choice.label}</button>)}{!request?.choices && <button disabled={!valid} className={`h-9 rounded-xl px-4 text-xs font-semibold disabled:cursor-not-allowed disabled:bg-accent-disabled disabled:text-text-disabled ${request?.danger ? 'bg-danger text-white hover:brightness-110' : 'bg-accent text-accent-foreground hover:bg-accent-hover active:bg-accent-active'}`}>{request?.confirmLabel || t('confirm')}</button>}</div>
          </form>
        </Dialog.Popup>
      </Dialog.Viewport>
    </Dialog.Portal>
  </Dialog.Root>
}
