import { useUIStore } from '../stores/ui'

export interface DockerNode {
  id: string
  name: string
  connection_type: 'unix' | 'tcp'
  endpoint: string
  tls_mode: 'required' | 'disabled'
  tls_credential_id?: number
  allowed_bind_roots: string[]
  enabled: boolean
  engine_id?: string
  engine_version?: string
  status: 'unknown' | 'online' | 'offline'
  last_error?: string
  last_latency_ms?: number
  last_checked_at?: string
  created_at: string
  updated_at: string
}

export const nodePath = (nodeID: string, path: string) => `/nodes/${encodeURIComponent(nodeID)}${path}`
export const currentNodePath = (path: string) => nodePath(useUIStore.getState().currentNodeID, path)
