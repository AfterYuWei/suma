export interface ComposeProject {
  node_id: string
  name: string
  path: string
  source: 'managed' | 'external'
  can_manage: boolean
  config_files: string[]
  status: string
  services: number
  containers: number
  compose: string
  environment: string
  created_at: string
  updated_at: string
}
