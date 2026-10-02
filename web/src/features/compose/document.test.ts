import { describe, expect, it } from 'vitest'
import {
  addService,
  composeProblems,
  configurationIssues,
  configValue,
  configureServiceMount,
  configureServicePort,
  connectServiceNetwork,
  normalizeImage,
  nextUnnamedServiceName,
  unnamedServiceNumber,
  parseServicePort,
  editConfig,
  envEntries,
  maskCompose,
  maskEnvironment,
  readonlyReason,
  readonlyGroupReason,
  references,
  REMOVE,
  renameService,
  resolveProjectResource,
  setEnv,
  variableReferences
} from './document'

describe('Service image and port forms', () => {
  it('creates unnamed services without default fields and skips existing generated names', () => {
    const source = '# retained\nservices:\n  api: {image: app, x-extra: keep} # keep service\n  unnamed-2: {}\n'
    const name = nextUnnamedServiceName(source)
    expect(name).toBe('unnamed-3')
    const changed = addService(source, name)
    expect(configValue(changed, ['services', name])).toEqual({})
    expect(changed).toContain('# keep service')
    expect(configValue(changed, ['services', 'api', 'x-extra'])).toBe('keep')
    expect(nextUnnamedServiceName('services: {}\n', 4)).toBe('unnamed-4')
    expect(unnamedServiceNumber(name)).toBe(3)
    expect(unnamedServiceNumber('unnamed-0')).toBeUndefined()
    expect(unnamedServiceNumber('api')).toBeUndefined()
  })
  it('preserves a configured unnamed service and its dependencies when assigning or clearing a name', () => {
    const source = '# project\nservices:\n  unnamed-1:\n    image: nginx:latest # image comment\n    restart: unless-stopped\n    ports: ["8080:80"]\n    x-extra: keep\n  worker:\n    image: busybox\n    depends_on: {unnamed-1: {condition: service_healthy, restart: true}}\n'
    const named = renameService(source, 'unnamed-1', 'api')
    expect(configValue(named, ['services', 'api'])).toEqual(configValue(source, ['services', 'unnamed-1']))
    expect(configValue(named, ['services', 'worker', 'depends_on', 'api'])).toEqual({condition: 'service_healthy', restart: true})
    expect(named).toContain('# image comment')
    const unnamed = renameService(named, 'api', nextUnnamedServiceName(named, 2))
    expect(configValue(unnamed, ['services', 'unnamed-2', 'ports'])).toEqual(['8080:80'])
    expect(configValue(unnamed, ['services', 'worker', 'depends_on', 'unnamed-2'])).toEqual({condition: 'service_healthy', restart: true})
    expect(unnamed).toContain('# image comment')
  })
  it('defaults untagged static references to latest without treating registry ports as tags', () => {
    expect(normalizeImage(' nginx ')).toBe('nginx:latest')
    expect(normalizeImage('registry.example:5000/team/app')).toBe('registry.example:5000/team/app:latest')
    expect(normalizeImage('registry.example:5000/team/app:1.2')).toBe('registry.example:5000/team/app:1.2')
    expect(normalizeImage('app@sha256:abc')).toBe('app@sha256:abc')
    expect(normalizeImage('${IMAGE}')).toBe('${IMAGE}')
    expect(normalizeImage('app:${TAG}')).toBe('app:${TAG}')
    expect(configValue(addService('services: {}\n', 'api', 'nginx'), ['services','api','image'])).toBe('nginx:latest')
  })
  it('parses host to container mappings, protocols, IPv6 and automatic host allocation', () => {
    expect(parseServicePort('8080:80')).toEqual({published:'8080',target:80})
    expect(parseServicePort('127.0.0.1:5353:53/udp')).toEqual({host_ip:'127.0.0.1',published:'5353',target:53,protocol:'udp'})
    expect(parseServicePort('[::1]:8080:80')).toEqual({host_ip:'::1',published:'8080',target:80})
    expect(parseServicePort('80')).toEqual({target:80})
    expect(parseServicePort('8080-8090:80')).toEqual({published:'8080-8090',target:80})
    expect(parseServicePort('${PORT}:80')).toBeUndefined()
  })
  it('preserves advanced port options, comments and other services when changing a mapping', () => {
    const source = '# ports\nservices:\n  api:\n    image: nginx\n    ports:\n      - target: 80\n        published: "8080" # keep port comment\n        app_protocol: http\n        name: web\n  db:\n    image: postgres\n    ports: ["5432:5432"] # keep service\n'
    const old = configValue(source, ['services','api','ports',0]) as Record<string, unknown>
    const next = configureServicePort(source, 'api', {...old, published:'8088'}, 0)
    expect(next).toContain('# keep port comment')
    expect(configValue(next, ['services','api','ports',0])).toEqual({...old,published:'8088'})
    expect(next.slice(next.indexOf('  db:'))).toBe(source.slice(source.indexOf('  db:')))
  })
  it('validates mappings without writing defaults, duplicating entries or expanding shared structures', () => {
    const source = 'services:\n  api: {image: nginx}\n'
    const next = configureServicePort(source, 'api', {target:80})
    expect(configValue(next, ['services','api','ports',0])).toEqual({target:80})
    expect(() => configureServicePort(next, 'api', {target:80})).toThrow('already exists')
    expect(() => configureServicePort(source, 'api', {target:0})).toThrow('Container port')
    expect(() => configureServicePort(source, 'api', {target:80,published:'65536'})).toThrow('Host port')
    expect(() => configureServicePort(source, 'api', {target:80,published:'8090-8080'})).toThrow('Host port')
    const shared = 'x-ports: &ports ["8080:80"]\nservices:\n  api: {image: nginx, ports: *ports}\n'
    expect(() => configureServicePort(shared, 'api', {target:80,published:'8088'})).toThrow('Shared')
  })
})

