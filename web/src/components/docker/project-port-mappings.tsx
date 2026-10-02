import { useEffect, useId, useRef, useState, type ReactNode } from 'react'
import { ArrowRight, Plus, Trash2 } from 'lucide-react'
import { configureServicePort, configValue, editConfig, parseServicePort, REMOVE } from '../../features/compose/document'
import { Alert, AlertDescription } from '../ui/alert'
import { Button } from '../ui/button'
import { Input } from '../ui/input'
import { Label } from '../ui/label'
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Textarea } from '../ui/textarea'

interface Context {
  compose: string
  service: string
  zh: boolean
  disabled?: boolean
  onCompose: (source: string) => void
  pending: (key: string, pending: boolean) => void
}
function portError(error: unknown, zh: boolean) {
  const message = error instanceof Error ? error.message : String(error)
  if (!zh) return message
  return ({
    'Container port must be between 1 and 65535': '容器端口须为 1～65535 的整数。',
    'Host port must be between 1 and 65535, or an ascending range': '主机端口须为 1～65535，或递增范围；留空由 Docker 分配。',
    'Port mapping already exists': '该端口映射已存在。',
    'Port mapping no longer exists': '端口配置已变化，请重新打开表单。'
  } as Record<string, string>)[message] ?? message
}

function PortMappingForm({ context: c, port, index, onClose }: { context: Context; port?: Record<string, unknown>; index?: number; onClose?: () => void }) {
  const { pending, service } = c
  const id = useId(), first = useRef<HTMLInputElement>(null)
  const signature = JSON.stringify(port)
  const [published, setPublished] = useState(String(port?.published ?? ''))
  const [target, setTarget] = useState(String(port?.target ?? ''))
  const [protocol, setProtocol] = useState(String(port?.protocol ?? 'tcp'))
  const [host, setHost] = useState(String(port?.host_ip ?? ''))
  const [error, setError] = useState('')
  const dirty = published !== String(port?.published ?? '') || target !== String(port?.target ?? '') || host !== String(port?.host_ip ?? '') || protocol !== String(port?.protocol ?? 'tcp')
  useEffect(() => {
    const initial = signature ? JSON.parse(signature) as Record<string, unknown> : undefined
    setPublished(String(initial?.published ?? '')); setTarget(String(initial?.target ?? ''))
    setProtocol(String(initial?.protocol ?? 'tcp')); setHost(String(initial?.host_ip ?? '')); setError('')
  }, [signature])
  useEffect(() => {
    const key = `ports:${service}:${index ?? 'new'}`
    pending(key, dirty)
    return () => pending(key, false)
  }, [pending, service, index, dirty])
  const apply = (keepOpen = false) => {
    if (c.disabled || (index !== undefined && !dirty)) return
    try {
      const next: Record<string, unknown> = { ...port, target: /^\d+$/.test(target.trim()) ? Number(target.trim()) : target.trim() }
      if (published.trim()) Object.assign(next, { published: published.trim() }); else delete next.published
      if (host.trim()) Object.assign(next, { host_ip: host.trim() }); else delete next.host_ip
      if (protocol === 'tcp' && port?.protocol === undefined) delete next.protocol
      else Object.assign(next, { protocol })
      c.onCompose(configureServicePort(c.compose, c.service, next, index))
      setError('')
      if (index === undefined && keepOpen) { setPublished(''); setTarget(''); first.current?.focus() }
      else onClose?.()
    } catch (failure) { setError(portError(failure, c.zh)) }
  }
  return <form className="space-y-2 border-b py-3" data-testid={index === undefined ? 'new-port-mapping' : `port-mapping-${index}`} onSubmit={event => { event.preventDefault(); apply() }}>
    <div className="grid grid-cols-[minmax(0,1fr)_24px_minmax(0,1fr)] items-end gap-2 lg:grid-cols-[minmax(0,1fr)_24px_minmax(0,1fr)_124px_auto]">
      <div className="min-w-0 space-y-1.5"><Label htmlFor={`${id}-published`}>{c.zh ? '主机端口 published' : 'Host port published'}</Label><Input ref={first} id={`${id}-published`} className="font-medium" autoFocus={index === undefined} value={published} placeholder={c.zh ? '如 8080；留空自动分配' : '8080; blank for automatic'} autoComplete="off" onChange={event => { setPublished(event.target.value); setError('') }} onBlur={() => { if (index !== undefined) apply() }} /></div>
      <ArrowRight className="mb-2 size-4 text-muted-foreground" aria-hidden />
      <div className="min-w-0 space-y-1.5"><Label htmlFor={`${id}-target`}>{c.zh ? '容器端口 target' : 'Container port target'}</Label><Input id={`${id}-target`} className="font-medium" inputMode="numeric" value={target} placeholder="80" autoComplete="off" onChange={event => { setTarget(event.target.value); setError('') }} onBlur={() => { if (index !== undefined) apply() }} /></div>
      <div className="col-span-2 flex flex-col gap-1.5 lg:col-span-1"><Label htmlFor={`${id}-protocol`}>{c.zh ? '协议 protocol' : 'Protocol protocol'}</Label><Select items={['tcp', 'udp', 'sctp'].map(value => ({ value, label: value.toUpperCase() }))} value={protocol} onValueChange={value => setProtocol(String(value))}><SelectTrigger id={`${id}-protocol`} className="w-full"><SelectValue /></SelectTrigger><SelectContent align="start" alignItemWithTrigger={false} className="min-w-0"><SelectGroup>{['tcp', 'udp', 'sctp'].map(value => <SelectItem key={value} value={value}>{value.toUpperCase()}</SelectItem>)}</SelectGroup></SelectContent></Select></div>
      {index !== undefined && <Button type="button" variant="ghost" size="icon-sm" aria-label={c.zh ? `移除端口映射 ${index + 1}` : `Remove port mapping ${index + 1}`} onClick={() => c.onCompose(editConfig(c.compose, ['services', c.service, 'ports', index], REMOVE))}><Trash2 /></Button>}
    </div>
    <details open={!!port?.host_ip} className="text-xs text-muted-foreground"><summary className="cursor-pointer py-1">{c.zh ? '绑定地址 · 可选' : 'Bind address · optional'}{host ? ` · ${host}` : ''}</summary><div className="max-w-sm space-y-1.5 py-2"><Label htmlFor={`${id}-host`}>{c.zh ? '绑定地址 host_ip' : 'Bind address host_ip'}</Label><Input id={`${id}-host`} value={host} placeholder={c.zh ? '默认所有地址；如 127.0.0.1' : 'All addresses; e.g. 127.0.0.1'} onChange={event => { setHost(event.target.value); setError('') }} onBlur={() => { if (index !== undefined) apply() }} /></div></details>
    {error && <Alert variant="destructive"><AlertDescription>{error}</AlertDescription></Alert>}
    {(index === undefined || dirty) && <div className="flex flex-wrap gap-2"><Button type="submit" size="sm" disabled={c.disabled}>{index === undefined ? (c.zh ? '添加映射' : 'Add mapping') : (c.zh ? '保存映射' : 'Save mapping')}</Button>{index === undefined && <Button type="button" variant="outline" size="sm" disabled={c.disabled} onClick={() => apply(true)}>{c.zh ? '添加并继续' : 'Add & continue'}</Button>}<Button type="button" size="sm" variant="ghost" onClick={() => { if (index === undefined) onClose?.(); else { setPublished(String(port?.published ?? '')); setTarget(String(port?.target ?? '')); setProtocol(String(port?.protocol ?? 'tcp')); setHost(String(port?.host_ip ?? '')); setError('') } }}>{c.zh ? '取消' : 'Cancel'}</Button></div>}
  </form>
}

