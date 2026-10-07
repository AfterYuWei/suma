import { Fragment, useEffect, useRef, useState, type ReactNode } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowDown, ArrowUp, BookOpen, ChevronDown, History, MessageSquare, Plus, Sparkles } from 'lucide-react'
import { Bubble, BubbleContent } from '../../components/ui/bubble'
import { Button } from '../../components/ui/button'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '../../components/ui/collapsible'
import { Input } from '../../components/ui/input'
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupTextarea } from '../../components/ui/input-group'
import { Message, MessageContent, MessageFooter } from '../../components/ui/message'
import { MessageScroller, MessageScrollerButton, MessageScrollerContent, MessageScrollerItem, MessageScrollerProvider, MessageScrollerViewport } from '../../components/ui/message-scroller'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '../../components/ui/sheet'
import { Markdown } from '../../components/ui/markdown'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../../components/ui/select'
import { Spinner } from '../../components/ui/spinner'
import { TooltipHint } from '../../components/ui/tooltip-hint'
import { api, demoMode } from '../../lib/api'
import { cn } from '../../lib/utils'
import type { DockerNode } from '../../lib/nodes'
import { useDateTime } from '../../lib/time-zone'
import { Check, ErrorText } from './common'
import { useOpsText, newAIRequestID } from './helpers'
import type { AIConversationDetail, AIConversationRecord, AIInteraction, AIOperation, AIRun, AISettings } from './types'

