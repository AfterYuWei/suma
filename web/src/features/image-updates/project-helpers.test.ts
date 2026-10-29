import { describe, expect, it } from 'vitest'
import { candidateStatus, generateCron } from './project-helpers'
describe('project image updates', () => {
 it('generates standard five-field schedules', () => {
  expect(generateCron('minutes', '15', '03:30', '1')).toBe('*/15 * * * *')
  expect(generateCron('hours', '6', '03:30', '1')).toBe('0 */6 * * *')
  expect(generateCron('daily', '6', '03:30', '1')).toBe('30 3 * * *')
  expect(generateCron('weekly', '6', '03:30', '1')).toBe('30 3 * * 1')
 })
 it('uses publishing directly, without an approved state', () => {
  expect(candidateStatus('pending', true)).toBe('等待发布')
  expect(candidateStatus('publishing', true)).toBe('发布中')
  expect(candidateStatus('superseded', false)).toContain('Version changed')
 })
})
