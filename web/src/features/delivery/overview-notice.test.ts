import { describe, expect, it } from 'vitest'
import { deliveryOverviewNotice } from './overview-notice'
import type { CDDrift, DeliveryRelease } from './types'

const desiredCommit = 'a'.repeat(40)
const failure = 'Error response from daemon: invalid mount config for type "bind": bind source path does not exist: /Data/Docker/DockerData/eizhu/application.yml'
const release = (values: Partial<DeliveryRelease> = {}): DeliveryRelease => ({
  id: 2, project_id: 1, repository_url: 'https://git.example.test/app', git_ref: 'main',
  commit_sha: desiredCommit, commit_message: 'Deploy app', commit_author: 'operator', config_hash: 'config',
  image_references: '[]', compose_files: '["compose.yml"]', status: 'failed', trigger_type: 'manual', trigger_actor: 'operator',
  created_at: '2026-10-26T06:00:00Z', updated_at: '2026-10-26T06:00:00Z',
  remediation: { retry_failed_node_ids: [], rollback_failed_node_ids: [] }, ...values,
})
const drift = (values: Partial<CDDrift> = {}): CDDrift => ({
  drifted: true, status: 'degraded', desired_commit: desiredCommit, observed_commit: desiredCommit, active_commit: '',
  runtime_healthy: false, reason: 'no release is active', reason_code: 'no_active_release', checked_at: '2026-10-26T06:00:00Z', nodes: [], ...values,
})

describe('delivery overview explains the current failure', () => {
  for (const zh of [true, false]) {
    it(`shows the Release failure instead of a missing-active-release symptom (${zh})`, () => {
      const notice = deliveryOverviewNotice({ drift: drift(), latest: release({ failure_reason: failure }), desiredCommit, zh })
      expect(notice.tone).toBe('warning')
      expect(notice.description).toBe(failure)
      expect(notice.title).toBe(zh ? '交付失败' : 'Delivery failed')
    })
  }

  it('prefers node errors to an aggregate release failure and keeps every failed node', () => {
    const latest = release({ status: 'partial_failed', failure_reason: 'one or more nodes failed', deployments: [
      { id: 1, node_id: 'local', node_name: 'Aliyun', status: 'failed', failure_reason: failure },
      { id: 2, node_id: 'edge', node_name: 'Edge', status: 'failed', failure_reason: 'pull access denied' },
      { id: 3, node_id: 'ok', node_name: 'Healthy', status: 'succeeded', failure_reason: 'obsolete failure' },
    ] })
    const notice = deliveryOverviewNotice({ drift: drift(), latest, desiredCommit, zh: true })
    expect(notice.title).toBe('部分节点交付失败')
    expect(notice.description).toBe(`Aliyun: ${failure}\nEdge: pull access denied`)
    expect(notice.description).not.toContain('obsolete')
  })

  it('uses the latest failed attempt rather than an older deployment error', () => {
    const latest = release({ deployments: [{ id: 1, node_id: 'local', node_name: 'Aliyun', status: 'failed', failure_reason: 'old failure', attempts: [
      { id: 1, deployment_id: 1, operation: 'deploy', target_release_id: 2, status: 'failed', failure_reason: 'old failure', created_at: '' },
      { id: 2, deployment_id: 1, operation: 'retry', target_release_id: 2, status: 'failed', failure_reason: failure, created_at: '' },
    ] }] })
    expect(deliveryOverviewNotice({ drift: drift(), latest, desiredCommit, zh: false }).description).toBe(`Aliyun: ${failure}`)
  })

  it('does not revive failures after the latest Release succeeds', () => {
    const notice = deliveryOverviewNotice({ drift: drift({ status: 'healthy', active_release_id: 3, active_commit: desiredCommit }), latest: release({ id: 3, status: 'succeeded', failure_reason: 'obsolete failure' }), desiredCommit, zh: true })
    expect(notice.tone).toBe('success')
    expect(notice.description).not.toContain('obsolete')
  })

  it('does not show a stale cached failure after a newer release is active', () => {
    const notice = deliveryOverviewNotice({ drift: drift({ status: 'healthy', active_release_id: 3 }), latest: release({ failure_reason: failure }), desiredCommit, zh: false })
    expect(notice.tone).toBe('success')
  })

  it('does not attribute a new desired commit to a failed historical Release', () => {
    const notice = deliveryOverviewNotice({ drift: drift(), latest: release({ commit_sha: 'b'.repeat(40), failure_reason: failure }), desiredCommit, zh: true })
    expect(notice.description).toBe('当前没有活动 Release。')
    expect(notice.description).not.toContain(failure)
  })

  it('shows known failure evidence while drift is still loading', () => {
    const notice = deliveryOverviewNotice({ latest: release({ failure_reason: failure }), desiredCommit, zh: true })
    expect(notice.tone).toBe('warning')
    expect(notice.description).toBe(failure)
  })

  it('keeps the failure visible after automatic rollback', () => {
    const notice = deliveryOverviewNotice({ drift: drift(), latest: release({ status: 'rolled_back', failure_reason: failure }), desiredCommit, zh: true })
    expect(notice.title).toBe('交付失败，已回滚')
    expect(notice.description).toBe(failure)
  })

  it('shows actual probe errors with node names', () => {
    const runtime = drift({ status: 'unknown', reason: 'no release is active', nodes: [
      { node_id: 'local', node_name: 'Aliyun', status: 'unknown', drifted: false, reason: 'Agent is disconnected', checked_at: '' },
    ] })
    expect(deliveryOverviewNotice({ drift: runtime, desiredCommit, zh: true }).description).toBe('Aliyun: Agent is disconnected')
  })

  it('reports API failure even when the cache still contains a healthy status', () => {
    const notice = deliveryOverviewNotice({ drift: drift({ status: 'healthy' }), desiredCommit, error: 'HTTP 503: engine unavailable', zh: true })
    expect(notice.tone).toBe('warning')
    expect(notice.description).toBe('HTTP 503: engine unavailable')
  })

  it('does not invent an error when no evidence was returned', () => {
    const notice = deliveryOverviewNotice({ drift: drift({ reason: undefined }), latest: release(), desiredCommit, zh: true })
    expect(notice.description).toContain('未返回具体失败原因')
    expect(notice.description).toContain('任务日志')
  })

  it('retains the initial checking state', () => {
    expect(deliveryOverviewNotice({ desiredCommit, zh: true }).tone).toBe('checking')
  })
})
