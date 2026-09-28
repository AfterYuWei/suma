import { useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, ArrowLeft, ChevronRight, Copy, File, FilePlus2, Folder, FolderPlus, History, MoreHorizontal, MoveRight, Pencil, RefreshCw, RotateCcw, Save, Trash2 } from 'lucide-react'
import { lazy, Suspense, useCallback, useEffect, useState } from 'react'
import { Alert, AlertDescription } from '../../components/ui/alert'
import { Badge } from '../../components/ui/badge'
import { Button } from '../../components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '../../components/ui/dropdown-menu'
import { Spinner } from '../../components/ui/spinner'
import type { ContainerDetail } from './types'
import { api, ApiError } from '../../lib/api'
import { useI18n } from '../../lib/i18n'
import { nodePath } from '../../lib/nodes'
import { confirmDialog, promptDialog } from '../../stores/dialog'
import { useUIStore } from '../../stores/ui'

const Monaco = lazy(() => import('../../lib/monaco-editor'))
const MonacoDiff = lazy(() => import('../../lib/monaco-editor').then((module) => ({ default: module.MonacoDiffEditor })))

interface Mount { type: string; name?: string; source?: string; destination: string; read_write: boolean; is_directory?: boolean }
interface Entry { name: string; path: string; type: 'file' | 'directory' | 'link' | 'special'; size: number; modified_at: number; mount?: Mount }
interface Listing { path: string; entries: Entry[]; next_cursor?: string; mounts: Mount[]; current_mount?: Mount }
interface Content { path: string; content: string; etag: string; persistent: boolean; read_only: boolean; single_file_bind: boolean; warning?: string }
interface Revision { id: number; hash: string; user_id?: number; created_at: string; baseline: boolean }
interface FileTask { id: string; status: string; progress: number; message: string }

const languageFor = (name: string) => {
  const basename = name.split('/').pop()?.toLowerCase() ?? ''
  if (basename === '.env' || basename.startsWith('.env.')) return 'ini'
  if (basename === 'dockerfile' || basename.startsWith('dockerfile.')) return 'dockerfile'
  const suffix = name.toLowerCase().split('.').pop() ?? ''
  return ({ js: 'javascript', jsx: 'javascript', ts: 'typescript', tsx: 'typescript', json: 'json', yaml: 'yaml', yml: 'yaml', html: 'html', css: 'css', scss: 'scss', go: 'go', sh: 'shell', bash: 'shell', md: 'markdown', xml: 'xml', sql: 'sql', py: 'python', toml: 'ini', ini: 'ini', conf: 'ini', properties: 'ini' } as Record<string, string>)[suffix] ?? 'plaintext'
}

const join = (folder: string, name: string) => `${folder === '/' ? '' : folder}/${name}`

