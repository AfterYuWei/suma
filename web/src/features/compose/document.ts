import {
  Document,
  isAlias,
  isMap,
  isScalar,
  isSeq,
  parseDocument,
  Scalar,
  type Node
} from 'yaml'

export type ConfigPath = (string | number)[]
export const REMOVE = Symbol('remove')
export const sensitiveKey = (key: string) =>
  /password|passwd|secret|token|credential|private.?key|api.?key|access.?key|auth/i.test(
    key
  )

export function parseCompose(source: string) {
  return parseDocument(source, {
    keepSourceTokens: true,
    uniqueKeys: true,
    logLevel: 'silent'
  })
}
export function composeProblems(
  source: string
): { message: string; line: number; column: number }[] {
  const document = parseCompose(source)
  return document.errors.map((error) => ({
    message: `YAML: ${error.code}`,
    line: error.linePos?.[0]?.line ?? 1,
    column: error.linePos?.[0]?.col ?? 1
  }))
}
export function configValue(source: string, path: ConfigPath): unknown {
  const doc = parseCompose(source)
  if (doc.errors.length) return undefined
  const project = (node: unknown): unknown => {
    if (isScalar(node)) return node.value
    if (isMap(node))
      return Object.fromEntries(
        node.items
          .filter((item) => isScalar(item.key))
          .map((item) => [
            String((item.key as Scalar).value),
            project(item.value)
          ])
      )
    if (isSeq(node)) return node.items.map(project)
    return undefined
  }
  return project(doc.getIn(path, true))
}
export function configKeys(source: string, path: ConfigPath): string[] {
  const node = parseCompose(source).getIn(path, true)
  return isMap(node)
    ? node.items.map((item) => String(isScalar(item.key) ? item.key.value : ''))
    : []
}

export function configurationIssues(
  source: string
): { path: ConfigPath; code: 'image' | 'port' | 'mount' | 'dependency' }[] {
  const issues: ReturnType<typeof configurationIssues> = []
  const services = configKeys(source, ['services'])
  for (const name of services) {
    const path = ['services', name]
    if (readonlyReason(source, path)) continue
    const image = configValue(source, [...path, 'image'])
    if (
      (image === undefined || image === '') &&
      configValue(source, [...path, 'build']) === undefined
    )
      issues.push({ path: [...path, 'image'], code: 'image' })
    const ports = configValue(source, [...path, 'ports'])
    if (Array.isArray(ports))
      ports.forEach((port, i) => {
        if (port && typeof port === 'object' && 'target' in port) {
          const target = String(port.target)
          if (
            !target.includes('$') &&
            (!/^\d+$/.test(target) ||
              Number(target) < 1 ||
              Number(target) > 65535)
          )
            issues.push({ path: [...path, 'ports', i, 'target'], code: 'port' })
        }
      })
    const mounts = configValue(source, [...path, 'volumes'])
    if (Array.isArray(mounts))
      mounts.forEach((mount, i) => {
        if (
          mount &&
          typeof mount === 'object' &&
          'target' in mount &&
          typeof mount.target === 'string' &&
          !mount.target.startsWith('/') &&
          !mount.target.includes('$')
        )
          issues.push({
            path: [...path, 'volumes', i, 'target'],
            code: 'mount'
          })
      })
    const dependencies = configValue(source, [...path, 'depends_on'])
    const names = Array.isArray(dependencies)
      ? dependencies
      : dependencies && typeof dependencies === 'object'
        ? Object.keys(dependencies)
        : []
    for (const dependency of names)
      if (
        typeof dependency === 'string' &&
        (!services.includes(dependency) || dependency === name)
      )
        issues.push({ path: [...path, 'depends_on'], code: 'dependency' })
  }
  return issues
}
function shared(node: unknown): boolean {
  return (
    isAlias(node) ||
    ((isMap(node) || isSeq(node) || isScalar(node)) && !!node.anchor) ||
    (isMap(node) &&
      node.items.some((item) => isScalar(item.key) && item.key.value === '<<'))
  )
}
function sharedDescendant(node: unknown): boolean {
  if (shared(node)) return true
  if (isMap(node))
    return node.items.some((item) => sharedDescendant(item.value))
  if (isSeq(node)) return node.items.some(sharedDescendant)
  return false
}
export function readonlyReason(
  source: string,
  path: ConfigPath
): 'syntax' | 'shared' | 'shape' | undefined {
  const doc = parseCompose(source)
  if (doc.errors.length || !isMap(doc.contents)) return 'syntax'
  for (let i = 0; i <= path.length; i++) {
    const node = doc.getIn(path.slice(0, i), true)
    if (shared(node)) return 'shared'
    if (
      i < path.length &&
      node != null &&
      !isMap(node) &&
      !isSeq(node) &&
      !(isScalar(node) && node.value === null)
    )
      return 'shape'
  }
  return undefined
}

