import { expect, test } from '@playwright/test'

for(const language of ['zh-CN','en-US']) for(const theme of ['dark','light']) for(const width of [390,1440]){
 test(`registry access token guidance ${language} ${theme} ${width}`,async({page})=>{
  const zh=language==='zh-CN'
  await page.addInitScript(({language,theme})=>{sessionStorage.setItem('suma-demo-session','1');localStorage.setItem('suma-language',language);localStorage.setItem('suma-theme',theme)}, {language,theme})
  await page.setViewportSize({width,height:900});await page.goto('/authentication')
  await page.getByRole('tab',{name:zh?'镜像仓库':'Registries',exact:true}).click()
  await page.getByRole('button',{name:zh?'新增凭据':'New credential',exact:true}).click()
  const sheet=page.getByRole('dialog',{name:zh?'新增镜像仓库凭据':'New registry credential',exact:true})
  const type=sheet.getByRole('combobox').first(), basic=zh?'用户名 + 密码／访问令牌':'Username + password / access token',token=zh?'Docker 身份令牌':'Docker identity token'
  await expect(type).toContainText(basic)
  await expect(sheet.getByText(/CNB/)).toBeVisible()
  await expect(sheet.getByLabel(zh?'密码或访问令牌':'Password or access token',{exact:true})).toHaveAttribute('type','password')
  await type.click();const menu=page.getByRole('listbox');await expect(page.getByRole('option',{name:basic,exact:true})).toBeVisible()
  const trigger=await type.boundingBox(),popup=await menu.boundingBox()
  expect(trigger).toBeTruthy();expect(popup).toBeTruthy()
  expect(popup!.y).toBeGreaterThanOrEqual(trigger!.y+trigger!.height-1)
  await expect.poll(async()=>{
   const trigger=await type.boundingBox(),popup=await menu.boundingBox()
   return trigger&&popup?Math.max(Math.abs(popup.x-trigger.x),Math.abs(popup.width-trigger.width)):Infinity
  }).toBeLessThanOrEqual(2)
  await page.getByRole('option',{name:token,exact:true}).click()
  await expect(sheet.getByLabel(zh?'用户名':'Username',{exact:true})).toHaveCount(0)
  await expect(sheet.getByLabel(token,{exact:true})).toHaveAttribute('type','password')
  await type.click();await page.getByRole('option',{name:basic,exact:true}).click()
  await expect(sheet.getByLabel(zh?'用户名':'Username',{exact:true})).toBeVisible()
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
  if(zh&&theme==='dark')await sheet.screenshot({path:`/private/tmp/suma-registry-guidance-${width}.png`})
 })
}
