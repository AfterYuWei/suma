import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Anchor, ArrowRight, CircleCheck, LoaderCircle, LockKeyhole, Terminal } from 'lucide-react'
import { type FormEvent, type ReactNode, useState } from 'react'
import { api, ApiError } from '../../lib/api'
import { useI18n } from '../../lib/i18n'
import { useUIStore } from '../../stores/ui'
import { ThemeToggle } from '../../components/ui/theme-toggle'

interface Status { needs_setup: boolean }
interface User { id: number; username: string }

function Field({ label, name, type = 'text', autoComplete }: { label: string; name: string; type?: string; autoComplete?: string }) {
  return <label className="block"><span className="mb-2 block font-mono text-[9px] font-medium uppercase tracking-[.18em] text-text-subtle">{label}</span><input name={name} type={type} autoComplete={autoComplete} required className="h-11 w-full rounded-xl border border-border bg-background/65 px-3.5 text-sm outline-none transition-all placeholder:text-text-subtle focus:border-accent/60 focus:bg-background" /></label>
}

function AuthFrame({ eyebrow, title, description, children }: { eyebrow: string; title: string; description: string; children: ReactNode }) {
  const { language, setLanguage } = useUIStore()
  const zh = language === 'zh-CN'
  return <main className="data-grid min-h-screen bg-background p-3 text-text sm:p-5"><div className="glass-panel mx-auto grid min-h-[calc(100vh-2.5rem)] max-w-[1440px] overflow-hidden rounded-[1.75rem] lg:grid-cols-[minmax(0,1.2fr)_minmax(400px,.8fr)]"><section className="scanline relative hidden flex-col justify-between overflow-hidden border-r border-border p-12 lg:flex xl:p-16"><div className="absolute -left-32 top-1/3 size-[30rem] rounded-full bg-accent/[.08] blur-3xl" /><div className="relative flex items-center gap-3"><span className="grid size-10 place-items-center rounded-xl border border-accent/30 bg-accent/10 text-accent"><Anchor className="size-5" strokeWidth={1.6} /></span><div><p className="text-sm font-semibold tracking-[-.03em]">DockPort</p><p className="font-mono text-[8px] uppercase tracking-[.22em] text-text-subtle">Local operations cockpit</p></div></div><div className="relative max-w-2xl"><p className="font-mono text-[9px] uppercase tracking-[.22em] text-accent">Infrastructure, in focus</p><h2 className="mt-5 text-[clamp(3.4rem,6vw,6.5rem)] font-medium leading-[.88] tracking-[-.075em]">Your engine.<br /><span className="text-text-subtle">One clear view.</span></h2><p className="mt-7 max-w-lg text-sm leading-6 text-text-muted">{zh ? '直接连接本地 Docker 引擎，在一个安全、专注的控制平面中完成日常运维。' : 'Connect directly to your local Docker engine and operate it from one secure, focused control plane.'}</p></div><div className="relative grid max-w-2xl grid-cols-3 gap-px overflow-hidden rounded-xl border border-border bg-border">{[{ icon: Terminal, label: zh ? '本地引擎' : 'Local engine' }, { icon: LockKeyhole, label: zh ? '会话保护' : 'Session secured' }, { icon: CircleCheck, label: zh ? '实时状态' : 'Live status' }].map(({ icon: Icon, label }) => <div key={label} className="bg-background/75 p-4"><Icon className="mb-3 size-4 text-accent" strokeWidth={1.5} /><p className="font-mono text-[9px] uppercase tracking-wider text-text-muted">{label}</p></div>)}</div></section><section className="flex items-center justify-center p-6 sm:p-10 lg:p-12"><div className="w-full max-w-sm"><div className="mb-12 flex items-center lg:hidden"><span className="grid size-9 place-items-center rounded-xl border border-accent/30 bg-accent/10 text-accent"><Anchor className="size-4" /></span><span className="ml-3 text-sm font-semibold">DockPort</span><button onClick={() => setLanguage(language === 'zh-CN' ? 'en-US' : 'zh-CN')} className="ml-auto h-9 rounded-xl border border-border bg-surface px-3 text-[10px] font-semibold">{language === 'zh-CN' ? 'EN' : '中文'}</button></div><div className="hidden justify-end lg:flex"><button onClick={() => setLanguage(language === 'zh-CN' ? 'en-US' : 'zh-CN')} className="h-9 rounded-xl border border-border bg-surface/60 px-3 text-[10px] font-semibold transition-colors hover:bg-surface-hover">{language === 'zh-CN' ? 'EN' : '中文'}</button></div><p className="mt-8 font-mono text-[9px] font-medium uppercase tracking-[.22em] text-accent">{eyebrow}</p><h1 className="mt-3 text-3xl font-medium tracking-[-.055em]">{title}</h1><p className="mt-3 text-sm leading-6 text-text-muted">{description}</p><div className="mt-9">{children}</div><p className="mt-10 border-t border-border pt-5 font-mono text-[8px] uppercase tracking-[.13em] text-text-subtle">{zh ? '本地访问 / HttpOnly 会话 / 无外部控制平面' : 'Local access / HttpOnly session / No external control plane'}</p></div></section></div></main>
}

