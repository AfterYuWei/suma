import { describe, expect, it } from 'vitest'
import { type LogRecord, mergeLogs } from './types'
const row = (id: string, text = 'same line'): LogRecord => ({ id, text, time: '2026-10-02T00:00:00Z', service: 'web', container_id: 'a', container_name: 'web-1', stream: 'stdout' })
describe('bounded project logs', () => {
 it('drops replayed identities while preserving identical independent lines', () => { expect(mergeLogs([row('a')], [row('a'), row('b')], 200).entries.map(r => r.id)).toEqual(['a', 'b']) })
 it('bounds aggregate lines and preserves the latest records', () => { const result = mergeLogs([], Array.from({ length: 300 }, (_, i) => row(String(i))), 100); expect(result.entries).toHaveLength(100); expect(result.entries[0].id).toBe('200'); expect(result.truncated).toBe(true) })
 it('bounds bytes independently of selected row count', () => { const result = mergeLogs([], Array.from({ length: 500 }, (_, i) => row(String(i), 'x'.repeat(65536))), 5000); expect(result.entries.length).toBeLessThan(64); expect(result.truncated).toBe(true) })
})
