import { useQuery } from '@tanstack/react-query'
import { lazy, Suspense } from 'react'
import type { ContainerDetail, ContainerSummary } from '../../features/containers/types'
import type { useContainerWorkspace } from '../../features/containers/use-container-workspace'
import { api } from '../../lib/api'
import { nodePath } from '../../lib/nodes'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '../ui/dialog'
import { Tabs, TabsList, TabsTrigger } from '../ui/tabs'
import { LoadingState } from '../ui/loading-state'
import { ErrorState } from '../ui/error-state'

const LogViewer = lazy(() => import('../../features/containers/log-viewer').then(module => ({ default: module.LogViewer })))
const FileManager = lazy(() => import('../../features/containers/file-manager').then(module => ({ default: module.ContainerFileManager })))
const Terminal = lazy(() => import('../../features/containers/terminal-view').then(module => ({ default: module.TerminalView })))

export function ContainerWorkspaceDialog({ workspace, rows, available, zh }: {
  workspace: ReturnType<typeof useContainerWorkspace>; rows: ContainerSummary[]; available: boolean; zh: boolean
}) {
  const active = workspace.active
  const current = active && rows.find(row => row.id === active.container.id)
  const gone = !!active && available && !current
  const row = current ?? active?.container
  const running = available && !gone && row?.state === 'running'
  return <Dialog open={!!active} onOpenChange={open => { if (!open) void workspace.close() }}>
    <DialogContent className="flex max-h-[90dvh] flex-col overflow-y-auto sm:max-w-[1100px]" finalFocus={() => workspace.opener.current}>
      {active && row && <>
        <DialogHeader className="pr-8"><DialogTitle className="break-all">{row.name}</DialogTitle><DialogDescription className="break-all">{row.image} · {row.state}</DialogDescription></DialogHeader>
        <Tabs value={active.tab} onValueChange={tab => void workspace.switchTab(tab as 'logs' | 'files' | 'terminal')}>
          <TabsList><TabsTrigger value="logs">{zh ? '日志' : 'Logs'}</TabsTrigger><TabsTrigger value="files" disabled={!running && active.tab !== 'files'}>{zh ? '文件' : 'Files'}</TabsTrigger><TabsTrigger value="terminal" disabled={!running}>{zh ? '终端' : 'Terminal'}</TabsTrigger></TabsList>
        </Tabs>
        {gone && <p role="alert" className="text-destructive">{zh ? '容器已删除或重建。未保存文件草稿仍保留，请复制后关闭。' : 'Container removed or recreated. Copy any unsaved file draft before closing.'}</p>}
        {!running && !gone && active.tab !== 'logs' && <p role="alert" className="text-muted-foreground">{!available ? zh ? '运行信息暂不可用，文件操作和终端已暂停。' : 'Runtime information unavailable; file operations and terminal are suspended.' : zh ? '容器未运行，文件操作和终端暂不可用。' : 'Container is not running. File operations and terminal are unavailable.'}</p>}
        <div className="min-h-0 min-w-0" key={`${active.nodeID}/${row.id}/${active.tab}`}>
          <Suspense fallback={<LoadingState compact label={zh ? '正在加载' : 'Loading'} />}>
            {active.tab === 'logs' && !gone && <LogViewer nodeID={active.nodeID} containerId={row.id} />}
            {active.tab === 'terminal' && running && <Terminal nodeID={active.nodeID} containerId={row.id} />}
            {active.tab === 'files' && <ContainerFiles nodeID={active.nodeID} row={row} running={running} gone={gone} onDirtyChange={workspace.setDirty} zh={zh} />}
          </Suspense>
        </div>
      </>}
    </DialogContent>
  </Dialog>
}

// Mount the query with the Files tab so switching tabs removes its observer and
// cancels an in-flight Inspect as well as the file manager's requests.
function ContainerFiles({ nodeID, row, running, gone, onDirtyChange, zh }: {
  nodeID: string; row: ContainerSummary; running: boolean; gone: boolean; onDirtyChange: (dirty: boolean) => void; zh: boolean
}) {
  const detail = useQuery({
    queryKey: ['container', nodeID, row.id],
    queryFn: ({ signal }) => api<ContainerDetail>(nodePath(nodeID, `/containers/${encodeURIComponent(row.id)}`), { signal }),
    enabled: running && !gone,
    refetchInterval: 10_000,
  })
  if (detail.data) return <FileManager nodeID={nodeID} container={{ ...detail.data, state: running ? 'running' : 'exited' }} onDirtyChange={onDirtyChange} />
  if (detail.isError) return <ErrorState description={detail.error.message} />
  return !gone && running ? <LoadingState compact label={zh ? '正在加载' : 'Loading'} /> : null
}
