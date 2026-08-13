import { Download, Pause, Play, Search } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useI18n } from '../../lib/i18n'

export function LogViewer({ nodeID, containerId }: { nodeID: string; containerId: string }) {
  const { language } = useI18n(); const zh = language === 'zh-CN'
  const [lines, setLines] = useState<string[]>([])
  const [paused, setPaused] = useState(false)
  const [search, setSearch] = useState('')
  const [connected, setConnected] = useState(false)
  const backlog = useRef<string[]>([])
  const end = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:'
    const socket = new WebSocket(`${protocol}//${location.host}/ws/nodes/${encodeURIComponent(nodeID)}/containers/${containerId}/logs?tail=500`)
    socket.onopen = () => setConnected(true)
    socket.onclose = () => setConnected(false)
    socket.onmessage = (event) => {
      const next = String(event.data).split('\n').filter(Boolean)
      if (paused) backlog.current.push(...next)
      else setLines((current) => [...current, ...next].slice(-5000))
    }
    return () => socket.close()
  }, [nodeID, containerId, paused])
  useEffect(() => { if (!paused) end.current?.scrollIntoView({ behavior: 'smooth' }) }, [lines, paused])
  const visible = useMemo(() => search ? lines.filter((line) => line.toLowerCase().includes(search.toLowerCase())) : lines, [lines, search])
  const toggle = () => { if (paused && backlog.current.length) { setLines((current) => [...current, ...backlog.current].slice(-5000)); backlog.current = [] }; setPaused(!paused) }
  const download = () => { const url = URL.createObjectURL(new Blob([lines.join('\n')], { type: 'text/plain' })); const anchor = document.createElement('a'); anchor.href = url; anchor.download = `${containerId.slice(0, 12)}.log`; anchor.click(); URL.revokeObjectURL(url) }
  return <div><div className="mb-3 flex items-center gap-2"><span className={`size-1.5 rounded-full ${connected ? 'bg-success' : 'bg-neutral-status'}`} /><span className="text-xs">{zh ? '实时' : 'Live'}</span><label className="ml-auto flex h-8 w-56 items-center gap-2 rounded-md border border-border bg-surface px-2 text-xs"><Search className="size-3.5 text-text-subtle" /><input value={search} onChange={(event) => setSearch(event.target.value)} className="min-w-0 flex-1 bg-transparent outline-none" placeholder={zh ? '搜索日志…' : 'Search logs…'} /></label><button onClick={toggle} className="grid size-8 place-items-center rounded-md border border-border bg-surface" title={paused ? (zh ? '继续' : 'Continue') : (zh ? '暂停' : 'Pause')}>{paused ? <Play className="size-3.5" /> : <Pause className="size-3.5" />}</button><button onClick={download} className="grid size-8 place-items-center rounded-md border border-border bg-surface" title={zh ? '下载' : 'Download'}><Download className="size-3.5" /></button></div><div className="h-[56vh] overflow-auto rounded-md border border-border bg-[var(--code-background)] p-4 font-mono text-[11px] leading-5 text-text-muted">{visible.map((line, index) => <div key={`${index}-${line.slice(0, 12)}`} className={line.includes('ERROR') ? 'text-danger' : line.includes('WARN') ? 'text-warning' : ''}>{line}</div>)}{visible.length === 0 && <span className="text-text-subtle">{zh ? '等待输出…' : 'Waiting for output…'}</span>}<div ref={end} /></div></div>
}
