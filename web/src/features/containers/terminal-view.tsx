import { Terminal } from '@xterm/xterm'
import '@xterm/xterm/css/xterm.css'
import { useEffect, useRef } from 'react'
import { useUIStore } from '../../stores/ui'

export function TerminalView({ nodeID, containerId }: { nodeID: string; containerId: string }) {
  const host = useRef<HTMLDivElement>(null)
  const theme = useUIStore((state) => state.theme)
  useEffect(() => {
    if (!host.current) return
    const styles = getComputedStyle(document.documentElement)
    const terminal = new Terminal({ cursorBlink: true, convertEol: true, fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace', fontSize: 12, theme: {
      background: styles.getPropertyValue('--background'),
      foreground: styles.getPropertyValue('--foreground'),
      cursor: styles.getPropertyValue('--primary'),
      selectionBackground: styles.getPropertyValue('--accent'),
    } })
    terminal.open(host.current)
    const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:'
    const socket = new WebSocket(`${protocol}//${location.host}/ws/nodes/${encodeURIComponent(nodeID)}/containers/${containerId}/terminal`)
    socket.binaryType = 'arraybuffer'
    socket.onopen = () => terminal.focus()
    socket.onmessage = (event) => terminal.write(typeof event.data === 'string' ? event.data : new Uint8Array(event.data))
    socket.onclose = () => terminal.write('\r\n\x1b[90mSession disconnected\x1b[0m')
    const input = terminal.onData((data) => { if (socket.readyState === WebSocket.OPEN) socket.send(JSON.stringify({ type: 'input', data })) })
    const resize = new ResizeObserver(([entry]) => { const cols = Math.max(40, Math.floor(entry.contentRect.width / 7.25)); const rows = Math.max(10, Math.floor(entry.contentRect.height / 17)); terminal.resize(cols, rows); if (socket.readyState === WebSocket.OPEN) socket.send(JSON.stringify({ type: 'resize', cols, rows })) })
    resize.observe(host.current)
    return () => { resize.disconnect(); input.dispose(); socket.close(); terminal.dispose() }
  }, [nodeID, containerId, theme])
  return <div ref={host} className="h-[56vh] overflow-hidden" />
}
