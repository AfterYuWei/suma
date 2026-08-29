import { Skeleton } from './skeleton'
import { Spinner } from './spinner'

interface LoadingStateProps {
  label: string
  rows?: number
  compact?: boolean
  embedded?: boolean
}

export function LoadingState({ label, rows = 5, compact = false, embedded = false }: LoadingStateProps) {
  const content = (
    <div className="flex w-full flex-col items-start gap-4">
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <Spinner className="size-4" />
        <span>{label}</span>
      </div>
      <div aria-hidden="true" className="flex w-full flex-col items-start gap-3">
        {Array.from({ length: rows }, (_, index) => (
          <div key={index} className="flex w-full items-center gap-3">
            <Skeleton className="size-6 rounded-full" />
            <div className="flex flex-1 flex-col gap-1.5">
              <Skeleton className="h-3.5 w-full max-w-sm" />
              {!compact && <Skeleton className="h-3 w-full max-w-xl" />}
            </div>
            <Skeleton className="h-7 w-16 rounded-lg" />
          </div>
        ))}
      </div>
    </div>
  )

  return (
    <div className="w-full" role="status" aria-live="polite" aria-label={label}>
      {embedded ? content : (
        <div className="flex flex-col gap-4 rounded-xl bg-card p-4 ring-1 ring-foreground/10">
          {content}
        </div>
      )}
    </div>
  )
}
