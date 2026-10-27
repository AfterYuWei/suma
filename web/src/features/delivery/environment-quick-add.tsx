import { Eye, EyeOff } from 'lucide-react'
import { useMemo, useState } from 'react'
import { Button } from '../../components/ui/button'
import { Checkbox } from '../../components/ui/checkbox'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '../../components/ui/dialog'
import { Textarea } from '../../components/ui/textarea'
import { cn } from '../../lib/utils'
import { parseEnvironmentInput, planEnvironmentImport } from './environment-input'
import type { CDEnvironmentVariable } from './types'

export function EnvironmentQuickAdd({ variables, zh, onAdd, onClose }: {
  variables: CDEnvironmentVariable[]
  zh: boolean
  onAdd: (variables: CDEnvironmentVariable[]) => void
  onClose: () => void
}) {
  const [source, setSource] = useState('')
  const [showValues, setShowValues] = useState(false)
  const [replace, setReplace] = useState(false)
  const preview = useMemo(() => {
    try {
      const incoming = parseEnvironmentInput(source, zh)
      return { incoming, plan: planEnvironmentImport(variables, incoming, replace, zh), error: '' }
    } catch (error) {
      return { incoming: [], plan: undefined, error: (error as Error).message }
    }
  }, [source, variables, replace, zh])
  const canAdd = !!preview.plan && preview.plan.added + preview.plan.replaced > 0
  const apply = () => {
    if (!canAdd || !preview.plan) return
    onAdd(preview.plan.variables)
    onClose()
  }

  return <Dialog open onOpenChange={open => { if (!open) onClose() }}>
    <DialogContent className="max-h-[calc(100dvh-2rem)] grid-cols-1 overflow-y-auto sm:max-w-xl">
      <DialogHeader>
        <DialogTitle>{zh ? '快速添加环境变量' : 'Quick add environment variables'}</DialogTitle>
        <DialogDescription>{zh ? '粘贴多行 KEY=VALUE，支持空行、注释、export 和引号。$ 与反斜杠按原文保留，不执行或展开。' : 'Paste KEY=VALUE lines, with optional comments, export and quotes. Dollar signs and backslashes stay literal; nothing is executed or expanded.'}</DialogDescription>
      </DialogHeader>
      <div className="flex flex-col gap-2">
        <label className="text-sm font-medium" htmlFor="cd-quick-environment">KEY=VALUE</label>
        <Textarea id="cd-quick-environment" value={source} onChange={event => setSource(event.target.value)} onKeyDown={event => {
          if (event.key === 'Enter' && (event.ctrlKey || event.metaKey)) { event.preventDefault(); event.stopPropagation(); apply() }
        }} aria-invalid={!!preview.error} aria-describedby="cd-quick-environment-feedback" autoComplete="off" spellCheck={false} maxLength={300000} rows={8} placeholder={'APP_ENV=production\nAPP_VERSION=1.2.3\nPASSWORD="your value"'} className={cn('h-48 resize-y [field-sizing:fixed] font-mono text-sm', !showValues && '[-webkit-text-security:disc]')} />
        <Button type="button" size="sm" variant="ghost" className="self-start" onClick={() => setShowValues(value => !value)}>{showValues ? <EyeOff /> : <Eye />}{showValues ? (zh ? '隐藏输入值' : 'Hide entered values') : (zh ? '显示输入值' : 'Show entered values')}</Button>
      </div>
      <label className="flex items-center gap-2 text-sm"><Checkbox checked={replace} onCheckedChange={checked => setReplace(checked === true)} />{zh ? '替换同名变量' : 'Replace matching variables'}</label>
      <div id="cd-quick-environment-feedback" className="min-w-0 rounded-lg bg-muted/40 p-3 text-xs">
        {preview.error ? <p role="alert" className="text-destructive">{preview.error}</p> : <>
          <p role="status">{zh ? `新增 ${preview.plan?.added ?? 0} 项，替换 ${preview.plan?.replaced ?? 0} 项，保留 ${preview.plan?.kept ?? 0} 项同名变量。` : `Add ${preview.plan?.added ?? 0}, replace ${preview.plan?.replaced ?? 0}, keep ${preview.plan?.kept ?? 0} matching variables.`}</p>
          {!!preview.incoming.length && <p className="mt-2 max-h-20 overflow-y-auto break-all font-mono text-muted-foreground">{preview.incoming.map(row => row.name).join(' · ')}</p>}
        </>}
      </div>
      <DialogFooter>
        <Button type="button" variant="outline" onClick={onClose}>{zh ? '取消' : 'Cancel'}</Button>
        <Button type="button" disabled={!canAdd} onClick={apply}>{zh ? '添加到表单' : 'Add to form'}</Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
}