export function readonlyGroupReason(source: string, path: ConfigPath) {
  return (
    readonlyReason(source, path) ||
    (sharedDescendant(parseCompose(source).getIn(path, true))
      ? 'shared'
      : undefined)
  )
}

function updateNode(doc: Document, existing: unknown, value: unknown): Node {
  if (
    isMap(existing) &&
    value &&
    typeof value === 'object' &&
    !Array.isArray(value)
  ) {
    const next = value as Record<string, unknown>
    for (const pair of [...existing.items])
      if (isScalar(pair.key) && !(String(pair.key.value) in next))
        existing.delete(pair.key.value)
    for (const [key, child] of Object.entries(next))
      existing.set(key, updateNode(doc, existing.get(key, true), child))
    return existing
  }
  if (isSeq(existing) && Array.isArray(value)) {
    existing.items = value.map((child, i) =>
      updateNode(doc, existing.items[i], child)
    )
    return existing
  }
  const next = doc.createNode(value)
  if (
    isScalar(existing) &&
    isScalar(next) &&
    typeof existing.value === typeof value
  )
    next.type = existing.type
  if (isMap(existing) || isSeq(existing) || isScalar(existing)) {
    next.commentBefore = existing.commentBefore
    next.comment = existing.comment
  }
  return next
}

// Render only the smallest changed node. The rest of the original source is
// copied verbatim; AST children keep unknown keys, scalar styles and comments.
function replaceNode(source: string, old: Node, next: Node): string {
  if (!old.range) throw new Error('Source range unavailable')
  const [start, end] = old.range
  const lineStart = source.lastIndexOf('\n', start - 1) + 1
  const prefix = source.slice(lineStart, start)
  const indent = prefix.match(/^ */)?.[0].length ?? 0
  const before = next.commentBefore,
    after = next.comment
  next.commentBefore = undefined
  next.comment = undefined
  const fragment = new Document()
  fragment.contents = next
  let text = fragment
    .toString({ lineWidth: 0, verifyAliasOrder: false })
    .trimEnd()
  next.commentBefore = before
  next.comment = after
  const block = isMap(next) || isSeq(next)
  if (block && text.includes('\n') && prefix.trim()) {
    text =
      '\n' +
      text
        .split('\n')
        .map((line) => ' '.repeat(indent + 2) + line)
        .join('\n')
  } else {
    text = text
      .split('\n')
      .map((line, i) => (i === 0 ? line : ' '.repeat(indent) + line))
      .join('\n')
  }
  if (source.slice(start, end).endsWith('\n')) text += '\n'
  return source.slice(0, start) + text + source.slice(end)
}

export function editConfig(
  source: string,
  path: ConfigPath,
  value: unknown | typeof REMOVE
): string {
  if (readonlyReason(source, path))
    throw new Error('Edit shared or complex configuration in Compose source')
  const doc = parseCompose(source)
  const existing = doc.getIn(path, true)
  if (sharedDescendant(existing))
    throw new Error('Edit shared configuration in Compose source')
  if (value === REMOVE && existing === undefined) return source
  if (
    value !== REMOVE &&
    isScalar(existing) &&
    (value == null || typeof value !== 'object')
  ) {
    const next = new Scalar(value)
    if (typeof value === 'string' && typeof existing.value === 'string')
      next.type = existing.type
    return replaceNode(source, existing, next)
  }
  // Structural edits operate on the nearest existing collection.
  let parent = path.slice(0, -1)
  while (
    parent.length &&
    !isMap(doc.getIn(parent, true)) &&
    !isSeq(doc.getIn(parent, true))
  )
    parent = parent.slice(0, -1)
  const old = doc.getIn(parent, true)
  if (!isMap(old) && !isSeq(old)) throw new Error('Compose must be a mapping')
  const original = parseCompose(source).getIn(parent, true) as Node
  for (let i = 1; i < path.length; i++) {
    const prefix = path.slice(0, i)
    const node = doc.getIn(prefix, true)
    if (isScalar(node) && node.value === null) {
      const replacement = doc.createNode(typeof path[i] === 'number' ? [] : {})
      replacement.commentBefore = node.commentBefore
      replacement.comment = node.comment
      doc.setIn(prefix, replacement)
    }
  }
  if (value === REMOVE) doc.deleteIn(path)
  else doc.setIn(path, updateNode(doc, existing, value))
  const next = doc.getIn(parent, true) as Node
  return replaceNode(source, original, next)
}

