import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowLeft, ArrowRight, Copy, Fingerprint, ShieldCheck } from 'lucide-react'
import { type FormEvent, type ReactNode, useEffect, useState } from 'react'
import { Alert, AlertDescription } from '../../components/ui/alert'
import { Button } from '../../components/ui/button'
import { Card, CardContent } from '../../components/ui/card'
import { Input } from '../../components/ui/input'
import { Label } from '../../components/ui/label'
import { LogoMark } from '../../components/ui/logo-mark'
import { Spinner } from '../../components/ui/spinner'
import { ThemeToggle } from '../../components/ui/theme-toggle'
import { api, ApiError, demoMode, getDemoCredentials, type DemoCredentials } from '../../lib/api'
import { useI18n } from '../../lib/i18n'
import { getPasskey, passkeysAvailable } from '../../lib/passkeys'
import type { User } from './types'

interface Status { needs_setup: boolean }
interface AuthValues { username: string; password: string; email?: string; nickname?: string; confirm_password?: string; setup_token?: string }
interface LoginResponse { requires_two_factor: boolean; challenge_token?: string; user?: User }
interface PasskeyOptions { ceremony_token: string; options: unknown }

const setupKeyCommand = String.raw`docker logs suma 2>&1 | sed -n 's/.*"msg":"SUMA initialization key".*"setup_token":"\([^"]*\)".*/\1/p' | tail -n 1`

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

function AuthForm({ setup, pending, error, initialValues, onSubmit }: { setup: boolean; pending: boolean; error: string; initialValues?: DemoCredentials | null; onSubmit: (values: AuthValues) => void }) {
  const { language } = useI18n()
  const zh = language === 'zh-CN'
  const [username, setUsername] = useState('')
  const [nickname, setNickname] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [setupToken, setSetupToken] = useState('')
  const [commandCopied, setCommandCopied] = useState(false)
  useEffect(() => {
    if (!setup && initialValues) {
      setUsername(initialValues.username)
      setPassword(initialValues.password)
    }
  }, [initialValues, setup])
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (pending) return
    onSubmit({ username, password, ...(setup ? { nickname, email, confirm_password: confirmPassword, setup_token: setupToken.trim() } : {}) })
  }
  return <form onSubmit={submit} autoComplete="on" className="flex w-full flex-col gap-4">
    {setup && <div className="flex flex-col gap-2 rounded-lg border bg-muted/30 p-3 text-xs">
      <p className="text-muted-foreground">{zh ? '在部署主机的终端运行下面的指令，直接取得最新初始化密钥。自定义容器名时请替换 suma。密钥 30 分钟后过期；过期后重启尚未初始化的容器，再运行指令。' : 'Run this command on the deployment host to print the latest initialization key. Replace suma if you use a different container name. The key expires after 30 minutes; restart the uninitialized container and run the command again.'}</p>
      <code className="select-all whitespace-pre-wrap break-all rounded bg-background p-2 font-mono text-[11px]">{setupKeyCommand}</code>
      <Button type="button" variant="outline" size="sm" className="self-start" onClick={async () => { try { await navigator.clipboard.writeText(setupKeyCommand); setCommandCopied(true) } catch { setCommandCopied(false) } }}><Copy />{commandCopied ? (zh ? '已复制指令' : 'Command copied') : (zh ? '复制指令' : 'Copy command')}</Button>
    </div>}
    {!setup && initialValues && <div className="flex items-center justify-between gap-4 rounded-lg border bg-muted/40 px-3 py-2 text-xs">
      <span className="text-muted-foreground">{zh ? '演示账号已填入' : 'Demo credentials filled'}</span>
      <code className="font-mono">{initialValues.username} / {initialValues.password}</code>
    </div>}
    <div className="flex flex-col gap-1.5">
      <Label htmlFor="auth-username">{setup ? (zh ? '用户名' : 'Username') : (zh ? '用户名或邮箱' : 'Username or email')}</Label>
      <Input id="auth-username" name="username" required autoComplete="username" value={username} onChange={(event) => setUsername(event.target.value)} />
    </div>
    {setup && <>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="auth-setup-token">{zh ? '初始化密钥' : 'Initialization key'}</Label>
        <Input id="auth-setup-token" name="setup_token" type="password" required autoComplete="off" spellCheck={false} autoCapitalize="off" value={setupToken} onChange={(event) => setSetupToken(event.target.value)} />
      </div>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="auth-nickname">{zh ? '昵称（可选）' : 'Nickname (optional)'}</Label>
        <Input id="auth-nickname" name="nickname" maxLength={64} autoComplete="name" value={nickname} onChange={(event) => setNickname(event.target.value)} />
      </div>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="auth-email">{zh ? '邮箱' : 'Email'}</Label>
        <Input id="auth-email" name="email" type="email" required maxLength={254} autoComplete="email" value={email} onChange={(event) => setEmail(event.target.value)} />
      </div>
    </>}
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