export function PortMappings({ context: c, sourceLink }: { context: Context; sourceLink: (index: number) => ReactNode }) {
  const { pending, service } = c
  const rows = (configValue(c.compose, ['services', c.service, 'ports']) ?? []) as unknown[]
  const [adding, setAdding] = useState(rows.length === 0), [bulk, setBulk] = useState(false), [paste, setPaste] = useState(''), [error, setError] = useState('')
  useEffect(() => { const key = `ports:${service}:batch`; pending(key, !!paste); return () => pending(key, false) }, [pending, service, paste])
  return <div className="space-y-2">
    <div className="space-y-1"><p className="text-sm font-medium">{c.zh ? '主机端口 → 容器端口' : 'Host port → container port'}</p><p className="text-xs text-muted-foreground">{c.zh ? '例如 8080 → 80，通过目标节点的 8080 访问容器内的 80。' : 'For example, 8080 → 80 reaches container port 80 through port 8080 on the target node.'}</p></div>
    {rows.map((value, index) => {
      const port = parseServicePort(value)
      return !port || ['target', 'published', 'host_ip', 'protocol'].some(key => port[key] != null && (typeof port[key] === 'object' || String(port[key]).includes('$'))) || !/^\d+$/.test(String(port.target))
        ? <div key={index} className="border-b py-3">{sourceLink(index)}</div>
        : <PortMappingForm key={index} context={c} port={port} index={index} />
    })}
    {adding && <PortMappingForm context={c} onClose={() => setAdding(false)} />}
    <div className="flex flex-wrap gap-2 py-2">{!adding && <Button variant="outline" size="sm" onClick={() => setAdding(true)}><Plus />{c.zh ? '添加端口映射' : 'Add port mapping'}</Button>}<Button variant="ghost" size="sm" onClick={() => setBulk(!bulk)}>{c.zh ? '批量填写' : 'Fill in bulk'}</Button></div>
    {bulk && <form className="space-y-3 border-b pb-3" data-testid="bulk-port-mappings" onSubmit={event => {
      event.preventDefault()
      if (c.disabled) return
      try {
        const lines = paste.split('\n').map(line => line.trim()).filter(Boolean)
        if (!lines.length) throw new Error(c.zh ? '请填写至少一行映射。' : 'Enter at least one mapping.')
        let next = c.compose
        for (const [index, line] of lines.entries()) {
          const port = parseServicePort(line)
          if (!port) throw new Error(c.zh ? `第 ${index + 1} 行格式无效，使用 8080:80 或 127.0.0.1:8080:80/tcp。` : `Invalid line ${index + 1}; use 8080:80 or 127.0.0.1:8080:80/tcp.`)
          try { next = configureServicePort(next, c.service, port) } catch (failure) { throw new Error(`${c.zh ? '第' : 'Line'} ${index + 1}: ${portError(failure, c.zh)}`) }
        }
        c.onCompose(next); setPaste(''); setBulk(false); setError('')
      } catch (failure) { setError(portError(failure, c.zh)) }
    }}><Label htmlFor={`bulk-ports-${c.service}`}>{c.zh ? '每行一条 主机端口:容器端口[/协议]' : 'One host:container[/protocol] mapping per line'}</Label><Textarea id={`bulk-ports-${c.service}`} value={paste} onChange={event => { setPaste(event.target.value); setError('') }} placeholder={'8080:80\n8443:443\n5353:53/udp'} rows={4} />{error && <Alert variant="destructive"><AlertDescription>{error}</AlertDescription></Alert>}<div className="flex gap-2"><Button size="sm" type="submit">{c.zh ? '添加全部' : 'Add all'}</Button><Button size="sm" type="button" variant="ghost" onClick={() => { setBulk(false); setPaste(''); setError('') }}>{c.zh ? '取消' : 'Cancel'}</Button></div></form>}
  </div>
}
