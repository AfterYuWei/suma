import { useId } from 'react'
import { Input } from '../../components/ui/input'
import { Label } from '../../components/ui/label'

export function PollIntervalInput({ value, onChange, zh }: { value: number; onChange: (value: number) => void; zh: boolean }) {
  const id = useId()
  return <div className="space-y-1.5"><Label htmlFor={id}>{zh ? '轮询间隔（秒）' : 'Polling interval (seconds)'}</Label><Input id={id} required min={30} max={86400} type="number" value={value} onChange={event => onChange(Number(event.target.value))} /><p className="text-xs text-muted-foreground">30–86400</p></div>
}