function TwoFactorForm({ pending, error, onBack, onSubmit }: { pending: boolean; error: string; onBack: () => void; onSubmit: (code: string) => void }) {
  const { language } = useI18n()
  const zh = language === 'zh-CN'
  const [code, setCode] = useState('')
  return <form className="flex w-full flex-col gap-4" onSubmit={(event) => { event.preventDefault(); if (!pending) onSubmit(code) }}>
    <div className="flex size-10 items-center justify-center rounded-lg bg-muted text-muted-foreground"><ShieldCheck className="size-5" /></div>
    <div className="flex flex-col gap-1.5">
      <Label htmlFor="auth-two-factor">{zh ? '验证码或恢复码' : 'Verification or recovery code'}</Label>
      <Input id="auth-two-factor" name="one-time-code" autoFocus required autoComplete="one-time-code" value={code} onChange={(event) => setCode(event.target.value)} placeholder={zh ? '输入 6 位验证码' : 'Enter the 6-digit code'} />
      <p className="text-xs leading-relaxed text-muted-foreground">{zh ? '打开认证器 App 获取验证码；无法访问认证器时可使用一枚恢复码。' : 'Use the code from your authenticator app, or enter one recovery code if the app is unavailable.'}</p>
    </div>
    {error && <Alert variant="destructive"><AlertDescription>{error}</AlertDescription></Alert>}
    <Button type="submit" size="lg" disabled={pending}>{pending && <Spinner />}<span>{zh ? '验证并登录' : 'Verify and sign in'}</span><ArrowRight /></Button>
    <Button type="button" variant="ghost" onClick={onBack}><ArrowLeft />{zh ? '返回密码登录' : 'Back to password sign-in'}</Button>
  </form>
}

