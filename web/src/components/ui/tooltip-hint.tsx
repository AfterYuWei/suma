import type { ReactNode } from 'react'

import { cn } from '@/lib/utils'
import { Tooltip, TooltipContent, TooltipTrigger } from './tooltip'

/** Global shadcn tooltip wrapper that also works around disabled controls. */
export function TooltipHint({ content, children, className }: { content?: ReactNode; children: ReactNode; className?: string }) {
  if (!content) return children
  return (
    <Tooltip>
      <TooltipTrigger render={<span className={cn('inline-flex min-w-0 max-w-full', className)} />}>
        {children}
      </TooltipTrigger>
      <TooltipContent>{content}</TooltipContent>
    </Tooltip>
  )
}
