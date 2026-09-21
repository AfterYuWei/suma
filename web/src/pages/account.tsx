import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Camera, Copy, Fingerprint, KeyRound, Pencil, Plus, RefreshCw, Save, ShieldCheck, Trash2, UserRound } from 'lucide-react'
import { type FormEvent, type PointerEvent as ReactPointerEvent, useEffect, useRef, useState } from 'react'
import { Alert, AlertDescription } from '../components/ui/alert'
import { Button } from '../components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '../components/ui/dialog'
import { Input } from '../components/ui/input'
import { Label } from '../components/ui/label'
import { Spinner } from '../components/ui/spinner'
import { UserAvatar } from '../components/ui/user-avatar'
import type { User } from '../features/auth/types'
import { api, demoMode } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { createPasskey, passkeysAvailable } from '../lib/passkeys'
import { confirmDialog } from '../stores/dialog'
import { ResourceFrame } from './images'

const maxAvatarBytes = 2 * 1024 * 1024
const avatarTypes = new Set(['image/jpeg', 'image/png', 'image/webp'])

interface ProfileValues { username: string; nickname: string; email: string; current_password: string }
interface CropSource { url: string; width: number; height: number }
interface TwoFactorStatus { enabled: boolean; recovery_codes_remaining: number }
interface TwoFactorSetup { secret: string; otpauth_uri: string; qr_code_data_url: string }
interface RecoveryCodes { recovery_codes: string[] }
interface PasskeyRecord { id: number; name: string; created_at: string; last_used_at?: string }
interface PasskeyOptions { ceremony_token: string; options: unknown }

