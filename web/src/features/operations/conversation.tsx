import { Fragment, useRef, useState, type ReactNode } from 'react'
import { Link } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowDown, ArrowUp, BookOpen, ChevronDown, History, MessageSquare, Plus, Sparkles } from 'lucide-react'
import { Bubble, BubbleContent } from '../../components/ui/bubble'
import { Button } from '../../components/ui/button'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '../../components/ui/collapsible'
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupTextarea } from '../../components/ui/input-group'
import { Message, MessageAvatar, MessageContent, MessageFooter, MessageHeader } from '../../components/ui/message'
import { MessageScroller, MessageScrollerButton, MessageScrollerContent, MessageScrollerItem, MessageScrollerProvider, MessageScrollerViewport } from '../../components/ui/message-scroller'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '../../components/ui/sheet'
import { Markdown } from '../../components/ui/markdown'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../../components/ui/select'
import { Spinner } from '../../components/ui/spinner'
import { TooltipHint } from '../../components/ui/tooltip-hint'
import { api } from '../../lib/api'
import { cn } from '../../lib/utils'
import type { DockerNode } from '../../lib/nodes'
import { useDateTime } from '../../lib/time-zone'
import { ErrorText } from './common'
import { useOpsText } from './helpers'
import type { AIOperation, AIRun, AISettings, Inbox } from './types'

// A follow-up is a separate diagnostic run, linked to its previous turn.
function conversationTurns(rows: AIRun[], selected: string) {
  const byID = new Map(rows.map(row => [row.id, row]))
  const turns: AIRun[] = [], visited = new Set<string>()
  let row = byID.get(selected)
  while (row && !visited.has(row.id)) {
    visited.add(row.id)
    turns.unshift(row)
    row = byID.get(row.parent_id)
  }
  return turns
}

