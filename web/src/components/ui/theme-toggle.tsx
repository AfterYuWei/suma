import { Moon, Sun } from 'lucide-react'
import type { MouseEvent } from 'react'
import { cn } from '../../lib/cn'
import { useUIStore } from '../../stores/ui'

interface ThemeViewTransition { finished: Promise<void> }
type TransitionDocument = Document & { startViewTransition?: (update: () => void) => ThemeViewTransition }

export function ThemeToggle({ className }: { className?: string }) {
  const { theme, language, setTheme } = useUIStore()
  const dark = theme === 'dark' || (theme === 'system' && matchMedia('(prefers-color-scheme: dark)').matches)
  const label = dark
    ? (language === 'zh-CN' ? '切换到浅色主题' : 'Switch to light theme')
    : (language === 'zh-CN' ? '切换到深色主题' : 'Switch to dark theme')

  const toggleTheme = (event: MouseEvent<HTMLButtonElement>) => {
    const nextTheme = dark ? 'light' : 'dark'
    const reducedMotion = matchMedia('(prefers-reduced-motion: reduce)').matches
    const transitionDocument = document as TransitionDocument
    if (!transitionDocument.startViewTransition || reducedMotion) {
      setTheme(nextTheme)
      return
    }

    const root = document.documentElement
    if (root.hasAttribute('data-theme-transitioning')) return
    const bounds = event.currentTarget.getBoundingClientRect()
    const keyboardActivation = event.clientX === 0 && event.clientY === 0
    const originX = keyboardActivation ? bounds.left + bounds.width / 2 : event.clientX
    const originY = keyboardActivation ? bounds.top + bounds.height / 2 : event.clientY
    const radiusX = Math.max(originX, innerWidth - originX)
    const radiusY = Math.max(originY, innerHeight - originY)
    const radius = Math.hypot(radiusX, radiusY)

    root.style.setProperty('--theme-reveal-x', `${originX}px`)
    root.style.setProperty('--theme-reveal-y', `${originY}px`)
    root.style.setProperty('--theme-reveal-radius', `${radius}px`)
    root.setAttribute('data-theme-transitioning', '')

    const transition = transitionDocument.startViewTransition(() => setTheme(nextTheme))
    void transition.finished.catch(() => undefined).finally(() => {
      root.removeAttribute('data-theme-transitioning')
      root.style.removeProperty('--theme-reveal-x')
      root.style.removeProperty('--theme-reveal-y')
      root.style.removeProperty('--theme-reveal-radius')
    })
  }

  return <button
    type="button"
    role="switch"
    aria-checked={dark}
    aria-label={label}
    title={label}
    onClick={toggleTheme}
    className={cn('theme-switch', dark && 'theme-switch--dark', className)}
  >
    <span className="theme-switch__wash" aria-hidden="true" />
    <span className="theme-switch__thumb" aria-hidden="true" />
    <Sun className="theme-switch__icon theme-switch__sun" strokeWidth={1.8} aria-hidden="true" />
    <Moon className="theme-switch__icon theme-switch__moon" strokeWidth={1.8} aria-hidden="true" />
  </button>
}
