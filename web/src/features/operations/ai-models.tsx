import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { ChevronDown, Plus, RefreshCw, Settings2, X } from 'lucide-react'
import { Button } from '../../components/ui/button'
import { Checkbox } from '../../components/ui/checkbox'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '../../components/ui/dialog'
import { Input } from '../../components/ui/input'
import { Spinner } from '../../components/ui/spinner'
import { api, ApiError } from '../../lib/api'
import { ErrorText } from './common'
import { toggle, useOpsText } from './helpers'

interface Connection { version: number; endpoint: string; api_key: string; allow_private: boolean; allow_insecure: boolean }

export function AIModels({ value, onChange, connection }: { value: string[]; onChange: (models: string[]) => void; connection: Connection }) {
  const t = useOpsText()
  const [open, setOpen] = useState(false)
  const [selected, setSelected] = useState<string[]>([])
  const [added, setAdded] = useState<string[]>([])
  const [search, setSearch] = useState('')
  const [manual, setManual] = useState('')
  const [manualError, setManualError] = useState('')
  const [collapsed, setCollapsed] = useState(false)
  const discover = useMutation({ mutationFn: (input: Connection) => api<{ models: string[] }>('/ai/settings/models', { method: 'POST', body: JSON.stringify(input) }) })
  const start = (probe = false) => {
    setSelected([...value]); setAdded([]); setSearch(''); setManual(''); setManualError(''); setCollapsed(false); discover.reset(); setOpen(true)
    if (probe) discover.mutate(connection)
  }
  const candidates = [...new Set([...(discover.data?.models || []), ...value, ...added, ...selected])]
  const visible = candidates.filter(model => model.toLowerCase().includes(search.toLowerCase().trim()))
  const selectedVisible = visible.filter(model => selected.includes(model)).length
  const add = () => {
    const models = [...new Set(manual.split(/[,，\n]/).map(model => model.trim()).filter(Boolean))]
    if (!models.length) return
    if (models.some(model => new TextEncoder().encode(model).length > 256 || [...model].some(character => { const code = character.charCodeAt(0); return code < 32 || (code >= 127 && code <= 159) })) || new Set([...selected, ...models]).size > 200) {
      setManualError(t('模型名称须为 1–256 字符，最多保留 200 个模型。', 'Model IDs must contain 1–256 characters. Select at most 200 models.')); return
    }
    setAdded(previous => [...new Set([...previous, ...models])]); setSelected(previous => [...new Set([...previous, ...models])]); setManual(''); setManualError(''); setSearch(''); setCollapsed(false)
  }
  return <>
    <section className="space-y-3 rounded-xl border p-4" aria-label={t('模型列表', 'Model list')}>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 space-y-1"><h3 className="text-sm font-medium">{t('模型', 'Models')} <span className="text-destructive">*</span></h3><p className="text-xs text-muted-foreground">{t('添加此服务支持的模型，供工作台切换或设为默认项。', 'Add supported models for workbench selection and the saved default.')}</p></div>
        <Button type="button" variant="outline" size="sm" onClick={() => start()}><Settings2 />{t('配置模型', 'Configure models')}</Button>
      </div>
      <div className="flex min-h-9 flex-wrap items-center gap-1.5 rounded-lg border bg-background px-2 py-1.5">
        {value.length ? value.map(model => <span key={model} className="inline-flex min-w-0 max-w-full items-center gap-1 rounded-md bg-muted px-2 py-0.5 text-xs"><span className="break-all">{model}</span><button type="button" className="shrink-0 rounded-sm p-0.5 text-muted-foreground hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring" aria-label={t(`移除模型 ${model}`, `Remove model ${model}`)} onClick={() => onChange(value.filter(id => id !== model))}><X className="size-3" /></button></span>) : <span className="text-xs text-muted-foreground">{t('尚未添加模型', 'No models added')}</span>}
      </div>
      <Button type="button" variant="ghost" size="sm" className="-ml-2" disabled={!connection.endpoint.trim()} onClick={() => start(true)}><RefreshCw />{t('自动探测模型', 'Auto-detect models')}</Button>
      <p className="text-xs text-muted-foreground">{t('探测使用当前填写的地址与连接开关；密钥留空时使用已保存密钥。应用后仍需保存 AI 设置。', 'Discovery uses the current URL and connection switches; a blank key uses the saved key. Save AI settings after applying.')}</p>
    </section>
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogContent className="flex max-h-[85dvh] flex-col sm:max-w-2xl">
        <DialogHeader><DialogTitle>{t('配置模型', 'Configure models')}</DialogTitle><DialogDescription>{t('选择要在此服务中保留的模型。可自动探测或手动输入模型名称。', 'Choose models to keep for this service. Discover models or enter their IDs manually.')}</DialogDescription></DialogHeader>
        <div className="min-h-0 space-y-4 overflow-y-auto p-0.5">
          <div className="flex items-center gap-2"><Input autoFocus aria-label={t('搜索模型', 'Search models')} placeholder={t('搜索模型…', 'Search models…')} value={search} onChange={event => setSearch(event.target.value)} /><Button type="button" size="sm" variant="outline" disabled={discover.isPending || !connection.endpoint.trim()} onClick={() => discover.mutate(connection)}>{discover.isPending ? <Spinner /> : <RefreshCw />}{t('自动探测', 'Discover')}</Button></div>
          <p className="text-xs text-muted-foreground" role="status">{discover.isPending ? t('正在获取模型列表…', 'Fetching available models…') : t(`当前模型：${candidates.length} 个`, `Available models: ${candidates.length}`)}</p>
          <ErrorText error={discover.error} />
          {discover.isError && <p className="text-xs text-muted-foreground">{discover.error instanceof ApiError && discover.error.code === 10007 ? t('来源校验失败：反向代理需保留原始 Host 和端口，并核对 SUMA_BROWSER_ORIGIN。允许 HTTP 模型连接不能修复此错误。', 'Origin check failed: preserve the original Host and port in your reverse proxy and check SUMA_BROWSER_ORIGIN. Allowing HTTP model connections cannot resolve this error.') : t('请检查基础地址、密钥和连接开关。服务不支持 /models 时，可在下方手动添加。', 'Check the base URL, key and connection switches. If /models is unsupported, add model IDs below.')}</p>}
          {candidates.length > 0 && <div className="rounded-lg border">
            <div className="flex items-center gap-3 border-b px-3 py-2.5">
              <Checkbox className="rounded-full" aria-label={t('选择所有搜索结果', 'Select all search results')} checked={visible.length > 0 && selectedVisible === visible.length} indeterminate={selectedVisible > 0 && selectedVisible < visible.length} disabled={!visible.length} onCheckedChange={checked => setSelected(previous => checked ? [...new Set([...previous, ...visible])] : previous.filter(model => !visible.includes(model)))} />
              <button type="button" className="flex min-w-0 flex-1 items-center justify-between gap-2 text-sm" aria-expanded={!collapsed} onClick={() => setCollapsed(previous => !previous)}><span>{t('可用模型', 'Available models')} ({visible.length})</span><span className="flex items-center gap-2 text-xs text-muted-foreground">{selectedVisible} / {visible.length}<ChevronDown className={`size-4 transition-transform ${collapsed ? '-rotate-90' : ''}`} /></span></button>
            </div>
            {!collapsed && <div className="grid max-h-60 gap-x-5 gap-y-3 overflow-y-auto p-3 sm:grid-cols-2">{visible.map(model => <label key={model} className="flex min-w-0 cursor-pointer items-start gap-3 text-sm"><Checkbox className="mt-0.5 rounded-full" checked={selected.includes(model)} onCheckedChange={() => setSelected(previous => toggle(previous, model))} /><span className="min-w-0 break-all">{model}</span></label>)}{!visible.length && <p className="text-xs text-muted-foreground">{t('没有匹配的模型', 'No matching models')}</p>}</div>}
          </div>}
          {discover.isSuccess && !candidates.length && <p className="text-xs text-muted-foreground">{t('服务返回了空模型列表，可手动添加模型。', 'The service returned no models. You can add model IDs manually.')}</p>}
          <div className="space-y-2"><label htmlFor="ai-manual-models" className="text-xs font-medium">{t('手动添加模型', 'Add model IDs manually')}</label><div className="flex items-center gap-2"><Input id="ai-manual-models" value={manual} placeholder={t('输入模型名称，多个模型用逗号分隔', 'Enter model IDs separated by commas')} onChange={event => setManual(event.target.value)} onKeyDown={event => { if (event.key === 'Enter' && !event.nativeEvent.isComposing) { event.preventDefault(); add() } }} /><Button type="button" size="sm" variant="outline" disabled={!manual.trim()} onClick={add}><Plus />{t('添加', 'Add')}</Button></div><ErrorText error={manualError} /></div>
          <p className="text-xs text-muted-foreground" role="status">{t(`已选 ${selected.length} 个模型`, `${selected.length} models selected`)}</p>
          {selected.length > 200 && <ErrorText error={t('最多保留 200 个模型，请减少选择。', 'Select at most 200 models. Deselect some models to continue.')} />}
        </div>
        <DialogFooter className="border-t bg-muted/30"><Button type="button" variant="outline" onClick={() => setOpen(false)}>{t('取消', 'Cancel')}</Button><Button type="button" disabled={selected.length > 200} onClick={() => { onChange(selected); setOpen(false) }}>{t('应用', 'Apply')}</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  </>
}