export function sourceLine(source: string, path: ConfigPath): number {
  const doc = parseCompose(source)
  let current = path
  let node = doc.getIn(current, true)
  while (node === undefined && current.length) {
    current = current.slice(0, -1)
    node = doc.getIn(current, true)
  }
  const offset =
    isMap(node) || isSeq(node) || isScalar(node) || isAlias(node)
      ? (node.range?.[0] ?? 0)
      : 0
  return source.slice(0, offset).split('\n').length
}
export function normalizeImage(image: string): string {
  const value = image.trim()
  // Interpolation may resolve to a full reference; digests never use a tag.
  if (!value || value.includes('$') || value.includes('@')) return value
  return value.slice(value.lastIndexOf('/') + 1).includes(':') ? value : `${value}:latest`
}

export function unnamedServiceNumber(name: string): number | undefined {
  const match = name.match(/^unnamed-([1-9]\d*)$/)
  const number = match ? Number(match[1]) : undefined
  return number !== undefined && Number.isSafeInteger(number) ? number : undefined
}

export function nextUnnamedServiceName(source: string, minimum = 1): string {
  const numbers = configKeys(source, ['services']).map(unnamedServiceNumber).filter((number): number is number => number !== undefined)
  const number = Math.max(minimum, 1, ...numbers.map(number => number + 1))
  return `unnamed-${number}`
}

export function addService(
  source: string,
  name: string,
  image = ''
): string {
  if (!/^[a-zA-Z0-9][a-zA-Z0-9_.-]*$/.test(name))
    throw new Error('Invalid service name')
  if (configKeys(source, ['services']).includes(name))
    throw new Error('Service name already exists')
  return editConfig(source, ['services', name], image.trim() ? { image: normalizeImage(image) } : {})
}

export function parseServicePort(value: unknown): Record<string, unknown> | undefined {
  if (value && typeof value === 'object' && !Array.isArray(value)) return value as Record<string, unknown>
  if (typeof value !== 'string' && typeof value !== 'number') return undefined
  if (typeof value === 'string' && value.includes('$')) return undefined
  const match = String(value).match(/^(?:(\[[^\]]+\]|[^:]+):)?(?:(\d+(?:-\d+)?):)?(\d+)(?:\/(tcp|udp|sctp))?$/)
  if (!match) return undefined
  let host: string | undefined = match[1], published: string | undefined = match[2]
  if (host && !published && /^\d+(?:-\d+)?$/.test(host)) { published = host; host = undefined }
  return { target: Number(match[3]), ...(published ? { published } : {}), ...(host ? { host_ip: host.replace(/^\[|\]$/g, '') } : {}), ...(match[4] ? { protocol: match[4] } : {}) }
}

export function configureServicePort(source: string, service: string, port: Record<string, unknown>, index?: number): string {
  const path = ['services', service, 'ports']
  if (!configKeys(source, ['services']).includes(service)) throw new Error('Service does not exist')
  if (readonlyGroupReason(source, path)) throw new Error('Shared ports must be edited in source')
  const raw = configValue(source, path)
  if (raw !== undefined && !Array.isArray(raw)) throw new Error('Complex ports must be edited in source')
  if (!Number.isInteger(port.target) || Number(port.target) < 1 || Number(port.target) > 65535) throw new Error('Container port must be between 1 and 65535')
  if (port.published !== undefined) {
    const match = String(port.published).match(/^(\d+)(?:-(\d+))?$/)
    if (!match || Number(match[1]) < 1 || Number(match[1]) > 65535 || (match[2] && (Number(match[2]) < Number(match[1]) || Number(match[2]) > 65535))) throw new Error('Host port must be between 1 and 65535, or an ascending range')
  }
  if (port.protocol !== undefined && !['tcp', 'udp', 'sctp'].includes(String(port.protocol))) throw new Error('Invalid port protocol')
  const rows = Array.isArray(raw) ? raw : []
  if (index !== undefined && (index < 0 || index >= rows.length)) throw new Error('Port mapping no longer exists')
  if (rows.some((value, i) => {
    if (i === index) return false
    const other = parseServicePort(value)
    return other && String(other.target) === String(port.target) && String(other.published ?? '') === String(port.published ?? '') && (other.host_ip || '') === (port.host_ip || '') && (other.protocol || 'tcp') === (port.protocol || 'tcp')
  })) throw new Error('Port mapping already exists')
  return index === undefined ? editConfig(source, path, [...rows, port]) : editConfig(source, [...path, index], port)
}

