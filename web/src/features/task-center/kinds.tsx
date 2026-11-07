import { RefreshCw, type LucideIcon } from 'lucide-react'
import type { ReactNode } from 'react'
import type { ImageUpdateSummary, TaskRecord } from './types'
import { ImageUpdateResultView } from './image-update-result'

// TaskKindView is the client-side registry: every server kind contributes how
// its records render. Future task types plug in here without touching the
// Task Center pages.
export interface TaskKindView {
  prefix: string
  label: (zh: boolean) => string
  icon: LucideIcon
  resultView?: (task: TaskRecord) => ReactNode
}

const imageUpdateResult = (task: TaskRecord): ReactNode => (
  <ImageUpdateResultView summary={task.result as ImageUpdateSummary | undefined ?? null} />
)

export const taskKinds: TaskKindView[] = [
  { prefix: 'image.update', label: (zh) => zh ? '镜像更新' : 'Image update', icon: RefreshCw, resultView: imageUpdateResult },
]

export const taskKindFor = (type: string) => taskKinds.find((kind) => type.startsWith(kind.prefix))