export function ContainerFileManager({ nodeID, container, onDirtyChange }: { nodeID: string; container: ContainerDetail; onDirtyChange?: (dirty: boolean) => void }) {
  const client = useQueryClient()
  const zh = useI18n().language === 'zh-CN'
  const tx = useCallback((chinese: string, english: string) => zh ? chinese : english, [zh])
  const dark = useUIStore((state) => state.resolvedDark)
  const [folder, setFolder] = useState('/')
  const [cursor, setCursor] = useState('')
  const [cursorStack, setCursorStack] = useState<string[]>([])
  const [selected, setSelected] = useState<string | null>(null)
  const [draft, setDraft] = useState<string | null>(null)
  const [notice, setNotice] = useState('')
  const [historyOpen, setHistoryOpen] = useState(false)
  const [revisionID, setRevisionID] = useState<number | null>(null)
  const [taskID, setTaskID] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [revealed, setRevealed] = useState(false)
  const [conflictContent, setConflictContent] = useState<Content | null>(null)
  const [showConflictDiff, setShowConflictDiff] = useState(false)
  const base = nodePath(nodeID, `/containers/${encodeURIComponent(container.id)}/files`)
  const listing = useQuery({ queryKey: ['container-files', nodeID, container.id, folder, cursor], queryFn: () => api<Listing>(`${base}?path=${encodeURIComponent(folder)}&cursor=${encodeURIComponent(cursor)}`), enabled: container.state === 'running', refetchInterval: 5_000 })
  const content = useQuery({ queryKey: ['container-file-content', nodeID, container.id, selected], queryFn: () => api<Content>(`${base}/content?path=${encodeURIComponent(selected ?? '')}`), enabled: !!selected && container.state === 'running', retry: false })
  const history = useQuery({ queryKey: ['container-file-history', nodeID, container.id, selected], queryFn: () => api<Revision[]>(`${base}/history?path=${encodeURIComponent(selected ?? '')}`), enabled: !!selected && historyOpen })
  const revision = useQuery({ queryKey: ['container-file-revision', nodeID, container.id, selected, revisionID], queryFn: () => api<Content>(`${base}/history/${revisionID}?path=${encodeURIComponent(selected ?? '')}`), enabled: !!selected && revisionID !== null })
  const task = useQuery({ queryKey: ['container-file-task', nodeID, taskID], queryFn: () => api<FileTask>(nodePath(nodeID, `/tasks/${taskID}`)), enabled: !!taskID, refetchInterval: (query) => { const state = query.state.data?.status; return state === 'success' || state === 'failed' || state === 'canceled' ? false : 1000 } })
  const dirty = content.data && draft !== null && draft !== content.data.content
  const canSave = content.data?.persistent && !content.data.read_only && dirty
  const sensitive = !!selected && (/(^|\/)(\.env(?:\..*)?|[^/]*(?:secret|token|credential|private[_-]?key)[^/]*)$/i.test(selected) || /\.(?:pem|p12|pfx|key)$/i.test(selected) || /(?:PASSWORD|TOKEN|SECRET|API_KEY|PRIVATE_KEY)\s*[:=]/i.test(content.data?.content ?? ''))

  useEffect(() => { if (content.data && draft === null) setDraft(content.data.content) }, [content.data, draft])
  useEffect(() => { onDirtyChange?.(!!dirty); return () => onDirtyChange?.(false) }, [dirty, onDirtyChange])
  useEffect(() => { const warn = (event: BeforeUnloadEvent) => { if (dirty) event.preventDefault() }; window.addEventListener('beforeunload', warn); return () => window.removeEventListener('beforeunload', warn) }, [dirty])
  useEffect(() => {
    if (!task.data || !['success', 'failed', 'canceled'].includes(task.data.status)) return
    setNotice(task.data.status === 'success' ? tx('文件操作已完成。', 'File operation completed.') : tx(`文件操作失败：${task.data.message}`, `File operation failed: ${task.data.message}`))
    void client.invalidateQueries({ queryKey: ['container-files', nodeID, container.id] })
    setTaskID(null)
  }, [task.data, client, nodeID, container.id, tx])

  const enter = async (next: string) => {
    if (dirty && !await confirmDialog({ title: tx('放弃未保存的更改？', 'Discard unsaved changes?'), description: selected ?? '', confirmLabel: tx('放弃', 'Discard'), danger: true })) return
    setFolder(next); setCursor(''); setCursorStack([]); setSelected(null); setDraft(null); setHistoryOpen(false); setRevisionID(null); setRevealed(false); setConflictContent(null); setShowConflictDiff(false)
  }
  const select = async (file: string) => {
    if (dirty && !await confirmDialog({ title: tx('放弃未保存的更改？', 'Discard unsaved changes?'), description: selected ?? '', confirmLabel: tx('放弃', 'Discard'), danger: true })) return
    setSelected(file); setDraft(null); setHistoryOpen(false); setRevisionID(null); setNotice(''); setRevealed(false); setConflictContent(null); setShowConflictDiff(false)
  }
  const refresh = () => { void listing.refetch(); if (selected) void content.refetch() }

  const save = async () => {
    if (!content.data || draft === null || !canSave || busy) return
    setBusy(true); setNotice('')
    try {
      const updated = await api<Content>(`${base}/content`, { method: 'PUT', body: JSON.stringify({ path: selected, content: draft, etag: content.data.etag }) })
      client.setQueryData(['container-file-content', nodeID, container.id, selected], updated)
      void client.invalidateQueries({ queryKey: ['container-file-history', nodeID, container.id, selected] })
      setConflictContent(null); setShowConflictDiff(false)
      setNotice(tx('文件已保存。', 'File saved.'))
    } catch (error) {
      if (error instanceof ApiError && error.status === 409 && selected) {
        try { setConflictContent(await api<Content>(`${base}/content?path=${encodeURIComponent(selected)}`)) } catch { /* Retain the draft even when refreshing the conflict fails. */ }
      }
      setNotice(error instanceof ApiError && error.status === 409 ? tx('文件已在别处修改。请查看差异或重新加载后再保存。', 'The file changed elsewhere. Review the diff or reload before saving.') : (error as Error).message)
    } finally { setBusy(false) }
  }

  useEffect(() => {
    const handler = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 's' && selected) { event.preventDefault(); void save() }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  })

  const action = async (kind: string, file?: Entry) => {
    let source = file?.path ?? folder
    let target = ''
    if (file && selected === file.path && dirty && ['rename', 'move', 'delete'].includes(kind) && !await confirmDialog({ title: tx('放弃未保存的更改？', 'Discard unsaved changes?'), description: file.path, confirmLabel: tx('放弃', 'Discard'), danger: true })) return
    if (kind === 'create_file' || kind === 'create_directory') {
      const name = await promptDialog({ title: kind === 'create_file' ? tx('新建文件', 'New file') : tx('新建文件夹', 'New folder'), confirmLabel: tx('创建', 'Create'), input: { label: tx('名称', 'Name') } })
      if (!name) return
      source = join(folder, name)
    } else if (kind === 'rename' && file) {
      const name = await promptDialog({ title: tx('重命名', 'Rename'), confirmLabel: tx('保存', 'Save'), input: { label: tx('新名称', 'New name'), initialValue: file.name } })
      if (!name) return
      target = join(folder, name)
    } else if ((kind === 'copy' || kind === 'move') && file) {
      const value = await promptDialog({ title: kind === 'copy' ? tx('复制到', 'Copy to') : tx('移动到', 'Move to'), confirmLabel: kind === 'copy' ? tx('复制', 'Copy') : tx('移动', 'Move'), input: { label: tx('目标绝对路径', 'Absolute destination path'), initialValue: join(folder, file.name) } })
      if (!value) return
      target = value
    } else if (kind === 'delete' && file) {
      if (!await confirmDialog({ title: tx(`删除 ${file.name}？`, `Delete ${file.name}?`), description: file.type === 'directory' ? tx('文件夹内的所有内容会被永久删除。', 'All contents of this folder will be permanently deleted.') : tx('此文件会被永久删除。', 'This file will be permanently deleted.'), confirmLabel: tx('删除', 'Delete'), danger: true })) return
    }
    setBusy(true); setNotice('')
    try {
      const result = await api<FileTask | { path: string }>(`${base}/actions`, { method: 'POST', body: JSON.stringify({ action: kind, path: source, target }) })
      if ('id' in result) { setTaskID(result.id); setNotice(tx('文件操作已开始。', 'File operation started.')) }
      else { setNotice(tx('文件操作已完成。', 'File operation completed.')); void client.invalidateQueries({ queryKey: ['container-files', nodeID, container.id] }) }
      if (selected === source && (kind === 'delete' || kind === 'move' || kind === 'rename')) { setSelected(null); setDraft(null) }
    } catch (error) { setNotice((error as Error).message) }
    finally { setBusy(false) }
  }

  const mounts = listing.data?.mounts ?? []
  const crumbs = folder === '/' ? [] : folder.split('/').filter(Boolean)
  if (container.state !== 'running') return <Alert><AlertTriangle /><AlertDescription>{tx('文件工具需要运行中的容器。启动容器后可浏览和编辑文件。', 'File tools require a running container. Start it to browse and edit files.')}</AlertDescription></Alert>

  return <div className="space-y-4">
    <div className="flex flex-wrap items-center gap-2">
      <span className="text-xs text-muted-foreground">{tx('持久化目录', 'Persistent folders')}</span>
      {listing.data && mounts.filter((mount) => mount.type === 'bind' || mount.type === 'volume').length === 0 && <span className="text-sm text-muted-foreground">{tx('此容器没有 bind mount 或 Docker volume。', 'This container has no bind mounts or Docker volumes.')}</span>}
      {mounts.filter((mount) => mount.type === 'bind' || mount.type === 'volume').map((mount) => <Button key={mount.destination} size="sm" variant="outline" title={`${mount.type}: ${mount.source || mount.name || ''}`} onClick={() => mount.is_directory ? void enter(mount.destination) : void select(mount.destination)}>{mount.is_directory ? <Folder className="size-4" /> : <File className="size-4" />}{mount.destination}<span className="max-w-36 truncate text-xs opacity-70">{mount.name || mount.source}</span><Badge variant="secondary">{mount.read_write ? tx('读写', 'Read/write') : tx('只读', 'Read only')}</Badge></Button>)}
    </div>
    <div className="grid min-w-0 gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)]">
      <section className={`min-w-0 rounded-lg border ${selected ? 'hidden lg:block' : ''}`}>
        <div className="flex flex-wrap items-center justify-between gap-2 border-b p-2">
          <div className="flex min-w-0 flex-wrap items-center gap-1 text-sm"><Button size="sm" variant="ghost" onClick={() => void enter('/')}>/</Button>{crumbs.map((part, index) => <span key={`${index}-${part}`} className="flex items-center gap-1"><ChevronRight className="size-3 text-muted-foreground" /><Button size="sm" variant="ghost" onClick={() => void enter(`/${crumbs.slice(0, index + 1).join('/')}`)}>{part}</Button></span>)}</div>
          <div className="flex items-center gap-1"><Button size="icon-sm" variant="ghost" aria-label={tx('刷新文件列表', 'Refresh files')} onClick={refresh}><RefreshCw /></Button><Button size="icon-sm" variant="ghost" aria-label={tx('新建文件', 'New file')} disabled={busy || listing.data?.current_mount?.read_write === false} onClick={() => void action('create_file')}><FilePlus2 /></Button><Button size="icon-sm" variant="ghost" aria-label={tx('新建文件夹', 'New folder')} disabled={busy || listing.data?.current_mount?.read_write === false} onClick={() => void action('create_directory')}><FolderPlus /></Button></div>
        </div>
        {listing.data?.current_mount && <p className="border-b px-3 py-2 text-xs text-muted-foreground">{listing.data.current_mount.type === 'bind' ? 'Bind mount' : 'Docker volume'} · {listing.data.current_mount.destination} · {listing.data.current_mount.read_write ? tx('持久化读写', 'Persistent, read/write') : tx('持久化只读', 'Persistent, read only')}</p>}
        {listing.isPending && <p className="p-4 text-sm text-muted-foreground">{tx('正在读取目录…', 'Loading directory…')}</p>}
        {listing.isError && <Alert variant="destructive" className="m-2 w-auto"><AlertDescription>{listing.error.message}</AlertDescription></Alert>}
        <div className="max-h-[62vh] overflow-auto divide-y divide-border">
          {listing.data?.entries.map((item) => <div key={item.path} className="group flex min-w-0 items-center gap-2 px-3 py-1.5 hover:bg-muted/40">
            <button type="button" className="flex min-w-0 flex-1 items-center gap-2 text-left" onClick={() => item.type === 'directory' ? void enter(item.path) : item.type === 'file' ? void select(item.path) : undefined}>
              {item.type === 'directory' ? <Folder className="size-4 shrink-0" /> : <File className="size-4 shrink-0" />}
              <span className="min-w-0 flex-1 truncate text-sm">{item.name}</span>
              {item.mount && (item.path === item.mount.destination || item.path.startsWith(`${item.mount.destination}/`)) && <span className="hidden shrink-0 text-xs text-muted-foreground sm:inline">{item.mount.type}</span>}
              {item.type === 'file' && <span className="shrink-0 text-xs text-muted-foreground">{item.size < 1024 ? `${item.size} B` : `${Math.round(item.size / 1024)} KB`}</span>}
            </button>
            {(item.type === 'file' || item.type === 'directory') && <DropdownMenu><DropdownMenuTrigger render={<Button size="icon-sm" variant="ghost" aria-label={tx(`${item.name} 操作`, `${item.name} actions`)}><MoreHorizontal /></Button>} /><DropdownMenuContent align="end">
              <DropdownMenuItem onClick={() => void action('rename', item)}><Pencil />{tx('重命名', 'Rename')}</DropdownMenuItem>
              <DropdownMenuItem onClick={() => void action('copy', item)}><Copy />{tx('复制', 'Copy')}</DropdownMenuItem>
              <DropdownMenuItem onClick={() => void action('move', item)}><MoveRight />{tx('移动', 'Move')}</DropdownMenuItem>
              <DropdownMenuItem variant="destructive" onClick={() => void action('delete', item)}><Trash2 />{tx('删除', 'Delete')}</DropdownMenuItem>
            </DropdownMenuContent></DropdownMenu>}
          </div>)}
          {listing.data?.entries.length === 0 && <p className="p-4 text-center text-sm text-muted-foreground">{tx('此目录为空。', 'This folder is empty.')}</p>}
        </div>
        {(listing.data?.next_cursor || cursorStack.length > 0) && <div className="flex justify-between border-t p-2"><Button size="sm" variant="ghost" disabled={!cursorStack.length} onClick={() => { setCursor(cursorStack.at(-1) ?? ''); setCursorStack((stack) => stack.slice(0, -1)) }}>{tx('上一页', 'Previous')}</Button><Button size="sm" variant="ghost" disabled={!listing.data?.next_cursor} onClick={() => { setCursorStack((stack) => [...stack, cursor]); setCursor(listing.data?.next_cursor ?? '') }}>{tx('下一页', 'Next')}</Button></div>}
      </section>
      {selected && <section className="min-w-0 overflow-hidden rounded-lg border">
        <div className="flex flex-wrap items-center justify-between gap-2 border-b p-2"><div className="flex min-w-0 items-center gap-2"><Button size="icon-sm" variant="ghost" aria-label={tx('返回文件列表', 'Back to files')} className="lg:hidden" onClick={() => void enter(folder)}><ArrowLeft /></Button><span className="truncate font-mono text-sm" title={selected}>{selected}</span></div><div className="flex items-center gap-1"><Button size="sm" variant="ghost" onClick={() => { setHistoryOpen((open) => !open); setRevisionID(null) }}><History />{tx('历史', 'History')}</Button><Button size="sm" disabled={!canSave || busy} onClick={() => void save()}>{busy ? <Spinner /> : <Save />}{tx('保存', 'Save')}</Button></div></div>
        {content.isPending && <p className="p-4 text-sm text-muted-foreground">{tx('正在读取文件…', 'Loading file…')}</p>}
        {content.isError && <Alert variant="destructive" className="m-2 w-auto"><AlertDescription>{content.error.message}</AlertDescription></Alert>}
        {content.data && <>
          {content.data.warning && <Alert variant="destructive" className="m-2 w-auto"><AlertTriangle /><AlertDescription>{tx('上次保存中断，当前内容与已验证版本不一致。请检查文件或从历史恢复。', content.data.warning)}</AlertDescription></Alert>}
          <div className="flex flex-wrap gap-2 border-b px-3 py-2 text-xs text-muted-foreground"><span>{content.data.persistent ? tx('持久化文件', 'Persistent file') : tx('容器临时层 · 只读预览', 'Container layer · preview only')}</span>{content.data.read_only && <span>{tx('不可编辑', 'Read only')}</span>}{content.data.single_file_bind && <span>{tx('单文件挂载 · 保存时短暂非原子', 'Single-file mount · brief non-atomic save')}</span>}{dirty && <span>{tx('未保存', 'Unsaved')}</span>}</div>
          {content.data.single_file_bind && <Alert className="m-2 w-auto"><AlertTriangle /><AlertDescription>{tx('此文件是单文件 bind mount。保存会短暂原位写入；请避免应用同时读取或修改它。', 'This file is a single-file bind mount. Saving writes in place briefly; avoid concurrent reads or writes by the application.')}</AlertDescription></Alert>}
          {sensitive && !revealed ? <div className="grid h-[50vh] min-h-80 place-content-center gap-3 px-4 text-center"><p className="text-sm text-muted-foreground">{tx('检测到可能包含密码或密钥的内容，默认隐藏。', 'Potential passwords or keys were detected. Content is hidden by default.')}</p><Button variant="outline" onClick={() => setRevealed(true)}>{tx('显示敏感内容', 'Reveal sensitive content')}</Button></div> : showConflictDiff && conflictContent ? <div className="h-[50vh] min-h-80"><Suspense fallback={<Spinner />}><MonacoDiff original={conflictContent.content} modified={draft ?? content.data.content} language={languageFor(selected)} theme={dark ? 'vs-dark' : 'light'} /></Suspense></div> : revisionID !== null && revision.data ? <div className="h-[50vh] min-h-80"><Suspense fallback={<Spinner />}><MonacoDiff original={revision.data.content} modified={draft ?? content.data.content} language={languageFor(selected)} theme={dark ? 'vs-dark' : 'light'} /></Suspense></div> : <div className="h-[50vh] min-h-80"><Suspense fallback={<Spinner />}><Monaco key={selected} value={draft ?? content.data.content} onChange={(value) => setDraft(value ?? '')} language={languageFor(selected)} theme={dark ? 'vs-dark' : 'light'} options={{ readOnly: content.data.read_only, automaticLayout: true, minimap: { enabled: false }, wordWrap: 'on', fontFamily: '"JetBrains Mono", monospace', scrollBeyondLastLine: false, wordBasedSuggestions: 'allDocuments' }} /></Suspense></div>}
          {historyOpen && <div className="max-h-44 overflow-auto border-t p-2"><p className="mb-1 text-xs text-muted-foreground">{tx('最近版本', 'Recent versions')}</p>{history.data?.map((item) => <div key={item.id} className="flex items-center justify-between gap-2 py-1 text-xs"><button className="truncate text-left hover:underline" onClick={() => setRevisionID(item.id)}>{new Date(item.created_at).toLocaleString()} {item.baseline ? tx('原始版本', 'Original') : `#${item.id}`}</button><Button size="sm" variant="ghost" disabled={busy || content.data?.read_only} onClick={() => { setRevisionID(item.id); void restoreRevision(item.id) }}><RotateCcw />{tx('恢复', 'Restore')}</Button></div>)}{history.data?.length === 0 && <p className="text-sm text-muted-foreground">{tx('还没有保存历史。', 'No saved versions yet.')}</p>}</div>}
          {(revisionID !== null || showConflictDiff) && <div className="border-t p-2"><Button size="sm" variant="outline" onClick={() => { setRevisionID(null); setShowConflictDiff(false) }}>{tx('返回编辑', 'Back to editor')}</Button></div>}
        </>}
      </section>}
      {!selected && <div className="hidden min-h-64 place-items-center rounded-lg border text-sm text-muted-foreground lg:grid">{tx('选择持久化文本文件开始编辑', 'Select a persistent text file to edit')}</div>}
    </div>
    {taskID && <p className="text-sm text-muted-foreground">{tx('操作进行中：', 'Operation in progress: ')}{task.data?.message ?? tx('等待任务', 'Waiting for task')} {task.data?.progress ?? 0}%</p>}
    {notice && <Alert variant={notice.includes('失败') || notice.includes('修改') || notice.includes('failed') || notice.includes('changed') ? 'destructive' : 'default'}><AlertDescription>{notice} {conflictContent && <Button size="sm" variant="ghost" onClick={() => setShowConflictDiff(true)}>{tx('查看差异', 'View diff')}</Button>} {conflictContent && <Button size="sm" variant="ghost" onClick={() => { client.setQueryData(['container-file-content', nodeID, container.id, selected], conflictContent); setDraft(conflictContent.content); setConflictContent(null); setShowConflictDiff(false); setNotice(tx('已加载最新内容。', 'Latest content loaded.')) }}>{tx('重新加载', 'Reload')}</Button>}</AlertDescription></Alert>}
  </div>

  async function restoreRevision(id: number) {
    if (!selected || !content.data) return
    if (!await confirmDialog({ title: tx('恢复此版本？', 'Restore this version?'), description: dirty ? tx('未保存更改将丢失；服务器上的当前内容会保留在编辑历史中。', 'Unsaved changes will be lost; the current saved content remains in history.') : tx('当前内容会保留在编辑历史中。', 'The current content will remain in editor history.'), confirmLabel: tx('恢复', 'Restore') })) return
    setBusy(true)
    try {
      const updated = await api<Content>(`${base}/restore`, { method: 'POST', body: JSON.stringify({ path: selected, revision_id: id, etag: content.data.etag }) })
      client.setQueryData(['container-file-content', nodeID, container.id, selected], updated)
      setDraft(updated.content); setRevisionID(null); setNotice(tx('版本已恢复。', 'Version restored.'))
      void client.invalidateQueries({ queryKey: ['container-file-history', nodeID, container.id, selected] })
    } catch (error) { setNotice((error as Error).message) }
    finally { setBusy(false) }
  }
}
