import type { ContainerPort } from '../../features/containers/types'
import { fullPortLabel, portLabel } from '../../features/containers/ports'
import { Popover, PopoverContent, PopoverTitle, PopoverTrigger } from '../ui/popover'
import { StatusBadge } from '../ui/status-badge'
import { TooltipHint } from '../ui/tooltip-hint'

const protocolBorder: Record<string, string> = {
  tcp: 'border-emerald-600/55 dark:border-emerald-400/55',
  udp: 'border-violet-600/55 dark:border-violet-400/55',
}

export function ContainerPortTag({ port, className = '' }: { port: ContainerPort; className?: string }) {
  return <TooltipHint content={port.type.toUpperCase()}><span data-slot="port-mapping" className="min-w-0 max-w-full">
    <StatusBadge tone="outline" className={`h-[18px] min-w-0 max-w-full self-center justify-start rounded-sm bg-transparent px-1.5 py-0 leading-4 text-foreground group-hover:bg-muted/40 ${protocolBorder[port.type] ?? 'border-border/60'} ${className}`}><span className="truncate">{portLabel(port)}</span></StatusBadge>
  </span></TooltipHint>
}

export function ContainerPorts({ ports, zh }: { ports: ContainerPort[]; zh: boolean }) {
  const values = [...new Map(ports.map(port => [portLabel(port), port])).values()]
  if (!values.length) return <div className="h-10 leading-5 text-muted-foreground">—</div>
  return <Popover>
    <PopoverTrigger className="group grid h-10 w-full min-w-0 grid-rows-2 justify-items-center text-center font-mono leading-5 outline-none focus-visible:ring-2 focus-visible:ring-ring" aria-label={zh ? '查看完整端口映射' : 'View full port mappings'}>
      {values.slice(0, 2).map((port, index) => <span key={portLabel(port)} data-slot="port-summary" className={`inline-flex h-5 items-center gap-1 whitespace-nowrap ${index === 0 ? 'text-sm' : 'text-xs'}`}>
        <ContainerPortTag port={port} className={index === 0 ? 'text-sm' : 'text-xs font-normal'} />
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
