import {expect,test,type Page} from '@playwright/test'

async function setup(page:Page,language='en-US',theme='dark',gitops=true){
 await page.addInitScript(({language,theme})=>{localStorage.setItem('suma-language',language);localStorage.setItem('suma-theme',theme);localStorage.setItem('suma-node','local')},{language,theme})
 const writes:{path:string;body:Record<string,unknown>}[]=[]
 const record={id:1,operation:'check',status:'succeeded',task_id:'check',actor:'Admin',revision:1,commit_sha:'a'.repeat(40),commit_message:'Update app',config_hash:'b'.repeat(64),created_at:'2026-11-01T00:00:00Z',failure_reason:''}
 let mode=gitops
 let config={configured:gitops,enabled:gitops,revision:1,repository:{clone_url:'https://git.example.test/app.git',ref_type:'branch',ref:'main',compose_files:['compose.yml','override.yml'],environment_file:'',authentication:{source:'none'}},environment:[{name:'API_TOKEN',has_value:true}],registry_credential_ids:[],deployment_timeout:120,sync_interval_seconds:300,automation_enabled:false,initial_application_required:true,webhook_enabled:false,candidate:gitops?record:undefined,applied:undefined as typeof record|undefined}
 const source='services:\n  app:\n    image: busybox:stable\n    environment:\n      API_TOKEN: ${API_TOKEN}\n'
 await page.route('**/api/v1/**',async route=>{
  const request=route.request(),path=new URL(request.url()).pathname.replace('/api/v1','')
  const body=request.method()==='GET'?{}:request.postDataJSON()??{}
  if(request.method()!=='GET')writes.push({path,body})
  if(request.method()==='PUT' && path.includes('/gitops') && body.expected_revision!==config.revision)return route.fulfill({status:409,json:{code:20902,message:'CD configuration changed',data:null}})
  let data:unknown=[]
  if(path==='/auth/status')data={needs_setup:false}
  else if(path==='/auth/session')data={id:1,username:'admin'}
  else if(path==='/settings')data={'general.timezone':'Asia/Shanghai'}
  else if(path==='/nodes')data=[{id:'local',name:'Local',enabled:true,connection_type:'unix',status:'online',group_ids:[1]}]
  else if(path==='/node-groups')data=[{id:1,name:'Default',is_default:true}]
  else if(path==='/notifications/inbox')data={items:[],unread:0}
  else if(path==='/nodes/local/projects/compose/app')data={name:'app',backend:'compose',managed:true,deployment_mode:mode?'cd':'manual',capabilities:['view','edit','deploy','services','logs'],status:'running',revision:'manual-r1',compose:source,environment:'APP_ENV=production\n',declared_service_names:['app'],metadata:{origin:'created',last_deployed_at:record.created_at}}
  else if(path.endsWith('/gitops') && request.method()==='GET')data=config
  else if(path.endsWith('/gitops') && request.method()==='PUT'){mode=true;config={...config,...body,configured:true,enabled:true,revision:config.revision+1} as typeof config;data=config}
  else if(path.endsWith('/gitops/drift'))data={status:'healthy',reason:'hash_and_health_match'}
  else if(path.endsWith('/gitops/preview'))data={record_id:record.id,commit_sha:record.commit_sha,applied:!!config.applied,compose:source,files:[{path:'compose.yml',content:source},{path:'override.yml',content:'services: {app: {restart: unless-stopped}}'}],repository_environment:'APP_ENV=production\nPASSWORD=repository-private\n'}
  else if(path.endsWith('/gitops/records'))data={items:[record,...config.applied?[config.applied]:[]],total:config.applied?2:1}
  else if(path.endsWith('/gitops/environment/values'))data={API_TOKEN:'private-value'}
  else if(path.endsWith('/gitops/environment')&&request.method()==='PUT'){config={...config,revision:config.revision+1,environment:(body.environment as {name:string;has_value:boolean}[]).map(row=>({name:row.name,has_value:true}))};data=config}
  else if(path.endsWith('/gitops/apply')){config={...config,initial_application_required:false,applied:{...record,id:2,operation:'apply'}};data={id:'apply-task'}}
  else if(path.endsWith('/gitops/detach')){mode=false;config={...config,enabled:false,automation_enabled:false};data={id:'detach-task'}}
  else if(path.endsWith('/gitops/automation')){config={...config,automation_enabled:body.enabled===true};data=config}
  else if(path.includes('/tasks/'))data={status:'success',message:'Completed',progress:100}
  else if(path.endsWith('/image-updates'))data={results:[]}
  return route.fulfill({json:{code:0,message:'',data}})
 })
 await page.goto('/projects/compose/app')
 await expect(page.getByRole('tab',{name:'CD',exact:true})).toBeVisible()
 return {writes,zh:language==='zh-CN',setRevision:(revision:number)=>{config={...config,revision}}}
}
for(const language of ['en-US','zh-CN'])for(const theme of ['dark','light'])for(const width of [390,1440]){
 test(`GitOps read-only layout ${language} ${theme} ${width}`,async({page})=>{
  await page.setViewportSize({width,height:1000});const {zh}=await setup(page,language,theme)
  const workspace=page.getByTestId('project-configuration')
  await expect(workspace.getByRole('tab',{name:zh?'可视化':'Visual',exact:true})).toBeVisible()
  await expect(workspace.getByRole('tab',{name:'.env',exact:true})).toBeVisible()
  await expect(workspace.getByRole('button',{name:zh?'添加服务':'Add service',exact:true})).toBeHidden()
  await expect(page.getByRole('button',{name:zh?'拉取并重建':'Pull & recreate',exact:true})).toHaveCount(0)
  await workspace.getByRole('tab',{name:zh?'端口':'Ports',exact:true}).click()
  await expect(workspace.getByRole('tab',{name:zh?'端口':'Ports',exact:true})).toHaveAttribute('aria-selected','true')
  await workspace.getByRole('tab',{name:zh?'源码':'Compose',exact:true}).click()
  const trigger=workspace.getByLabel(zh?'源码文件':'Source file');await trigger.click()
  const menu=page.locator('[data-slot="select-content"]:visible');await expect(menu).toBeVisible();await menu.evaluate(async element=>{await Promise.all(element.getAnimations({subtree:true}).map(animation=>animation.finished.catch(()=>{})))})
  const anchor=await trigger.boundingBox(),bounds=await menu.boundingBox();expect(Math.abs(anchor!.width-bounds!.width)).toBeLessThanOrEqual(1);expect(bounds!.y>=anchor!.y+anchor!.height-1||bounds!.y+bounds!.height<=anchor!.y+1).toBe(true)
  await page.getByRole('option',{name:'override.yml',exact:true}).click()
  await workspace.getByRole('tab',{name:'.env',exact:true}).click();await expect(page.getByTestId('gitops-environment')).toBeVisible()
  await expect(page.getByLabel(`${zh?'变量值':'Variable value'} 1`,{exact:true})).toHaveAttribute('type','password')
  await page.getByRole('tab',{name:'CD',exact:true}).click();await expect(page.getByText(zh?'项目 CD · 单节点交付':'Project CD · Single-node delivery',{exact:true})).toBeVisible()
  await expect(page.getByRole('switch',{name:zh?'自动同步并应用':'Automatically sync and apply'})).toBeDisabled()
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth+1)).toBe(true)
  await page.screenshot({path:`/tmp/gitops-${language}-${theme}-${width}.png`,fullPage:true})
 })
}
test('GitOps variable edits stay private and application uses the reviewed candidate',async({page})=>{
 const {writes}=await setup(page)
 await page.getByRole('tab',{name:'.env',exact:true}).click()
 await page.getByRole('button',{name:'Show variable values',exact:true}).click()
 await expect(page.getByLabel('Variable value 1',{exact:true})).toHaveValue('private-value')
 await page.getByRole('button',{name:'Hide variable values',exact:true}).click()
 await expect(page.getByLabel('Variable value 1',{exact:true})).toHaveValue('')
 await page.getByLabel('Variable value 1',{exact:true}).fill('edited-private')
 await page.getByRole('button',{name:'Save variables',exact:true}).click()
 await expect.poll(()=>writes.filter(row=>row.path.endsWith('/environment')).length).toBe(1)
 await expect(page.getByLabel('Variable value 1',{exact:true})).toHaveAttribute('type','password')
})
test('GitOps review applies a fixed snapshot and manual mode restores editing',async({page})=>{
 const {writes}=await setup(page)
 await page.getByRole('tab',{name:'CD',exact:true}).click()
 await page.getByRole('button',{name:'Review and apply',exact:true}).click()
 await expect(page.getByRole('region',{name:'Application review',exact:true})).toContainText('aaaaaaaa')
 await page.getByRole('button',{name:'Confirm application',exact:true}).click()
 await expect.poll(()=>writes.find(row=>row.path.endsWith('/apply'))?.body).toEqual({candidate_id:1,expected_revision:1})
 await expect(page.getByRole('switch',{name:'Automatically sync and apply'})).toBeEnabled()
 await page.getByRole('button',{name:'Return to manual',exact:true}).click()
 await page.getByRole('dialog').getByRole('button',{name:'Confirm',exact:true}).click()
 await page.getByRole('tab',{name:'Configuration',exact:true}).click()
 await expect(page.getByRole('button',{name:'Pull & recreate',exact:true})).toBeVisible()
})

