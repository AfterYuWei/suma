import { useEffect, useRef, useState } from 'react'

export function useObservationStream(url: string | null, consume: (payload: string) => void, onGap?: () => void) {
  const handler = useRef(consume), gap = useRef(onGap)
  handler.current = consume; gap.current = onGap
  const [status, setStatus] = useState<'connecting' | 'live' | 'offline' | 'paused'>('connecting')
  useEffect(() => {
    if (!url) return
    const controller = new AbortController()
    let disposed = false, socket: WebSocket | undefined, timer: ReturnType<typeof setTimeout> | undefined, attempt = 0, openedAt = 0
    const stop = () => { clearTimeout(timer); if (socket) { socket.onclose = null; socket.close(); socket = undefined } }
    const connect = () => {
      if (disposed || document.hidden) return
      setStatus('connecting')
      openedAt = 0
      socket = new WebSocket(url)
      socket.onopen = () => { openedAt = Date.now(); setStatus('live') }
      socket.onmessage = e => { try { handler.current(String(e.data)) } catch { socket?.close() } }
      socket.onclose = () => { if (disposed || document.hidden) return; setStatus('offline'); gap.current?.(); if (openedAt > 0 && Date.now() - openedAt >= 10_000) attempt = 0; const delay = Math.min(15_000, 1000 * 2 ** attempt++) * (1 + Math.random() * 0.25); void fetch('/api/v1/auth/session', { credentials: 'include', signal: controller.signal }).then(response => { if (!disposed && response.status === 401) { location.reload(); return } if (!disposed && !document.hidden) timer = setTimeout(connect, delay) }).catch(() => { if (!disposed && !document.hidden) timer = setTimeout(connect, delay) }) }
      socket.onerror = () => socket?.close()
    }
    const visibility = () => { stop(); if (document.hidden) setStatus('paused'); else { gap.current?.(); connect() } }
    document.addEventListener('visibilitychange', visibility); connect()
    return () => { disposed = true; controller.abort(); stop(); document.removeEventListener('visibilitychange', visibility) }
  }, [url])
  return status
}
