import WebSocket from 'ws'

const [cookie, containerId, taskId] = process.argv.slice(2)
if (!cookie || !containerId) throw new Error('usage: node smoke-ws.mjs <cookie> <container-id> [task-id]')

function check(name, path, onOpen, accept) {
  return new Promise((resolve, reject) => {
    const socket = new WebSocket(`ws://127.0.0.1:8080${path}`, { headers: { Cookie: cookie } })
    const timeout = setTimeout(() => { socket.terminate(); reject(new Error(`${name} timed out`)) }, 10_000)
    socket.on('open', () => onOpen?.(socket))
    socket.on('message', (value) => { const text = value.toString(); if (accept(text)) { clearTimeout(timeout); socket.close(); resolve(text) } })
    socket.on('error', (error) => { clearTimeout(timeout); reject(error) })
  })
}

await check('logs', `/ws/containers/${containerId}/logs?tail=30`, undefined, (value) => value.includes('dockport-smoke'))
await check('stats', `/ws/containers/${containerId}/stats`, undefined, (value) => value.includes('cpu_stats'))
await check('terminal', `/ws/containers/${containerId}/terminal`, (socket) => {
  socket.send(JSON.stringify({ type: 'resize', cols: 100, rows: 30 }))
  socket.send(JSON.stringify({ type: 'input', data: 'echo TERMINAL_OK\nexit\n' }))
}, (value) => value.includes('TERMINAL_OK'))
if (taskId) await check('task', `/ws/tasks/${taskId}`, undefined, (value) => value.includes('message'))
console.log('logs=ok stats=ok terminal=ok task=' + (taskId ? 'ok' : 'skipped'))
