import {gitOpsPath,type ProjectCDConfiguration,type ProjectCDPreview,type ProjectCDRecord} from './queries'
import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useBlocker } from '@tanstack/react-router'
import { Button } from '../../components/ui/button'
import { Input } from '../../components/ui/input'
import { Checkbox } from '../../components/ui/checkbox'
import { Switch } from '../../components/ui/switch'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../../components/ui/select'
import { InlineStatus } from '../../components/ui/inline-status'
import { Textarea } from '../../components/ui/textarea'
import { api } from '../../lib/api'
import { nodePath } from '../../lib/nodes'
import { registerProjectNavigationGuard } from '../../lib/project-navigation-guard'
import { confirmDialog } from '../../stores/dialog'
import { CredentialSelector } from '../delivery/settings'
import { GitConnectionTest } from '../delivery/connection-test'
import { EnvironmentFields } from '../delivery/environment-fields'
import { parseEnvironmentInput } from '../delivery/environment-input'
import type { CDEnvironmentVariable, GitRepositoryConfiguration, GitRefType } from '../delivery/types'
import { maskEnvironment, maskCompose } from '../compose/document'

function Field({label,children}:{label:string;children:ReactNode}){return <label className="flex min-w-0 flex-col gap-1.5 text-sm"><span className="min-w-0 whitespace-normal break-words">{label}</span>{children}</label>}
function useDraftGuard(dirty:boolean,zh:boolean){
 useBlocker({disabled:!dirty,enableBeforeUnload:dirty,shouldBlockFn:async()=>!(await confirmDialog({title:zh?'放弃未保存的 CD 配置？':'Discard unsaved CD settings?',danger:true}))})
 useEffect(()=>registerProjectNavigationGuard(async()=>!dirty || await confirmDialog({title:zh?'放弃未保存的 CD 配置并切换节点？':'Discard CD settings and switch node?',danger:true})),[dirty,zh])
}

