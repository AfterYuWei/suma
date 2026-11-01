import {prepareCDSettingsInput} from '../delivery/settings-input'
import {gitOpsPath,type ProjectCDConfiguration,type ProjectCDPreview,type ProjectCDRecord} from './queries'
import { useCallback, useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Button } from '../../components/ui/button'
import { InlineStatus } from '../../components/ui/inline-status'
import { api } from '../../lib/api'
import { nodePath } from '../../lib/nodes'
import { confirmDialog } from '../../stores/dialog'
import { CDSettingsFields, CDEnvironmentSettings, CDWebhookSecretNotice } from '../delivery/settings'
import type { ProjectCDEnvironmentDraft } from './environment'
import { parseEnvironmentInput } from '../delivery/environment-input'
import type { CDConfigureInput } from '../delivery/types'
import { maskEnvironment, maskCompose } from '../compose/document'


function toDraft(configuration:ProjectCDConfiguration,nodeID:string):CDConfigureInput {
 return {repository:configuration.repository,environment:[],automation_enabled:configuration.automation_enabled,reconcile_mode:configuration.reconcile_mode,sync_interval_seconds:configuration.sync_interval_seconds,auto_rollback:configuration.auto_rollback,deployment_timeout:configuration.deployment_timeout,webhook_enabled:configuration.webhook_enabled,webhook_secret:'',node_ids:[nodeID],registry_credential_ids:configuration.registry_credential_ids}
}
export function ProjectCD({nodeID,projectName,configuration,initialEnvironment,baselineCompose,zh,onDirtyChange,environment}:{onDirtyChange:(value:boolean)=>void;nodeID:string;projectName:string;configuration:ProjectCDConfiguration;initialEnvironment:string;baselineCompose:string;zh:boolean;environment:ProjectCDEnvironmentDraft}){
 const path=gitOpsPath(nodeID,projectName),client=useQueryClient()
 const [draft,setDraft]=useState(()=>toDraft(configuration,nodeID)),[composeFiles,setComposeFiles]=useState(configuration.repository.compose_files)
 const [reviewed,setReviewed]=useState(false),[secret,setSecret]=useState(''),[taskID,setTaskID]=useState(''),[page,setPage]=useState(1),[review,setReview]=useState(false),[showBefore,setShowBefore]=useState(false),[importError,setImportIssue]=useState('')
 const [baseline,setBaseline]=useState(()=>JSON.stringify([toDraft(configuration,nodeID),configuration.repository.compose_files])),[expectedRevision,setExpectedRevision]=useState(configuration.revision)
 const settingsDirty=JSON.stringify([draft,composeFiles])!==baseline
 const dirty=settingsDirty||environment.dirty
 useEffect(()=>{onDirtyChange(settingsDirty);return ()=>onDirtyChange(false)},[settingsDirty,onDirtyChange])
 useEffect(()=>{if(!settingsDirty){const next=toDraft(configuration,nodeID);setDraft(next);setComposeFiles(configuration.repository.compose_files);setBaseline(JSON.stringify([next,configuration.repository.compose_files]));setExpectedRevision(configuration.revision)}},[configuration,nodeID,settingsDirty])
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
 const save=useMutation({mutationFn:async()=>{
  const input=await prepareCDSettingsInput({...draft,environment:environment.variables},composeFiles,zh)
  if(!input)return null
  return api<ProjectCDConfiguration>(path,{method:'PUT',body:JSON.stringify({...input,expected_revision:environment.dirty?Math.min(expectedRevision,environment.expectedRevision):expectedRevision,reviewed_environment:reviewed})})
 },onSuccess:async value=>{if(!value)return;const next=toDraft(value,nodeID);setDraft(next);setComposeFiles(value.repository.compose_files);setBaseline(JSON.stringify([next,value.repository.compose_files]));setExpectedRevision(value.revision);environment.accept(value);setSecret(value.webhook_secret||'');await refresh()}})
 const busy=action.isPending || save.isPending || (!!activeTaskID && (!task.data || !['success','failed','canceled'].includes(task.data.status)))
 const rollback=async(record:ProjectCDRecord)=>{if(await confirmDialog({title:zh?'回滚到此成功配置？':'Restore this successful configuration?',description:zh?'交付策略切换为手动；重新拉取镜像并重建，数据卷保留。可变镜像标签仍使用仓库当前内容。':'Delivery policy switches to manual. Images are pulled and containers recreated; volumes are retained. Mutable tags use their current registry contents.',danger:true})){action.mutate({route:`/records/${record.id}/rollback`,body:{expected_revision:configuration.revision}})}}
 return <div className="w-full min-w-0 space-y-4" data-testid="project-gitops">
  {(configuration.enabled||task.data||preview.error||action.error||save.error)&&<section className="section-surface space-y-3">
   {drift.data && <p className="text-xs text-muted-foreground">{drift.data.status==='healthy'?(zh?'已应用配置指纹与运行健康一致':'Applied configuration hash and runtime health match'):drift.data.status==='drifted'?(zh?'检测到运行偏离，请检查节点；不会自动修复。':'Runtime drift detected; inspect the node. Automatic repair is disabled.'):(zh?'运行状态尚未确认':'Runtime state is unconfirmed')}</p>}
   {drift.error&&<InlineStatus tone="error">{drift.error.message}</InlineStatus>}
   {configuration.enabled && <><div className="flex flex-wrap gap-x-6 gap-y-1 font-mono text-xs"><span>{zh?'候选':'Candidate'} {configuration.candidate?.commit_sha.slice(0,8)||'—'}</span><span>{zh?'生效':'Applied'} {configuration.applied?.commit_sha.slice(0,8)||'—'}</span></div><div className="flex flex-wrap items-center gap-2">
    <Button variant="outline" size="sm" disabled={busy||dirty} onClick={()=>action.mutate({route:'/check'})}>{zh?'同步 Git':'Sync Git'}</Button>
    <Button size="sm" disabled={busy||dirty||configuration.candidate?.status!=='succeeded'||configuration.candidate.revision!==configuration.revision||configuration.reconcile_mode==='observe'} onClick={()=>setReview(true)}>{zh?'审阅并应用':'Review and apply'}</Button>
    <Button variant="ghost" size="sm" disabled={busy||dirty} onClick={async()=>{if(await confirmDialog({title:zh?'切回手动模式？':'Return to manual mode?',description:zh?'导出最近成功配置，未应用草稿不保留。运行容器不变，原镜像自动更新策略将恢复。':'Exports the last successful configuration and discards unapplied drafts. Containers keep running; the original image update policy resumes.',danger:true})){action.mutate({route:'/detach',body:{expected_revision:configuration.revision}})}}}>{zh?'切回手动模式':'Return to manual'}</Button>
   </div></>}
   {task.data && <div className="rounded-md bg-muted/40 p-3 text-xs" role="status">{task.data.status} · {task.data.progress}% · {task.data.message}<a className="ml-2 underline" href={`/tasks#${encodeURIComponent(activeTaskID)}`}>{zh?'任务日志':'Task logs'}</a></div>}
   {preview.error && <InlineStatus tone="error">{preview.error.message}</InlineStatus>}
   {(action.error||save.error) && <InlineStatus tone="error">{(action.error||save.error)?.message}</InlineStatus>}
  </section>}
  {review && preview.data && <section className="section-surface space-y-3" role="region" aria-label={zh?'应用确认':'Application review'}><h3 className="text-sm font-semibold">{zh?'确认应用固定快照':'Confirm the frozen snapshot'} · {preview.data.commit_sha.slice(0,8)}</h3><p className="text-xs text-muted-foreground">{nodeID} / {projectName} · {preview.data.files.map(file=>file.path).join(' → ')}</p><Button variant="ghost" size="sm" onClick={()=>setShowBefore(!showBefore)}>{showBefore?(zh?'隐藏当前配置':'Hide current configuration'):(zh?'比较当前配置':'Compare current configuration')}</Button>{showBefore&&<pre className="max-h-64 overflow-auto rounded-md bg-muted/30 p-3 text-xs">{maskCompose(baselineCompose)}</pre>}<pre className="max-h-72 overflow-auto rounded-md bg-muted/30 p-3 text-xs">{maskCompose(preview.data.compose)}</pre><p className="text-xs text-muted-foreground">{zh?'配置可能重建服务或清理已移除的服务容器，数据卷保留。':'Applying may recreate services or remove orphan containers. Data volumes are retained.'}</p><div className="flex gap-2"><Button disabled={busy} onClick={()=>{action.mutate({route:'/apply',body:{candidate_id:preview.data.record_id,expected_revision:configuration.revision}});setReview(false)}}>{zh?'确认应用':'Confirm application'}</Button><Button variant="ghost" onClick={()=>setReview(false)}>{zh?'取消':'Cancel'}</Button></div></section>}
  <form onSubmit={event=>{event.preventDefault();save.mutate()}} className="flex w-full max-w-[1120px] flex-col gap-6" aria-label={zh?'交付项目配置':'Delivery project configuration'}>
   <fieldset disabled={busy} className="flex min-w-0 flex-col gap-7">
    <CDSettingsFields projectName={projectName} configuration={configuration} draft={{...draft,environment:environment.variables}} setDraft={update=>{const next=typeof update==='function'?update({...draft,environment:environment.variables}):update;environment.setVariables(next.environment??[]);setDraft({...next,environment:[]})}} composeFiles={composeFiles} setComposeFiles={setComposeFiles} zh={zh} fixedNodeID={nodeID} connectionEndpoint={`${path}/test-connection`} webhookURL={configuration.webhook_id?`${window.location.origin}/api/v1/webhooks/gitops/${configuration.webhook_id}`:''} environmentValues={environment.values}/>
    {!configuration.enabled && initialEnvironment && <div className="space-y-2 rounded-lg bg-muted/40 p-3"><p className="text-xs">{zh?'启用前请审阅并导入原 .env；复杂表达式需手动整理，不能静默忽略。':'Review and import the original .env. Resolve complex expressions explicitly before enabling.'}</p><Button type="button" size="sm" variant="outline" onClick={()=>{try{if(initialEnvironment.includes('$') || initialEnvironment.includes('\\'))throw new Error(zh?'原 .env 含插值或转义，请先在 .env 中整理为实际值。':'Resolve interpolation and escapes in the original .env before importing.');const variables=parseEnvironmentInput(initialEnvironment,zh);environment.setVariables(variables);setReviewed(true)}catch(error){save.reset();action.reset();setImportError(error instanceof Error?error.message:String(error))}}}>{zh?'审阅并导入原 .env':'Review and import original .env'}</Button><p className="text-xs">{environment.variables.map(row=>row.name).join(' · ')}</p>{importError&&<InlineStatus tone="error">{importError}</InlineStatus>}</div>}
    <p className="text-xs text-muted-foreground">{zh?'仅支持预构建镜像，沿用持续交付安全限制。首次应用或 Git 来源变化需人工确认；保存本身不部署。':'Prebuilt images only, with CD deployment restrictions. First applications and source changes require confirmation. Saving does not deploy.'}</p>
   </fieldset>
   <div className="flex flex-wrap items-center gap-3"><Button className="ml-auto" type="submit" disabled={busy||(!configuration.enabled&&!!initialEnvironment&&!reviewed)}>{configuration.enabled?(zh?'保存 CD 配置':'Save CD settings'):(zh?'连接并保存':'Connect and save')}</Button></div>
  </form>
  <CDWebhookSecretNotice secret={secret} zh={zh}/>
  {configuration.configured && <section className="section-surface space-y-3"><h3 className="text-sm font-semibold">{zh?'同步记录':'Synchronization records'}</h3>{records.data?.items.map(record=><details key={record.id} className="rounded-md bg-muted/30 p-3 text-xs"><summary className="flex cursor-pointer flex-wrap items-center gap-2"><span>{record.operation} · {record.status}</span><span className="font-mono">{record.commit_sha.slice(0,8)}</span><span>{record.actor}</span><time className="ml-auto">{new Date(record.created_at).toLocaleString()}</time></summary><div className="mt-3 space-y-2 break-all"><p>{record.commit_message}</p><p className="font-mono">{record.config_hash}</p>{record.failure_reason&&<InlineStatus tone="error">{record.failure_reason}</InlineStatus>}<a className="underline" href={`/tasks#${encodeURIComponent(record.task_id)}`}>{zh?'任务日志':'Task logs'}</a>{record.status==='succeeded'&&['apply','rollback','auto_rollback'].includes(record.operation)&&configuration.enabled&&configuration.reconcile_mode!=='observe'&&<Button size="sm" variant="ghost" disabled={busy||dirty} onClick={()=>void rollback(record)}>{zh?'回滚到此配置':'Restore this configuration'}</Button>}</div></details>)}{records.error&&<InlineStatus tone="error">{records.error.message}</InlineStatus>}<div className="flex items-center gap-2"><Button size="sm" variant="ghost" disabled={page===1} onClick={()=>setPage(value=>value-1)}>{zh?'上一页':'Previous'}</Button><span className="text-xs">{page}</span><Button size="sm" variant="ghost" disabled={page*20>=(records.data?.total??0)} onClick={()=>setPage(value=>value+1)}>{zh?'下一页':'Next'}</Button></div></section>}
 </div>
 function setImportError(message:string){setImportIssue(message)}
}

