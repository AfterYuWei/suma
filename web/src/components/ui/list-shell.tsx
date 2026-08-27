import type { ReactNode } from "react"

import { cn } from "@/lib/utils"

/** Card shell shared by every resource list (tables and stacked lists alike). */
export function ListShell({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <div
      data-slot="list-shell"
      className={cn("w-full overflow-hidden rounded-xl border border-border bg-card", className)}
    >
      {children}
    </div>
  )
}
