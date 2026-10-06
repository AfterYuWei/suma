import { afterAll, beforeAll, expect, test } from 'vitest'
import { createServer, request, type IncomingMessage, type Server } from 'node:http'
import type { AddressInfo } from 'node:net'
import { fileURLToPath } from 'node:url'
import { createServer as createViteServer, type ViteDevServer } from 'vite'
import { WebSocket, WebSocketServer } from 'ws'

const browserHost = '192.168.1.100:5173'
const browserOrigin = `http://${browserHost}`
let upstream: Server
let sockets: WebSocketServer
let vite: ViteDevServer
let port: number
let socketHeaders: { host?: string; origin?: string }
const previousTarget = process.env.SUMA_DEV_API

beforeAll(async () => {
  upstream = createServer((req, res) => {
    res.writeHead(req.headers.origin === `http://${req.headers.host}` ? 200 : 403, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify({ host: req.headers.host, origin: req.headers.origin, path: req.url, method: req.method }))
  })
  sockets = new WebSocketServer({ server: upstream, path: '/ws/probe', verifyClient: (info: { origin: string; req: IncomingMessage }) => info.origin === `http://${info.req.headers.host}` })
  sockets.on('connection', (socket, req) => {
    socketHeaders = { host: req.headers.host, origin: req.headers.origin }
    socket.on('message', () => socket.send('pong'))
  })
  await new Promise<void>(resolve => upstream.listen(0, '127.0.0.1', resolve))
  process.env.SUMA_DEV_API = `http://127.0.0.1:${(upstream.address() as AddressInfo).port}`
  vite = await createViteServer({
    configFile: fileURLToPath(new URL('../../vite.config.ts', import.meta.url)),
    server: { host: '127.0.0.1', port: 0, strictPort: true, watch: null },
    optimizeDeps: { noDiscovery: true, include: [] },
    logLevel: 'silent',
  })
  await vite.listen()
  port = (vite.httpServer!.address() as AddressInfo).port
}, 20_000)

afterAll(async () => {
  if (previousTarget === undefined) delete process.env.SUMA_DEV_API
  else process.env.SUMA_DEV_API = previousTarget
  if (vite) await vite.close()
  if (sockets) {
    for (const socket of sockets.clients) socket.terminate()
    await new Promise<void>(resolve => sockets.close(() => resolve()))
  }
  if (upstream) await new Promise<void>(resolve => upstream.close(() => resolve()))
})

function discover(origin: string) {
  return new Promise<{ status: number; body: Record<string, string> }>((resolve, reject) => {
    const req = request({ hostname: '127.0.0.1', port, path: '/api/v1/ai/settings/models', method: 'POST', agent: false, headers: { Host: browserHost, Origin: origin, 'Content-Type': 'application/json' } }, res => {
      let body = ''
      res.setEncoding('utf8')
      res.on('data', chunk => { body += chunk })
      res.on('end', () => resolve({ status: res.statusCode!, body: JSON.parse(body) }))
    })
    req.on('error', reject)
    req.end(JSON.stringify({ allow_insecure: true }))
  })
}

test('development API proxy preserves the LAN browser Host and Origin for model discovery', async () => {
  const accepted = await discover(browserOrigin)
  expect(accepted).toEqual({ status: 200, body: { host: browserHost, origin: browserOrigin, path: '/api/v1/ai/settings/models', method: 'POST' } })
  const rejected = await discover('http://unrelated.example.test')
  expect(rejected.status).toBe(403)
  expect(rejected.body).toMatchObject({ host: browserHost, origin: 'http://unrelated.example.test' })
})

test('development WebSocket proxy preserves the same browser origin', async () => {
  const socket = new WebSocket(`ws://127.0.0.1:${port}/ws/probe`, { headers: { Host: browserHost, Origin: browserOrigin } })
  try {
    await new Promise<void>((resolve, reject) => { socket.once('open', resolve); socket.once('error', reject) })
    const reply = new Promise<string>((resolve, reject) => { socket.once('message', value => resolve(value.toString())); socket.once('error', reject) })
    socket.send('ping')
    expect(await reply).toBe('pong')
    expect(socketHeaders).toEqual({ host: browserHost, origin: browserOrigin })
  } finally {
    socket.terminate()
  }
})
