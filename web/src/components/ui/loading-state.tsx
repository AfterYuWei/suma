import { LoaderCircle } from 'lucide-react'

interface LoadingStateProps {
  label: string
  rows?: number
  compact?: boolean
  embedded?: boolean
}

export function LoadingState({ label, rows = 5, compact = false, embedded = false }: LoadingStateProps) {
  return <div role="status" aria-live="polite" aria-label={label} className={`overflow-hidden bg-background/40 ${embedded ? '' : 'rounded-2xl border border-border'}`}>
    <div className="flex items-center gap-2.5 border-b border-border px-4 py-3 font-mono text-[10px] uppercase tracking-[.16em] text-text-subtle">
      <LoaderCircle className="size-3.5 animate-spin text-accent" />
      <span>{label}</span>
      <span className="ml-auto flex gap-1" aria-hidden="true">
        {[0, 1, 2].map((item) => <span key={item} className="size-1 rounded-full bg-accent animate-pulse" style={{ animationDelay: `${item * 180}ms` }} />)}
      </span>
    </div>
    <div className="divide-y divide-border/70" aria-hidden="true">
      {Array.from({ length: rows }, (_, index) => <div key={index} className={`flex items-center gap-3 px-4 ${compact ? 'h-14' : 'h-16'}`}>
        <span className="size-2 shrink-0 animate-pulse rounded-full bg-muted" style={{ animationDelay: `${index * 90}ms` }} />
        <div className="min-w-0 flex-1 space-y-2">
          <div className="h-2.5 animate-pulse rounded-full bg-muted" style={{ width: `${42 + index % 3 * 12}%`, animationDelay: `${index * 90}ms` }} />
          <div className="h-1.5 w-1/3 animate-pulse rounded-full bg-muted/70" style={{ animationDelay: `${index * 90 + 100}ms` }} />
        </div>
        <div className="hidden h-2 w-20 animate-pulse rounded-full bg-muted/70 sm:block" style={{ animationDelay: `${index * 90 + 50}ms` }} />
      </div>)}
    </div>
  </div>
}
