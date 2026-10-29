import type { ComponentProps } from 'react'
import { CheckCircle2, CircleAlert, Loader2 } from 'lucide-react'
import { cn } from '../../lib/utils'

export function InlineStatus({ tone, className, children, ...props }: ComponentProps<'span'> & { tone: 'success' | 'error' | 'pending' }) {
  const Icon = tone === 'success' ? CheckCircle2 : tone === 'error' ? CircleAlert : Loader2
  return <span data-slot="inline-status" role={tone === 'error' ? 'alert' : 'status'} className={cn(
    'inline-flex min-w-0 max-w-full items-start gap-1.5 text-xs leading-5',
    tone === 'success' ? 'text-emerald-700 dark:text-emerald-400' : tone === 'error' ? 'text-destructive' : 'text-muted-foreground',
    className,
  )} {...props}>
    <Icon aria-hidden="true" className={cn('mt-0.5 size-3.5 shrink-0', tone === 'pending' && 'animate-spin motion-reduce:animate-none')} />
    <span className="min-w-0 whitespace-pre-wrap [overflow-wrap:anywhere]">{children}</span>
  </span>
}
