import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import { defaultNotificationText, notificationCard, notificationTimestamp, templateExample } from './presentation'
import type { NotificationCard } from './presentation'
import type { NotificationEvent } from './types'

const cases=JSON.parse(readFileSync(new URL('../../../../server/internal/notification/testdata/messages.json',import.meta.url),'utf8')) as {event:NotificationEvent;timezone:string;zh:string;en:string}[]
const cards=JSON.parse(readFileSync(new URL('../../../../server/internal/notification/testdata/cards.json',import.meta.url),'utf8')) as {event:NotificationEvent;timestamp:string;zh:NotificationCard;en:NotificationCard}[]
describe('notification preview and inbox follow the delivery wording contract',()=>{
 for(const sample of cards) for(const [language,expected] of [['zh-CN',sample.zh],['en-US',sample.en]] as const) {
  it(`platform card ${sample.event.type} ${language}`,()=>expect(notificationCard(sample.event,language,sample.timestamp)).toEqual(expected))
 }
 for(const sample of cases) for(const [language,expected] of [['zh-CN',sample.zh],['en-US',sample.en]]) {
  it(`${sample.event.type} ${language} ${sample.event.title}`,()=>expect(defaultNotificationText(sample.event,language,notificationTimestamp(sample.event.time,sample.timezone))).toBe(expected))
 }
 it('uses the saved timezone through a DST transition',()=>expect(notificationTimestamp('2026-10-25T01:30:00Z','Europe/Berlin')).toBe('2026-10-25 02:30'))
 it('keeps default previews short and gives recovery its own wording',()=>{
  const fault=defaultNotificationText(templateExample('container.oom','zh-CN'),'zh-CN','2026-10-11 12:00')
  const recovery=defaultNotificationText(templateExample('container.recovered','zh-CN'),'zh-CN','2026-10-11 12:00')
  expect(fault).toBe('容器内存不足\nProduction · redis · 2026-10-11 12:00')
  expect(recovery).toContain('容器运行已稳定')
  expect(fault).not.toMatch(/\[error\]|container-example|oom exit=/)
 })
})
