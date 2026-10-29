import { expect, test } from '@playwright/test'

for(const provider of ['feishu_app','telegram','webhook']) for(const language of ['zh-CN','en-US']) for(const theme of ['dark','light']) for(const width of [390,1440]) {
 test(`platform notification cards ${provider} ${language} ${theme} ${width}`,async({page})=>{
  const zh=language==='zh-CN'
  await page.addInitScript(({provider,language,theme})=>{
   sessionStorage.setItem('suma-demo-session','1');localStorage.setItem('suma-language',language);localStorage.setItem('suma-theme',theme)
   const policy={mode:'immediate',timezone:'Asia/Shanghai',digest_hour:9,quiet_start:'',quiet_end:'',template:'',fallback_id:'',fallback_chat_id:''}
   const rule={id:'faults',name:'Faults',version:1,config:{events:['container.oom'],severities:[],node_ids:[],group_ids:[],projects:[]}}
   const channel={id:'concise',name:'Operations',provider,enabled:true,version:1,has_secrets:true,config:{app_id:'cli_concise',endpoint:'',chat_id:provider==='telegram'?'123456':'',targets:[{chat_id:'oc_operations',name:'Operations'}],language,subscription:{rule_ids:['faults'],policy},allow_private:false,auto_discover:false,public_url:'https://suma.example'}}
   sessionStorage.setItem('suma-demo-notifications-v2',JSON.stringify({serial:1,channels:[channel],rules:[rule],deliveries:[],inbox:{unread:0,items:[]}}))
  },{provider,language,theme})
  await page.setViewportSize({width,height:1000});await page.goto('/settings#notifications')
  await page.getByRole('button',{name:'Operations',exact:true}).click()
  const sheet=page.getByRole('dialog',{name:zh?'配置通知渠道':'Configure channel',exact:true})
  await sheet.getByRole('tab',{name:zh?'通知订阅':'Subscriptions',exact:true}).click()
  await sheet.getByText(zh?'消息模板与预览':'Message template and preview',{exact:true}).click()
  const preview=sheet.getByLabel(zh?'消息预览':'Message preview',{exact:true})
  const template=sheet.getByLabel(zh?'消息模板（可选）':'Message template (optional)',{exact:true})
  const scenarios=sheet.getByRole('group',{name:zh?'预览场景':'Preview scenario',exact:true})
  await expect(preview).toContainText(zh?'容器内存不足':'Container out of memory')
  await expect(preview).toContainText('redis')
  await expect(preview).toContainText('Production')
  await expect(preview).toContainText(provider==='webhook'?'2026-10-11 12:00':'2026-10-11 12:00 +08:00')
  if(provider!=='webhook')await expect(preview.locator('[data-card-status]')).toHaveAttribute('data-card-status','error')
  await expect(preview).not.toContainText('error')
  await scenarios.getByRole('button',{name:zh?'恢复':'Recovery',exact:true}).click()
  await expect(preview).toContainText(zh?'容器运行已稳定':'Container running stably')
  await scenarios.getByRole('button',{name:zh?'结果':'Result',exact:true}).click()
  await expect(preview).toContainText(zh?'发布完成':'Deployment completed')
  await scenarios.getByRole('button',{name:zh?'安全':'Security',exact:true}).click()
  await expect(preview).toContainText('IP 203.0.113.10')
  await template.fill('[Ops] {{title}}\n{{time}}\n{{url}}')
  await expect(preview).toContainText('2026-10-11 12:00:00 +08:00')
  await expect(preview).toContainText('https://suma.example/notifications#example')
  await sheet.getByRole('button',{name:zh?'保存渠道':'Save channel',exact:true}).click()
  await expect(sheet.getByRole('status',{name:zh?'保存状态':'Save status',exact:true})).toHaveText(zh?'已保存':'Saved')
  await sheet.getByRole('button',{name:zh?'恢复默认':'Restore default',exact:true}).click()
  await expect(template).toHaveValue('')
  await expect(preview).not.toContainText('[Ops]')
  await scenarios.getByRole('button',{name:zh?'退出':'Exit',exact:true}).click()
  await expect(preview).toContainText('137')
  if(provider!=='webhook') {
   await expect(preview).toContainText('SIGKILL')
   await expect(preview).toContainText(zh?'建议排查':'Suggested checks')
   await expect(preview.locator('[data-card-status]')).toHaveAttribute('data-card-status','warning')
   await expect(sheet.getByRole('button',{name:zh?'查看详情':'View details',exact:true})).toBeDisabled()
  } else await expect(sheet.getByRole('button',{name:zh?'查看详情':'View details',exact:true})).toHaveCount(0)
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
  if(provider==='feishu_app' && language==='zh-CN') {
   await page.setViewportSize({width,height:2200})
   await preview.screenshot({path:`/private/tmp/suma-notification-card-${theme}-${width}.png`})
   await page.setViewportSize({width,height:1000})
  }
  await sheet.getByRole('button',{name:zh?'保存并完成':'Save and finish',exact:true}).click()
  await expect(sheet).toBeHidden()
  const saved=await page.evaluate(()=>JSON.parse(sessionStorage.getItem('suma-demo-notifications-v2')||'{}'))
  expect(saved.channels[0].config.subscription.policy.template).toBe('')
  expect(saved.channels[0].config.subscription.rule_ids).toEqual(['faults'])
  expect(saved.deliveries).toHaveLength(0)
 })
}