export function AIConversation({ selected, onSelect, runID, node, model, setModel, resourceKind, resourceID, operations, renderOperation, question, setQuestion }: {
 selected: string; onSelect: (id: string) => void; runID: string; node: string; model: string; setModel: (value: string) => void; resourceKind: string; resourceID: string; operations: AIOperation[]; renderOperation: (op: AIOperation) => ReactNode; question: string; setQuestion: (value: string) => void
}) {
 const t = useOpsText(), client = useQueryClient(), { formatDateTime } = useDateTime()
 const [historyOpen, setHistoryOpen] = useState(false), conversationVersion = useRef(0), cursors = useRef(new Map<string, number>())
 const settings = useQuery({ queryKey: ['ai-settings'], queryFn: () => api<AISettings>('/ai/settings') })
 const conversations = useQuery({ queryKey: ['ai-conversations'], queryFn: () => api<AIConversationRecord[]>('/ai/conversations'), refetchInterval: 5000 })
 const current = useQuery({ queryKey: ['ai-conversation', selected], queryFn: () => api<AIConversationDetail>(`/ai/conversations/${encodeURIComponent(selected)}`), enabled: !!selected, refetchInterval: query => ['queued', 'running', 'waiting_task', 'waiting_input', 'waiting_approval'].includes(query.state.data?.current_run?.status || '') ? 2000 : false })
 const linked = useQuery({ queryKey: ['ai-run', runID], queryFn: () => api<AIRun>(`/ai/runs/${encodeURIComponent(runID)}`), enabled: !!runID })
 useEffect(() => { if (linked.data?.conversation_id) onSelect(linked.data.conversation_id) }, [linked.data?.conversation_id, onSelect])
 useEffect(() => {
  if (!selected || demoMode) return
  let stopped = false, socket: WebSocket | undefined, retry: ReturnType<typeof setTimeout> | undefined
  const connect = () => {
   if (stopped) return
   const url = new URL(`/ws/ai/conversations/${encodeURIComponent(selected)}`, window.location.href); url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'; url.searchParams.set('after', String(cursors.current.get(selected) || 0))
   socket = new WebSocket(url)
   socket.onmessage = event => {
    try { const value: { seq?: number } = JSON.parse(event.data); if (!value.seq || value.seq <= (cursors.current.get(selected) || 0)) return; cursors.current.set(selected, value.seq) } catch { return }
    void client.invalidateQueries({ queryKey: ['ai-conversation', selected] }); void client.invalidateQueries({ queryKey: ['ai-operations'] }); void client.invalidateQueries({ queryKey: ['tasks'] })
   }
   socket.onclose = () => { if (!stopped) retry = setTimeout(connect, 2000) }
  }
  connect(); return () => { stopped = true; clearTimeout(retry); socket?.close() }
 }, [selected, client])
 const nodes = useQuery({ queryKey: ['nodes'], queryFn: () => api<DockerNode[]>('/nodes') })
 const turns = current.data?.runs || [], run = current.data?.current_run, models = settings.data?.models || [], activeModel = model || run?.model || settings.data?.model || ''
 const nodeLabel = (id: string) => nodes.data?.find(item => item.id === id)?.name || id
 const targetLabel = run?.target_node_ids?.length ? run.target_node_ids.map(nodeLabel).join(', ') : t('待确定目标', 'Target not yet selected')
 const sourceLabel = ({ resource: t('资源入口', 'Resource context'), conversation: t('当前任务', 'Current task'), explicit: t('本轮指定', 'This request'), selection: t('已确认选择', 'Confirmed selection') } as Record<string, string>)[run?.target_source || ''] || ''
 const start = useMutation({
  mutationFn: async ({ text, requestID }: { text: string; requestID: string }) => {
   const conv = selected ? { id: selected } : await api<AIConversationDetail>('/ai/conversations', { method: 'POST', body: '{}' })
   const context = !selected && resourceKind && resourceID && node ? { resource_node_id: node, resource_kind: resourceKind, resource_id: resourceID } : undefined
   const row = await api<AIRun>(`/ai/conversations/${conv.id}/messages`, { method: 'POST', body: JSON.stringify({ question: text, model: activeModel, request_id: requestID, expected_revision: current.data?.revision, context }) })
   return { row, conversation: conv.id }
  },
  onMutate: () => conversationVersion.current,
  onSuccess: ({ row, conversation }, _input, version) => {
   client.setQueryData(['ai-run', row.id], row)
   if (version === conversationVersion.current) { onSelect(conversation); setQuestion('') }
   void client.invalidateQueries({ queryKey: ['ai-conversations'] }); void client.invalidateQueries({ queryKey: ['ai-conversation', conversation] }); void client.invalidateQueries({ queryKey: ['ai-operations'] })
  },
 })
 const cancel = useMutation({ mutationFn: () => api(`/ai/runs/${run?.id}/cancel`, { method: 'POST' }), onSuccess: () => { void client.invalidateQueries({ queryKey: ['ai-conversation', selected] }); void client.invalidateQueries({ queryKey: ['ai-operations'] }) } })
 const canSend = !!question.trim() && !start.isPending && !!settings.data?.enabled && !!settings.data.node_ids.length && models.includes(activeModel) && (!selected || !!current.data)
 function choose(id: string) { conversationVersion.current++; onSelect(id); setModel(''); setQuestion(''); setHistoryOpen(false); start.reset() }
 const history = <><Button variant="ghost" size="sm" className="w-full justify-start" onClick={() => choose('')}><Plus />{t('新诊断', 'New diagnosis')}</Button><div className="mt-4 flex-1 space-y-1 overflow-y-auto overscroll-contain">{conversations.data?.map(row => <button key={row.id} type="button" aria-pressed={selected === row.id} onClick={() => choose(row.id)} className={cn('block w-full rounded-md px-3 py-2.5 text-left hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring', selected === row.id && 'bg-muted')}><span className="line-clamp-2 break-words text-sm">{row.title || t('新会话', 'New conversation')}</span><span className="mt-1 block truncate text-xs text-muted-foreground">{formatDateTime(row.updated_at)}</span></button>)}{!conversations.data?.length && <p className="px-3 py-4 text-xs text-muted-foreground">{t('诊断会话会保存在这里', 'Your conversations will appear here')}</p>}</div></>
 return <div className="grid min-w-0 gap-6 lg:grid-cols-[220px_minmax(0,1fr)]">
  <aside aria-label={t('会话历史', 'Conversation history')} className="hidden h-[calc(100dvh-15.5rem)] min-h-96 flex-col rounded-lg bg-muted/30 p-3 lg:flex">{history}</aside>
  <div className="flex h-[calc(100dvh-15.5rem)] min-h-96 min-w-0 flex-col">
   <header className="flex min-w-0 items-center gap-2 pb-3"><MessageSquare className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" /><div className="min-w-0 flex-1"><h3 className="truncate text-sm font-medium">{current.data?.title || t('新会话', 'New conversation')}</h3><p className="mt-0.5 truncate text-xs text-muted-foreground">{targetLabel}{sourceLabel && ` · ${sourceLabel}`}</p></div><TooltipHint content={t('会话历史', 'Conversation history')}><Button size="icon-sm" variant="ghost" className="lg:hidden" aria-label={t('会话历史', 'Conversation history')} onClick={() => setHistoryOpen(true)}><History /></Button></TooltipHint><TooltipHint content={t('新诊断', 'New diagnosis')}><Button size="icon-sm" variant="ghost" className="lg:hidden" aria-label={t('新诊断', 'New diagnosis')} onClick={() => choose('')}><Plus /></Button></TooltipHint></header>
   <ErrorText error={settings.error || conversations.error || current.error || linked.error || start.error || cancel.error} />
   <MessageScrollerProvider key={selected || 'new'} autoScroll defaultScrollPosition="last-anchor"><MessageScroller className="flex-1"><MessageScrollerViewport aria-label={t('诊断对话', 'Diagnosis conversation')}><MessageScrollerContent className="gap-6 px-1 py-5 sm:px-3">
    {!turns.length && !start.isPending && <MessageScrollerItem messageId="welcome" className="flex flex-1 flex-col items-center justify-center gap-3 py-10 text-center"><div className="flex size-10 items-center justify-center rounded-lg bg-muted"><Sparkles className="size-5 text-muted-foreground" /></div><div><h4 className="text-sm font-medium">{t('从一个运维问题开始', 'Start with an operations question')}</h4><p className="mt-2 max-w-sm text-xs leading-6 text-muted-foreground">{t('描述任务即可。目标不明确时，AI 会询问节点或资源；变更逐项审核。', 'Describe the task. AI asks for unclear targets; each change needs review.')}</p></div></MessageScrollerItem>}
    {turns.map(turn => <Fragment key={turn.id}>
     <MessageScrollerItem messageId={`${turn.id}-user`} scrollAnchor><Message align="end"><MessageContent><Bubble variant="secondary"><BubbleContent className="whitespace-pre-wrap break-words rounded-lg">{turn.question}</BubbleContent></Bubble></MessageContent></Message></MessageScrollerItem>
     <MessageScrollerItem messageId={`${turn.id}-assistant`}><Message><MessageContent><Bubble variant="outline"><BubbleContent className="space-y-4 rounded-lg">
      {['queued', 'running'].includes(turn.status) && <p role="status" className="flex items-center gap-2 text-sm text-muted-foreground"><Spinner aria-hidden="true" />{t('正在分析任务…', 'Analyzing the task…')}</p>}
      {turn.error && <p role="alert" className="text-sm text-destructive">{turn.error}</p>}
      {current.data?.messages.filter(message => message.run_id === turn.id && message.role === 'interaction').map(message => <p key={message.id} className="whitespace-pre-wrap break-words text-xs text-muted-foreground">{t('已补充信息', 'Input recorded')}: {message.content}</p>)}
      {turn.result.summary && <Markdown>{turn.result.summary}</Markdown>}
      {turn.steps?.length > 0 && <ol aria-label={t('任务计划', 'Task plan')} className="divide-y rounded-md border px-3">{turn.steps.map(step => <li key={step.id} className="flex gap-3 py-2 text-xs"><span className="text-muted-foreground">{step.position}</span><div className="min-w-0 flex-1"><p className="break-words">{step.title}</p><p className="mt-1 break-all text-muted-foreground">{nodeLabel(step.node_id)} · {step.status}</p>{step.expected && <p className="mt-1 text-muted-foreground">{step.expected}</p>}</div></li>)}</ol>}
      {turn.status === 'waiting_input' && turn.interaction && <InteractionInput key={turn.interaction.id} run={turn} input={turn.interaction} conversationID={selected} />}
      {turn.status === 'waiting_task' && <p role="status" className="flex items-center gap-2 text-sm text-muted-foreground"><Spinner aria-hidden="true" />{turn.phase === 'evidence' ? t('正在检测镜像并等待结果', 'Checking image metadata') : t('已批准的步骤正在执行并验证；新消息会调整后续计划。', 'The approved step is executing and verifying. A new message adjusts the remaining plan.')} · {turn.task_progress || 0}%</p>}
      {turn.result.evidence.length > 0 && <Collapsible className="space-y-1"><CollapsibleTrigger render={<Button variant="ghost" size="sm" className="group h-8 px-2 text-xs text-muted-foreground" />}><BookOpen className="size-3.5" />{t('诊断证据', 'Evidence')} · {turn.result.evidence.length}<ChevronDown className="size-3.5" /></CollapsibleTrigger><CollapsibleContent className="space-y-3 rounded-lg bg-muted/40 p-3">{turn.result.evidence.map((e, i) => <div key={i} className="space-y-2"><p className="break-all text-xs text-muted-foreground">{e.node_id && `${nodeLabel(e.node_id)} · `}{e.source} · {e.resource} · {formatDateTime(e.time)}{e.unavailable && ` · ${t('不可用', 'Unavailable')}`}</p><pre className="max-h-48 overflow-auto whitespace-pre-wrap break-all rounded-md bg-background/60 p-3 text-xs">{e.content}</pre></div>)}</CollapsibleContent></Collapsible>}
      {operations.some(op => op.run_id === turn.id) && <section aria-label={t('建议操作', 'Suggested operations')} className="space-y-2"><p className="text-xs font-medium text-muted-foreground">{t('建议操作 · 逐项审核', 'Suggested actions · individual review')}</p><div className="space-y-1 rounded-lg bg-muted/40 p-1">{operations.filter(op => op.run_id === turn.id).map(op => <Fragment key={op.id}>{renderOperation(op)}</Fragment>)}</div></section>}
      {turn.result.missing.length > 0 && <p className="text-xs text-muted-foreground">{t('缺少信息', 'Missing information')}: {turn.result.missing.join(', ')}</p>}
     </BubbleContent></Bubble><MessageFooter className="gap-2 text-[11px]">{turn.status} · {turn.model} · {turn.target_node_ids?.map(nodeLabel).join(', ') || t('待确定目标', 'Target not yet selected')} · {turn.tokens} tokens</MessageFooter></MessageContent></Message></MessageScrollerItem>
    </Fragment>)}
    {start.isPending && <MessageScrollerItem messageId="pending-user" scrollAnchor><Message align="end"><MessageContent><Bubble variant="secondary"><BubbleContent className="whitespace-pre-wrap">{start.variables?.text}</BubbleContent></Bubble></MessageContent></Message></MessageScrollerItem>}
   </MessageScrollerContent></MessageScrollerViewport><MessageScrollerButton aria-label={t('回到最新回复', 'Jump to latest reply')}><ArrowDown /></MessageScrollerButton></MessageScroller></MessageScrollerProvider>
   <form className="shrink-0 pt-3" aria-label={t('发送诊断请求', 'Send diagnosis request')} onSubmit={e => { e.preventDefault(); if (canSend) start.mutate({ text: question.trim(), requestID: newAIRequestID() }) }}>
    <div className="mb-2 flex items-center justify-between gap-2 text-xs text-muted-foreground"><span className="truncate">{targetLabel}{sourceLabel && ` · ${sourceLabel}`}</span>{run && !['completed', 'failed', 'canceled'].includes(run.status) && <Button size="sm" variant="ghost" disabled={cancel.isPending} onClick={() => cancel.mutate()}>{t('取消任务', 'Cancel task')}</Button>}</div>
    <InputGroup className="bg-background"><InputGroupTextarea aria-label={t('分析请求', 'Diagnosis request')} aria-describedby="ai-composer-hint" className="min-h-20 max-h-40 px-3 py-3 field-sizing-content" maxLength={4000} value={question} disabled={start.isPending} onChange={e => setQuestion(e.target.value)} placeholder={t('描述任务，或调整当前要求…', 'Describe a task or adjust the current request…')} onKeyDown={e => { if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing && e.keyCode !== 229) { e.preventDefault(); e.currentTarget.form?.requestSubmit() } }} /><InputGroupAddon align="block-end" className="justify-between gap-2"><span className="min-w-0 flex-1 truncate text-xs font-normal text-muted-foreground">{!selected && resourceID ? `${resourceKind} · ${resourceID}` : t('变更操作需要单独审核', 'Changes require individual review')}</span>{models.length > 0 && <Select items={models.map(value => ({ value, label: value }))} value={activeModel} onValueChange={value => { if (value) setModel(value) }} disabled={start.isPending}><SelectTrigger aria-label={t('对话模型', 'Conversation model')} className="min-w-0 max-w-40 border-0 px-2 text-xs dark:bg-transparent sm:max-w-64"><SelectValue className="min-w-0 truncate" /></SelectTrigger><SelectContent side="top" align="end" alignItemWithTrigger={false}>{models.map(value => <SelectItem key={value} value={value}>{value}</SelectItem>)}</SelectContent></Select>}
     <TooltipHint content={t('发起诊断', 'Start diagnosis')}><InputGroupButton type="submit" variant="default" size="icon-sm" aria-label={t('发起诊断', 'Start diagnosis')} disabled={!canSend}>{start.isPending ? <Spinner aria-hidden="true" /> : <ArrowUp />}</InputGroupButton></TooltipHint>
    </InputGroupAddon></InputGroup><p id="ai-composer-hint" className="mt-2 text-[11px] text-muted-foreground">{!settings.data?.enabled ? t('请在 AI 设置中连接模型并授权节点。', 'Connect a model and authorize nodes in AI settings.') : t('Enter 发送，Shift + Enter 换行。等待选择或审核时可发送新要求。', 'Enter to send, Shift + Enter for a new line. You can adjust a task while waiting for input or review.')}</p>
   </form>
  </div>
  <Sheet open={historyOpen} onOpenChange={setHistoryOpen}><SheetContent side="left" className="w-80 max-w-[85vw]"><SheetHeader><SheetTitle>{t('会话历史', 'Conversation history')}</SheetTitle><SheetDescription>{t('打开已有会话，或发起新的诊断。', 'Open a conversation or start a new diagnosis.')}</SheetDescription></SheetHeader><div className="flex min-h-0 flex-1 flex-col px-4 pb-4">{history}</div></SheetContent></Sheet>
 </div>
}
function InteractionInput({ run, input, conversationID }: { run: AIRun; input: AIInteraction; conversationID: string }) {
 const t = useOpsText(), client = useQueryClient(), [values, setValues] = useState<string[]>([]), [text, setText] = useState('')
 const answer = useMutation({ mutationFn: ({ requestID }: { requestID: string }) => api(`/ai/runs/${run.id}/inputs`, { method: 'POST', body: JSON.stringify({ interaction_id: input.id, expected_revision: input.revision, request_id: requestID, values, text }) }), onSuccess: () => { void client.invalidateQueries({ queryKey: ['ai-conversation', conversationID] }) } })
 return <form aria-label={t('补充任务信息', 'Provide task input')} className="space-y-3 rounded-md border p-3" onSubmit={e => { e.preventDefault(); answer.mutate({ requestID: newAIRequestID() }) }}><p className="text-sm">{input.prompt}</p><ErrorText error={answer.error} />{input.options.length ? <div className="max-h-56 space-y-2 overflow-y-auto">{input.options.map(option => <Check key={option.id} label={`${option.name} · ${option.detail || option.id}`} checked={values.includes(option.id)} onChange={checked => setValues(input.multiple ? checked ? [...values, option.id] : values.filter(value => value !== option.id) : checked ? [option.id] : [])} />)}</div> : <Input aria-label={t('补充参数', 'Missing parameter')} value={text} maxLength={4000} onChange={e => setText(e.target.value)} />}<Button size="sm" type="submit" disabled={answer.isPending || (input.options.length ? !values.length : !text.trim()) || new Date(input.expires_at).getTime() <= Date.now()}>{answer.isPending && <Spinner />}{t('确认并继续', 'Confirm and continue')}</Button></form>
}
