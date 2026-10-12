import type { ContainerPort } from '../../features/containers/types'
import { fullPortLabel, portLabel } from '../../features/containers/ports'
import { Popover, PopoverContent, PopoverTitle, PopoverTrigger } from '../ui/popover'

export function ContainerPorts({ ports, zh }: { ports: ContainerPort[]; zh: boolean }) {
  const values = [...new Map(ports.map(port => [portLabel(port), port])).values()]
  if (!values.length) return <div className="h-10 leading-5 text-muted-foreground">—</div>
  return <Popover>
    <PopoverTrigger className="group grid h-10 w-full min-w-0 grid-rows-2 justify-items-center text-center font-mono leading-5 outline-none focus-visible:ring-2 focus-visible:ring-ring" aria-label={zh ? '查看完整端口映射' : 'View full port mappings'}>
      {values.slice(0, 2).map((port, index) => <span key={portLabel(port)} data-slot="port-summary" className={`inline-flex h-5 items-center gap-1 whitespace-nowrap ${index === 0 ? 'text-sm' : 'text-xs'}`}>
        <span data-slot="port-mapping" className="inline-flex h-[18px] items-center gap-0.75 rounded-sm border border-border bg-muted/40 px-0.75 leading-none group-hover:bg-muted/70">
          {port.public_port ? <span className="font-medium">{port.public_port}</span> : null}
          <span className={port.public_port ? 'border-l border-border pl-0.75' : 'font-medium'}>{port.public_port ? '→ ' : ''}{port.private_port}</span>
          <span className="px-0.5 text-[11px] text-muted-foreground">{port.type}</span>
        </span>
        {index === 1 && values.length > 2 && <span className="text-muted-foreground">+{values.length - 2}</span>}
      </span>)}
    </PopoverTrigger>
    <PopoverContent align="start" className="w-80 max-w-[calc(100vw-2rem)]">
      <PopoverTitle>{zh ? '完整端口映射' : 'Full port mappings'}</PopoverTitle>
      <ul className="max-h-64 space-y-1 overflow-auto font-mono text-xs">
        {ports.map((port, index) => <li key={index} className="break-all">{fullPortLabel(port)}</li>)}
      </ul>
    </PopoverContent>
  </Popover>
}
