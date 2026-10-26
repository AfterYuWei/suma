import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useId } from 'react'
import { Switch } from '../../components/ui/switch'
import { api } from '../../lib/api'
import type { CDConfiguration, DeliveryProject } from './types'

export function DeliveryAutomationSwitch({ projectName, enabled, zh }: { projectName: string; enabled: boolean; zh: boolean }) {
  const client = useQueryClient()
  const descriptionID = useId()
  const update = useMutation({
    mutationFn: (value: boolean) => api<DeliveryProject>(`/delivery-projects/${encodeURIComponent(projectName)}/automation`, { method: 'PUT', body: JSON.stringify({ enabled: value }) }),
    onSuccess: (project) => {
      client.setQueryData(['delivery-project', projectName], project)
      client.setQueryData<DeliveryProject[]>(['delivery-projects'], (rows) => rows?.map((row) => row.name === projectName ? project : row))
      client.setQueryData<CDConfiguration>(['delivery-configuration', projectName], (value) => value ? { ...value, automation_enabled: project.automation_enabled } : value)
      void client.invalidateQueries({ queryKey: ['delivery-projects'] })
      void client.invalidateQueries({ queryKey: ['cd-overview'] })
    },
  })
  return <div className="flex min-w-0 flex-col gap-1">
    <div className="flex w-fit items-center gap-2 text-xs text-muted-foreground">
      <Switch size="sm" checked={enabled} disabled={update.isPending} onCheckedChange={(value) => update.mutate(value)} aria-label={zh ? `${projectName} 自动交付触发` : `${projectName} automatic delivery triggers`} aria-describedby={descriptionID} />
      {enabled ? (zh ? '已开启' : 'On') : (zh ? '已暂停' : 'Paused')}
    </div>
    <span id={descriptionID} className="sr-only">{zh ? '控制轮询和 Webhook 触发，保留手动同步、发布和回滚。' : 'Controls polling and webhooks. Manual sync, deploy and rollback remain available.'}</span>
    {update.isError && <p role="alert" className="max-w-60 whitespace-normal text-xs text-destructive">{update.error.message}</p>}
  </div>
}