describe('Service resource configuration', () => {
  const source = `# project comment
services:
  api:
    image: app:1
    environment: {MODE: api}
    networks:
      private:
        aliases: [api-local] # preserve alias
        priority: 10
    volumes:
      - type: volume
        source: data
        target: /data
        volume: {nocopy: true} # preserve options
  worker:
    image: app:1 # preserve other service
    environment: {MODE: worker}
    networks: [private]
    volumes: ["data:/work:ro"]
    x-custom: {unchanged: true}
networks:
  private: {}
volumes:
  data: {}
`
  it('connects one service to an existing network without altering other services or network options', () => {
    const next = connectServiceNetwork(source, 'api', { kind: 'existing', name: 'node-shared' }, false)
    expect(configValue(next, ['networks', 'node-shared'])).toEqual({external: true, name: 'node-shared'})
    expect(configValue(next, ['services', 'api', 'networks', 'private'])).toEqual({aliases: ['api-local'], priority: 10})
    expect(configValue(next, ['services', 'worker'])).toEqual(configValue(source, ['services', 'worker']))
    expect(next).toContain('# preserve alias')
    expect(next.slice(next.indexOf('  worker:'), next.indexOf('networks:\n  private:'))).toBe(source.slice(source.indexOf('  worker:'), source.indexOf('networks:\n  private:')))
    expect(configValue(next, ['networks', 'default'])).toBeUndefined()
  })
  it('makes the default connection explicit only when selected and preserves list order', () => {
    const implicit = 'services:\n  api: {image: app}\n  worker: {image: app}\n'
    const keep = connectServiceNetwork(implicit, 'api', {kind: 'new', name: 'private'}, true)
    expect(configValue(keep, ['services', 'api', 'networks'])).toEqual({default: null, private: null})
    expect(configValue(keep, ['services', 'worker', 'networks'])).toBeUndefined()
    expect(configValue(keep, ['networks', 'default'])).toEqual({})
    const only = connectServiceNetwork(implicit, 'api', {kind: 'new', name: 'private'}, false)
    expect(configValue(only, ['services', 'api', 'networks'])).toEqual({private: null})
    expect(configValue(only, ['networks', 'default'])).toBeUndefined()
    const list = 'services:\n  api: {image: app, networks: [default, private]}\nnetworks: {default: {}, private: {}}\n'
    expect(configValue(connectServiceNetwork(list, 'api', {kind: 'new', name: 'extra'}, true), ['services','api','networks'])).toEqual(['default','private','extra'])
  })
  it('reuses external declarations and handles native-name collisions without changing the default network', () => {
    const declared = 'services: {}\nnetworks:\n  default: {}\n  shared: {external: true, name: native}\n  native: {}\n'
    expect(resolveProjectResource(declared, 'networks', {kind: 'existing', name: 'native'})).toEqual({source: declared, name: 'shared'})
    const collision = resolveProjectResource(declared, 'networks', {kind: 'existing', name: 'default'})
    expect(collision.name).toBe('network_default')
    expect(configValue(collision.source, ['networks', 'default'])).toEqual({})
    const duplicate = resolveProjectResource(source, 'networks', {kind: 'existing', name: 'private'})
    expect(duplicate.name).toBe('private_2')
    expect(configValue(duplicate.source, ['networks', 'private_2', 'external'])).toBe(true)
  })
  it('edits a service mount while keeping advanced options, comments and the neighboring service intact', () => {
    const original = configValue(source, ['services', 'api', 'volumes', 0]) as Record<string, unknown>
    const next = configureServiceMount(source, 'api', {...original, target: '/app/data', read_only: true}, undefined, 0)
    expect(configValue(next, ['services','api','volumes',0])).toEqual({...original, target: '/app/data', read_only: true})
    expect(next).toContain('# preserve options')
    expect(configValue(next, ['services','worker'])).toEqual(configValue(source, ['services','worker']))
    expect(configValue(next, ['volumes','data'])).toEqual({})
  })
  it('adds an external volume to one service and keeps declarations when a mount is removed', () => {
    const next = configureServiceMount(source, 'api', {type:'volume',target:'/cache'}, {kind:'existing',name:'node-cache'})
    expect(configValue(next, ['volumes','node-cache'])).toEqual({external:true,name:'node-cache'})
    expect(configValue(next, ['services','api','volumes',1])).toEqual({type:'volume',target:'/cache',source:'node-cache'})
    const removed = editConfig(next, ['services','api','volumes',1], REMOVE)
    expect(configValue(removed, ['volumes','node-cache'])).toEqual({external:true,name:'node-cache'})
    expect(configValue(removed, ['services','worker','volumes'])).toEqual(['data:/work:ro'])
  })
  it('rejects invalid targets, duplicate targets, shared structures and conflicting network modes before editing', () => {
    expect(() => configureServiceMount(source, 'api', {type:'volume',target:'/data'}, {kind:'new',name:'new-data'})).toThrow('already has a mount')
    expect(() => configureServiceMount(source, 'api', {type:'bind',target:'relative'})).toThrow('absolute')
    const shared = 'x-nets: &nets [private]\nservices:\n  api: {image: app, networks: *nets}\nnetworks: {private: {}}\n'
    expect(() => connectServiceNetwork(shared, 'api', {kind:'new',name:'new'}, true)).toThrow('Shared')
    const mode = 'services:\n  api: {image: app, network_mode: host}\n'
    expect(() => connectServiceNetwork(mode, 'api', {kind:'new',name:'new'}, true)).toThrow('network_mode')
    const mounts = 'x-mounts: &mounts ["data:/data"]\nservices:\n  api: {image: app, volumes: *mounts}\nvolumes: {data: {}}\n'
    expect(() => configureServiceMount(mounts, 'api', {type:'tmpfs',target:'/tmp'})).toThrow('Shared')
  })
})

