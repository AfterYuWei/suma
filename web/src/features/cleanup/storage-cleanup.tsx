import { useDateTime } from '../../lib/time-zone'
import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Copy,
  Ellipsis,
  RefreshCw,
  Save,
  Search,
  ShieldCheck,
} from 'lucide-react'
import { Button } from '../../components/ui/button'
import { Checkbox } from '../../components/ui/checkbox'
import { ErrorState } from '../../components/ui/error-state'
import { Input } from '../../components/ui/input'
import { Label } from '../../components/ui/label'
import { ListShell } from '../../components/ui/list-shell'
import { ListPagination } from '../../components/ui/list-pagination'
import { useListPagination } from '../../components/ui/use-list-pagination'
import { LoadingState } from '../../components/ui/loading-state'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '../../components/ui/select'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '../../components/ui/sheet'
import { Spinner } from '../../components/ui/spinner'
import { StatusBadge } from '../../components/ui/status-badge'
import { Switch } from '../../components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '../../components/ui/table'
import { Tabs, TabsList, TabsTrigger } from '../../components/ui/tabs'
import { Textarea } from '../../components/ui/textarea'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '../../components/ui/dropdown-menu'
import { api } from '../../lib/api'
import { useI18n } from '../../lib/i18n'
import { nodePath } from '../../lib/nodes'
import {
  confirmDialog,
  promptDialog,
  promptWithCheckboxDialog,
} from '../../stores/dialog'
import {
  bytes,
  configuration,
  expands,
  kindLabel,
  kinds,
  reasonLabel,
  scopeLabel,
  statusLabel,
  type Config,
  type Kind,
  type Policy,
  type Preview,
  type Run,
  type RunPage,
  type Summary,
  type View,
} from './types'

const tone = (status: string) =>
  status === 'success' || status === 'deleted'
    ? 'success'
    : ['failed', 'partial_failed', 'interrupted'].includes(status)
      ? 'critical'
      : status === 'running'
        ? 'warning'
        : 'neutral'
const root = (nodeID: string) => nodePath(nodeID, '/cleanup')

