import { useEffect, useState } from 'react'

export function useSmallScreen() {
  const [small, setSmall] = useState(() => matchMedia('(max-width: 767px)').matches)
  useEffect(() => { const q = matchMedia('(max-width: 767px)'), update = () => setSmall(q.matches); q.addEventListener('change', update); return () => q.removeEventListener('change', update) }, [])
  return small
}
