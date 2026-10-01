import { ApiError } from './api'
import type { DockerNode } from './nodes'
import {
  configuration,
  expands,
  type Policy,
  type Preview,
  type Resource,
  type Run,
  type View,
} from '../features/cleanup/types'

export function createMockCleanup(nodes: DockerNode[]) {
  const policies = new Map<string, Policy>()
  const previews = new Map<string, Preview>()
  const runs = new Map<string, Run[]>()
  const removed = new Map<string, Set<string>>()
  const defaults = (id: string): Policy => ({
    node_id: id,
    version: 0,
    enabled: false,
    schedule: {
      frequency: 'weekly',
      weekday: 0,
      hour: 3,
      minute: 0,
      timezone: 'Asia/Shanghai',
    },
    images: { enabled: true, include_tagged: false, retention_days: 7 },
    cache: { enabled: true, retention_days: 7, reserved_bytes: 10 * 1024 ** 3 },
    containers: { enabled: false, retention_days: 7 },
    networks: { enabled: false, retention_days: 7 },
    scan_volumes: true,
    protected: {},
  })
  const nextRuns = (policy: Policy) => {
    const result: string[] = []
    const dates = new Set<string>()
    const now = Date.now()
    const format = new Intl.DateTimeFormat('en-US', {
      timeZone: policy.schedule.timezone,
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      weekday: 'short',
      hourCycle: 'h23',
    })
    const parts = (value: number) =>
      Object.fromEntries(
        format
          .formatToParts(new Date(value))
          .map((part) => [part.type, part.value]),
      )
    for (
      let time = Math.floor(now / 3600000) * 3600000;
      time < now + 23 * 86400000 && result.length < 3;
      time += 3600000
    ) {
      const initial = parts(time)
      const candidate =
        time +
        ((policy.schedule.minute - Number(initial.minute) + 60) % 60) * 60000
      const local = parts(candidate)
      const day = `${local.year}-${local.month}-${local.day}`
      if (
        candidate > now &&
        Number(local.hour) === policy.schedule.hour &&
        (policy.schedule.frequency === 'daily' ||
          ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'].indexOf(
            local.weekday,
          ) === policy.schedule.weekday) &&
        !dates.has(day)
      ) {
        result.push(new Date(candidate).toISOString())
        dates.add(day)
      }
    }
    return result
  }
  const view = (id: string): View => {
    const policy = policies.get(id) ?? defaults(id)
    return {
      policy,
      capabilities: { available: true, build_cache: true, api_version: '1.51' },
      next_runs: nextRuns(policy),
      latest_run: runs.get(id)?.[0] ?? null,
      active_run: null,
    }
  }
  return (
    pathname: string,
    method: string,
    body: Record<string, unknown>,
    url: URL,
  ): unknown | undefined => {
    if (pathname === '/cleanup/policies')
      return nodes.map((node) => ({
        node_id: node.id,
        node_name: node.name,
        view: view(node.id),
      }))
    const volumeDeletion = pathname.match(
      /^\/nodes\/([^/]+)\/volumes\/([^/]+)$/,
    )
    if (
      volumeDeletion &&
      method === 'DELETE' &&
      decodeURIComponent(volumeDeletion[2]) === 'demo-unused-volume'
    ) {
      const [, nodeID, encodedName] = volumeDeletion
      const name = decodeURIComponent(encodedName)
      if (url.searchParams.get('confirm') !== name)
        throw new ApiError('Confirm the volume name', 20601, 422)
      if (view(nodeID).policy.protected.volume?.includes(name))
        throw new ApiError('Volume is protected', 20601, 409)
      const deleted = removed.get(nodeID) ?? new Set<string>()
      deleted.add(name)
      removed.set(nodeID, deleted)
      return {}
    }
    const match = pathname.match(/^\/nodes\/([^/]+)\/cleanup(\/.*)$/)
    if (!match) return undefined
    const [, nodeID, suffix] = match
    const node = nodes.find((item) => item.id === nodeID)
    if (!node) throw new ApiError('Node not found', 20601, 404)
    if (suffix === '/policy' && method === 'GET') return view(nodeID)
    if (suffix === '/policy' && method === 'PUT') {
      const old = view(nodeID).policy
      const candidate = body as unknown as Policy
      if (candidate.version !== old.version)
        throw new ApiError('Policy version changed', 20601, 409)
      if (
        !['daily', 'weekly'].includes(candidate.schedule.frequency) ||
        candidate.images.retention_days < 1 ||
        candidate.cache.retention_days < 1 ||
        candidate.containers.retention_days < 1 ||
        candidate.networks.retention_days < 1
      )
        throw new ApiError('Invalid retention or schedule', 20601, 422)
      try {
        new Intl.DateTimeFormat('en', { timeZone: candidate.schedule.timezone })
      } catch {
        throw new ApiError('Invalid timezone', 20601, 422)
      }
      if (
        candidate.enabled &&
        (!old.enabled || !old.authorized_at || expands(old, candidate)) &&
        (body.confirmation_name !== node.name || body.authorize !== true)
      )
        throw new ApiError(
          'Confirm node and automatic deletion authorization',
          20601,
          422,
        )
      const policy: Policy = {
        ...configuration(candidate),
        node_id: nodeID,
        version: old.version + 1,
        authorized_at: body.authorize
          ? new Date().toISOString()
          : old.authorized_at,
        authorized_by: body.authorize ? 1 : old.authorized_by,
      }
      if (policy.enabled) policy.next_run_at = nextRuns(policy)[0]
      policies.set(nodeID, policy)
      for (const [key, preview] of previews)
        if (preview.node_id === nodeID) previews.delete(key)
      return policy
    }
    if (suffix === '/preview' && method === 'POST') {
      const policy = view(nodeID).policy
      const old = new Date(Date.now() - 90 * 86400000).toISOString()
      const resources: Resource[] = [
        {
          kind: 'image',
          id: 'sha256:demo-dangling',
          name: 'sha256:demo-dangling',
          created_at: old,
          size_bytes: 128 * 1024 ** 2,
          candidate: policy.images.enabled,
          manual: false,
          reason: policy.images.enabled ? 'eligible' : 'disabled',
        },
        {
          kind: 'image',
          id: 'sha256:demo-active',
          name: 'postgres:17-alpine',
          created_at: old,
          size_bytes: 256 * 1024 ** 2,
          candidate: false,
          manual: false,
          reason: 'in_use',
        },
        {
          kind: 'container',
          id: 'demo-old-worker',
          name: 'legacy-worker',
          created_at: old,
          size_bytes: 32 * 1024 ** 2,
          candidate: policy.containers.enabled,
          manual: false,
          reason: policy.containers.enabled ? 'eligible' : 'disabled',
        },
        {
          kind: 'container',
          id: 'demo-compose',
          name: 'gateway-prod',
          created_at: old,
          size_bytes: null,
          candidate: false,
          manual: false,
          reason: 'compose_project',
        },
        {
          kind: 'cache',
          id: 'demo-cache',
          name: 'demo-cache',
          created_at: old,
          last_used_at: old,
          size_bytes: 512 * 1024 ** 2,
          candidate: policy.cache.enabled,
          manual: false,
          reason: policy.cache.enabled ? 'eligible' : 'disabled',
        },
        {
          kind: 'network',
          id: 'demo-unused-network',
          name: 'legacy_default',
          created_at: old,
          size_bytes: null,
          candidate: policy.networks.enabled,
          manual: false,
          reason: policy.networks.enabled ? 'eligible' : 'disabled',
        },
        {
          kind: 'network',
          id: 'demo-bridge',
          name: 'bridge',
          created_at: old,
          size_bytes: null,
          candidate: false,
          manual: false,
          reason: 'system_resource',
        },
        {
          kind: 'volume',
          id: 'demo-unused-volume',
          name: 'demo-unused-volume',
          created_at: old,
          size_bytes: 64 * 1024 ** 2,
          candidate: false,
          manual: policy.scan_volumes,
          reason: policy.scan_volumes ? 'manual_confirmation' : 'disabled',
        },
      ].filter(
        (resource) => !removed.get(nodeID)?.has(resource.id),
      ) as Resource[]
      for (const resource of resources)
        if (
          policy.protected[resource.kind]?.includes(resource.id) ||
          policy.protected[resource.kind]?.includes(resource.name)
        ) {
          resource.candidate = false
          resource.manual = false
          resource.reason = 'protected_reference'
        }
      const generated = new Date()
      const preview: Preview = {
        id: crypto.randomUUID(),
        node_id: nodeID,
        policy_version: policy.version,
        generated_at: generated.toISOString(),
        expires_at: new Date(generated.getTime() + 300000).toISOString(),
        resources,
        capabilities: {
          available: true,
          build_cache: true,
          api_version: '1.51',
        },
        image_layers_bytes: 4 * 1024 ** 3,
        cache_approximate: true,
        policy,
      }
      previews.set(preview.id, preview)
      return preview
    }
    if (suffix === '/run' && method === 'POST') {
      const preview = previews.get(String(body.preview_id))
      if (
        !preview ||
        preview.node_id !== nodeID ||
        preview.policy_version !== view(nodeID).policy.version ||
        Date.parse(preview.expires_at) <= Date.now()
      )
        throw new ApiError('Preview expired or changed', 20601, 409)
      if (body.confirmation_name !== node.name)
        throw new ApiError('Confirm node name', 20601, 422)
      const outcomes = preview.resources
        .filter((resource) => resource.candidate || resource.manual)
        .map((resource) => ({
          kind: resource.kind,
          id: resource.id,
          name: resource.name,
          status: resource.manual ? 'scanned' : 'deleted',
          reason: resource.manual ? 'manual_confirmation' : undefined,
        }))
      const stats: Run['result']['stats'] = {}
      for (const outcome of outcomes) {
        const stat = (stats[outcome.kind] ??= {
          deleted: 0,
          skipped: 0,
          failed: 0,
          scanned: 0,
        })
        if (outcome.status === 'deleted') stat.deleted++
        else stat.scanned++
        if (outcome.kind === 'cache') stat.reclaimed_bytes = 512 * 1024 ** 2
      }
      const id = crypto.randomUUID()
      const taskID = `cleanup-${id}`
      const run: Run = {
        id,
        node_id: nodeID,
        node_name: node.name,
        policy_version: preview.policy_version,
        trigger: 'manual',
        task_id: taskID,
        status: 'success',
        message: 'Demo cleanup completed; volumes preserved',
        created_at: new Date().toISOString(),
        finished_at: new Date().toISOString(),
        policy: configuration(preview.policy),
        result: {
          outcomes,
          stats,
          image_layers_before: 4 * 1024 ** 3,
          image_layers_after: 4 * 1024 ** 3 - 128 * 1024 ** 2,
        },
      }
      runs.set(nodeID, [run, ...(runs.get(nodeID) ?? [])])
      previews.delete(preview.id)
      const deleted = removed.get(nodeID) ?? new Set<string>()
      outcomes
        .filter((outcome) => outcome.status === 'deleted')
        .forEach((outcome) => deleted.add(outcome.id))
      removed.set(nodeID, deleted)
      return {
        id: taskID,
        scope: 'node',
        node_id: nodeID,
        node_name: node.name,
        type: 'system.cleanup',
        name: 'Docker storage cleanup',
        status: 'success',
        progress: 100,
        created_at: run.created_at,
      }
    }
    if (suffix === '/runs') {
      const all = (runs.get(nodeID) ?? []).filter(
        (run) =>
          url.searchParams.get('failed') !== 'true' ||
          ['failed', 'partial_failed', 'interrupted'].includes(run.status),
      )
      const page = Math.max(1, Number(url.searchParams.get('page')) || 1)
      return {
        items: all.slice((page - 1) * 20, page * 20),
        total: all.length,
        page,
      }
    }
    if (suffix.startsWith('/runs/')) {
      const run = runs.get(nodeID)?.find((row) => row.id === suffix.slice(6))
      if (!run) throw new ApiError('Run not found', 20601, 404)
      return run
    }
    return undefined
  }
}
