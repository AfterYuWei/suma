import { ChevronLeft, ChevronRight } from 'lucide-react'
import { type ListPageSize, listPageSizeOptions } from '../../stores/ui'
import { Button } from './button'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from './select'

export function ListPagination({ total, page, pageCount, pageSize, setPage, setPageSize, zh }: {
  total: number
  page: number
  pageCount: number
  pageSize: ListPageSize
  setPage: (page: number) => void
  setPageSize: (pageSize: ListPageSize) => void
  zh: boolean
}) {
  return <nav aria-label={zh ? '列表分页' : 'List pagination'} className="flex flex-wrap items-center justify-between gap-3 pt-3 text-sm text-muted-foreground">
    <span className="tabular-nums">{zh ? `共 ${total} 条` : `${total} items`}</span>
    <div className="flex flex-wrap items-center gap-2">
      <span>{zh ? '每页' : 'Per page'}</span>
      <Select<ListPageSize> value={pageSize} onValueChange={(value) => { if (value !== null) setPageSize(value) }}>
        <SelectTrigger size="sm" className="w-20" aria-label={zh ? '每页数量' : 'Items per page'}><SelectValue /></SelectTrigger>
        <SelectContent align="end">
          {listPageSizeOptions.map((size) => <SelectItem key={size} value={size}>{size}</SelectItem>)}
        </SelectContent>
      </Select>
      <span className="min-w-16 text-center tabular-nums">{page + 1} / {pageCount}</span>
      <Button variant="outline" size="icon-sm" disabled={page === 0} aria-label={zh ? '上一页' : 'Previous page'} onClick={() => setPage(page - 1)}><ChevronLeft /></Button>
      <Button variant="outline" size="icon-sm" disabled={page >= pageCount - 1} aria-label={zh ? '下一页' : 'Next page'} onClick={() => setPage(page + 1)}><ChevronRight /></Button>
    </div>
  </nav>
}