export interface ProjectResourceChoice {
  kind: 'project' | 'existing' | 'new'
  name: string
}

export function resolveProjectResource(
  source: string,
  section: 'networks' | 'volumes',
  choice: ProjectResourceChoice
): { source: string; name: string } {
  const name = choice.name.trim()
  const keys = configKeys(source, [section])
  if (choice.kind === 'project') {
    if (!keys.includes(name)) throw new Error('Select a declared project resource')
    return { source, name }
  }
  if (choice.kind === 'new') {
    if (!/^[a-zA-Z0-9][a-zA-Z0-9_.-]*$/.test(name) || keys.includes(name) || (section === 'networks' && name === 'default'))
      throw new Error('Invalid or duplicate resource name')
    return { source: editConfig(source, [section, name], {}), name }
  }
  if (!name || name.includes('$')) throw new Error('Select an existing node resource')
  const match = keys.find((key) => {
    const value = configValue(source, [section, key]) as Record<string, unknown> | undefined
    return value?.external === true && (value.name ?? key) === name
  })
  if (match) return { source, name: match }
  let base = name.replace(/[^a-zA-Z0-9_.-]/g, '_')
  if (!/^[a-zA-Z0-9]/.test(base) || (section === 'networks' && base === 'default'))
    base = `${section === 'networks' ? 'network' : 'volume'}_${base}`
  let key = base, suffix = 2
  while (keys.includes(key)) key = `${base}_${suffix++}`
  return { source: editConfig(source, [section, key], { external: true, name }), name: key }
}

export function connectServiceNetwork(
  source: string,
  service: string,
  choice: ProjectResourceChoice,
  keepDefault: boolean
): string {
  const path = ['services', service, 'networks']
  if (!configKeys(source, ['services']).includes(service)) throw new Error('Service does not exist')
  if (configValue(source, ['services', service, 'network_mode']) !== undefined)
    throw new Error('Edit network_mode in source before attaching networks')
  if (readonlyGroupReason(source, path)) throw new Error('Shared network configuration must be edited in source')
  const raw = configValue(source, path)
  if (raw != null && (typeof raw !== 'object' || (Array.isArray(raw) && raw.some(item => typeof item !== 'string'))))
    throw new Error('Complex network configuration must be edited in source')
  const resource = resolveProjectResource(source, 'networks', choice)
  let nextSource = resource.source
  if (keepDefault && !configKeys(nextSource, ['networks']).includes('default'))
    nextSource = editConfig(nextSource, ['networks', 'default'], {})
  if (Array.isArray(raw)) {
    const next = keepDefault ? [...raw] : raw.filter(name => name !== 'default')
    if (keepDefault && !next.includes('default')) next.push('default')
    if (!next.includes(resource.name)) next.push(resource.name)
    return editConfig(nextSource, path, next)
  }
  const next = { ...(raw as Record<string, unknown> | undefined) }
  if (keepDefault) next.default = next.default ?? null
  else delete next.default
  if (!(resource.name in next)) next[resource.name] = null
  return editConfig(nextSource, path, next)
}

