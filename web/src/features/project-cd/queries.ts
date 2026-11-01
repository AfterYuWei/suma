import {useQuery} from '@tanstack/react-query'
import {api} from '../../lib/api'
import {nodePath} from '../../lib/nodes'
import type {CDEnvironmentVariable,GitRepositoryConfiguration} from '../delivery/types'
export interface ProjectCDRecord {id:number;operation:string;status:string;task_id:string;actor:string;revision:number;commit_sha:string;commit_message:string;config_hash:string;created_at:string;failure_reason:string}
export interface ProjectCDConfiguration {
 configured:boolean;enabled:boolean;revision:number;repository:GitRepositoryConfiguration;environment:CDEnvironmentVariable[];registry_credential_ids:number[];deployment_timeout:number;sync_interval_seconds:number;automation_enabled:boolean;initial_application_required:boolean;webhook_enabled:boolean;webhook_id?:string;webhook_secret?:string;candidate?:ProjectCDRecord;applied?:ProjectCDRecord
}
export interface ProjectCDPreview {record_id:number;commit_sha:string;applied:boolean;compose:string;files:{path:string;content:string}[];repository_environment:string}
export const gitOpsPath=(node:string,name:string)=>nodePath(node,`/projects/compose/${encodeURIComponent(name)}/gitops`)
export function useProjectCD(node:string,name:string,enabled:boolean){
 const path=gitOpsPath(node,name)
 const configuration=useQuery({queryKey:['project-gitops',node,name],queryFn:()=>api<ProjectCDConfiguration>(path),enabled,refetchInterval:enabled?3000:false})
 const preview=useQuery({queryKey:['project-gitops-preview',node,name,configuration.data?.candidate?.id,configuration.data?.applied?.id],queryFn:()=>api<ProjectCDPreview>(`${path}/preview`),enabled:enabled && !!(configuration.data?.candidate || configuration.data?.applied)})
 return {configuration,preview}
}