export function AuthGate({ children }: { children: ReactNode }) {
  const client = useQueryClient()
  const [error, setError] = useState('')
  const [challengeToken, setChallengeToken] = useState('')
  const { language, t } = useI18n()
  const zh = language === 'zh-CN'
  const credentials = useQuery({ queryKey: ['demo-credentials'], queryFn: getDemoCredentials, enabled: demoMode, staleTime: Infinity })
  const status = useQuery({ queryKey: ['auth-status'], queryFn: () => api<Status>('/auth/status') })
  const session = useQuery({ queryKey: ['session'], queryFn: () => api<User>('/auth/session'), enabled: status.isSuccess && !status.data.needs_setup, retry: false })
  const initialize = useMutation({
    mutationFn: (body: AuthValues) => api<User>('/auth/initialize', { method: 'POST', body: JSON.stringify(body) }),
    onSuccess: async () => { setError(''); await client.invalidateQueries({ queryKey: ['auth-status'] }) },
    onError: (value) => {
      if (value instanceof ApiError) {
        if (value.status === 409) void client.invalidateQueries({ queryKey: ['auth-status'] })
        const messages: Record<number, [string, string]> = {
          403: ['初始化密钥无效，请重新核对。', 'Invalid initialization key. Check it and try again.'],
          410: ['初始化密钥已过期。请重启尚未初始化的容器，再运行上方指令。', 'The initialization key has expired. Restart the uninitialized container, then run the command again.'],
          429: ['尝试次数过多，请稍后重试。', 'Too many attempts. Try again later.'],
          409: ['管理员已创建，正在切换到登录页。', 'Administrator already exists. Switching to sign in.'],
        }
        setError(messages[value.status]?.[zh ? 0 : 1] ?? value.message)
      } else setError(zh ? '无法创建管理员，请重试。' : 'Unable to create administrator. Try again.')
    },
  })
  const login = useMutation({
    mutationFn: (body: AuthValues) => api<LoginResponse>('/auth/login', { method: 'POST', body: JSON.stringify(body) }),
    onSuccess: (result) => {
      setError('')
      if (result.requires_two_factor && result.challenge_token) setChallengeToken(result.challenge_token)
      else if (result.user) client.setQueryData(['session'], result.user)
    },
    onError: (value) => setError(value instanceof ApiError ? value.message : 'Unable to sign in'),
  })
  const verifyTwoFactor = useMutation({
    mutationFn: (code: string) => api<User>('/auth/two-factor', { method: 'POST', body: JSON.stringify({ challenge_token: challengeToken, code }) }),
    onSuccess: (user) => { setError(''); setChallengeToken(''); client.setQueryData(['session'], user) },
    onError: (value) => setError(value instanceof ApiError ? value.message : 'Unable to verify the code'),
  })
  const passkeyLogin = useMutation({
    mutationFn: async () => {
      const begin = await api<PasskeyOptions>('/auth/passkey/options', { method: 'POST' })
      const credential = await getPasskey(begin.options)
      return api<User>('/auth/passkey', { method: 'POST', headers: { 'X-WebAuthn-Ceremony': begin.ceremony_token }, body: JSON.stringify(credential) })
    },
    onSuccess: (user) => { setError(''); client.setQueryData(['session'], user) },
    onError: (value) => setError(value instanceof ApiError ? value.message : (zh ? '无法使用 Passkey 登录，请重试。' : 'Unable to sign in with a passkey. Try again.')),
  })

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
    if (challengeToken) {
      return <AuthFrame title={zh ? '两步验证' : 'Two-factor authentication'} description={zh ? '密码验证成功。请完成第二步身份验证。' : 'Your password was accepted. Complete the second verification step.'}>
        <TwoFactorForm pending={verifyTwoFactor.isPending} error={error} onBack={() => { setChallengeToken(''); setError(''); verifyTwoFactor.reset() }} onSubmit={(code) => verifyTwoFactor.mutate(code)} />
      </AuthFrame>
    }
    return <AuthFrame title={zh ? '登录 SUMA' : 'Sign in to SUMA'} description={zh ? '使用本地管理员账户管理此 Docker 主机。' : 'Manage this Docker host with your local administrator account.'}>
      <AuthForm setup={false} pending={login.isPending} error={error} initialValues={credentials.data} onSubmit={(values) => login.mutate(values)} />
      {!demoMode && passkeysAvailable() && <div className="flex w-full flex-col gap-4">
        <div className="flex items-center gap-3 text-xs text-muted-foreground"><span className="h-px flex-1 bg-border" /><span>{zh ? '或' : 'or'}</span><span className="h-px flex-1 bg-border" /></div>
        <Button type="button" variant="outline" size="lg" className="w-full" disabled={passkeyLogin.isPending || login.isPending} onClick={() => passkeyLogin.mutate()}>{passkeyLogin.isPending ? <Spinner /> : <Fingerprint />}{zh ? '使用 Passkey 登录' : 'Sign in with a passkey'}</Button>
      </div>}
    </AuthFrame>
  }
  return children
}
