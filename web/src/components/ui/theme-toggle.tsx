import { MonitorIcon, MoonIcon, SunIcon } from 'lucide-react'
import { useUIStore, type Theme } from '../../stores/ui'
import { Button } from './button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from './dropdown-menu'

export function ThemeToggle({ className }: { className?: string }) {
  const { theme, setTheme, language } = useUIStore()
  const zh = language === 'zh-CN'
  const options: { value: Theme; label: string; icon: typeof SunIcon }[] = [
    { value: 'light', label: zh ? '浅色' : 'Light', icon: SunIcon },
    { value: 'dark', label: zh ? '深色' : 'Dark', icon: MoonIcon },
    { value: 'system', label: zh ? '跟随系统' : 'System', icon: MonitorIcon },
  ]

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            variant="ghost"
            size="icon"
            className={`relative ${className ?? ''}`}
            aria-label={zh ? '切换主题' : 'Toggle theme'}
          >
            <SunIcon className="size-4 scale-100 rotate-0 transition-all dark:scale-0 dark:-rotate-90" />
            <MoonIcon className="absolute size-4 scale-0 rotate-90 transition-all dark:scale-100 dark:rotate-0" />
          </Button>
        }
      />
      <DropdownMenuContent align="end">
        {options.map(({ value, label, icon: Icon }) => (
          <DropdownMenuItem key={value} onClick={() => setTheme(value)}>
            <Icon />
            {label}
            {theme === value && <span className="ml-auto text-xs text-muted-foreground">✓</span>}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