describe('Compose source editing', () => {
  const source =
    '# project comment\nservices:\n  api:\n    image: "api:1" # keep inline\n    environment:\n      TOKEN: ${API_TOKEN}\n    x-custom: { thing: yes }\n  database:\n    image: postgres:18\n    deploy:\n      resources:\n        limits:\n          memory: 256M\n'
  it('changes one scalar without touching comments, unknown fields or other services', () => {
    const changed = editConfig(source, ['services', 'api', 'image'], 'api:2')
    expect(changed).toBe(source.replace('"api:1"', '"api:2"'))
    expect(composeProblems(changed)).toEqual([])
  })
  it('adds fields to existing and empty mappings without dropping unknown children', () => {
    const changed = editConfig(
      source,
      ['services', 'api', 'ports'],
      [{ target: 80, published: '8080' }]
    )
    expect(composeProblems(changed)).toEqual([])
    expect(configValue(changed, ['services', 'api', 'x-custom'])).toEqual({
      thing: 'yes'
    })
    expect(changed.slice(changed.indexOf('  database:'))).toBe(
      source.slice(source.indexOf('  database:'))
    )
    const first = addService('services: {}\n', 'app', 'nginx:alpine')
    expect(configValue(first, ['services', 'app', 'image'])).toBe(
      'nginx:alpine'
    )
    expect(composeProblems(first)).toEqual([])
  })
  it('preserves default, empty and argv command distinctions', () => {
    let changed = editConfig(source, ['services', 'api', 'command'], [])
    expect(configValue(changed, ['services', 'api', 'command'])).toEqual([])
    changed = editConfig(
      changed,
      ['services', 'api', 'command'],
      ['sh', '-c', 'echo $$HOME']
    )
    expect(configValue(changed, ['services', 'api', 'command'])).toEqual([
      'sh',
      '-c',
      'echo $$HOME'
    ])
    changed = editConfig(changed, ['services', 'api', 'command'], REMOVE)
    expect(configValue(changed, ['services', 'api', 'command'])).toBeUndefined()
  })
  it('removes list entries and retains advanced options on neighboring entries', () => {
    const ports =
      'services:\n  app:\n    image: nginx\n    ports:\n      - target: 80\n        published: "8080"\n        app_protocol: http\n      - "8443:443"\n'
    const changed = editConfig(ports, ['services', 'app', 'ports', 1], REMOVE)
    expect(configValue(changed, ['services', 'app', 'ports'])).toEqual([
      { target: 80, published: '8080', app_protocol: 'http' }
    ])
  })
  it('locks only shared structures and retains independent editing', () => {
    const shared =
      'x-base: &base\n  restart: always\nservices:\n  shared:\n    <<: *base\n    image: busybox\n  plain:\n    image: nginx\n'
    expect(readonlyReason(shared, ['services', 'shared', 'image'])).toBe(
      'shared'
    )
    expect(() =>
      editConfig(shared, ['services', 'shared', 'image'], 'other')
    ).toThrow()
    expect(
      editConfig(shared, ['services', 'plain', 'image'], 'nginx:alpine')
    ).toBe(shared.replace('image: nginx', 'image: nginx:alpine'))
  })
  it('renames native service references without losing dependency options', () => {
    const input =
      'services:\n  db:\n    image: postgres\n  api:\n    image: api\n    depends_on:\n      db:\n        condition: service_healthy\n        restart: true\n    network_mode: service:db\n'
    const changed = renameService(input, 'db', 'database')
    expect(
      configValue(changed, ['services', 'api', 'depends_on', 'database'])
    ).toEqual({ condition: 'service_healthy', restart: true })
    expect(configValue(changed, ['services', 'api', 'network_mode'])).toBe(
      'service:database'
    )
    expect(composeProblems(changed)).toEqual([])
  })
  it('keeps shared descendants intact when editing an independent sibling', () => {
    const input =
      'x-value: &value { cache: warm }\nservices:\n  app:\n    image: nginx\n    x-extra: *value\n    environment:\n      PORT: &port "80"\n  copy:\n    image: nginx\n    environment:\n      PORT: *port\n'
    const changed = editConfig(input, ['services', 'app', 'restart'], 'always')
    expect(changed).toContain('x-extra: *value')
    expect(composeProblems(changed)).toEqual([])
    expect(() =>
      editConfig(input, ['services', 'app', 'environment'], { PORT: '90' })
    ).toThrow()
    expect(() => editConfig(input, ['services', 'app'], REMOVE)).toThrow()
    expect(readonlyGroupReason(input, ['services', 'app', 'environment'])).toBe(
      'shared'
    )
    expect(readonlyReason(input, ['services', 'app', 'image'])).toBeUndefined()
  })
  it('preserves long mount and port options and locates resource users', () => {
    const input =
      'services:\n  app:\n    image: nginx\n    volumes:\n      - type: volume\n        source: data\n        target: /data\n        volume: { nocopy: true }\n    networks: { shared: { aliases: [api] } }\n    depends_on: [db]\n    ports:\n      - target: 80\n        published: "8080"\n        app_protocol: http\n  db:\n    image: postgres\nvolumes: { data: {} }\nnetworks: { shared: { external: true } }\n'
    let changed = editConfig(
      input,
      ['services', 'app', 'volumes', 0, 'read_only'],
      true
    )
    changed = editConfig(
      changed,
      ['services', 'app', 'ports', 0, 'published'],
      '8081'
    )
    expect(
      configValue(changed, ['services', 'app', 'volumes', 0, 'volume'])
    ).toEqual({ nocopy: true })
    expect(
      configValue(changed, ['services', 'app', 'ports', 0, 'app_protocol'])
    ).toBe('http')
    expect(references(changed, 'volumes', 'data')).toEqual(['app'])
    expect(references(changed, 'networks', 'shared')).toEqual(['app'])
    expect(references(changed, 'services', 'db')).toEqual(['app'])
    expect(
      configValue(renameService(changed, 'db', 'database'), [
        'services',
        'app',
        'depends_on'
      ])
    ).toEqual(['database'])
  })
  it('updates long syntax while retaining comments and quoted unknown options', () => {
    const input =
      'services:\n  app:\n    image: nginx\n    ports:\n      - target: 80\n        published: "8080" # address\n        app_protocol: "http" # retained option\n'
    const changed = editConfig(input, ['services', 'app', 'ports', 0], {
      target: 80,
      published: '8081',
      app_protocol: 'http'
    })
    expect(changed).toContain('published: "8081" # address')
    expect(changed).toContain('app_protocol: "http" # retained option')
    expect(composeProblems(changed)).toEqual([])
  })
  it('supports null resource declarations and null service network attachments', () => {
    const input =
      'services:\n  app:\n    image: nginx\n    networks:\n      private:\nvolumes:\n  data: # persistent\nnetworks:\n  private:\n'
    let changed = editConfig(input, ['volumes', 'data', 'name'], 'app-data')
    changed = editConfig(
      changed,
      ['services', 'app', 'networks', 'private', 'aliases'],
      ['api']
    )
    expect(configValue(changed, ['volumes', 'data', 'name'])).toBe('app-data')
    expect(changed).toContain('persistent')
    expect(
      configValue(changed, [
        'services',
        'app',
        'networks',
        'private',
        'aliases'
      ])
    ).toEqual(['api'])
    expect(composeProblems(changed)).toEqual([])
  })
  it('masks sensitive aliases at their definitions without masking public values', () => {
    const input =
      'x-value: &credential shared-secret\nservices:\n  app:\n    image: nginx\n    environment:\n      PASSWORD: *credential\n      TOKEN: *credential\n      PUBLIC: visible\n'
    expect(maskCompose(input)).not.toContain('shared-secret')
    expect(maskCompose(input)).toContain('visible')
  })
  it('locates common form errors while allowing build services and interpolated fields', () => {
    const source =
      'services:\n  draft:\n    ports: [{target: 70000}]\n    volumes: [{type: bind, source: /tmp/data, target: relative}]\n    depends_on: [missing]\n  built:\n    build: .\n    ports: [{target: "${PORT}"}]\n'
    expect(configurationIssues(source).map((issue) => issue.code)).toEqual([
      'image',
      'port',
      'mount',
      'dependency'
    ])
    expect(configurationIssues(source)[1].path).toEqual([
      'services',
      'draft',
      'ports',
      0,
      'target'
    ])
  })
  it('does not expose secret values in syntax errors or masked source', () => {
    const invalid = 'services: [ password: very-secret'
    expect(JSON.stringify(composeProblems(invalid))).not.toContain(
      'very-secret'
    )
    expect(maskCompose(invalid)).not.toContain('very-secret')
    const input =
      'services:\n  app:\n    image: app\n    environment:\n      PASSWORD: very-secret\n      PUBLIC: visible\n      API_TOKEN: |\n        line-one\n        line-two\n'
    expect(maskCompose(input)).not.toContain('very-secret')
    expect(maskCompose(input)).not.toContain('line-one')
    expect(maskCompose(input)).toContain('visible')
  })
})

