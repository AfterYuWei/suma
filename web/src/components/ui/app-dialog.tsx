import { AlertTriangle } from 'lucide-react'
import { useEffect, useState, type FormEvent } from 'react'
import { useI18n } from '../../lib/i18n'
import { finishDialog, useDialogStore } from '../../stores/dialog'
import { Alert, AlertDescription } from './alert'
import { Button } from './button'
import { Checkbox } from './checkbox'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from './dialog'
import { Input } from './input'

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
  const complete = () => finishDialog(request?.checkbox ? { value: request.input ? value : 'confirmed', checked } : request?.input ? value : 'confirmed')
  const submit = (event: FormEvent) => { event.preventDefault(); if (!request?.choices && valid) complete() }

  return <Dialog open={!!request} onOpenChange={(open) => { if (!open) finishDialog(null) }}>
    <DialogContent className="max-w-sm" showCloseButton={false}>
      <form onSubmit={submit}>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            {request?.danger && <AlertTriangle className="size-4 text-destructive" aria-hidden="true" />}
            {request?.title}
          </DialogTitle>
          {request?.description && <DialogDescription>{request.description}</DialogDescription>}
        </DialogHeader>
        <div className="flex w-full flex-col items-start gap-3">
          {request?.danger && (
            <Alert variant="destructive">
              <AlertTriangle />
              <AlertDescription>{t('dangerWarning')}</AlertDescription>
            </Alert>
          )}
          {request?.input && (
            <div className="flex w-full flex-col items-start gap-1.5">
              <label className="w-full">
                <span className="mb-1.5 block text-sm font-medium">{request.input.label}</span>
                <Input autoFocus value={value} onChange={(event) => setValue(event.target.value)} placeholder={request.input.placeholder} aria-invalid={!valid} />
              </label>
              {request.input.requiredValue && (
                <p className={`text-xs ${valid ? 'text-muted-foreground' : 'text-destructive'}`}>
                  {t('typeToConfirm', { value: request.input.requiredValue })}
                </p>
              )}
            </div>
          )}
          {request?.checkbox && (
            <label className="flex items-start gap-2.5">
              <Checkbox checked={checked} onCheckedChange={(next) => setChecked(Boolean(next))} className="mt-0.5" />
              <span className="flex flex-col gap-0.5">
                <span className="text-sm">{request.checkbox.label}</span>
                {request.checkbox.description && <span className="text-xs text-muted-foreground">{request.checkbox.description}</span>}
              </span>
            </label>
          )}
        </div>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={() => finishDialog(null)}>{request?.cancelLabel || t('cancel')}</Button>
          {request?.choices?.map((choice) => (
            <Button key={choice.value} type="button" variant={choice.primary ? 'default' : 'outline'} onClick={() => finishDialog(choice.value)}>
              {choice.label}
            </Button>
          ))}
          {!request?.choices && (
            <Button type="submit" variant={request?.danger ? 'destructive' : 'default'} disabled={!valid}>
              {request?.confirmLabel || t('confirm')}
            </Button>
          )}
        </DialogFooter>
      </form>
    </DialogContent>
  </Dialog>
}
