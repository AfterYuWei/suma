import type { ButtonHTMLAttributes } from 'react'
import { cn } from '../../lib/cn'

export function Button({ className, ...props }: ButtonHTMLAttributes<HTMLButtonElement>) {
  return <button className={cn('inline-flex h-9 items-center justify-center gap-2 rounded-xl border border-border bg-surface px-3 text-sm font-medium text-text transition-all hover:border-accent/25 hover:bg-surface-hover active:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/30 disabled:pointer-events-none disabled:text-text-disabled disabled:opacity-60', className)} {...props} />
}
