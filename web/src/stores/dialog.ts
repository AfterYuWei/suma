import { create } from 'zustand'

export interface DialogRequest {
  title: string
  description?: string
  confirmLabel?: string
  cancelLabel?: string
  danger?: boolean
  input?: { label: string; initialValue?: string; placeholder?: string; requiredValue?: string }
  checkbox?: { label: string; description?: string; initialChecked?: boolean }
  choices?: { value: string; label: string; primary?: boolean }[]
}

export interface DialogSubmission { value: string; checked: boolean }

interface DialogState { request: DialogRequest | null; open: (request: DialogRequest) => void; close: () => void }
type DialogResult = string | DialogSubmission | null
let resolver: ((value: DialogResult) => void) | null = null

export const useDialogStore = create<DialogState>((set) => ({
  request: null,
  open: (request) => set({ request }),
  close: () => set({ request: null }),
}))

function requestDialog(request: DialogRequest) {
  resolver?.(null)
  useDialogStore.getState().open(request)
  return new Promise<DialogResult>((resolve) => { resolver = resolve })
}

export async function confirmDialog(request: DialogRequest) { return (await requestDialog(request)) !== null }
export async function promptDialog(request: DialogRequest) { return await requestDialog(request) as string | null }
export async function promptWithCheckboxDialog(request: DialogRequest) { return await requestDialog(request) as DialogSubmission | null }
export async function choiceDialog(request: DialogRequest) { return await requestDialog(request) as string | null }
export function finishDialog(value: DialogResult) {
  const resolve = resolver
  resolver = null
  useDialogStore.getState().close()
  resolve?.(value)
}
