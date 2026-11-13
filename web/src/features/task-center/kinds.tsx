import { GitBranch, RefreshCw, Trash2, type LucideIcon } from 'lucide-react'
import type { ComponentType, ReactNode } from 'react'
import type { DockerNode } from '../../lib/nodes'
import { CleanupNodeSheet } from '../cleanup/storage-cleanup'
import type { ImageUpdateSummary, Schedule, TaskRecord } from './types'
import { ImageUpdateResultView } from './image-update-result'
import { ImageUpdateSettingsDialog } from './image-update-settings'
import { CleanupTaskResult, DeliveryTaskResult } from './business-results'
import { CDScheduleEditor } from './cd-schedule-editor'

export interface KindEditorProps {
  node?: DockerNode
  subject: string
  initial?: Schedule
  open: boolean
  onOpenChange: (open: boolean) => void
  businessHref?: string
  manual?: boolean
}

export interface TaskKindView {
  prefix: string
  label: (zh: boolean) => string
  icon: LucideIcon
  Editor: ComponentType<KindEditorProps>
  requiresSubject?: boolean
  createHref?: string
  businessHref?: (subject: string) => string
  manualAction: 'check' | 'run'
  manualViaEditor?: boolean
  resultView?: (task: TaskRecord) => ReactNode
}

const imageUpdateResult = (task: TaskRecord): ReactNode => <ImageUpdateResultView summary={task.result as ImageUpdateSummary | undefined ?? null} />

export const taskKinds: TaskKindView[] = [
  { prefix: 'image.update', label: zh => zh ? '镜像更新' : 'Image update', icon: RefreshCw, Editor: ({ node, subject, open, onOpenChange }) => node ? <ImageUpdateSettingsDialog node={node} project={{ name: subject }} open={open} onOpenChange={onOpenChange} /> : null, requiresSubject: true, businessHref: subject => `/projects/compose/${encodeURIComponent(subject)}`, manualAction: 'check', resultView: imageUpdateResult },
  { prefix: 'system.cleanup', label: zh => zh ? '存储清理' : 'Storage cleanup', icon: Trash2, Editor: ({ node, open, onOpenChange, manual }) => node ? <CleanupNodeSheet node={node} initialTab={manual ? 'preview' : 'policy'} open={open} onOpenChange={onOpenChange} /> : null, manualAction: 'run', manualViaEditor: true, resultView: task => <CleanupTaskResult task={task} /> },
  { prefix: 'cd.', label: zh => zh ? '全局 CD' : 'Global CD', icon: GitBranch, Editor: CDScheduleEditor, createHref: '/continuous-delivery/new/project', businessHref: subject => `/continuous-delivery/${encodeURIComponent(subject)}`, manualAction: 'check', resultView: task => <DeliveryTaskResult task={task} /> },
  { prefix: 'project.cd.', label: zh => zh ? '项目 CD' : 'Project CD', icon: GitBranch, Editor: CDScheduleEditor, createHref: '/projects', businessHref: subject => `/projects/compose/${encodeURIComponent(subject)}?view=cd`, manualAction: 'check', resultView: task => <DeliveryTaskResult task={task} /> },
]

export const taskKindFor = (type: string) => taskKinds.find(kind => type.startsWith(kind.prefix))
