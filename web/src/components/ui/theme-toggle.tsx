import { Moon, Sun } from 'lucide-react'
import { cn } from '../../lib/cn'
import { useUIStore } from '../../stores/ui'

export function ThemeToggle({ className }: { className?: string }) {
  const { theme, language, setTheme } = useUIStore()
  const dark = theme === 'dark' || (theme === 'system' && matchMedia('(prefers-color-scheme: dark)').matches)
  const label = dark
    ? (language === 'zh-CN' ? '切换到浅色主题' : 'Switch to light theme')
    : (language === 'zh-CN' ? '切换到深色主题' : 'Switch to dark theme')

  return <button
    type="button"
    role="switch"
    aria-checked={dark}
    aria-label={label}
    title={label}
    onClick={() => setTheme(dark ? 'light' : 'dark')}
    className={cn('theme-switch', dark && 'theme-switch--dark', className)}
  >
    <span className="theme-switch__wash" aria-hidden="true" />
    <span className="theme-switch__thumb" aria-hidden="true" />
    <Sun className="theme-switch__icon theme-switch__sun" strokeWidth={1.8} aria-hidden="true" />
    <Moon className="theme-switch__icon theme-switch__moon" strokeWidth={1.8} aria-hidden="true" />
  </button>
}