export function AccountPage() {
  const client = useQueryClient()
  const { language } = useI18n()
  const zh = language === 'zh-CN'
  const session = useQuery({ queryKey: ['session'], queryFn: () => api<User>('/auth/session') })
  const [profile, setProfile] = useState<ProfileValues>({ username: '', nickname: '', email: '', current_password: '' })
  const [passwords, setPasswords] = useState({ current_password: '', new_password: '', confirm_password: '' })
  const [crop, setCrop] = useState<CropSource | null>(null)
  const [fileError, setFileError] = useState('')

  useEffect(() => {
    if (!session.data) return
    setProfile({ username: session.data.username, nickname: session.data.nickname, email: session.data.email, current_password: '' })
  }, [session.data])

  const profileChanged = !!session.data && (profile.username.trim() !== session.data.username || profile.nickname.trim() !== session.data.nickname || profile.email.trim().toLowerCase() !== session.data.email)
  const identityChanged = !!session.data && (profile.username.trim() !== session.data.username || profile.email.trim().toLowerCase() !== session.data.email)
  const updateProfile = useMutation({
    mutationFn: () => api<User>('/account/profile', { method: 'PUT', body: JSON.stringify({ ...profile, username: profile.username.trim(), nickname: profile.nickname.trim(), email: profile.email.trim() }) }),
    onSuccess: (user) => { client.setQueryData(['session'], user); setProfile({ username: user.username, nickname: user.nickname, email: user.email, current_password: '' }) },
  })
  const changePassword = useMutation({
    mutationFn: () => api('/account/password', { method: 'PUT', body: JSON.stringify(passwords) }),
    onSuccess: () => setPasswords({ current_password: '', new_password: '', confirm_password: '' }),
  })
  const uploadAvatar = useMutation({
    mutationFn: async (blob: Blob) => { const form = new FormData(); form.append('avatar', blob, 'avatar.webp'); return api<User>('/account/avatar', { method: 'PUT', body: form }) },
    onSuccess: (user) => { client.setQueryData(['session'], user); closeCrop() },
  })
  const deleteAvatar = useMutation({
    mutationFn: () => api<User>('/account/avatar', { method: 'DELETE' }),
    onSuccess: (user) => client.setQueryData(['session'], user),
  })

  const closeCrop = () => {
    setCrop((current) => { if (current) URL.revokeObjectURL(current.url); return null })
  }
  useEffect(() => () => { if (crop) URL.revokeObjectURL(crop.url) }, [crop])

  const selectAvatar = async (file?: File) => {
    setFileError('')
    uploadAvatar.reset()
    if (!file) return
    if (!avatarTypes.has(file.type)) { setFileError(zh ? '请选择 JPEG、PNG 或 WebP 图片。' : 'Choose a JPEG, PNG, or WebP image.'); return }
    if (file.size > maxAvatarBytes) { setFileError(zh ? '图片不能超过 2 MB。' : 'The image must not exceed 2 MB.'); return }
    const bytes = new Uint8Array(await file.arrayBuffer())
    if (isAnimatedSource(bytes, file.type)) { setFileError(zh ? '不支持动画头像。' : 'Animated avatars are not supported.'); return }
    const url = URL.createObjectURL(file)
    const image = new Image()
    image.onload = () => {
      if (!image.naturalWidth || !image.naturalHeight || image.naturalWidth * image.naturalHeight > 25_000_000) {
        URL.revokeObjectURL(url)
        setFileError(zh ? '图片尺寸无效或像素过大。' : 'The image dimensions are invalid or too large.')
        return
      }
      closeCrop()
      setCrop({ url, width: image.naturalWidth, height: image.naturalHeight })
    }
    image.onerror = () => { URL.revokeObjectURL(url); setFileError(zh ? '无法读取这张图片。' : 'The image could not be read.') }
    image.src = url
  }

  const removeAvatar = async () => {
    if (!session.data?.has_avatar) return
    const confirmed = await confirmDialog({ title: zh ? '删除头像？' : 'Remove avatar?', description: zh ? '将恢复为昵称或用户名缩写。' : 'Your nickname or username initials will be shown instead.', confirmLabel: zh ? '删除头像' : 'Remove avatar', danger: true })
    if (confirmed) deleteAvatar.mutate()
  }

  const submitProfile = (event: FormEvent) => { event.preventDefault(); if (profileChanged) updateProfile.mutate() }
  const submitPassword = (event: FormEvent) => { event.preventDefault(); changePassword.mutate() }
  const user = session.data

  return <ResourceFrame title={zh ? '账户设置' : 'Account settings'} detail={zh ? '管理本地管理员资料、头像和登录安全。' : 'Manage the local administrator profile, avatar, and sign-in security.'}>
    {!user ? <div className="flex min-h-48 items-center justify-center"><Spinner /></div> : <div className="mx-auto w-full max-w-5xl overflow-hidden rounded-xl border border-border/80 bg-card/40">
      <section className="flex flex-col gap-5 px-5 py-6 sm:px-7 lg:flex-row lg:flex-wrap lg:items-center">
        <UserAvatar user={user} className="size-18 shrink-0 text-xl ring-1 ring-foreground/10" />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
            <h3 className="cn-font-heading truncate text-lg font-semibold">{user.nickname || user.username}</h3>
            <span className="text-sm text-muted-foreground">@{user.username}</span>
          </div>
          <p className="mt-1 truncate text-sm text-muted-foreground">{user.email || (zh ? '尚未设置邮箱' : 'No email address')}</p>
          <p className="mt-2 text-xs text-muted-foreground">{zh ? 'JPEG、PNG 或 WebP，最大 2 MB；上传后可裁剪。' : 'JPEG, PNG, or WebP up to 2 MB; crop after upload.'}</p>
        </div>
        <div className="flex shrink-0 flex-wrap items-center gap-2">
          <Button variant="outline" render={<label />}><Camera />{user.has_avatar ? (zh ? '更换头像' : 'Replace avatar') : (zh ? '上传头像' : 'Upload avatar')}<input className="sr-only" type="file" accept="image/jpeg,image/png,image/webp" onChange={(event) => { void selectAvatar(event.target.files?.[0]); event.target.value = '' }} /></Button>
          {user.has_avatar && <Button variant="ghost" className="text-muted-foreground hover:text-destructive" disabled={deleteAvatar.isPending} onClick={() => void removeAvatar()}>{deleteAvatar.isPending ? <Spinner /> : <Trash2 />}{zh ? '移除' : 'Remove'}</Button>}
        </div>
        {(fileError || deleteAvatar.isError) && <div className="w-full space-y-2 lg:basis-full">{fileError && <InlineError message={fileError} />}{deleteAvatar.isError && <InlineError message={deleteAvatar.error.message} />}</div>}
      </section>

      <section className="grid gap-6 border-t border-border/70 px-5 py-7 sm:px-7 lg:grid-cols-[220px_minmax(0,1fr)] lg:gap-10">
        <SectionHeading icon={<UserRound />} title={zh ? '个人资料' : 'Profile'} description={zh ? '用于识别当前管理员，并作为登录凭据。' : 'Identify this administrator and manage sign-in details.'} />
        <form className="flex min-w-0 max-w-2xl flex-col gap-5" onSubmit={submitProfile}>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label={zh ? '昵称' : 'Nickname'} htmlFor="account-nickname"><Input id="account-nickname" maxLength={64} autoComplete="name" value={profile.nickname} onChange={(event) => setProfile({ ...profile, nickname: event.target.value })} /></Field>
            <Field label={zh ? '用户名' : 'Username'} htmlFor="account-username"><Input id="account-username" required minLength={3} maxLength={64} autoComplete="username" value={profile.username} onChange={(event) => setProfile({ ...profile, username: event.target.value })} /></Field>
            <div className="sm:col-span-2"><Field label={zh ? '邮箱' : 'Email'} htmlFor="account-email"><Input id="account-email" required type="email" maxLength={254} autoComplete="email" value={profile.email} onChange={(event) => setProfile({ ...profile, email: event.target.value })} /></Field></div>
            {identityChanged && <div className="sm:col-span-2"><Field label={zh ? '验证当前密码' : 'Verify current password'} htmlFor="profile-password" hint={zh ? '用户名或邮箱发生变更' : 'Username or email changed'}><Input id="profile-password" required type="password" autoComplete="current-password" value={profile.current_password} onChange={(event) => setProfile({ ...profile, current_password: event.target.value })} /></Field></div>}
          </div>
          {updateProfile.isError && <InlineError message={updateProfile.error.message} />}
          <div className="flex min-h-8 flex-wrap items-center justify-end gap-3">
            {updateProfile.isSuccess && <Success message={zh ? '个人资料已保存。' : 'Profile saved.'} />}
            <Button type="submit" disabled={!profileChanged || updateProfile.isPending}>{updateProfile.isPending ? <Spinner /> : <Save />}{zh ? '保存更改' : 'Save changes'}</Button>
          </div>
        </form>
      </section>

      <section className="grid gap-6 border-t border-border/70 px-5 py-7 sm:px-7 lg:grid-cols-[220px_minmax(0,1fr)] lg:gap-10">
        <SectionHeading icon={<ShieldCheck />} title={zh ? '登录安全' : 'Sign-in security'} description={zh ? '更新密码后，除当前设备外的其他会话将退出。' : 'Updating your password signs out every session except this device.'} />
        <form className="flex min-w-0 max-w-2xl flex-col gap-5" onSubmit={submitPassword}>
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="sm:col-span-2"><Field label={zh ? '当前密码' : 'Current password'} htmlFor="password-current"><Input id="password-current" required type="password" autoComplete="current-password" value={passwords.current_password} onChange={(event) => setPasswords({ ...passwords, current_password: event.target.value })} /></Field></div>
            <Field label={zh ? '新密码' : 'New password'} htmlFor="password-new" hint={zh ? '8–128 个字符' : '8–128 characters'}><Input id="password-new" required minLength={8} maxLength={128} type="password" autoComplete="new-password" value={passwords.new_password} onChange={(event) => setPasswords({ ...passwords, new_password: event.target.value })} /></Field>
            <Field label={zh ? '确认新密码' : 'Confirm new password'} htmlFor="password-confirm"><Input id="password-confirm" required minLength={8} maxLength={128} type="password" autoComplete="new-password" value={passwords.confirm_password} onChange={(event) => setPasswords({ ...passwords, confirm_password: event.target.value })} /></Field>
          </div>
          {changePassword.isError && <InlineError message={changePassword.error.message} />}
          <div className="flex min-h-8 flex-wrap items-center justify-end gap-3">
            {changePassword.isSuccess && <Success message={zh ? '密码已更新，其他登录会话已退出。' : 'Password updated and other sessions signed out.'} />}
            <Button type="submit" disabled={changePassword.isPending}>{changePassword.isPending ? <Spinner /> : <Save />}{zh ? '更新密码' : 'Update password'}</Button>
          </div>
        </form>
      </section>

      <section className="grid gap-6 border-t border-border/70 px-5 py-7 sm:px-7 lg:grid-cols-[220px_minmax(0,1fr)] lg:gap-10">
        <SectionHeading icon={<KeyRound />} title={zh ? '两步验证' : 'Two-factor authentication'} description={zh ? '登录时除密码外，还需要认证器生成的一次性验证码。' : 'Require a one-time authenticator code in addition to your password.'} />
        <TwoFactorSettings zh={zh} />
      </section>

      <section className="grid gap-6 border-t border-border/70 px-5 py-7 sm:px-7 lg:grid-cols-[220px_minmax(0,1fr)] lg:gap-10">
        <SectionHeading icon={<Fingerprint />} title="Passkey" description={zh ? '使用设备解锁、指纹或面容直接登录，无需输入密码。' : 'Sign in directly with device unlock, fingerprint, or face recognition.'} />
        <PasskeySettings zh={zh} twoFactorEnabled={user.two_factor_enabled} />
      </section>
    </div>}
    <AvatarCropDialog source={crop} zh={zh} pending={uploadAvatar.isPending} error={uploadAvatar.error?.message} onClose={closeCrop} onSave={(blob) => uploadAvatar.mutate(blob)} />
  </ResourceFrame>
}