export function configureServiceMount(
  source: string,
  service: string,
  mount: Record<string, unknown>,
  resource?: ProjectResourceChoice,
  index?: number
): string {
  const path = ['services', service, 'volumes']
  if (!configKeys(source, ['services']).includes(service)) throw new Error('Service does not exist')
  if (readonlyGroupReason(source, path)) throw new Error('Shared mount configuration must be edited in source')
  const raw = configValue(source, path)
  if (raw !== undefined && !Array.isArray(raw)) throw new Error('Complex mount configuration must be edited in source')
  if (typeof mount.target !== 'string' || !mount.target.startsWith('/'))
    throw new Error('Container path must be absolute')
  const rows = Array.isArray(raw) ? raw : []
  if (index !== undefined && (index < 0 || index >= rows.length)) throw new Error('Mount no longer exists')
  if (rows.some((item, i) => i !== index && (typeof item === 'string' ? item.split(':').slice(item.includes(':') ? 1 : 0, item.includes(':') ? 2 : 1)[0] : (item as Record<string, unknown> | null)?.target) === mount.target))
    throw new Error('Container path already has a mount')
  let nextSource = source
  const next = { ...mount }
  if (mount.type === 'volume' && resource) {
    const resolved = resolveProjectResource(source, 'volumes', resource)
    nextSource = resolved.source
    next.source = resolved.name
  }
  if (index !== undefined) return editConfig(nextSource, [...path, index], next)
  return editConfig(nextSource, path, [...rows, next])
}

export function references(
  source: string,
  section: 'services' | 'networks' | 'volumes',
  name: string
): string[] {
  const doc = parseCompose(source)
  const found = new Set<string>()
  for (const service of configKeys(source, ['services'])) {
    const raw = doc.getIn(['services', service], true)
    if (!isMap(raw)) {
      found.add(service)
      continue
    }
    const value = configValue(source, ['services', service]) as
      Record<string, unknown> | undefined
    if (!value) {
      found.add(service)
      continue
    }
    if (readonlyReason(source, ['services', service])) {
      found.add(service)
      continue
    }
    const resourceFields =
      section === 'services'
        ? [
            'depends_on',
            'network_mode',
            'ipc',
            'pid',
            'volumes_from',
            'extends'
          ]
        : section === 'networks'
          ? ['networks']
          : ['volumes']
    if (
      resourceFields.some((field) =>
        sharedDescendant(doc.getIn(['services', service, field], true))
      )
    ) {
      found.add(service)
      continue
    }
    if (section === 'services') {
      const depends = value.depends_on
      if (
        (Array.isArray(depends) && depends.includes(name)) ||
        (depends && typeof depends === 'object' && name in depends)
      )
        found.add(service)
      for (const field of ['network_mode', 'ipc', 'pid'])
        if (value[field] === `service:${name}`) found.add(service)
      if (
        Array.isArray(value.volumes_from) &&
        value.volumes_from.some(
          (item) => typeof item === 'string' && item.split(':')[0] === name
        )
      )
        found.add(service)
      if (
        value.extends &&
        typeof value.extends === 'object' &&
        (value.extends as Record<string, unknown>).service === name
      )
        found.add(service)
    } else if (section === 'networks') {
      const networks = value.networks
      if (
        (Array.isArray(networks) && networks.includes(name)) ||
        (networks && typeof networks === 'object' && name in networks)
      )
        found.add(service)
    } else if (Array.isArray(value.volumes)) {
      if (
        value.volumes.some((mount) =>
          typeof mount === 'string'
            ? mount.split(':')[0] === name
            : mount &&
              typeof mount === 'object' &&
              (mount as Record<string, unknown>).source === name
        )
      )
        found.add(service)
    }
  }
  return [...found]
}
export function renameService(
  source: string,
  from: string,
  to: string
): string {
  if (
    !/^[a-zA-Z0-9][a-zA-Z0-9_.-]*$/.test(to) ||
    configKeys(source, ['services']).includes(to)
  )
    throw new Error('Invalid or duplicate service name')
  if (readonlyReason(source, ['services', from]))
    throw new Error('Rename shared configuration in source')
  for (const name of configKeys(source, ['services']))
    if (readonlyReason(source, ['services', name]))
      throw new Error('Shared references must be renamed in source')
  for (const name of configKeys(source, ['services']))
    for (const field of ['network_mode', 'ipc', 'pid']) {
      const value = configValue(source, ['services', name, field])
      if (
        typeof value === 'string' &&
        value.startsWith('service:') &&
        value.includes('$')
      )
        throw new Error(
          'Interpolated service references must be renamed in source'
        )
    }
  const doc = parseCompose(source),
    services = doc.get('services', true)
  if (!isMap(services)) throw new Error('Services unavailable')
  const pair = services.items.find(
    (item) => isScalar(item.key) && item.key.value === from
  )
  if (!pair || !isScalar(pair.key)) throw new Error('Service unavailable')
  let result = replaceNode(source, pair.key, new Scalar(to))
  for (const name of configKeys(result, ['services'])) {
    const base = ['services', name]
    const depends = configValue(result, [...base, 'depends_on'])
    if (Array.isArray(depends))
      result = editConfig(
        result,
        [...base, 'depends_on'],
        depends.map((item) => (item === from ? to : item))
      )
    else if (depends && typeof depends === 'object' && from in depends) {
      const mapping = parseCompose(result).getIn([...base, 'depends_on'], true)
      if (isMap(mapping)) {
        const item = mapping.items.find(
          (entry) => isScalar(entry.key) && entry.key.value === from
        )
        if (item && isScalar(item.key))
          result = replaceNode(result, item.key, new Scalar(to))
      }
    }
    for (const field of ['network_mode', 'ipc', 'pid'])
      if (configValue(result, [...base, field]) === `service:${from}`)
        result = editConfig(result, [...base, field], `service:${to}`)
    const volumes = configValue(result, [...base, 'volumes_from'])
    if (Array.isArray(volumes))
      result = editConfig(
        result,
        [...base, 'volumes_from'],
        volumes.map((item) =>
          typeof item === 'string' && item.split(':')[0] === from
            ? to + item.slice(from.length)
            : item
        )
      )
    if (configValue(result, [...base, 'extends', 'service']) === from) {
      if (configValue(result, [...base, 'extends', 'file']))
        throw new Error('External extends references must be renamed in source')
      result = editConfig(result, [...base, 'extends', 'service'], to)
    }
  }
  return result
}

