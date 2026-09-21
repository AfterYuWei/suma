import { useUIStore } from '../stores/ui'

export interface DockerNode {
  id: string
  name: string
  connection_type: 'unix' | 'tcp'
  endpoint: string
  tls_mode: 'required' | 'disabled'
  tls_credential_id?: number
  enabled: boolean
  engine_id?: string
  engine_version?: string
  status: 'unknown' | 'online' | 'offline'
  last_error?: string
  last_latency_ms?: number
  last_checked_at?: string
  created_at: string
  updated_at: string
	group_ids: number[]
}

export interface NodeGroup {
  id: number
  name: string
  description: string
  is_default: boolean
  node_count: number
  created_at: string
  updated_at: string
}

export type NodeGroupFilter = 'default' | 'all' | `group:${number}`

export const groupFilterID = (filter: NodeGroupFilter): number | null => {
  if (!filter.startsWith('group:')) return null
  const id = Number(filter.slice(6))
  return Number.isSafeInteger(id) && id > 0 ? id : null
}

export const filterNodesByGroup = (nodes: DockerNode[], filter: NodeGroupFilter): DockerNode[] => {
  if (filter === 'all' || filter === 'default') return nodes
  const id = groupFilterID(filter)
  return id == null ? nodes : nodes.filter((node) => node.group_ids.includes(id))
}

export const nodesForFilteredSelection = (nodes: DockerNode[], filter: NodeGroupFilter, selectedIDs: string[]) => {
  const filtered = filterNodesByGroup(nodes, filter)
  const filteredIDs = new Set(filtered.map((node) => node.id))
  const selected = new Set(selectedIDs)
  return {
    filtered,
    outsideSelected: nodes.filter((node) => selected.has(node.id) && !filteredIDs.has(node.id)),
  }
}

export const fleetGroupQuery = (filter: NodeGroupFilter): string => {
  const id = groupFilterID(filter)
  return id == null ? '' : `?group_id=${id}`
}

export const nodePath = (nodeID: string, path: string) => `/nodes/${encodeURIComponent(nodeID)}${path}`
export const currentNodePath = (path: string) => nodePath(useUIStore.getState().currentNodeID, path)
