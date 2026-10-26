import { useMutation } from '@tanstack/react-query'
import { Check, PlugZap } from 'lucide-react'
import { useEffect, useRef } from 'react'
import { Alert, AlertDescription, AlertTitle } from '../../components/ui/alert'
import { Button } from '../../components/ui/button'
import { Spinner } from '../../components/ui/spinner'
import { api } from '../../lib/api'
import type { GitConnectionInput, GitConnectionResult, GitRepositoryConfiguration } from './types'

export function GitConnectionTest({ repository, nodeIDs, projectName, creating, zh }: {
  repository: GitRepositoryConfiguration
  nodeIDs: string[]
  projectName: string
  creating: boolean
  zh: boolean
}) {
  const controller = useRef<AbortController | null>(null)
  const scope = creating ? '' : projectName
  useEffect(() => () => controller.current?.abort(), [scope])
  const { summary: _summary, save_to_center: _save, ...authentication } = repository.authentication
  const input: GitConnectionInput = { repository: { clone_url: repository.clone_url, ref_type: repository.ref_type, ref: repository.ref, authentication }, node_ids: nodeIDs }
  const signature = JSON.stringify({ scope, input })
  const test = useMutation({
    mutationFn: ({ input }: { input: GitConnectionInput; signature: string }) => {
      controller.current?.abort()
      controller.current = new AbortController()
      return api<GitConnectionResult>(creating ? '/delivery-projects/test-connection' : `/delivery-projects/${encodeURIComponent(projectName)}/test-connection`, { method: 'POST', body: JSON.stringify(input), signal: controller.current.signal })
    },
  })
  const current = test.variables?.signature === signature
  const missingCredential = authentication.source === 'center' && !authentication.credential_id
  return <div className="flex min-w-0 flex-col gap-3">
    <div className="flex flex-wrap items-center gap-2">
      <Button type="button" variant="outline" size="sm" aria-busy={test.isPending} disabled={test.isPending || !input.repository.clone_url || !input.repository.ref || missingCredential} onClick={() => test.mutate({ input, signature })}>
        {test.isPending ? <Spinner aria-hidden="true" /> : <PlugZap />}{test.isPending ? (zh ? '测试中…' : 'Testing…') : (zh ? '测试连接' : 'Test connection')}
      </Button>
      <span className="text-xs text-muted-foreground">{zh ? '使用当前填写的配置，仅读取远端引用，不保存或发布。' : 'Uses the current draft to read remote refs. Does not save or deploy.'}</span>
    </div>
    {current && test.isSuccess && <Alert role="status" className="min-w-0">
      <Check /><AlertTitle>{zh ? '连接成功' : 'Connection successful'}</AlertTitle>
      <AlertDescription className="break-words [overflow-wrap:anywhere]">{test.data.ref_checked
        ? `${input.repository.ref_type}:${input.repository.ref} · ${test.data.commit_sha}`
        : (zh ? '仓库可访问。指定 Commit 将在同步时验证；本次测试未检查 Compose 文件。' : 'Repository is accessible. The specified commit will be verified during sync; Compose files were not checked.')}
        {test.data.ref_checked && <span className="mt-1 block">{zh ? '已验证仓库访问及引用；Compose 文件将在同步时检查。' : 'Repository access and ref verified; Compose files are checked during sync.'}</span>}
      </AlertDescription>
    </Alert>}
    {current && test.isError && <Alert variant="destructive" className="min-w-0">
      <AlertTitle>{zh ? '连接失败' : 'Connection failed'}</AlertTitle>
      <AlertDescription className="whitespace-pre-wrap break-words [overflow-wrap:anywhere]">{test.error.message}
        <span className="mt-1 block">{zh ? '请检查仓库地址、引用、凭据及令牌权限。404 也可能表示私有仓库无访问权限；超时请检查控制端网络。' : 'Check the URL, ref, credential and token permissions. A 404 can also mean a private repository is inaccessible. For timeouts, check control-plane connectivity.'}</span>
      </AlertDescription>
    </Alert>}
  </div>
}
