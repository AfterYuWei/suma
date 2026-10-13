import type { ContainerNetwork } from '../../features/containers/types'
import { TooltipHint } from '../ui/tooltip-hint'

export function ContainerNetworks({ networks, zh }: { networks?: ContainerNetwork[] | null; zh: boolean }) {
  const first = networks?.[0]
  const content = networks?.length ? <div className="space-y-2 text-left">
    {networks.map(network => <div key={network.name} className="space-y-0.5">
      <div className="font-medium">{network.name}</div>
      <div className="font-mono text-muted-foreground">{network.ip_address || '—'}</div>
      {network.ipv6_address && <div className="font-mono text-muted-foreground">{network.ipv6_address}</div>}
    </div>)}
  </div> : undefined
  return <TooltipHint content={content} className="w-full">
    <div className="grid h-10 w-full min-w-0 grid-rows-2 justify-items-center leading-5">
      <span className="flex min-w-0 max-w-full items-center gap-1 text-sm"><span className="truncate">{first?.name ?? (networks ? zh ? '无网络' : 'No network' : '—')}</span>{networks && networks.length > 1 && <span className="shrink-0 text-xs text-muted-foreground">+{networks.length - 1}</span>}</span>
      <span className="max-w-full truncate font-mono text-xs text-muted-foreground">{first?.ip_address || first?.ipv6_address || '—'}</span>
    </div>
  </TooltipHint>
}