test('CD environment conflict preserves edits across background configuration refresh',async({page})=>{
 const {writes,setRevision}=await setup(page)
 await page.getByRole('tab',{name:'.env',exact:true}).click()
 await page.getByLabel('Variable value 1',{exact:true}).fill('keep-my-edit')
 setRevision(2)
 await page.waitForResponse(async response=>response.url().endsWith('/gitops') && response.request().method()==='GET' && (await response.json()).data.revision===2)
 await expect(page.getByLabel('Variable value 1',{exact:true})).toHaveValue('keep-my-edit')
 await page.getByRole('button',{name:'Save variables',exact:true}).click()
 await expect(page.getByText('CD configuration changed',{exact:true})).toBeVisible()
 await expect(page.getByLabel('Variable value 1',{exact:true})).toHaveValue('keep-my-edit')
 expect(writes.find(row=>row.path.endsWith('/environment'))?.body.expected_revision).toBe(1)
})
test('CD settings conflict retains the original version and repository draft',async({page})=>{
 const {writes,setRevision}=await setup(page)
 await page.getByRole('tab',{name:'CD',exact:true}).click()
 await page.getByLabel('Branch / Tag / Commit',{exact:true}).fill('my-draft')
 setRevision(2)
 await page.waitForResponse(async response=>response.url().endsWith('/gitops') && response.request().method()==='GET' && (await response.json()).data.revision===2)
 await page.getByRole('button',{name:'Save CD settings',exact:true}).click()
 await expect(page.getByText('CD configuration changed',{exact:true})).toBeVisible()
 await expect(page.getByLabel('Branch / Tag / Commit',{exact:true})).toHaveValue('my-draft')
 expect(writes.find(row=>row.path.endsWith('/gitops'))?.body.expected_revision).toBe(1)
})
