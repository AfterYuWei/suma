import { type UIEventHandler, useCallback, useLayoutEffect, useRef } from 'react'

const bottomThreshold = 32

// Container log surfaces start at the newest output and keep following while
// the reader remains near the bottom. Scrolling up disables follow mode until
// the reader returns to the bottom, so incoming logs do not steal their place.
export function useLogAutoScroll<T extends HTMLElement>(contentKey: unknown, sourceKey: unknown) {
  const viewportRef = useRef<T>(null)
  const following = useRef(true)
  const initialized = useRef(false)

  useLayoutEffect(() => {
    following.current = true
    initialized.current = false
  }, [sourceKey])

  useLayoutEffect(() => {
    const viewport = viewportRef.current
    if (!viewport || (initialized.current && !following.current)) return
    viewport.scrollTop = viewport.scrollHeight
    initialized.current = true
    following.current = true
  }, [contentKey, sourceKey])

  const onScroll = useCallback<UIEventHandler<T>>((event) => {
    const viewport = event.currentTarget
    const distance = viewport.scrollHeight - viewport.scrollTop - viewport.clientHeight
    following.current = distance <= bottomThreshold
  }, [])

  return { viewportRef, onScroll }
}
