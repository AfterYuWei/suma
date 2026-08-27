import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowRight } from 'lucide-react'
import { type FormEvent, type ReactNode, useState } from 'react'
import { Alert, AlertDescription } from '../../components/ui/alert'
import { Button } from '../../components/ui/button'
import { Card, CardContent } from '../../components/ui/card'
import { Input } from '../../components/ui/input'
import { Label } from '../../components/ui/label'
import { LogoMark } from '../../components/ui/logo-mark'
import { Spinner } from '../../components/ui/spinner'
import { ThemeToggle } from '../../components/ui/theme-toggle'
import { api, ApiError } from '../../lib/api'
import { useI18n } from '../../lib/i18n'

interface Status { needs_setup: boolean }
interface User { id: number; username: string }
interface AuthValues { username: string; password: string; confirm_password?: string }

function AuthFrame({ title, description, children }: { title: string; description: string; children: ReactNode }) {
  return <main className="grid min-h-screen place-items-center p-6">
    <div className="fixed top-6 right-6"><ThemeToggle /></div>
    <Card className="w-full max-w-[420px]">
      <CardContent className="flex flex-col items-start gap-6">
        <div className="flex items-center gap-2.5">
          <LogoMark width={32} height={32} />
          <span className="text-lg font-semibold tracking-tight">SUMA</span>
        </div>
        <div className="flex flex-col gap-1.5">
          <h1 className="text-xl font-semibold tracking-tight">{title}</h1>
          <p className="text-sm leading-relaxed text-muted-foreground">{description}</p>
        </div>
        {children}
      </CardContent>
    </Card>
  </main>
}

function AuthForm({ setup, pending, error, onSubmit }: { setup: boolean; pending: boolean; error: string; onSubmit: (values: AuthValues) => void }) {
  const { language } = useI18n()
  const zh = language === 'zh-CN'
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (pending) return
    onSubmit({ username, password, ...(setup ? { confirm_password: confirmPassword } : {}) })
  }
  return <form onSubmit={submit} autoComplete="on" className="flex w-full flex-col gap-4">
    <div className="flex flex-col gap-1.5">
      <Label htmlFor="auth-username">{zh ? '用户名' : 'Username'}</Label>
      <Input id="auth-username" name="username" required autoComplete="username" value={username} onChange={(event) => setUsername(event.target.value)} />
    </div>
    <div className="flex flex-col gap-1.5">
      <Label htmlFor="auth-password">{zh ? '密码' : 'Password'}</Label>
      <Input id="auth-password" name="password" type="password" required autoComplete={setup ? 'new-password' : 'current-password'} value={password} onChange={(event) => setPassword(event.target.value)} />
    </div>
    {setup && <div className="flex flex-col gap-1.5">
      <Label htmlFor="auth-confirm-password">{zh ? '确认密码' : 'Confirm password'}</Label>
      <Input id="auth-confirm-password" name="confirm_password" type="password" required autoComplete="new-password" value={confirmPassword} onChange={(event) => setConfirmPassword(event.target.value)} />
    </div>}
    {error && <Alert variant="destructive"><AlertDescription>{error}</AlertDescription></Alert>}
    <Button type="submit" size="lg" disabled={pending}>{pending && <Spinner />}<span>{setup ? (zh ? '创建管理员' : 'Create administrator') : (zh ? '登录' : 'Sign in')}</span><ArrowRight /></Button>
  </form>
}

export function AuthGate({ children }: { children: ReactNode }) {
  const client = useQueryClient()
  const [error, setError] = useState('')
  const { language, t } = useI18n()
  const zh = language === 'zh-CN'
  const status = useQuery({ queryKey: ['auth-status'], queryFn: () => api<Status>('/auth/status') })
  const session = useQuery({ queryKey: ['session'], queryFn: () => api<User>('/auth/session'), enabled: status.isSuccess && !status.data.needs_setup, retry: false })
  const initialize = useMutation({ mutationFn: (body: AuthValues) => api<User>('/auth/initialize', { method: 'POST', body: JSON.stringify(body) }), onSuccess: async () => { setError(''); await client.invalidateQueries({ queryKey: ['auth-status'] }) }, onError: (value) => setError(value instanceof ApiError ? value.message : 'Unable to create administrator') })
  const login = useMutation({ mutationFn: (body: AuthValues) => api<User>('/auth/login', { method: 'POST', body: JSON.stringify(body) }), onSuccess: (user) => { setError(''); client.setQueryData(['session'], user) }, onError: (value) => setError(value instanceof ApiError ? value.message : 'Unable to sign in') })

  if (status.isPending || (!status.data?.needs_setup && session.isPending)) {
    return <main className="grid min-h-screen place-items-center"><div className="flex flex-col items-center gap-2"><Spinner className="size-6 text-muted-foreground" /><p className="text-sm text-muted-foreground">{t('loading')}</p></div></main>
  }
  if (status.isError) {
    return <AuthFrame title={zh ? 'SUMA 暂不可用' : 'SUMA is unavailable'} description={zh ? '服务器没有响应，请检查 SUMA 服务后重试。' : 'The server did not respond. Check the SUMA service and try again.'}>
      <Button variant="outline" size="lg" className="w-full" onClick={() => status.refetch()}>{t('retry')}</Button>
    </AuthFrame>
  }
  if (status.data?.needs_setup) {
    return <AuthFrame title={zh ? '创建管理员' : 'Create administrator'} description={zh ? '为此 SUMA 实例设置本地管理员账户。' : 'Set up the local administrator account for this SUMA instance.'}>
      <AuthForm setup pending={initialize.isPending} error={error} onSubmit={(values) => initialize.mutate(values)} />
    </AuthFrame>
  }
  if (!session.data) {
    return <AuthFrame title={zh ? '登录 SUMA' : 'Sign in to SUMA'} description={zh ? '使用本地管理员账户管理此 Docker 主机。' : 'Manage this Docker host with your local administrator account.'}>
      <AuthForm setup={false} pending={login.isPending} error={error} onSubmit={(values) => login.mutate(values)} />
    </AuthFrame>
  }
  return children
}
