import {expect,test,type Page} from '@playwright/test'
import type {DeliveryHistoryRecord} from '../src/features/delivery/types'

async function setup(page:Page,language='en-US',theme='dark',many=false){
 await page.addInitScript(({language,theme})=>{localStorage.setItem('suma-language',language);localStorage.setItem('suma-theme',theme);localStorage.setItem('suma-node','local')},{language,theme})
 const base:DeliveryHistoryRecord={id:'continuous_delivery:1',source:'continuous_delivery',record_id:1,project_name:'batch-app',project_id:1,release_id:9,node_exists:false,node_ids:['local','remote'],operation:'deploy',status:'succeeded',actor_name:'Operator',task_id:'batch-task',commit_sha:'a'.repeat(40),config_hash:'batch-hash',failure_reason:'',created_at:'2026-11-01T09:00:00Z'}
 const records:DeliveryHistoryRecord[]=[
  {...base,id:'project_cd:1',source:'project_cd',project_name:'app',release_id:undefined,node_id:'local',node_name:'Local',node_exists:true,node_ids:['local'],task_id:'local-task',created_at:'2026-11-01T12:00:00Z'},
  {...base,id:'project_cd:2',record_id:2,source:'project_cd',project_name:'app',release_id:undefined,node_id:'remote',node_name:'Edge',node_exists:true,node_ids:['remote'],operation:'auto_rollback',status:'failed',source_record_id:1,task_id:'remote-task',failure_reason:'Recovery failed; inspect Docker state',created_at:'2026-11-01T11:00:00Z'},
  base,
  {...base,id:'project_cd:3',record_id:3,source:'project_cd',project_name:'old-app',release_id:undefined,node_id:'removed',node_name:'Removed node',node_exists:false,node_ids:['removed'],operation:'manual_rollback',status:'interrupted',task_id:'old-task',created_at:'2026-11-01T08:00:00Z'},
 ]
 if(many)for(let i=4;i<28;i++)records.push({...records[0],id:`project_cd:${i}`,record_id:i,operation:'check',created_at:'2026-10-01T00:00:00Z'})
 const requests:string[]=[],writes:string[]=[]
 let fail=false
 await page.route('**/api/v1/**',async route=>{
  const request=route.request(),url=new URL(request.url()),path=url.pathname.replace('/api/v1','')
  requests.push(path+url.search)
  if(request.method()!=='GET')writes.push(path)
  let data:unknown=[]
  if(path==='/auth/status')data={needs_setup:false}
  else if(path==='/auth/session')data={id:1,username:'admin'}
  else if(path==='/settings')data={'general.timezone':'UTC'}
  else if(path==='/nodes')data=[{id:'local',name:'Local',enabled:true,status:'online',connection_type:'unix',group_ids:[1]},{id:'remote',name:'Edge',enabled:true,status:'online',connection_type:'tcp',group_ids:[1]}]
  else if(path==='/node-groups')data=[{id:1,name:'Default',is_default:true}]
  else if(path==='/notifications/inbox')data={items:[],unread:0}
  else if(path==='/delivery-projects')data=[]
  else if(path==='/delivery-records'){
   if(fail)return route.fulfill({status:500,json:{code:17706,message:'Delivery records unavailable',data:null}})
   const source=url.searchParams.get('source'),operation=url.searchParams.get('operation'),status=url.searchParams.get('status'),search=(url.searchParams.get('search')||'').toLowerCase()
   const filtered=records.filter(row=>(!source||row.source===source)&&(!operation||row.operation===operation)&&(!status||row.status===status)&&(!search||`${row.project_name} ${row.node_name||''} ${row.node_id||''}`.toLowerCase().includes(search)))
   const pageNumber=Number(url.searchParams.get('page')||1),size=Number(url.searchParams.get('page_size')||20)
   data={items:filtered.slice((pageNumber-1)*size,pageNumber*size),total:filtered.length,page:pageNumber,page_size:size}
  }
  else if(/^\/nodes\/(local|remote)\/projects\/compose\/app$/.test(path))data={node_id:path.split('/')[2],name:'app',backend:'compose',managed:true,deployment_mode:'cd',capabilities:['view','services','logs'],status:'running',revision:'manual-r1',compose:'services: {app: {image: busybox:stable}}',environment:'',metadata:{origin:'created'}}
  else if(path==='/delivery-projects/batch-app')data={id:1,name:'batch-app',configured:true,node_ids:['local','remote']}
  else if(path==='/delivery-projects/batch-app/configuration')data={configured:true,repository:{clone_url:'https://git.example.test/batch.git',ref_type:'branch',ref:'main',compose_files:['compose.yml'],environment_file:'',authentication:{source:'none'}},environment:[],node_ids:['local','remote'],registry_credential_ids:[],reconcile_mode:'manual',automation_enabled:false,auto_rollback:false,sync_interval_seconds:300,deployment_timeout:120,webhook_enabled:false}
  else if(path==='/delivery-projects/batch-app/records')data={items:[],total:0,page:1,page_size:20,counts:{}}
  else if(path.endsWith('/gitops'))data={configured:true,enabled:true,revision:1,repository:{clone_url:'https://git.example.test/app.git',ref_type:'branch',ref:'main',compose_files:['compose.yml'],environment_file:'',authentication:{source:'none'}},environment:[],registry_credential_ids:[],deployment_timeout:120,sync_interval_seconds:300,automation_enabled:false,reconcile_mode:'manual',auto_rollback:false,initial_application_required:true,webhook_enabled:false}
  else if(path.endsWith('/gitops/records'))data={items:[],total:0}
  else if(path.endsWith('/image-updates'))data={results:[]}
  return route.fulfill({json:{code:0,message:'',data}})
 })
 await page.goto('/continuous-delivery?view=records')
 return {requests,writes,setFailure:()=>{fail=true}}
}

