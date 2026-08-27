import type { SVGProps } from 'react'
import { cn } from '@/lib/utils'

/** SUMA radar-berth mark. Brand asset; product actions continue to use Lucide. */
export function LogoMark({ className, ...props }: SVGProps<SVGSVGElement>) {
  return <svg
    viewBox="0 0 32 32"
    fill="none"
    aria-hidden="true"
    className={cn('shrink-0', className)}
    {...props}
  >
    <circle cx="16" cy="16" r="11.5" stroke="currentColor" strokeWidth="4" strokeLinecap="round" strokeDasharray="57 16" transform="rotate(-36 16 16)" />
    <path d="M16 16 23.25 9.25" stroke="currentColor" strokeWidth="3" strokeLinecap="round" />
    <circle cx="16" cy="16" r="3.75" fill="currentColor" />
    <circle cx="25.25" cy="7.25" r="2.75" fill="currentColor" />
  </svg>
}