export function ProjectCDEnvironment({repositoryEnvironment,zh,environment}:{repositoryEnvironment:string;zh:boolean;environment:ProjectCDEnvironmentDraft}){
 const [showRepository,setShowRepository]=useState(false)
 return <div className="min-w-0 space-y-4" data-testid="gitops-environment">
  <section className="section-surface space-y-2"><div className="flex flex-wrap items-center justify-between gap-2"><h3 className="text-sm font-semibold">{zh?'仓库环境文件（只读）':'Repository environment (read only)'}</h3><Button size="sm" variant="ghost" onClick={()=>setShowRepository(!showRepository)}>{showRepository?(zh?'隐藏仓库变量值':'Hide repository values'):(zh?'显示仓库变量值':'Reveal repository values')}</Button></div><pre className="max-h-60 overflow-auto whitespace-pre-wrap break-all text-xs">{(showRepository?repositoryEnvironment:maskEnvironment(repositoryEnvironment)) || (zh?'未配置仓库变量':'No repository variables')}</pre></section>
  <fieldset disabled={environment.save.isPending} className="min-w-0"><CDEnvironmentSettings variables={environment.variables} onChange={environment.setVariables} zh={zh} values={environment.values}>
   <div className="flex flex-wrap items-center gap-3"><Button className="self-start" disabled={!environment.dirty||environment.save.isPending} onClick={()=>environment.save.mutate()}>{zh?'保存变量':'Save variables'}</Button>{environment.save.error&&<InlineStatus tone="error">{environment.save.error.message}</InlineStatus>}</div>
  </CDEnvironmentSettings></fieldset>
 </div>
}