for(const language of ['en-US','zh-CN'])for(const theme of ['dark','light'])for(const width of [390,1440]){
 test(`unified CD history ${language} ${theme} ${width}`,async({page})=>{
  await page.setViewportSize({width,height:1000});const {writes}=await setup(page,language,theme)
  const zh=language==='zh-CN',history=page.getByTestId('delivery-history')
  await expect(history.getByTestId('delivery-history-row')).toHaveCount(4)
  await expect(history.locator('[data-record-id="project_cd:1"]')).toContainText('Local')
  await expect(history.locator('[data-record-id="continuous_delivery:1"]')).toContainText('batch-app')
  const source=page.getByLabel(zh?'记录来源':'Record source',{exact:true});await source.click()
  const menu=page.locator('[data-slot="select-content"]:visible');await expect(menu).toBeVisible();await menu.evaluate(async element=>{await Promise.all(element.getAnimations({subtree:true}).map(animation=>animation.finished.catch(()=>{})))})
  const anchor=await source.boundingBox(),bounds=await menu.boundingBox();expect(Math.abs(anchor!.width-bounds!.width)).toBeLessThanOrEqual(1);expect(bounds!.y>=anchor!.y+anchor!.height-1||bounds!.y+bounds!.height<=anchor!.y+1).toBe(true)
  await page.getByRole('option',{name:zh?'节点项目 CD':'Node project CD',exact:true}).click()
  await expect(history.getByTestId('delivery-history-row')).toHaveCount(3)
  await expect(source).toContainText(zh?'节点项目 CD':'Node project CD')
  await expect(page.getByLabel(zh?'操作类型':'Operation type',{exact:true})).toContainText(zh?'全部操作':'All operations')
  await expect(page.getByLabel(zh?'记录状态':'Record status',{exact:true})).toContainText(zh?'全部状态':'All statuses')
  await history.locator('[data-record-id="project_cd:2"] summary').click()
  await expect(history.locator('[data-record-id="project_cd:2"]')).toContainText('Recovery failed; inspect Docker state')
  await expect(history.locator('[data-record-id="project_cd:2"]')).toContainText(zh?'来源记录':'Source record')
  await expect(history.getByRole('link',{name:zh?'任务日志':'Task logs',exact:true})).toHaveAttribute('href','/tasks#remote-task')
  expect(await history.evaluate(element=>element.scrollWidth<=element.clientWidth+1)).toBe(true)
  await page.screenshot({path:`/tmp/delivery-history-${language}-${theme}-${width}.png`,fullPage:true})
  expect(writes).toEqual([])
 })
}