export function AIConversation({ selected, onSelect, node, scopeNode, setScopeNode, model, setModel, resourceKind, resourceID, operations, renderOperation, question, setQuestion }: {
  selected: string
  onSelect: (id: string) => void
  node: string
  scopeNode: string
  setScopeNode: (value: string) => void
  model: string
  setModel: (value: string) => void
  resourceKind: string
  resourceID: string
  operations: AIOperation[]
  renderOperation: (op: AIOperation) => ReactNode
  question: string
  setQuestion: (value: string) => void
}) {
  const t = useOpsText(), client = useQueryClient(), { formatDateTime } = useDateTime()
  const [historyOpen, setHistoryOpen] = useState(false)
  const conversationVersion = useRef(0)
  const settings = useQuery({ queryKey: ['ai-settings'], queryFn: () => api<AISettings>('/ai/settings') })
  const runs = useQuery({ queryKey: ['ai-runs'], queryFn: () => api<AIRun[]>('/ai/runs'), refetchInterval: 2000 })
  const current = useQuery({ queryKey: ['ai-run', selected], queryFn: () => api<AIRun>(`/ai/runs/${encodeURIComponent(selected)}`), enabled: !!selected, refetchInterval: query => query.state.data?.status === 'running' ? 2000 : false })
  const timeline = useQuery({ queryKey: ['notification-inbox'], queryFn: () => api<Inbox>('/notifications/inbox'), enabled: !!selected, refetchInterval: 5000 })
  const nodes = useQuery({ queryKey: ['nodes'], queryFn: () => api<DockerNode[]>('/nodes') })
  const rows = current.data ? [current.data, ...(runs.data ?? []).filter(row => row.id !== current.data.id)] : runs.data ?? []
  const turns = conversationTurns(rows, selected), run = turns.at(-1), activeNode = scopeNode
  const parentIDs = new Set(rows.map(row => row.parent_id))
  const conversations = rows.filter(row => !parentIDs.has(row.id)).sort((a, b) => b.created_at.localeCompare(a.created_at))
  const busy = run?.status === 'running'
  const models = settings.data?.models || (settings.data?.model ? [settings.data.model] : [])
  const activeModel = model || run?.model || settings.data?.model || ''
  const authorizedNodes = (nodes.data || []).filter(item => settings.data?.node_ids.includes(item.id))
  const includeResource = !selected && (!activeNode || activeNode === node)
  const start = useMutation({
    mutationFn: (text: string) => api<AIRun>('/ai/runs', { method: 'POST', body: JSON.stringify({ node_id: activeNode, model: activeModel, resource_node_id: includeResource ? node : '', question: text, parent_id: selected, resource_type: includeResource ? resourceKind : '', resource_id: includeResource ? resourceID : '' }) }),
    onMutate: () => conversationVersion.current,
    onSuccess: (row, _text, version) => {
      client.setQueryData(['ai-run', row.id], row)
      client.setQueryData<AIRun[]>(['ai-runs'], previous => [row, ...(previous ?? []).filter(item => item.id !== row.id)])
      if (version === conversationVersion.current) {
        onSelect(row.id)
        setQuestion('')
      }
      void client.invalidateQueries({ queryKey: ['ai-runs'] })
      void client.invalidateQueries({ queryKey: ['ai-operations'] })
    },
  })
  const canSend = !!question.trim() && !start.isPending && !busy && !!settings.data?.enabled && settings.data.node_ids.length > 0 && (!activeNode || settings.data.node_ids.includes(activeNode)) && models.includes(activeModel) && (!selected || !!run)
  const nodeLabel = (id: string) => id ? nodes.data?.find(item => item.id === id)?.name || id : t('全部授权节点', 'All authorized nodes')
  const nodeName = nodeLabel(activeNode)
  function choose(id: string) { conversationVersion.current++; onSelect(id); setScopeNode(''); setModel(rows.find(row => row.id === id)?.model || ''); setQuestion(''); setHistoryOpen(false); start.reset() }
  const history = <>
    <Button variant="ghost" size="sm" className="w-full justify-start" onClick={() => choose('')}><Plus />{t('新诊断', 'New diagnosis')}</Button>
    <div className="mt-4 flex-1 space-y-1 overflow-y-auto overscroll-contain">
      {conversations.map(row => {
        const title = conversationTurns(rows, row.id)[0]?.question || row.question
        return <button key={row.id} type="button" aria-pressed={selected === row.id} onClick={() => choose(row.id)} className={cn('block w-full min-w-0 rounded-md px-3 py-2.5 text-left transition-colors hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring', selected === row.id && 'bg-muted')}>
          <span className="line-clamp-2 break-words text-sm">{title}</span>
          <span className="mt-1 block truncate text-xs text-muted-foreground">{nodeLabel(row.node_id)} · {row.status === 'running' ? t('分析中', 'Analyzing') : row.status === 'completed' ? t('完成', 'Completed') : row.status} · {formatDateTime(row.created_at)}</span>
        </button>
      })}
      {!conversations.length && <p className="px-3 py-4 text-xs text-muted-foreground">{t('诊断会话会保存在这里', 'Your conversations will appear here')}</p>}
    </div>
  </>

  return <div className="grid min-w-0 gap-6 lg:grid-cols-[220px_minmax(0,1fr)]">
    <aside aria-label={t('会话历史', 'Conversation history')} className="hidden h-[calc(100dvh-15.5rem)] min-h-96 flex-col rounded-lg bg-muted/30 p-3 lg:flex">{history}</aside>
    <div className="flex h-[calc(100dvh-15.5rem)] min-h-96 min-w-0 flex-col">
      <header className="flex min-w-0 items-center gap-2 pb-3">
        <MessageSquare className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
        <div className="min-w-0 flex-1"><h3 className="truncate text-sm font-medium">{turns[0]?.question || t('新会话', 'New conversation')}</h3><p className="mt-0.5 truncate text-xs text-muted-foreground">{nodeName}</p></div>
        <TooltipHint content={t('会话历史', 'Conversation history')}><Button size="icon-sm" variant="ghost" className="lg:hidden" aria-label={t('会话历史', 'Conversation history')} onClick={() => setHistoryOpen(true)}><History /></Button></TooltipHint>
        <TooltipHint content={t('新诊断', 'New diagnosis')}><Button size="icon-sm" variant="ghost" className="lg:hidden" aria-label={t('新诊断', 'New diagnosis')} onClick={() => choose('')}><Plus /></Button></TooltipHint>
      </header>
      <ErrorText error={settings.error || runs.error || current.error || start.error} />
      <MessageScrollerProvider key={turns[0]?.id || 'new'} autoScroll defaultScrollPosition="last-anchor">
        <MessageScroller className="flex-1">
          <MessageScrollerViewport aria-label={t('诊断对话', 'Diagnosis conversation')}>
            <MessageScrollerContent className="gap-6 px-1 py-5 sm:px-3">
              {turns.length === 0 && !start.isPending && <MessageScrollerItem messageId="welcome" className="flex flex-1 flex-col items-center justify-center gap-3 py-10 text-center">
                <div className="flex size-10 items-center justify-center rounded-lg bg-muted"><Sparkles className="size-5 text-muted-foreground" /></div>
                <div><h4 className="text-sm font-medium">{t('从一个运维问题开始', 'Start with an operations question')}</h4><p className="mt-2 max-w-sm text-xs leading-6 text-muted-foreground">{t('结合节点状态与诊断证据排查问题，建议操作逐项审核。', 'Investigate with node status and evidence, then review each suggested action.')}</p></div>
                <div className="mt-1 flex max-w-md flex-wrap justify-center gap-2">
                  {[t('这个容器为什么一直重启？', 'Why does this container keep restarting?'), t('哪些镜像有更新？', 'Which images have updates?'), t('清理为什么没有释放空间？', 'Why did cleanup reclaim no space?')].map(text => <Button key={text} variant="outline" size="sm" className="h-auto whitespace-normal py-1.5 text-xs" onClick={() => setQuestion(text)}>{text}</Button>)}
                </div>
              </MessageScrollerItem>}
              {turns.map(turn => <Fragment key={turn.id}>
                <MessageScrollerItem messageId={`${turn.id}-user`} scrollAnchor>
                  <Message align="end" aria-label={t('你的消息', 'Your message')}>
                    <MessageContent><Bubble variant="secondary"><BubbleContent className="whitespace-pre-wrap break-words rounded-lg">{turn.question}</BubbleContent></Bubble><MessageFooter>{t('你', 'You')} · {formatDateTime(turn.created_at)}</MessageFooter></MessageContent>
                  </Message>
                </MessageScrollerItem>
                <MessageScrollerItem messageId={`${turn.id}-assistant`}>
                  <Message aria-label={t('AI 回复', 'AI response')}>
                    <MessageAvatar className="size-7 min-w-7 self-start rounded-lg group-has-data-[slot=message-footer]/message:translate-y-0"><Sparkles className="size-3.5" aria-hidden="true" /></MessageAvatar>
                    <MessageContent>
                      <MessageHeader>SUMA · {t('运维助手', 'Operations assistant')}</MessageHeader>
                      <Bubble variant="ghost" className="w-full"><BubbleContent className="w-full space-y-3">
                        {turn.result?.summary && <Markdown>{turn.result.summary}</Markdown>}
                        {turn.status === 'running' && <p role="status" className="flex items-center gap-2 text-sm text-muted-foreground"><Spinner aria-hidden="true" />{t('正在分析节点与诊断证据…', 'Analyzing node status and evidence…')}</p>}
                        <ErrorText error={turn.error} />
                        {turn.status === 'interrupted' || turn.status === 'canceled' ? <p className="text-sm text-muted-foreground">{t('本次诊断已中断，可发送新问题继续排查。', 'This diagnosis was interrupted. Send a new question to continue.')}</p> : null}
                        {!!turn.result?.missing?.length && <p className="text-xs leading-6 text-muted-foreground">{t('缺失信息', 'Missing evidence')}: {turn.result.missing.join('; ')}</p>}
                        {!!turn.result?.evidence?.length && <Collapsible className="space-y-1">
                          <CollapsibleTrigger render={<Button variant="ghost" size="sm" className="group h-8 justify-start px-2 text-xs text-muted-foreground" />}><BookOpen className="size-3.5" />{t('诊断证据', 'Diagnostic evidence')} · {turn.result.evidence.length}<ChevronDown className="size-3.5 transition-transform group-aria-expanded:rotate-180" /></CollapsibleTrigger>
                          <CollapsibleContent className="space-y-3 rounded-lg bg-muted/40 p-3">{turn.result.evidence.map((e, i) => <div key={i} className="space-y-2">
                            <p className="break-all text-xs text-muted-foreground">{e.node_id ? `${nodeLabel(e.node_id)} · ` : ''}{e.source} · {e.resource} · {formatDateTime(e.time)}{e.unavailable ? ` · ${t('不可用', 'Unavailable')}` : ''}</p>
                            <pre className="max-h-48 overflow-auto overscroll-contain whitespace-pre-wrap break-all rounded-md bg-background/60 p-3 text-xs leading-relaxed">{e.content}</pre>
                          </div>)}</CollapsibleContent>
                        </Collapsible>}
                        {operations.some(op => op.run_id === turn.id) && <section aria-label={t('建议操作', 'Suggested operations')} className="space-y-2"><p className="text-xs font-medium text-muted-foreground">{t('建议操作 · 逐项审核', 'Suggested actions · individual review')}</p><div className="space-y-1 rounded-lg bg-muted/40 p-1">{operations.filter(op => op.run_id === turn.id).map(op => <Fragment key={op.id}>{renderOperation(op)}</Fragment>)}</div></section>}
                        {!!timeline.data?.items.some(item => item.run_id === turn.id || item.id === turn.event_id) && <Collapsible className="space-y-1">
                          <CollapsibleTrigger render={<Button variant="ghost" size="sm" className="group h-8 px-2 text-xs text-muted-foreground" />}><History className="size-3.5" />{t('事件与执行时间线', 'Event and execution timeline')}<ChevronDown className="size-3.5 transition-transform group-aria-expanded:rotate-180" /></CollapsibleTrigger>
                          <CollapsibleContent className="space-y-4 rounded-lg bg-muted/40 p-3">{timeline.data.items.filter(item => item.run_id === turn.id || item.id === turn.event_id).slice().reverse().map(item => <div key={item.id} className="space-y-1 text-xs"><p>{formatDateTime(item.time)} · {item.title}</p><p className="whitespace-pre-wrap break-words text-muted-foreground">{item.message}</p>{item.task_id && <Link to="/tasks" hash={item.task_id} className="underline">{t('查看任务', 'View task')}</Link>}</div>)}</CollapsibleContent>
                        </Collapsible>}
                      </BubbleContent></Bubble>
                      <MessageFooter className="gap-2 text-[11px]">{turn.status === 'completed' ? t('诊断完成', 'Diagnosis completed') : turn.status} · {turn.model || t('未记录模型', 'Model not recorded')} · {nodeLabel(turn.node_id)} · {turn.tokens} tokens</MessageFooter>
                    </MessageContent>
                  </Message>
                </MessageScrollerItem>
              </Fragment>)}
              {start.isPending && <>
                <MessageScrollerItem messageId="pending-user" scrollAnchor><Message align="end"><MessageContent><Bubble variant="secondary"><BubbleContent className="whitespace-pre-wrap rounded-lg">{start.variables}</BubbleContent></Bubble></MessageContent></Message></MessageScrollerItem>
                <MessageScrollerItem messageId="pending-assistant"><Message><MessageContent><p role="status" className="flex items-center gap-2 text-sm text-muted-foreground"><Spinner aria-hidden="true" />{t('正在发送分析请求…', 'Sending diagnosis request…')}</p></MessageContent></Message></MessageScrollerItem>
              </>}
            </MessageScrollerContent>
          </MessageScrollerViewport>
          <MessageScrollerButton aria-label={t('回到最新回复', 'Jump to latest reply')}><ArrowDown /><span className="sr-only">{t('回到最新回复', 'Jump to latest reply')}</span></MessageScrollerButton>
        </MessageScroller>
      </MessageScrollerProvider>
      <form className="shrink-0 pt-3" aria-label={t('发送诊断请求', 'Send diagnosis request')} onSubmit={e => { e.preventDefault(); if (canSend) start.mutate(question.trim()) }}>
        <div className="mb-2 flex min-w-0 flex-wrap gap-2">
          <Select items={[{ value: 'all', label: t('全部授权节点', 'All authorized nodes') }, ...authorizedNodes.map(item => ({ value: item.id, label: item.name }))]} value={activeNode || 'all'} onValueChange={value => { if (value) setScopeNode(value === 'all' ? '' : value) }} disabled={start.isPending || busy}>
            <SelectTrigger aria-label={t('诊断范围', 'Diagnosis scope')} className="min-w-0 flex-1 sm:max-w-56"><SelectValue /></SelectTrigger>
            <SelectContent align="start" alignItemWithTrigger={false}><SelectItem value="all">{t('全部授权节点', 'All authorized nodes')}</SelectItem>{authorizedNodes.map(item => <SelectItem key={item.id} value={item.id}>{item.name}</SelectItem>)}</SelectContent>
          </Select>
          {models.length > 0 && <Select items={models.map(value => ({ value, label: value }))} value={activeModel} onValueChange={value => { if (value) setModel(value) }} disabled={start.isPending || busy}>
            <SelectTrigger aria-label={t('对话模型', 'Conversation model')} className="min-w-0 flex-1 sm:max-w-64"><SelectValue /></SelectTrigger>
            <SelectContent align="start" alignItemWithTrigger={false}>{models.map(value => <SelectItem key={value} value={value}>{value}</SelectItem>)}</SelectContent>
          </Select>}
        </div>
        <InputGroup className="bg-background">
          <InputGroupTextarea aria-label={t('分析请求', 'Diagnosis request')} aria-describedby="ai-composer-hint" className="min-h-20 max-h-40 px-3 py-3 field-sizing-content" maxLength={4000} value={question} disabled={start.isPending || busy} onChange={e => setQuestion(e.target.value)} placeholder={t('描述问题，或继续追问…', 'Describe a problem or ask a follow-up…')} onKeyDown={e => { if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing && e.keyCode !== 229) { e.preventDefault(); e.currentTarget.form?.requestSubmit() } }} />
          <InputGroupAddon align="block-end" className="justify-between gap-2"><span className="min-w-0 truncate text-xs font-normal text-muted-foreground">{includeResource && resourceID ? `${resourceKind} · ${resourceID}` : t('变更操作需要单独审核', 'Changes require individual review')}</span><TooltipHint content={t('发起诊断', 'Start diagnosis')}><InputGroupButton type="submit" variant="default" size="icon-sm" aria-label={t('发起诊断', 'Start diagnosis')} disabled={!canSend}>{start.isPending || busy ? <Spinner aria-hidden="true" /> : <ArrowUp />}</InputGroupButton></TooltipHint></InputGroupAddon>
        </InputGroup>
        <p id="ai-composer-hint" className="mt-2 text-[11px] text-muted-foreground">{!settings.data?.enabled ? t('请在 AI 设置中连接模型并授权节点。', 'Connect a model and authorize nodes in AI settings.') : activeNode && !settings.data.node_ids.includes(activeNode) ? t('当前节点未授权 AI 访问，请在 AI 设置中选择。', 'Authorize this node in AI settings.') : !models.includes(activeModel) ? t('所选模型已移除，请重新选择。', 'This model was removed. Select another model.') : t('Enter 发送，Shift + Enter 换行', 'Enter to send, Shift + Enter for a new line')}</p>
      </form>
    </div>
    <Sheet open={historyOpen} onOpenChange={setHistoryOpen}><SheetContent side="left" className="w-80 max-w-[85vw]"><SheetHeader><SheetTitle>{t('会话历史', 'Conversation history')}</SheetTitle><SheetDescription>{t('打开已有会话，或发起新的诊断。', 'Open a conversation or start a new diagnosis.')}</SheetDescription></SheetHeader><div className="flex min-h-0 flex-1 flex-col px-4 pb-4">{history}</div></SheetContent></Sheet>
  </div>
}
