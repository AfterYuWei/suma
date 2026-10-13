import type { ReactNode } from "react"

import { cn } from "@/lib/utils"

/** Shared resource-list boundary; functional headers supply their own surface. */
export function ListShell({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <div
      data-slot="list-shell"
      className={cn("w-full overflow-hidden rounded-lg", className)}
    >
      {children}
    </div>
  )
}
