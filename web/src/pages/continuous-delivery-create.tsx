import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useBlocker, useNavigate } from '@tanstack/react-router'
import { ChevronLeft } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { Button } from '../components/ui/button'
import { ErrorState } from '../components/ui/error-state'
import { LoadingState } from '../components/ui/loading-state'
import { CDSettings } from '../features/delivery/settings'
import { defaultRepository, type CDConfiguration, type CDConfigureInput, type CreatedDeliveryProject } from '../features/delivery/types'
import { api } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { filterNodesByGroup, type DockerNode } from '../lib/nodes'
import { confirmDialog } from '../stores/dialog'
import { useUIStore } from '../stores/ui'
import { ResourceFrame } from './images'

export function ContinuousDeliveryCreatePage() {
  const { language } = useI18n()
  const zh = language === 'zh-CN'
  const navigate = useNavigate()
  const client = useQueryClient()
  const nodes = useQuery({ queryKey: ['nodes'], queryFn: () => api<DockerNode[]>('/nodes') })
  const [name, setName] = useState('')
  const [configuration, setConfiguration] = useState<CDConfiguration | null>(null)
  const [dirty, setDirty] = useState(false)
  const [created, setCreated] = useState(false)
  const createdName = useRef('')
  const bypass = useRef(false)

  useEffect(() => {
    if (!nodes.data || configuration) return
    const { currentNodeID, currentGroupFilter } = useUIStore.getState()
    const enabled = filterNodesByGroup(nodes.data, currentGroupFilter).filter(node => node.enabled)
    const nodeID = enabled.find(node => node.id === currentNodeID)?.id ?? enabled[0]?.id
    setConfiguration({
      configured: false, repository: defaultRepository(), reconcile_mode: 'manual',
      sync_interval_seconds: 300, deployment_timeout: 120, auto_rollback: false,
      webhook_enabled: false, desired_commit: '', observed_commit: '',
      node_ids: nodeID ? [nodeID] : [], registry_credential_ids: []
    })
  }, [nodes.data, configuration])

  useBlocker({
    disabled: !dirty || created,
    enableBeforeUnload: dirty && !created,
    shouldBlockFn: async () => bypass.current ? false : !(await confirmDialog({
      title: zh ? '放弃未保存的交付配置？' : 'Discard unsaved delivery configuration?',
      danger: true
    }))
  })

  const openProject = () => {
    bypass.current = true
    void navigate({ to: '/continuous-delivery/$projectName', params: { projectName: createdName.current } })
  }
  const submit = async (input: CDConfigureInput) => {
    const { configuration: saved, ...project } = await api<CreatedDeliveryProject>('/delivery-projects', {
      method: 'POST', body: JSON.stringify({ name: name.trim(), configuration: input })
    })
    createdName.current = project.name
    setCreated(true)
    setDirty(false)
    client.setQueryData(['delivery-project', project.name], project)
    client.setQueryData(['delivery-configuration', project.name], { ...saved, webhook_secret: undefined })
    void client.invalidateQueries({ queryKey: ['delivery-projects'] })
    void client.invalidateQueries({ queryKey: ['git-credentials'] })
    setConfiguration(saved)
    return saved
  }

  return <div className="flex w-full min-w-0 flex-col gap-4">
    <Button type="button" size="sm" variant="ghost" className="-ml-2 self-start text-muted-foreground" onClick={() => void navigate({ to: '/continuous-delivery' })}>
      <ChevronLeft />{zh ? '持续交付' : 'Continuous Delivery'}
    </Button>
    <ResourceFrame title={zh ? '新建交付项目' : 'New delivery project'} detail={zh ? '在此填写完整的 Git 来源、目标节点和交付策略。' : 'Configure the Git source, target nodes and delivery policy together.'}>
      {nodes.isError ? <ErrorState description={nodes.error.message} /> : !configuration ? <LoadingState compact label={zh ? '正在加载目标节点' : 'Loading target nodes'} /> : <CDSettings
        projectName={createdName.current || name}
        configuration={configuration}
        zh={zh}
        onDirtyChange={setDirty}
        onSaved={value => { if (!value.webhook_secret) openProject() }}
        creation={{ name, onNameChange: setName, submit, created, onDone: openProject, onCancel: () => void navigate({ to: '/continuous-delivery' }) }}
      />}
    </ResourceFrame>
  </div>
}