function SectionHeading({ icon, title, description }: { icon: React.ReactNode; title: string; description: string }) {
  return <div className="flex gap-3 lg:block">
    <div className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-muted text-muted-foreground [&_svg]:size-4">{icon}</div>
    <div className="lg:mt-3">
      <h3 className="cn-font-heading text-sm font-semibold">{title}</h3>
      <p className="mt-1 max-w-xs text-sm leading-5 text-muted-foreground">{description}</p>
    </div>
  </div>
}

function Field({ label, htmlFor, hint, children }: { label: string; htmlFor: string; hint?: string; children: React.ReactNode }) {
  return <div className="flex flex-col gap-1.5"><div className="flex items-center justify-between gap-3"><Label htmlFor={htmlFor}>{label}</Label>{hint && <span className="text-xs text-muted-foreground">{hint}</span>}</div>{children}</div>
}

function InlineError({ message }: { message: string }) { return <Alert variant="destructive"><AlertDescription>{message}</AlertDescription></Alert> }
function Success({ message }: { message: string }) { return <p className="text-sm text-emerald-600 dark:text-emerald-400">{message}</p> }

function TwoFactorSettings({ zh }: { zh: boolean }) {
  const client = useQueryClient()
  const status = useQuery({ queryKey: ['two-factor-status'], queryFn: () => api<TwoFactorStatus>('/account/two-factor') })
  const [setupOpen, setSetupOpen] = useState(false)
  const [setupPassword, setSetupPassword] = useState('')
  const [setupData, setSetupData] = useState<TwoFactorSetup | null>(null)
  const [setupCode, setSetupCode] = useState('')
  const [action, setAction] = useState<'regenerate' | 'disable' | null>(null)
  const [actionPassword, setActionPassword] = useState('')
  const [actionCode, setActionCode] = useState('')
  const [recoveryCodes, setRecoveryCodes] = useState<string[] | null>(null)
  const [copied, setCopied] = useState(false)

  const refresh = async () => {
    await Promise.all([
      client.invalidateQueries({ queryKey: ['two-factor-status'] }),
      client.invalidateQueries({ queryKey: ['session'] }),
    ])
  }
  const begin = useMutation({
    mutationFn: () => api<TwoFactorSetup>('/account/two-factor/setup', { method: 'POST', body: JSON.stringify({ current_password: setupPassword }) }),
    onSuccess: setSetupData,
  })
  const enable = useMutation({
    mutationFn: () => api<RecoveryCodes>('/account/two-factor/enable', { method: 'POST', body: JSON.stringify({ code: setupCode }) }),
    onSuccess: (result) => { setSetupOpen(false); setRecoveryCodes(result.recovery_codes); void refresh() },
  })
  const regenerate = useMutation({
    mutationFn: () => api<RecoveryCodes>('/account/two-factor/recovery-codes', { method: 'POST', body: JSON.stringify({ current_password: actionPassword, code: actionCode }) }),
    onSuccess: (result) => { setAction(null); setRecoveryCodes(result.recovery_codes); setActionPassword(''); setActionCode(''); void refresh() },
  })
  const disable = useMutation({
    mutationFn: () => api('/account/two-factor', { method: 'DELETE', body: JSON.stringify({ current_password: actionPassword, code: actionCode }) }),
    onSuccess: () => { setAction(null); setActionPassword(''); setActionCode(''); void refresh() },
  })
  const closeSetup = () => {
    if (begin.isPending || enable.isPending) return
    setSetupOpen(false); setSetupPassword(''); setSetupData(null); setSetupCode(''); begin.reset(); enable.reset()
  }
  const openSetup = () => { setSetupOpen(true); setSetupPassword(''); setSetupData(null); setSetupCode(''); begin.reset(); enable.reset() }
  const openAction = (next: 'regenerate' | 'disable') => { setAction(next); setActionPassword(''); setActionCode(''); regenerate.reset(); disable.reset() }
  const actionMutation = action === 'disable' ? disable : regenerate

  return <div className="flex min-w-0 max-w-2xl flex-col gap-4">
    {status.isPending ? <div className="flex min-h-16 items-center"><Spinner /></div> : status.isError ? <InlineError message={status.error.message} /> : <>
      <div className="flex flex-wrap items-center justify-between gap-4 rounded-lg border border-border/70 bg-background/40 px-4 py-3.5">
        <div className="min-w-0">
          <div className="flex items-center gap-2 text-sm font-medium">
            <span className={`size-2 rounded-full ${status.data.enabled ? 'bg-emerald-500' : 'bg-muted-foreground/40'}`} />
            {status.data.enabled ? (zh ? '已启用' : 'Enabled') : (zh ? '未启用' : 'Not enabled')}
          </div>
          <p className="mt-1 text-sm text-muted-foreground">{status.data.enabled
            ? (zh ? `剩余 ${status.data.recovery_codes_remaining} 枚一次性恢复码。` : `${status.data.recovery_codes_remaining} one-time recovery codes remaining.`)
            : (zh ? '支持所有兼容 TOTP 的认证器 App。' : 'Works with any TOTP-compatible authenticator app.')}</p>
        </div>
        {!status.data.enabled ? <Button onClick={openSetup}><ShieldCheck />{zh ? '启用两步验证' : 'Enable 2FA'}</Button> : <div className="flex flex-wrap gap-2">
          <Button variant="outline" onClick={() => openAction('regenerate')}><RefreshCw />{zh ? '重新生成恢复码' : 'New recovery codes'}</Button>
          <Button variant="destructive" onClick={() => openAction('disable')}>{zh ? '停用' : 'Disable'}</Button>
        </div>}
      </div>
    </>}

    <Dialog open={setupOpen} onOpenChange={(open) => { if (!open) closeSetup() }}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{zh ? '启用两步验证' : 'Enable two-factor authentication'}</DialogTitle>
          <DialogDescription>{setupData ? (zh ? '扫描二维码并输入认证器显示的验证码。' : 'Scan the QR code and enter the code shown by your authenticator.') : (zh ? '先验证当前密码，再配置认证器。' : 'Verify your current password before configuring an authenticator.')}</DialogDescription>
        </DialogHeader>
        {!setupData ? <form className="flex flex-col gap-4" onSubmit={(event) => { event.preventDefault(); begin.mutate() }}>
          <Field label={zh ? '当前密码' : 'Current password'} htmlFor="two-factor-setup-password"><Input id="two-factor-setup-password" autoFocus required type="password" autoComplete="current-password" value={setupPassword} onChange={(event) => setSetupPassword(event.target.value)} /></Field>
          {begin.isError && <InlineError message={begin.error.message} />}
          <DialogFooter><Button type="button" variant="outline" onClick={closeSetup}>{zh ? '取消' : 'Cancel'}</Button><Button type="submit" disabled={begin.isPending}>{begin.isPending && <Spinner />}{zh ? '继续' : 'Continue'}</Button></DialogFooter>
        </form> : <form className="flex flex-col gap-4" onSubmit={(event) => { event.preventDefault(); enable.mutate() }}>
          <div className="flex flex-col items-center gap-3 sm:flex-row sm:items-start">
            {setupData.qr_code_data_url && <div className="shrink-0 rounded-lg bg-white p-2 ring-1 ring-black/10"><img src={setupData.qr_code_data_url} className="size-44" alt={zh ? '认证器设置二维码' : 'Authenticator setup QR code'} /></div>}
            <div className="min-w-0 flex-1 space-y-2 text-sm">
              <p className="text-muted-foreground">{zh ? '无法扫描？手动输入此密钥：' : 'Cannot scan? Enter this key manually:'}</p>
              <code className="block break-all rounded-lg bg-muted px-3 py-2 font-mono text-xs tracking-wider">{setupData.secret}</code>
              <div className="flex flex-wrap gap-2">
                <Button type="button" variant="outline" size="sm" onClick={() => void navigator.clipboard.writeText(setupData.secret)}><Copy />{zh ? '复制密钥' : 'Copy key'}</Button>
                <Button variant="ghost" size="sm" render={<a href={setupData.otpauth_uri} />}>{zh ? '在认证器中打开' : 'Open authenticator'}</Button>
              </div>
            </div>
          </div>
          <Field label={zh ? '6 位验证码' : '6-digit verification code'} htmlFor="two-factor-setup-code"><Input id="two-factor-setup-code" autoFocus required inputMode="numeric" autoComplete="one-time-code" minLength={6} maxLength={6} pattern="[0-9]{6}" value={setupCode} onChange={(event) => setSetupCode(event.target.value.replace(/\D/g, '').slice(0, 6))} /></Field>
          {enable.isError && <InlineError message={enable.error.message} />}
          <DialogFooter><Button type="button" variant="outline" onClick={closeSetup}>{zh ? '取消' : 'Cancel'}</Button><Button type="submit" disabled={enable.isPending || setupCode.length !== 6}>{enable.isPending && <Spinner />}{zh ? '验证并启用' : 'Verify and enable'}</Button></DialogFooter>
        </form>}
      </DialogContent>
    </Dialog>

    <Dialog open={action !== null} onOpenChange={(open) => { if (!open && !actionMutation.isPending) setAction(null) }}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{action === 'disable' ? (zh ? '停用两步验证' : 'Disable two-factor authentication') : (zh ? '重新生成恢复码' : 'Generate new recovery codes')}</DialogTitle>
          <DialogDescription>{action === 'disable' ? (zh ? '停用后，登录将只需要密码。' : 'After disabling, sign-in will only require your password.') : (zh ? '现有恢复码将立即失效。' : 'Every existing recovery code will be invalidated immediately.')}</DialogDescription>
        </DialogHeader>
        <form className="flex flex-col gap-4" onSubmit={(event) => { event.preventDefault(); if (action === 'disable') disable.mutate(); else regenerate.mutate() }}>
          <Field label={zh ? '当前密码' : 'Current password'} htmlFor="two-factor-action-password"><Input id="two-factor-action-password" required type="password" autoComplete="current-password" value={actionPassword} onChange={(event) => setActionPassword(event.target.value)} /></Field>
          <Field label={zh ? '验证码或恢复码' : 'Verification or recovery code'} htmlFor="two-factor-action-code"><Input id="two-factor-action-code" required autoComplete="one-time-code" value={actionCode} onChange={(event) => setActionCode(event.target.value)} /></Field>
          {actionMutation.isError && <InlineError message={actionMutation.error.message} />}
          <DialogFooter><Button type="button" variant="outline" disabled={actionMutation.isPending} onClick={() => setAction(null)}>{zh ? '取消' : 'Cancel'}</Button><Button type="submit" variant={action === 'disable' ? 'destructive' : 'default'} disabled={actionMutation.isPending}>{actionMutation.isPending && <Spinner />}{action === 'disable' ? (zh ? '确认停用' : 'Disable 2FA') : (zh ? '生成新恢复码' : 'Generate codes')}</Button></DialogFooter>
        </form>
      </DialogContent>
    </Dialog>

    <Dialog open={recoveryCodes !== null} onOpenChange={(open) => { if (!open) { setRecoveryCodes(null); setCopied(false) } }}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader><DialogTitle>{zh ? '保存恢复码' : 'Save your recovery codes'}</DialogTitle><DialogDescription>{zh ? '每枚恢复码只能使用一次。请立即保存到密码管理器；关闭后将无法再次查看。' : 'Each code works once. Save them in a password manager now; they cannot be viewed again after closing.'}</DialogDescription></DialogHeader>
        <div className="grid grid-cols-2 gap-2 rounded-lg border bg-muted/30 p-3">
          {(recoveryCodes ?? []).map((code) => <code key={code} className="select-all text-center font-mono text-xs sm:text-sm">{code}</code>)}
        </div>
        <DialogFooter><Button variant="outline" onClick={() => { void navigator.clipboard.writeText((recoveryCodes ?? []).join('\n')); setCopied(true) }}><Copy />{copied ? (zh ? '已复制' : 'Copied') : (zh ? '复制全部' : 'Copy all')}</Button><Button onClick={() => { setRecoveryCodes(null); setCopied(false) }}>{zh ? '我已保存' : 'I saved them'}</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
}

