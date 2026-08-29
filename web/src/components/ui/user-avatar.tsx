import type { User } from '../../features/auth/types'
import { cn } from '../../lib/utils'

function userInitials(user: Pick<User, 'nickname' | 'username'>) {
  const value = (user.nickname || user.username).trim()
  if (!value) return '?'
  const parts = value.split(/\s+/).filter(Boolean)
  return (parts.length > 1 ? `${parts[0][0]}${parts.at(-1)?.[0] ?? ''}` : [...value].slice(0, 2).join('')).toUpperCase()
}

export function UserAvatar({ user, className }: { user: User; className?: string }) {
  return <span className={cn('relative inline-flex size-8 shrink-0 items-center justify-center overflow-hidden rounded-full bg-muted text-xs font-semibold text-muted-foreground ring-1 ring-border', className)}>
    {user.has_avatar && user.avatar_url ? <img src={user.avatar_url} alt="" className="size-full object-cover" /> : userInitials(user)}
  </span>
}
