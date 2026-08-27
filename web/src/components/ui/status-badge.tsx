import type { ReactNode } from "react"

import { cn } from "@/lib/utils"
import { Badge } from "@/components/ui/badge"

export type StatusTone = "success" | "warning" | "critical" | "neutral" | "outline"

const toneClass: Record<Exclude<StatusTone, "outline">, string> = {
  success: "border-transparent bg-emerald-500/15 text-emerald-600 dark:text-emerald-400",
  warning: "border-transparent bg-amber-500/15 text-amber-600 dark:text-amber-400",
  critical: "border-transparent bg-red-500/15 text-red-600 dark:text-red-400",
  neutral: "border-transparent bg-muted/60 text-muted-foreground",
}

/** Shared status pill so every list renders states with identical semantics/colors. */
export function StatusBadge({ tone = "neutral", children, className }: { tone?: StatusTone; children: ReactNode; className?: string }) {
  if (tone === "outline") {
    return <Badge variant="outline" className={cn("text-muted-foreground", className)}>{children}</Badge>
  }
  return <Badge variant="secondary" className={cn(toneClass[tone], className)}>{children}</Badge>
}
