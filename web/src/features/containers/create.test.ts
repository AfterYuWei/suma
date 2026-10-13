import { describe, expect, it } from 'vitest'
import { containerCreateRequest, emptyContainerDraft, mountsDockerSocket } from './create'

describe('standalone container configuration', () => {
  const draft = () => ({ ...emptyContainerDraft(), name: 'my-app', image: 'alpine:3.24' })
  it('preserves argv boundaries and literal environment values without shell expansion', () => {
    const input = containerCreateRequest({ ...draft(), command: 'sh\n-c\nsleep 3600', environment: [{ key: 'TOKEN', value: '${literal} $value\nsecret' }], memory_mb: '128', cpus: '0.5' }, false)
    expect(input.command).toEqual(['sh', '-c', 'sleep 3600'])
    expect(input.environment[0].value).toBe('${literal} $value\nsecret')
    expect(input.memory_mb).toBe(128)
    expect(input.cpus).toBe(.5)
    expect(input.start).toBe(true)
  })
  it('rejects duplicate bindings/variables, unsafe paths and invalid resource limits', () => {
    for (const value of [
      { ...draft(), ports: [{ host_port: 8080, container_port: 80, host_ip: '127.0.0.1', protocol: 'tcp' as const }, { host_port: 8080, container_port: 81, host_ip: '127.0.0.1', protocol: 'tcp' as const }] },
      { ...draft(), environment: [{ key: 'TOKEN', value: '1' }, { key: 'TOKEN', value: '2' }] },
      { ...draft(), mounts: [{ type: 'bind' as const, source: '/srv/${ROOT}', target: '/data', read_only: true }] },
      { ...draft(), mounts: [{ type: 'bind' as const, source: '/srv/../root', target: '/data', read_only: false }] },
      { ...draft(), network: 'host', ports: [{ host_port: 0, container_port: 80, host_ip: '127.0.0.1', protocol: 'tcp' as const }] },
      { ...draft(), memory_mb: '5' }, { ...draft(), cpus: '-1' },
    ]) expect(() => containerCreateRequest(value, false)).toThrow()
  })
  it('recognizes Docker socket access regardless of read-only setting and target alias', () => {
    expect(mountsDockerSocket([{ type: 'bind', source: '/run/docker.sock', target: '/socket', read_only: true }])).toBe(true)
    expect(mountsDockerSocket([{ type: 'bind', source: '/srv/engine.sock', target: '/var/run/docker.sock', read_only: false }])).toBe(true)
  })
})
