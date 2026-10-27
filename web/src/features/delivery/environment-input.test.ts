import { describe, expect, it } from 'vitest'
import { parseEnvironmentInput, planEnvironmentImport } from './environment-input'

describe('delivery quick environment input', () => {
  it('accepts .env lines, comments, exports, empty values and quoted whitespace', () => {
    expect(parseEnvironmentInput('# config\r\n\r\n export APP_ENV = production # note\r\nEMPTY=\r\nSPACE="  two words  "\r\nSINGLE=\'inside # quote\' # note', false)).toEqual([
      { name: 'APP_ENV', value: 'production' }, { name: 'EMPTY', value: '' },
      { name: 'SPACE', value: '  two words  ' }, { name: 'SINGLE', value: 'inside # quote' },
    ])
  })
  it('preserves dollars, backslashes, equals and literal hash characters', () => {
    expect(parseEnvironmentInput('TOKEN=a=b=c#literal\nURL="${BASE}/$literal"\nPATH_VALUE=C:\\app\\data\nTEXT="literal\\nvalue"', false)).toEqual([
      { name: 'TOKEN', value: 'a=b=c#literal' }, { name: 'URL', value: '${BASE}/$literal' },
      { name: 'PATH_VALUE', value: 'C:\\app\\data' }, { name: 'TEXT', value: 'literal\\nvalue' },
    ])
    expect(parseEnvironmentInput('VALUE="quoted" # a "quoted" comment', false)).toEqual([{ name: 'VALUE', value: 'quoted' }])
  })
  it.each(['private-input-without-equals', '1BAD=private-input', 'A=private-input\nA=second', 'A="private-input', 'A="private-input" trailing', 'A=private-input\0', 'DOCKER_HOST=private-input', 'COMPOSE_FILE=private-input'])('rejects invalid source without repeating its private content (%#)', source => {
    for (const zh of [true, false]) {
      expect(() => parseEnvironmentInput(source, zh)).toThrow(zh ? /第 \d+ 行/ : /Line \d+/)
      try { parseEnvironmentInput(source, zh) } catch (error) { expect((error as Error).message).not.toContain('private-input') }
    }
  })
  it('enforces count and UTF-8 byte limits', () => {
    expect(() => parseEnvironmentInput(Array.from({ length: 101 }, (_, i) => `KEY_${i}=value`).join('\n'), false)).toThrow('100')
    expect(() => parseEnvironmentInput(`VALUE=${'中'.repeat(22000)}`, false)).toThrow('64 KiB')
    expect(() => parseEnvironmentInput(Array.from({ length: 4 }, (_, i) => `KEY_${i}=${'a'.repeat(65536)}`).join('\n'), false)).toThrow('256 KiB')
  })
  it('keeps saved values by default and replaces them only when selected', () => {
    const current = [{ name: 'PASSWORD', has_value: true }, { name: 'KEEP', value: 'draft' }, { name: '', value: undefined }]
    const incoming = parseEnvironmentInput('PASSWORD=replace-me\nNEW=\n', false)
    const keep = planEnvironmentImport(current, incoming, false, false)
    expect(keep).toEqual({ variables: [{ name: 'PASSWORD', has_value: true }, { name: 'KEEP', value: 'draft' }, { name: 'NEW', value: '' }], added: 1, replaced: 0, kept: 1 })
    const replace = planEnvironmentImport(current, incoming, true, false)
    expect(replace.variables[0]).toEqual({ name: 'PASSWORD', has_value: true, value: 'replace-me' })
    expect(replace).toMatchObject({ added: 1, replaced: 1, kept: 0 })
    expect(current[0]).toEqual({ name: 'PASSWORD', has_value: true })
  })
  it('rejects an overflowing merge and permits replacement at the limit', () => {
    const current = Array.from({ length: 100 }, (_, i) => ({ name: `KEY_${i}`, has_value: true }))
    expect(() => planEnvironmentImport(current, [{ name: 'NEW', value: 'value' }], false, false)).toThrow('100')
    expect(planEnvironmentImport(current, [{ name: 'KEY_0', value: 'new' }], true, false).variables).toHaveLength(100)
    expect(() => planEnvironmentImport([{ name: 'DUP' }, { name: 'DUP' }], [], false, false)).toThrow('duplicate')
  })
})
