import type { CDEnvironmentVariable } from './types'

const bytes = (value: string) => new TextEncoder().encode(value).length

export function parseEnvironmentInput(source: string, zh: boolean): CDEnvironmentVariable[] {
  const variables: CDEnvironmentVariable[] = []
  const names = new Set<string>()
  let size = 0
  for (const [index, raw] of source.split(/\r?\n/).entries()) {
    const line = raw.trim()
    if (!line || line.startsWith('#')) continue
    const fail = (message: string): never => { throw new Error(`${zh ? `第 ${index + 1} 行` : `Line ${index + 1}`}: ${message}`) }
    const match = /^(?:export[\t ]+)?([A-Za-z_][A-Za-z0-9_]{0,255})[\t ]*=(.*)$/.exec(line)
    if (!match) fail(zh ? '请使用 KEY=VALUE，名称不能以数字开头。' : 'Use KEY=VALUE with a name starting with a letter or underscore.')
    const [, name, rawValue] = match!
    if (names.has(name)) fail(zh ? '变量名称重复，请只保留一行。' : 'Duplicate variable name; keep only one entry.')
    if (/^(DOCKER_|COMPOSE_)/i.test(name) || /^(HOME|PATH|TMPDIR)$/i.test(name)) {
      fail(zh ? '该名称为执行器保留变量。' : 'This variable name is reserved for the runner.')
    }
    let value = rawValue.trim()
    if (value.startsWith('"') || value.startsWith("'")) {
      const quote = value[0]
      let end = 1
      while (end < value.length && value[end] !== quote) {
        end += value[end] === '\\' ? 2 : 1
      }
      const tail = value.slice(end + 1).trim()
      if (end >= value.length || tail && !tail.startsWith('#')) fail(zh ? '引号未闭合，或值后存在多余内容；每个变量请写在一行。' : 'Close the quote and remove trailing text; use one line per variable.')
      value = value.slice(1, end)
    } else {
      value = value.replace(/\s+#.*$/, '').trimEnd()
    }
    if (value.includes('\0') || bytes(value) > 65536) fail(zh ? '值含无效字符或超过 64 KiB。' : 'The value contains NUL or exceeds 64 KiB.')
    size += bytes(name) + bytes(value)
    if (size > 262144) fail(zh ? '名称和值合计不能超过 256 KiB。' : 'Names and values must total at most 256 KiB.')
    names.add(name)
    variables.push({ name, value })
    if (variables.length > 100) fail(zh ? '最多添加 100 个变量。' : 'At most 100 variables are allowed.')
  }
  return variables
}

export function planEnvironmentImport(current: CDEnvironmentVariable[], incoming: CDEnvironmentVariable[], replace: boolean, zh: boolean) {
  // Drop only untouched blank rows created by the single-variable button.
  const existing = current.filter(row => row.name || row.value !== undefined || row.has_value)
  const names = new Set(existing.map(row => row.name))
  if (names.size !== existing.length) throw new Error(zh ? '现有列表存在重名变量，请先修正。' : 'Fix duplicate names in the current list first.')
  const replacements = new Map(incoming.map(row => [row.name, row]))
  const added = incoming.filter(row => !names.has(row.name))
  const duplicates = incoming.length - added.length
  const variables = [...existing.map(row => replace && replacements.has(row.name) ? { ...row, value: replacements.get(row.name)!.value } : row), ...added]
  if (variables.length > 100) throw new Error(zh ? '合并后最多保留 100 个变量，请减少输入或删除现有项。' : 'The merged list exceeds 100 variables; remove existing entries or paste fewer variables.')
  if (variables.reduce((size, row) => size + bytes(row.name) + bytes(row.value ?? ''), 0) > 262144) throw new Error(zh ? '合并后的名称和值超过 256 KiB。' : 'Merged names and entered values exceed 256 KiB.')
  return { variables, added: added.length, replaced: replace ? duplicates : 0, kept: replace ? 0 : duplicates }
}