export interface EnvEntry {
  key: string
  value: string
  raw: string
  start: number
  end: number
  editable: boolean
}
export function envEntries(source: string): EnvEntry[] {
  const result: EnvEntry[] = []
  const expression =
    /^(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=([^\r\n]*)(?:\r?\n|$)/gm
  let match: RegExpExecArray | null
  while ((match = expression.exec(source))) {
    let raw = match[2].trim()
    const quote = raw[0]
    let value = raw,
      editable = true
    if (quote === '"' || quote === "'") {
      const closed = raw.match(
        quote === '"'
          ? /^"((?:\\.|[^"\\])*)"\s*(?:#.*)?$/
          : /^'((?:\\.|[^'\\])*)'\s*(?:#.*)?$/
      )
      if (!closed) {
        const valueStart =
          match.index +
          match[0].indexOf('=') +
          1 +
          match[2].length -
          match[2].trimStart().length
        let endQuote = -1
        for (let i = valueStart + 1; i < source.length; i++) {
          if (source[i] === '\\') {
            i++
            continue
          }
          if (source[i] === quote) {
            endQuote = i
            break
          }
        }
        const nextLine = endQuote < 0 ? -1 : source.indexOf('\n', endQuote)
        expression.lastIndex = nextLine < 0 ? source.length : nextLine + 1
        raw = source.slice(valueStart, expression.lastIndex).trimEnd()
        value = raw
      }
      if (!closed || /\\|\$/.test(closed[1])) editable = false
      else value = closed[1]
    } else {
      value = raw.replace(/\s+#.*$/, '')
      if (/\$|\\/.test(value)) editable = false
    }
    result.push({
      key: match[1],
      value,
      raw,
      start: match.index,
      end: expression.lastIndex,
      editable
    })
    if (!match[0].length) break
  }
  const counts = new Map<string, number>()
  for (const entry of result)
    counts.set(entry.key, (counts.get(entry.key) ?? 0) + 1)
  return result.map((entry) => ({
    ...entry,
    editable: entry.editable && counts.get(entry.key) === 1
  }))
}
export function setEnv(
  source: string,
  key: string,
  value: string | typeof REMOVE
): string {
  if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(key))
    throw new Error('Invalid variable name')
  const entry = envEntries(source).find((item) => item.key === key)
  if (entry && !entry.editable)
    throw new Error('Edit complex or duplicate assignments in .env source')
  if (value === REMOVE)
    return entry
      ? source.slice(0, entry.start) + source.slice(entry.end)
      : source
  if (
    !entry &&
    envEntries(source).some((item) => {
      const quote = item.raw[0]
      return (
        (quote === '"' || quote === "'") &&
        !(quote === '"' ? /^"(?:\\.|[^"\\])*"/ : /^'(?:\\.|[^'\\])*'/).test(
          item.raw
        )
      )
    })
  )
    throw new Error(
      'Fix the unterminated quoted assignment in .env source first'
    )
  if (/[\r\n]/.test(value))
    throw new Error('Edit multiline values in .env source')
  const encoded = /^[A-Za-z0-9_./:@+-]*$/.test(value)
    ? value
    : `'${value.replaceAll("'", "\\'")}'`
  if (!entry)
    return (
      source +
      (source && !source.endsWith('\n') ? '\n' : '') +
      `${key}=${encoded}\n`
    )
  const assignment = source.slice(entry.start, entry.end)
  const prefix = assignment.slice(0, assignment.indexOf('=') + 1)
  const quote = entry.raw[0]
  const preserved =
    quote === '"'
      ? `"${value.replaceAll('\\', '\\\\').replaceAll('"', '\\"').replaceAll('$', '\\$')}"`
      : quote === "'"
        ? `'${value.replaceAll("'", "\\'")}'`
        : encoded
  const comment =
    (quote === '"' || quote === "'"
      ? entry.raw.match(
          /^(?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*')(\s+#.*)?$/
        )?.[1]
      : entry.raw.match(/\s+#.*$/)?.[0]) ?? ''
  const newline = assignment.endsWith('\r\n')
    ? '\r\n'
    : assignment.endsWith('\n')
      ? '\n'
      : ''
  return (
    source.slice(0, entry.start) +
    prefix +
    preserved +
    comment +
    newline +
    source.slice(entry.end)
  )
}
export function variableReferences(source: string, key: string): string[] {
  return configKeys(source, ['services']).filter((service) => {
    const value = parseCompose(source).getIn(['services', service], true)
    return (
      (value != null && String(value).includes('${' + key + '}')) ||
      (value != null &&
        new RegExp(`\\$\\{${key}(?=[:?+-])|\\$${key}(?![A-Za-z0-9_])`).test(
          String(value)
        ))
    )
  })
}
export function maskCompose(source: string): string {
  const doc = parseCompose(source)
  if (doc.errors.length)
    return '# Invalid YAML — reveal source to inspect / YAML 无效，请显示源码检查\n'
  const patches: { start: number; end: number }[] = []
  const resolving = new Set<unknown>()
  const visit = (node: unknown, secret = false) => {
    if (isAlias(node) && secret && !resolving.has(node)) {
      resolving.add(node)
      visit(node.resolve(doc), true)
      resolving.delete(node)
    } else if (isScalar(node) && node.range) {
      if (
        secret ||
        (typeof node.value === 'string' &&
          /^([^=]+)=/.test(node.value) &&
          sensitiveKey(node.value.split('=')[0]))
      )
        patches.push({ start: node.range[0], end: node.range[1] })
    } else if (isMap(node))
      for (const pair of node.items)
        visit(
          pair.value,
          secret || (isScalar(pair.key) && sensitiveKey(String(pair.key.value)))
        )
    else if (isSeq(node)) for (const item of node.items) visit(item, secret)
  }
  visit(doc.contents)
  let result = source
  for (const patch of patches
    .filter(
      (item, index) =>
        patches.findIndex((other) => other.start === item.start) === index
    )
    .sort((a, b) => b.start - a.start))
    result =
      result.slice(0, patch.start) + '"••••••••"' + result.slice(patch.end)
  return result
}
export function maskEnvironment(source: string): string {
  let result = source
  for (const entry of envEntries(source).reverse())
    if (sensitiveKey(entry.key)) {
      if (
        !entry.editable &&
        /^['"]/.test(entry.raw) &&
        !new RegExp(`^${entry.raw[0]}.*${entry.raw[0]}(?:\\s*#.*)?$`).test(
          entry.raw
        )
      )
        return '# Complex sensitive value — reveal .env source / 请显示 .env 源码\n'
      result =
        result.slice(0, entry.start) +
        `${entry.key}=••••••••\n` +
        result.slice(entry.end)
    }
  return result
}