function PasskeySettings({ zh, twoFactorEnabled }: { zh: boolean; twoFactorEnabled: boolean }) {
  const client = useQueryClient()
  const passkeys = useQuery({ queryKey: ['passkeys'], queryFn: () => api<PasskeyRecord[]>('/account/passkeys') })
  const [addOpen, setAddOpen] = useState(false)
  const [name, setName] = useState('')
  const [password, setPassword] = useState('')
  const [code, setCode] = useState('')
  const [rename, setRename] = useState<PasskeyRecord | null>(null)
  const [renameValue, setRenameValue] = useState('')
  const [remove, setRemove] = useState<PasskeyRecord | null>(null)
  const available = !demoMode && passkeysAvailable()

  const add = useMutation({
    mutationFn: async () => {
      const begin = await api<PasskeyOptions>('/account/passkeys/options', { method: 'POST', body: JSON.stringify({ name, current_password: password, code }) })
      const credential = await createPasskey(begin.options)
      return api<PasskeyRecord>('/account/passkeys', { method: 'POST', headers: { 'X-WebAuthn-Ceremony': begin.ceremony_token }, body: JSON.stringify(credential) })
    },
    onSuccess: async () => { setAddOpen(false); setName(''); setPassword(''); setCode(''); await client.invalidateQueries({ queryKey: ['passkeys'] }) },
  })
  const updateName = useMutation({
    mutationFn: () => api(`/account/passkeys/${rename?.id}`, { method: 'PATCH', body: JSON.stringify({ name: renameValue }) }),
    onSuccess: async () => { setRename(null); setRenameValue(''); await client.invalidateQueries({ queryKey: ['passkeys'] }) },
  })
  const deletePasskey = useMutation({
    mutationFn: () => api(`/account/passkeys/${remove?.id}`, { method: 'DELETE', body: JSON.stringify({ current_password: password, code }) }),
    onSuccess: async () => { setRemove(null); setPassword(''); setCode(''); await client.invalidateQueries({ queryKey: ['passkeys'] }) },
  })
  const closeAdd = () => { if (!add.isPending) { setAddOpen(false); setName(''); setPassword(''); setCode(''); add.reset() } }
  const closeDelete = () => { if (!deletePasskey.isPending) { setRemove(null); setPassword(''); setCode(''); deletePasskey.reset() } }

  return <div className="flex min-w-0 max-w-2xl flex-col gap-4">
    {!available && <InlineError message={demoMode ? (zh ? '演示模式不会创建真实 Passkey。' : 'Demo mode does not create real passkeys.') : (zh ? '当前浏览器或连接不支持 Passkey。请使用 HTTPS，或在 localhost 上访问。' : 'This browser or connection does not support passkeys. Use HTTPS or access SUMA on localhost.')} />}
    {passkeys.isPending ? <div className="flex min-h-16 items-center"><Spinner /></div> : passkeys.isError ? <InlineError message={passkeys.error.message} /> : <div className="overflow-hidden rounded-lg border border-border/70 bg-background/40">
      {passkeys.data.length === 0 ? <div className="px-4 py-5 text-sm text-muted-foreground">{zh ? '尚未添加 Passkey。添加后即可在登录页直接使用设备验证。' : 'No passkeys yet. Add one to sign in with device verification.'}</div> : passkeys.data.map((passkey, index) => <div key={passkey.id} className={`flex flex-wrap items-center gap-3 px-4 py-3.5 ${index ? 'border-t border-border/70' : ''}`}>
        <div className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-muted text-muted-foreground"><Fingerprint className="size-4" /></div>
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm font-medium">{passkey.name}</p>
          <p className="mt-0.5 text-xs text-muted-foreground">{passkey.last_used_at ? (zh ? `最近使用 ${new Date(passkey.last_used_at).toLocaleString()}` : `Last used ${new Date(passkey.last_used_at).toLocaleString()}`) : (zh ? `添加于 ${new Date(passkey.created_at).toLocaleDateString()}` : `Added ${new Date(passkey.created_at).toLocaleDateString()}`)}</p>
        </div>
        <div className="flex items-center gap-1">
          <Button size="icon-sm" variant="ghost" aria-label={zh ? '重命名 Passkey' : 'Rename passkey'} onClick={() => { setRename(passkey); setRenameValue(passkey.name); updateName.reset() }}><Pencil /></Button>
          <Button size="icon-sm" variant="ghost" className="text-muted-foreground hover:text-destructive" aria-label={zh ? '删除 Passkey' : 'Delete passkey'} onClick={() => { setRemove(passkey); setPassword(''); setCode(''); deletePasskey.reset() }}><Trash2 /></Button>
        </div>
      </div>)}
    </div>}
    <div className="flex justify-end"><Button disabled={!available} onClick={() => { setAddOpen(true); add.reset() }}><Plus />{zh ? '添加 Passkey' : 'Add passkey'}</Button></div>

    <Dialog open={addOpen} onOpenChange={(open) => { if (!open) closeAdd() }}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader><DialogTitle>{zh ? '添加 Passkey' : 'Add a passkey'}</DialogTitle><DialogDescription>{zh ? '为这台设备或安全密钥创建一个容易识别的名称。' : 'Create a recognizable name for this device or security key.'}</DialogDescription></DialogHeader>
        <form className="flex flex-col gap-4" onSubmit={(event) => { event.preventDefault(); add.mutate() }}>
          <Field label={zh ? '名称' : 'Name'} htmlFor="passkey-name"><Input id="passkey-name" autoFocus required maxLength={64} value={name} onChange={(event) => setName(event.target.value)} placeholder={zh ? '例如：MacBook Touch ID' : 'e.g. MacBook Touch ID'} /></Field>
          <Field label={zh ? '当前密码' : 'Current password'} htmlFor="passkey-password"><Input id="passkey-password" required type="password" autoComplete="current-password" value={password} onChange={(event) => setPassword(event.target.value)} /></Field>
          {twoFactorEnabled && <Field label={zh ? '验证码或恢复码' : 'Verification or recovery code'} htmlFor="passkey-code"><Input id="passkey-code" required autoComplete="one-time-code" value={code} onChange={(event) => setCode(event.target.value)} /></Field>}
          {add.isError && <InlineError message={add.error.message} />}
          <DialogFooter><Button type="button" variant="outline" disabled={add.isPending} onClick={closeAdd}>{zh ? '取消' : 'Cancel'}</Button><Button type="submit" disabled={add.isPending || !name.trim()}>{add.isPending ? <Spinner /> : <Fingerprint />}{zh ? '继续设备验证' : 'Continue'}</Button></DialogFooter>
        </form>
      </DialogContent>
    </Dialog>

    <Dialog open={rename !== null} onOpenChange={(open) => { if (!open && !updateName.isPending) setRename(null) }}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader><DialogTitle>{zh ? '重命名 Passkey' : 'Rename passkey'}</DialogTitle><DialogDescription>{zh ? '名称仅用于帮助你识别登录设备。' : 'The name only helps you identify the sign-in device.'}</DialogDescription></DialogHeader>
        <form className="flex flex-col gap-4" onSubmit={(event) => { event.preventDefault(); updateName.mutate() }}>
          <Field label={zh ? '名称' : 'Name'} htmlFor="passkey-rename"><Input id="passkey-rename" autoFocus required maxLength={64} value={renameValue} onChange={(event) => setRenameValue(event.target.value)} /></Field>
          {updateName.isError && <InlineError message={updateName.error.message} />}
          <DialogFooter><Button type="button" variant="outline" disabled={updateName.isPending} onClick={() => setRename(null)}>{zh ? '取消' : 'Cancel'}</Button><Button type="submit" disabled={updateName.isPending || !renameValue.trim()}>{updateName.isPending && <Spinner />}{zh ? '保存' : 'Save'}</Button></DialogFooter>
        </form>
      </DialogContent>
    </Dialog>

    <Dialog open={remove !== null} onOpenChange={(open) => { if (!open) closeDelete() }}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader><DialogTitle>{zh ? `删除“${remove?.name ?? ''}”？` : `Delete “${remove?.name ?? ''}”?`}</DialogTitle><DialogDescription>{zh ? '删除后，这枚 Passkey 将不能再登录 SUMA。其他设备上的会话也会退出。' : 'This passkey will no longer sign in to SUMA. Sessions on other devices will also be signed out.'}</DialogDescription></DialogHeader>
        <form className="flex flex-col gap-4" onSubmit={(event) => { event.preventDefault(); deletePasskey.mutate() }}>
          <Field label={zh ? '当前密码' : 'Current password'} htmlFor="passkey-delete-password"><Input id="passkey-delete-password" autoFocus required type="password" autoComplete="current-password" value={password} onChange={(event) => setPassword(event.target.value)} /></Field>
          {twoFactorEnabled && <Field label={zh ? '验证码或恢复码' : 'Verification or recovery code'} htmlFor="passkey-delete-code"><Input id="passkey-delete-code" required autoComplete="one-time-code" value={code} onChange={(event) => setCode(event.target.value)} /></Field>}
          {deletePasskey.isError && <InlineError message={deletePasskey.error.message} />}
          <DialogFooter><Button type="button" variant="outline" disabled={deletePasskey.isPending} onClick={closeDelete}>{zh ? '取消' : 'Cancel'}</Button><Button type="submit" variant="destructive" disabled={deletePasskey.isPending}>{deletePasskey.isPending && <Spinner />}{zh ? '删除 Passkey' : 'Delete passkey'}</Button></DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  </div>
}