describe('Project .env editing', () => {
  it('retains comments, quotes, unrelated lines and expressions', () => {
    const input =
      '# important\nexport HOST="localhost" # address\nPASSWORD=${SECRET:-default}\nOTHER=untouched\n'
    const changed = setEnv(input, 'HOST', 'example.org')
    expect(changed).toBe(input.replace('"localhost"', '"example.org"'))
    expect(() => setEnv(input, 'PASSWORD', 'value')).toThrow()
    expect(setEnv(input, 'NEW', 'with space')).toContain("NEW='with space'\n")
  })
  it('handles duplicate or multiline assignments conservatively', () => {
    expect(envEntries('A=1\nA=2\n').every((item) => !item.editable)).toBe(true)
    expect(() =>
      setEnv('PASSWORD="first\nsecond"\n', 'PASSWORD', 'new')
    ).toThrow()
    expect(maskEnvironment('PASSWORD="first\nsecond"\n')).not.toContain(
      'second'
    )
  })
  it('finds references with fallbacks and preserves secret masking', () => {
    const source =
      'services:\n  app:\n    image: app:${TAG:-latest}\n    environment:\n      SECRET: ${PASSWORD}\n'
    expect(variableReferences(source, 'TAG')).toEqual(['app'])
    expect(variableReferences(source, 'PASSWORD')).toEqual(['app'])
    expect(maskEnvironment('PASSWORD=secret\nPUBLIC=value\n')).not.toContain(
      'secret'
    )
  })
  it('does not treat assignments inside a multiline value as project variables', () => {
    const input = "MULTI='first\nFAKE=embedded\nlast'\nREAL=1\n"
    expect(envEntries(input).map((entry) => entry.key)).toEqual([
      'MULTI',
      'REAL'
    ])
    expect(setEnv(input, 'REAL', '2')).toBe(input.replace('REAL=1', 'REAL=2'))
    expect(() => setEnv("MULTI='unclosed\n", 'NEW', '1')).toThrow()
  })
  it('preserves quoted hash characters, comments and CRLF line endings', () => {
    const input = 'VALUE="a # b" # comment\r\nOTHER=1\r\n'
    expect(setEnv(input, 'VALUE', 'c # d')).toBe(
      input.replace('a # b', 'c # d')
    )
    const literal = setEnv('', 'VALUE', '${LITERAL} with space')
    expect(literal).toBe("VALUE='${LITERAL} with space'\n")
  })
})
