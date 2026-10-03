import { useQuery } from '@tanstack/react-query'
import { api } from '../../lib/api'
import { nodePath } from '../../lib/nodes'
import type { ImageUpdateView } from './types'
export function useImageUpdates(nodeID: string, projectName?: string, enabled = true) {
 return useQuery({ queryKey: ['image-updates', nodeID, projectName || ''], queryFn: () => api<ImageUpdateView>(nodePath(nodeID, `/image-updates${projectName ? `?project_name=${encodeURIComponent(projectName)}` : ''}`)), enabled: !!nodeID && enabled, refetchInterval: query => query.state.data?.running_task_id ? 1000 : false })
}