function AvatarCropDialog({ source, zh, pending, error, onClose, onSave }: { source: CropSource | null; zh: boolean; pending: boolean; error?: string; onClose: () => void; onSave: (blob: Blob) => void }) {
  const viewport = 280
  const output = 512
  const [zoom, setZoom] = useState(1)
  const [offset, setOffset] = useState({ x: 0, y: 0 })
  const [exportError, setExportError] = useState('')
  const drag = useRef<{ x: number; y: number; ox: number; oy: number } | null>(null)
  useEffect(() => { setZoom(1); setOffset({ x: 0, y: 0 }); setExportError('') }, [source])
  if (!source) return null
  const baseScale = Math.max(output / source.width, output / source.height)
  const rendered = { width: source.width * baseScale * zoom, height: source.height * baseScale * zoom }
  const limit = { x: Math.max(0, (rendered.width - output) / 2), y: Math.max(0, (rendered.height - output) / 2) }
  const clamp = (value: { x: number; y: number }) => ({ x: Math.max(-limit.x, Math.min(limit.x, value.x)), y: Math.max(-limit.y, Math.min(limit.y, value.y)) })
  const beginDrag = (event: ReactPointerEvent<HTMLDivElement>) => { event.currentTarget.setPointerCapture(event.pointerId); drag.current = { x: event.clientX, y: event.clientY, ox: offset.x, oy: offset.y } }
  const moveDrag = (event: ReactPointerEvent<HTMLDivElement>) => { if (!drag.current) return; const factor = output / viewport; setOffset(clamp({ x: drag.current.ox + (event.clientX - drag.current.x) * factor, y: drag.current.oy + (event.clientY - drag.current.y) * factor })) }
  const save = async () => {
    setExportError('')
    try {
      const image = new Image()
      image.src = source.url
      await image.decode()
      const canvas = document.createElement('canvas')
      canvas.width = output; canvas.height = output
      const context = canvas.getContext('2d')
      if (!context) throw new Error('canvas unavailable')
      context.drawImage(image, (output - rendered.width) / 2 + offset.x, (output - rendered.height) / 2 + offset.y, rendered.width, rendered.height)
      const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, 'image/webp', 0.9))
      if (!blob || blob.type !== 'image/webp' || blob.size > maxAvatarBytes) throw new Error('webp export failed')
      onSave(blob)
    } catch {
      setExportError(zh ? '浏览器无法生成头像，请换一张图片后重试。' : 'The browser could not create the avatar. Try another image.')
    }
  }
  const displayFactor = viewport / output
  return <Dialog open onOpenChange={(open) => { if (!open && !pending) onClose() }}>
    <DialogContent className="sm:max-w-md">
      <DialogHeader><DialogTitle>{zh ? '裁剪头像' : 'Crop avatar'}</DialogTitle><DialogDescription>{zh ? '拖动图片调整位置，并使用滑块缩放。' : 'Drag to reposition the image and use the slider to zoom.'}</DialogDescription></DialogHeader>
      <div className="flex flex-col items-center gap-4">
        <div className="relative size-[280px] touch-none cursor-move overflow-hidden rounded-xl bg-muted ring-1 ring-border" onPointerDown={beginDrag} onPointerMove={moveDrag} onPointerUp={() => { drag.current = null }} onPointerCancel={() => { drag.current = null }}>
          <img src={source.url} alt="" draggable={false} className="pointer-events-none absolute max-w-none select-none" style={{ width: rendered.width * displayFactor, height: rendered.height * displayFactor, left: (viewport - rendered.width * displayFactor) / 2 + offset.x * displayFactor, top: (viewport - rendered.height * displayFactor) / 2 + offset.y * displayFactor }} />
          <div className="pointer-events-none absolute inset-0 rounded-full ring-[70px] ring-black/45" />
        </div>
        <div className="flex w-full items-center gap-3"><UserRound className="size-4 text-muted-foreground" /><input className="w-full accent-foreground" type="range" min="1" max="3" step="0.01" value={zoom} aria-label={zh ? '头像缩放' : 'Avatar zoom'} onChange={(event) => { const next = Number(event.target.value); setZoom(next); setOffset({ x: 0, y: 0 }) }} /><Camera className="size-5 text-muted-foreground" /></div>
        {(error || exportError) && <InlineError message={error || exportError} />}
      </div>
      <DialogFooter><Button variant="outline" disabled={pending} onClick={onClose}>{zh ? '取消' : 'Cancel'}</Button><Button disabled={pending} onClick={() => void save()}>{pending ? <Spinner /> : <Save />}{zh ? '保存头像' : 'Save avatar'}</Button></DialogFooter>
    </DialogContent>
  </Dialog>
}

function isAnimatedSource(bytes: Uint8Array, type: string) {
  const ascii = (offset: number, length: number) => String.fromCharCode(...bytes.subarray(offset, offset + length))
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength)
  if (type === 'image/webp' && bytes.length >= 12 && ascii(0, 4) === 'RIFF' && ascii(8, 4) === 'WEBP') {
    for (let offset = 12; offset + 8 <= bytes.length;) {
      const name = ascii(offset, 4)
      const size = view.getUint32(offset + 4, true)
      if (name === 'ANIM') return true
      if (offset + 8 + size > bytes.length) return false
      offset += 8 + size + size % 2
    }
  }
  if (type === 'image/png' && bytes.length >= 8) {
    for (let offset = 8; offset + 12 <= bytes.length;) {
      const size = view.getUint32(offset, false)
      if (ascii(offset + 4, 4) === 'acTL') return true
      if (offset + 12 + size > bytes.length) return false
      offset += 12 + size
    }
  }
  return false
}
