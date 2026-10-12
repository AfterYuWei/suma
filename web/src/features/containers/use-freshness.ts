import { useEffect, useState } from 'react'

export function useFreshness(updatedAt: number, ttl = 30_000) {
  const [now, setNow] = useState(Date.now)
  useEffect(() => { const timer = setInterval(() => setNow(Date.now()), 1000); return () => clearInterval(timer) }, [])
  return updatedAt === 0 || now - updatedAt <= ttl
}
