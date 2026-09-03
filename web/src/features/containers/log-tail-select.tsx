import { ListEnd } from 'lucide-react'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../../components/ui/select'
import { type LogTail, logTailOptions, useUIStore } from '../../stores/ui'

export function LogTailSelect({ zh }: { zh: boolean }) {
  const tail = useUIStore((state) => state.logTail)
  const setTail = useUIStore((state) => state.setLogTail)

  return <div className="flex items-center gap-2">
    <span className="text-xs text-muted-foreground">{zh ? '最大行数' : 'Max lines'}</span>
    <Select<LogTail> value={tail} onValueChange={(value) => { if (value !== null) setTail(value) }}>
      <SelectTrigger size="sm" className="min-w-24">
        <ListEnd />
        <SelectValue />
      </SelectTrigger>
      <SelectContent align="end">
        {logTailOptions.map((value) => <SelectItem key={value} value={value}>{value} {zh ? '行' : 'lines'}</SelectItem>)}
      </SelectContent>
    </Select>
  </div>
}
