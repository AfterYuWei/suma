import { Download, Pause, Play, Search } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { Button } from '../../components/ui/button'
import { Input } from '../../components/ui/input'
import { cn } from '../../lib/utils'
import { useI18n } from '../../lib/i18n'
import { demoMode, subscribeDemoStream } from '../../lib/api'

export function LogViewer({ nodeID, containerId }: { nodeID: string; containerId: string }) {
  const { language } = useI18n(); const zh = language === 'zh-CN'
  const [lines, setLines] = useState<string[]>([])
  const [paused, setPaused] = useState(false)
  const [search, setSearch] = useState('')
  const [connected, setConnected] = useState(false)
  const backlog = useRef<string[]>([])
  const end = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const consume = (payload: string) => {
      const next = payload.split('\n').filter(Boolean)
      if (paused) backlog.current.push(...next)
      else setLines((current) => [...current, ...next].slice(-5000))
    }
    if (demoMode) {
      let disposed = false
      let cleanup: () => void = () => undefined
      setConnected(true)
      void subscribeDemoStream('logs', consume).then((next) => { if (disposed) next(); else cleanup = next })
      return () => { disposed = true; cleanup(); setConnected(false) }
    }
    const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:'
    const socket = new WebSocket(`${protocol}//${location.host}/ws/nodes/${encodeURIComponent(nodeID)}/containers/${containerId}/logs?tail=500`)
    socket.onopen = () => setConnected(true)
    socket.onclose = () => setConnected(false)
    socket.onmessage = (event) => consume(String(event.data))
    return () => socket.close()
  }, [nodeID, containerId, paused])
  useEffect(() => { if (!paused) end.current?.scrollIntoView({ behavior: 'smooth' }) }, [lines, paused])
  const visible = useMemo(() => search ? lines.filter((line) => line.toLowerCase().includes(search.toLowerCase())) : lines, [lines, search])
  const toggle = () => { if (paused && backlog.current.length) { setLines((current) => [...current, ...backlog.current].slice(-5000)); backlog.current = [] }; setPaused(!paused) }
  const download = () => { const url = URL.createObjectURL(new Blob([lines.join('\n')], { type: 'text/plain' })); const anchor = document.createElement('a'); anchor.href = url; anchor.download = `${containerId.slice(0, 12)}.log`; anchor.click(); URL.revokeObjectURL(url) }
  return <div className="flex w-full flex-col gap-3">
    <div className="flex w-full flex-wrap items-center gap-2">
      <span className="inline-flex items-center gap-2 text-sm">
        <span className={cn('size-2 rounded-full', connected ? 'bg-emerald-500' : 'bg-muted-foreground/40')} aria-hidden="true" />
        {zh ? '实时' : 'Live'}
      </span>
      <div className="relative ml-auto w-full sm:w-60">
        <Search className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
        <Input value={search} onChange={(event) => setSearch(event.target.value)} placeholder={zh ? '搜索日志…' : 'Search logs…'} className="pl-8" />
      </div>
      <Button variant="outline" size="icon" aria-label={paused ? (zh ? '继续' : 'Continue') : (zh ? '暂停' : 'Pause')} onClick={toggle}>{paused ? <Play /> : <Pause />}</Button>
      <Button variant="outline" size="icon" aria-label={zh ? '下载' : 'Download'} onClick={download}><Download /></Button>
    </div>
    <div className="h-[56vh] w-full overflow-auto rounded-xl bg-card p-3 font-mono text-xs ring-1 ring-foreground/10">
      {visible.map((line, index) => <div key={`${index}-${line.slice(0, 12)}`} className={cn('whitespace-pre-wrap break-all leading-relaxed', line.includes('ERROR') ? 'text-red-500 dark:text-red-400' : line.includes('WARN') ? 'text-amber-500 dark:text-amber-400' : 'text-foreground/80')}>{line}</div>)}
      {visible.length === 0 && <p className="text-muted-foreground">{zh ? '等待输出…' : 'Waiting for output…'}</p>}
      <div ref={end} />
    </div>
  </div>
}
