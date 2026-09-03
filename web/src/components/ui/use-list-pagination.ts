import { useEffect, useState } from 'react'
import { useUIStore } from '../../stores/ui'

export function useListPagination<T>(items: T[], resetKey?: string | number) {
  const pageSize = useUIStore((state) => state.listPageSize)
  const setPageSize = useUIStore((state) => state.setListPageSize)
  const [requestedPage, setRequestedPage] = useState(0)
  const pageCount = Math.max(1, Math.ceil(items.length / pageSize))
  const page = Math.min(requestedPage, pageCount - 1)

  useEffect(() => setRequestedPage(0), [pageSize, resetKey])

  return {
    items: items.slice(page * pageSize, (page + 1) * pageSize),
    total: items.length,
    page,
    pageCount,
    pageSize,
    setPage: setRequestedPage,
    setPageSize,
  }
}
