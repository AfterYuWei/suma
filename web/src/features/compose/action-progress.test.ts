import { describe, expect, it } from 'vitest'
import { composeOutputLogs, composeProgress, parseComposeResource, type ComposeTask, type ComposeTaskLog } from './action-progress'

const task = (overrides: Partial<ComposeTask> = {}): ComposeTask => ({ id: 'pull-task', name: 'Update app', type: 'compose.update', status: 'running', progress: 55, message: '', ...overrides })
const logs = (...messages: string[]): ComposeTaskLog[] => messages.map((message, id) => ({ id, message, level: 'info', created_at: '2026-10-24T04:00:00Z' }))

describe('Compose live output', () => {
  it('replaces a persisted counter with the live frame without inventing a timestamp', () => {
    const history = logs('api Pulling', 'abcdef123456 Downloading 1MB/10MB', 'fedcba654321 Waiting')
    const original = structuredClone(history)
    for (const counter of ['4MB', '8MB']) {
      const message = `abcdef123456 Downloading ${counter}/10MB`
      const output = composeOutputLogs(task({ message }), history)
      expect(output).toHaveLength(3)
      expect(output.at(-1)).toEqual({ id: 'live', level: 'info', message })
      expect(output.map(row => row.message)).not.toContain(history[1].message)
    }
    expect(history).toEqual(original)
    expect(composeOutputLogs(task({ message: 'abcdef123456 Extracting 8MB' }), [])).toEqual([
      { id: 'live', level: 'info', message: 'abcdef123456 Extracting 8MB' },
    ])
  })

  it('retains newer historical stages and counters when task polling lags', () => {
    for (const newer of ['abcdef123456 Downloading 9MB/10MB', 'abcdef123456 Download complete', 'abcdef123456 Extracting 1MB', 'abcdef123456 Pull complete']) {
      const history = logs(newer)
      expect(composeOutputLogs(task({ message: 'abcdef123456 Downloading 8MB/10MB' }), history)).toEqual(history)
    }
  })

  it('does not duplicate persisted frames or display terminal tasks as live', () => {
    const history = logs('abcdef123456 Downloading 8MB/10MB')
    expect(composeOutputLogs(task({ message: history[0].message }), history)).toEqual(history)
    for (const status of ['pending', 'success', 'failed', 'canceled']) {
      expect(composeOutputLogs(task({ status, message: 'abcdef123456 Downloading 9MB/10MB' }), history)).toEqual(history)
    }
    expect(composeOutputLogs(undefined, history)).toEqual(history)
    expect(composeOutputLogs(task({ message: 'Starting docker compose update' }), history)).toEqual(history)
  })
})

describe('Compose resource progress', () => {
  it('reads old and current Compose image formats without guessing layer ownership', () => {
    expect(parseComposeResource(' api Pulling ')).toMatchObject({ kind: 'image', name: 'api', stage: 'pulling', state: 'active' })
    expect(parseComposeResource(' Image ghcr.io/team/app:latest Pulled ')).toMatchObject({ kind: 'image', name: 'ghcr.io/team/app:latest', state: 'complete' })
    expect(parseComposeResource('abcdef123456: Already exists')).toMatchObject({ kind: 'layer', name: 'abcdef123456', state: 'complete' })
    expect(parseComposeResource('db Skipped - No image to be pulled')).toMatchObject({ kind: 'image', state: 'complete' })
  })

  it('uses byte ratios only, with decimal and binary units and no ASCII bars', () => {
    expect(parseComposeResource('abcdef123456 Downloading [=====> ] 2.5MB/10MB')).toMatchObject({ percent: 25, detail: '2.5MB / 10MB' })
    expect(parseComposeResource('abcdef123456 Extracting 512 KiB / 1 MiB')).toMatchObject({ percent: 50 })
    expect(parseComposeResource('abcdef123456 Downloading 500B/1kB')).toMatchObject({ percent: 50 })
    expect(parseComposeResource('abcdef123456 Downloading 3MB/1MB')).toMatchObject({ percent: 100 })
    expect(parseComposeResource('abcdef123456 Downloading 20MB')?.percent).toBeUndefined()
    expect(parseComposeResource('abcdef123456 Downloading 0B/0B')?.percent).toBeUndefined()
  })

  it('reads resource identity and lifecycle without equating started with healthy', () => {
    expect(parseComposeResource(' Container app-api-1 Recreating ')).toMatchObject({ kind: 'container', name: 'app-api-1', stage: 'recreating', state: 'active' })
    expect(parseComposeResource('Container app-api-1 Started')).toMatchObject({ stage: 'started', state: 'complete' })
    expect(parseComposeResource('Network app_default Created')).toMatchObject({ kind: 'network', name: 'app_default' })
    expect(parseComposeResource('Volume app_data Created')).toMatchObject({ kind: 'volume', name: 'app_data' })
    expect(parseComposeResource('Container app-db-1 Error dependency failed to start')).toMatchObject({ state: 'failed' })
    expect(parseComposeResource('app Warning pull access denied')).toMatchObject({ state: 'warning' })
    expect(parseComposeResource('unrecognized output remains available in raw output')).toBeUndefined()
  })

  it('keeps stable row order and replaces stale counters with the current task message', () => {
    const result = composeProgress('update', task({ message: 'abcdef123456 Downloading 8MB/10MB' }), logs('api Pulling', 'abcdef123456 Downloading 1MB/10MB', 'fedcba654321 Waiting', 'api Pulled'))
    expect(result.images).toHaveLength(1)
    expect(result.images[0].state).toBe('complete')
    expect(result.layers.map(row => row.name)).toEqual(['abcdef123456', 'fedcba654321'])
    expect(result.layers[0].percent).toBe(80)
  })

  it('never treats the output-based overall percentage as proof that pulling has finished', () => {
    expect(composeProgress('update', task({ progress: 95 }), logs('api Pulled')).phase).toBe('pull')
    const result = composeProgress('update', task({ progress: 60 }), logs('api Pulled', 'Images ready; recreating Compose services', 'Container app-api-1 Starting'))
    expect(result.phase).toBe('recreate')
    expect(result.pullComplete).toBe(true)
    expect(composeProgress('update', task({ message: 'Images ready; recreating Compose services' }), []).phase).toBe('recreate')
  })

  it('does not roll a lifecycle transition back when task polling lags behind logs', () => {
    const result = composeProgress('update', task({ message: 'Container app-api-1 Starting' }), logs('Images ready; recreating Compose services', 'Container app-api-1 Started'))
    expect(result.resources[0].stage).toBe('started')
  })

  it('retains the interrupted phase and per-resource evidence after failure or cancellation', () => {
    for (const status of ['failed', 'canceled']) {
      const result = composeProgress('update', task({ status, progress: 100, message: 'exit status 1' }), logs('api Pulling', 'abcdef123456 Downloading 5MB/10MB'))
      expect(result.phase).toBe('pull')
      expect(result.pullComplete).toBe(false)
      expect(result.layers[0].state).toBe('active')
    }
    expect(composeProgress('update', task({ status: 'success' }), []).pullComplete).toBe(true)
    expect(composeProgress('restart', task(), logs('Container app-api-1 Restarting')).phase).toBe('execute')
  })
})