function SubmitButton({ pending, children }: { pending: boolean; children: ReactNode }) {
  return <><button disabled={pending} className="group mt-3 flex h-11 w-full items-center justify-center gap-2 rounded-xl bg-accent text-sm font-semibold text-accent-foreground transition-all hover:brightness-110 disabled:opacity-50">{pending ? <LoaderCircle className="size-4 animate-spin" /> : <>{children}<ArrowRight className="size-4 transition-transform group-hover:translate-x-1" /></>}</button><div className="mt-6 flex justify-center"><ThemeToggle /></div></>
}

export function AuthGate({ children }: { children: ReactNode }) {
  const client = useQueryClient()
  const [error, setError] = useState('')
  const { language, t } = useI18n(); const zh = language === 'zh-CN'
  const status = useQuery({ queryKey: ['auth-status'], queryFn: () => api<Status>('/auth/status') })
  const session = useQuery({ queryKey: ['session'], queryFn: () => api<User>('/auth/session'), enabled: status.isSuccess && !status.data.needs_setup, retry: false })
  const initialize = useMutation({ mutationFn: (body: Record<string, string>) => api<User>('/auth/initialize', { method: 'POST', body: JSON.stringify(body) }), onSuccess: async () => { setError(''); await client.invalidateQueries({ queryKey: ['auth-status'] }) }, onError: (value) => setError(value instanceof ApiError ? value.message : 'Unable to create administrator') })
  const login = useMutation({ mutationFn: (body: Record<string, string>) => api<User>('/auth/login', { method: 'POST', body: JSON.stringify(body) }), onSuccess: (user) => { setError(''); client.setQueryData(['session'], user) }, onError: (value) => setError(value instanceof ApiError ? value.message : 'Unable to sign in') })

  if (status.isPending || (!status.data?.needs_setup && session.isPending)) return <div className="grid min-h-screen place-items-center bg-background text-text-subtle"><LoaderCircle className="size-5 animate-spin" /></div>
  if (status.isError) return <AuthFrame eyebrow={zh ? '连接错误' : 'Connection error'} title={zh ? 'DockPort 暂不可用' : 'DockPort is unavailable'} description={zh ? '服务器没有响应，请检查 DockPort 服务后重试。' : 'The server did not respond. Check the DockPort service and try again.'}><button onClick={() => status.refetch()} className="h-10 w-full rounded-md border border-border bg-surface text-sm">{t('retry')}</button></AuthFrame>

  if (status.data?.needs_setup) {
    const submit = (event: FormEvent<HTMLFormElement>) => { event.preventDefault(); const data = new FormData(event.currentTarget); initialize.mutate({ username: String(data.get('username')), password: String(data.get('password')), confirm_password: String(data.get('confirm_password')) }) }
    return <AuthFrame eyebrow={zh ? '首次运行' : 'First run'} title={zh ? '创建管理员' : 'Create administrator'} description={zh ? '为此 DockPort 实例设置本地管理员账户。' : 'Set up the local administrator account for this DockPort instance.'}><form className="space-y-4" onSubmit={submit}><Field label={zh ? '用户名' : 'Username'} name="username" autoComplete="username" /><Field label={zh ? '密码' : 'Password'} name="password" type="password" autoComplete="new-password" /><Field label={zh ? '确认密码' : 'Confirm password'} name="confirm_password" type="password" autoComplete="new-password" />{error && <p role="alert" className="text-xs text-red-400">{error}</p>}<SubmitButton pending={initialize.isPending}>{zh ? '创建管理员' : 'Create administrator'}</SubmitButton></form></AuthFrame>
  }

  if (!session.data) {
    const submit = (event: FormEvent<HTMLFormElement>) => { event.preventDefault(); const data = new FormData(event.currentTarget); login.mutate({ username: String(data.get('username')), password: String(data.get('password')) }) }
    return <AuthFrame eyebrow={zh ? '欢迎回来' : 'Welcome back'} title={zh ? '登录 DockPort' : 'Sign in to DockPort'} description={zh ? '使用本地管理员账户管理此 Docker 主机。' : 'Manage this Docker host with your local administrator account.'}><form className="space-y-4" onSubmit={submit}><Field label={zh ? '用户名' : 'Username'} name="username" autoComplete="username" /><Field label={zh ? '密码' : 'Password'} name="password" type="password" autoComplete="current-password" />{error && <p role="alert" className="flex items-center gap-2 text-xs text-red-400"><LockKeyhole className="size-3.5" />{error}</p>}<SubmitButton pending={login.isPending}>{zh ? '登录' : 'Sign in'}</SubmitButton></form></AuthFrame>
  }
  return children
}
