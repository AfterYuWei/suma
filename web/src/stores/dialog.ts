import { create } from 'zustand'

export interface DialogRequest {
  title: string
  description?: string
  confirmLabel?: string
  cancelLabel?: string
  danger?: boolean
  input?: { label: string; initialValue?: string; placeholder?: string; requiredValue?: string }
}

interface DialogState { request: DialogRequest | null; open: (request: DialogRequest) => void; close: () => void }
let resolver: ((value: string | null) => void) | null = null

export const useDialogStore = create<DialogState>((set) => ({
  request: null,
  open: (request) => set({ request }),
  close: () => set({ request: null }),
}))

function requestDialog(request: DialogRequest) {
  resolver?.(null)
  useDialogStore.getState().open(request)
  return new Promise<string | null>((resolve) => { resolver = resolve })
}

export async function confirmDialog(request: DialogRequest) { return (await requestDialog(request)) !== null }
export function promptDialog(request: DialogRequest) { return requestDialog(request) }
export function finishDialog(value: string | null) {
  const resolve = resolver
  resolver = null
  useDialogStore.getState().close()
  resolve?.(value)
}