export function ProjectCD({nodeID,projectName,configuration,initialEnvironment,baselineCompose,zh,onDirtyChange}:{onDirtyChange:(value:boolean)=>void;nodeID:string;projectName:string;configuration:ProjectCDConfiguration;initialEnvironment:string;baselineCompose:string;zh:boolean}){
 const path=gitOpsPath(nodeID,projectName),client=useQueryClient()
 const [draft,setDraft]=useState(configuration),[reviewed,setReviewed]=useState(false),[imported,setImported]=useState<CDEnvironmentVariable[]>([]),[secret,setSecret]=useState(''),[taskID,setTaskID]=useState(''),[page,setPage]=useState(1),[review,setReview]=useState(false),[showBefore,setShowBefore]=useState(false),[importError,setImportIssue]=useState('')
 const [baseline,setBaseline]=useState(JSON.stringify(configuration))
 const dirty=JSON.stringify(draft)!==baseline || imported.length>0
 useDraftGuard(dirty,zh)
 useEffect(()=>{onDirtyChange(dirty);return ()=>onDirtyChange(false)},[dirty,onDirtyChange])
 useEffect(()=>{if(!dirty){setDraft(configuration);setBaseline(JSON.stringify(configuration))}},[configuration,dirty])
 const preview=useQuery({queryKey:['project-gitops-review',nodeID,projectName,configuration.candidate?.id],queryFn:()=>api<ProjectCDPreview>(`${path}/preview`),enabled:!!configuration.candidate})
 const drift=useQuery({queryKey:['project-cd-drift',nodeID,projectName],queryFn:()=>api<{status:string;reason:string}>(`${path}/drift`),enabled:configuration.enabled && !!configuration.applied,refetchInterval:10000})
 const records=useQuery({queryKey:['project-gitops-records',nodeID,projectName,page],queryFn:()=>api<{items:ProjectCDRecord[];total:number}>(`${path}/records?page=${page}`),enabled:configuration.configured,refetchInterval:configuration.enabled?3000:false})
 const activeTaskID=records.data?.items.find(record=>record.status==='running')?.task_id || taskID
 const task=useQuery({queryKey:['gitops-task',nodeID,activeTaskID],queryFn:()=>api<{status:string;message:string;progress:number}>(nodePath(nodeID,`/tasks/${activeTaskID}`)),enabled:!!activeTaskID,refetchInterval:query=>['success','failed','canceled'].includes(query.state.data?.status ?? '')?false:1000})
 const refresh=useCallback(async()=>{await Promise.all([client.invalidateQueries({queryKey:['project-gitops',nodeID,projectName]}),client.invalidateQueries({queryKey:['project',nodeID,'compose',projectName]}),client.invalidateQueries({queryKey:['projects',nodeID]}),client.invalidateQueries({queryKey:['project-gitops-records',nodeID,projectName]})])},[client,nodeID,projectName])
 const taskStatus=task.data?.status
 useEffect(()=>{if(taskStatus && ['success','failed','canceled'].includes(taskStatus)){void refresh()}},[taskStatus,refresh])
 const action=useMutation({mutationFn:async({route,body,method='POST'}:{route:string;body?:unknown;method?:string})=>{
  const result=await api<{id?:string}>(path+route,{method,body:body===undefined?undefined:JSON.stringify(body)});if(result.id)setTaskID(result.id);await refresh();return result
 }})
 const save=useMutation({mutationFn:()=>api<ProjectCDConfiguration>(path,{method:'PUT',body:JSON.stringify({...draft,expected_revision:draft.revision,reviewed_environment:reviewed,environment:!configuration.enabled?imported:undefined})}),onSuccess:async value=>{setDraft(value);setBaseline(JSON.stringify(value));setImported([]);if(value.webhook_secret)setSecret(value.webhook_secret);await refresh()}})
 const busy=action.isPending || save.isPending || (!!activeTaskID && (!task.data || !['success','failed','canceled'].includes(task.data.status)))
 const updateRepo=<K extends keyof GitRepositoryConfiguration>(key:K,value:GitRepositoryConfiguration[K])=>setDraft(current=>({...current,repository:{...current.repository,[key]:value}}))
 const rollback=async(record:ProjectCDRecord)=>{if(await confirmDialog({title:zh?'回滚到此成功配置？':'Restore this successful configuration?',description:zh?'自动同步会暂停；重新拉取镜像并重建，数据卷保留。可变镜像标签仍使用仓库当前内容。':'Automation will pause. Images are pulled and containers recreated; volumes are retained. Mutable tags use their current registry contents.',danger:true})){action.mutate({route:`/records/${record.id}/rollback`,body:{expected_revision:configuration.revision}})}}
 return <div className="w-full min-w-0 space-y-4" data-testid="project-gitops">
  <section className="section-surface space-y-3">
   <div className="flex flex-wrap items-center justify-between gap-2"><div><h3 className="text-sm font-semibold">{zh?'项目 CD · 单节点交付':'Project CD · Single-node delivery'}</h3><p className="text-xs text-muted-foreground">{projectName} · {zh?'仅管理当前节点项目':'This node project only'}</p></div><span className="text-xs">{!configuration.enabled?(zh?'手动模式':'Manual mode'):configuration.initial_application_required?(zh?'待首次应用':'First application required'):(zh?'CD 部署模式':'CD deployment mode')}</span></div>
   {drift.data && <p className="text-xs text-muted-foreground">{drift.data.status==='healthy'?(zh?'已应用配置指纹与运行健康一致':'Applied configuration hash and runtime health match'):drift.data.status==='drifted'?(zh?'检测到运行偏离，请检查节点；不会自动修复。':'Runtime drift detected; inspect the node. Automatic repair is disabled.'):(zh?'运行状态尚未确认':'Runtime state is unconfirmed')}</p>}
   {drift.error&&<InlineStatus tone="error">{drift.error.message}</InlineStatus>}
   {configuration.enabled && <><div className="flex flex-wrap gap-x-6 gap-y-1 font-mono text-xs"><span>{zh?'候选':'Candidate'} {configuration.candidate?.commit_sha.slice(0,8)||'—'}</span><span>{zh?'生效':'Applied'} {configuration.applied?.commit_sha.slice(0,8)||'—'}</span></div><div className="flex flex-wrap items-center gap-2">
    <Button variant="outline" size="sm" disabled={busy||dirty} onClick={()=>action.mutate({route:'/check'})}>{zh?'检查更新':'Check updates'}</Button>
    <Button size="sm" disabled={busy||dirty||configuration.candidate?.status!=='succeeded'||configuration.candidate.revision!==configuration.revision} onClick={()=>setReview(true)}>{zh?'审阅并应用':'Review and apply'}</Button>
    <label className="ml-auto flex items-center gap-2 text-xs"><Switch aria-label={zh?'自动同步并应用':'Automatically sync and apply'} checked={configuration.automation_enabled} disabled={busy||dirty||configuration.initial_application_required} onCheckedChange={enabled=>action.mutate({route:'/automation',method:'PUT',body:{enabled,expected_revision:configuration.revision}})} />{zh?'自动同步并应用':'Automatically sync and apply'}</label>
    <Button variant="ghost" size="sm" disabled={busy||dirty} onClick={async()=>{if(await confirmDialog({title:zh?'切回手动模式？':'Return to manual mode?',description:zh?'导出最近成功配置，未应用草稿不保留。运行容器不变，原镜像自动更新策略将恢复。':'Exports the last successful configuration and discards unapplied drafts. Containers keep running; the original image update policy resumes.',danger:true})){action.mutate({route:'/detach',body:{expected_revision:configuration.revision}})}}}>{zh?'切回手动模式':'Return to manual'}</Button>
   </div></>}
   {task.data && <div className="rounded-md bg-muted/40 p-3 text-xs" role="status">{task.data.status} · {task.data.progress}% · {task.data.message}<a className="ml-2 underline" href={`/tasks#${encodeURIComponent(activeTaskID)}`}>{zh?'任务日志':'Task logs'}</a></div>}
   {preview.error && <InlineStatus tone="error">{preview.error.message}</InlineStatus>}
   {(action.error||save.error) && <InlineStatus tone="error">{(action.error||save.error)?.message}</InlineStatus>}
  </section>
  {review && preview.data && <section className="section-surface space-y-3" role="region" aria-label={zh?'应用确认':'Application review'}><h3 className="text-sm font-semibold">{zh?'确认应用固定快照':'Confirm the frozen snapshot'} · {preview.data.commit_sha.slice(0,8)}</h3><p className="text-xs text-muted-foreground">{nodeID} / {projectName} · {preview.data.files.map(file=>file.path).join(' → ')}</p><Button variant="ghost" size="sm" onClick={()=>setShowBefore(!showBefore)}>{showBefore?(zh?'隐藏当前配置':'Hide current configuration'):(zh?'比较当前配置':'Compare current configuration')}</Button>{showBefore&&<pre className="max-h-64 overflow-auto rounded-md bg-muted/30 p-3 text-xs">{maskCompose(baselineCompose)}</pre>}<pre className="max-h-72 overflow-auto rounded-md bg-muted/30 p-3 text-xs">{maskCompose(preview.data.compose)}</pre><p className="text-xs text-muted-foreground">{zh?'配置可能重建服务或清理已移除的服务容器，数据卷保留。':'Applying may recreate services or remove orphan containers. Data volumes are retained.'}</p><div className="flex gap-2"><Button disabled={busy} onClick={()=>{action.mutate({route:'/apply',body:{candidate_id:preview.data.record_id,expected_revision:configuration.revision}});setReview(false)}}>{zh?'确认应用':'Confirm application'}</Button><Button variant="ghost" onClick={()=>setReview(false)}>{zh?'取消':'Cancel'}</Button></div></section>}
  <form onSubmit={event=>{event.preventDefault();save.mutate()}} className="section-surface min-w-0 space-y-4">
   <h3 className="text-sm font-semibold">{zh?'Git 来源与部署设置':'Git source and deployment settings'}</h3>
   <fieldset disabled={busy} className="min-w-0 space-y-4">
    <Field label="Git Clone URL"><Input required value={draft.repository.clone_url} onChange={event=>updateRepo('clone_url',event.target.value)} placeholder="https://git.example.com/team/app.git" /></Field>
    <div className="grid gap-3 sm:grid-cols-2"><Field label={zh?'引用类型':'Ref type'}><Select value={draft.repository.ref_type} items={['branch','tag','commit'].map(value=>({value,label:value}))} onValueChange={value=>updateRepo('ref_type',value as GitRefType)}><SelectTrigger className="w-full"><SelectValue /></SelectTrigger><SelectContent>{['branch','tag','commit'].map(value=><SelectItem key={value} value={value}>{value}</SelectItem>)}</SelectContent></Select></Field><Field label="Branch / Tag / Commit"><Input required value={draft.repository.ref} onChange={event=>updateRepo('ref',event.target.value)} /></Field></div>
    <Field label={zh?'Compose 文件（按顺序，每行一个）':'Compose files (ordered, one per line)'}><Textarea required value={draft.repository.compose_files.join('\n')} onChange={event=>updateRepo('compose_files',event.target.value.split('\n'))} /></Field>
    <Field label={zh?'环境文件（相对仓库根目录，可选）':'Environment file (relative to repository root, optional)'}><Input value={draft.repository.environment_file||''} onChange={event=>updateRepo('environment_file',event.target.value)} /></Field>
    <CredentialSelector value={draft.repository.authentication} onChange={value=>updateRepo('authentication',value)} nodeIDs={[nodeID]} zh={zh} />
    <GitConnectionTest repository={draft.repository} nodeIDs={[nodeID]} projectName={projectName} creating={false} zh={zh} endpoint={`${path}/test-connection`} />
    <RegistrySelection nodeID={nodeID} selected={draft.registry_credential_ids} onChange={registry_credential_ids=>setDraft(current=>({...current,registry_credential_ids}))} zh={zh}/>
    <div className="grid gap-3 sm:grid-cols-2"><Field label={zh?'轮询间隔（秒）':'Polling interval (seconds)'}><Input type="number" min={30} max={86400} value={draft.sync_interval_seconds} onChange={event=>setDraft(current=>({...current,sync_interval_seconds:Number(event.target.value)}))}/></Field><Field label={zh?'部署超时（秒）':'Deployment timeout (seconds)'}><Input type="number" min={10} max={3600} value={draft.deployment_timeout} onChange={event=>setDraft(current=>({...current,deployment_timeout:Number(event.target.value)}))}/></Field></div>
    <label className="flex items-center gap-2 text-sm"><Checkbox checked={draft.webhook_enabled} onCheckedChange={value=>setDraft(current=>({...current,webhook_enabled:value===true}))}/>Webhook</label>
    {configuration.webhook_id && draft.webhook_enabled && <Field label="Webhook URL"><Input readOnly value={`${location.origin}/api/v1/webhooks/gitops/${configuration.webhook_id}`}/></Field>}
    {!configuration.enabled && initialEnvironment && <div className="space-y-2 rounded-lg bg-muted/40 p-3"><p className="text-xs">{zh?'启用前请审阅并导入原 .env；复杂表达式需手动整理，不能静默忽略。':'Review and import the original .env. Resolve complex expressions explicitly before enabling.'}</p><Button type="button" size="sm" variant="outline" onClick={()=>{try{if(initialEnvironment.includes('$') || initialEnvironment.includes('\\'))throw new Error(zh?'原 .env 含插值或转义，请先在 .env 中整理为实际值。':'Resolve interpolation and escapes in the original .env before importing.');const variables=parseEnvironmentInput(initialEnvironment,zh);setImported(variables);setReviewed(true)}catch(error){save.reset();action.reset();setImportError(error instanceof Error?error.message:String(error))}}}>{zh?'审阅并导入原 .env':'Review and import original .env'}</Button><p className="text-xs">{imported.map(row=>row.name).join(' · ')}</p>{importError&&<InlineStatus tone="error">{importError}</InlineStatus>}</div>}
    <p className="text-xs text-muted-foreground">{zh?'仅支持预构建镜像，沿用持续交付安全限制。首次应用或 Git 来源变化需人工确认；保存本身不部署。':'Prebuilt images only, with CD deployment restrictions. First applications and source changes require confirmation. Saving does not deploy.'}</p>
    <Button type="submit" disabled={busy||(!configuration.enabled&&!!initialEnvironment&&!reviewed)}>{configuration.enabled?(zh?'保存 CD 配置':'Save CD settings'):(zh?'关联仓库并启用 CD 模式':'Connect repository and enable CD mode')}</Button>
   </fieldset>
  </form>
  {secret && <section className="section-surface space-y-2"><p className="text-sm">{zh?'Webhook Secret 仅显示一次':'Webhook secret is shown once'}</p><Input type="password" readOnly value={secret}/><Button size="sm" onClick={async()=>{await navigator.clipboard.writeText(secret);setSecret('')}}>{zh?'复制并隐藏':'Copy and hide'}</Button></section>}
  {configuration.configured && <section className="section-surface space-y-3"><h3 className="text-sm font-semibold">{zh?'同步记录':'Synchronization records'}</h3>{records.data?.items.map(record=><details key={record.id} className="rounded-md bg-muted/30 p-3 text-xs"><summary className="flex cursor-pointer flex-wrap items-center gap-2"><span>{record.operation} · {record.status}</span><span className="font-mono">{record.commit_sha.slice(0,8)}</span><span>{record.actor}</span><time className="ml-auto">{new Date(record.created_at).toLocaleString()}</time></summary><div className="mt-3 space-y-2 break-all"><p>{record.commit_message}</p><p className="font-mono">{record.config_hash}</p>{record.failure_reason&&<InlineStatus tone="error">{record.failure_reason}</InlineStatus>}<a className="underline" href={`/tasks#${encodeURIComponent(record.task_id)}`}>{zh?'任务日志':'Task logs'}</a>{record.status==='succeeded'&&['apply','rollback'].includes(record.operation)&&configuration.enabled&&<Button size="sm" variant="ghost" disabled={busy||dirty} onClick={()=>void rollback(record)}>{zh?'回滚到此配置':'Restore this configuration'}</Button>}</div></details>)}{records.error&&<InlineStatus tone="error">{records.error.message}</InlineStatus>}<div className="flex items-center gap-2"><Button size="sm" variant="ghost" disabled={page===1} onClick={()=>setPage(value=>value-1)}>{zh?'上一页':'Previous'}</Button><span className="text-xs">{page}</span><Button size="sm" variant="ghost" disabled={page*20>=(records.data?.total??0)} onClick={()=>setPage(value=>value+1)}>{zh?'下一页':'Next'}</Button></div></section>}
 </div>
 function setImportError(message:string){setImportIssue(message)}
}