test('history switches to the originating node and opens project CD',async({page})=>{
 const {requests,writes}=await setup(page)
 const remote=page.locator('[data-record-id="project_cd:2"]')
 await remote.locator('summary').click()
 await remote.getByRole('button',{name:'Open project CD',exact:true}).click()
 await expect(page).toHaveURL(/\/projects\/compose\/app\?view=cd$/)
 await expect(page.getByRole('tab',{name:'CD',exact:true})).toHaveAttribute('aria-selected','true')
 await expect.poll(()=>page.evaluate(()=>localStorage.getItem('suma-node'))).toBe('remote')
 expect(requests.some(path=>path==='/nodes/remote/projects/compose/app')).toBe(true)
 expect(writes).toEqual([])
})

test('history filters reset server pagination and preserve source identity',async({page})=>{
 const {requests}=await setup(page,'en-US','dark',true)
 await expect(page.getByTestId('delivery-history-row')).toHaveCount(20)
 await page.getByRole('button',{name:'Next page',exact:true}).click()
 await expect.poll(()=>requests.some(path=>path.startsWith('/delivery-records?')&&new URLSearchParams(path.split('?')[1]).get('page')==='2')).toBe(true)
 await page.getByLabel('Record source',{exact:true}).click()
 await page.getByRole('option',{name:'Batch delivery',exact:true}).click()
 await expect(page.getByTestId('delivery-history-row')).toHaveCount(1)
 const query=new URLSearchParams(requests.filter(path=>path.startsWith('/delivery-records?')).at(-1)!.split('?')[1])
 expect(query.get('source')).toBe('continuous_delivery');expect(query.get('page')).toBe('1')
 await page.getByLabel('Search projects or nodes',{exact:true}).fill('missing')
 await expect(page.getByText('No matching delivery records',{exact:true})).toBeVisible()
})

test('history preserves removed-node evidence and reports loading failures',async({page})=>{
 const {setFailure}=await setup(page)
 const removed=page.locator('[data-record-id="project_cd:3"]')
 await removed.locator('summary').click()
 await expect(removed.getByRole('button',{name:'Open project CD',exact:true})).toBeDisabled()
 setFailure()
 await page.getByLabel('Search projects or nodes',{exact:true}).fill('retry')
 await expect(page.getByText('Delivery records unavailable',{exact:true})).toBeVisible()
})


test('operation and status filters combine across both history sources',async({page})=>{
 const {requests}=await setup(page)
 await page.getByLabel('Operation type',{exact:true}).click()
 await page.getByRole('option',{name:'Automatic rollback',exact:true}).click()
 await page.getByLabel('Record status',{exact:true}).click()
 await page.getByRole('option',{name:'failed',exact:true}).click()
 await expect(page.getByTestId('delivery-history-row')).toHaveCount(1)
 await expect(page.locator('[data-record-id="project_cd:2"]')).toBeVisible()
 const last=new URLSearchParams(requests.filter(path=>path.startsWith('/delivery-records?')).at(-1)!.split('?')[1])
 expect(last.get('operation')).toBe('auto_rollback');expect(last.get('status')).toBe('failed')
})

test('batch history opens its own records without changing the selected node',async({page})=>{
 const {writes}=await setup(page)
 const batch=page.locator('[data-record-id="continuous_delivery:1"]')
 await batch.locator('summary').click()
 await batch.getByRole('button',{name:'Open delivery project',exact:true}).click()
 await expect(page).toHaveURL(/\/continuous-delivery\/batch-app\?view=records$/)
 await expect(page.getByRole('tab',{name:'Records',exact:true})).toHaveAttribute('aria-selected','true')
 expect(await page.evaluate(()=>localStorage.getItem('suma-node'))).toBe('local')
 expect(writes).toEqual([])
})