export function StorageCleanup() {
  const { formatDateTime, timeZone } = useDateTime()
  const { language } = useI18n()
  const zh = language === 'zh-CN'
  const client = useQueryClient()
  const query = useQuery({
    queryKey: ['cleanup-policies'],
    queryFn: () => api<Summary[]>('/cleanup/policies'),
    refetchInterval: 15_000,
  })
  const [filter, setFilter] = useState('')
  const [failedOnly, setFailedOnly] = useState(false)
  const [node, setNode] = useState<{ id: string; name: string } | null>(null)
  const rows = (query.data ?? []).filter(
    (row) =>
      row.node_name.toLowerCase().includes(filter.toLowerCase()) &&
      (!failedOnly ||
        ['failed', 'partial_failed', 'interrupted'].includes(
          row.view.latest_run?.status ?? '',
        )),
  )
  const pagination = useListPagination(rows, `${filter}-${failedOnly}`)
  const pause = useMutation({
    mutationFn: (row: Summary) =>
      api<Policy>(`${root(row.node_id)}/policy`, {
        method: 'PUT',
        body: JSON.stringify({
          ...configuration(row.view.policy),
          enabled: false,
          version: row.view.policy.version,
        }),
      }),
    onSuccess: () =>
      client.invalidateQueries({ queryKey: ['cleanup-policies'] }),
  })
  return (
    <div className="flex flex-col gap-4">
      <div className="space-y-1">
        <h3 className="text-sm font-medium">
          {zh ? 'Docker 存储清理' : 'Docker storage cleanup'}
        </h3>
        <p className="text-sm text-muted-foreground">
          {zh
            ? '每节点独立策略。卷仅扫描，删除需逐卷确认；不处理日志或独立 Buildx Builder。'
            : 'Independent node policies. Volumes require individual confirmation; logs and independent Buildx builders are excluded.'}
        </p>
      </div>
      <div className="flex flex-wrap items-center gap-3">
        <div className="relative">
          <Search className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
          <Input
            className="pl-8"
            aria-label={zh ? '筛选节点' : 'Filter nodes'}
            placeholder={zh ? '筛选节点…' : 'Filter nodes…'}
            value={filter}
            onChange={(event) => setFilter(event.target.value)}
          />
        </div>
        <Label className="flex items-center gap-2">
          <Checkbox
            checked={failedOnly}
            onCheckedChange={(value) => setFailedOnly(value === true)}
          />
          {zh ? '最近执行失败' : 'Latest execution failed'}
        </Label>
      </div>
      {query.isPending ? (
        <LoadingState
          label={zh ? '正在加载清理数据' : 'Loading cleanup data'}
          rows={4}
        />
      ) : query.isError ? (
        <ErrorState description={query.error.message} />
      ) : (
        <>
          <ListShell>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{zh ? '节点' : 'Node'}</TableHead>
                  <TableHead>{zh ? '策略' : 'Policy'}</TableHead>
                  <TableHead>{zh ? '清理范围' : 'Categories'}</TableHead>
                  <TableHead>{zh ? '下次执行' : 'Next execution'}</TableHead>
                  <TableHead>{zh ? '最近结果' : 'Last result'}</TableHead>
                  <TableHead className="w-10" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {pagination.items.map((row) => (
                  <TableRow key={row.node_id}>
                    <TableCell>
                      <Button
                        variant="link"
                        className="h-auto p-0"
                        onClick={() =>
                          setNode({ id: row.node_id, name: row.node_name })
                        }
                      >
                        {row.node_name}
                      </Button>
                    </TableCell>
                    <TableCell>
                      <StatusBadge
                        tone={row.view.policy.enabled ? 'success' : 'neutral'}
                      >
                        {row.view.policy.enabled
                          ? zh
                            ? '已开启'
                            : 'Enabled'
                          : zh
                            ? '已暂停'
                            : 'Paused'}
                      </StatusBadge>
                    </TableCell>
                    <TableCell className="max-w-80 whitespace-normal text-xs">
                      {scopeLabel(row.view.policy, zh)}
                    </TableCell>
                    <TableCell>
                      <div className="text-xs">
                        {formatDateTime(row.view.policy.next_run_at)}
                      </div>
                      <div className="text-xs text-muted-foreground">
                        {timeZone}
                      </div>
                    </TableCell>
                    <TableCell>
                      {row.view.latest_run ? (
                        <StatusBadge tone={tone(row.view.latest_run.status)}>
                          {statusLabel(row.view.latest_run.status, zh)}
                        </StatusBadge>
                      ) : (
                        '—'
                      )}
                    </TableCell>
                    <TableCell>
                      <DropdownMenu>
                        <DropdownMenuTrigger
                          render={
                            <Button
                              variant="ghost"
                              size="icon-xs"
                              aria-label={
                                zh
                                  ? `${row.node_name} 操作`
                                  : `${row.node_name} actions`
                              }
                            />
                          }
                        >
                          <Ellipsis />
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          <DropdownMenuItem
                            onClick={() =>
                              setNode({ id: row.node_id, name: row.node_name })
                            }
                          >
                            {zh
                              ? '管理清理'
                              : 'Manage cleanup'}
                          </DropdownMenuItem>
                          <DropdownMenuItem
                            disabled={
                              !row.view.policy.enabled || pause.isPending
                            }
                            onClick={() => pause.mutate(row)}
                          >
                            {zh ? '暂停后续调度' : 'Pause future runs'}
                          </DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </TableCell>
                  </TableRow>
                ))}
                {rows.length === 0 && (
                  <TableRow>
                    <TableCell
                      colSpan={6}
                      className="py-10 text-center text-muted-foreground"
                    >
                      {zh ? '没有匹配的节点' : 'No matching nodes'}
                    </TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>
          </ListShell>
          <ListPagination {...pagination} zh={zh} />
        </>
      )}
      {pause.isError && <ErrorState description={pause.error.message} />}
      {node && (
        <CleanupNodeSheet
          key={node.id}
          node={node}
          open
          onOpenChange={(open) => {
            if (!open) setNode(null)
          }}
        />
      )}
    </div>
  )
}

export function CleanupNodeSheet({
  node,
  open,
  onOpenChange,
  initialTab = 'policy',
}: {
  node: { id: string; name: string }
  open: boolean
  onOpenChange: (open: boolean) => void
  initialTab?: 'policy' | 'preview'
}) {
  const { formatDateTime, timeZone } = useDateTime()
  const { language } = useI18n()
  const zh = language === 'zh-CN'
  const client = useQueryClient()
  const [tab, setTab] = useState<string>(initialTab)
  const [draft, setDraft] = useState<Config | null>(null)
  const [saved, setSaved] = useState<Policy | null>(null)
  const [preview, setPreview] = useState<Preview | null>(null)
  const [notice, setNotice] = useState('')
  const [error, setError] = useState('')
  const [copyFrom, setCopyFrom] = useState('')
  const [now, setNow] = useState(() => Date.now())
  const view = useQuery({
    queryKey: ['cleanup-policy', node.id],
    queryFn: () => api<View>(`${root(node.id)}/policy`),
    enabled: open,
    refetchInterval: (query) => (query.state.data?.active_run ? 2000 : false),
  })
  const summaries = useQuery({
    queryKey: ['cleanup-policies'],
    queryFn: () => api<Summary[]>('/cleanup/policies'),
    enabled: open,
  })
  useEffect(() => {
    if (view.data && !saved) {
      setSaved(view.data.policy)
      setDraft(configuration(view.data.policy))
    }
  }, [view.data, saved])
  useEffect(() => {
    if (!preview) return
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [preview])
  const dirty =
    !!draft &&
    !!saved &&
    JSON.stringify(draft) !== JSON.stringify(configuration(saved))
  const refresh = async () => {
    await Promise.all([
      client.invalidateQueries({ queryKey: ['cleanup-policy', node.id] }),
      client.invalidateQueries({ queryKey: ['cleanup-policies'] }),
      client.invalidateQueries({ queryKey: ['cleanup-runs', node.id] }),
      client.invalidateQueries({ queryKey: ['tasks'] }),
    ])
  }
  const save = useMutation({
    onError: refresh,
    mutationFn: (
      input: Config & {
        version: number
        confirmation_name?: string
        authorize?: boolean
      },
    ) =>
      api<Policy>(`${root(node.id)}/policy`, {
        method: 'PUT',
        body: JSON.stringify(input),
      }),
    onSuccess: async (policy) => {
      setSaved(policy)
      setDraft(configuration(policy))
      setPreview(null)
      setError('')
      setNotice(zh ? '策略已保存。' : 'Policy saved.')
      await refresh()
    },
  })
  const scan = useMutation({
    mutationFn: () =>
      api<Preview>(`${root(node.id)}/preview`, { method: 'POST', body: '{}' }),
    onSuccess: (value) => {
      setPreview(value)
      setNow(Date.now())
      setError('')
      setNotice('')
      void client.invalidateQueries({ queryKey: ['cleanup-policy', node.id] })
    },
  })
  const run = useMutation({
    onError: refresh,
    mutationFn: (input: { preview_id: string; confirmation_name: string }) =>
      api<{ id: string }>(`${root(node.id)}/run`, {
        method: 'POST',
        body: JSON.stringify(input),
      }),
    onSuccess: async () => {
      setPreview(null)
      setTab('history')
      setNotice(zh ? '清理任务已启动。' : 'Cleanup task started.')
      await refresh()
    },
  })
  const cancel = useMutation({
    mutationFn: (taskID: string) =>
      api(nodePath(node.id, `/tasks/${encodeURIComponent(taskID)}/cancel`), {
        method: 'POST',
      }),
    onSuccess: refresh,
  })
  const mutationError =
    error ||
    save.error?.message ||
    scan.error?.message ||
    run.error?.message ||
    cancel.error?.message
  const submit = async () => {
    if (!draft || !saved) return
    const normalized = {
      ...draft,
      protected: Object.fromEntries(
        Object.entries(draft.protected).map(([kind, values]) => [
          kind,
          [
            ...new Set(
              (values ?? []).map((value) => value.trim()).filter(Boolean),
            ),
          ],
        ]),
      ),
    }
    const needsAuthorization =
      normalized.enabled &&
      (!saved.enabled ||
        !saved.authorized_at ||
        expands(configuration(saved), normalized))
    let confirmation: { confirmation_name?: string; authorize?: boolean } = {}
    if (needsAuthorization) {
      const answer = await promptWithCheckboxDialog({
        title: zh
          ? `授权 ${node.name} 定时清理`
          : `Authorize scheduled cleanup for ${node.name}`,
        description: `${scopeLabel(draft, zh)}\n${zh ? '系统将按保留期自动删除符合规则的资源；卷仍需手动确认。' : 'The system will automatically delete eligible resources according to retention. Volumes still require manual confirmation.'}`,
        danger: true,
        confirmLabel: zh ? '授权并保存' : 'Authorize and save',
        input: {
          label: zh ? '输入节点名称' : 'Type the node name',
          requiredValue: node.name,
        },
        checkbox: {
          label: zh
            ? '允许按此策略自动删除资源'
            : 'Allow automatic resource deletion under this policy',
          initialChecked: false,
          required: true,
        },
      })
      if (answer?.value !== node.name || !answer.checked) {
        if (answer)
          setError(
            zh
              ? '必须勾选自动删除授权。'
              : 'Automatic deletion authorization must be checked.',
          )
        return
      }
      confirmation = {
        confirmation_name: answer.value,
        authorize: answer.checked,
      }
    }
    save.mutate({ ...normalized, version: saved.version, ...confirmation })
  }
  const execute = async () => {
    if (!preview) return
    const answer = await promptDialog({
      title: zh ? `立即清理 ${node.name}？` : `Clean ${node.name} now?`,
      description: `${scopeLabel(preview.policy, zh)}\n${zh ? '仅处理本次预览候选，缓存按规则执行。已经删除的资源无法撤销。' : 'Only preview candidates are processed; cache cleanup is rule-based. Completed deletions cannot be undone.'}`,
      danger: true,
      confirmLabel: zh ? '开始清理' : 'Start cleanup',
      input: {
        label: zh ? '输入节点名称' : 'Type the node name',
        requiredValue: node.name,
      },
    })
    if (answer === node.name)
      run.mutate({ preview_id: preview.id, confirmation_name: answer })
  }
  const close = async () => {
    if (
      dirty &&
      !(await confirmDialog({
        title: zh ? '放弃未保存的策略？' : 'Discard unsaved policy?',
        description: zh
          ? '未保存的修改将丢失。'
          : 'Unsaved changes will be lost.',
        confirmLabel: zh ? '放弃修改' : 'Discard',
      }))
    )
      return
    onOpenChange(false)
  }
  const busy =
    save.isPending || scan.isPending || run.isPending || !!view.data?.active_run
  return (
    <Sheet
      open={open}
      onOpenChange={(next) => {
        if (!next) void close()
      }}
    >
      <SheetContent className="gap-0 data-[side=right]:w-full data-[side=right]:sm:w-[780px] data-[side=right]:sm:max-w-[calc(100vw-2rem)]">
        <SheetHeader className="border-b pr-12">
          <SheetTitle>
            {node.name} · {zh ? '存储清理' : 'Storage cleanup'}
          </SheetTitle>
          <SheetDescription>
            {zh
              ? '保守清理，保护项目资源；删除前实时复核。'
              : 'Conservative cleanup with project protection and live checks before deletion.'}
          </SheetDescription>
        </SheetHeader>
        <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto p-4">
          <Tabs value={tab} onValueChange={setTab}>
            <TabsList>
              <TabsTrigger value="policy">
                {zh ? '清理策略' : 'Cleanup policy'}
              </TabsTrigger>
              <TabsTrigger value="preview">
                {zh ? '清理预览' : 'Cleanup preview'}
              </TabsTrigger>
              <TabsTrigger value="history">
                {zh ? '执行历史' : 'Run history'}
              </TabsTrigger>
            </TabsList>
          </Tabs>
          {view.isPending ? (
            <LoadingState
              label={zh ? '正在加载清理数据' : 'Loading cleanup data'}
              rows={5}
            />
          ) : view.isError ? (
            <ErrorState description={view.error.message} />
          ) : (
            <>
              {view.data.active_run && (
                <div className="flex items-center justify-between gap-2 rounded-md border p-3">
                  <span className="text-sm">
                    {zh
                      ? '清理正在执行；暂停策略不会取消当前任务。'
                      : 'Cleanup is running. Pausing the policy does not cancel this task.'}
                  </span>
                  {view.data.policy.enabled && (
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={save.isPending}
                      onClick={() =>
                        save.mutate({
                          ...configuration(view.data.policy),
                          enabled: false,
                          version: view.data.policy.version,
                        })
                      }
                    >
                      {zh ? '暂停后续调度' : 'Pause future runs'}
                    </Button>
                  )}
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={cancel.isPending || !view.data.active_run.task_id}
                    onClick={() => {
                      const taskID = view.data.active_run?.task_id
                      if (taskID) cancel.mutate(taskID)
                    }}
                  >
                    {zh ? '取消任务' : 'Cancel task'}
                  </Button>
                </div>
              )}
              {tab === 'policy' && draft && saved && (
                <>
                  <div className="flex flex-wrap items-center gap-2">
                    <Select<string>
                      value={copyFrom}
                      onValueChange={(value) => setCopyFrom(value ?? '')}
                    >
                      <SelectTrigger
                        className="w-52"
                        aria-label={zh ? '复制来源节点' : 'Copy from node'}
                      >
                        <SelectValue
                          placeholder={
                            zh ? '复制其他节点配置' : 'Copy another node policy'
                          }
                        >
                          {
                            summaries.data?.find(
                              (row) => row.node_id === copyFrom,
                            )?.node_name
                          }
                        </SelectValue>
                      </SelectTrigger>
                      <SelectContent>
                        {summaries.data
                          ?.filter((row) => row.node_id !== node.id)
                          .map((row) => (
                            <SelectItem key={row.node_id} value={row.node_id}>
                              {row.node_name}
                            </SelectItem>
                          ))}
                      </SelectContent>
                    </Select>
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={!copyFrom || busy}
                      onClick={() => {
                        const source = summaries.data?.find(
                          (row) => row.node_id === copyFrom,
                        )
                        if (source) {
                          setDraft({
                            ...configuration(source.view.policy),
                            enabled: false,
                          })
                          setNotice(
                            zh
                              ? '配置已复制到草稿，定时清理保持关闭。保存后生效。'
                              : 'Copied into the draft with scheduling disabled. Save to apply.',
                          )
                          setCopyFrom('')
                        }
                      }}
                    >
                      <Copy />
                      {zh ? '复制' : 'Copy'}
                    </Button>
                  </div>
                  <PolicyFields
                    draft={draft}
                    setDraft={setDraft}
                    zh={zh}
                    disabled={busy}
                  />
                  <div className="space-y-1 rounded-md border p-3">
                    <p className="text-xs text-muted-foreground">
                      {zh
                        ? '已保存策略接下来的三次计划（暂停时不执行）'
                        : 'Next three occurrences of the saved policy (paused policies do not execute)'}
                    </p>
                    {view.data.next_runs.map((value) => (
                      <div key={value} className="text-sm tabular-nums">
                        {formatDateTime(value)} ·{' '}
                        {timeZone}
                      </div>
                    ))}
                  </div>
                  <div className="flex items-center gap-3">
                    <Button
                      disabled={busy || (!dirty && saved.version > 0)}
                      onClick={() => void submit()}
                    >
                      {save.isPending ? <Spinner /> : <Save />}
                      {zh ? '保存策略' : 'Save policy'}
                    </Button>
                    <span className="text-xs text-muted-foreground">
                      {dirty
                        ? zh
                          ? '有未保存修改'
                          : 'Unsaved changes'
                        : zh
                          ? '新节点默认关闭定时清理'
                          : 'Scheduling is disabled for new nodes'}
                    </span>
                  </div>
                </>
              )}
              {tab === 'preview' && (
                <>
                  <div className="flex flex-wrap items-center gap-3">
                    <Button
                      variant="outline"
                      disabled={busy || dirty}
                      onClick={() => scan.mutate()}
                    >
                      {scan.isPending ? <Spinner /> : <RefreshCw />}
                      {zh ? '生成预览' : 'Generate preview'}
                    </Button>
                    <span className="text-xs text-muted-foreground">
                      {dirty
                        ? zh
                          ? '请先保存策略修改'
                          : 'Save policy changes first'
                        : scopeLabel(view.data.policy, zh)}
                    </span>
                  </div>
                  {preview && (
                    <>
                      <p className="text-xs text-muted-foreground">
                        {zh ? '生成于' : 'Generated'}{' '}
                        {formatDateTime(preview.generated_at)} ·{' '}
                        {zh ? '有效至' : 'Expires'}{' '}
                        {formatDateTime(preview.expires_at)}
                        {now >= Date.parse(preview.expires_at) && (
                          <span className="text-destructive">
                            {' '}
                            ·{' '}
                            {zh
                              ? '已过期，请重新预览'
                              : 'Expired; regenerate preview'}
                          </span>
                        )}
                      </p>
                      <PreviewResources
                        preview={preview}
                        node={node}
                        zh={zh}
                        onVolumeDeleted={() => {
                          setPreview(null)
                          scan.mutate()
                          void refresh()
                        }}
                      />
                      <Button
                        variant="destructive"
                        disabled={
                          busy ||
                          dirty ||
                          now >= Date.parse(preview.expires_at) ||
                          preview.policy_version !== view.data.policy.version
                        }
                        onClick={() => void execute()}
                      >
                        {run.isPending && <Spinner />}
                        {zh ? '立即执行清理' : 'Run cleanup now'}
                      </Button>
                    </>
                  )}
                  {!preview && (
                    <p className="text-sm text-muted-foreground">
                      {zh
                        ? '预览不删除任何资源。执行前会重新检查状态，新增资源不会追加到本次候选。'
                        : 'Preview never deletes resources. State is rechecked before execution, and new resources are not added to the candidate set.'}
                    </p>
                  )}
                  {!view.data.capabilities.available && (
                    <p className="text-sm text-muted-foreground">
                      {zh
                        ? '节点当前不可用，仍可编辑策略。'
                        : 'Node currently unavailable; policies can still be edited.'}
                    </p>
                  )}
                </>
              )}
              {tab === 'history' && (
                <CleanupHistory nodeID={node.id} zh={zh} />
              )}
            </>
          )}
          {notice && (
            <p role="status" className="text-sm text-muted-foreground">
              {notice}
            </p>
          )}
          {mutationError && <ErrorState description={mutationError} />}
          {view.data && saved && view.data.policy.version > saved.version && (
            <div className="space-y-2 border-t pt-3">
              <p className="text-sm text-muted-foreground">
                {zh
                  ? '策略已在其他位置更新，请重新加载后编辑。'
                  : 'The policy was updated elsewhere. Reload before editing.'}
              </p>
              <Button
                variant="outline"
                onClick={async () => {
                  if (
                    dirty &&
                    !(await confirmDialog({
                      title: zh
                        ? '重新加载并放弃当前草稿？'
                        : 'Reload and discard this draft?',
                      description: zh
                        ? '将读取最新策略，未保存修改会丢失。'
                        : 'The latest policy will replace unsaved changes.',
                      confirmLabel: zh ? '重新加载' : 'Reload',
                    }))
                  )
                    return
                  setSaved(view.data.policy)
                  setDraft(configuration(view.data.policy))
                  setPreview(null)
                  setError('')
                  save.reset()
                }}
              >
                {zh ? '重新加载策略' : 'Reload policy'}
              </Button>
            </div>
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}

function PolicyFields({
  draft,
  setDraft,
  zh,
  disabled,
}: {
  draft: Config
  setDraft: (value: Config) => void
  zh: boolean
  disabled: boolean
}) {
  const weekdays = zh
    ? ['周日', '周一', '周二', '周三', '周四', '周五', '周六']
    : [
        'Sunday',
        'Monday',
        'Tuesday',
        'Wednesday',
        'Thursday',
        'Friday',
        'Saturday',
      ]
  return (
    <fieldset disabled={disabled} className="space-y-5">
      <Label className="flex items-center justify-between border-b pb-3">
        {zh ? '开启定时清理' : 'Enable scheduled cleanup'}
        <Switch
          checked={draft.enabled}
          onCheckedChange={(enabled) => setDraft({ ...draft, enabled })}
        />
      </Label>
      <div className="grid gap-4 sm:grid-cols-2">
        <div className="space-y-1.5">
          <Label>{zh ? '执行频率' : 'Frequency'}</Label>
          <Select<Config['schedule']['frequency']>
            value={draft.schedule.frequency}
            onValueChange={(frequency) => {
              if (frequency)
                setDraft({
                  ...draft,
                  schedule: { ...draft.schedule, frequency },
                })
            }}
          >
            <SelectTrigger aria-label={zh ? '执行频率' : 'Frequency'}>
              <SelectValue>
                {draft.schedule.frequency === 'daily'
                  ? zh
                    ? '每日'
                    : 'Daily'
                  : zh
                    ? '每周'
                    : 'Weekly'}
              </SelectValue>
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="daily">{zh ? '每日' : 'Daily'}</SelectItem>
              <SelectItem value="weekly">{zh ? '每周' : 'Weekly'}</SelectItem>
            </SelectContent>
          </Select>
        </div>
        {draft.schedule.frequency === 'weekly' && (
          <div className="space-y-1.5">
            <Label>{zh ? '星期' : 'Weekday'}</Label>
            <Select<string>
              value={String(draft.schedule.weekday)}
              onValueChange={(value) =>
                setDraft({
                  ...draft,
                  schedule: { ...draft.schedule, weekday: Number(value) },
                })
              }
            >
              <SelectTrigger aria-label={zh ? '星期' : 'Weekday'}>
                <SelectValue>{weekdays[draft.schedule.weekday]}</SelectValue>
              </SelectTrigger>
              <SelectContent>
                {weekdays.map((label, index) => (
                  <SelectItem key={index} value={String(index)}>
                    {label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        )}
        <div className="space-y-1.5">
          <Label htmlFor="cleanup-time">{zh ? '执行时间' : 'Time'}</Label>
          <Input
            id="cleanup-time"
            type="time"
            value={`${String(draft.schedule.hour).padStart(2, '0')}:${String(draft.schedule.minute).padStart(2, '0')}`}
            onChange={(event) => {
              const [hour, minute] = event.target.value.split(':').map(Number)
              setDraft({
                ...draft,
                schedule: { ...draft.schedule, hour, minute },
              })
            }}
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="cleanup-timezone">
            {zh ? 'IANA 时区' : 'IANA timezone'}
          </Label>
          <Input
            id="cleanup-timezone"
            placeholder="Asia/Shanghai"
            value={draft.schedule.timezone}
            onChange={(event) =>
              setDraft({
                ...draft,
                schedule: { ...draft.schedule, timezone: event.target.value },
              })
            }
          />
        </div>
      </div>
      <div className="divide-y rounded-md border px-3">
        {(['images', 'cache', 'containers', 'networks'] as const).map((key) => {
          const kind: Kind =
            key === 'images'
              ? 'image'
              : key === 'containers'
                ? 'container'
                : key === 'networks'
                  ? 'network'
                  : 'cache'
          return (
            <div key={key} className="space-y-2 py-3">
              <Label className="flex items-center justify-between">
                <span>{kindLabel(kind, zh)}</span>
                <Switch
                  checked={draft[key].enabled}
                  onCheckedChange={(enabled) =>
                    setDraft({ ...draft, [key]: { ...draft[key], enabled } })
                  }
                />
              </Label>
              <div className="flex flex-wrap items-center gap-3">
                <Label
                  htmlFor={`cleanup-${key}-days`}
                  className="text-xs text-muted-foreground"
                >
                  {key === 'containers'
                    ? zh
                      ? '停止保留天数'
                      : 'Days since stop'
                    : key === 'cache'
                      ? zh
                        ? '最近使用保留天数'
                        : 'Days since last use'
                      : zh
                        ? '创建保留天数'
                        : 'Days since creation'}
                </Label>
                <Input
                  id={`cleanup-${key}-days`}
                  className="w-24"
                  type="number"
                  min={1}
                  max={3650}
                  value={draft[key].retention_days}
                  onChange={(event) =>
                    setDraft({
                      ...draft,
                      [key]: {
                        ...draft[key],
                        retention_days: Number(event.target.value),
                      },
                    })
                  }
                />
              </div>
              {key === 'images' && (
                <Label className="flex items-center gap-2 text-xs">
                  <Checkbox
                    checked={draft.images.include_tagged}
                    onCheckedChange={(value) =>
                      setDraft({
                        ...draft,
                        images: {
                          ...draft.images,
                          include_tagged: value === true,
                        },
                      })
                    }
                  />
                  {zh
                    ? '扩大到未被任何容器引用的带标签镜像'
                    : 'Include tagged images unreferenced by any container'}
                </Label>
              )}
              {key === 'cache' && (
                <>
                  <div className="flex flex-wrap items-center gap-3">
                    <Label
                      htmlFor="cleanup-cache-budget"
                      className="text-xs text-muted-foreground"
                    >
                      {zh ? '保留空间预算（GiB）' : 'Reserved budget (GiB)'}
                    </Label>
                    <Input
                      id="cleanup-cache-budget"
                      className="w-24"
                      type="number"
                      min={0}
                      max={1048576}
                      step={0.1}
                      value={draft.cache.reserved_bytes / 1024 ** 3}
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          cache: {
                            ...draft.cache,
                            reserved_bytes: Math.round(
                              Number(event.target.value) * 1024 ** 3,
                            ),
                          },
                        })
                      }
                    />
                  </div>
                  <p className="text-xs text-muted-foreground">
                    {zh
                      ? '仅 Engine 内置 Builder；预算由 Engine 尽力执行，不保证精确保留或释放量。'
                      : 'Engine builder only. The budget is best-effort, with no guarantee of exact retained or reclaimed bytes.'}
                  </p>
                </>
              )}
            </div>
          )
        })}
        <div className="space-y-1 py-3">
          <Label className="flex items-center justify-between">
            {kindLabel('volume', zh)}
            <Switch
              checked={draft.scan_volumes}
              onCheckedChange={(scan_volumes) =>
                setDraft({ ...draft, scan_volumes })
              }
            />
          </Label>
          <p className="text-xs text-muted-foreground">
            {zh
              ? '不会自动删除。每个卷都必须输入卷名并确认数据永久丢失。'
              : 'Never automatically deleted. Each volume requires its name and confirmation of permanent data loss.'}
          </p>
        </div>
      </div>
      <div className="flex gap-2 text-xs text-muted-foreground">
        <ShieldCheck className="size-4 shrink-0" />
        <span>
          {zh
            ? '始终保护 Compose 项目、控制平面、Agent、Builder、托管配置及 CD 发布引用。'
            : 'Always protects Compose projects, control plane, Agent, builders, managed configuration and CD release references.'}
        </span>
      </div>
      <details className="space-y-3">
        <summary className="cursor-pointer text-sm font-medium">
          {zh ? '额外保护名单' : 'Additional protected resources'}
        </summary>
        <p className="text-xs text-muted-foreground">
          {zh
            ? '输入完整资源 ID 或名称，每行一个；也可使用 suma.cleanup.protect=true 标签。'
            : 'One complete resource ID or name per line. The suma.cleanup.protect=true label also protects resources.'}
        </p>
        {(['image', 'container', 'network', 'volume'] as const).map((kind) => (
          <div key={kind} className="space-y-1">
            <Label htmlFor={`cleanup-protect-${kind}`}>
              {kindLabel(kind, zh)}
            </Label>
            <Textarea
              id={`cleanup-protect-${kind}`}
              rows={2}
              value={(draft.protected[kind] ?? []).join('\n')}
              onChange={(event) =>
                setDraft({
                  ...draft,
                  protected: {
                    ...draft.protected,
                    [kind]: event.target.value.split('\n'),
                  },
                })
              }
            />
          </div>
        ))}
      </details>
      <p className="text-xs text-muted-foreground">
        {zh
          ? '离线或错过的计划不补跑。重启不会重放删除；暂停不撤销已完成操作。'
          : 'Offline or missed schedules are not replayed. Restart never replays deletions; pausing cannot undo completed work.'}
      </p>
    </fieldset>
  )
}

function PreviewResources({
  preview,
  node,
  zh,
  onVolumeDeleted,
}: {
  preview: Preview
  node: { id: string; name: string }
  zh: boolean
  onVolumeDeleted: () => void
}) {
  const [kind, setKind] = useState<Kind>('image')
  const [candidatesOnly, setCandidatesOnly] = useState(false)
  const resources = preview.resources.filter(
    (row) =>
      row.kind === kind && (!candidatesOnly || row.candidate || row.manual),
  )
  const pagination = useListPagination(resources, `${kind}-${candidatesOnly}`)
  const remove = useMutation({
    mutationFn: (name: string) =>
      api(
        nodePath(
          node.id,
          `/volumes/${encodeURIComponent(name)}?confirm=${encodeURIComponent(name)}`,
        ),
        { method: 'DELETE' },
      ),
    onSuccess: onVolumeDeleted,
  })
  const removeVolume = async (name: string) => {
    const answer = await promptDialog({
      title: zh ? `永久删除卷 ${name}？` : `Permanently delete volume ${name}?`,
      description: zh
        ? '卷中的数据将永久丢失，无法撤销。删除前将重新检查引用及保护状态。'
        : 'All data in this volume will be permanently lost. References and protection are checked again before deletion.',
      danger: true,
      confirmLabel: zh ? '永久删除' : 'Delete permanently',
      input: {
        label: zh ? '输入完整卷名' : 'Type the full volume name',
        requiredValue: name,
      },
    })
    if (answer === name) remove.mutate(name)
  }
  return (
    <div className="space-y-3">
      <p className="text-xs text-muted-foreground">
        {zh
          ? '容量均为估算或当前报告，镜像含共享层，不汇总成预计释放空间。缓存候选由 Engine 按规则最终决定。'
          : 'Sizes are estimates or current reports; shared image layers are not summed as reclaimable space. Engine selects the final cache records.'}
      </p>
      <Tabs value={kind} onValueChange={(value) => setKind(value as Kind)}>
        <TabsList className="flex h-auto flex-wrap">
          {kinds.map((value) => (
            <TabsTrigger key={value} value={value}>
              {kindLabel(value, zh)}
            </TabsTrigger>
          ))}
        </TabsList>
      </Tabs>
      {kind === 'cache' && !preview.capabilities.build_cache && (
        <p className="text-sm text-muted-foreground">
          {zh
            ? '此 Engine 不支持构建缓存清理，本项会跳过。'
            : 'This Engine does not support build cache cleanup; this category will be skipped.'}
        </p>
      )}
      <Label className="flex items-center gap-2 text-xs">
        <Checkbox
          checked={candidatesOnly}
          onCheckedChange={(value) => setCandidatesOnly(value === true)}
        />
        {zh
          ? '仅显示候选／待确认卷'
          : 'Show candidates / volumes awaiting confirmation'}
      </Label>
      <ListShell>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{zh ? '资源' : 'Resource'}</TableHead>
              {kind !== 'network' && (
                <TableHead>{zh ? '容量（估算）' : 'Size (estimate)'}</TableHead>
              )}
              <TableHead>{zh ? '判定' : 'Decision'}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {pagination.items.map((row) => (
              <TableRow key={row.id}>
                <TableCell className="max-w-72 break-all whitespace-normal">
                  <div>{row.name}</div>
                  {row.name !== row.id && (
                    <div className="text-xs text-muted-foreground">
                      {row.id}
                    </div>
                  )}
                </TableCell>
                {kind !== 'network' && (
                  <TableCell className="text-xs tabular-nums">
                    {bytes(row.size_bytes, zh)}
                  </TableCell>
                )}
                <TableCell className="max-w-56 whitespace-normal text-xs">
                  <span
                    className={
                      row.candidate || row.manual
                        ? 'text-foreground'
                        : 'text-muted-foreground'
                    }
                  >
                    {reasonLabel(row.reason, zh)}
                  </span>
                  {row.manual && (
                    <Button
                      variant="ghost"
                      size="sm"
                      className="ml-1 text-destructive"
                      disabled={remove.isPending}
                      onClick={() => void removeVolume(row.name)}
                    >
                      {zh ? '确认删除' : 'Confirm deletion'}
                    </Button>
                  )}
                </TableCell>
              </TableRow>
            ))}
            {resources.length === 0 && (
              <TableRow>
                <TableCell
                  colSpan={kind === 'network' ? 2 : 3}
                  className="py-6 text-center text-muted-foreground"
                >
                  {zh ? '没有匹配资源' : 'No matching resources'}
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </ListShell>
      <ListPagination {...pagination} zh={zh} />
      {remove.isError && <ErrorState description={remove.error.message} />}
    </div>
  )
}

function CleanupHistory({
  nodeID,
  zh,
}: {
  nodeID: string
  zh: boolean
}) {
  const { formatDateTime } = useDateTime()
  const [page, setPage] = useState(1)
  const [failedOnly, setFailedOnly] = useState(false)
  const [expanded, setExpanded] = useState<string | null>(null)
  const query = useQuery({
    queryKey: ['cleanup-runs', nodeID, page, failedOnly],
    queryFn: () =>
      api<RunPage>(`${root(nodeID)}/runs?page=${page}&failed=${failedOnly}`),
    refetchInterval: 2000,
  })
  return (
    <div className="space-y-3">
      <Label className="flex items-center gap-2">
        <Checkbox
          checked={failedOnly}
          onCheckedChange={(value) => {
            setFailedOnly(value === true)
            setPage(1)
          }}
        />
        {zh ? '仅失败／中断记录' : 'Failures / interruptions only'}
      </Label>
      {query.isPending ? (
        <LoadingState
          label={zh ? '正在加载清理数据' : 'Loading cleanup data'}
          rows={4}
        />
      ) : query.isError ? (
        <ErrorState description={query.error.message} />
      ) : (
        <>
          {query.data.items.length === 0 && (
            <p className="py-6 text-center text-sm text-muted-foreground">
              {zh ? '暂无执行记录' : 'No executions yet'}
            </p>
          )}
          {query.data.items.map((row) => (
            <div key={row.id} className="rounded-md border">
              <button
                type="button"
                className="flex w-full flex-wrap items-center justify-between gap-2 p-3 text-left"
                aria-expanded={expanded === row.id}
                onClick={() => setExpanded(expanded === row.id ? null : row.id)}
              >
                <div className="text-sm">
                  <span>{formatDateTime(row.created_at)}</span>
                  <div className="text-xs text-muted-foreground">
                    {row.trigger === 'scheduled'
                      ? zh
                        ? '定时'
                        : 'Scheduled'
                      : zh
                        ? '手动'
                        : 'Manual'}{' '}
                    · {zh ? '策略版本' : 'Policy version'} {row.policy_version}
                  </div>
                </div>
                <StatusBadge tone={tone(row.status)}>
                  {statusLabel(row.status, zh)}
                </StatusBadge>
              </button>
              {expanded === row.id && (
                <RunDetails
                  nodeID={nodeID}
                  row={row}
                  zh={zh}
                />
              )}
            </div>
          ))}
          <div className="flex items-center justify-between gap-2 text-xs text-muted-foreground">
            <span>
              {zh
                ? `共 ${query.data.total} 条 · 第 ${page} 页`
                : `${query.data.total} executions · Page ${page}`}
            </span>
            <div className="flex gap-2">
              <Button
                variant="outline"
                size="sm"
                disabled={page <= 1}
                onClick={() => setPage(page - 1)}
              >
                {zh ? '上一页' : 'Previous'}
              </Button>
              <Button
                variant="outline"
                size="sm"
                disabled={page * 20 >= query.data.total}
                onClick={() => setPage(page + 1)}
              >
                {zh ? '下一页' : 'Next'}
              </Button>
            </div>
          </div>
        </>
      )}
    </div>
  )
}
function RunDetails({
  nodeID,
  row,
  zh,
}: {
  nodeID: string
  row: Run
  zh: boolean
}) {
  const { formatTime } = useDateTime()
  const details = useQuery({
    queryKey: ['cleanup-run', nodeID, row.id],
    queryFn: () => api<Run>(`${root(nodeID)}/runs/${row.id}`),
    refetchInterval: ['running', 'pending'].includes(row.status) ? 2000 : false,
  })
  const run = details.data ?? row
  const logs = useQuery({
    queryKey: ['task-logs', 'node', nodeID, row.task_id],
    queryFn: () =>
      api<{ id: number; message: string; created_at: string }[]>(
        nodePath(nodeID, `/tasks/${row.task_id}/logs`),
      ),
    enabled: !!row.task_id,
    refetchInterval: ['running', 'pending'].includes(row.status) ? 2000 : false,
  })
  const outcomes = useListPagination(run.result.outcomes ?? [])
  return (
    <div className="space-y-3 border-t p-3">
      <p className="text-xs text-muted-foreground">{run.message}</p>
      <p className="text-xs">{scopeLabel(run.policy, zh)}</p>
      <div className="space-y-1 text-xs">
        {kinds.map((kind) => {
          const stats = run.result.stats?.[kind]
          return (
            stats && (
              <div key={kind} className="flex flex-wrap justify-between gap-2">
                <span>{kindLabel(kind, zh)}</span>
                <span>
                  {zh
                    ? `删除 ${stats.deleted} · 跳过 ${stats.skipped} · 失败 ${stats.failed} · 扫描 ${stats.scanned}`
                    : `${stats.deleted} deleted · ${stats.skipped} skipped · ${stats.failed} failed · ${stats.scanned} scanned`}
                  {kind === 'cache' &&
                    stats.reclaimed_bytes != null &&
                    ` · ${zh ? 'Engine 报告回收' : 'Engine reported reclaimed'} ${bytes(stats.reclaimed_bytes, zh)}`}
                </span>
              </div>
            )
          )
        })}
      </div>
      <p className="text-xs text-muted-foreground">
        {zh
          ? '镜像层用量观测（不等同于本任务独占回收）'
          : 'Observed image-layer usage (not exclusive task reclamation)'}
        ：{bytes(run.result.image_layers_before, zh)} →{' '}
        {bytes(run.result.image_layers_after, zh)}
      </p>
      <div className="space-y-1 text-xs text-muted-foreground">
        {(['container_bytes', 'volume_bytes'] as const).map((key) => (
          <div key={key}>
            {key === 'container_bytes'
              ? zh
                ? '容器可写层用量观测'
                : 'Observed container writable-layer usage'
              : zh
                ? '卷用量观测'
                : 'Observed volume usage'}
            ：{bytes(run.result.usage_before?.[key], zh)} →{' '}
            {bytes(run.result.usage_after?.[key], zh)}
          </div>
        ))}
      </div>
      {outcomes.items.map((outcome) => (
        <div
          key={`${outcome.kind}-${outcome.id}`}
          className="flex flex-wrap justify-between gap-2 text-xs"
        >
          <span className="max-w-96 break-all">{outcome.name}</span>
          <span>
            {outcome.kind !== 'network' &&
              outcome.kind !== 'cache' &&
              `${zh ? '容量估算' : 'Estimated size'} ${bytes(outcome.estimated_bytes, zh)} · `}
            {statusLabel(outcome.status, zh)}
            {outcome.reason && ` · ${reasonLabel(outcome.reason, zh)}`}
          </span>
        </div>
      ))}
      {(run.result.outcomes?.length ?? 0) > 0 && (
        <ListPagination {...outcomes} zh={zh} />
      )}
      {details.isError && <ErrorState description={details.error.message} />}
      {row.task_id && (
        <details>
          <summary className="cursor-pointer text-xs font-medium">
            {zh ? '任务日志' : 'Task logs'} · {row.task_id.slice(0, 12)}
          </summary>
          {logs.isError ? (
            <ErrorState description={logs.error.message} />
          ) : (
            <div className="mt-2 max-h-56 space-y-1 overflow-y-auto rounded-md bg-muted p-2 font-mono text-xs">
              {logs.data?.map((log) => (
                <div key={log.id} className="break-all">
                  {formatTime(log.created_at)}{' '}
                  {log.message}
                </div>
              ))}
            </div>
          )}
        </details>
      )}
    </div>
  )
}
