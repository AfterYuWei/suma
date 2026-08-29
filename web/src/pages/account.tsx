import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Camera, Save, Trash2, UserRound } from 'lucide-react'
import { type FormEvent, type PointerEvent as ReactPointerEvent, useEffect, useRef, useState } from 'react'
import { Alert, AlertDescription } from '../components/ui/alert'
import { Button } from '../components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '../components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '../components/ui/dialog'
import { Input } from '../components/ui/input'
import { Label } from '../components/ui/label'
import { Spinner } from '../components/ui/spinner'
import { UserAvatar } from '../components/ui/user-avatar'
import type { User } from '../features/auth/types'
import { api } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { confirmDialog } from '../stores/dialog'
import { ResourceFrame } from './images'

const maxAvatarBytes = 2 * 1024 * 1024
const avatarTypes = new Set(['image/jpeg', 'image/png', 'image/webp'])

interface ProfileValues { username: string; nickname: string; email: string; current_password: string }
interface CropSource { url: string; width: number; height: number }

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

  return <ResourceFrame title={zh ? '账户设置' : 'Account settings'} detail={zh ? '管理本地管理员资料、头像和登录密码。' : 'Manage the local administrator profile, avatar, and password.'}>
    {!user ? <div className="flex min-h-48 items-center justify-center"><Spinner /></div> : <div className="grid w-full max-w-4xl gap-5 lg:grid-cols-2">
      <Card className="lg:col-span-2">
        <CardHeader><CardTitle>{zh ? '头像' : 'Avatar'}</CardTitle></CardHeader>
        <CardContent className="flex flex-wrap items-center gap-5">
          <UserAvatar user={user} className="size-20 text-xl" />
          <div className="flex min-w-0 flex-1 flex-col gap-2">
            <p className="text-sm text-muted-foreground">{zh ? '支持 JPEG、PNG、WebP，最大 2 MB。上传后裁剪为正方形。' : 'JPEG, PNG, or WebP up to 2 MB. Images are cropped to a square.'}</p>
            <div className="flex flex-wrap gap-2">
              <Button variant="outline" render={<label />}><Camera />{user.has_avatar ? (zh ? '更换头像' : 'Replace avatar') : (zh ? '上传头像' : 'Upload avatar')}<input className="sr-only" type="file" accept="image/jpeg,image/png,image/webp" onChange={(event) => { void selectAvatar(event.target.files?.[0]); event.target.value = '' }} /></Button>
              {user.has_avatar && <Button variant="ghost" disabled={deleteAvatar.isPending} onClick={() => void removeAvatar()}>{deleteAvatar.isPending ? <Spinner /> : <Trash2 />}{zh ? '删除' : 'Remove'}</Button>}
            </div>
            {fileError && <InlineError message={fileError} />}
            {deleteAvatar.isError && <InlineError message={deleteAvatar.error.message} />}
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader><CardTitle>{zh ? '个人资料' : 'Profile'}</CardTitle></CardHeader>
        <CardContent><form className="flex flex-col gap-4" onSubmit={submitProfile}>
          <Field label={zh ? '昵称' : 'Nickname'} htmlFor="account-nickname"><Input id="account-nickname" maxLength={64} autoComplete="name" value={profile.nickname} onChange={(event) => setProfile({ ...profile, nickname: event.target.value })} /></Field>
          <Field label={zh ? '用户名' : 'Username'} htmlFor="account-username"><Input id="account-username" required minLength={3} maxLength={64} autoComplete="username" value={profile.username} onChange={(event) => setProfile({ ...profile, username: event.target.value })} /></Field>
          <Field label={zh ? '邮箱' : 'Email'} htmlFor="account-email"><Input id="account-email" required type="email" maxLength={254} autoComplete="email" value={profile.email} onChange={(event) => setProfile({ ...profile, email: event.target.value })} /></Field>
          {identityChanged && <Field label={zh ? '当前密码' : 'Current password'} htmlFor="profile-password" hint={zh ? '修改用户名或邮箱时必须验证。' : 'Required when changing username or email.'}><Input id="profile-password" required type="password" autoComplete="current-password" value={profile.current_password} onChange={(event) => setProfile({ ...profile, current_password: event.target.value })} /></Field>}
          {updateProfile.isError && <InlineError message={updateProfile.error.message} />}
          {updateProfile.isSuccess && <Success message={zh ? '个人资料已保存。' : 'Profile saved.'} />}
          <Button type="submit" disabled={!profileChanged || updateProfile.isPending}>{updateProfile.isPending ? <Spinner /> : <Save />}{zh ? '保存资料' : 'Save profile'}</Button>
        </form></CardContent>
      </Card>

      <Card>
        <CardHeader><CardTitle>{zh ? '修改密码' : 'Change password'}</CardTitle></CardHeader>
        <CardContent><form className="flex flex-col gap-4" onSubmit={submitPassword}>
          <Field label={zh ? '当前密码' : 'Current password'} htmlFor="password-current"><Input id="password-current" required type="password" autoComplete="current-password" value={passwords.current_password} onChange={(event) => setPasswords({ ...passwords, current_password: event.target.value })} /></Field>
          <Field label={zh ? '新密码' : 'New password'} htmlFor="password-new" hint={zh ? '8–128 个字符' : '8–128 characters'}><Input id="password-new" required minLength={8} maxLength={128} type="password" autoComplete="new-password" value={passwords.new_password} onChange={(event) => setPasswords({ ...passwords, new_password: event.target.value })} /></Field>
          <Field label={zh ? '确认新密码' : 'Confirm new password'} htmlFor="password-confirm"><Input id="password-confirm" required minLength={8} maxLength={128} type="password" autoComplete="new-password" value={passwords.confirm_password} onChange={(event) => setPasswords({ ...passwords, confirm_password: event.target.value })} /></Field>
          {changePassword.isError && <InlineError message={changePassword.error.message} />}
          {changePassword.isSuccess && <Success message={zh ? '密码已更新，其他登录会话已退出。' : 'Password updated and other sessions signed out.'} />}
          <Button type="submit" disabled={changePassword.isPending}>{changePassword.isPending ? <Spinner /> : <Save />}{zh ? '更新密码' : 'Update password'}</Button>
        </form></CardContent>
      </Card>
    </div>}
    <AvatarCropDialog source={crop} zh={zh} pending={uploadAvatar.isPending} error={uploadAvatar.error?.message} onClose={closeCrop} onSave={(blob) => uploadAvatar.mutate(blob)} />
  </ResourceFrame>
}

function Field({ label, htmlFor, hint, children }: { label: string; htmlFor: string; hint?: string; children: React.ReactNode }) {
  return <div className="flex flex-col gap-1.5"><div className="flex items-center justify-between gap-3"><Label htmlFor={htmlFor}>{label}</Label>{hint && <span className="text-xs text-muted-foreground">{hint}</span>}</div>{children}</div>
}

function InlineError({ message }: { message: string }) { return <Alert variant="destructive"><AlertDescription>{message}</AlertDescription></Alert> }
function Success({ message }: { message: string }) { return <p className="text-sm text-emerald-600 dark:text-emerald-400">{message}</p> }

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
