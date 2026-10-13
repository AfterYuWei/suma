import { useBlocker } from '@tanstack/react-router'
import { useCallback, useEffect, useRef, useState } from 'react'
import type { ContainerSummary } from './types'
import { registerProjectNavigationGuard } from '../../lib/project-navigation-guard'
import { confirmDialog } from '../../stores/dialog'
type WorkspaceTab = 'logs' | 'files' | 'terminal'

export function useContainerWorkspace(nodeID: string, zh: boolean, scopeKey = '') {
  const [active, setActive] = useState<{ nodeID: string; container: ContainerSummary; tab: WorkspaceTab } | null>(null)
  const [dirty, setDirty] = useState(false)
  const opener = useRef<HTMLElement | null>(null)
  const dirtyRef = useRef(dirty)
  dirtyRef.current = dirty
  const allowDiscard = useCallback(async () => !dirtyRef.current || await confirmDialog({ title: zh ? '放弃未保存的文件更改？' : 'Discard unsaved file changes?', confirmLabel: zh ? '放弃' : 'Discard', danger: true }), [zh])
  useBlocker({ disabled: !dirty, enableBeforeUnload: dirty, shouldBlockFn: async () => !await allowDiscard() })
  useEffect(() => registerProjectNavigationGuard(allowDiscard), [allowDiscard])
  useEffect(() => { setActive(null); setDirty(false) }, [nodeID, scopeKey])
  const open = async (container: ContainerSummary, tab: WorkspaceTab, source?: HTMLElement) => {
    if (!await allowDiscard()) return
    opener.current = source ?? document.activeElement as HTMLElement | null
    setDirty(false)
    setActive({ nodeID, container, tab })
  }
  const close = async () => { if (await allowDiscard()) { setActive(null); setDirty(false) } }
  const switchTab = async (tab: WorkspaceTab) => {
    if (!active || tab === active.tab || !await allowDiscard()) return
    setDirty(false)
    setActive({ ...active, tab })
  }
  return { active, open, close, switchTab, setDirty, opener }
}
