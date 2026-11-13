import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../../components/ui/select'
import { ErrorState } from '../../components/ui/error-state'
import { useSchedules } from './queries'

import { triggerSourceLabel } from './history-labels'

export function SourceFilter({ value, onChange, zh, includeUnrecorded = false }: { value: string; onChange: (value: string) => void; zh: boolean; includeUnrecorded?: boolean }) {
  const sources = ['manual', 'scheduled', 'webhook', 'system', ...(includeUnrecorded ? ['unrecorded'] : [])]
  return <Select value={value} onValueChange={value => { if (value !== null) onChange(value) }}>
    <SelectTrigger className="w-36 max-w-full" aria-label={zh ? '触发来源' : 'Trigger source'}><SelectValue>{value ? triggerSourceLabel(value, zh) : zh ? '全部来源' : 'All sources'}</SelectValue></SelectTrigger>
    <SelectContent><SelectItem value="">{zh ? '全部来源' : 'All sources'}</SelectItem>{sources.map(source => <SelectItem key={source} value={source}>{triggerSourceLabel(source, zh)}</SelectItem>)}</SelectContent>
  </Select>
}

export function RuleFilter({ value, onChange, zh }: { value: number | undefined; onChange: (value: number | undefined) => void; zh: boolean }) {
  // Global rules must remain selectable when their node child tasks are listed.
  const query = useSchedules(undefined, { scope: 'all', includeArchived: true })
  const selected = query.data?.find(rule => rule.id === value)
  return <>
    <Select value={value ? String(value) : ''} onValueChange={value => { if (value !== null) onChange(value ? Number(value) : undefined) }}>
      <SelectTrigger className="w-60 max-w-full" disabled={query.isPending || query.isError} aria-label={zh ? '自动化规则筛选' : 'Automation rule filter'}><SelectValue>{selected ? `#${selected.id} ${selected.name}` : value ? `#${value}` : zh ? '全部规则' : 'All rules'}</SelectValue></SelectTrigger>
      <SelectContent><SelectItem value="">{zh ? '全部规则' : 'All rules'}</SelectItem>{query.data?.map(rule => <SelectItem key={rule.id} value={String(rule.id)}>#{rule.id} {rule.name} · {rule.node_name || rule.node_id || (zh ? '控制平面' : 'Control plane')}{rule.archived_at ? (zh ? ' · 已归档' : ' · Archived') : ''}</SelectItem>)}</SelectContent>
    </Select>
    {query.isError && <ErrorState description={query.error.message} />}
  </>
}