function RegistrySelection({nodeID,selected,onChange,zh}:{nodeID:string;selected:number[];onChange:(value:number[])=>void;zh:boolean}){
 const query=useQuery({queryKey:['registry-credentials'],queryFn:()=>api<{id:number;name:string;authorized_node_ids:string[]}[]>('/credentials/registries')})
 return <div className="space-y-2"><p className="text-sm">{zh?'镜像仓库凭据':'Registry credentials'}</p>{query.error&&<InlineStatus tone="error">{query.error.message}</InlineStatus>}{query.data?.filter(row=>row.authorized_node_ids?.includes(nodeID)||selected.includes(row.id)).map(row=><label key={row.id} className="flex items-center gap-2 text-sm"><Checkbox checked={selected.includes(row.id)} onCheckedChange={checked=>onChange(checked?[...selected,row.id]:selected.filter(id=>id!==row.id))}/>{row.name}</label>)}</div>
}

export function ProjectCDEnvironment({nodeID,projectName,configuration,repositoryEnvironment,zh,onDirtyChange}:{onDirtyChange:(value:boolean)=>void;nodeID:string;projectName:string;configuration:ProjectCDConfiguration;repositoryEnvironment:string;zh:boolean}){
 const path=gitOpsPath(nodeID,projectName),client=useQueryClient()
 const [variables,setVariables]=useState(configuration.environment),[baseline,setBaseline]=useState(JSON.stringify(configuration.environment)),[expectedRevision,setExpectedRevision]=useState(configuration.revision)
 const [values,setValues]=useState<Record<string,string>|null>(null),[revealed,setRevealed]=useState(false),[showRepository,setShowRepository]=useState(false),[error,setError]=useState(''),[loading,setLoading]=useState(false)
 const request=useRef<AbortController|null>(null)
 const dirty=JSON.stringify(variables)!==baseline
 useDraftGuard(dirty,zh)
 useEffect(()=>{onDirtyChange(dirty);return ()=>onDirtyChange(false)},[dirty,onDirtyChange])
 useEffect(()=>{if(!dirty){setVariables(configuration.environment);setBaseline(JSON.stringify(configuration.environment));setExpectedRevision(configuration.revision)}},[configuration.environment,configuration.revision,dirty])
 useEffect(()=>{request.current?.abort();setValues(null);setRevealed(false);setLoading(false);return ()=>request.current?.abort()},[configuration.revision,nodeID,projectName])
 const save=useMutation({mutationFn:()=>api<ProjectCDConfiguration>(`${path}/environment`,{method:'PUT',body:JSON.stringify({expected_revision:expectedRevision,environment:variables})}),onSuccess:async updated=>{request.current?.abort();setValues(null);setRevealed(false);client.setQueryData(['project-gitops',nodeID,projectName],updated);setVariables(updated.environment);setBaseline(JSON.stringify(updated.environment));setExpectedRevision(updated.revision);await client.invalidateQueries({queryKey:['project-gitops',nodeID,projectName]})}})
 const toggle=async()=>{
  setError('');request.current?.abort()
  if(revealed){setValues(null);setRevealed(false);setLoading(false);return}
  const controller=new AbortController();request.current=controller;setLoading(true)
  try{const result=await api<Record<string,string>>(`${path}/environment/values`,{signal:controller.signal});if(!controller.signal.aborted){setValues(result);setRevealed(true)}}catch(error){if(!controller.signal.aborted)setError(error instanceof Error?error.message:String(error))}finally{if(!controller.signal.aborted)setLoading(false)}
 }
 return <div className="min-w-0 space-y-4" data-testid="gitops-environment">
  <section className="section-surface space-y-2"><div className="flex flex-wrap items-center justify-between gap-2"><h3 className="text-sm font-semibold">{zh?'仓库环境文件（只读）':'Repository environment (read only)'}</h3><Button size="sm" variant="ghost" onClick={()=>setShowRepository(!showRepository)}>{showRepository?(zh?'隐藏变量值':'Hide variable values'):(zh?'显示仓库变量值':'Reveal repository values')}</Button></div><pre className="max-h-60 overflow-auto whitespace-pre-wrap break-all text-xs">{(showRepository?repositoryEnvironment:maskEnvironment(repositoryEnvironment)) || (zh?'未配置仓库变量':'No repository variables')}</pre></section>
  <section className="section-surface space-y-3"><h3 className="text-sm font-semibold">{zh?'本地覆盖变量':'Local environment overrides'}</h3><p className="text-xs text-muted-foreground">{zh?'加密保存，优先于仓库环境文件；仅用于 Compose 插值，不会自动注入所有容器。':'Encrypted values override the repository environment for Compose interpolation; they are not automatically injected into every container.'}</p>
   <fieldset disabled={save.isPending} className="min-w-0 space-y-3"><EnvironmentFields variables={variables} onChange={setVariables} zh={zh} environmentValues={values} showEnvironment={revealed} environmentLoading={loading} environmentError={error} toggleEnvironment={toggle}/><Button disabled={!dirty||save.isPending} onClick={()=>save.mutate()}>{zh?'保存变量':'Save variables'}</Button></fieldset>{save.error&&<InlineStatus tone="error">{save.error.message}</InlineStatus>}
  </section>
 </div>
}
